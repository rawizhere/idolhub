package main

import (
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"idolhub/cmd/parser/web/templates"
	"idolhub/internal/config"
	"idolhub/internal/gallery"
	"idolhub/internal/logging"
	"idolhub/internal/orchestrator"
	"idolhub/internal/scraper"

	"github.com/a-h/templ"
)

//go:embed all:web/static
var staticAssets embed.FS

type App struct {
	cfg        config.Store
	orch       orchestrator.Orchestrator
	mediaIndex gallery.MediaIndex
}

func main() {
	logging.Init()

	if err := config.LoadConfig(); err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	gallery.Init()
	orchestrator.InitOrchestrator(config.GlobalStore, gallery.GlobalIndex)
	orchestrator.GlobalOrchestrator.SyncTargets(config.GetConfig().Accounts)

	scraper.MigrateThumbnails()

	app := &App{
		cfg:        config.GlobalStore,
		orch:       orchestrator.GlobalOrchestrator,
		mediaIndex: gallery.GlobalIndex,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.serveStatic)
	mux.HandleFunc("GET /api/config", app.handleConfigGet)
	mux.HandleFunc("POST /api/config", app.handleConfigPost)
	mux.HandleFunc("GET /api/progress", app.handleProgress)
	mux.HandleFunc("POST /api/scrape/start", app.handleScrapeStart)
	mux.HandleFunc("POST /api/scrape/cancel", app.handleScrapeCancel)
	mux.HandleFunc("POST /api/scrape/clear", app.handleScrapeClear)
	mux.HandleFunc("GET /api/gallery", app.handleGallery)
	mux.HandleFunc("GET /api/events", app.handleSSE)
	mux.HandleFunc("GET /gallery/", app.handleGalleryPage)
	mux.HandleFunc("GET /media/", app.handleMedia)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	srv := &http.Server{Addr: port, Handler: mux}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Starting IdolHub dashboard server", "addr", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-quit
	slog.Info("Received signal, shutting down", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	app.orch.Shutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown failed", "error", err)
	}
	slog.Info("Server exited gracefully")
}

