package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"idolhub/internal/config"
	"idolhub/internal/store"
)

const exportDBPath = "configs/idolhub.db"

type legacyPost struct {
	TweetID     string   `json:"tweet_id"`
	Date        string   `json:"date"`
	Text        string   `json:"text"`
	MediaURLs   []string `json:"media_urls"`
	YoutubeURLs []string `json:"youtube_urls,omitempty"`
}

func runExportJSON(args []string) error {
	fs := flag.NewFlagSet("export-json", flag.ExitOnError)
	out := fs.String("out", ".", "target directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	st, err := store.Open(exportDBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Warn("failed to close store", "error", err)
		}
	}()

	if err := config.LoadConfig(st); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	cfgPath := filepath.Join(*out, "configs", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		return err
	}

	ctx := context.Background()
	for _, acc := range cfg.Accounts {
		posts, err := st.Posts.ListByAccount(ctx, acc.Platform, acc.Username, postExportLimit)
		if err != nil {
			return err
		}
		dir := filepath.Join(*out, "downloads", acc.Platform, acc.Username)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		entries := make([]legacyPost, 0, len(posts))
		for _, p := range posts {
			entry := legacyPost{TweetID: p.ExternalID, Text: p.Text}
			if !p.PostedAt.IsZero() {
				entry.Date = p.PostedAt.Format("2006-01-02_15-04-05")
			}
			for _, m := range p.Media {
				if m.Kind == "youtube" {
					entry.YoutubeURLs = append(entry.YoutubeURLs, m.URL)
				} else {
					entry.MediaURLs = append(entry.MediaURLs, m.URL)
				}
			}
			entries = append(entries, entry)
		}
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "posts.json"), data, 0o644); err != nil {
			return err
		}
		slog.Info("Exported posts", "platform", acc.Platform, "user", acc.Username, "count", len(entries))
	}

	slog.Info("Export complete", "accounts", len(cfg.Accounts), "config", cfgPath)
	return nil
}

const postExportLimit = 10000
