package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmaxmax/go-sse"

	"github.com/robfig/cron/v3"

	"idolhub/internal/config"
	"idolhub/internal/gallery"
	"idolhub/internal/scraper"
)

const numScrapeWorkers = 3

type scrapeJob struct {
	username      string
	platform      string
	saveText      bool
	lastSync      time.Time
	forceFullSync bool
	beforeCount   int
}

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
	globalLogs     []TaskLog
	cancels        map[string]context.CancelFunc
	LastSync       time.Time
	sseServer      *sse.Server
	autoSyncCtx    context.Context
	autoSyncCancel context.CancelFunc
	mediaIndex     *gallery.Index
	jobCh          chan scrapeJob
}

var GlobalOrchestrator *Orchestrator

func InitOrchestrator(mediaIndex *gallery.Index) {
	autoSyncCtx, autoSyncCancel := context.WithCancel(context.Background())
	sseServer := &sse.Server{
		OnSession: func(w http.ResponseWriter, r *http.Request) ([]string, bool) {
			_, _ = fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return nil, true
		},
	}
	orch := &Orchestrator{
		progress:       make(map[string]*TaskProgress),
		globalLogs:     make([]TaskLog, 0, 100),
		cancels:        make(map[string]context.CancelFunc),
		sseServer:      sseServer,
		LastSync:       time.Now(),
		autoSyncCtx:    autoSyncCtx,
		autoSyncCancel: autoSyncCancel,
		mediaIndex:     mediaIndex,
		jobCh:          make(chan scrapeJob, 100),
	}
	GlobalOrchestrator = orch

	slog.SetDefault(slog.New(&taskLogHandler{
		Handler: slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
		orch: orch,
	}))

	for i := 0; i < numScrapeWorkers; i++ {
		go orch.worker()
	}

	scraper.EnsureYTDLP(context.Background())
	scraper.StartYTDLPUpdateLoop(autoSyncCtx)
	go orch.StartAutoSyncLoop(autoSyncCtx)
}

type taskLogHandler struct {
	slog.Handler
	orch *Orchestrator
}

func (h *taskLogHandler) Handle(ctx context.Context, r slog.Record) error {
	var targetUser string
	var attrs []string

	r.Attrs(func(a slog.Attr) bool {
		k := a.Key
		if k == "user" || k == "username" || k == "target" {
			targetUser = a.Value.String()
			return true
		}
		val := a.Value.Any()
		if val != nil && val != "" {
			attrs = append(attrs, fmt.Sprintf("%s=%v", k, val))
		}
		return true
	})

	formattedMsg := r.Message
	if len(attrs) > 0 {
		formattedMsg = fmt.Sprintf("%s (%s)", formattedMsg, strings.Join(attrs, ", "))
	}

	if targetUser != "" {
		h.orch.AppendTaskLog(targetUser, time.Now(), r.Level.String(), formattedMsg)
	} else {
		h.orch.AppendGlobalLog(time.Now(), r.Level.String(), formattedMsg)
	}

	return h.Handler.Handle(ctx, r)
}

func (o *Orchestrator) AppendGlobalLog(t time.Time, level, msg string) {
	o.mu.Lock()
	displayMsg := msg
	if !strings.HasPrefix(displayMsg, "[SYSTEM]") && !strings.HasPrefix(displayMsg, "[@") {
		displayMsg = "[SYSTEM] " + displayMsg
	}
	o.globalLogs = append(o.globalLogs, TaskLog{
		Timestamp: t,
		Level:     level,
		Message:   displayMsg,
	})
	if len(o.globalLogs) > maxTaskLogs {
		o.globalLogs = o.globalLogs[len(o.globalLogs)-maxTaskLogs:]
	}
	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: "system",
		Level:    level,
		Message:  msg,
	})
}

// Subscribe is unused; SSE is served directly via sseServer.
func (o *Orchestrator) SSEHandler() http.Handler {
	return o.sseServer
}