func (a *App) serveStatic(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	if r.URL.Path == "/" {
		data, err := staticAssets.ReadFile("web/static/index.html")
		if err != nil {
			http.Error(w, "File not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/static/") {
		filePath := "web" + r.URL.Path
		data, err := staticAssets.ReadFile(filePath)
		if err != nil {
			http.Error(w, "File not found", 404)
			return
		}

		if strings.HasSuffix(filePath, ".css") {
			w.Header().Set("Content-Type", "text/css")
		} else if strings.HasSuffix(filePath, ".js") {
			w.Header().Set("Content-Type", "application/javascript")
		}
		_, _ = w.Write(data)
		return
	}

	http.NotFound(w, r)
}

func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (a *App) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if err := a.cfg.Load(); err != nil {
		slog.Warn("Failed to reload configuration", "error", err)
	}
	a.orch.SyncTargets(a.cfg.Get().Accounts)
	_ = json.NewEncoder(w).Encode(a.cfg.Get())
}

func (a *App) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	oldCfg := a.cfg.Get()
	for i, newAcc := range cfg.Accounts {
		var oldAcc *config.Account
		for _, acc := range oldCfg.Accounts {
			if strings.EqualFold(acc.Username, newAcc.Username) {
				oldAcc = &acc
				break
			}
		}
		if oldAcc != nil {
			cfg.Accounts[i].LastSyncMode = oldAcc.LastSyncMode
			if !accountsEqual(*oldAcc, newAcc) {
				cfg.Accounts[i].LastSyncStatus = "idle"
				cfg.Accounts[i].LastSyncTime = time.Time{}
			} else {
				cfg.Accounts[i].LastSyncStatus = oldAcc.LastSyncStatus
				cfg.Accounts[i].LastSyncTime = oldAcc.LastSyncTime
			}
		}
	}

	if err := a.cfg.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.orch.SyncTargets(cfg.Accounts)

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type ProgressResponse struct {
	Targets          []orchestrator.TaskProgress `json:"targets"`
	LastSync         string                      `json:"last_sync"`
	AutoSyncInterval int                         `json:"auto_sync_interval"`
}

func (a *App) handleProgress(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	c := a.cfg.Get()
	targets := a.orch.GetAllProgress(c.Accounts)

	resp := ProgressResponse{
		Targets:          targets,
		LastSync:         a.orch.LastSyncTime().Format(time.RFC3339),
		AutoSyncInterval: c.AutoSyncInterval,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

type scrapeRequest struct {
	Username string `json:"username"`
}

type GalleryFile struct {
	Filename     string `json:"filename"`
	Type         string `json:"type"` // "image" or "video"
	Date         string `json:"date"`
	Size         int64  `json:"size"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type PostMediaFile struct {
	Filename     string `json:"filename"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type GalleryPost struct {
	TweetID     string          `json:"tweet_id"`
	Date        string          `json:"date"`
	Text        string          `json:"text"`
	MediaURLs   []string        `json:"media_urls"`
	LocalFiles  []PostMediaFile `json:"local_files"`
	YoutubeURLs []string        `json:"youtube_urls,omitempty"`
}

type GalleryResponse struct {
	Username string        `json:"username"`
	Platform string        `json:"platform"`
	Files    []GalleryFile `json:"files"`
	Posts    []GalleryPost `json:"posts"`
}

func (a *App) handleGallery(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	platform := r.URL.Query().Get("platform")
	username := r.URL.Query().Get("username")
	if platform == "" || username == "" {
		http.Error(w, "Missing platform or username", http.StatusBadRequest)
		return
	}

	dir := filepath.Join("downloads", platform, username)

	entries := a.mediaIndex.Get(platform, username)

	var files []GalleryFile
	fileIndex := make(map[string]int)
	thumbDir := filepath.Join(dir, "thumbnails")

	for _, e := range entries {
		name := e.Filename
		date := ""
		if len(name) >= 10 {
			date = name[:10]
		}

		ext := filepath.Ext(name)
		thumbFilename := strings.TrimSuffix(name, ext) + ".jpg"
		thumbnailURL := "/media/" + platform + "/" + username + "/thumbnails/" + thumbFilename

		thumbPath := filepath.Join(thumbDir, thumbFilename)
		go func(src, dst string) {
			if info, err := os.Stat(dst); err != nil || (err == nil && info.Size() == 0) {
				_ = scraper.GenerateThumbnail(src, dst)
			}
		}(filepath.Join(dir, name), thumbPath)

		i := len(files)
		fileIndex[name] = i
		files = append(files, GalleryFile{
			Filename:     name,
			Type:         e.Type,
			Date:         date,
			Size:         e.Size,
			URL:          "/media/" + platform + "/" + username + "/" + name,
			ThumbnailURL: thumbnailURL,
		})
	}

	for i, f := range files {
		fileIndex[f.Filename] = i
	}

	var posts []GalleryPost
	postsPath := filepath.Join(dir, "posts.json")
	if data, err := os.ReadFile(postsPath); err == nil {
		var rawPosts []GalleryPost
		if json.Unmarshal(data, &rawPosts) == nil {
			for i, p := range rawPosts {
				var localFiles []PostMediaFile
				for _, mediaURL := range p.MediaURLs {
					parsed, err := url.Parse(mediaURL)
					if err != nil {
						continue
					}
					parts := strings.Split(parsed.Path, "/")
					if len(parts) == 0 {
						continue
					}
					baseName := parts[len(parts)-1]
					if strings.Contains(baseName, "?") {
						baseName = strings.Split(baseName, "?")[0]
					}

					if idx, ok := fileIndex[baseName]; ok {
						localFiles = append(localFiles, PostMediaFile{
							Filename:     files[idx].Filename,
							URL:          files[idx].URL,
							ThumbnailURL: files[idx].ThumbnailURL,
						})
						continue
					}
					videoName := p.TweetID + "_video.mp4"
					if idx, ok := fileIndex[videoName]; ok {
						localFiles = append(localFiles, PostMediaFile{
							Filename:     files[idx].Filename,
							URL:          files[idx].URL,
							ThumbnailURL: files[idx].ThumbnailURL,
						})
						continue
					}
					for _, f := range files {
						if baseName != "" && strings.Contains(f.Filename, baseName) {
							localFiles = append(localFiles, PostMediaFile{
								Filename:     f.Filename,
								URL:          f.URL,
								ThumbnailURL: f.ThumbnailURL,
							})
							break
						}
					}
				}
				rawPosts[i].LocalFiles = localFiles
			}
			slices.SortFunc(rawPosts, func(a, b GalleryPost) int {
				return cmp.Compare(b.Date, a.Date)
			})
			posts = rawPosts
		}
	}
	if posts == nil {
		posts = []GalleryPost{}
	}

	_ = json.NewEncoder(w).Encode(GalleryResponse{
		Username: username,
		Platform: platform,
		Files:    files,
		Posts:    posts,
	})
}

func (a *App) handleMedia(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/media/")
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	filePath := filepath.Join("downloads", filepath.FromSlash(cleaned))
	http.ServeFile(w, r, filePath)
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, unsubscribe := a.orch.Subscribe()
	defer unsubscribe()

	_, _ = fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (a *App) handleScrapeStart(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)

	var req scrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c := a.cfg.Get()

	if req.Username == "all" {
		for _, acc := range c.Accounts {
			a.orch.StartScrape(acc.Username, acc.Platform, acc.SaveText)
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

	a.orch.StartScrape(targetAccount.Username, targetAccount.Platform, targetAccount.SaveText)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "target": targetAccount.Username})
}

type clearRequest struct {
	Username string `json:"username"`
	Platform string `json:"platform"`
}

func (a *App) handleScrapeCancel(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)

	var req scrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		http.Error(w, "Missing username", http.StatusBadRequest)
		return
	}

	ok := a.orch.CancelScrape(req.Username)
	w.Header().Set("Content-Type", "application/json")
	if ok {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled", "target": req.Username})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_running", "target": req.Username})
	}
}

