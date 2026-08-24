package scraper

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

func GenerateThumbnail(srcPath, dstPath string) error {
	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create thumbnail directory: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(srcPath))

	if ext == ".mp4" || ext == ".mov" || ext == ".m4v" {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			slog.Warn("ffmpeg not found in PATH, skipping video thumbnail generation", "src", srcPath)
			return nil
		}

		cmd := exec.Command("ffmpeg", "-y", "-ss", "00:00:00.100", "-i", srcPath, "-frames:v", "1", "-vf", "scale=480:-2", "-q:v", "3", "-update", "1", dstPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("ffmpeg failed to extract video thumbnail", "src", srcPath, "error", err, "output", string(out))
			return err
		}
		slog.Debug("Video thumbnail generated successfully", "src", srcPath, "dst", dstPath)
		return nil
	}

	srcImg, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		slog.Warn("Failed to decode image, falling back to copying original", "src", srcPath, "error", err)
		in, oerr := os.Open(srcPath)
		if oerr != nil {
			return oerr
		}
		defer func() { _ = in.Close() }()
		out, oerr := os.Create(dstPath)
		if oerr != nil {
			return oerr
		}
		defer func() { _ = out.Close() }()
		_, oerr = io.Copy(out, in)
		return oerr
	}

	thumb := imaging.Thumbnail(srcImg, 480, 480, imaging.Lanczos)
	if err := imaging.Save(thumb, dstPath, imaging.JPEGQuality(80)); err != nil {
		return fmt.Errorf("failed to encode thumbnail as jpeg: %w", err)
	}

	slog.Debug("Image thumbnail generated successfully", "src", srcPath, "dst", dstPath)
	return nil
}

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
				if err := GenerateThumbnail(path, thumbPath); err != nil {
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
