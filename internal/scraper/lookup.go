package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"idolhub/internal/igbrowser"
	"idolhub/internal/xscraper"
)

var (
	// ErrLookupUnsupported means the platform has no cheap way to inspect a profile or session.
	ErrLookupUnsupported = errors.New("not supported for this platform")
	// ErrProfileNotFound means the platform reports no such account.
	ErrProfileNotFound = errors.New("profile not found")
)

// Profile is a public account summary shown before a target is added.
type Profile struct {
	Username  string `json:"username"`
	Name      string `json:"name,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Posts     int    `json:"posts,omitempty"`
	Followers int    `json:"followers,omitempty"`
	Private   bool   `json:"private,omitempty"`
}

// LookupProfile fetches a profile summary with the configured session. It is one request, made only on user action.
func LookupProfile(ctx context.Context, platform, username string, opts Options) (Profile, error) {
	switch platform {
	case "twitter":
		s, err := xscraper.New(xscraper.Session{AuthToken: opts.TwitterAuthToken, Cookies: opts.TwitterCookies})
		if err != nil {
			return Profile{}, err
		}
		p, err := s.Profile(ctx, username)
		if errors.Is(err, xscraper.ErrNotFound) {
			return Profile{}, ErrProfileNotFound
		}
		if errors.Is(err, xscraper.ErrAuthExpired) {
			return Profile{}, fmt.Errorf("%w: %w", ErrAuthExpired, err)
		}
		if err != nil {
			return Profile{}, err
		}
		return Profile{Username: p.ScreenName, Name: p.Name, Avatar: p.Avatar, Posts: p.Tweets, Followers: p.Followers, Private: p.Protected}, nil
	case "instagram":
		f, err := instagramFetcher(ctx, opts)
		if err != nil {
			return Profile{}, err
		}
		return lookupInstagram(ctx, f, username)
	}
	return Profile{}, ErrLookupUnsupported
}

// CheckSession makes one authenticated request and reports who the session belongs to.
func CheckSession(ctx context.Context, platform string, opts Options) (string, error) {
	switch platform {
	case "twitter":
		if opts.TwitterAuthToken == "" && opts.TwitterCookies == "" {
			return "", errors.New("no x.com cookies saved")
		}
		s, err := xscraper.New(xscraper.Session{AuthToken: opts.TwitterAuthToken, Cookies: opts.TwitterCookies})
		if err != nil {
			return "", err
		}
		name, err := s.Viewer(ctx)
		if err != nil {
			return "", err
		}
		return "@" + name, nil
	case "instagram":
		if opts.InstagramSidecarURL != "" {
			st, err := igbrowser.New(opts.InstagramSidecarURL).Status(ctx)
			if err != nil {
				return "", fmt.Errorf("sidecar unreachable: %w", err)
			}
			if st.Session != "ok" {
				return "", fmt.Errorf("%w: sidecar session is %s", ErrAuthExpired, st.Session)
			}
			return "user id " + st.DsUserID, nil
		}
		if opts.InstagramSessionID == "" && opts.InstagramCookies == "" {
			return "", errors.New("no instagram.com cookies saved")
		}
		c := getIGClient(opts.InstagramSessionID, opts.InstagramCookies)
		if err := c.bootstrap(ctx); err != nil {
			return "", fmt.Errorf("%w: %w", ErrAuthExpired, err)
		}
		return "user id " + c.userID, nil
	}
	return "", ErrLookupUnsupported
}

func instagramFetcher(ctx context.Context, opts Options) (igFetcher, error) {
	if opts.InstagramSidecarURL != "" {
		bc := newIGBrowserClient(opts.InstagramSidecarURL)
		if err := bc.OwnID(ctx); err != nil {
			return nil, err
		}
		return bc, nil
	}
	if opts.InstagramSessionID == "" && opts.InstagramCookies == "" {
		return nil, errors.New("instagram session is not configured")
	}
	c := getIGClient(opts.InstagramSessionID, opts.InstagramCookies)
	if c.csrf == "" || c.userID == "" {
		if err := c.bootstrap(ctx); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAuthExpired, err)
		}
	}
	return c, nil
}

// lookupInstagram uses topsearch, which is far less throttled than web_profile_info.
func lookupInstagram(ctx context.Context, f igFetcher, username string) (Profile, error) {
	body, err := f.doGet(ctx, "https://www.instagram.com/api/v1/web/search/topsearch/?query="+url.QueryEscape(username), username)
	if err != nil {
		return Profile{}, err
	}
	return parseInstagramSearch(body, username)
}

func parseInstagramSearch(body []byte, username string) (Profile, error) {
	var search struct {
		Users []struct {
			User struct {
				Username      string `json:"username"`
				FullName      string `json:"full_name"`
				ProfilePicURL string `json:"profile_pic_url"`
				IsPrivate     bool   `json:"is_private"`
				FollowerCount int    `json:"follower_count"`
			} `json:"user"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &search); err != nil {
		return Profile{}, fmt.Errorf("parse instagram search: %w", err)
	}
	for _, u := range search.Users {
		if strings.EqualFold(u.User.Username, username) {
			return Profile{
				Username:  u.User.Username,
				Name:      u.User.FullName,
				Avatar:    u.User.ProfilePicURL,
				Followers: u.User.FollowerCount,
				Private:   u.User.IsPrivate,
			}, nil
		}
	}
	return Profile{}, ErrProfileNotFound
}