func (a *App) handleScrapeClear(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)

	var req clearRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Platform == "" {
		http.Error(w, "Missing platform or username", http.StatusBadRequest)
		return
	}

	dir := filepath.Join("downloads", req.Platform, req.Username)
	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	orchestrator.SavePersistedSyncInfo(a.cfg, req.Username, "idle", time.Time{})
	orchestrator.SavePersistedSyncMode(a.cfg, req.Username, "")
	a.mediaIndex.Invalidate(req.Platform, req.Username)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleared", "target": req.Username})
}

func accountsEqual(a, b config.Account) bool {
	if a.Username != b.Username || a.Platform != b.Platform || a.SaveText != b.SaveText || a.SkipRetweets != b.SkipRetweets {
		return false
	}
	if len(a.Filters) != len(b.Filters) {
		return false
	}
	for i := range a.Filters {
		if a.Filters[i] != b.Filters[i] {
			return false
		}
	}
	if (a.DownloadPhotos == nil) != (b.DownloadPhotos == nil) {
		return false
	}
	if a.DownloadPhotos != nil && *a.DownloadPhotos != *b.DownloadPhotos {
		return false
	}
	if (a.DownloadVideos == nil) != (b.DownloadVideos == nil) {
		return false
	}
	if a.DownloadVideos != nil && *a.DownloadVideos != *b.DownloadVideos {
		return false
	}
	return true
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
}

