package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"idolhub/internal/config"
	"idolhub/internal/gallery"
	"idolhub/internal/scraper"
	"idolhub/internal/store"
)

// maxScrapeWorkers is the worker pool size; the configured concurrency (config.Concurrency) gates how many of them scrape at once.
const maxScrapeWorkers = 8

type scrapeJob struct {
	username      string
	platform      string
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
	Paused     bool      `json:"paused"`

	mediaCountCached   int
	mediaCountCachedAt time.Time
}

// SSEEvent pushed to all connected SSE clients
type SSEEvent struct {
	Type     string `json:"type"` // "log", "status", "progress"
	Username string `json:"username"`
	Platform string `json:"platform,omitempty"`
	Level    string `json:"level,omitempty"`
	Message  string `json:"message,omitempty"`
	Status   string `json:"status,omitempty"`
	Progress int    `json:"progress,omitempty"`
}

type Orchestrator struct {
	mu sync.RWMutex
	// progress and cancels are keyed by taskKey: the same handle on two platforms is two independent targets with their own watermark.
	progress       map[string]*TaskProgress
	globalLogs     []TaskLog
	cancels        map[string]context.CancelFunc
	lastSync       time.Time
	events         *sseHub
	autoSyncCtx    context.Context
	autoSyncCancel context.CancelFunc
	mediaIndex     *gallery.Index
	accounts       *store.AccountStore
	posts          *store.PostStore
	settings       *store.SettingsStore
	// fullResync holds task keys whose next scrape must be a full one (media was quarantined after their watermark).
	fullResync map[string]bool
	// blockUntil is the per-platform circuit breaker: no scrapes of that platform before the time.
	blockUntil map[string]time.Time
	twitterMu  sync.Mutex
	jobCh      chan scrapeJob
	logEvents  chan logEvent
	runs       *store.RunStore

	slotMu   sync.Mutex
	slotCond *sync.Cond
	active   int
}

type logEvent struct {
	user     string
	platform string
	t        time.Time
	level    string
	msg      string
}

// taskKey identifies one scrape target.
func taskKey(platform, username string) string {
	return platform + "/" + strings.ToLower(username)
}

// New builds an orchestrator; Start runs its workers and schedulers.
func New(mediaIndex *gallery.Index, st *store.Store) *Orchestrator {
	autoSyncCtx, autoSyncCancel := context.WithCancel(context.Background())
	orch := &Orchestrator{
		progress:       make(map[string]*TaskProgress),
		globalLogs:     make([]TaskLog, 0, 100),
		cancels:        make(map[string]context.CancelFunc),
		events:         newSSEHub(),
		lastSync:       time.Now(),
		autoSyncCtx:    autoSyncCtx,
		autoSyncCancel: autoSyncCancel,
		mediaIndex:     mediaIndex,
		accounts:       st.Accounts,
		posts:          st.Posts,
		settings:       st.Settings,
		fullResync:     loadFullResync(st.Settings),
		blockUntil:     make(map[string]time.Time),
		jobCh:          make(chan scrapeJob, 100),
		logEvents:      make(chan logEvent, 4096),
		runs:           st.Runs,
	}
	orch.slotCond = sync.NewCond(&orch.slotMu)
	return orch
}

// Start routes the process logs into the task logs and starts the workers and the auto-sync loop.
func (o *Orchestrator) Start() {
	go func() {
		for evt := range o.logEvents {
			o.dispatch(evt)
		}
	}()

	slog.SetDefault(slog.New(&taskLogHandler{
		Handler: slog.NewJSONHandler(os.Stderr, nil),
		orch:    o,
	}))

	for range maxScrapeWorkers {
		go o.worker()
	}

	scraper.EnsureYTDLP(context.Background())
	scraper.StartYTDLPUpdateLoop(o.autoSyncCtx)
	go o.StartAutoSyncLoop(o.autoSyncCtx)
}

type taskLogHandler struct {
	slog.Handler
	orch *Orchestrator
}

