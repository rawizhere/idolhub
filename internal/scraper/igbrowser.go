package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"idolhub/internal/igbrowser"
)

// igFetcher is the request surface scrapeInstagramDirect needs; both the
// tls-client and the browser-sidecar paths implement it.
type igFetcher interface {
	doGet(ctx context.Context, apiURL, username string) ([]byte, error)
	doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error)
	resolveUserID(ctx context.Context, username string) (string, error)
}

// resolveUserIDShared resolves the numeric user id: topsearch first, then
// web_profile_info. Works over any fetcher.
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
	Resolved bool
}

func newIGBrowserClient(sidecarURL string) *igBrowserClient {
	return &igBrowserClient{sc: igbrowser.New(sidecarURL), docID: defaultIGGraphQLDocID}
}

func (b *igBrowserClient) doGet(ctx context.Context, apiURL, username string) ([]byte, error) {
	res, err := b.sc.Fetch(ctx, apiURL, "GET", "")
	if err != nil {
		return nil, err
	}
	return handleBrowserStatus(res, apiURL)
}

func (b *igBrowserClient) doGraphQL(ctx context.Context, username, userID, after string, count int) ([]byte, error) {
	form := buildGraphQLForm(username, userID, after, count, b.ownID, "", b.docID)
	body := form.Encode()
	res, err := b.sc.Fetch(ctx, "https://www.instagram.com/graphql/query", "POST", body)
	if err != nil {
		return nil, err
	}
	return handleBrowserStatus(res, "graphql")
}

// handleBrowserStatus maps page fetch statuses to scraper errors.
func handleBrowserStatus(res *igbrowser.FetchResult, what string) ([]byte, error) {
	switch {
	case res.Status == 200:
		if strings.HasPrefix(strings.TrimSpace(res.Body), "<") {
			return nil, fmt.Errorf("%w: %s returned HTML instead of JSON", ErrAuthExpired, what)
		}
		return []byte(res.Body), nil
	case res.Status == 401 || res.Status == 403 || res.Status == 429 || (res.Status >= 300 && res.Status < 400):
		return nil, fmt.Errorf("%w: %s returned %d", ErrAuthExpired, what, res.Status)
	default:
		return nil, fmt.Errorf("instagram %s returned status %d", what, res.Status)
	}
}

func (b *igBrowserClient) resolveUserID(ctx context.Context, username string) (string, error) {
	return resolveUserIDShared(ctx, b, username)
}

// OwnID refreshes ds_user_id from the sidecar session status.
func (b *igBrowserClient) OwnID(ctx context.Context) error {
	st, err := b.sc.Status(ctx)
	if err != nil {
		return err
	}
	if st.Session != "ok" {
		return fmt.Errorf("%w: sidecar session is %s", ErrAuthExpired, st.Session)
	}
	b.ownID = st.DsUserID
	return nil
}
