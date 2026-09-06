package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"idolhub/internal/download"

	"golang.org/x/time/rate"
)

// igPkRe extracts the logged-in user id from the accounts/edit page HTML.
var igPkRe = regexp.MustCompile(`"pk":"(\d+)"|"id":"(\d{6,})"`)

const igAppID = "936619743392459"

// igLimiter paces Instagram requests to avoid rate limits.
var igLimiter = rate.NewLimiter(rate.Every(2*time.Second), 1)

type igClient struct {
	http    *http.Client
	limiter *rate.Limiter
	csrf    string
	userID  string
}

// igGraphQLDocID is the Relay doc_id of PolarisProfilePostsTabContentQuery_connection.
// Instagram rotates it with frontend deploys; if requests start failing, grab a
// fresh one from any logged-in profile page request in the browser network tab.
const igGraphQLDocID = "39535953862670189"

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

// bootstrap fetches the csrf token and own user id cookies Instagram now
// requires for its web graphql endpoints.
func (c *igClient) bootstrap(ctx context.Context) error {
	home, err := c.fetchPage(ctx, "https://www.instagram.com/")
	if err != nil {
		return fmt.Errorf("fetch instagram home: %w", err)
	}
	_ = home

	edit, err := c.fetchPage(ctx, "https://www.instagram.com/accounts/edit/")
	if err != nil {
		return fmt.Errorf("fetch instagram edit page: %w", err)
	}
	m := igPkRe.FindStringSubmatch(string(edit))
	if m == nil {
		return fmt.Errorf("could not find own user id in edit page")
	}
	if m[1] != "" {
		c.userID = m[1]
	} else {
		c.userID = m[2]
	}
	u := &url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}
	c.http.Jar.SetCookies(u, []*http.Cookie{{
		Name: "ds_user_id", Value: c.userID, Domain: ".instagram.com", Path: "/", Secure: true, HttpOnly: true,
	}})
	return nil
}

func (c *igClient) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, download.StatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "csrftoken" {
			c.csrf = ck.Value
		}
	}
	return body, nil
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

// doGraphQL fetches one page of the profile posts timeline via the web graphql endpoint.
func (c *igClient) doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	vars := map[string]any{
		"username":               username,
		"first":                  count,
		"after":                  after,
		"before":                 nil,
		"last":                   nil,
		"include_multi_captions": true,
		"data": map[string]any{
			"count":                             count,
			"include_reel_media_seen_timestamp": true,
			"include_relationship_info":         true,
			"latest_besties_reel_media":         true,
			"latest_reel_media":                 true,
		},
		"__relay_internal__pv__PolarisMultiCaptionCarouselEnabledrelayprovider":  true,
		"__relay_internal__pv__PolarisShortDramaEnabledrelayprovider":            false,
		"__relay_internal__pv__PolarisReelsRecoDebugOverlayEnabledrelayprovider": false,
	}
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("av", "0")
	form.Set("__d", "www")
	form.Set("__user", "0")
	form.Set("__a", "1")
	form.Set("__ccg", "EXCELLENT")
	form.Set("__comet_req", "7")
	form.Set("__spin_r", "1046911589")
	form.Set("__spin_b", "trunk")
	form.Set("__spin_t", "1788682161")
	form.Set("__crn", "comet.igweb.PolarisProfilePostsTabRoute")
	form.Set("fb_api_caller_class", "RelayModern")
	form.Set("fb_api_req_friendly_name", "PolarisProfilePostsTabContentQuery_connection")
	form.Set("server_timestamps", "true")
	form.Set("variables", string(varsJSON))
	form.Set("doc_id", igGraphQLDocID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.instagram.com/graphql/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRFToken", c.csrf)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("Origin", "https://www.instagram.com")
	req.Header.Set("Referer", "https://www.instagram.com/"+username+"/")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if len(body) > 0 && body[0] == '<' {
			return nil, fmt.Errorf("%w: instagram returned HTML instead of JSON (session rejected or doc_id outdated)", ErrAuthExpired)
		}
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram graphql response is rate limiting", "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
			return nil, fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, resp.StatusCode)
		}
		slog.Warn("Instagram rejected the graphql request", "status", resp.StatusCode, "body", strings.TrimSpace(string(body))[:min(300, len(strings.TrimSpace(string(body))))])
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
