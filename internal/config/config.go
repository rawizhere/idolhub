package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/caarlos0/env/v11"
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
	TwitterAuthToken   string    `json:"twitter_auth_token" env:"TWITTER_AUTH_TOKEN"`
	InstagramSessionID string    `json:"instagram_session_id" env:"INSTAGRAM_SESSION_ID"`
	TikTokCookies      string    `json:"tiktok_cookies" env:"TIKTOK_COOKIES"`
	AutoSyncInterval   int       `json:"auto_sync_interval"` // In hours
}

var (
	configMu     sync.RWMutex
	globalConfig Config
)

func UpdateConfig(fn func(*Config)) {
	configMu.Lock()
	fn(&globalConfig)
	_ = saveConfigLocked()
	configMu.Unlock()
}

const configPath = "configs/config.json"

func LoadConfig() error {
	configMu.Lock()
	defer configMu.Unlock()

	globalConfig = Config{
		Accounts: []Account{},
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return saveConfigLocked()
		}
		if fi, statErr := os.Stat(configPath); statErr == nil && fi.IsDir() {
			return nil
		}
		return err
	}

	if err := json.Unmarshal(data, &globalConfig); err != nil {
		return err
	}

	applyEnvSecrets()

	return nil
}

// applyEnvSecrets fills secrets from environment if they are missing in config.
func applyEnvSecrets() {
	var envCfg Config
	if err := env.Parse(&envCfg); err != nil {
		slog.Warn("failed to parse env secrets", "error", err)
		return
	}
	if globalConfig.TwitterAuthToken == "" && envCfg.TwitterAuthToken != "" {
		globalConfig.TwitterAuthToken = envCfg.TwitterAuthToken
	}
	if globalConfig.InstagramSessionID == "" && envCfg.InstagramSessionID != "" {
		globalConfig.InstagramSessionID = envCfg.InstagramSessionID
	}
	if globalConfig.TikTokCookies == "" && envCfg.TikTokCookies != "" {
		globalConfig.TikTokCookies = envCfg.TikTokCookies
	}
}

func SaveConfig(cfg Config) error {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = cfg
	return saveConfigLocked()
}

func saveConfigLocked() error {
	if globalConfig.Accounts == nil {
		globalConfig.Accounts = []Account{}
	}
	data, err := json.MarshalIndent(globalConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}
