package xscraper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNotFound means X has no visible account with that screen name.
var ErrNotFound = errors.New("x account not found")

// Profile is the public summary of an X account.
type Profile struct {
	ID         string
	ScreenName string
	Name       string
	Avatar     string
	Tweets     int
	Media      int
	Followers  int
	Protected  bool
}

// Profile looks up a screen name; it costs one rate-limited GraphQL request.
func (s *Scraper) Profile(ctx context.Context, screenName string) (Profile, error) {
	body, err := s.doGet(ctx, opUserByScreenName, map[string]interface{}{"screen_name": screenName}, "https://x.com/"+screenName)
	if err != nil {
		return Profile{}, err
	}
	return parseProfile(body)
}

func parseProfile(body []byte) (Profile, error) {
	var resp struct {
		Data struct {
			User struct {
				Result struct {
					Typename string `json:"__typename"`
					RestID   string `json:"rest_id"`
					Core     struct {
						Name       string `json:"name"`
						ScreenName string `json:"screen_name"`
					} `json:"core"`
					Avatar struct {
						ImageURL string `json:"image_url"`
					} `json:"avatar"`
					Privacy struct {
						Protected bool `json:"protected"`
					} `json:"privacy"`
					Legacy struct {
						Name                 string `json:"name"`
						ScreenName           string `json:"screen_name"`
						ProfileImageURLHTTPS string `json:"profile_image_url_https"`
						StatusesCount        int    `json:"statuses_count"`
						MediaCount           int    `json:"media_count"`
						FollowersCount       int    `json:"followers_count"`
						Protected            bool   `json:"protected"`
					} `json:"legacy"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Profile{}, fmt.Errorf("parse x profile: %w", err)
	}
	r := resp.Data.User.Result
	if r.RestID == "" || r.Typename == "UserUnavailable" {
		return Profile{}, ErrNotFound
	}
	// X moved name and avatar out of legacy in 2025; read both shapes
	p := Profile{
		ID:         r.RestID,
		ScreenName: firstNonEmpty(r.Core.ScreenName, r.Legacy.ScreenName),
		Name:       firstNonEmpty(r.Core.Name, r.Legacy.Name),
		Avatar:     firstNonEmpty(r.Avatar.ImageURL, r.Legacy.ProfileImageURLHTTPS),
		Tweets:     r.Legacy.StatusesCount,
		Media:      r.Legacy.MediaCount,
		Followers:  r.Legacy.FollowersCount,
		Protected:  r.Privacy.Protected || r.Legacy.Protected,
	}
	return p, nil
}

// xOwnScreenNameRe reads the logged-in user out of the home page embedded state.
var xOwnScreenNameRe = regexp.MustCompile(`"screen_name":"([A-Za-z0-9_]{1,20})"`)

// Viewer returns the screen name the session is logged in as; it doubles as a live session check.
// The 1.1 settings/verify_credentials endpoints are retired (code 34 for any caller), so it reads the
// screen name out of the logged-in home page state instead.
func (s *Scraper) Viewer(ctx context.Context) (string, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return "", err
	}
	html, err := s.client.page(ctx, "https://x.com/home?lang=en")
	if err != nil {
		return "", err
	}
	m := xOwnScreenNameRe.FindSubmatch(html)
	if m == nil {
		return "", fmt.Errorf("%w: x.com served a logged-out home page", ErrAuthExpired)
	}
	return string(m[1]), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
