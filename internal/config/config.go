package config

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"idolhub/internal/store"
)

type Account struct {
	Username       string    `json:"username"`
	Platform       string    `json:"platform"`      // "instagram" or "twitter"
	SaveText       bool      `json:"save_text"`     // for twitter only
	SkipRetweets   bool      `json:"skip_retweets"` // for twitter only
	Filters        []string  `json:"filters"`       // for twitter only, match keywords/phrases
	DownloadPhotos *bool     `json:"download_photos,omitempty"`
	DownloadVideos *bool     `json:"download_videos,omitempty"`
	LastSyncStatus string    `json:"last_sync_status,omitempty"`
	LastSyncTime   time.Time `json:"last_sync_time,omitempty"`
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
	Accounts           []Account `json:"accounts"`
	TwitterAuthToken   string    `json:"twitter_auth_token"`
	InstagramSessionID string    `json:"instagram_session_id"`
	TikTokCookies      string    `json:"tiktok_cookies"`
	AutoSyncInterval   int       `json:"auto_sync_interval"` // In hours
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
			Username:       row.Username,
			Platform:       row.Platform,
			SaveText:       row.SaveText,
			SkipRetweets:   row.SkipRetweets,
			Filters:        row.Filters,
			DownloadPhotos: row.DownloadPhotos,
			DownloadVideos: row.DownloadVideos,
			LastSyncStatus: info.Status,
			LastSyncTime:   info.Time,
		})
	}
	globalConfig = Config{
		Accounts:           accounts,
		TwitterAuthToken:   settingString(st, ctx, "twitter_auth_token"),
		InstagramSessionID: settingString(st, ctx, "instagram_session_id"),
		TikTokCookies:      settingString(st, ctx, "tiktok_cookies"),
		AutoSyncInterval:   settingInt(st, ctx, "auto_sync_interval"),
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

// SaveConfig updates the in-memory config (hot reload) and persists it to
// the store DB.
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
		keep[acc.Platform+"/"+strings.ToLower(acc.Username)] = true
		err := st.Accounts.Upsert(ctx, store.Account{
			Platform:       acc.Platform,
			Username:       acc.Username,
			SaveText:       acc.SaveText,
			SkipRetweets:   acc.SkipRetweets,
			DownloadPhotos: acc.DownloadPhotos,
			DownloadVideos: acc.DownloadVideos,
			Filters:        acc.Filters,
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
		if keep[row.Platform+"/"+strings.ToLower(row.Username)] {
			continue
		}
		if err := st.Accounts.Delete(ctx, row.Platform, row.Username); err != nil {
			return err
		}
	}
	settings := map[string]any{
		"twitter_auth_token":   globalConfig.TwitterAuthToken,
		"instagram_session_id": globalConfig.InstagramSessionID,
		"tiktok_cookies":       globalConfig.TikTokCookies,
		"auto_sync_interval":   globalConfig.AutoSyncInterval,
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

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}
