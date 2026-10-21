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
	go migrateThumbnails(thumbsDir)
}

func migrateThumbnails(thumbsDir string) {
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

	copied, generated, relocated, checked := 0, 0, 0, 0
	_ = filepath.WalkDir(mediaRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// dot dirs hold quarantined or temporary files, never library media
			if d.Name() == "thumbnails" || strings.HasPrefix(d.Name(), ".") {
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
		dest := thumbDest(thumbsDir, path)
		if dest == "" {
			return nil
		}
		if !upgradeAll {
			if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
				return nil
			}
		}
		// Earlier builds wrote under <thumbs>/downloads/..., where the server never looks; move those into place instead of regenerating.
		if misplaced := filepath.Join(thumbsDir, filepath.Dir(path), "thumbnails", thumbFilename); misplaced != dest && misplaced != legacy {
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				if info, err := os.Stat(misplaced); err == nil && info.Size() > 0 {
					if mkErr := os.MkdirAll(filepath.Dir(dest), 0755); mkErr == nil && os.Rename(misplaced, dest) == nil {
						relocated++
						return nil
					}
				}
			}
		}
		src, oErr := os.Open(legacy)
		if oErr == nil {
			if mkErr := os.MkdirAll(filepath.Dir(dest), 0755); mkErr != nil {
				slog.Error("Migration mkdir failed", "dir", filepath.Dir(dest), "error", mkErr)
			}
			dst, cErr := os.Create(dest)
			if cErr == nil {
				_, cErr = io.Copy(dst, src)
				_ = dst.Close()
			}
			_ = src.Close()
			if cErr == nil {
				copied++
			} else {
				slog.Error("Migration copy failed", "dest", dest, "error", cErr)
			}
			return nil
		}
		if checked <= 3 {
			slog.Warn("Migration legacy open failed, will generate", "legacy", legacy, "error", oErr)
		}
		_ = os.MkdirAll(filepath.Dir(dest), 0755)
		if err := download.GenerateThumbnail(path, dest); err != nil {
			slog.Warn("Thumbnail generation failed during migration", "file", path, "error", err)
		} else {
			generated++
		}
		return nil
	})

	pruneMisplacedThumbs(thumbsDir)

	if wErr := os.WriteFile(marker, []byte("480p\n"), 0644); wErr != nil {
		slog.Error("Cannot write migration marker", "marker", marker, "error", wErr)
	}
	if copied+generated+relocated > 0 {
		slog.Info("Thumbnail migration completed", "copied", copied, "generated", generated, "relocated", relocated, "sources", checked)
	} else {
		slog.Info("Thumbnail check completed: all thumbnails in place", "sources", checked)
	}
}

const mediaRoot = "downloads"

// thumbDest maps a media file under downloads/ to its preview in thumbsDir. The layout mirrors downloads/ without the root itself: downloads/<platform>/<user>/x.mp4 -> <thumbs>/<platform>/<user>/thumbnails/x.jpg, the same path handleMedia serves.
func thumbDest(thumbsDir, mediaPath string) string {
	rel, err := filepath.Rel(mediaRoot, filepath.Dir(mediaPath))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	name := filepath.Base(mediaPath)
	return filepath.Join(thumbsDir, rel, "thumbnails", strings.TrimSuffix(name, filepath.Ext(name))+".jpg")
}

// pruneMisplacedThumbs removes the empty directories left under <thumbs>/downloads after relocation. It only deletes empty dirs, and never touches the real media root even if thumbsDir points at its parent.
func pruneMisplacedThumbs(thumbsDir string) {
	old := filepath.Join(thumbsDir, mediaRoot)
	oldInfo, err := os.Stat(old)
	if err != nil || !oldInfo.IsDir() {
		return
	}
	if rootInfo, err := os.Stat(mediaRoot); err == nil && os.SameFile(oldInfo, rootInfo) {
		return
	}
	var dirs []string
	_ = filepath.WalkDir(old, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	// deepest first; os.Remove fails on non-empty dirs, which keeps any preview that was not moved
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

func isThumbSource(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp.mp4") || strings.Contains(name, ".transcoding.") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return download.IsVideo(name)
}
