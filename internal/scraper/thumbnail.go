package scraper

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"idolhub/internal/download"
)

const thumbVersionMarker = ".thumb_v480p_fit"

// MigrateThumbnails fills thumbsDir: existing thumbs are copied from the media share, missing ones are generated from the source files. With an empty thumbsDir it keeps the legacy in-place behavior under downloads/.
func MigrateThumbnails(thumbsDir string) {
	if thumbsDir == "" {
		return
	}
	go func() {
		slog.Info("Thumbnail migration: copying previews to fast storage", "dir", thumbsDir)
		if err := os.MkdirAll(thumbsDir, 0755); err != nil {
			slog.Error("Cannot create thumbnails dir", "dir", thumbsDir, "error", err)
			return
		}
		marker := filepath.Join(thumbsDir, thumbVersionMarker)
		upgradeAll := false
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			upgradeAll = true
		}

		copied, generated, checked := 0, 0, 0
		_ = filepath.WalkDir("downloads", func(path string, d os.DirEntry, err error) error {
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
			if !isThumbSource(name) {
				return nil
			}
			checked++
			thumbFilename := strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
			legacy := filepath.Join(filepath.Dir(path), "thumbnails", thumbFilename)
			dest := filepath.Join(thumbsDir, filepath.Dir(path), "thumbnails", thumbFilename)
			if !upgradeAll {
				if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
					return nil
				}
			}
			if src, err := os.Open(legacy); err == nil {
				_ = os.MkdirAll(filepath.Dir(dest), 0755)
				dst, err := os.Create(dest)
				if err == nil {
					_, _ = io.Copy(dst, src)
					_ = dst.Close()
					copied++
				}
				_ = src.Close()
				return nil
			}
			_ = os.MkdirAll(filepath.Dir(dest), 0755)
			if err := download.GenerateThumbnail(path, dest); err != nil {
				slog.Warn("Thumbnail generation failed during migration", "file", path, "error", err)
			} else {
				generated++
			}
			return nil
		})

		_ = os.WriteFile(marker, []byte("480p\n"), 0644)
		if copied+generated > 0 {
			slog.Info("Thumbnail migration completed", "copied", copied, "generated", generated, "sources", checked)
		} else {
			slog.Info("Thumbnail check completed: all thumbnails in place", "sources", checked)
		}
	}()
}

func isThumbSource(name string) bool {
	if name == "posts.json" || strings.HasSuffix(name, ".bak") || name == ".DS_Store" {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp.mp4") || strings.Contains(name, ".transcoding.") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".mp4", ".mov", ".m4v":
		return true
	}
	return false
}
