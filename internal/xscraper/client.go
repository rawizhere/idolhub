package xscraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"idolhub/internal/browser"
)

// RateLimitError is returned on HTTP 429 with the server-provided delay.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
	}
	return "rate limited"
}

// xClient issues requests with a rotated browser TLS fingerprint.
type xClient struct {
	http      tls_client.HttpClient
	ua        string
	authToken string
	csrfToken string
}

func newXClient(authToken, csrfToken string) (*xClient, error) {
	tp := browser.Random()
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.Profile),
	}...)
	if err != nil {
		return nil, err
	}
	slog.Info("Twitter client using rotated TLS profile", "tls_fingerprint", tp.Profile.GetClientHelloStr())
	return &xClient{http: client, ua: tp.UA, authToken: authToken, csrfToken: csrfToken}, nil
}

func (c *xClient) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = fhttp.Header{
		"user-agent":                []string{c.ua},
		"accept":                    []string{"*/*"},
		"authorization":             []string{"Bearer " + bearer},
		"cookie":                    []string{"auth_token=" + c.authToken + "; ct0=" + c.csrfToken},
		"x-csrf-token":              []string{c.csrfToken},
		"x-twitter-active-user":     []string{"yes"},
		"x-twitter-client-language": []string{"en"},
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == fhttp.StatusTooManyRequests {
		return nil, &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != fhttp.StatusOK {
		return nil, fmt.Errorf("response status %s: %s", resp.Status, body)
	}
	return body, nil
}

func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
