package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idolhub/internal/config"

	ytdlp "github.com/lrstanley/go-ytdlp"
)

var (
	ytResolvedReady bool
	ytExecutable    string
)

// EnsureYTDLP resolves the yt-dlp binary, falling back to go-ytdlp install if missing from PATH.
func EnsureYTDLP(ctx context.Context) {
	if path, err := exec.LookPath("yt-dlp"); err == nil {
		ytExecutable = path
		ytResolvedReady = true
		slog.Info("yt-dlp binary resolved from PATH", "path", ytExecutable)
		return
	}
	slog.Info("yt-dlp not found in PATH, attempting auto-install...")
	resolved, err := ytdlp.Install(ctx, &ytdlp.InstallOptions{AllowVersionMismatch: true})
	if err != nil {
		slog.Warn("yt-dlp not found and auto-install failed; video sources will fail until yt-dlp is installed", "error", err)
		return
	}
	ytExecutable = resolved.Executable
	ytResolvedReady = true
	slog.Info("yt-dlp binary auto-installed successfully", "path", ytExecutable)
}

// StartYTDLPUpdateLoop self-updates yt-dlp at startup and then once per day via `--update-to nightly`.
func StartYTDLPUpdateLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		updateYTDLP(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updateYTDLP(ctx)
			}
		}
	}()
}

func updateYTDLP(parent context.Context) {
	if !ytResolvedReady || ytExecutable == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ytExecutable, "--update-to", "nightly")
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("yt-dlp self-update failed", "error", err, "output", strings.TrimSpace(string(out)))
		return
	}
	slog.Info("yt-dlp self-update done", "output", strings.TrimSpace(string(out)))
}

type ytdlpPlatform struct {
	url    string // base URL template
	cookie string
}

func ytdlpPlatformConfig(platform string) ytdlpPlatform {
	c := config.GetConfig()
	switch platform {
	case "tiktok":
		return ytdlpPlatform{
			url:    "https://www.tiktok.com/@%s",
			cookie: c.TikTokCookies,
		}
	default:
		return ytdlpPlatform{}
	}
}

// ScrapeYTDLP downloads videos from TikTok via yt-dlp. Optional Netscape cookies from config are written to a temp file and cleaned up after.
func ScrapeYTDLP(ctx context.Context, t Target, opts Options) error {
	platform := t.Platform
	username := t.Username
	saveText := t.SaveText
	lastSync := opts.LastSync
	pc := ytdlpPlatformConfig(platform)
	if pc.url == "" {
		return fmt.Errorf("unsupported yt-dlp platform: %s", platform)
	}
	if !ytResolvedReady {
		return fmt.Errorf("yt-dlp is not available; install yt-dlp or restart the server")
	}

	report := func(pct int, msg string) {
		if opts.OnProgress != nil {
			opts.OnProgress(pct, msg)
		}
	}
	slog.Info("yt-dlp scrape starting", "platform", platform, "user", username, "last_sync", lastSync)

	outputDir := filepath.Join("downloads", platform, username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	targetURL := fmt.Sprintf(pc.url, username)

	cmd := ytdlp.New().
		Format("bestvideo[vcodec^=avc]+bestaudio[ext=m4a]/bestvideo[vcodec^=avc]+bestaudio/best[vcodec^=avc]/bestvideo+bestaudio/best").
		IgnoreErrors().
		PrintJSON().
		SocketTimeout(15).
		ExtractorRetries("1").
		Paths(outputDir).
		Output("%(id)s.%(ext)s").
		ProgressFunc(3*time.Second, func(u ytdlp.ProgressUpdate) {
			slog.Info("Downloading video stream", "platform", platform, "user", username, "filename", filepath.Base(u.Filename), "percent", u.PercentString(), "status", u.Status)
		})

	if !lastSync.IsZero() {
		cmd = cmd.DateAfter(lastSync.Format("20060102")).BreakOnExisting().PlaylistItems(":50")
	}

	if pc.cookie != "" {
		cookiePath, err := writeCookieFile(pc.cookie)
		if err != nil {
			slog.Warn("failed to write yt-dlp cookie file, continuing without cookies", "platform", platform, "error", err)
		} else {
			defer func() { _ = os.Remove(cookiePath) }()
			cmd = cmd.Cookies(cookiePath)
			slog.Info("yt-dlp using cookies", "platform", platform, "user", username)
			report(20, "using cookies")
		}
	}

	result, err := cmd.Run(ctx, targetURL)
	if err != nil {
		if result != nil && result.ExitCode == 101 {
			slog.Info("yt-dlp reached previously downloaded or older videos, stopped early", "platform", platform, "user", username)
		} else {
			if result != nil {
				slog.Error("yt-dlp run failed", "platform", platform, "user", username, "code", result.ExitCode, "stderr", result.Stderr, "error", err)
			} else {
				slog.Error("yt-dlp run failed", "platform", platform, "user", username, "error", err)
			}
			return fmt.Errorf("yt-dlp run failed: %w", err)
		}
	}

	if saveText {
		infos, perr := result.GetExtractedInfo()
		if perr != nil {
			slog.Warn("failed to parse yt-dlp JSON output", "platform", platform, "user", username, "error", perr)
		} else if posts := collectYTDLPPosts(infos); len(posts) > 0 {
			mergePostsJSON(filepath.Join(outputDir, "posts.json"), posts)
			slog.Info("Saved video posts metadata", "user", username, "count", len(posts))
		}
	}

	thumbDir := filepath.Join(outputDir, "thumbnails")
	if err := os.MkdirAll(thumbDir, 0755); err == nil {
		if entries, err := os.ReadDir(outputDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".mp4" || ext == ".webm" || ext == ".mov" {
					thumbName := strings.TrimSuffix(name, ext) + ".jpg"
					thumbPath := filepath.Join(thumbDir, thumbName)
					if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
						_ = GenerateThumbnail(filepath.Join(outputDir, name), thumbPath)
					}
				}
			}
		}
	}

	// Ensure video codecs are browser-compatible (H.264)
	EnsureDirectoryVideoCodecs(ctx, outputDir)

	slog.Info("yt-dlp video scraping completed successfully", "platform", platform, "user", username)
	report(90, "downloads done")
	return nil
}

