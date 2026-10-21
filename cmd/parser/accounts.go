package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idolhub/internal/config"
	"idolhub/internal/cookies"
	"idolhub/internal/orchestrator"
	"idolhub/internal/scraper"
	"idolhub/internal/store"
)

var platforms = []string{"instagram", "twitter", "tiktok"}

const (
	maxFilters  = 50
	maxPostsCap = 100000
)

// accountView is an account as the browser sees it: the per-account cookie export is reduced to a status.
type accountView struct {
	Username       string        `json:"username"`
	Platform       string        `json:"platform"`
	SaveText       bool          `json:"save_text"`
	SkipRetweets   bool          `json:"skip_retweets"`
	Filters        []string      `json:"filters"`
	DownloadPhotos bool          `json:"download_photos"`
	DownloadVideos bool          `json:"download_videos"`
	Paused         bool          `json:"paused"`
	Since          string        `json:"since"`
	MaxPosts       int           `json:"max_posts"`
	Cookies        *cookieStatus `json:"cookies,omitempty"`
}

func viewAccount(acc config.Account) accountView {
	v := accountView{
		Username:       acc.Username,
		Platform:       acc.Platform,
		SaveText:       acc.SaveText,
		SkipRetweets:   acc.SkipRetweets,
		Filters:        acc.Filters,
		DownloadPhotos: acc.ShouldDownloadPhotos(),
		DownloadVideos: acc.ShouldDownloadVideos(),
		Paused:         acc.Paused,
		Since:          acc.SinceDate,
		MaxPosts:       acc.MaxPosts,
	}
	if v.Filters == nil {
		v.Filters = []string{}
	}
	if acc.Cookies != "" {
		st := inspectCookies(acc.Platform, acc.Cookies)
		v.Cookies = &st
	}
	return v
}

// accountPatch carries optional fields; nil leaves the stored value alone. Cookies "" removes the account's own session.
type accountPatch struct {
	Username       *string   `json:"username"`
	SaveText       *bool     `json:"save_text"`
	SkipRetweets   *bool     `json:"skip_retweets"`
	Filters        *[]string `json:"filters"`
	DownloadPhotos *bool     `json:"download_photos"`
	DownloadVideos *bool     `json:"download_videos"`
	Paused         *bool     `json:"paused"`
	Since          *string   `json:"since"`
	MaxPosts       *int      `json:"max_posts"`
	Cookies        *string   `json:"cookies"`
}

// apply validates the patch and writes it onto acc. contentChanged reports edits that change which posts a sync
// keeps, so the watermark must be reset for the next sync to revisit older posts.
func (p accountPatch) apply(acc *config.Account) (contentChanged bool, err error) {
	if p.SaveText != nil && *p.SaveText != acc.SaveText {
		acc.SaveText, contentChanged = *p.SaveText, true
	}
	if p.SkipRetweets != nil && *p.SkipRetweets != acc.SkipRetweets {
		acc.SkipRetweets, contentChanged = *p.SkipRetweets, true
	}
	if p.DownloadPhotos != nil && *p.DownloadPhotos != acc.ShouldDownloadPhotos() {
		acc.DownloadPhotos, contentChanged = new(*p.DownloadPhotos), true
	}
	if p.DownloadVideos != nil && *p.DownloadVideos != acc.ShouldDownloadVideos() {
		acc.DownloadVideos, contentChanged = new(*p.DownloadVideos), true
	}
	if p.Filters != nil {
		filters, err := cleanFilters(*p.Filters)
		if err != nil {
			return false, err
		}
		if !slices.Equal(filters, acc.Filters) {
			acc.Filters, contentChanged = filters, true
		}
	}
	if p.Since != nil {
		since := strings.TrimSpace(*p.Since)
		if since != "" {
			d, err := time.Parse(time.DateOnly, since)
			if err != nil {
				return false, fmt.Errorf("since must be a YYYY-MM-DD date")
			}
			if d.After(time.Now()) {
				return false, fmt.Errorf("since cannot be in the future")
			}
		}
		if since != acc.SinceDate {
			acc.SinceDate, contentChanged = since, true
		}
	}
	if p.MaxPosts != nil {
		if *p.MaxPosts < 0 || *p.MaxPosts > maxPostsCap {
			return false, fmt.Errorf("max_posts must be between 0 and %d", maxPostsCap)
		}
		acc.MaxPosts = *p.MaxPosts
	}
	if p.Paused != nil {
		acc.Paused = *p.Paused
	}
	if p.Cookies != nil {
		raw, err := normaliseCookies(*p.Cookies)
		if err != nil {
			return false, err
		}
		acc.Cookies = raw
	}
	return contentChanged, nil
}

