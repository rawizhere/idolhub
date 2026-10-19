package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tmaxmax/go-sse"

	"idolhub/internal/config"
	"idolhub/internal/gallery"
	"idolhub/internal/logging"
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
	LastSync       time.Time
	sseServer      *sse.Server
	autoSyncCtx    context.Context
	autoSyncCancel context.CancelFunc
	mediaIndex     *gallery.Index
	accounts       *store.AccountStore
	posts          *store.PostStore
	settings       *store.SettingsStore
	// fullResync holds task keys whose next scrape must be a full one (media was quarantined after their watermark).
	fullResync   map[string]bool
	igBlockUntil time.Time
	twBlockUntil time.Time
	twitterMu    sync.Mutex
	jobCh        chan scrapeJob
	logEvents    chan logEvent
}

type logEvent struct {
	user     string
	platform string
	t        time.Time
	level    string
	msg      string
}

var GlobalOrchestrator *Orchestrator

// taskKey identifies one scrape target.
func taskKey(platform, username string) string {
	return platform + "/" + strings.ToLower(username)
}

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
		settings:       st.Settings,
		fullResync:     loadFullResync(st.Settings),
		jobCh:          make(chan scrapeJob, 100),
		logEvents:      make(chan logEvent, 4096),
	}
	GlobalOrchestrator = orch

	go func() {
		for evt := range orch.logEvents {
			orch.dispatch(evt)
		}
	}()

	slog.SetDefault(slog.New(&taskLogHandler{
		Handler: logging.NewHandler(),
		orch:    orch,
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

	evt := logEvent{user: targetUser, platform: targetPlatform, t: time.Now(), level: r.Level.String(), msg: formattedMsg}
	if h.orch.logEvents == nil {
		h.orch.dispatch(evt)
	} else {
		select {
		case h.orch.logEvents <- evt:
		default:
		}
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

// SSEHandler serves the SSE stream directly via sseServer.
func (o *Orchestrator) SSEHandler() http.Handler {
	return o.sseServer
}

func (o *Orchestrator) ShutdownSSE(ctx context.Context) {
	if o.sseServer == nil {
		return
	}
	if err := o.sseServer.Shutdown(ctx); err != nil {
		slog.Warn("SSE shutdown failed", "error", err)
	}
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

func (o *Orchestrator) setProgress(key string, pct int) {
	o.mu.Lock()
	if p, exists := o.progress[key]; exists && (p.Status == "running" || p.Status == "queued") {
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

	if platform == "instagram" || platform == "twitter" {
		// o.mu is already held here; read the breaker field directly
		blockedUntil := o.igBlockUntil
		if platform == "twitter" {
			blockedUntil = o.twBlockUntil
		}
		if time.Now().Before(blockedUntil) {
			o.mu.Unlock()
			slog.Warn("Skipping scrape, session is flagged (circuit breaker); save new session settings to reset it", "user", username, "platform", platform, "resume_after", blockedUntil.Format(time.RFC3339))
			return
		}
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

	lastSync := p.UpdatedAt
	forceFullSync := forceFull
	if forceFullSync {
		lastSync = time.Time{}
	}
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
	if forceFullSync {
		lastSync = time.Time{}
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

	o.broadcast(SSEEvent{Type: "status", Username: username, Platform: platform, Status: "queued", Progress: 0})
	o.SavePersistedSyncInfo(platform, username, "queued", queuedAt)
}

func (o *Orchestrator) worker() {
	for job := range o.jobCh {
		o.mu.RLock()
		p, ok := o.progress[taskKey(job.platform, job.username)]
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
	persistStatus := p.Status
	persistUpdated := p.UpdatedAt
	o.mu.Unlock()

	slog.Info("Starting media scrape worker", "user", username, "platform", platform)
	o.broadcast(SSEEvent{Type: "status", Username: username, Platform: platform, Status: "running", Progress: 5})
	o.SavePersistedSyncInfo(platform, persistUser, persistStatus, persistUpdated)

	ctx, cancel := context.WithCancel(context.Background())
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Hour)
	defer timeoutCancel()

	o.mu.Lock()
	o.cancels[key] = cancel
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
			o.setProgress(key, pct)
		},
	}

	c := config.GetConfig()
	for _, acc := range c.Accounts {
		if acc.Platform == platform && strings.EqualFold(acc.Username, username) {
			target.SkipRetweets = acc.SkipRetweets
			target.Filters = acc.Filters
			dp := acc.ShouldDownloadPhotos()
			dv := acc.ShouldDownloadVideos()
			target.DownloadPhotos = &dp
			target.DownloadVideos = &dv
			// per-account token: one auth_token across all targets is the loudest ban signal X sees
			if acc.TwitterAuthToken != "" {
				opts.TwitterAuthToken = acc.TwitterAuthToken
			}
			break
		}
	}

	if opts.TwitterAuthToken == "" {
		opts.TwitterAuthToken = c.TwitterAuthToken
	}
	opts.TwitterCookies = c.TwitterCookies
	opts.InstagramSessionID = c.InstagramSessionID
	opts.InstagramCookies = c.InstagramCookies
	opts.InstagramSidecarURL = c.InstagramSidecarURL
	opts.TikTokCookies = c.TikTokCookies
	opts.Posts = o.posts

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
	// Circuit breaker: once instagram flags the session (temporary invalidation / "please wait" checkpoint), further requests with the dead session only prolong the block. Skip the remaining instagram targets until the next sync window instead.
	if platform == "instagram" && errors.Is(err, scraper.ErrAuthExpired) {
		o.mu.Lock()
		o.igBlockUntil = time.Now().Add(6 * time.Hour)
		o.mu.Unlock()
		slog.Warn("Instagram session flagged; pausing instagram targets for 6 hours", "resume_after", time.Now().Add(6*time.Hour).Format(time.RFC3339))
	}
	if platform == "twitter" && errors.Is(err, scraper.ErrAuthExpired) {
		o.mu.Lock()
		o.twBlockUntil = time.Now().Add(6 * time.Hour)
		o.mu.Unlock()
		slog.Warn("Twitter session flagged; pausing twitter targets for 6 hours", "resume_after", time.Now().Add(6*time.Hour).Format(time.RFC3339))
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
	return o.LastSync
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
		if !f.IsDir() && f.Name() != "posts.json" && !strings.HasSuffix(f.Name(), ".bak") && f.Name() != ".DS_Store" {
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
			if runningSet[taskKey(acc.Platform, acc.Username)] {
				slog.Debug("Skipping auto-sync, task already running or queued", "user", acc.Username)
				continue
			}
			if acc.Platform == "instagram" && o.instagramBlocked() {
				slog.Info("Skipping auto-sync, instagram session is flagged (circuit breaker)", "user", acc.Username)
				continue
			}
			o.StartScrape(acc.Username, acc.Platform, acc.SaveText, false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5*time.Minute + time.Duration(rand.Int63n(int64(10*time.Minute)))):
			}
		}
		o.mu.Lock()
		o.LastSync = time.Now()
		o.mu.Unlock()
	}
}

// instagramBlocked reports whether the instagram circuit breaker is active.
func (o *Orchestrator) instagramBlocked() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return time.Now().Before(o.igBlockUntil)
}

const fullResyncSetting = "pending_full_resync"

func loadFullResync(st *store.SettingsStore) map[string]bool {
	out := make(map[string]bool)
	if st == nil {
		return out
	}
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
	if o.settings == nil {
		return
	}
	keys := make([]string, 0, len(o.fullResync))
	for k := range o.fullResync {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	b, _ := json.Marshal(keys)
	if err := o.settings.Set(context.Background(), fullResyncSetting, string(b)); err != nil {
		slog.Warn("Failed to persist pending full resyncs", "error", err)
	}
}

// RequestFullResync makes the next scrape of a target a full one; it survives restarts and is cleared by the first successful full run.
func (o *Orchestrator) RequestFullResync(platform, username string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fullResync == nil {
		o.fullResync = make(map[string]bool)
	}
	o.fullResync[taskKey(platform, username)] = true
	o.saveFullResyncLocked()
}

// ResetSessionBlocks clears the circuit breakers; called when the operator saves fresh session settings.
func (o *Orchestrator) ResetSessionBlocks() {
	o.mu.Lock()
	o.igBlockUntil = time.Time{}
	o.twBlockUntil = time.Time{}
	o.mu.Unlock()
}

// hintSessionRefresh tells the operator how to restore a stale scraping session.
func hintSessionRefresh(err error) {
	switch {
	case errors.Is(err, scraper.ErrRateLimited):
		slog.Warn("Instagram rate-limited requests (429); the session is valid, wait for the throttle to decay instead of re-saving cookies", "error", err)
	case errors.Is(err, scraper.ErrAuthExpired):
		if err != nil && strings.Contains(err.Error(), "twitter") {
			slog.Warn("Scraping session rejected by Twitter; the auth_token expired, paste a fresh cookie export in settings", "error", err)
		} else {
			slog.Warn("Scraping session rejected by Instagram; the sessionid cookie expired, paste a fresh one in settings", "error", err)
		}
	case err != nil && strings.Contains(err.Error(), "Cannot find query with id"):
		slog.Warn("Twitter rejected the queryId even after the bundle harvest; paste a fresh queryId in settings", "error", err)
	}
}
