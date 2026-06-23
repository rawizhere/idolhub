package config

import (
	"cmp"
	"encoding/json"
	"os"
	"sync"
	"time"
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
	LastSyncMode   string    `json:"last_sync_mode,omitempty"` // instagram backend used for the last sync ("picuki"|"direct")
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
	Concurrency        int       `json:"concurrency"`
	TwitterAuthToken   string    `json:"twitter_auth_token"`
	InstagramSessionID string    `json:"instagram_session_id"`
	InstagramMode      string    `json:"instagram_mode"`     // "picuki" (default) or "direct"
	AutoSyncInterval   int       `json:"auto_sync_interval"` // In hours
}

// Store provides access to the application configuration
type Store interface {
	Get() Config
	Save(Config) error
	Load() error
	Update(func(*Config))
}

var GlobalStore Store = &configStore{}

var (
	configMu     sync.RWMutex
	globalConfig Config
)

type configStore struct{}

func (configStore) Get() Config           { return GetConfig() }
func (configStore) Save(cfg Config) error { return SaveConfig(cfg) }
func (configStore) Load() error           { return LoadConfig() }
func (configStore) Update(fn func(*Config)) {
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
		Accounts:    []Account{},
		Concurrency: 5,
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

	// Fall back to environment variables if not set in config
	if globalConfig.TwitterAuthToken == "" {
		globalConfig.TwitterAuthToken = os.Getenv("TWITTER_AUTH_TOKEN")
	}
	if globalConfig.InstagramSessionID == "" {
		globalConfig.InstagramSessionID = os.Getenv("INSTAGRAM_SESSION_ID")
	}

	globalConfig.InstagramMode = cmp.Or(globalConfig.InstagramMode, "picuki")

	return nil
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
