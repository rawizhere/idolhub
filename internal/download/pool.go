package download

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/errgroup"
)

// Pool runs fn over jobs across numWorkers goroutines and tallies results.
type Pool[T any] struct {
	g *errgroup.Group
	d atomic.Int32
	s atomic.Int32
}

// Start launches workers consuming jobs immediately, so the caller can keep
// producing into jobs concurrently. Call Wait after closing jobs.
func Start[T any](ctx context.Context, jobs <-chan T, numWorkers int, fn func(ctx context.Context, item T) bool) *Pool[T] {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(numWorkers)
	p := &Pool[T]{g: g}
	for range numWorkers {
		g.Go(func() error {
			for item := range jobs {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				if fn(ctx, item) {
					p.d.Add(1)
				} else {
					p.s.Add(1)
				}
			}
			return nil
		})
	}
	return p
}

// Wait blocks until all workers finish and returns downloaded/skipped counts.
func (p *Pool[T]) Wait() (int32, int32) {
	_ = p.g.Wait()
	return p.d.Load(), p.s.Load()
}

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

func GenerateVideoThumbnails(outputDir string) {
	thumbDir := filepath.Join(outputDir, "thumbnails")
	if err := os.MkdirAll(thumbDir, 0755); err != nil {
		return
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}
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
