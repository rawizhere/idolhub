package main

import (
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agnivade/levenshtein"

	"idolhub/cmd/parser/web/templates"
	"idolhub/internal/config"
	"idolhub/internal/download"
	"idolhub/internal/gallery"
	"idolhub/internal/logging"
	"idolhub/internal/orchestrator"
	"idolhub/internal/scraper"
	"idolhub/internal/store"

	"github.com/a-h/templ"
	"golang.org/x/sync/singleflight"
)

//go:embed all:web/static
var staticAssets embed.FS

const maxBodyBytes = 1 << 20

var segmentRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func validSegment(s string) bool {
	return s != "" && s != "." && !strings.Contains(s, "..") && segmentRe.MatchString(s)
}

type App struct {
	orch       *orchestrator.Orchestrator
	mediaIndex *gallery.Index
	st         *store.Store
	thumbs     singleflight.Group
	thumbSem   chan struct{}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data: https://img.youtube.com; media-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return json.NewDecoder(r.Body).Decode(v)
}

func main() {
	logging.Init()

	switch {
	case len(os.Args) > 1 && os.Args[1] == "import-json":
		if err := runImportJSON(os.Args[2:]); err != nil {
			slog.Error("import-json failed", "error", err)
			os.Exit(1)
		}
		return
	case len(os.Args) > 1 && os.Args[1] == "export-json":
		if err := runExportJSON(os.Args[2:]); err != nil {
			slog.Error("export-json failed", "error", err)
			os.Exit(1)
		}
		return
	}

	st, err := store.Open("configs/idolhub.db")
	if err != nil {
		slog.Error("Failed to open store", "error", err)
		os.Exit(1)
	}

	if err := config.LoadConfig(st); err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	gallery.Init(st.Posts)
	orchestrator.InitOrchestrator(gallery.GlobalIndex, st)
	cfg := config.GetConfig()
	slog.Info("Configuration loaded successfully", "targets_count", len(cfg.Accounts), "auto_sync_interval_hours", cfg.AutoSyncInterval)
	app := &App{
		orch:       orchestrator.GlobalOrchestrator,
		mediaIndex: gallery.GlobalIndex,
		st:         st,
		thumbSem:   make(chan struct{}, 2),
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", app.serveStatic())
	mux.HandleFunc("GET /api/config", app.handleConfigGet)
	mux.HandleFunc("POST /api/config", app.handleConfigPost)
	mux.HandleFunc("GET /api/progress", app.handleProgress)
	mux.HandleFunc("POST /api/scrape/start", app.handleScrapeStart)
	mux.HandleFunc("POST /api/scrape/cancel", app.handleScrapeCancel)
	mux.HandleFunc("POST /api/scrape/clear", app.handleScrapeClear)
	mux.HandleFunc("GET /api/gallery", app.handleGallery)
	mux.HandleFunc("GET /api/search", app.handleGlobalSearchAPI)
	mux.HandleFunc("GET /api/events", app.handleSSE)
	mux.HandleFunc("GET /gallery/{platform}/{username}", app.handleGalleryPage)
	mux.HandleFunc("GET /gallery/{platform}/{username}/page/{page}", app.handleGalleryPage)
	mux.HandleFunc("GET /gallery/{platform}/{username}/posts", app.handleGalleryPage)
	mux.HandleFunc("GET /gallery/{platform}/{username}/posts/page/{page}", app.handleGalleryPage)
	mux.HandleFunc("GET /media/", app.handleMedia)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	srv := &http.Server{
		Addr:              port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Starting IdolHub dashboard server", "addr", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	orchestrator.GlobalOrchestrator.SyncTargets(cfg.Accounts)

	scraper.MigrateThumbnails()
	scraper.MigrateVideoCodecs()

	sig := <-quit
	slog.Info("Received signal, shutting down", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	sseCtx, sseCancel := context.WithTimeout(shutdownCtx, 5*time.Second)
	defer sseCancel()
	app.orch.ShutdownSSE(sseCtx)
	app.orch.Shutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown failed", "error", err)
	}
	if err := st.Close(); err != nil {
		slog.Warn("Failed to close store", "error", err)
	}
	slog.Info("Server exited gracefully")
}

func init() {
	for ext, typ := range map[string]string{
		".mp4":  "video/mp4",
		".m4v":  "video/mp4",
		".webm": "video/webm",
		".mov":  "video/quicktime",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

func (a *App) serveStatic() http.Handler {
	sub, _ := fs.Sub(staticAssets, "web/static")
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/static")
		fileServer.ServeHTTP(w, r)
	})
}

func (a *App) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(config.GetConfig())
}

func (a *App) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	var cfg config.Config
	if err := decodeJSON(w, r, &cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	oldCfg := config.GetConfig()
	var changed []config.Account
	for i, newAcc := range cfg.Accounts {
		var oldAcc *config.Account
		for _, acc := range oldCfg.Accounts {
			if strings.EqualFold(acc.Username, newAcc.Username) {
				oldAcc = &acc
				break
			}
		}
		if oldAcc != nil {
			if !accountsEqual(*oldAcc, newAcc) {
				cfg.Accounts[i].LastSyncStatus = "idle"
				cfg.Accounts[i].LastSyncTime = time.Time{}
				changed = append(changed, newAcc)
			} else {
				cfg.Accounts[i].LastSyncStatus = oldAcc.LastSyncStatus
				cfg.Accounts[i].LastSyncTime = oldAcc.LastSyncTime
			}
		}
	}

	if err := config.SaveConfig(a.st, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, acc := range changed {
		if a.st == nil {
			continue
		}
		err := a.st.Accounts.SetSyncInfo(r.Context(), acc.Platform, acc.Username, "idle", time.Time{})
		if err != nil {
			slog.Warn("Failed to reset sync info", "user", acc.Username, "error", err)
		}
	}

	a.orch.SyncTargets(cfg.Accounts)

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type ProgressResponse struct {
	Targets          []orchestrator.TaskProgress `json:"targets"`
	GlobalLogs       []orchestrator.TaskLog      `json:"global_logs"`
	LastSync         string                      `json:"last_sync"`
	AutoSyncInterval int                         `json:"auto_sync_interval"`
}

func (a *App) handleProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")

	c := config.GetConfig()
	targets := a.orch.GetAllProgress(c.Accounts)
	globalLogs := a.orch.GetGlobalLogs()

	resp := ProgressResponse{
		Targets:          targets,
		GlobalLogs:       globalLogs,
		LastSync:         a.orch.LastSyncTime().Format(time.RFC3339),
		AutoSyncInterval: c.AutoSyncInterval,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

type scrapeRequest struct {
	Username  string `json:"username"`
	ForceFull bool   `json:"force_full,omitempty"`
}

type GalleryResponse struct {
	Username string         `json:"username"`
	Platform string         `json:"platform"`
	Files    []gallery.File `json:"files"`
	Posts    []gallery.Post `json:"posts"`
}

func (a *App) handleGallery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")

	platform := r.URL.Query().Get("platform")
	username := r.URL.Query().Get("username")
	if platform == "" || username == "" {
		http.Error(w, "Missing platform or username", http.StatusBadRequest)
		return
	}

	files := a.mediaIndex.View(platform, username)
	posts := a.mediaIndex.Posts(platform, username)

	for i, p := range posts {
		var localFiles []gallery.PostMediaFile
		for _, mediaURL := range p.MediaURLs {
			gf := a.findLocalFile(mediaURL, platform, username, files)
			if gf == nil {
				videoName := p.TweetID + "_video.mp4"
				for j := range files {
					if files[j].Filename == videoName {
						gf = &files[j]
						break
					}
				}
			}
			if gf != nil {
				localFiles = append(localFiles, gallery.PostMediaFile{
					Filename:     gf.Filename,
					URL:          gf.URL,
					ThumbnailURL: gf.ThumbnailURL,
				})
			}
		}
		posts[i].LocalFiles = localFiles
	}
	slices.SortFunc(posts, func(a, b gallery.Post) int {
		return cmp.Compare(b.Date, a.Date)
	})

	_ = json.NewEncoder(w).Encode(GalleryResponse{
		Username: username,
		Platform: platform,
		Files:    files,
		Posts:    posts,
	})
}

func (a *App) handleMedia(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/media/")
	for _, seg := range strings.Split(relPath, "/") {
		if !validSegment(seg) {
			http.NotFound(w, r)
			return
		}
	}
	filePath := filepath.Join("downloads", filepath.FromSlash(filepath.Clean(relPath)))

	if info, err := os.Stat(filePath); err != nil || info.IsDir() {
		if err == nil {
			http.NotFound(w, r)
			return
		}
		a.ensureThumbnail(filePath)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".mp4", ".m4v":
		w.Header().Set("Content-Type", "video/mp4")
	case ".webm":
		w.Header().Set("Content-Type", "video/webm")
	case ".mov":
		w.Header().Set("Content-Type", "video/quicktime")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	default:
		if ct := mime.TypeByExtension(ext); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}
	w.Header().Set("Accept-Ranges", "bytes")

	http.ServeFile(w, r, filePath)
}

var thumbnailExts = []string{".mp4", ".mov", ".webm", ".jpg", ".jpeg", ".png", ".webp"}

func (a *App) ensureThumbnail(thumbPath string) {
	if !strings.Contains(thumbPath, "thumbnails"+string(filepath.Separator)) {
		return
	}
	dir := filepath.Dir(thumbPath)
	parentDir := filepath.Dir(dir)
	base := strings.TrimSuffix(filepath.Base(thumbPath), filepath.Ext(thumbPath))
	var srcFile string
	for _, ext := range thumbnailExts {
		candidate := filepath.Join(parentDir, base+ext)
		if _, err := os.Stat(candidate); err == nil {
			srcFile = candidate
			break
		}
	}
	if srcFile == "" {
		return
	}
	_, _, _ = a.thumbs.Do(thumbPath, func() (any, error) {
		a.thumbSem <- struct{}{}
		defer func() { <-a.thumbSem }()
		if _, err := os.Stat(thumbPath); err == nil {
			return nil, nil
		}
		_ = os.MkdirAll(dir, 0755)
		_ = download.GenerateThumbnail(srcFile, thumbPath)
		return nil, nil
	})
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	a.orch.SSEHandler().ServeHTTP(w, r)
}

func (a *App) handleScrapeStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	var req scrapeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c := config.GetConfig()

	if req.Username == "all" {
		if req.ForceFull {
			slog.Info("Triggered force full resync for all configured targets")
		} else {
			slog.Info("Triggered sync for all configured targets")
		}
		for _, acc := range c.Accounts {
			a.orch.StartScrape(acc.Username, acc.Platform, acc.SaveText, req.ForceFull)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "target": "all"})
		return
	}

	var targetAccount *config.Account
	for i := range c.Accounts {
		if strings.EqualFold(c.Accounts[i].Username, req.Username) {
			targetAccount = &c.Accounts[i]
			break
		}
	}

	if targetAccount == nil {
		http.Error(w, "Account not found in settings", http.StatusNotFound)
		return
	}

	if req.ForceFull {
		slog.Info("Triggered force full resync for target", "user", targetAccount.Username, "platform", targetAccount.Platform)
	} else {
		slog.Info("Triggered sync for target", "user", targetAccount.Username, "platform", targetAccount.Platform)
	}
	a.orch.StartScrape(targetAccount.Username, targetAccount.Platform, targetAccount.SaveText, req.ForceFull)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "target": targetAccount.Username})
}

