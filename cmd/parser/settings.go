package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"idolhub/internal/config"
	"idolhub/internal/cookies"

	fhttp "github.com/bogdanfinn/fhttp"
)

// configDTO is the wire shape of /api/config. Secrets are never sent to the browser: GET omits them and
// reports cookieStatus instead, POST treats an absent secret as "keep" and an empty string as "clear".
// The depth-0 pointer fields shadow the embedded Config fields of the same JSON name.
type configDTO struct {
	config.Config
	// Accounts is optional on POST: the settings form leaves it out and the account API owns the list.
	Accounts           *[]config.Account `json:"accounts,omitempty"`
	TwitterAuthToken   *string           `json:"twitter_auth_token,omitempty"`
	TwitterCookies     *string           `json:"twitter_cookies,omitempty"`
	InstagramSessionID *string           `json:"instagram_session_id,omitempty"`
	InstagramCookies   *string           `json:"instagram_cookies,omitempty"`
	TikTokCookies      *string           `json:"tiktok_cookies,omitempty"`

	CookieStatus map[string]cookieStatus `json:"cookie_status,omitempty"`
}

type cookieStatus struct {
	Set     bool     `json:"set"`
	Count   int      `json:"count"`
	Missing []string `json:"missing,omitempty"`
	// Expires is the earliest expiry among the required cookies; empty for session cookies.
	Expires string `json:"expires,omitempty"`
	Expired bool   `json:"expired,omitempty"`
	Error   string `json:"error,omitempty"`
}

var requiredCookies = map[string][]string{
	"twitter":   {"auth_token", "ct0"},
	"instagram": {"sessionid"},
	"tiktok":    {"sessionid"},
}

// inspectCookies is a static check of a Netscape export: it cannot tell a revoked session from a live one.
func inspectCookies(platform, raw string) cookieStatus {
	st := cookieStatus{Set: strings.TrimSpace(raw) != ""}
	if !st.Set {
		return st
	}
	parsed, err := cookies.ParseNetscape(raw, "")
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Count = len(parsed)
	var earliest time.Time
	for _, name := range requiredCookies[platform] {
		i := slices.IndexFunc(parsed, func(c *fhttp.Cookie) bool { return c.Name == name })
		if i < 0 {
			st.Missing = append(st.Missing, name)
			continue
		}
		exp := parsed[i].Expires
		if exp.Unix() <= 0 {
			continue
		}
		if earliest.IsZero() || exp.Before(earliest) {
			earliest = exp
		}
	}
	if !earliest.IsZero() {
		st.Expires = earliest.UTC().Format(time.RFC3339)
		st.Expired = earliest.Before(time.Now())
	}
	return st
}

func (a *App) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")

	cfg := config.GetConfig()
	// copy before blanking: GetConfig shares the accounts backing array with the live config
	accounts := slices.Clone(cfg.Accounts)
	for i := range accounts {
		accounts[i].TwitterAuthToken = ""
		accounts[i].Cookies = ""
	}
	_ = json.NewEncoder(w).Encode(configDTO{
		Config:   cfg,
		Accounts: &accounts,
		CookieStatus: map[string]cookieStatus{
			"twitter":   inspectCookies("twitter", cfg.TwitterCookies),
			"instagram": inspectCookies("instagram", cfg.InstagramCookies),
			"tiktok":    inspectCookies("tiktok", cfg.TikTokCookies),
		},
	})
}

// merge resolves the request against the stored config: absent secrets keep their stored value and
// cookie-editor JSON exports are normalised to Netscape.
func (d configDTO) merge(old config.Config) (config.Config, error) {
	cfg := d.Config
	if err := validateSyncSettings(syncSettings{AutoSyncInterval: cfg.AutoSyncInterval, QuietHours: cfg.QuietHours, MaxConcurrent: cfg.MaxConcurrent}); err != nil {
		return cfg, err
	}
	if d.Accounts == nil {
		cfg.Accounts = slices.Clone(old.Accounts)
	} else {
		cfg.Accounts = *d.Accounts
	}
	pick := func(v *string, cur string) string {
		if v == nil {
			return cur
		}
		return strings.TrimSpace(*v)
	}
	cfg.TwitterAuthToken = pick(d.TwitterAuthToken, old.TwitterAuthToken)
	cfg.InstagramSessionID = pick(d.InstagramSessionID, old.InstagramSessionID)
	for _, f := range []struct {
		dst *string
		v   *string
		cur string
	}{
		{&cfg.TwitterCookies, d.TwitterCookies, old.TwitterCookies},
		{&cfg.InstagramCookies, d.InstagramCookies, old.InstagramCookies},
		{&cfg.TikTokCookies, d.TikTokCookies, old.TikTokCookies},
	} {
		val := pick(f.v, f.cur)
		if strings.HasPrefix(val, "[") {
			converted, err := cookies.JSONToNetscape(val)
			if err != nil {
				return cfg, err
			}
			val = converted
		}
		*f.dst = val
	}
	// per-account secrets are never sent to the browser, so a client that posts accounts back cannot carry them
	for i, acc := range cfg.Accounts {
		for _, o := range old.Accounts {
			if o.Platform == acc.Platform && strings.EqualFold(o.Username, acc.Username) {
				if acc.TwitterAuthToken == "" {
					cfg.Accounts[i].TwitterAuthToken = o.TwitterAuthToken
				}
				if acc.Cookies == "" {
					cfg.Accounts[i].Cookies = o.Cookies
				}
				break
			}
		}
	}
	return cfg, nil
}
