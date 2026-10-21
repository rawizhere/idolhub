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
	defaultIGGraphQLDocID = "29658130283778926"
	defaultIGSpinR        = "1046911589"
)

// igSrcRe extracts script bundle urls from an instagram page.
var igSrcRe = regexp.MustCompile(`src="([^"]+\.js)"`)

// igOpDocIDRe extracts the profile-posts doc_id from the Relay operation definition inside a JS bundle. Instagram stopped embedding doc_id in the page HTML; the mapping now lives in a bundle next to the query name.
var igOpDocIDRe = regexp.MustCompile(
	`PolarisProfilePostsTabContentQuery_connection_instagramRelayOperation",\[\],\(function\([^)]*\)\{[a-z]\.exports="([0-9]{10,21})"`)

// igDTSGRe extracts the fb_dtsg token (DTSGInitData) from the page HTML; the /api/graphql endpoint rejects graphql posts without it.
var igDTSGRe = regexp.MustCompile(`DTSGInitData",\[\],\{"token":"([^"]+)"`)

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
			if tm := igDTSGRe.FindStringSubmatch(string(page)); tm != nil {
				c.dtsg = tm[1]
			}
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
	csrf   string
	userID string
	docID  string
	dtsg   string

	docIDRefreshed bool
	bootMu         sync.Mutex

	// key identifies the cookie export this client was built from; the shared client is rebuilt only when it changes.
	key string
}

// igSharedClientMu guards the process-level instagram client. One long-lived client (and cookie jar) is reused across targets and sync windows: a real browser does not grow a fresh device identity every twelve hours.
var (
	igSharedClientMu sync.Mutex
	igSharedClient   *igClient
)

// getIGClient returns the shared instagram client, rebuilding it only when the configured credentials change (e.g. the user pasted a new cookie export).
func getIGClient(cookieExport string) *igClient {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cookieExport)))
	igSharedClientMu.Lock()
	defer igSharedClientMu.Unlock()
	if igSharedClient != nil && igSharedClient.key == key {
		return igSharedClient
	}
	c := newIGClient(cookieExport)
	c.key = key
	igSharedClient = c
	return c
}

