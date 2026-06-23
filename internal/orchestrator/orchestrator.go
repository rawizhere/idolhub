package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"idolhub/internal/config"
	"idolhub/internal/gallery"
	"idolhub/internal/scraper"
)

const maxTaskLogs = 1000

type TaskLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type TaskProgress struct {
	Username   string    `json:"username"`
	Platform   string    `json:"platform"`
	Status     string    `json:"status"` // "idle", "running", "completed", "failed"
	Progress   int       `json:"progress"`
	Logs       []TaskLog `json:"logs"`
	UpdatedAt  time.Time `json:"updated_at"`
	MediaCount int       `json:"media_count"`
	NewCount   int       `json:"new_count"`
	AuthError  bool      `json:"auth_error"`

	mediaCountCached   int       `json:"-"`
	mediaCountCachedAt time.Time `json:"-"`
}

// SSEEvent pushed to all connected SSE clients
type SSEEvent struct {
	Type     string `json:"type"` // "log", "status", "progress"
	Username string `json:"username"`
	Level    string `json:"level,omitempty"`
	Message  string `json:"message,omitempty"`
	Status   string `json:"status,omitempty"`
	Progress int    `json:"progress,omitempty"`
}

type Orchestrator struct {
	mu             sync.RWMutex
	progress       map[string]*TaskProgress
	cancels        map[string]context.CancelFunc
	LastSync       time.Time
	sseMu          sync.RWMutex
	sseClients     map[chan SSEEvent]struct{}
	autoSyncCtx    context.Context
	autoSyncCancel context.CancelFunc
	mediaIndex     *gallery.Index
}

var GlobalOrchestrator *Orchestrator

func InitOrchestrator(mediaIndex *gallery.Index) {
	autoSyncCtx, autoSyncCancel := context.WithCancel(context.Background())
	orch := &Orchestrator{
		progress:       make(map[string]*TaskProgress),
		cancels:        make(map[string]context.CancelFunc),
		sseClients:     make(map[chan SSEEvent]struct{}),
		LastSync:       time.Now(),
		autoSyncCtx:    autoSyncCtx,
		autoSyncCancel: autoSyncCancel,
		mediaIndex:     mediaIndex,
	}
	GlobalOrchestrator = orch

	slog.SetDefault(slog.New(&taskLogHandler{
		Handler: slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
		orch: orch,
	}))

	go orch.StartAutoSyncLoop(autoSyncCtx)
}

type taskLogHandler struct {
	slog.Handler
	orch *Orchestrator
}

func (h *taskLogHandler) Handle(ctx context.Context, r slog.Record) error {
	var username string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "user" {
			username = a.Value.String()
			return false
		}
		return true
	})
	if username != "" {
		h.orch.AppendTaskLog(username, time.Now(), r.Level.String(), r.Message)
	}
	return h.Handler.Handle(ctx, r)
}

// Subscribe registers a new SSE client channel and returns an unsubscribe function
func (o *Orchestrator) Subscribe() (chan SSEEvent, func()) {
	ch := make(chan SSEEvent, 256)
	o.sseMu.Lock()
	o.sseClients[ch] = struct{}{}
	o.sseMu.Unlock()
	return ch, func() {
		o.sseMu.Lock()
		delete(o.sseClients, ch)
		o.sseMu.Unlock()
		close(ch)
	}
}

func (o *Orchestrator) broadcast(evt SSEEvent) {
	o.sseMu.RLock()
	for ch := range o.sseClients {
		select {
		case ch <- evt:
		default:
			// Drop event if client buffer is full (slow consumer)
		}
	}
	o.sseMu.RUnlock()
}

func loadPersistedSyncInfo(username string) (string, time.Time) {
	c := config.GetConfig()
	for _, acc := range c.Accounts {
		if strings.EqualFold(acc.Username, username) {
			status := acc.LastSyncStatus
			if status == "" {
				status = "idle"
			}
			return status, acc.LastSyncTime
		}
	}
	return "idle", time.Time{}
}

