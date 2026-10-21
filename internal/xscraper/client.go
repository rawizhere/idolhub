package xscraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"idolhub/internal/browser"
	"idolhub/internal/cookies"
)

// ErrAuthExpired means X rejected the session (dead auth_token, logged out, bad csrf); a fresh cookie export is needed.
var ErrAuthExpired = errors.New("x.com session rejected")

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

// xClient issues requests with a stable browser TLS fingerprint.
type xClient struct {
	http      tls_client.HttpClient
	cookieHdr string
	csrfToken string
}

// newXClient builds the client from a Netscape cookie export: it carries the full device context (auth_token, kdt,
// twid, guest_id). ct0 is replaced by csrfToken, which the client also sends as the csrf header.
func newXClient(csrfToken, cookiesRaw string) (*xClient, error) {
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(browser.TLS),
	)
	if err != nil {
		return nil, err
	}
	parsed, err := cookies.ParseNetscape(cookiesRaw, "x.com")
	if err != nil {
		return nil, fmt.Errorf("twitter cookie export: %w", err)
	}
	slog.Info("Loaded twitter cookie export into client", "cookies", len(parsed))
	// keep the cookie header in the export's jar order; a hand-sorted header is a subtle automation tell
	var pairs []string
	sawAuth, sawCt0 := false, false
	for _, ck := range parsed {
		switch ck.Name {
		case "auth_token":
			sawAuth = ck.Value != ""
		case "ct0":
			sawCt0 = true
			pairs = append(pairs, "ct0="+csrfToken)
			continue
		}
		pairs = append(pairs, ck.Name+"="+ck.Value)
	}
	if !sawAuth {
		return nil, errors.New("no x.com auth_token in cookie export")
	}
	if !sawCt0 {
		pairs = append(pairs, "ct0="+csrfToken)
	}
	slog.Info("Twitter client TLS profile", "tls_fingerprint", browser.TLS.GetClientHelloStr())
	return &xClient{http: client, cookieHdr: strings.Join(pairs, "; "), csrfToken: csrfToken}, nil
}

func (c *xClient) get(ctx context.Context, rawURL, referer string) ([]byte, error) {
	return c.do(ctx, rawURL, fhttp.Header{
		"user-agent":                []string{browser.UA},
		"accept":                    []string{"*/*"},
		"accept-language":           []string{"en-US,en;q=0.9"},
		"authorization":             []string{"Bearer " + bearer},
		"cookie":                    []string{c.cookieHdr},
		"sec-fetch-dest":            []string{"empty"},
		"sec-fetch-mode":            []string{"cors"},
		"sec-fetch-site":            []string{"same-origin"},
		"x-csrf-token":              []string{c.csrfToken},
		"x-twitter-active-user":     []string{"yes"},
		"x-twitter-client-language": []string{"en"},
		"x-twitter-auth-type":       []string{"OAuthWebSession"},
		"referer":                   []string{referer},
	})
}

// page fetches an HTML document with browser-like navigation headers; the api
// headers (bearer, OAuthWebSession) on an html route answer 401.
func (c *xClient) page(ctx context.Context, rawURL string) ([]byte, error) {
	return c.do(ctx, rawURL, fhttp.Header{
		"user-agent":      []string{browser.UA},
		"accept":          []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"accept-language": []string{"en-US,en;q=0.9"},
		"cookie":          []string{c.cookieHdr},
		"sec-fetch-dest":  []string{"document"},
		"sec-fetch-mode":  []string{"navigate"},
		"sec-fetch-site":  []string{"none"},
	})
}

func (c *xClient) do(ctx context.Context, rawURL string, header fhttp.Header) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch {
	case len(body) == 0:
		return nil, fmt.Errorf("x.com returned an empty body with status %d", resp.StatusCode)
	case resp.StatusCode == fhttp.StatusTooManyRequests:
		return nil, &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode == fhttp.StatusUnauthorized || resp.StatusCode == fhttp.StatusForbidden:
		return nil, fmt.Errorf("%w: response status %s", ErrAuthExpired, resp.Status)
	case resp.StatusCode != fhttp.StatusOK:
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
