package config

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"idolhub/internal/store"
)

type Account struct {
	Username         string    `json:"username"`
	Platform         string    `json:"platform"`                     // "instagram" or "twitter"
	SaveText         bool      `json:"save_text"`                    // for twitter only
	SkipRetweets     bool      `json:"skip_retweets"`                // for twitter only
	Filters          []string  `json:"filters"`                      // for twitter only, match keywords/phrases
	TwitterAuthToken string    `json:"twitter_auth_token,omitempty"` // per-account override of the global twitter_auth_token
	DownloadPhotos   *bool     `json:"download_photos,omitempty"`
	DownloadVideos   *bool     `json:"download_videos,omitempty"`
	Paused           bool      `json:"paused,omitempty"`
	Cookies          string    `json:"cookies,omitempty"`   // per-account Netscape export; replaces the platform session
	SinceDate        string    `json:"since,omitempty"`     // YYYY-MM-DD lower bound for every sync
	MaxPosts         int       `json:"max_posts,omitempty"` // cap for a sync without a watermark (first or full)
	LastSyncStatus   string    `json:"last_sync_status,omitempty"`
	LastSyncTime     time.Time `json:"last_sync_time,omitempty"`
}

func (a Account) ShouldDownloadPhotos() bool {
	if a.DownloadPhotos == nil {
		return true
	}
	return *a.DownloadPhotos
}

func (a Account) ShouldDownloadVideos() bool {
	if a.DownloadVideos == nil {
		return true
	}
	return *a.DownloadVideos
}

type Config struct {
	Accounts            []Account `json:"accounts"`
	TwitterAuthToken    string    `json:"twitter_auth_token"`
	TwitterCookies      string    `json:"twitter_cookies"`
	InstagramSessionID  string    `json:"instagram_session_id"`
	InstagramCookies    string    `json:"instagram_cookies"`
	InstagramSidecarURL string    `json:"instagram_sidecar_url"`
	ThumbnailsDir       string    `json:"thumbnails_dir"`
	TikTokCookies       string    `json:"tiktok_cookies"`
	AutoSyncInterval    int       `json:"auto_sync_interval"` // In hours
	// QuietHours is "HH-HH" in server local time; auto-sync does not start inside the window. Empty disables it.
	QuietHours string `json:"quiet_hours"`
	// MaxConcurrent bounds simultaneous scrapes; 0 means DefaultMaxConcurrent.
	MaxConcurrent int `json:"max_concurrent"`
}

// DefaultMaxConcurrent is the scrape parallelism when none is configured.
const DefaultMaxConcurrent = 3

// Concurrency returns the effective scrape parallelism.
func (c Config) Concurrency() int {
	if c.MaxConcurrent <= 0 {
		return DefaultMaxConcurrent
	}
	return c.MaxConcurrent
}

// InQuietHours reports whether t falls inside the configured quiet window, which may wrap midnight.
func (c Config) InQuietHours(t time.Time) bool {
	from, to, ok := ParseQuietHours(c.QuietHours)
	if !ok || from == to {
		return false
	}
	h := t.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// ParseQuietHours parses "HH-HH"; ok is false for an empty or malformed value.
func ParseQuietHours(v string) (from, to int, ok bool) {
	a, b, found := strings.Cut(strings.TrimSpace(v), "-")
	if !found {
		return 0, 0, false
	}
	from, err1 := strconv.Atoi(strings.TrimSpace(a))
	to, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || from < 0 || from > 23 || to < 0 || to > 23 {
		return 0, 0, false
	}
	return from, to, true
}

var (
	configMu     sync.RWMutex
	globalConfig Config
)

// LoadConfig loads accounts and secrets from the store DB, the single source of truth.
func LoadConfig(st *store.Store) error {
	configMu.Lock()
	defer configMu.Unlock()

	globalConfig = Config{
		Accounts: []Account{},
	}
	if st == nil {
		return nil
	}
	loadFromStoreLocked(st)
	return nil
}

func loadFromStoreLocked(st *store.Store) {
	ctx := context.Background()
	rows, err := st.Accounts.List(ctx)
	if err != nil {
		slog.Warn("Failed to read accounts from store", "error", err)
		return
	}
	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		info, err := st.Accounts.GetSyncInfo(ctx, row.Platform, row.Username)
		if err != nil {
			slog.Warn("Failed to read sync info from store", "user", row.Username, "error", err)
		}
		accounts = append(accounts, Account{
			Username:         row.Username,
			Platform:         row.Platform,
			SaveText:         row.SaveText,
			SkipRetweets:     row.SkipRetweets,
			Filters:          row.Filters,
			DownloadPhotos:   row.DownloadPhotos,
			DownloadVideos:   row.DownloadVideos,
			TwitterAuthToken: row.TwitterAuthToken,
			Paused:           row.Paused,
			Cookies:          row.Cookies,
			SinceDate:        row.SinceDate,
			MaxPosts:         row.MaxPosts,
			LastSyncStatus:   info.Status,
			LastSyncTime:     info.Time,
		})
	}
	globalConfig = Config{
		Accounts:            accounts,
		TwitterAuthToken:    settingString(st, ctx, "twitter_auth_token"),
		TwitterCookies:      settingString(st, ctx, "twitter_cookies"),
		InstagramSessionID:  settingString(st, ctx, "instagram_session_id"),
		InstagramCookies:    settingString(st, ctx, "instagram_cookies"),
		InstagramSidecarURL: settingString(st, ctx, "instagram_sidecar_url"),
		ThumbnailsDir:       settingString(st, ctx, "thumbnails_dir"),
		TikTokCookies:       settingString(st, ctx, "tiktok_cookies"),
		AutoSyncInterval:    settingInt(st, ctx, "auto_sync_interval"),
		QuietHours:          settingString(st, ctx, "quiet_hours"),
		MaxConcurrent:       settingInt(st, ctx, "max_concurrent"),
	}
}

