package scraper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	fcookiejar "github.com/bogdanfinn/fhttp/cookiejar"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"idolhub/internal/browser"
	"idolhub/internal/cookies"
	"idolhub/internal/download"

	"golang.org/x/time/rate"
)

const igAppID = "936619743392459"

// igASBD-ID is the anti-abuse header value instagram's web frontend sends on every XHR. Any plausible numeric value works; it must simply be present.
const igASBDID = "129477"

// Built-in graphql doc_id and frontend build revision. The doc_id refreshes itself from the js bundles when instagram starts answering with HTML.
const (
	defaultIGGraphQLDocID = "39535953862670189"
	defaultIGSpinR        = "1046911589"
)

// igSrcRe extracts script bundle urls from an instagram page.
var igSrcRe = regexp.MustCompile(`src="([^"]+\.js)"`)

// igOpDocIDRe extracts the profile-posts doc_id from the Relay operation definition inside a JS bundle. Instagram stopped embedding doc_id in the page HTML; the mapping now lives in a bundle next to the query name.
var igOpDocIDRe = regexp.MustCompile(
	`PolarisProfilePostsTabContentQuery_connection_instagramRelayOperation",\[\],\(function\([^)]*\)\{[a-z]\.exports="([0-9]{10,21})"`)

// refreshDocID harvests the current doc_id from the JS bundles referenced by the profile page.
func (c *igClient) refreshDocID(ctx context.Context, username string) error {
	page, err := c.fetchPage(ctx, "https://www.instagram.com/"+username+"/")
	if err != nil {
		return fmt.Errorf("fetch profile page: %w", err)
	}
	bundles := 0
	for _, m := range igSrcRe.FindAllStringSubmatch(string(page), -1) {
		src := m[1]
		if !strings.HasPrefix(src, "http") {
			src = "https://www.instagram.com" + src
		}
		bundle, err := c.fetchPage(ctx, src)
		if err != nil {
			continue
		}
		bundles++
		if dm := igOpDocIDRe.FindStringSubmatch(string(bundle)); dm != nil {
			c.docID = dm[1]
			slog.Info("Refreshed Instagram doc_id from js bundle", "doc_id", c.docID, "bundles", bundles)
			return nil
		}
	}
	return fmt.Errorf("doc_id not found in %d js bundles", bundles)
}

// igLimiter paces Instagram requests to avoid rate limits.
var igLimiter = rate.NewLimiter(rate.Every(2*time.Second), 1)

// igPace waits for the limiter, then pauses a random extra 0.3-2.7s.
func igPace(ctx context.Context) error {
	if err := igLimiter.Wait(ctx); err != nil {
		return err
	}
	time.Sleep(time.Duration(300+rand.Int63n(2400)) * time.Millisecond)
	return nil
}

// igPkRe extracts the logged-in user id from the accounts/edit page HTML.
var igPkRe = regexp.MustCompile(`"pk":"(\d+)"|"id":"(\d{6,})"`)

type igClient struct {
	client tls_client.HttpClient
	ua     string
	csrf   string
	userID string
	docID  string

	docIDRefreshed bool
	bootMu         sync.Mutex

	// key identifies the credential set (sessionid + exported cookies) this client was built from; the shared client is rebuilt only when it changes.
	key string
}

// igSharedClientMu guards the process-level instagram client. One long-lived client (and cookie jar) is reused across targets and sync windows: a real browser does not grow a fresh device identity every twelve hours.
var (
	igSharedClientMu sync.Mutex
	igSharedClient   *igClient
)

// getIGClient returns the shared instagram client, rebuilding it only when the configured credentials change (e.g. the user pasted a new cookie export).
func getIGClient(sessionID, cookieExport string) *igClient {
	key := fmt.Sprintf("%d|%x", len(sessionID), sha256.Sum256([]byte(cookieExport)))
	igSharedClientMu.Lock()
	defer igSharedClientMu.Unlock()
	if igSharedClient != nil && igSharedClient.key == key {
		return igSharedClient
	}
	c := newIGClient(sessionID, cookieExport)
	c.key = key
	igSharedClient = c
	return c
}

// newIGClient builds a client whose cookie jar starts from a full browser cookie export (Netscape format) when one is configured. Device cookies (mid, ig_did, datr) are what makes the session look like the browser it was created in; a bare sessionid is a strong automation signal.
func newIGClient(sessionID, cookieExport string) *igClient {
	tp := browser.FirefoxProfile
	jar, _ := fcookiejar.New(nil)
	u := &url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}
	if cookieExport != "" {
		parsed, err := cookies.ParseNetscape(cookieExport, "instagram.com")
		if err != nil {
			slog.Warn("Ignoring invalid instagram cookie export, falling back to sessionid only", "error", err)
		} else {
			jar.SetCookies(u, parsed)
			slog.Info("Loaded instagram cookie export into client jar", "cookies", len(parsed))
		}
	}
	// The sessionid from the export wins; the separate sessionid setting is only a fallback for setups that never exported a full jar.
	if sessionID != "" && jarCookie(jar, "sessionid") == "" {
		jar.SetCookies(u, []*fhttp.Cookie{{
			Name:     "sessionid",
			Value:    sessionID,
			Domain:   ".instagram.com",
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
		}})
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.Profile),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	)
	if err != nil {
		slog.Error("Failed to create tls client, check profiles", "error", err)
	}
	slog.Info("Instagram client TLS profile", "tls_fingerprint", tp.Profile.GetClientHelloStr())
	c := &igClient{
		client: client,
		ua:     tp.UA,
		docID:  defaultIGGraphQLDocID,
	}
	// A full export already carries csrftoken and ds_user_id; seeding them here skips the bootstrap round-trips entirely.
	if ck := jarCookie(jar, "csrftoken"); ck != "" {
		c.csrf = ck
	}
	if ck := jarCookie(jar, "ds_user_id"); ck != "" {
		c.userID = ck
	}
	return c
}