// SavePersistedSyncInfo persists an account's sync status under the config lock
func SavePersistedSyncInfo(username, status string, updatedAt time.Time) {
	config.UpdateConfig(func(c *config.Config) {
		for i, acc := range c.Accounts {
			if strings.EqualFold(acc.Username, username) {
				c.Accounts[i].LastSyncStatus = status
				c.Accounts[i].LastSyncTime = updatedAt
				return
			}
		}
	})
}

func (o *Orchestrator) SyncTargets(accounts []config.Account) {
	o.mu.Lock()
	defer o.mu.Unlock()

	current := make(map[string]bool)
	for _, acc := range accounts {
		current[acc.Username] = true
		if _, exists := o.progress[acc.Username]; !exists {
			status, updatedAt := loadPersistedSyncInfo(acc.Username)
			// Stale "running" status from a previous instance — reset to idle
			if status == "running" {
				status = "idle"
				SavePersistedSyncInfo(acc.Username, "idle", updatedAt)
			}
			o.progress[acc.Username] = &TaskProgress{
				Username:  acc.Username,
				Platform:  acc.Platform,
				Status:    status,
				Progress:  0,
				Logs:      []TaskLog{},
				UpdatedAt: updatedAt,
			}
		} else {
			// Update platform if changed
			o.progress[acc.Username].Platform = acc.Platform
		}
	}

	// Persist final status of removed accounts before deleting from memory
	for k := range o.progress {
		if !current[k] {
			SavePersistedSyncInfo(k, o.progress[k].Status, o.progress[k].UpdatedAt)
			delete(o.progress, k)
		}
	}
}

func (o *Orchestrator) AppendTaskLog(username string, t time.Time, level, msg string) {
	o.mu.Lock()
	p, exists := o.progress[username]
	if !exists {
		o.mu.Unlock()
		return
	}

	p.Logs = append(p.Logs, TaskLog{
		Timestamp: t,
		Level:     level,
		Message:   msg,
	})

	// Trim to max size to prevent unbounded memory growth
	if len(p.Logs) > maxTaskLogs {
		p.Logs = p.Logs[len(p.Logs)-maxTaskLogs:]
	}

	p.UpdatedAt = time.Now()

	status := p.Status
	currentProgress := p.Progress
	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: username,
		Level:    level,
		Message:  msg,
	})

	// Estimate progress based on log message content
	if status == "running" {
		progress := currentProgress
		if progress < 10 {
			progress = 10
		}
		if strings.Contains(strings.ToLower(msg), "navigating") || strings.Contains(strings.ToLower(msg), "testing twitter mirror") || strings.Contains(strings.ToLower(msg), "instagram session is valid") {
			progress = 20
		} else if strings.Contains(strings.ToLower(msg), "timeline page parsed") || strings.Contains(strings.ToLower(msg), "instagram post count") || strings.Contains(strings.ToLower(msg), "instagram feed page parsed") || strings.Contains(strings.ToLower(msg), "resolved instagram user id") {
			progress = 50
		} else if strings.Contains(strings.ToLower(msg), "concurrent download workers") {
			progress = 70
		} else if strings.Contains(strings.ToLower(msg), "file downloaded") || strings.Contains(strings.ToLower(msg), "instagram file downloaded") {
			if progress < 95 {
				progress += 2
			}
		}
		if progress != currentProgress {
			o.mu.Lock()
			if p, exists := o.progress[username]; exists {
				p.Progress = progress
			}
			o.mu.Unlock()
		}
	}
}