// newIGClient builds a client whose cookie jar starts from the browser cookie export (Netscape format). Device cookies (mid, ig_did, datr) are what makes the session look like the browser it was created in.
func newIGClient(cookieExport string) *igClient {
	jar, _ := fcookiejar.New(nil)
	parsed, err := cookies.ParseNetscape(cookieExport, "instagram.com")
	if err != nil {
		slog.Warn("Ignoring invalid instagram cookie export", "error", err)
	} else {
		jar.SetCookies(&url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}, parsed)
		slog.Info("Loaded instagram cookie export into client jar", "cookies", len(parsed))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(browser.TLS),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	)
	if err != nil {
		slog.Error("Failed to create tls client, check profiles", "error", err)
	}
	slog.Info("Instagram client TLS profile", "tls_fingerprint", browser.TLS.GetClientHelloStr())
	c := &igClient{
		client: client,
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

	if c.csrf == "" || c.dtsg == "" {
		home, err := c.fetchPage(ctx, "https://www.instagram.com/")
		if err != nil {
			return fmt.Errorf("fetch instagram home: %w", err)
		}
		if tm := igDTSGRe.FindStringSubmatch(string(home)); tm != nil {
			c.dtsg = tm[1]
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
	c.client.GetCookieJar().SetCookies(&url.URL{Scheme: "https", Host: "www.instagram.com", Path: "/"}, []*fhttp.Cookie{{
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
	req.Header.Set("User-Agent", browser.UA)
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

// setXHRHeaders sets the headers instagram's web frontend sends on its own API requests.
func (c *igClient) setXHRHeaders(req *fhttp.Request, username string) {
	req.Header.Set("User-Agent", browser.UA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", igASBDID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Storage-Access", "active")
	req.Header.Set("Referer", "https://www.instagram.com/"+username+"/")
}

// do sends an API request and returns the body of any response.
func (c *igClient) do(req *fhttp.Request) (int, []byte, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, body, err
}

// igStatusErr maps a non-200 API answer to a scraper error.
func igStatusErr(status int, body []byte) error {
	switch {
	case status == fhttp.StatusUnauthorized || status == fhttp.StatusForbidden:
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		// Rate limiting can masquerade as 401/403 with a "please wait" body.
		if isRateLimitBody(string(body)) {
			slog.Warn("Instagram auth-style response is actually rate limiting", "status", status, "body", snippet)
			return fmt.Errorf("%w: instagram returned %d (rate limited)", download.ErrRateLimited, status)
		}
		slog.Warn("Instagram rejected the session", "status", status, "body", snippet)
		return fmt.Errorf("%w: instagram returned %d", ErrAuthExpired, status)
	case status == fhttp.StatusTooManyRequests:
		return fmt.Errorf("%w: instagram returned 429", download.ErrRateLimited)
	case status >= 300 && status < 400:
		return sessionRedirectErr(status)
	default:
		return download.StatusError(status)
	}
}

func (c *igClient) doGet(ctx context.Context, apiURL, username string) ([]byte, error) {
	if err := igPace(ctx); err != nil {
		return nil, err
	}
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	c.setXHRHeaders(req, username)
	if c.csrf != "" {
		req.Header.Set("X-CSRFToken", c.csrf)
	}
	status, body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if status != fhttp.StatusOK {
		return nil, igStatusErr(status, body)
	}
	return body, nil
}

// doGraphQL fetches one page of the profile posts timeline via the web graphql endpoint.
func (c *igClient) doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error) {
	if err := igPace(ctx); err != nil {
		return nil, err
	}
	form := buildGraphQLForm(username, userID, after, count, c.userID, c.docID)
	form.Set("__spin_t", strconv.FormatInt(time.Now().Unix(), 10))
	form.Set("fb_dtsg", c.dtsg)

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, "https://www.instagram.com/api/graphql", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	c.setXHRHeaders(req, username)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRFToken", c.csrf)
	req.Header.Set("Origin", "https://www.instagram.com")

	status, body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	// HTML or the legacy "for (;;);{"__ar":1,...}" envelope on 200, or a logged-in "Page Not Found" on 403, mean an outdated doc_id or a rejected session
	html := len(body) > 0 && body[0] == '<'
	stale := status == fhttp.StatusOK && (html || strings.HasPrefix(string(body), "for (;;);")) ||
		(status == fhttp.StatusUnauthorized || status == fhttp.StatusForbidden) && html && !isRateLimitBody(string(body))
	if stale && !c.docIDRefreshed {
		// re-read the doc_id once before giving up
		c.docIDRefreshed = true
		if rerr := c.refreshDocID(ctx, username); rerr == nil {
			return c.doGraphQL(ctx, username, userID, after, count)
		} else {
			slog.Warn("Instagram doc_id refresh failed", "error", rerr)
		}
	}
	if status != fhttp.StatusOK {
		return nil, igStatusErr(status, body)
	}
	if stale {
		return nil, fmt.Errorf("%w: instagram returned HTML instead of JSON (session rejected or doc_id outdated)", ErrAuthExpired)
	}
	// Successful page: re-arm the one-time doc_id refresh so the long-lived shared client can re-harvest a rotated doc_id later.
	c.docIDRefreshed = false
	return body, nil
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
func buildGraphQLForm(username, userID, after string, count int, ownUserID, docID string) url.Values {
	v := url.Values{}
	vars := map[string]any{
		"username": username,
		"data": map[string]any{
			"count":                             count,
			"include_reel_media_seen_timestamp": true,
			"include_relationship_info":         true,
			"latest_besties_reel_media":         true,
			"latest_reel_media":                 true,
		},
		"__relay_internal__pv__PolarisMultiCaptionCarouselEnabledrelayprovider":  true,
		"__relay_internal__pv__PolarisShortDramaEnabledrelayprovider":            true,
		"__relay_internal__pv__PolarisThreadsStyleCommentsEnabledrelayprovider":  false,
		"__relay_internal__pv__PolarisReelsRecoDebugOverlayEnabledrelayprovider": false,
	}
	if after != "" {
		vars["after"] = after
	}
	varsJSON, _ := json.Marshal(vars)
	user := ownUserID
	if user == "" {
		user = "0"
	}
	v.Set("av", user)
	v.Set("__d", "www")
	// the new /api/graphql endpoint treats a non-zero __user that does not match the session actor as logged out
	v.Set("__user", "0")
	v.Set("__a", "1")
	v.Set("__ccg", "EXCELLENT")
	v.Set("__comet_req", "7")
	v.Set("__spin_r", defaultIGSpinR)
	v.Set("__spin_b", "trunk")
	v.Set("__crn", "comet.igweb.PolarisProfilePostsTabRoute")
	v.Set("fb_api_caller_class", "RelayModern")
	v.Set("fb_api_req_friendly_name", "PolarisProfilePostsQuery")
	v.Set("server_timestamps", "true")
	v.Set("variables", string(varsJSON))
	v.Set("doc_id", docID)
	return v
}
