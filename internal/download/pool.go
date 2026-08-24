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
	"sync"
	"sync/atomic"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// Pool runs fn over jobs across numWorkers goroutines and tallies results.
type Pool[T any] struct {
	wg sync.WaitGroup
	d  int32
	s  int32
}

// Start launches workers consuming jobs immediately, so the caller can keep
// producing into jobs concurrently. Call Wait after closing jobs.
func Start[T any](ctx context.Context, jobs <-chan T, numWorkers int, fn func(ctx context.Context, item T) bool) *Pool[T] {
	p := &Pool[T]{}
	for i := 0; i < numWorkers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if fn(ctx, item) {
					atomic.AddInt32(&p.d, 1)
				} else {
					atomic.AddInt32(&p.s, 1)
				}
			}
		}()
	}
	return p
}

// Wait blocks until all workers finish and returns downloaded/skipped counts.
func (p *Pool[T]) Wait() (downloaded, skipped int32) {
	p.wg.Wait()
	return p.d, p.s
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