func parseGalleryParams(r *http.Request) galleryPageParams {
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/gallery/"), "/")
	p := galleryPageParams{
		Platform: pathParts[0],
		Username: pathParts[1],
		Sort:     r.URL.Query().Get("sort"),
		Search:   r.URL.Query().Get("q"),
		Year:     r.URL.Query().Get("year"),
		Month:    r.URL.Query().Get("month"),
		Dir:      filepath.Join("downloads", pathParts[0], pathParts[1]),
	}

	var pageStr string
	if len(pathParts) >= 4 && pathParts[2] == "page" {
		pageStr = pathParts[3]
	} else if len(pathParts) >= 5 && pathParts[2] == "posts" && pathParts[3] == "page" {
		pageStr = pathParts[4]
	} else if len(pathParts) >= 4 && pathParts[2] == "posts" {
		pageStr = "1"
	} else if len(pathParts) == 2 {
		pageStr = "1"
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	p.Page = page
	return p
}

// handleGalleryPage renders a gallery grid or posts page
func (a *App) handleGalleryPage(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/gallery/"), "/")
	if len(pathParts) < 2 {
		http.Error(w, "Invalid gallery path", http.StatusBadRequest)
		return
	}

	p := parseGalleryParams(r)

	platform := p.Platform
	username := p.Username
	page := p.Page
	dir := p.Dir
	search := p.Search
	sortParam := p.Sort
	year := p.Year
	month := p.Month

	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}

	entries := a.mediaIndex.Get(platform, username)
	thumbDir := filepath.Join(dir, "thumbnails")

	var allFiles []templates.GalleryFileData
	for _, e := range entries {
		name := e.Filename
		date := ""
		if len(name) >= 10 {
			date = name[:10]
		}
		ext := filepath.Ext(name)
		thumbFilename := strings.TrimSuffix(name, ext) + ".jpg"
		thumbURL := "/media/" + platform + "/" + username + "/thumbnails/" + thumbFilename
		mediaURL := "/media/" + platform + "/" + username + "/" + name

		thumbPath := filepath.Join(thumbDir, thumbFilename)
		go func(src, dst string) {
			if info, err := os.Stat(dst); err != nil || (err == nil && info.Size() == 0) {
				_ = scraper.GenerateThumbnail(src, dst)
			}
		}(filepath.Join(dir, name), thumbPath)

		allFiles = append(allFiles, templates.GalleryFileData{
			Filename:     name,
			Type:         e.Type,
			Date:         date,
			Size:         e.Size,
			URL:          mediaURL,
			ThumbnailURL: thumbURL,
		})
	}

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
		search = strings.ToLower(search)
		filtered := allFiles[:0]
		for _, f := range allFiles {
			if strings.Contains(strings.ToLower(f.Filename), search) || strings.Contains(f.Date, search) {
				filtered = append(filtered, f)
			}
		}
		allFiles = filtered
	}

	var years []string
	if year != "" && year != "all" {
		years = strings.Split(year, ",")
	}
	var months []string
	if month != "" && month != "all" {
		months = strings.Split(month, ",")
	}

	if len(years) > 0 || len(months) > 0 {
		filtered := allFiles[:0]
		for _, f := range allFiles {
			yearMatch := len(years) == 0
			if len(years) > 0 && len(f.Date) >= 4 {
				for _, y := range years {
					if f.Date[:4] == y {
						yearMatch = true
						break
					}
				}
			}
			monthMatch := len(months) == 0
			if len(months) > 0 && len(f.Date) >= 7 {
				for _, m := range months {
					if f.Date[5:7] == m {
						monthMatch = true
						break
					}
				}
			}
			if yearMatch && monthMatch {
				filtered = append(filtered, f)
			}
		}
		allFiles = filtered
	}

	if sortParam == "asc" {
		for i, j := 0, len(allFiles)-1; i < j; i, j = i+1, j-1 {
			allFiles[i], allFiles[j] = allFiles[j], allFiles[i]
		}
	}

	isPosts := len(pathParts) >= 4 && pathParts[2] == "posts"
	if isPosts {
		a.handleGalleryPostsPage(w, r, p)
		return
	}

	total := len(allFiles)
	totalPages := (total + galleryPageSize - 1) / galleryPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	offset := (page - 1) * galleryPageSize
	end := offset + galleryPageSize
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	pageFiles := allFiles[offset:end]
	templ.Handler(templates.GalleryGridPage(pageFiles, platform, username, filter, search, sortParam, year, month, page, totalPages)).ServeHTTP(w, r)
}

