package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: instagram returned %d", ErrAuthExpired, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: instagram returned 429", download.ErrRateLimited)
	case http.StatusOK:
	default:
		return nil, download.StatusError(resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
