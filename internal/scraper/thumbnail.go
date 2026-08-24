package scraper

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"idolhub/internal/download"
)

const thumbVersionMarker = "downloads/.thumb_v480p"

func MigrateThumbnails() {
	go func() {
		downloadsDir := "downloads"
		if _, err := os.Stat(downloadsDir); os.IsNotExist(err) {
			return
		}

		upgradeAll := false
		if _, err := os.Stat(thumbVersionMarker); os.IsNotExist(err) {
			upgradeAll = true
		}

		slog.Info("Starting thumbnail migration check...")
		totalChecked := 0
		generatedCount := 0

		_ = filepath.WalkDir(downloadsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "thumbnails" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if name == "posts.json" || name == ".DS_Store" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp.mp4") || strings.Contains(name, ".transcoding.") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" && ext != ".mp4" && ext != ".mov" && ext != ".m4v" {
				return nil
			}

			totalChecked++
			thumbFilename := strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
			thumbPath := filepath.Join(filepath.Dir(path), "thumbnails", thumbFilename)
			info, err := os.Stat(thumbPath)
			if upgradeAll || os.IsNotExist(err) || (err == nil && info.Size() == 0) {
				if err := download.GenerateThumbnail(path, thumbPath); err != nil {
					slog.Error("Failed to generate thumbnail during migration", "file", path, "error", err)
				} else {
					generatedCount++
				}
			}
			return nil
		})

		if upgradeAll {
			_ = os.WriteFile(thumbVersionMarker, []byte("480p\n"), 0644)
		}

		if generatedCount > 0 {
			slog.Info("Thumbnail migration completed", "generated", generatedCount, "total_checked", totalChecked)
		} else {
			slog.Info("Thumbnail check completed: all thumbnails up to date", "total_checked", totalChecked)
		}
	}()
}
