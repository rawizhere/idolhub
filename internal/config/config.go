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

// Account is a stored account row plus its sync bookkeeping.
type Account struct {
	store.Account
	LastSyncStatus string
	LastSyncTime   time.Time
}

func (a Account) ShouldDownloadPhotos() bool {
	return a.DownloadPhotos == nil || *a.DownloadPhotos
}

func (a Account) ShouldDownloadVideos() bool {
	return a.DownloadVideos == nil || *a.DownloadVideos
}

type Config struct {
	// Accounts never goes over the wire: secrets live in it, and the account API owns the list.
	Accounts            []Account `json:"-"`
	TwitterCookies      string    `json:"twitter_cookies"`
	InstagramCookies    string    `json:"instagram_cookies"`
	InstagramSidecarURL string    `json:"instagram_sidecar_url"`
	ThumbnailsDir       string    `json:"thumbnails_dir"`
	TikTokCookies       string    `json:"tiktok_cookies"`
	YouTubeCookies      string    `json:"youtube_cookies"`
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

	globalConfig = Config{Accounts: []Account{}}
	ctx := context.Background()
	rows, err := st.Accounts.List(ctx)
	if err != nil {
		slog.Warn("Failed to read accounts from store", "error", err)
		return nil
	}
	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		info, err := st.Accounts.GetSyncInfo(ctx, row.Platform, row.Username)
		if err != nil {
			slog.Warn("Failed to read sync info from store", "user", row.Username, "error", err)
		}
		accounts = append(accounts, Account{Account: row, LastSyncStatus: info.Status, LastSyncTime: info.Time})
	}
	globalConfig = Config{
		Accounts:            accounts,
		TwitterCookies:      setting[string](st, ctx, "twitter_cookies"),
		InstagramCookies:    setting[string](st, ctx, "instagram_cookies"),
		InstagramSidecarURL: setting[string](st, ctx, "instagram_sidecar_url"),
		ThumbnailsDir:       setting[string](st, ctx, "thumbnails_dir"),
		TikTokCookies:       setting[string](st, ctx, "tiktok_cookies"),
		YouTubeCookies:      setting[string](st, ctx, "youtube_cookies"),
		AutoSyncInterval:    setting[int](st, ctx, "auto_sync_interval"),
		QuietHours:          setting[string](st, ctx, "quiet_hours"),
		MaxConcurrent:       setting[int](st, ctx, "max_concurrent"),
	}
	return nil
}

// setting reads a JSON-encoded settings value; a missing or unreadable one is the zero value.
func setting[T any](st *store.Store, ctx context.Context, key string) T {
	var value T
	if raw, err := st.Settings.Get(ctx, key); err == nil {
		_ = json.Unmarshal([]byte(raw), &value)
	}
	return value
}

// SaveConfig hot-reloads the in-memory config and persists it to the store DB.
func SaveConfig(st *store.Store, cfg Config) error {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = cfg
	return saveToStore(st)
}

func saveToStore(st *store.Store) error {
	ctx := context.Background()
	keep := make(map[string]bool)
	for _, acc := range globalConfig.Accounts {
		keep[acc.Platform+"/"+acc.Username] = true
		if err := st.Accounts.Upsert(ctx, acc.Account); err != nil {
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
		"twitter_cookies":       globalConfig.TwitterCookies,
		"instagram_cookies":     globalConfig.InstagramCookies,
		"instagram_sidecar_url": globalConfig.InstagramSidecarURL,
		"thumbnails_dir":        globalConfig.ThumbnailsDir,
		"tiktok_cookies":        globalConfig.TikTokCookies,
		"youtube_cookies":       globalConfig.YouTubeCookies,
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
	if err := saveToStore(st); err != nil {
		globalConfig.Accounts = prev
		return globalConfig, err
	}
	return globalConfig, nil
}

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}