type clearRequest struct {
	Username string `json:"username"`
	Platform string `json:"platform"`
}

func (a *App) handleScrapeCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	var req scrapeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		http.Error(w, "Missing username", http.StatusBadRequest)
		return
	}

	slog.Info("Cancelling active sync", "user", req.Username)
	ok := a.orch.CancelScrape(req.Username)
	w.Header().Set("Content-Type", "application/json")
	if ok {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled", "target": req.Username})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_running", "target": req.Username})
	}
}

func (a *App) handleScrapeClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	var req clearRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Platform == "" {
		http.Error(w, "Missing platform or username", http.StatusBadRequest)
		return
	}
	if !validSegment(req.Platform) || !validSegment(req.Username) {
		http.Error(w, "Invalid platform or username", http.StatusBadRequest)
		return
	}

	slog.Info("Clearing downloaded files for target", "user", req.Username, "platform", req.Platform)
	dir := filepath.Join("downloads", req.Platform, req.Username)
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("Failed to delete target directory", "user", req.Username, "dir", dir, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.orch.SavePersistedSyncInfo(req.Platform, req.Username, "idle", time.Time{})
	if a.mediaIndex != nil {
		a.mediaIndex.Invalidate(req.Platform, req.Username)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleared", "target": req.Username})
}

func accountsEqual(a, b config.Account) bool {
	a.LastSyncStatus, b.LastSyncStatus = "", ""
	a.LastSyncTime, b.LastSyncTime = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func (a *App) findLocalFile(mediaURL, platform, username string, files []gallery.File) *gallery.File {
	name, ok := a.mediaIndex.FindLocalFile(platform, username, mediaURL)
	if !ok {
		return nil
	}
	for i := range files {
		if files[i].Filename == name {
			return &files[i]
		}
	}
	return nil
}

func fuzzyContains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	h := strings.ToLower(haystack)
	n := strings.ToLower(needle)
	if strings.Contains(h, n) {
		return true
	}
	for _, w := range strings.Fields(h) {
		if levenshtein.ComputeDistance(w, n) <= 2 {
			return true
		}
	}
	return false
}

const galleryPageSize = 48

var youtubeRe = regexp.MustCompile(`^.*(youtu\.be/|v/|u/\w/|embed/|watch\?v=|&v=|shorts/)([^#&?]*).*`)

func getYoutubeID(rawURL string) string {
	m := youtubeRe.FindStringSubmatch(rawURL)
	if len(m) >= 3 && len(m[2]) == 11 {
		return m[2]
	}
	return ""
}

type galleryPageParams struct {
	Platform string
	Username string
	Page     int
	Dir      string
	Sort     string
	Search   string
	Year     string
	Month    string
	Tags     string
}

func parseGalleryParams(r *http.Request) galleryPageParams {
	p := galleryPageParams{
		Platform: r.PathValue("platform"),
		Username: r.PathValue("username"),
		Sort:     r.URL.Query().Get("sort"),
		Search:   r.URL.Query().Get("q"),
		Year:     r.URL.Query().Get("year"),
		Month:    r.URL.Query().Get("month"),
		Tags:     r.URL.Query().Get("tags"),
	}
	page := 1
	if pageStr := r.PathValue("page"); pageStr != "" {
		if n, err := strconv.Atoi(pageStr); err == nil && n >= 1 {
			page = n
		}
	}
	p.Page = page
	p.Dir = filepath.Join("downloads", p.Platform, p.Username)
	return p
}

// handleGalleryPage renders a gallery grid or posts page
func (a *App) handleGalleryPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	p := parseGalleryParams(r)
	if p.Platform == "" || p.Username == "" {
		http.Error(w, "Invalid gallery path", http.StatusBadRequest)
		return
	}

	if strings.Contains(r.URL.Path, "/posts") {
		a.handleGalleryPostsPage(w, r, p)
		return
	}

	platform := p.Platform
	username := p.Username
	page := p.Page
	search := p.Search
	sortParam := p.Sort
	year := p.Year
	month := p.Month

	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}

	galleryFiles := a.mediaIndex.View(platform, username)
	allFiles := make([]templates.GalleryFileData, 0, len(galleryFiles))
	for _, f := range galleryFiles {
		allFiles = append(allFiles, templates.GalleryFileData{
			Filename:     f.Filename,
			Type:         f.Type,
			Date:         f.Date,
			Size:         f.Size,
			URL:          f.URL,
			ThumbnailURL: f.ThumbnailURL,
		})
	}

	filePostText := gallery.GlobalIndex.FilePostText(platform, username)

	if filter != "all" {
		filtered := allFiles[:0]
		for _, f := range allFiles {
			if f.Type == filter {
				filtered = append(filtered, f)
			}
		}
		allFiles = filtered
	}

	if search != "" {
		search = strings.ToLower(strings.TrimSpace(search))
		filtered := allFiles[:0]
		for _, f := range allFiles {
			matchesFilename := strings.Contains(strings.ToLower(f.Filename), search)
			matchesDate := strings.Contains(f.Date, search)
			postText := strings.ToLower(filePostText[f.Filename])
			matchesPost := fuzzyContains(postText, search)
			if matchesFilename || matchesDate || matchesPost {
				filtered = append(filtered, f)
			}
		}
		allFiles = filtered
	}

	allFiles = gallery.FilterByYearMonth(allFiles, func(f templates.GalleryFileData) string { return f.Date }, gallery.SplitList(year), gallery.SplitList(month))

	if sortParam == "asc" {
		for i, j := 0, len(allFiles)-1; i < j; i, j = i+1, j-1 {
			allFiles[i], allFiles[j] = allFiles[j], allFiles[i]
		}
	}

	if p.Tags != "" && p.Tags != "all" {
		matchingFilenames := make(map[string]bool)
		for _, rp := range a.mediaIndex.Posts(platform, username) {
			if !gallery.TagSelected(gallery.SplitList(p.Tags), gallery.Hashtags(rp.Text)) {
				continue
			}
			for _, mu := range rp.MediaURLs {
				if gf := a.findLocalFile(mu, platform, username, galleryFiles); gf != nil {
					matchingFilenames[gf.Filename] = true
				}
			}
			videoName := rp.TweetID + "_video.mp4"
			for _, gf := range galleryFiles {
				if gf.Filename == videoName {
					matchingFilenames[gf.Filename] = true
					break
				}
			}
		}
		filtered := allFiles[:0]
		for _, f := range allFiles {
			if matchingFilenames[f.Filename] {
				filtered = append(filtered, f)
			}
		}
		allFiles = filtered
	}

	pageFiles, totalPages := gallery.Page(allFiles, page, galleryPageSize)
	templ.Handler(templates.GalleryGridPage(pageFiles, platform, username, filter, search, sortParam, year, month, p.Tags, page, totalPages)).ServeHTTP(w, r)
}