// jarCookie reads a single cookie value from the client jar.
func jarCookie(jar fhttp.CookieJar, name string) string {
	if jar == nil {
		return ""
	}
	u, _ := url.Parse("https://www.instagram.com")
	for _, ck := range jar.Cookies(u) {
		if ck.Name == name && ck.Value != "" {
			return ck.Value
		}
	}
	return ""
}

// csrfFromJar refreshes c.csrf from the cookie jar. resp.Cookies() does not see instagram Set-Cookie headers through the tls-client fork, the jar does.
func (c *igClient) csrfFromJar() {
	if v := jarCookie(c.client.GetCookieJar(), "csrftoken"); v != "" {
		c.csrf = v
	}
}

// sessionRedirectErr: a 3xx means the session cookie was rejected.
func sessionRedirectErr(code int) error {
	return fmt.Errorf("unexpected status %d: instagram session cookie (sessionid) is invalid or expired", code)
}

// bootstrap fetches whatever session metadata is still missing: the csrf token from the home page and the own user id from the edit page. Both are skipped when a full cookie export already provided them. Serialized with bootMu so concurrent targets do not double-bootstrap the shared client.
func (c *igClient) bootstrap(ctx context.Context) error {
	c.bootMu.Lock()
	defer c.bootMu.Unlock()

	if c.csrf == "" {
		if _, err := c.fetchPage(ctx, "https://www.instagram.com/"); err != nil {
			return fmt.Errorf("fetch instagram home: %w", err)
		}
	}
	if c.userID == "" {
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
	}
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
	if err := igPace(ctx); err != nil {
		return nil, err
	}
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	// Minimal headers on purpose: the Sec-Fetch navigation trio makes instagram serve a degraded page variant without the doc_id bundles.
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fhttp.StatusOK {
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return nil, sessionRedirectErr(resp.StatusCode)
		}
		return nil, download.StatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	c.csrfFromJar()
	return body, nil
}

func (c *igClient) doGet(ctx context.Context, apiURL, username string) ([]byte, error) {
	if err := igPace(ctx); err != nil {
		return nil, err
	}
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", igASBDID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if c.csrf != "" {
		req.Header.Set("X-CSRFToken", c.csrf)
	}
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Storage-Access", "active")
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
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return nil, sessionRedirectErr(resp.StatusCode)
		}
		return nil, download.StatusError(resp.StatusCode)
	}
}

// doGraphQL fetches one page of the profile posts timeline via the web graphql endpoint.
func (c *igClient) doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error) {
	if err := igPace(ctx); err != nil {
		return nil, err
	}
	form := buildGraphQLForm(username, userID, after, count, c.userID, c.csrf, c.docID)
	form.Set("__spin_t", strconv.FormatInt(time.Now().Unix(), 10))

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, "https://www.instagram.com/graphql/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRFToken", c.csrf)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", igASBDID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Storage-Access", "active")
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
			// Stale doc_id or rejected session: re-read doc_id once.
			if !c.docIDRefreshed {
				c.docIDRefreshed = true
				if rerr := c.refreshDocID(ctx, username); rerr == nil {
					return c.doGraphQL(ctx, username, userID, after, count)
				} else {
					slog.Warn("Instagram doc_id refresh failed", "error", rerr)
				}
			}
			return nil, fmt.Errorf("%w: instagram returned HTML instead of JSON (session rejected or doc_id outdated)", ErrAuthExpired)
		}
		if c.docIDRefreshed {
			// Successful page: re-arm the one-time doc_id refresh so the long-lived shared client can re-harvest a rotated doc_id later.
			c.docIDRefreshed = false
		}
		return body, nil
	case fhttp.StatusUnauthorized, fhttp.StatusForbidden:
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram graphql response is rate limiting", "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
			return nil, fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, resp.StatusCode)
		}
		// A 403 with a logged-in "Page Not Found" page means an unknown doc_id, not a dead session; re-read it once before giving up.
		if len(body) > 0 && body[0] == '<' && !c.docIDRefreshed {
			c.docIDRefreshed = true
			if rerr := c.refreshDocID(ctx, username); rerr == nil {
				return c.doGraphQL(ctx, username, userID, after, count)
			} else {
				slog.Warn("Instagram doc_id refresh failed", "error", rerr)
			}
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
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return nil, sessionRedirectErr(resp.StatusCode)
		}
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

// buildGraphQLForm builds the PolarisProfilePostsTabContentQuery form shared by the tls-client and browser fetch paths.
func buildGraphQLForm(username, userID, after string, count int, ownUserID, csrf, docID string) *url.Values {
	v := url.Values{}
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
		return &v
	}
	user := ownUserID
	if user == "" {
		user = "0"
	}
	v.Set("av", user)
	v.Set("__d", "www")
	v.Set("__user", user)
	v.Set("__a", "1")
	v.Set("__ccg", "EXCELLENT")
	v.Set("__comet_req", "7")
	v.Set("__spin_r", defaultIGSpinR)
	v.Set("__spin_b", "trunk")
	v.Set("__crn", "comet.igweb.PolarisProfilePostsTabRoute")
	v.Set("fb_api_caller_class", "RelayModern")
	v.Set("fb_api_req_friendly_name", "PolarisProfilePostsTabContentQuery_connection")
	v.Set("server_timestamps", "true")
	v.Set("variables", string(varsJSON))
	v.Set("doc_id", docID)
	return &v
}