func (o *Orchestrator) StartScrape(username string, platform string, saveText bool) {
	o.mu.Lock()
	p, exists := o.progress[username]
	if !exists {
		p = &TaskProgress{
			Username: username,
			Platform: platform,
			Status:   "idle",
			Logs:     []TaskLog{},
		}
		o.progress[username] = p
	}

	if p.Status == "running" {
		o.mu.Unlock()
		slog.Warn("Scraping task is already running for user", "user", username)
		return
	}

	if p.Status == "failed" || p.Status == "completed" {
		p.Status = "idle"
		p.Progress = 0
		p.NewCount = 0
	}

	lastSync := p.UpdatedAt
	forceFullTwitterSync := false
	if platform == "twitter" && saveText {
		postsPath := filepath.Join("downloads", "twitter", username, "posts.json")
		if _, err := os.Stat(postsPath); os.IsNotExist(err) {
			forceFullTwitterSync = true
			lastSync = time.Time{}
		}
	}
	forceFullInstagramSync := false
	if platform == "instagram" {
		if saveText {
			postsPath := filepath.Join("downloads", "instagram", username, "posts.json")
			if _, err := os.Stat(postsPath); os.IsNotExist(err) {
				forceFullInstagramSync = true
				lastSync = time.Time{}
			}
		}
	}

	p.Status = "running"
	p.Progress = 5
	p.Logs = []TaskLog{} // clear old logs
	p.AuthError = false
	persistUser := p.Username
	persistStatus := p.Status
	persistUpdated := p.UpdatedAt
	beforeCount := o.countDownloadedMedia(platform, username)
	o.mu.Unlock()

	if forceFullTwitterSync || forceFullInstagramSync {
		slog.Info("Posts.json not found for target, forcing full sync", "user", username)
	}

	o.broadcast(SSEEvent{Type: "status", Username: username, Status: "running", Progress: 5})

	SavePersistedSyncInfo(persistUser, persistStatus, persistUpdated)
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Hour)
		defer timeoutCancel()

		o.mu.Lock()
		o.cancels[username] = cancel
		o.mu.Unlock()

		var err error
		var scrapeFn func(ctx context.Context, username string, saveText bool, lastSync time.Time) error
		switch platform {
		case "instagram":
			scrapeFn = func(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
				return scraper.ScrapeInstagramUser(ctx, username, saveText, lastSync)
			}
		case "twitter":
			scrapeFn = func(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
				c := config.GetConfig()
				var skipRetweets bool
				var filters []string
				downloadPhotos := true
				downloadVideos := true
				for _, acc := range c.Accounts {
					if strings.EqualFold(acc.Username, username) {
						skipRetweets = acc.SkipRetweets
						filters = acc.Filters
						downloadPhotos = acc.ShouldDownloadPhotos()
						downloadVideos = acc.ShouldDownloadVideos()
						break
					}
				}
				return scraper.ScrapeTwitterUser(ctx, username, saveText, skipRetweets, filters, downloadPhotos, downloadVideos, lastSync)
			}
		default:
			err = fmt.Errorf("unknown platform: %s", platform)
		}
		if err == nil {
			err = scrapeFn(timeoutCtx, username, saveText, lastSync)
		}
		o.mu.Lock()
		delete(o.cancels, username)
		if err != nil {
			if ctx.Err() != nil {
				p.Status = "idle"
				p.Progress = 0
			} else {
				p.Status = "failed"
				p.Progress = 100
			}
			p.AuthError = errors.Is(err, scraper.ErrAuthExpired)
		} else {
			p.Status = "completed"
			p.Progress = 100
			p.AuthError = false
		}
		p.UpdatedAt = time.Now()
		p.mediaCountCachedAt = time.Time{} // force recount on next poll
		if o.mediaIndex != nil {
			o.mediaIndex.Invalidate(platform, username)
		}
		afterCount := o.countDownloadedMedia(platform, username)
		newCount := afterCount - beforeCount
		if newCount < 0 {
			newCount = 0
		}
		p.NewCount = newCount
		p.mediaCountCached = afterCount
		p.mediaCountCachedAt = time.Now()
		SavePersistedSyncInfo(p.Username, p.Status, p.UpdatedAt)
		o.mu.Unlock()

		o.broadcast(SSEEvent{Type: "status", Username: username, Status: p.Status, Progress: p.Progress})

		if err != nil {
			if ctx.Err() != nil {
				o.AppendTaskLog(username, time.Now(), "ERROR", "Scraping task cancelled by user.")
			} else {
				o.AppendTaskLog(username, time.Now(), "ERROR", fmt.Sprintf("Scraping task aborted with error: %v", err))
			}
		} else {
			o.AppendTaskLog(username, time.Now(), "INFO", "Scraping completed successfully.")
		}
	}()
}

