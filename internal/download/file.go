package download

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
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
	err = retry.Do(func() error {
		r, doErr := client.Do(req)
		if doErr != nil {
			return doErr
		}
		if r.StatusCode != http.StatusOK {
			_ = r.Body.Close()
			return StatusError(r.StatusCode)
		}
		resp = r
		return nil
	}, retryOpts(ctx)...)
	if err != nil {
		return false, err
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

func retryOpts(ctx context.Context) []retry.Option {
	return []retry.Option{
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	}
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
