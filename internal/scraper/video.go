package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// probeVideoCodec returns the video codec name using ffprobe.
func probeVideoCodec(ctx context.Context, srcPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1",
		srcPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(string(out))), nil
}

// TranscodeToH264 transcodes a video file to H.264 / AAC MP4 with faststart.
func TranscodeToH264(ctx context.Context, srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat src file: %w", err)
	}
	modTime := info.ModTime()

	tmpPath := srcPath + ".transcoding.tmp.mp4"
	_ = os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", srcPath,
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "20",
		"-c:a", "copy",
		"-movflags", "+faststart",
		tmpPath,
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg transcoding failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	if err := os.Rename(tmpPath, srcPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace original file with transcoded: %w", err)
	}

	_ = os.Chtimes(srcPath, modTime, modTime)

	// Regenerate thumbnail to match new container
	thumbFilename := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath)) + ".jpg"
	thumbPath := filepath.Join(filepath.Dir(srcPath), "thumbnails", thumbFilename)
	_ = GenerateThumbnail(srcPath, thumbPath)

	return nil
}

// MigrateVideoCodecs scans downloads directory in background and transcodes incompatible codecs (HEVC/H.265/ByteVC1) to universal H.264.
func MigrateVideoCodecs() {
	go func() {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			slog.Debug("ffmpeg not found in PATH, skipping video codec migration")
			return
		}
		if _, err := exec.LookPath("ffprobe"); err != nil {
			slog.Debug("ffprobe not found in PATH, skipping video codec migration")
			return
		}

		downloadsDir := "downloads"
		if _, err := os.Stat(downloadsDir); os.IsNotExist(err) {
			return
		}

		var filesToTranscode []string

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
			if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp.mp4") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".mp4" && ext != ".mov" && ext != ".m4v" {
				return nil
			}

			codec, err := probeVideoCodec(context.Background(), path)
			if err != nil {
				return nil
			}

			// Codecs that are problematic on Windows/Firefox without extra codec packs
			if codec == "hevc" || codec == "h265" || codec == "bytevc1" {
				filesToTranscode = append(filesToTranscode, path)
			}
			return nil
		})

		if len(filesToTranscode) == 0 {
			return
		}

		slog.Info("Starting legacy video codec migration to H.264", "count", len(filesToTranscode))

		for i, filePath := range filesToTranscode {
			slog.Info("Transcoding video to H.264 for cross-browser playback",
				"file", filePath,
				"progress", fmt.Sprintf("%d/%d", i+1, len(filesToTranscode)),
			)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			err := TranscodeToH264(ctx, filePath)
			cancel()

			if err != nil {
				slog.Warn("Failed to transcode legacy video", "file", filePath, "error", err)
			} else {
				slog.Info("Successfully transcoded video to H.264", "file", filePath)
			}
		}

		slog.Info("Completed video codec migration to H.264", "total", len(filesToTranscode))
	}()
}
