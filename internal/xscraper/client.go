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
	ua        string
	cookieHdr string
	csrfToken string
}

// newXClient builds the client. When a Netscape cookie export is provided it carries the full device context (auth_token, ct0, kdt, twid, guest_id); a bare auth_token with a fabricated ct0 is a weak bot signature.
func newXClient(authToken, csrfToken, cookiesRaw string) (*xClient, error) {
	tp := browser.FirefoxProfile
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.Profile),
	}...)
	if err != nil {
		return nil, err
	}
	pairs := make([]string, 0, 8)
	if cookiesRaw != "" {
		parsed, err := cookies.ParseNetscape(cookiesRaw, "x.com")
		if err != nil {
			slog.Warn("Ignoring invalid twitter cookie export, falling back to auth_token only", "error", err)
		} else {
			slog.Info("Loaded twitter cookie export into client", "cookies", len(parsed))
			for _, ck := range parsed {
				pairs = append(pairs, ck.Name+"="+ck.Value)
				if ck.Name == "auth_token" && authToken == "" {
					authToken = ck.Value
				}
				if ck.Name == "ct0" {
					csrfToken = ck.Value
				}
			}
		}
	}
	if authToken == "" {
		return nil, errors.New("no x.com auth_token in cookie export")
	}
	pairs = append(pairs, "auth_token="+authToken)
	if csrfToken != "" {
		pairs = append(pairs, "ct0="+csrfToken)
	}
	slog.Info("Twitter client TLS profile", "tls_fingerprint", tp.Profile.GetClientHelloStr())
	return &xClient{http: client, ua: tp.UA, cookieHdr: strings.Join(pairs, "; "), csrfToken: csrfToken}, nil
}

func (c *xClient) get(ctx context.Context, rawURL, referer string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = fhttp.Header{
		"user-agent":                []string{c.ua},
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
	if len(body) == 0 {
		return nil, fmt.Errorf("x.com returned an empty body with status %d", resp.StatusCode)
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
