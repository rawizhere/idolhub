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

// Result is the outcome of one pool job.
type Result int

const (
	Skipped Result = iota
	Downloaded
	// Failed marks a retryable failure: the sync must not advance its watermark past it.
	Failed
)

// Counts tallies pool job outcomes.
type Counts struct {
	Downloaded, Skipped, Failed int32
}

// Pool runs fn over jobs across numWorkers goroutines and tallies results.
type Pool struct {
	wg sync.WaitGroup
	d  atomic.Int32
	s  atomic.Int32
	f  atomic.Int32
}

// Start launches workers right away; close jobs, then call Wait.
func Start[T any](ctx context.Context, jobs <-chan T, numWorkers int, fn func(ctx context.Context, item T) Result) *Pool {
	p := &Pool{}
	for range numWorkers {
		p.wg.Go(func() {
			for item := range jobs {
				if ctx.Err() != nil {
					// keep draining so producers never block on a full channel
					p.f.Add(1)
					continue
				}
				switch fn(ctx, item) {
				case Downloaded:
					p.d.Add(1)
				case Failed:
					p.f.Add(1)
				default:
					p.s.Add(1)
				}
			}
		})
	}
	return p
}

// Wait blocks until all workers finish and returns the tallies.
func (p *Pool) Wait() Counts {
	p.wg.Wait()
	return Counts{Downloaded: p.d.Load(), Skipped: p.s.Load(), Failed: p.f.Load()}
}

// IsVideo reports whether a file name has a video extension.
func IsVideo(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mov", ".m4v", ".webm":
		return true
	}
	return false
}

func GenerateThumbnail(srcPath, dstPath string) error {
	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create thumbnail directory: %w", err)
	}

	if IsVideo(srcPath) {
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

	thumb := imaging.Fit(srcImg, 480, 480, imaging.Lanczos)
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
		if IsVideo(name) {
			thumbPath := filepath.Join(thumbDir, strings.TrimSuffix(name, filepath.Ext(name))+".jpg")
			if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
				_ = GenerateThumbnail(filepath.Join(outputDir, name), thumbPath)
			}
		}
	}
}
