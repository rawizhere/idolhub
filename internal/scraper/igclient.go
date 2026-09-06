package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"idolhub/internal/download"

	"golang.org/x/time/rate"
)

const igAppID = "936619743392459"

// igLimiter paces Instagram requests to avoid rate limits.
var igLimiter = rate.NewLimiter(rate.Every(2*time.Second), 1)

type igClient struct {
	http    *http.Client
	limiter *rate.Limiter
}

func newIGClient(sessionID string) *igClient {
	jar, _ := cookiejar.New(nil)
	u := &url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}
	jar.SetCookies(u, []*http.Cookie{{
		Name:     "sessionid",
		Value:    sessionID,
		Domain:   ".instagram.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}})
	return &igClient{
		http:    &http.Client{Timeout: 30 * time.Second, Jar: jar},
		limiter: igLimiter,
	}
}

func (c *igClient) doGet(ctx context.Context, apiURL, username string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", "https://www.instagram.com/"+username+"/")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusUnauthorized, http.StatusForbidden:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		// Rate limiting can masquerade as 401/403 with a "please wait" body.
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram auth-style response is actually rate limiting", "url", apiURL, "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
			return nil, fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, resp.StatusCode)
		}
		slog.Warn("Instagram rejected the session", "url", apiURL, "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
		return nil, fmt.Errorf("%w: instagram returned %d", ErrAuthExpired, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: instagram returned 429", download.ErrRateLimited)
	default:
		return nil, download.StatusError(resp.StatusCode)
	}
}

// isRateLimitBody reports whether an Instagram error body means rate limiting, not a bad session.
func isRateLimitBody(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"please wait", "rate limit", "too many requests", "try again later", "throttled"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