func (a *App) handleGalleryPostsPage(w http.ResponseWriter, r *http.Request, gp galleryPageParams) {
	rawPosts := a.mediaIndex.Posts(gp.Platform, gp.Username)
	files := a.mediaIndex.View(gp.Platform, gp.Username)

	var allPosts []templates.GalleryPostData
	for _, p := range rawPosts {
		dateLabel := ""
		if p.Date != "" {
			dateLabel = strings.ReplaceAll(p.Date, "_", " ")
			if len(dateLabel) >= 10 {
				dateLabel = dateLabel[:10]
			}
		}
		cleanText := regexp.MustCompile(`https://t\.co/\S+`).ReplaceAllString(p.Text, "")
		cleanText = strings.TrimSpace(cleanText)

		tweetIDSuffix := ""
		if p.TweetID != "" {
			if len(p.TweetID) > 6 {
				tweetIDSuffix = p.TweetID[len(p.TweetID)-6:]
			} else {
				tweetIDSuffix = p.TweetID
			}
		}

		var localFiles []templates.GalleryPostMediaFile
		for _, mediaURL := range p.MediaURLs {
			gf := a.findLocalFile(mediaURL, gp.Platform, gp.Username, files)
			if gf == nil {
				videoName := p.TweetID + "_video.mp4"
				for j := range files {
					if files[j].Filename == videoName {
						gf = &files[j]
						break
					}
				}
			}
			if gf != nil {
				localFiles = append(localFiles, templates.GalleryPostMediaFile{
					Filename:     gf.Filename,
					URL:          gf.URL,
					ThumbnailURL: gf.ThumbnailURL,
					IsVideo:      gf.Type == "video",
				})
			}
		}

		var youtubeURLs []templates.GalleryPostYoutubeURL
		for _, ytURL := range p.YoutubeURLs {
			videoID := getYoutubeID(ytURL)
			if videoID == "" {
				videoID = ytURL
			}
			youtubeURLs = append(youtubeURLs, templates.GalleryPostYoutubeURL{
				URL:     ytURL,
				VideoID: videoID,
			})
		}

		allPosts = append(allPosts, templates.GalleryPostData{
			TweetID:       p.TweetID,
			TweetIDSuffix: tweetIDSuffix,
			DateLabel:     dateLabel,
			CleanText:     cleanText,
			Platform:      gp.Platform,
			LocalFiles:    localFiles,
			YoutubeURLs:   youtubeURLs,
		})
	}

	// Sort posts by date (default descending = newest first)
	slices.SortFunc(allPosts, func(a, b templates.GalleryPostData) int {
		if gp.Sort == "asc" {
			return cmp.Compare(a.DateLabel, b.DateLabel)
		}
		return cmp.Compare(b.DateLabel, a.DateLabel)
	})

	if gp.Search != "" {
		searchLower := strings.ToLower(gp.Search)
		filtered := allPosts[:0]
		for _, p := range allPosts {
			if strings.Contains(strings.ToLower(p.CleanText), searchLower) ||
				strings.Contains(strings.ToLower(p.TweetID), searchLower) ||
				strings.Contains(p.DateLabel, gp.Search) {
				filtered = append(filtered, p)
			}
		}
		allPosts = filtered
	}

	if gp.Tags != "" && gp.Tags != "all" {
		filtered := allPosts[:0]
		for _, p := range allPosts {
			if gallery.TagSelected(gallery.SplitList(gp.Tags), gallery.Hashtags(p.CleanText)) {
				filtered = append(filtered, p)
			}
		}
		allPosts = filtered
	}

	allPosts = gallery.FilterByYearMonth(allPosts, func(p templates.GalleryPostData) string { return p.DateLabel }, gallery.SplitList(gp.Year), gallery.SplitList(gp.Month))

	pagePosts, totalPages := gallery.Page(allPosts, gp.Page, galleryPageSize)

	templ.Handler(templates.GalleryPostsPage(pagePosts, gp.Platform, gp.Username, gp.Sort, gp.Search, gp.Year, gp.Month, gp.Tags, gp.Page, totalPages)).ServeHTTP(w, r)
}