func (a *App) handleGalleryPostsPage(w http.ResponseWriter, r *http.Request, gp galleryPageParams) {
	postsPath := filepath.Join(gp.Dir, "posts.json")
	data, err := os.ReadFile(postsPath)
	if err != nil {
		templ.Handler(templates.GalleryPostsPage(nil, gp.Platform, gp.Username, gp.Sort, gp.Search, gp.Year, gp.Month, 1, 1)).ServeHTTP(w, r)
		return
	}

	var rawPosts []GalleryPost
	if json.Unmarshal(data, &rawPosts) != nil {
		templ.Handler(templates.GalleryPostsPage(nil, gp.Platform, gp.Username, gp.Sort, gp.Search, gp.Year, gp.Month, 1, 1)).ServeHTTP(w, r)
		return
	}

	entries := a.mediaIndex.Get(gp.Platform, gp.Username)
	fileIndex := make(map[string]templates.GalleryFileData)
	for _, e := range entries {
		name := e.Filename
		ext := filepath.Ext(name)
		thumbFilename := strings.TrimSuffix(name, ext) + ".jpg"
		fileIndex[name] = templates.GalleryFileData{
			Filename:     name,
			Type:         e.Type,
			URL:          "/media/" + gp.Platform + "/" + gp.Username + "/" + name,
			ThumbnailURL: "/media/" + gp.Platform + "/" + gp.Username + "/thumbnails/" + thumbFilename,
		}
	}

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
			parsed, err := url.Parse(mediaURL)
			if err != nil {
				continue
			}
			parts := strings.Split(parsed.Path, "/")
			if len(parts) == 0 {
				continue
			}
			baseName := parts[len(parts)-1]
			if strings.Contains(baseName, "?") {
				baseName = strings.Split(baseName, "?")[0]
			}

			var matched *templates.GalleryFileData
			if fd, ok := fileIndex[baseName]; ok {
				matched = &fd
			}
			if matched == nil {
				videoName := p.TweetID + "_video.mp4"
				if fd, ok := fileIndex[videoName]; ok {
					matched = &fd
				}
			}
			if matched == nil {
				for _, fd := range fileIndex {
					if baseName != "" && strings.Contains(fd.Filename, baseName) {
						matched = &fd
						break
					}
				}
			}
			if matched != nil {
				localFiles = append(localFiles, templates.GalleryPostMediaFile{
					Filename:     matched.Filename,
					URL:          matched.URL,
					ThumbnailURL: matched.ThumbnailURL,
					IsVideo:      matched.Type == "video",
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

	var years []string
	if gp.Year != "" && gp.Year != "all" {
		years = strings.Split(gp.Year, ",")
	}
	var months []string
	if gp.Month != "" && gp.Month != "all" {
		months = strings.Split(gp.Month, ",")
	}

	if len(years) > 0 || len(months) > 0 {
		filtered := allPosts[:0]
		for _, p := range allPosts {
			yearMatch := len(years) == 0
			if len(years) > 0 && len(p.DateLabel) >= 4 {
				for _, y := range years {
					if p.DateLabel[:4] == y {
						yearMatch = true
						break
					}
				}
			}
			monthMatch := len(months) == 0
			if len(months) > 0 && len(p.DateLabel) >= 7 {
				for _, m := range months {
					if p.DateLabel[5:7] == m {
						monthMatch = true
						break
					}
				}
			}
			if yearMatch && monthMatch {
				filtered = append(filtered, p)
			}
		}
		allPosts = filtered
	}

	total := len(allPosts)
	totalPages := (total + galleryPageSize - 1) / galleryPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	offset := (gp.Page - 1) * galleryPageSize
	end := offset + galleryPageSize
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	pagePosts := allPosts[offset:end]

	templ.Handler(templates.GalleryPostsPage(pagePosts, gp.Platform, gp.Username, gp.Sort, gp.Search, gp.Year, gp.Month, gp.Page, totalPages)).ServeHTTP(w, r)
}
