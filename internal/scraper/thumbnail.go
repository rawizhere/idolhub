package scraper

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// GenerateThumbnail creates a 320px JPEG thumbnail
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

		// Run ffmpeg to extract a frame at 0.1s
		cmd := exec.Command("ffmpeg", "-y", "-ss", "00:00:00.100", "-i", srcPath, "-frames:v", "1", "-vf", "scale=320:-1", "-q:v", "5", dstPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("ffmpeg failed to extract video thumbnail", "src", srcPath, "error", err, "output", string(out))
			return err
		}
		slog.Debug("Video thumbnail generated successfully", "src", srcPath, "dst", dstPath)
		return nil
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source image: %w", err)
	}
	defer func() { _ = f.Close() }()

	img, format, err := image.Decode(f)
	if err != nil {
		slog.Warn("Failed to decode image, falling back to copying original", "src", srcPath, "error", err)
		_ = f.Close()
		return copyFile(srcPath, dstPath)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Target width is 320px
	targetWidth := 320
	if width < targetWidth {
		targetWidth = width
	}
	targetHeight := int((float64(height) / float64(width)) * float64(targetWidth))

	rect := image.Rect(0, 0, targetWidth, targetHeight)
	dstImg := image.NewRGBA(rect)

	// Downscale using bilinear filter
	draw.BiLinear.Scale(dstImg, rect, img, bounds, draw.Over, nil)

	// Save to dstPath as JPEG (quality 80)
	outF, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination thumbnail: %w", err)
	}
	defer func() { _ = outF.Close() }()

	if err := jpeg.Encode(outF, dstImg, &jpeg.Options{Quality: 80}); err != nil {
		return fmt.Errorf("failed to encode thumbnail as jpeg (%s): %w", format, err)
	}

	slog.Debug("Image thumbnail generated successfully", "src", srcPath, "dst", dstPath, "format", format)
	return nil
}

func init() {
	image.RegisterFormat("png", "png", png.Decode, png.DecodeConfig)
}

// MigrateThumbnails scans downloads and generates missing thumbnails
func MigrateThumbnails() {
	go func() {
		slog.Info("Starting background thumbnail migration for existing media...")
		downloadsDir := "downloads"
		if _, err := os.Stat(downloadsDir); os.IsNotExist(err) {
			return
		}

		platforms, err := os.ReadDir(downloadsDir)
		if err != nil {
			return
		}

		for _, p := range platforms {
			if !p.IsDir() {
				continue
			}
			platformPath := filepath.Join(downloadsDir, p.Name())
			users, err := os.ReadDir(platformPath)
			if err != nil {
				continue
			}

			for _, u := range users {
				if !u.IsDir() {
					continue
				}
				userPath := filepath.Join(platformPath, u.Name())
				files, err := os.ReadDir(userPath)
				if err != nil {
					continue
				}

				for _, f := range files {
					if f.IsDir() {
						continue
					}
					name := f.Name()
					if name == "posts.json" || name == ".DS_Store" {
						continue
					}

					thumbFilename := strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
					thumbPath := filepath.Join(userPath, "thumbnails", thumbFilename)

					info, err := os.Stat(thumbPath)
					if os.IsNotExist(err) || (err == nil && info.Size() == 0) {
						srcPath := filepath.Join(userPath, name)
						slog.Info("Generating missing thumbnail", "file", srcPath)
						if err := GenerateThumbnail(srcPath, thumbPath); err != nil {
							slog.Error("Failed to generate thumbnail during migration", "file", srcPath, "error", err)
						}
					}
				}
			}
		}
		slog.Info("Background thumbnail migration completed.")
	}()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}
