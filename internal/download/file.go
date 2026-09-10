package download

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File downloads url to dstPath unless the file already exists.
func File(ctx context.Context, client *http.Client, rawURL, dstPath string, o FileOpts) (bool, error) {
	if _, err := os.Stat(dstPath); err == nil {
		return false, nil
	}
	if o.Jitter > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(rand.Int64N(int64(o.Jitter)))):
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, err
	}
	req.Header = o.Header

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3 && resp == nil; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
			}
		}
		r, doErr := client.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		if r.StatusCode != http.StatusOK {
			_ = r.Body.Close()
			lastErr = StatusError(r.StatusCode)
			if !isRetryable(lastErr) {
				return false, lastErr
			}
			continue
		}
		resp = r
	}
	if resp == nil {
		return false, lastErr
	}
	defer func() { _ = resp.Body.Close() }()

	out, err := os.Create(dstPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return false, err
	}
	return true, nil
}

type FileOpts struct {
	Header http.Header
	Jitter time.Duration
}

// isRetryable allows retries only for network errors and 5xx responses.
func isRetryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= 500
	}
	return true
}

// ThumbnailAsync regenerates the thumbnail of path in background.
func ThumbnailAsync(srcPath string) {
	go func() {
		dir := filepath.Dir(srcPath)
		name := filepath.Base(srcPath)
		thumb := filepath.Join(dir, "thumbnails", strings.TrimSuffix(name, filepath.Ext(name))+".jpg")
		_ = GenerateThumbnail(srcPath, thumb)
	}()
}