func (h *taskLogHandler) Handle(ctx context.Context, r slog.Record) error {
	var targetUser, targetPlatform string
	var attrs []string

	r.Attrs(func(a slog.Attr) bool {
		k := a.Key
		if k == "user" || k == "username" || k == "target" {
			targetUser = a.Value.String()
			return true
		}
		if k == "platform" {
			targetPlatform = a.Value.String()
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

	select {
	case h.orch.logEvents <- logEvent{user: targetUser, platform: targetPlatform, t: time.Now(), level: r.Level.String(), msg: formattedMsg}:
	default:
	}

	return h.Handler.Handle(ctx, r)
}

func (o *Orchestrator) dispatch(evt logEvent) {
	if evt.user != "" {
		o.AppendTaskLog(evt.platform, evt.user, evt.t, evt.level, evt.msg)
	} else {
		o.AppendGlobalLog(evt.t, evt.level, evt.msg)
	}
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

// SSEHandler serves the dashboard event stream.
func (o *Orchestrator) SSEHandler() http.Handler {
	return o.events
}

// ShutdownSSE ends every open event stream so the HTTP server can drain.
func (o *Orchestrator) ShutdownSSE() {
	o.events.close()
}

func (o *Orchestrator) broadcast(evt SSEEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	o.events.publish(data)
}

func (o *Orchestrator) loadPersistedSyncInfo(platform, username string) (string, time.Time) {
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
	if err := o.accounts.SetSyncInfo(context.Background(), platform, username, status, updatedAt); err != nil {
		slog.Warn("Failed to persist sync info", "user", username, "error", err)
	}
}

func (o *Orchestrator) SyncTargets(accounts []config.Account) {
	o.mu.Lock()
	defer o.mu.Unlock()

	current := make(map[string]bool)
	for _, acc := range accounts {
		key := taskKey(acc.Platform, acc.Username)
		current[key] = true
		if _, exists := o.progress[key]; !exists {
			status, updatedAt := o.loadPersistedSyncInfo(acc.Platform, acc.Username)
			// Stale "running" or "queued" status from a previous instance; reset to idle
			if status == "running" || status == "queued" {
				status = "idle"
				o.SavePersistedSyncInfo(acc.Platform, acc.Username, "idle", updatedAt)
			}
			o.progress[key] = &TaskProgress{
				Username:  acc.Username,
				Platform:  acc.Platform,
				Status:    status,
				Progress:  0,
				Logs:      []TaskLog{},
				UpdatedAt: updatedAt,
			}
		}
	}

	// Removed accounts: their rows are already gone from the store, persisting status here would only recreate them.
	for k := range o.progress {
		if !current[k] {
			delete(o.progress, k)
		}
	}
}

// AppendTaskLog appends a log line to a target. Without a platform the line goes to the active target of that username (or every one of them when none is active).
func (o *Orchestrator) AppendTaskLog(platform, username string, t time.Time, level, msg string) {
	o.mu.Lock()
	for _, p := range o.logTargetsLocked(platform, username) {
		p.Logs = pushLog(p.Logs, t, level, msg)
		// don't clobber the last_sync watermark after the task stops running
		if p.Status == "running" {
			p.UpdatedAt = time.Now()
		}
		if platform == "" {
			platform = p.Platform
		}
	}

	o.globalLogs = pushLog(o.globalLogs, t, level, fmt.Sprintf("[@%s] %s", username, msg))

	o.mu.Unlock()

	o.broadcast(SSEEvent{
		Type:     "log",
		Username: username,
		Platform: platform,
		Level:    level,
		Message:  msg,
	})
}

func (o *Orchestrator) logTargetsLocked(platform, username string) []*TaskProgress {
	if platform != "" {
		if p, ok := o.progress[taskKey(platform, username)]; ok {
			return []*TaskProgress{p}
		}
		return nil
	}
	var all, active []*TaskProgress
	for _, p := range o.progress {
		if !strings.EqualFold(p.Username, username) {
			continue
		}
		all = append(all, p)
		if p.Status == "running" || p.Status == "queued" {
			active = append(active, p)
		}
	}
	if len(active) > 0 {
		return active
	}
	return all
}

// setProgress records a running target's percentage and pushes changes to the dashboard.
func (o *Orchestrator) setProgress(key string, pct int) {
	o.mu.Lock()
	p, exists := o.progress[key]
	changed := exists && (p.Status == "running" || p.Status == "queued") && p.Progress != pct
	var evt SSEEvent
	if changed {
		p.Progress = pct
		evt = SSEEvent{Type: "progress", Username: p.Username, Platform: p.Platform, Status: p.Status, Progress: pct}
	}
	o.mu.Unlock()
	if changed {
		o.broadcast(evt)
	}
}

func (o *Orchestrator) GetGlobalLogs() []TaskLog {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return slices.Clone(o.globalLogs)
}

// account finds a configured target.
func account(platform, username string) (config.Account, bool) {
	for _, acc := range config.GetConfig().Accounts {
		if acc.Platform == platform && strings.EqualFold(acc.Username, username) {
			return acc, true
		}
	}
	return config.Account{}, false
}

func (o *Orchestrator) StartScrape(username, platform string, forceFull bool) {
	key := taskKey(platform, username)
	o.mu.Lock()
	p, exists := o.progress[key]
	if !exists {
		p = &TaskProgress{
			Username: username,
			Platform: platform,
			Status:   "idle",
			Logs:     []TaskLog{},
		}
		o.progress[key] = p
	}

	if p.Status == "running" || p.Status == "queued" {
		o.mu.Unlock()
		slog.Warn("Scraping task already running or queued", "user", username, "platform", platform)
		return
	}

	if blockedUntil := o.blockUntil[platform]; time.Now().Before(blockedUntil) {
		o.mu.Unlock()
		slog.Warn("Skipping scrape, session is flagged (circuit breaker); save new session settings to reset it", "user", username, "platform", platform, "resume_after", blockedUntil.Format(time.RFC3339))
		return
	}

	if o.fullResync[key] && !forceFull {
		forceFull = true
		slog.Info("Full resync pending for target (media was re-queued after an integrity check)", "user", username, "platform", platform)
	}

	if p.Status == "failed" || p.Status == "completed" {
		p.Status = "idle"
		p.Progress = 0
		p.NewCount = 0
	}

	// a target that keeps post text but has none stored yet needs a full pass to backfill it
	if acc, _ := account(platform, username); !forceFull && (acc.SaveText || platform == "tiktok") {
		count, err := o.posts.CountByAccount(context.Background(), platform, username)
		if err != nil {
			slog.Warn("Failed to count stored posts", "user", username, "error", err)
		}
		forceFull = count == 0
	}
	lastSync := p.UpdatedAt
	if forceFull {
		lastSync = time.Time{}
	}

	beforeCount := o.mediaIndex.Count(platform, username)
	p.Status = "queued"
	p.Logs = []TaskLog{}
	p.AuthError = false
	queuedAt := p.UpdatedAt
	o.mu.Unlock()

	if forceFull {
		slog.Info("Forcing full sync for target", "user", username)
	}

	o.jobCh <- scrapeJob{
		username:      username,
		platform:      platform,
		lastSync:      lastSync,
		forceFullSync: forceFull,
		beforeCount:   beforeCount,
	}

	o.broadcast(SSEEvent{Type: "status", Username: username, Platform: platform, Status: "queued", Progress: 0})
	o.SavePersistedSyncInfo(platform, username, "queued", queuedAt)
}

func (o *Orchestrator) worker() {
	for job := range o.jobCh {
		o.acquireSlot()
		// re-check after waiting for a slot: the job may have been cancelled or its target removed meanwhile
		o.mu.RLock()
		p, ok := o.progress[taskKey(job.platform, job.username)]
		skip := !ok || p.Status != "queued"
		o.mu.RUnlock()
		if !skip {
			o.runScrape(job)
		}
		o.releaseSlot()
	}
}

func (o *Orchestrator) acquireSlot() {
	o.slotMu.Lock()
	for o.active >= config.GetConfig().Concurrency() {
		o.slotCond.Wait()
	}
	o.active++
	o.slotMu.Unlock()
}

func (o *Orchestrator) releaseSlot() {
	o.slotMu.Lock()
	o.active--
	o.slotMu.Unlock()
	o.slotCond.Broadcast()
}

// ConcurrencyChanged wakes workers waiting for a slot after the limit was raised.
func (o *Orchestrator) ConcurrencyChanged() {
	o.slotCond.Broadcast()
}

func (o *Orchestrator) runScrape(job scrapeJob) {
	username := job.username
	platform := job.platform
	lastSync := job.lastSync
	beforeCount := job.beforeCount
	key := taskKey(platform, username)

	o.mu.Lock()
	p := o.progress[key]
	if p == nil {
		o.mu.Unlock()
		slog.Warn("Skipping scrape for removed target", "user", username)
		return
	}
	p.Status = "running"
	p.Progress = 5
	prevUpdated := p.UpdatedAt
	persistUser := p.Username
	o.mu.Unlock()

	slog.Info("Starting media scrape worker", "user", username, "platform", platform)
	o.broadcast(SSEEvent{Type: "status", Username: username, Platform: platform, Status: "running", Progress: 5})
	o.SavePersistedSyncInfo(platform, persistUser, "running", prevUpdated)

	ctx, cancel := context.WithCancel(context.Background())
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Hour)
	defer timeoutCancel()

	o.mu.Lock()
	o.cancels[key] = cancel
	o.mu.Unlock()

	target := scraper.Target{Username: username, Platform: platform, DownloadPhotos: true, DownloadVideos: true}
	var err error
	opts := scraper.Options{
		LastSync:  lastSync,
		ForceFull: job.forceFullSync,
		OnProgress: func(pct int) {
			o.setProgress(key, pct)
		},
		Posts: o.posts,
	}

	SessionOptions(config.GetConfig(), &opts)
	if acc, ok := account(platform, username); ok {
		target.SaveText = acc.SaveText
		target.SkipRetweets = acc.SkipRetweets
		target.Filters = acc.Filters
		target.DownloadPhotos = acc.ShouldDownloadPhotos()
		target.DownloadVideos = acc.ShouldDownloadVideos()
		AccountSession(acc, &opts)
		if since, err := time.ParseInLocation(time.DateOnly, acc.SinceDate, time.Local); err == nil {
			opts.Since = since
		}
		// the cap bounds how far back a first or full sync reaches; incremental syncs stop at the watermark anyway
		if lastSync.IsZero() {
			opts.MaxPosts = acc.MaxPosts
		}
	}

	// watermark = start time: posts during a long scrape must not be skipped next run
	scrapeStart := time.Now()
	s, ok := scraper.Get(platform)
	if !ok {
		err = fmt.Errorf("unknown platform: %s", platform)
	} else {
		if platform == "twitter" {
			o.twitterMu.Lock()
			defer o.twitterMu.Unlock()
		}
		err = s(timeoutCtx, target, opts)
	}

	if err != nil {
		hintSessionRefresh(err)
	}
	// Circuit breaker: once a platform flags the session (temporary invalidation / "please wait" checkpoint), further requests with the dead session only prolong the block. Skip the remaining targets of that platform until the next sync window instead.
	if (platform == "instagram" || platform == "twitter") && errors.Is(err, scraper.ErrAuthExpired) {
		until := time.Now().Add(6 * time.Hour)
		o.mu.Lock()
		o.blockUntil[platform] = until
		o.mu.Unlock()
		slog.Warn("Session flagged; pausing the platform's targets for 6 hours", "platform", platform, "resume_after", until.Format(time.RFC3339))
	}

	o.mu.Lock()
	delete(o.cancels, key)
	if err != nil {
		if ctx.Err() != nil {
			p.Status = "idle"
			p.Progress = 0
		} else {
			p.Status = "failed"
			p.Progress = 100
		}
		p.UpdatedAt = prevUpdated
		p.AuthError = errors.Is(err, scraper.ErrAuthExpired)
	} else {
		p.Status = "completed"
		p.Progress = 100
		p.AuthError = false
		p.UpdatedAt = scrapeStart
		if job.forceFullSync && o.fullResync[key] {
			delete(o.fullResync, key)
			o.saveFullResyncLocked()
		}
	}
	o.mediaIndex.Invalidate(platform, username)
	afterCount := o.mediaIndex.Count(platform, username)
	newCount := max(0, afterCount-beforeCount)
	p.NewCount = newCount
	p.mediaCountCached = afterCount
	p.mediaCountCachedAt = time.Now()
	o.SavePersistedSyncInfo(platform, p.Username, p.Status, p.UpdatedAt)
	o.mu.Unlock()

	o.broadcast(SSEEvent{Type: "status", Username: username, Platform: platform, Status: p.Status, Progress: p.Progress})

	if err != nil {
		if ctx.Err() != nil {
			slog.Info("Scraping task cancelled by user", "user", username, "platform", platform)
		} else {
			slog.Error("Scraping task aborted with error", "user", username, "platform", platform, "error", err)
		}
	} else {
		slog.Info("Scraping completed successfully", "user", username, "platform", platform, "new_files", newCount, "total_files", afterCount)
	}
	o.recordRun(platform, username, scrapeStart, job.forceFullSync, newCount, err, ctx.Err() != nil)
}

func (o *Orchestrator) recordRun(platform, username string, started time.Time, full bool, newFiles int, err error, cancelled bool) {
	run := store.Run{Platform: platform, Username: username, StartedAt: started, FinishedAt: time.Now(), Status: "completed", FullSync: full, NewFiles: newFiles}
	switch {
	case cancelled:
		run.Status = "cancelled"
	case err != nil:
		run.Status = "failed"
		run.Error = err.Error()
	}
	if rerr := o.runs.Add(context.Background(), run); rerr != nil {
		slog.Warn("Failed to record sync run", "user", username, "error", rerr)
	}
}

// SessionOptions fills the platform-wide credentials from the config.
func SessionOptions(c config.Config, opts *scraper.Options) {
	opts.TwitterCookies = c.TwitterCookies
	opts.InstagramCookies = c.InstagramCookies
	opts.InstagramSidecarURL = c.InstagramSidecarURL
	opts.TikTokCookies = c.TikTokCookies
	opts.YouTubeCookies = c.YouTubeCookies
	opts.ThumbnailsDir = c.ThumbnailsDir
}

// AccountSession swaps in an account's own session. Its cookie export replaces the platform one whole: mixing one
// account's auth cookie with another browser's device cookies is a stronger bot signal than either alone.
func AccountSession(acc config.Account, opts *scraper.Options) {
	if acc.Cookies == "" {
		return
	}
	switch acc.Platform {
	case "twitter":
		opts.TwitterCookies = acc.Cookies
	case "instagram":
		opts.InstagramCookies = acc.Cookies
		// the sidecar holds one browser session; an account with its own cookies goes direct
		opts.InstagramSidecarURL = ""
	case "tiktok":
		opts.TikTokCookies = acc.Cookies
	case "youtube":
		opts.YouTubeCookies = acc.Cookies
	}
}

// IsActive reports whether a target is running or queued.
func (o *Orchestrator) IsActive(platform, username string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p, ok := o.progress[taskKey(platform, username)]
	return ok && (p.Status == "running" || p.Status == "queued")
}

// RenameTarget moves in-memory state of a target to its new handle; the store rows are renamed by the caller.
func (o *Orchestrator) RenameTarget(platform, from, to string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	oldKey, newKey := taskKey(platform, from), taskKey(platform, to)
	if p, ok := o.progress[oldKey]; ok {
		delete(o.progress, oldKey)
		p.Username = to
		o.progress[newKey] = p
	}
	if o.fullResync[oldKey] {
		delete(o.fullResync, oldKey)
		o.fullResync[newKey] = true
		o.saveFullResyncLocked()
	}
}

// CancelAll cancels all running and queued scrape tasks.
func (o *Orchestrator) CancelAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for key, cancel := range o.cancels {
		delete(o.cancels, key)
		cancel()
	}
	for {
		select {
		case job := <-o.jobCh:
			if p, ok := o.progress[taskKey(job.platform, job.username)]; ok && p.Status == "queued" {
				p.Status = "idle"
				p.Progress = 0
			}
		default:
			return
		}
	}
}

// CancelScrape cancels the running or queued scrape of a target. An empty platform cancels every target with that username.
func (o *Orchestrator) CancelScrape(platform, username string) bool {
	var (
		toCancel []context.CancelFunc
		dequeued []TaskProgress
	)
	o.mu.Lock()
	for key, p := range o.progress {
		if !strings.EqualFold(p.Username, username) || (platform != "" && p.Platform != platform) {
			continue
		}
		if cancel, ok := o.cancels[key]; ok {
			delete(o.cancels, key)
			toCancel = append(toCancel, cancel)
		}
		if p.Status == "queued" {
			p.Status = "idle"
			p.Progress = 0
			o.SavePersistedSyncInfo(p.Platform, p.Username, "idle", p.UpdatedAt)
			dequeued = append(dequeued, *p)
		}
	}
	o.mu.Unlock()

	for _, cancel := range toCancel {
		cancel()
	}
	for _, p := range dequeued {
		o.broadcast(SSEEvent{Type: "status", Username: p.Username, Platform: p.Platform, Status: "idle", Progress: 0})
	}
	return len(toCancel) > 0 || len(dequeued) > 0
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
	return o.lastSync
}

func (o *Orchestrator) GetAllProgress(accounts []config.Account) []TaskProgress {
	const mediaCountTTL = 30 * time.Second

	now := time.Now()
	o.mu.RLock()
	sources := make([]*TaskProgress, 0, len(accounts))
	result := make([]TaskProgress, 0, len(accounts))
	for _, acc := range accounts {
		v, exists := o.progress[taskKey(acc.Platform, acc.Username)]
		sources = append(sources, v)
		entry := TaskProgress{}
		if exists {
			// Copy under read lock to avoid racing concurrent writers.
			entry = *v
			entry.MediaCount = v.mediaCountCached
		}
		entry.Paused = acc.Paused
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
		count := o.mediaIndex.Count(result[i].Platform, result[i].Username)
		o.mu.Lock()
		v.mediaCountCached = count
		v.mediaCountCachedAt = now
		o.mu.Unlock()
		result[i].MediaCount = count
	}
	return result
}

func (o *Orchestrator) StartAutoSyncLoop(ctx context.Context) {
	slog.Info("Starting auto-sync scheduler")

	o.mu.Lock()
	o.lastSync = time.Now()
	o.mu.Unlock()

	for {
		interval := config.GetConfig().AutoSyncInterval
		wait := time.Minute
		if interval > 0 {
			d := time.Duration(interval) * time.Hour
			wait = time.Until(time.Now().Truncate(d).Add(d))
			if wait <= 0 {
				wait = time.Minute
			}
			// Wake a random 0-20 min off the exact boundary; fixed wall-clock times twice a day are a signature.
			wait += time.Duration(rand.Int63n(int64(20 * time.Minute)))
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
		if c.InQuietHours(time.Now()) {
			slog.Info("Auto-sync window falls in quiet hours, skipping this run", "quiet_hours", c.QuietHours)
			continue
		}
		slog.Info("Auto-sync interval reached, triggering sync for all accounts", "interval_hours", interval)
		o.mu.RLock()
		runningSet := make(map[string]bool)
		for _, p := range o.progress {
			if p.Status == "running" || p.Status == "queued" {
				runningSet[taskKey(p.Platform, p.Username)] = true
			}
		}
		o.mu.RUnlock()

		// Random order and uneven gaps: a fixed sequence at fixed gaps reads as a batch job. Gaps of several minutes spread the targets across the window instead of bursting them into one cluster.
		accs := slices.Clone(c.Accounts)
		rand.Shuffle(len(accs), func(i, j int) { accs[i], accs[j] = accs[j], accs[i] })
		for _, acc := range accs {
			if acc.Paused {
				slog.Debug("Skipping auto-sync, target is paused", "user", acc.Username, "platform", acc.Platform)
				continue
			}
			if config.GetConfig().InQuietHours(time.Now()) {
				slog.Info("Quiet hours started, leaving the remaining targets for the next run")
				break
			}
			if runningSet[taskKey(acc.Platform, acc.Username)] {
				slog.Debug("Skipping auto-sync, task already running or queued", "user", acc.Username)
				continue
			}
			if o.blocked(acc.Platform) {
				slog.Info("Skipping auto-sync, the platform session is flagged (circuit breaker)", "user", acc.Username, "platform", acc.Platform)
				continue
			}
			o.StartScrape(acc.Username, acc.Platform, false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5*time.Minute + time.Duration(rand.Int63n(int64(10*time.Minute)))):
			}
		}
		o.mu.Lock()
		o.lastSync = time.Now()
		o.mu.Unlock()
	}
}

// blocked reports whether the platform's circuit breaker is active.
func (o *Orchestrator) blocked(platform string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return time.Now().Before(o.blockUntil[platform])
}

const fullResyncSetting = "pending_full_resync"

func loadFullResync(st *store.SettingsStore) map[string]bool {
	out := make(map[string]bool)
	raw, err := st.Get(context.Background(), fullResyncSetting)
	if err != nil {
		return out
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		slog.Warn("Failed to parse pending full resyncs", "error", err)
		return out
	}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

func (o *Orchestrator) saveFullResyncLocked() {
	b, _ := json.Marshal(slices.Sorted(maps.Keys(o.fullResync)))
	if err := o.settings.Set(context.Background(), fullResyncSetting, string(b)); err != nil {
		slog.Warn("Failed to persist pending full resyncs", "error", err)
	}
}

// RequestFullResync makes the next scrape of a target a full one; it survives restarts and is cleared by the first successful full run.
func (o *Orchestrator) RequestFullResync(platform, username string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fullResync[taskKey(platform, username)] = true
	o.saveFullResyncLocked()
}

// ResetSessionBlocks clears the circuit breakers; called when the operator saves fresh session settings.
func (o *Orchestrator) ResetSessionBlocks() {
	o.mu.Lock()
	clear(o.blockUntil)
	o.mu.Unlock()
}

// hintSessionRefresh tells the operator how to restore a stale scraping session.
func hintSessionRefresh(err error) {
	switch {
	case errors.Is(err, scraper.ErrRateLimited):
		slog.Warn("Instagram rate-limited requests (429); the session is valid, wait for the throttle to decay instead of re-saving cookies", "error", err)
	case errors.Is(err, scraper.ErrAuthExpired):
		if strings.Contains(err.Error(), "twitter") {
			slog.Warn("Scraping session rejected by Twitter; the auth_token expired, paste a fresh cookie export in settings", "error", err)
		} else {
			slog.Warn("Scraping session rejected by Instagram; the sessionid cookie expired, paste a fresh one in settings", "error", err)
		}
	case err != nil && strings.Contains(err.Error(), "Cannot find query with id"):
		slog.Warn("Twitter rejected the queryId even after the bundle harvest; paste a fresh queryId in settings", "error", err)
	}
}
