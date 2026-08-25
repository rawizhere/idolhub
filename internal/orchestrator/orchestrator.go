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
	"idolhub/internal/store"
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
	accounts       *store.AccountStore
	posts          *store.PostStore
	jobCh          chan scrapeJob
}

var GlobalOrchestrator *Orchestrator

func InitOrchestrator(mediaIndex *gallery.Index, st *store.Store) {
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
		accounts:       st.Accounts,
		posts:          st.Posts,
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

func pushLog(logs []TaskLog, t time.Time, level, msg string) []TaskLog {
	logs = append(logs, TaskLog{Timestamp: t, Level: level, Message: msg})
	if len(logs) > maxTaskLogs {
		return logs[len(logs)-maxTaskLogs:]
	}
	return logs
}

func (o *Orchestrator) AppendGlobalLog(t time.Time, level, msg string) {
	o.mu.Lock()
	displayMsg := msg
	if !strings.HasPrefix(displayMsg, "[SYSTEM]") && !strings.HasPrefix(displayMsg, "[@") {
		displayMsg = "[SYSTEM] " + displayMsg
	}
	o.globalLogs = pushLog(o.globalLogs, t, level, displayMsg)
	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: "system",
		Level:    level,
		Message:  msg,
	})
}

// SSEHandler serves the SSE stream directly via sseServer.
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

func (o *Orchestrator) loadPersistedSyncInfo(platform, username string) (string, time.Time) {
	if o.accounts == nil {
		return "idle", time.Time{}
	}
	info, err := o.accounts.GetSyncInfo(context.Background(), platform, username)
	if err != nil {
		slog.Warn("Failed to load sync info", "user", username, "error", err)
		return "idle", time.Time{}
	}
	if info.Status == "" {
		info.Status = "idle"
	}
	return info.Status, info.Time
}

// SavePersistedSyncInfo persists an account's sync status in the store.
func (o *Orchestrator) SavePersistedSyncInfo(platform, username, status string, updatedAt time.Time) {
	if o.accounts == nil {
		return
	}
	if err := o.accounts.SetSyncInfo(context.Background(), platform, username, status, updatedAt); err != nil {
		slog.Warn("Failed to persist sync info", "user", username, "error", err)
	}
}

func (o *Orchestrator) SyncTargets(accounts []config.Account) {
	o.mu.Lock()
	defer o.mu.Unlock()

	current := make(map[string]bool)
	for _, acc := range accounts {
		current[acc.Username] = true
		if _, exists := o.progress[acc.Username]; !exists {
			status, updatedAt := o.loadPersistedSyncInfo(acc.Platform, acc.Username)
			// Stale "running" or "queued" status from a previous instance — reset to idle
			if status == "running" || status == "queued" {
				status = "idle"
				o.SavePersistedSyncInfo(acc.Platform, acc.Username, "idle", updatedAt)
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
			o.SavePersistedSyncInfo(o.progress[k].Platform, k, o.progress[k].Status, o.progress[k].UpdatedAt)
			delete(o.progress, k)
		}
	}
}

func (o *Orchestrator) AppendTaskLog(username string, t time.Time, level, msg string) {
	o.mu.Lock()
	p, exists := o.progress[username]
	if exists {
		p.Logs = pushLog(p.Logs, t, level, msg)
		p.UpdatedAt = time.Now()
	}

	o.globalLogs = pushLog(o.globalLogs, t, level, fmt.Sprintf("[@%s] %s", username, msg))

	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: username,
		Level:    level,
		Message:  msg,
	})
}