func (o *Orchestrator) broadcast(evt SSEEvent) {
	if o.sseServer == nil {
		return
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	msg := &sse.Message{}
	msg.AppendData(string(data))
	_ = o.sseServer.Publish(msg)
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
			// Stale "running" or "queued" status from a previous instance — reset to idle
			if status == "running" || status == "queued" {
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
	if exists {
		p.Logs = append(p.Logs, TaskLog{
			Timestamp: t,
			Level:     level,
			Message:   msg,
		})
		if len(p.Logs) > maxTaskLogs {
			p.Logs = p.Logs[len(p.Logs)-maxTaskLogs:]
		}
		p.UpdatedAt = time.Now()
	}

	o.globalLogs = append(o.globalLogs, TaskLog{
		Timestamp: t,
		Level:     level,
		Message:   fmt.Sprintf("[@%s] %s", username, msg),
	})
	if len(o.globalLogs) > maxTaskLogs {
		o.globalLogs = o.globalLogs[len(o.globalLogs)-maxTaskLogs:]
	}

	status := ""
	currentProgress := 0
	if exists {
		status = p.Status
		currentProgress = p.Progress
	}
	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: username,
		Level:    level,
		Message:  msg,
	})

	// Estimate progress based on log message content
	if exists && (status == "running" || status == "queued") {
		progress := currentProgress
		if progress < 10 {
			progress = 10
		}
		ml := strings.ToLower(msg)
		if strings.Contains(ml, "navigating") || strings.Contains(ml, "testing twitter mirror") || strings.Contains(ml, "session is valid") || strings.Contains(ml, "yt-dlp using cookies") {
			progress = 20
		} else if strings.Contains(ml, "timeline page parsed") || strings.Contains(ml, "instagram post count") || strings.Contains(ml, "instagram feed page parsed") || strings.Contains(ml, "resolved instagram user id") || strings.Contains(ml, "downloading video stream") || strings.Contains(ml, "yt-dlp progress") {
			progress = 50
		} else if strings.Contains(ml, "concurrent download workers") {
			progress = 70
		} else if strings.Contains(ml, "file downloaded") || strings.Contains(ml, "instagram file downloaded") || strings.Contains(ml, "twitter image downloaded") || strings.Contains(ml, "twitter video downloaded") || strings.Contains(ml, "video scraping completed") {
			progress = 90
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

func (o *Orchestrator) GetGlobalLogs() []TaskLog {
	o.mu.RLock()
	defer o.mu.RUnlock()
	copied := make([]TaskLog, len(o.globalLogs))
	copy(copied, o.globalLogs)
	return copied
}

func (o *Orchestrator) StartScrape(username string, platform string, saveText bool, forceFull bool) {
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

	if p.Status == "running" || p.Status == "queued" {
		o.mu.Unlock()
		slog.Warn("Scraping task already running or queued", "user", username)
		return
	}

	if p.Status == "failed" || p.Status == "completed" {
		p.Status = "idle"
		p.Progress = 0
		p.NewCount = 0
	}

	lastSync := p.UpdatedAt
	forceFullSync := forceFull
	if forceFull {
		lastSync = time.Time{}
	} else if saveText || platform == "tiktok" {
		postsPath := filepath.Join("downloads", platform, username, "posts.json")
		if _, err := os.Stat(postsPath); os.IsNotExist(err) {
			forceFullSync = true
			lastSync = time.Time{}
		} else if isPostsCorrupted(postsPath) {
			forceFullSync = true
			lastSync = time.Time{}
		}
	}

	beforeCount := o.countDownloadedMedia(platform, username)
	p.Status = "queued"
	p.Logs = []TaskLog{}
	p.AuthError = false
	o.mu.Unlock()

	if forceFullSync {
		slog.Info("Forcing full sync for target", "user", username)
	}

	o.jobCh <- scrapeJob{
		username:      username,
		platform:      platform,
		saveText:      saveText,
		lastSync:      lastSync,
		forceFullSync: forceFullSync,
		beforeCount:   beforeCount,
	}

	o.broadcast(SSEEvent{Type: "status", Username: username, Status: "queued", Progress: 0})
	SavePersistedSyncInfo(username, "queued", p.UpdatedAt)
}

func (o *Orchestrator) worker() {
	for job := range o.jobCh {
		o.mu.RLock()
		p, ok := o.progress[job.username]
		skip := ok && p.Status != "queued"
		o.mu.RUnlock()
		if skip {
			continue
		}
		o.runScrape(job)
	}
}

func (o *Orchestrator) runScrape(job scrapeJob) {
	username := job.username
	platform := job.platform
	saveText := job.saveText
	lastSync := job.lastSync
	beforeCount := job.beforeCount

	o.mu.Lock()
	p := o.progress[username]
	p.Status = "running"
	p.Progress = 5
	persistUser := p.Username
	persistStatus := p.Status
	persistUpdated := p.UpdatedAt
	o.mu.Unlock()

	slog.Info("Starting media scrape worker", "user", username, "platform", platform)
	o.broadcast(SSEEvent{Type: "status", Username: username, Status: "running", Progress: 5})
	SavePersistedSyncInfo(persistUser, persistStatus, persistUpdated)

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
	case "tiktok":
		scrapeFn = func(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
			return scraper.ScrapeYTDLP(ctx, platform, username, saveText, lastSync)
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
		p.UpdatedAt = time.Now()
	}
	p.mediaCountCachedAt = time.Time{}
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
			slog.Info("Scraping task cancelled by user", "user", username, "platform", platform)
		} else {
			slog.Error("Scraping task aborted with error", "user", username, "platform", platform, "error", err)
		}
	} else {
		slog.Info("Scraping completed successfully", "user", username, "platform", platform, "new_files", newCount, "total_files", afterCount)
	}
}

// CancelAll cancels all running and queued scrape tasks.
func (o *Orchestrator) CancelAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for username, cancel := range o.cancels {
		delete(o.cancels, username)
		cancel()
	}
	for {
		select {
		case job := <-o.jobCh:
			if p, ok := o.progress[job.username]; ok && p.Status == "queued" {
				p.Status = "idle"
				p.Progress = 0
			}
		default:
			return
		}
	}
}