// collectYTDLPPosts flattens yt-dlp playlist entries into posts.json format for the gallery.
func collectYTDLPPosts(infos []*ytdlp.ExtractedInfo) []map[string]interface{} {
	var posts []map[string]interface{}
	for _, info := range infos {
		videos := info.Entries
		if videos == nil {
			videos = []*ytdlp.ExtractedInfo{info}
		}
		for _, v := range videos {
			if v == nil || v.ID == "" {
				continue
			}
			date := ""
			if v.UploadDate != nil && len(*v.UploadDate) == 8 {
				ud := *v.UploadDate
				date = ud[:4] + "-" + ud[4:6] + "-" + ud[6:]
			}
			title := ""
			if v.Title != nil {
				title = *v.Title
			}
			description := ""
			if v.Description != nil {
				description = *v.Description
			}
			entry := map[string]interface{}{
				"tweet_id":   v.ID,
				"date":       date,
				"media_urls": []string{v.ID},
			}
			if title != "" && description != "" && title != description {
				entry["text"] = title + "\n\n" + description
			} else if title != "" {
				entry["text"] = title
			} else {
				entry["text"] = description
			}
			if len(v.Tags) > 0 {
				entry["tags"] = v.Tags
			}
			posts = append(posts, entry)
		}
	}
	return posts
}

// writeCookieFile writes raw Netscape cookies (Cookie-Editor export format) to a temp file for yt-dlp.
// mergePostsJSON reads existing posts.json, appends unseen tweet_ids, writes back.
func mergePostsJSON(path string, newPosts []map[string]interface{}) {
	var existing []map[string]interface{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	seen := make(map[string]bool, len(existing))
	for _, p := range existing {
		if id, ok := p["tweet_id"].(string); ok {
			seen[id] = true
		}
	}
	for _, p := range newPosts {
		if id, ok := p["tweet_id"].(string); ok && !seen[id] {
			existing = append(existing, p)
			seen[id] = true
		}
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal merged posts.json", "path", path, "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		slog.Warn("failed to write merged posts.json", "path", path, "error", err)
	}
}

func writeCookieFile(raw string) (string, error) {
	f, err := os.CreateTemp("", "ytdlp-cookies-*.txt")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(raw); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
