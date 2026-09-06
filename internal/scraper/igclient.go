package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
	"time"

	fcookiejar "github.com/bogdanfinn/fhttp/cookiejar"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"idolhub/internal/download"

	"golang.org/x/time/rate"
)

const igAppID = "936619743392459"

// igGraphQLDocID is the Relay doc_id of PolarisProfilePostsTabContentQuery_connection.
// Instagram rotates it with frontend deploys; if requests start failing, grab a
// fresh one from any logged-in profile page request in the browser network tab.
const igGraphQLDocID = "39535953862670189"

// igLimiter paces Instagram requests to avoid rate limits.
var igLimiter = rate.NewLimiter(rate.Every(2*time.Second), 1)

// igPkRe extracts the logged-in user id from the accounts/edit page HTML.
var igPkRe = regexp.MustCompile(`"pk":"(\d+)"|"id":"(\d{6,})"`)

// igTLSProfile pairs a tls fingerprint with a matching User-Agent so the
// client looks like one coherent browser build.
type igTLSProfile struct {
	profile profiles.ClientProfile
	ua      string
}

var igTLSProfiles = []igTLSProfile{
	{profiles.Chrome_131, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	{profiles.Chrome_133, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
	{profiles.Chrome_146, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"},
	{profiles.Chrome_152, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"},
	{profiles.Firefox_135, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:135.0) Gecko/20100101 Firefox/135.0"},
	{profiles.Firefox_148, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0"},
}

type igClient struct {
	client  tls_client.HttpClient
	limiter *rate.Limiter
	ua      string
	csrf    string
	userID  string
}

func newIGClient(sessionID string) *igClient {
	tp := igTLSProfiles[rand.Intn(len(igTLSProfiles))]
	jar, _ := fcookiejar.New(nil)
	u := &url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}
	jar.SetCookies(u, []*fhttp.Cookie{{
		Name:     "sessionid",
		Value:    sessionID,
		Domain:   ".instagram.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}})
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.profile),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	)
	if err != nil {
		slog.Error("Failed to create tls client, check profiles", "error", err)
	}
	slog.Info("Instagram client using rotated TLS profile", "tls_fingerprint", tp.profile.GetClientHelloStr())
	return &igClient{
		client:  client,
		limiter: igLimiter,
		ua:      tp.ua,
	}
}

// bootstrap fetches the csrf token and own user id cookies Instagram now
// requires for its web graphql endpoints.
func (c *igClient) bootstrap(ctx context.Context) error {
	if _, err := c.fetchPage(ctx, "https://www.instagram.com/"); err != nil {
		return fmt.Errorf("fetch instagram home: %w", err)
	}

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
	c.setCookie("ds_user_id", c.userID)
	return nil
}

func (c *igClient) setCookie(name, value string) {
	jar := c.client.GetCookieJar()
	if jar == nil {
		return
	}
	u := &url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}
	jar.SetCookies(u, []*fhttp.Cookie{{
		Name: name, Value: value, Domain: ".instagram.com", Path: "/", Secure: true, HttpOnly: true,
	}})
}

func (c *igClient) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fhttp.StatusOK {
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
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", "https://www.instagram.com/"+username+"/")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case fhttp.StatusOK:
		return body, nil
	case fhttp.StatusUnauthorized, fhttp.StatusForbidden:
		// Rate limiting can masquerade as 401/403 with a "please wait" body.
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram auth-style response is actually rate limiting", "url", apiURL, "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
			return nil, fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, resp.StatusCode)
		}
		slog.Warn("Instagram rejected the session", "url", apiURL, "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
		return nil, fmt.Errorf("%w: instagram returned %d", ErrAuthExpired, resp.StatusCode)
	case fhttp.StatusTooManyRequests:
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

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, "https://www.instagram.com/graphql/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRFToken", c.csrf)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("Origin", "https://www.instagram.com")
	req.Header.Set("Referer", "https://www.instagram.com/"+username+"/")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case fhttp.StatusOK:
		if len(body) > 0 && body[0] == '<' {
			return nil, fmt.Errorf("%w: instagram returned HTML instead of JSON (session rejected or doc_id outdated)", ErrAuthExpired)
		}
		return body, nil
	case fhttp.StatusUnauthorized, fhttp.StatusForbidden:
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram graphql response is rate limiting", "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
			return nil, fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, resp.StatusCode)
		}
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		slog.Warn("Instagram rejected the graphql request", "status", resp.StatusCode, "body", snippet)
		return nil, fmt.Errorf("%w: instagram returned %d", ErrAuthExpired, resp.StatusCode)
	case fhttp.StatusTooManyRequests:
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