// CancelScrape cancels a single scrape task by username, running or queued.
func (o *Orchestrator) CancelScrape(username string) bool {
	o.mu.Lock()
	cancel, ok := o.cancels[username]
	if ok {
		delete(o.cancels, username)
	}
	if p, exists := o.progress[username]; exists && p.Status == "queued" {
		p.Status = "idle"
		p.Progress = 0
		SavePersistedSyncInfo(username, "idle", time.Now())
		o.mu.Unlock()
		o.broadcast(SSEEvent{Type: "status", Username: username, Status: "idle", Progress: 0})
		return true
	}
	o.mu.Unlock()
	if !ok || cancel == nil {
		return false
	}
	cancel()
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
	slog.Info("Starting auto-sync scheduler")

	// Initialize LastSync to now so we don't trigger immediately on startup
	o.mu.Lock()
	o.LastSync = time.Now()
	o.mu.Unlock()

	for {
		interval := config.GetConfig().AutoSyncInterval
		if interval <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				continue
			}
		}

		wait := time.Until(cron.Every(time.Duration(interval) * time.Hour).Next(time.Now()))
		if wait < 0 {
			wait = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		c := config.GetConfig()
		slog.Info("Auto-sync interval reached, triggering sync for all accounts", "interval_hours", interval)
		// Skip accounts that are currently running or queued to avoid spam
		o.mu.RLock()
		runningSet := make(map[string]bool)
		for _, p := range o.progress {
			if p.Status == "running" || p.Status == "queued" {
				runningSet[p.Username] = true
			}
		}
		o.mu.RUnlock()

		for _, acc := range c.Accounts {
			if runningSet[acc.Username] {
				slog.Debug("Skipping auto-sync, task already running or queued", "user", acc.Username)
				continue
			}
			o.StartScrape(acc.Username, acc.Platform, acc.SaveText, false)
			time.Sleep(10 * time.Second)
		}
		o.mu.Lock()
		o.LastSync = time.Now()
		o.mu.Unlock()
	}
}

// isPostsCorrupted returns true when posts.json has ≤5 entries (old overwrite-bug ate the rest).
func isPostsCorrupted(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return false
	}
	// 5 is arbitrary — accounts with ≤5 real posts also resync, harmless since media already exists.
	return len(entries) <= 5
}