func cleanFilters(in []string) ([]string, error) {
	out := []string{}
	for _, f := range in {
		f = strings.TrimSpace(f)
		if f != "" && !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, f) }) {
			out = append(out, f)
		}
	}
	if len(out) > maxFilters {
		return nil, fmt.Errorf("at most %d filters", maxFilters)
	}
	return out, nil
}

// normaliseCookies accepts a Netscape or Cookie-Editor JSON export and rejects anything ParseNetscape cannot read.
func normaliseCookies(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "[") {
		converted, err := cookies.JSONToNetscape(raw)
		if err != nil {
			return "", err
		}
		raw = converted
	}
	if _, err := cookies.ParseNetscape(raw, ""); err != nil {
		return "", fmt.Errorf("cookie export: %w", err)
	}
	return raw, nil
}

func cleanUsername(raw string) (string, error) {
	u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "@"), "/")
	if !validSegment(u) {
		return "", fmt.Errorf("invalid username %q", raw)
	}
	return u, nil
}

func findAccount(accs []config.Account, platform, username string) int {
	return slices.IndexFunc(accs, func(a config.Account) bool {
		return a.Platform == platform && strings.EqualFold(a.Username, username)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *App) handleAccountsList(w http.ResponseWriter, r *http.Request) {
	accs := config.GetConfig().Accounts
	out := make([]accountView, 0, len(accs))
	for _, acc := range accs {
		out = append(out, viewAccount(acc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

type createAccountRequest struct {
	accountPatch
	Platform string `json:"platform"`
	SyncNow  bool   `json:"sync_now"`
}

func (a *App) handleAccountCreate(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := decodeJSON(w, r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	acc, status, err := a.createAccount(req)
	if err != nil {
		jsonError(w, status, err.Error())
		return
	}
	if req.SyncNow {
		a.orch.StartScrape(acc.Username, acc.Platform, false)
	}
	writeJSON(w, http.StatusCreated, viewAccount(acc))
}

func (a *App) createAccount(req createAccountRequest) (config.Account, int, error) {
	if !slices.Contains(platforms, req.Platform) {
		return config.Account{}, http.StatusBadRequest, fmt.Errorf("unknown platform %q", req.Platform)
	}
	if req.Username == nil {
		return config.Account{}, http.StatusBadRequest, errors.New("username is required")
	}
	username, err := cleanUsername(*req.Username)
	if err != nil {
		return config.Account{}, http.StatusBadRequest, err
	}
	acc := config.Account{Account: store.Account{Username: username, Platform: req.Platform, Filters: []string{}}, LastSyncStatus: "idle"}
	if _, err := req.apply(&acc); err != nil {
		return config.Account{}, http.StatusBadRequest, err
	}
	status := http.StatusInternalServerError
	cfg, err := config.UpdateAccounts(a.st, func(accs []config.Account) ([]config.Account, error) {
		if findAccount(accs, acc.Platform, acc.Username) >= 0 {
			status = http.StatusConflict
			return nil, fmt.Errorf("@%s is already configured on %s", acc.Username, acc.Platform)
		}
		return append(accs, acc), nil
	})
	if err != nil {
		return config.Account{}, status, err
	}
	a.orch.SyncTargets(cfg.Accounts)
	slog.Info("Added target", "user", acc.Username, "platform", acc.Platform)
	return acc, http.StatusCreated, nil
}

func (a *App) handleAccountPatch(w http.ResponseWriter, r *http.Request) {
	platform, username := r.PathValue("platform"), r.PathValue("username")
	var patch accountPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	if patch.Username != nil && *patch.Username != username {
		renamed, status, err := a.renameAccount(platform, username, *patch.Username)
		if err != nil {
			jsonError(w, status, err.Error())
			return
		}
		username = renamed
	}

	var (
		updated config.Account
		reset   bool
		status  = http.StatusInternalServerError
	)
	cfg, err := config.UpdateAccounts(a.st, func(accs []config.Account) ([]config.Account, error) {
		i := findAccount(accs, platform, username)
		if i < 0 {
			status = http.StatusNotFound
			return nil, errors.New("target not found")
		}
		changed, err := patch.apply(&accs[i])
		if err != nil {
			status = http.StatusBadRequest
			return nil, err
		}
		reset = changed
		updated = accs[i]
		return accs, nil
	})
	if err != nil {
		jsonError(w, status, err.Error())
		return
	}
	if reset {
		// filters, media types or the date bound changed: the next sync must revisit posts the old settings skipped
		a.orch.RequestFullResync(platform, updated.Username)
	}
	a.orch.SyncTargets(cfg.Accounts)
	writeJSON(w, http.StatusOK, viewAccount(updated))
}

// renameAccount moves a target to a new handle: its download folders, store rows and in-memory state.
func (a *App) renameAccount(platform, from, toRaw string) (string, int, error) {
	to, err := cleanUsername(toRaw)
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	cfg := config.GetConfig()
	i := findAccount(cfg.Accounts, platform, from)
	if i < 0 {
		return "", http.StatusNotFound, errors.New("target not found")
	}
	from = cfg.Accounts[i].Username
	if from == to {
		return to, http.StatusOK, nil
	}
	if j := findAccount(cfg.Accounts, platform, to); j >= 0 && j != i {
		return "", http.StatusConflict, fmt.Errorf("@%s is already configured on %s", to, platform)
	}
	if a.orch.IsActive(platform, from) {
		return "", http.StatusConflict, errors.New("wait for the running sync to finish before renaming")
	}

	dirs := [][2]string{{filepath.Join("downloads", platform, from), filepath.Join("downloads", platform, to)}}
	if td := cfg.ThumbnailsDir; td != "" {
		dirs = append(dirs, [2]string{filepath.Join(td, platform, from), filepath.Join(td, platform, to)})
	}
	for _, d := range dirs {
		// a case-only rename on a case-insensitive filesystem stats the same directory; that is fine to rename
		if _, err := os.Stat(d[1]); err == nil && !strings.EqualFold(d[0], d[1]) {
			return "", http.StatusConflict, fmt.Errorf("folder %s already exists", d[1])
		}
	}
	for _, d := range dirs {
		if err := os.Rename(d[0], d[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", http.StatusInternalServerError, fmt.Errorf("move %s: %w", d[0], err)
		}
	}
	if err := a.st.Accounts.Rename(context.Background(), platform, from, to); err != nil {
		return "", http.StatusInternalServerError, err
	}
	a.orch.RenameTarget(platform, from, to)
	if _, err := config.UpdateAccounts(a.st, func(accs []config.Account) ([]config.Account, error) {
		if k := findAccount(accs, platform, from); k >= 0 {
			accs[k].Username = to
		}
		return accs, nil
	}); err != nil {
		return "", http.StatusInternalServerError, err
	}
	a.mediaIndex.Invalidate(platform, from)
	a.mediaIndex.Invalidate(platform, to)
	slog.Info("Renamed target", "platform", platform, "from", from, "to", to)
	return to, http.StatusOK, nil
}

func (a *App) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	platform, username := r.PathValue("platform"), r.PathValue("username")
	status := http.StatusInternalServerError
	cfg, err := config.UpdateAccounts(a.st, func(accs []config.Account) ([]config.Account, error) {
		i := findAccount(accs, platform, username)
		if i < 0 {
			status = http.StatusNotFound
			return nil, errors.New("target not found")
		}
		username = accs[i].Username
		return slices.Delete(accs, i, i+1), nil
	})
	if err != nil {
		jsonError(w, status, err.Error())
		return
	}
	a.orch.CancelScrape(platform, username)
	a.orch.SyncTargets(cfg.Accounts)
	if err := a.st.Runs.DeleteAccount(r.Context(), platform, username); err != nil {
		slog.Warn("Failed to drop sync history", "user", username, "error", err)
	}
	slog.Info("Removed target", "user", username, "platform", platform)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleAccountRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.st.Runs.List(r.Context(), r.PathValue("platform"), r.PathValue("username"), 20)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// handleFilterPreview counts stored posts the given keyword filters would keep, matching the scraper's rule:
// a post is kept when its text contains any filter, case-insensitively.
func (a *App) handleFilterPreview(w http.ResponseWriter, r *http.Request) {
	platform, username := r.PathValue("platform"), r.PathValue("username")
	filters, err := cleanFilters(r.URL.Query()["f"])
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	posts := a.mediaIndex.Posts(platform, username)
	matched := 0
	for _, p := range posts {
		if scraper.MatchesFilters(p.Text, filters) {
			matched++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"total": len(posts), "matched": matched})
}

// handleLookup previews a profile before it is added. Errors carry a code the UI can explain.
func (a *App) handleLookup(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	username, err := cleanUsername(r.URL.Query().Get("username"))
	if err != nil || !slices.Contains(platforms, platform) {
		jsonError(w, http.StatusBadRequest, "invalid platform or username")
		return
	}
	var opts scraper.Options
	orchestrator.SessionOptions(config.GetConfig(), &opts)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	p, err := scraper.LookupProfile(ctx, platform, username, opts)
	if err != nil {
		code := "error"
		switch {
		case errors.Is(err, scraper.ErrProfileNotFound):
			code = "not_found"
		case errors.Is(err, scraper.ErrLookupUnsupported):
			code = "unsupported"
		case errors.Is(err, scraper.ErrAuthExpired):
			code = "auth"
		}
		writeJSON(w, http.StatusOK, map[string]string{"code": code, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": "ok", "profile": p})
}

// handleSessionCheck makes one authenticated request with the platform session, or an account's own one.
func (a *App) handleSessionCheck(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if !slices.Contains(platforms, platform) {
		jsonError(w, http.StatusBadRequest, "unknown platform")
		return
	}
	cfg := config.GetConfig()
	var opts scraper.Options
	orchestrator.SessionOptions(cfg, &opts)
	if u := r.URL.Query().Get("username"); u != "" {
		i := findAccount(cfg.Accounts, platform, u)
		if i < 0 {
			jsonError(w, http.StatusNotFound, "target not found")
			return
		}
		orchestrator.AccountSession(cfg.Accounts[i], &opts)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	who, err := scraper.CheckSession(ctx, platform, opts)
	switch {
	case errors.Is(err, scraper.ErrLookupUnsupported):
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "unsupported": true, "error": "online check is not available for " + platform})
	case err != nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account": who})
	}
}

/* ---------- export / import ---------- */

type accountsExport struct {
	ExportedAt time.Time     `json:"exported_at"`
	Accounts   []accountView `json:"accounts"`
	Settings   *syncSettings `json:"settings,omitempty"`
}

type syncSettings struct {
	AutoSyncInterval int    `json:"auto_sync_interval"`
	QuietHours       string `json:"quiet_hours"`
	MaxConcurrent    int    `json:"max_concurrent"`
}

// handleAccountsExport downloads the target list and sync settings; sessions and cookies are never exported.
func (a *App) handleAccountsExport(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()
	out := accountsExport{ExportedAt: time.Now().UTC(), Accounts: []accountView{}}
	for _, acc := range cfg.Accounts {
		v := viewAccount(acc)
		v.Cookies = nil
		out.Accounts = append(out.Accounts, v)
	}
	out.Settings = &syncSettings{AutoSyncInterval: cfg.AutoSyncInterval, QuietHours: cfg.QuietHours, MaxConcurrent: cfg.MaxConcurrent}
	w.Header().Set("Content-Disposition", `attachment; filename="idolhub-targets-`+time.Now().Format("2006-01-02")+`.json"`)
	writeJSON(w, http.StatusOK, out)
}

// handleAccountsImport adds targets that are not configured yet; existing ones are left untouched.
func (a *App) handleAccountsImport(w http.ResponseWriter, r *http.Request) {
	var in accountsExport
	if err := decodeJSON(w, r, &in); err != nil {
		jsonError(w, http.StatusBadRequest, "not an IdolHub export: "+err.Error())
		return
	}
	added, skipped := 0, 0
	var problems []string
	for _, v := range in.Accounts {
		username := v.Username
		filters := v.Filters
		req := createAccountRequest{Platform: v.Platform, accountPatch: accountPatch{
			Username: &username, SaveText: &v.SaveText, SkipRetweets: &v.SkipRetweets, Filters: &filters,
			DownloadPhotos: &v.DownloadPhotos, DownloadVideos: &v.DownloadVideos, Paused: &v.Paused, Since: &v.Since, MaxPosts: &v.MaxPosts,
		}}
		_, status, err := a.createAccount(req)
		switch {
		case status == http.StatusConflict:
			skipped++
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s/@%s: %v", v.Platform, v.Username, err))
		default:
			added++
		}
	}
	if in.Settings != nil && r.URL.Query().Get("settings") == "1" {
		if err := a.applySyncSettings(*in.Settings); err != nil {
			problems = append(problems, "settings: "+err.Error())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped, "problems": problems})
}

func (a *App) applySyncSettings(s syncSettings) error {
	if err := validateSyncSettings(s); err != nil {
		return err
	}
	cfg := config.GetConfig()
	cfg.AutoSyncInterval, cfg.QuietHours, cfg.MaxConcurrent = s.AutoSyncInterval, s.QuietHours, s.MaxConcurrent
	if err := config.SaveConfig(a.st, cfg); err != nil {
		return err
	}
	a.orch.ConcurrencyChanged()
	return nil
}

func validateSyncSettings(s syncSettings) error {
	if s.AutoSyncInterval < 0 || s.AutoSyncInterval > 168 {
		return errors.New("auto-sync interval must be between 0 and 168 hours")
	}
	if s.QuietHours != "" {
		if _, _, ok := config.ParseQuietHours(s.QuietHours); !ok {
			return errors.New("quiet hours must look like 23-07")
		}
	}
	if s.MaxConcurrent < 0 || s.MaxConcurrent > 8 {
		return errors.New("parallel syncs must be between 1 and 8")
	}
	return nil
}