type GlobalSearchResultFile struct {
	Platform     string `json:"platform"`
	Username     string `json:"username"`
	Filename     string `json:"filename"`
	Type         string `json:"type"`
	Date         string `json:"date"`
	Size         int64  `json:"size"`
	SizeHuman    string `json:"size_human"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Caption      string `json:"caption,omitempty"`
}

type GlobalSearchResponse struct {
	Query      string                   `json:"query"`
	TotalFiles int                      `json:"total_files"`
	Files      []GlobalSearchResultFile `json:"files"`
}

func (a *App) handleGlobalSearchAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")

	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	cfg := config.GetConfig()

	var matchingFiles []GlobalSearchResultFile

	for _, acc := range cfg.Accounts {
		platform := acc.Platform
		username := acc.Username
		galleryFiles := a.mediaIndex.View(platform, username)
		if len(galleryFiles) == 0 {
			continue
		}

		filePostText := gallery.GlobalIndex.FilePostText(platform, username)

		for _, gf := range galleryFiles {
			caption := filePostText[gf.Filename]
			matchesQuery := query == "" ||
				strings.Contains(strings.ToLower(gf.Filename), query) ||
				strings.Contains(gf.Date, query) ||
				fuzzyContains(caption, query) ||
				strings.Contains(strings.ToLower(username), query)

			if matchesQuery {
				matchingFiles = append(matchingFiles, GlobalSearchResultFile{
					Platform:     platform,
					Username:     username,
					Filename:     gf.Filename,
					Type:         gf.Type,
					Date:         gf.Date,
					Size:         gf.Size,
					URL:          gf.URL,
					ThumbnailURL: gf.ThumbnailURL,
					Caption:      caption,
				})
			}
		}
	}

	slices.SortFunc(matchingFiles, func(a, b GlobalSearchResultFile) int {
		return cmp.Compare(b.Date, a.Date)
	})

	_ = json.NewEncoder(w).Encode(GlobalSearchResponse{
		Query:      query,
		TotalFiles: len(matchingFiles),
		Files:      matchingFiles,
	})
}