// CancelAll cancels all running scrape tasks
func (o *Orchestrator) CancelAll() {
	o.mu.Lock()
	for username, cancel := range o.cancels {
		delete(o.cancels, username)
		cancel()
		slog.Debug("Cancelled scrape task", "user", username)
	}
	o.mu.Unlock()
}

// CancelScrape cancels a single scrape task by username
func (o *Orchestrator) CancelScrape(username string) bool {
	o.mu.Lock()
	cancel, ok := o.cancels[username]
	if ok {
		delete(o.cancels, username)
	}
	o.mu.Unlock()
	if !ok || cancel == nil {
		return false
	}
	cancel()
	slog.Info("Cancel signal sent for scraping task", "user", username)
	return true
}

// Shutdown stops the auto-sync loop and cancels all running tasks
func (o *Orchestrator) Shutdown() {
	slog.Info("Shutting down orchestrator")
	o.autoSyncCancel()
	o.CancelAll()
	slog.Info("Orchestrator shut down")
}

func (o *Orchestrator) LastSyncTime() time.Time {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.LastSync
}

func (o *Orchestrator) GetAllProgress(accounts []config.Account) []TaskProgress {
	o.mu.RLock()
	snapshot := make([]*TaskProgress, 0, len(accounts))
	for _, acc := range accounts {
		if v, exists := o.progress[acc.Username]; exists {
			snapshot = append(snapshot, v)
		}
	}
	o.mu.RUnlock()

	const mediaCountTTL = 30 * time.Second

	result := make([]TaskProgress, 0, len(snapshot))
	for _, v := range snapshot {
		entry := *v

		now := time.Now()
		if v.mediaCountCachedAt.IsZero() || now.Sub(v.mediaCountCachedAt) > mediaCountTTL {
			count := o.countDownloadedMedia(entry.Platform, entry.Username)
			o.mu.Lock()
			v.mediaCountCached = count
			v.mediaCountCachedAt = now
			o.mu.Unlock()
			entry.MediaCount = count
		} else {
			entry.MediaCount = v.mediaCountCached
		}
		result = append(result, entry)
	}
	return result
}

func (o *Orchestrator) countDownloadedMedia(platform, username string) int {
	if o.mediaIndex != nil {
		return o.mediaIndex.Count(platform, username)
	}
	dir := filepath.Join("downloads", platform, username)
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, f := range files {
		if !f.IsDir() && f.Name() != "posts.json" && f.Name() != ".DS_Store" {
			count++
		}
	}
	return count
}

func (o *Orchestrator) StartAutoSyncLoop(ctx context.Context) {
	slog.Info("Starting auto-sync background worker loop")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Initialize LastSync to now so we don't trigger immediately on startup
	o.mu.Lock()
	o.LastSync = time.Now()
	o.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c := config.GetConfig()
			interval := c.AutoSyncInterval
			if interval <= 0 {
				continue
			}

			o.mu.Lock()
			timeSinceLastSync := time.Since(o.LastSync)
			o.mu.Unlock()

			if timeSinceLastSync >= time.Duration(interval)*time.Hour {
				slog.Info("Auto-sync interval reached, triggering sync for all accounts", "interval_hours", interval)
				// Skip accounts that are currently running to avoid spam
				o.mu.RLock()
				runningSet := make(map[string]bool)
				for _, p := range o.progress {
					if p.Status == "running" {
						runningSet[p.Username] = true
					}
				}
				o.mu.RUnlock()

				for _, acc := range c.Accounts {
					if runningSet[acc.Username] {
						slog.Info("Skipping auto-sync, task already running", "user", acc.Username)
						continue
					}
					o.StartScrape(acc.Username, acc.Platform, acc.SaveText)
					// Stagger launches so we don't hammer every platform at once
					time.Sleep(10 * time.Second)
				}
				o.mu.Lock()
				o.LastSync = time.Now()
				o.mu.Unlock()
			}
		}
	}
}