func (o *Orchestrator) setProgress(username string, pct int) {
	o.mu.Lock()
	if p, exists := o.progress[username]; exists && (p.Status == "running" || p.Status == "queued") {
		p.Progress = pct
	}
	o.mu.Unlock()
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
	if !forceFullSync && (saveText || platform == "tiktok") {
		storedCount := 0
		if o.posts != nil {
			count, err := o.posts.CountByAccount(context.Background(), platform, username)
			if err != nil {
				slog.Warn("Failed to count stored posts", "user", username, "error", err)
			}
			storedCount = count
		}
		if storedCount == 0 {
			forceFullSync = true
			lastSync = time.Time{}
		}
	}

	beforeCount := o.countDownloadedMedia(platform, username)
	p.Status = "queued"
	p.Logs = []TaskLog{}
	p.AuthError = false
	queuedAt := p.UpdatedAt
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
	o.SavePersistedSyncInfo(platform, username, "queued", queuedAt)
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
	if p == nil {
		o.mu.Unlock()
		slog.Warn("Skipping scrape for removed target", "user", username)
		return
	}
	p.Status = "running"
	p.Progress = 5
	persistUser := p.Username
	persistStatus := p.Status
	persistUpdated := p.UpdatedAt
	o.mu.Unlock()

	slog.Info("Starting media scrape worker", "user", username, "platform", platform)
	o.broadcast(SSEEvent{Type: "status", Username: username, Status: "running", Progress: 5})
	o.SavePersistedSyncInfo(platform, persistUser, persistStatus, persistUpdated)

	ctx, cancel := context.WithCancel(context.Background())
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Hour)
	defer timeoutCancel()

	o.mu.Lock()
	o.cancels[username] = cancel
	o.mu.Unlock()

	target := scraper.Target{
		Username: username,
		Platform: platform,
		SaveText: saveText,
	}
	var err error
	opts := scraper.Options{
		LastSync:  lastSync,
		ForceFull: job.forceFullSync,
		OnProgress: func(pct int, _ string) {
			o.setProgress(username, pct)
		},
	}

	c := config.GetConfig()
	for _, acc := range c.Accounts {
		if strings.EqualFold(acc.Username, username) {
			target.SkipRetweets = acc.SkipRetweets
			target.Filters = acc.Filters
			dp := acc.ShouldDownloadPhotos()
			dv := acc.ShouldDownloadVideos()
			target.DownloadPhotos = &dp
			target.DownloadVideos = &dv
			break
		}
	}

	opts.TwitterAuthToken = c.TwitterAuthToken
	opts.InstagramSessionID = c.InstagramSessionID
	opts.TikTokCookies = c.TikTokCookies
	opts.Posts = o.posts

	s, ok := scraper.Get(platform)
	if !ok {
		err = fmt.Errorf("unknown platform: %s", platform)
	} else {
		err = s.Scrape(timeoutCtx, target, opts)
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
	o.SavePersistedSyncInfo(platform, p.Username, p.Status, p.UpdatedAt)
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
		o.SavePersistedSyncInfo(p.Platform, username, "idle", time.Now())
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
	const mediaCountTTL = 30 * time.Second

	now := time.Now()
	o.mu.RLock()
	sources := make([]*TaskProgress, 0, len(accounts))
	result := make([]TaskProgress, 0, len(accounts))
	for _, acc := range accounts {
		v, exists := o.progress[acc.Username]
		sources = append(sources, v)
		entry := TaskProgress{}
		if exists {
			// Copy under read lock to avoid racing concurrent writers.
			entry = *v
			entry.MediaCount = v.mediaCountCached
		}
		result = append(result, entry)
	}
	o.mu.RUnlock()

	for i, v := range sources {
		if v == nil {
			continue
		}
		if !result[i].mediaCountCachedAt.IsZero() && now.Sub(result[i].mediaCountCachedAt) <= mediaCountTTL {
			continue
		}
		count := o.countDownloadedMedia(result[i].Platform, result[i].Username)
		o.mu.Lock()
		v.mediaCountCached = count
		v.mediaCountCachedAt = now
		o.mu.Unlock()
		result[i].MediaCount = count
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

	o.mu.Lock()
	o.LastSync = time.Now()
	o.mu.Unlock()

	for {
		interval := config.GetConfig().AutoSyncInterval
		wait := time.Minute
		if interval > 0 {
			wait = time.Until(cron.Every(time.Duration(interval) * time.Hour).Next(time.Now()))
			if wait <= 0 {
				wait = time.Minute
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if interval <= 0 {
			continue
		}

		c := config.GetConfig()
		slog.Info("Auto-sync interval reached, triggering sync for all accounts", "interval_hours", interval)
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
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		o.mu.Lock()
		o.LastSync = time.Now()
		o.mu.Unlock()
	}
}
