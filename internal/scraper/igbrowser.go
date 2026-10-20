package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"idolhub/internal/igbrowser"
)

// igFetcher is the request surface scrapeInstagramDirect needs; both the tls-client and the browser-sidecar paths implement it.
type igFetcher interface {
	doGet(ctx context.Context, apiURL, username string) ([]byte, error)
	doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error)
	resolveUserID(ctx context.Context, username string) (string, error)
}

// resolveUserIDShared resolves the numeric user id: topsearch first, then web_profile_info. Works over any fetcher.
func resolveUserIDShared(ctx context.Context, f igFetcher, username string) (string, error) {
	if id, err := f.doGet(ctx, fmt.Sprintf("https://www.instagram.com/api/v1/web/search/topsearch/?query=%s", url.PathEscape(username)), username); err == nil {
		var search struct {
			Users []struct {
				User struct {
					ID       string `json:"pk"`
					Username string `json:"username"`
				} `json:"user"`
			} `json:"users"`
		}
		if json.Unmarshal(id, &search) == nil {
			for _, u := range search.Users {
				if strings.EqualFold(u.User.Username, username) {
					return u.User.ID, nil
				}
			}
		}
	}
	profile, err := f.doGet(ctx, fmt.Sprintf("https://www.instagram.com/api/v1/users/web_profile_info/?username=%s", url.PathEscape(username)), username)
	if err != nil {
		// transport-level failure (sidecar down, network): report as-is, not as auth expiry
		if !errors.Is(err, ErrAuthExpired) {
			return "", fmt.Errorf("could not resolve Instagram user ID for @%s: %w", username, err)
		}
		return "", fmt.Errorf("%w: could not resolve Instagram user ID for @%s (session may be expired or rate-limited)", ErrAuthExpired, username)
	}
	var profileData struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(profile, &profileData) == nil && profileData.Data.User.ID != "" {
		return profileData.Data.User.ID, nil
	}
	return "", fmt.Errorf("%w: could not resolve Instagram user ID for @%s", ErrAuthExpired, username)
}

// igBrowserClient runs instagram requests inside the camoufox sidecar.
type igBrowserClient struct {
	sc       *igbrowser.Client
	ownID    string
	docID    string
	dtsg     string
	Resolved bool
}

func newIGBrowserClient(sidecarURL string) *igBrowserClient {
	return &igBrowserClient{sc: igbrowser.New(sidecarURL), docID: defaultIGGraphQLDocID}
}

func (b *igBrowserClient) doGet(ctx context.Context, apiURL, username string) ([]byte, error) {
	res, err := b.sc.Fetch(ctx, apiURL, "GET", "", "")
	if err != nil {
		return nil, err
	}
	out, herr := handleBrowserStatus(res, apiURL)
	if herr == nil {
		return out, nil
	}
	// A stale page context answers as logged-out HTML (topsearch 404) even when the cookie session is valid; retry once with a fresh instagram.com load.
	if res.Status != 429 {
		if res2, err2 := b.sc.Fetch(ctx, apiURL, "GET", "", "https://www.instagram.com/"); err2 == nil {
			if out2, herr2 := handleBrowserStatus(res2, apiURL); herr2 == nil {
				return out2, nil
			}
		}
	}
	return nil, herr
}

func (b *igBrowserClient) doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error) {
	send := func() ([]byte, error) {
		form := buildGraphQLForm(username, userID, after, count, b.ownID, "", b.docID)
		form.Set("fb_dtsg", b.dtsg)
		body := form.Encode()
		// Navigate to the profile first: the page's own JS fires the same graphql operation, and the sidecar harvests the fresh doc_id / fb_dtsg / asbd-id / www-claim from it before we send ours.
		res, err := b.sc.Fetch(ctx, "https://www.instagram.com/api/graphql", "POST", body,
			"https://www.instagram.com/"+username+"/")
		if err != nil {
			return nil, err
		}
		return handleBrowserStatus(res, "graphql")
	}
	out, err := send()
	if err == nil || !errors.Is(err, ErrAuthExpired) {
		return out, err
	}
	// Instagram rotates fb_dtsg / doc_id with page loads; re-read them from the sidecar once and retry.
	if st, serr := b.sc.Status(ctx); serr == nil {
		if st.FbDTSG != "" {
			b.dtsg = st.FbDTSG
		}
		if st.DocID != "" {
			b.docID = st.DocID
		}
		return send()
	}
	return out, err
}

// handleBrowserStatus maps page fetch statuses to scraper errors.
func handleBrowserStatus(res *igbrowser.FetchResult, what string) ([]byte, error) {
	// surface instagram's own failure message ("useragent mismatch", "Please wait...") — the bare status hides the cause
	reason := func() string {
		var msg struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(res.Body), &msg) == nil && msg.Message != "" {
			return ": " + msg.Message
		}
		return ""
	}
	switch {
	case res.Status == 200:
		trimmed := strings.TrimSpace(res.Body)
		// "for (;;);{"__ar":1,...}" is the legacy envelope instagram serves for stale fb_dtsg/doc_id or a rejected session
		if strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "for (;;);") {
			return nil, fmt.Errorf("%w: %s returned a non-JSON response (stale request identity or session rejected)", ErrAuthExpired, what)
		}
		return []byte(res.Body), nil
	case res.Status == 429:
		return nil, fmt.Errorf("%w: %s returned 429%s", ErrRateLimited, what, reason())
	case res.Status == 401 || res.Status == 403 || (res.Status >= 300 && res.Status < 400):
		return nil, fmt.Errorf("%w: %s returned %d%s", ErrAuthExpired, what, res.Status, reason())
	default:
		return nil, fmt.Errorf("instagram %s returned status %d%s", what, res.Status, reason())
	}
}

var profilePagePK = regexp.MustCompile(`profilePage_([0-9]+)`)

// resolveUserID reads the numeric pk straight from the profile page HTML: topsearch is dead for web clients (404 for everyone) and web_profile_info is aggressively rate-limited, while the page itself always renders.
func (b *igBrowserClient) resolveUserID(ctx context.Context, username string) (string, error) {
	res, err := b.sc.Fetch(ctx, "https://www.instagram.com/"+username+"/", "GET", "", "https://www.instagram.com/")
	if err == nil && res.Status == 200 {
		if m := profilePagePK.FindStringSubmatch(res.Body); m != nil {
			// the page load refreshed the sidecar's captured request identity; pull it in for graphql
			if st, serr := b.sc.Status(ctx); serr == nil {
				if st.DocID != "" {
					b.docID = st.DocID
				}
				if st.DsUserID != "" {
					b.ownID = st.DsUserID
				}
			}
			return m[1], nil
		}
	}
	return resolveUserIDShared(ctx, b, username)
}

// OwnID refreshes ds_user_id and the captured request identity (doc_id, www-claim) from the sidecar session status.
func (b *igBrowserClient) OwnID(ctx context.Context) error {
	st, err := b.sc.Status(ctx)
	if err != nil {
		return err
	}
	if st.Session != "ok" {
		return fmt.Errorf("%w: sidecar session is %s", ErrAuthExpired, st.Session)
	}
	b.ownID = st.DsUserID
	if st.DocID != "" {
		b.docID = st.DocID
	}
	if st.FbDTSG != "" {
		b.dtsg = st.FbDTSG
	}
	return nil
}
