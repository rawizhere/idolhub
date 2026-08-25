package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"idolhub/internal/config"
	"idolhub/internal/store"
)

const importDBPath = "configs/idolhub.db"

type importSetting struct {
	key   string
	value any
}

func runImportJSON(args []string) error {
	fs := flag.NewFlagSet("import-json", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "report the import without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	data, err := os.ReadFile("configs/config.json")
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	slog.Info("Config parsed", "accounts_found", len(cfg.Accounts))

	settings := []importSetting{
		{"twitter_auth_token", cfg.TwitterAuthToken},
		{"instagram_session_id", cfg.InstagramSessionID},
		{"tiktok_cookies", cfg.TikTokCookies},
		{"auto_sync_interval", cfg.AutoSyncInterval},
	}

	if *dryRun {
		slog.Info("Dry run complete", "accounts_found", len(cfg.Accounts), "settings_keys", settingKeys(settings))
		return nil
	}

	st, err := store.Open(importDBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Warn("failed to close store", "error", err)
		}
	}()

	ctx := context.Background()
	imported := 0
	for _, acc := range cfg.Accounts {
		err := st.Accounts.Upsert(ctx, store.Account{
			Platform:       acc.Platform,
			Username:       acc.Username,
			SaveText:       acc.SaveText,
			SkipRetweets:   acc.SkipRetweets,
			DownloadPhotos: acc.DownloadPhotos,
			DownloadVideos: acc.DownloadVideos,
			Filters:        acc.Filters,
		})
		if err != nil {
			return err
		}
		imported++
	}

	written := make([]string, 0, len(settings))
	for _, s := range settings {
		b, err := json.Marshal(s.value)
		if err != nil {
			return fmt.Errorf("encode setting %s: %w", s.key, err)
		}
		if err := st.Settings.Set(ctx, s.key, string(b)); err != nil {
			return err
		}
		written = append(written, s.key)
	}

	slog.Info("Import complete",
		"accounts_found", len(cfg.Accounts),
		"accounts_imported", imported,
		"settings_keys_written", written)
	return nil
}

func settingKeys(settings []importSetting) []string {
	keys := make([]string, 0, len(settings))
	for _, s := range settings {
		keys = append(keys, s.key)
	}
	return keys
}
