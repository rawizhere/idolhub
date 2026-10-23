package scraper

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MigrateTikTokNames gives the id-only files of earlier builds the <date>_<id> name ytdlpOutput now produces. A TikTok
// id carries its creation time in the upper 32 bits, the same instant yt-dlp reports as the timestamp, so no metadata
// is needed. Safe to run on every start. It returns the accounts whose files changed, for the gallery cache.
func MigrateTikTokNames(thumbsDir string) []string {
	root := filepath.Join(mediaRoot, "tiktok")
	accounts, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var changed []string
	for _, a := range accounts {
		if !a.IsDir() || strings.HasPrefix(a.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, a.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		renamed := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			want := ttDatedName(e.Name())
			if want == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, want)); err == nil {
				continue
			}
			if err := renameMedia(dir, thumbsDir, "tiktok", a.Name(), e.Name(), want); err != nil {
				slog.Warn("Failed to rename TikTok video", "file", e.Name(), "error", err)
				continue
			}
			renamed++
		}
		if renamed > 0 {
			changed = append(changed, a.Name())
			slog.Info("TikTok file name migration", "user", a.Name(), "renamed", renamed)
		}
	}
	return changed
}

// ttDatedName returns the dated name of an id-only file such as 7699367800736845063.mp4, or "" for any other name.
func ttDatedName(name string) string {
	id, ext, _ := strings.Cut(name, ".")
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || len(id) < 15 || ext == "" {
		return ""
	}
	return igDatePrefix(time.Unix(int64(n>>32), 0)) + name
}