func settingString(st *store.Store, ctx context.Context, key string) string {
	raw, err := st.Settings.Get(ctx, key)
	if err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return ""
	}
	return value
}

func settingInt(st *store.Store, ctx context.Context, key string) int {
	raw, err := st.Settings.Get(ctx, key)
	if err != nil {
		return 0
	}
	var value int
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return 0
	}
	return value
}

// SaveConfig hot-reloads the in-memory config and persists it to the store DB.
func SaveConfig(st *store.Store, cfg Config) error {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = cfg
	if st == nil {
		return nil
	}
	return saveToStore(st)
}

func saveToStore(st *store.Store) error {
	ctx := context.Background()
	keep := make(map[string]bool)
	for _, acc := range globalConfig.Accounts {
		keep[acc.Platform+"/"+acc.Username] = true
		err := st.Accounts.Upsert(ctx, store.Account{
			Platform:         acc.Platform,
			Username:         acc.Username,
			SaveText:         acc.SaveText,
			SkipRetweets:     acc.SkipRetweets,
			DownloadPhotos:   acc.DownloadPhotos,
			DownloadVideos:   acc.DownloadVideos,
			Filters:          acc.Filters,
			TwitterAuthToken: acc.TwitterAuthToken,
			Paused:           acc.Paused,
			Cookies:          acc.Cookies,
			SinceDate:        acc.SinceDate,
			MaxPosts:         acc.MaxPosts,
		})
		if err != nil {
			return err
		}
	}
	rows, err := st.Accounts.List(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		// exact match: the primary key is case-sensitive, so a renamed "Foo" -> "foo" must drop the old row
		if keep[row.Platform+"/"+row.Username] {
			continue
		}
		if err := st.Accounts.Delete(ctx, row.Platform, row.Username); err != nil {
			return err
		}
	}
	settings := map[string]any{
		"twitter_auth_token":    globalConfig.TwitterAuthToken,
		"twitter_cookies":       globalConfig.TwitterCookies,
		"instagram_session_id":  globalConfig.InstagramSessionID,
		"instagram_cookies":     globalConfig.InstagramCookies,
		"instagram_sidecar_url": globalConfig.InstagramSidecarURL,
		"thumbnails_dir":        globalConfig.ThumbnailsDir,
		"tiktok_cookies":        globalConfig.TikTokCookies,
		"auto_sync_interval":    globalConfig.AutoSyncInterval,
		"quiet_hours":           globalConfig.QuietHours,
		"max_concurrent":        globalConfig.MaxConcurrent,
	}
	for key, value := range settings {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := st.Settings.Set(ctx, key, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// UpdateAccounts applies fn to the account list and persists the result under the config lock, so concurrent
// edits from several requests or tabs cannot overwrite each other.
func UpdateAccounts(st *store.Store, fn func([]Account) ([]Account, error)) (Config, error) {
	configMu.Lock()
	defer configMu.Unlock()
	next, err := fn(slices.Clone(globalConfig.Accounts))
	if err != nil {
		return globalConfig, err
	}
	prev := globalConfig.Accounts
	globalConfig.Accounts = next
	if st != nil {
		if err := saveToStore(st); err != nil {
			globalConfig.Accounts = prev
			return globalConfig, err
		}
	}
	return globalConfig, nil
}

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}
