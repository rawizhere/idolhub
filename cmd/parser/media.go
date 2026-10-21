package main

import (
	"archive/zip"
	"cmp"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"idolhub/internal/config"
)

const (
	recentDefaultLimit = 24
	recentMaxLimit     = 100
	maxBulkFiles       = 2000
)

// handleRecent lists the newest files across every target for the overview strip.
func (a *App) handleRecent(w http.ResponseWriter, r *http.Request) {
	limit := recentDefaultLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, recentMaxLimit)
	}

	var out []GlobalSearchResultFile
	for _, acc := range config.GetConfig().Accounts {
		files := a.mediaIndex.View(acc.Platform, acc.Username)
		if len(files) == 0 {
			continue
		}
		captions := a.mediaIndex.FilePostText(acc.Platform, acc.Username)
		for _, f := range files {
			out = append(out, GlobalSearchResultFile{
				Platform:     acc.Platform,
				Username:     acc.Username,
				Filename:     f.Filename,
				Type:         f.Type,
				Date:         f.Date,
				Size:         f.Size,
				URL:          f.URL,
				ThumbnailURL: f.ThumbnailURL,
				Caption:      captions[f.Filename],
			})
		}
	}
	// filenames start with the date, so they break same-day ties newest-first too
	slices.SortFunc(out, func(x, y GlobalSearchResultFile) int {
		return cmp.Or(cmp.Compare(y.Date, x.Date), cmp.Compare(y.Filename, x.Filename))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []GlobalSearchResultFile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

// targetFiles validates a bulk request and returns the on-disk paths of the selected files.
func targetFiles(platform, username string, names []string) ([]string, error) {
	if !validSegment(platform) || !validSegment(username) {
		return nil, errors.New("invalid platform or username")
	}
	if len(names) == 0 || len(names) > maxBulkFiles {
		return nil, errors.New("select between 1 and " + strconv.Itoa(maxBulkFiles) + " files")
	}
	dir := filepath.Join("downloads", platform, username)
	paths := make([]string, 0, len(names))
	for _, n := range names {
		if !validSegment(n) {
			return nil, errors.New("invalid filename: " + n)
		}
		paths = append(paths, filepath.Join(dir, n))
	}
	return paths, nil
}

// handleMediaZip streams the selected files as an uncompressed zip; media is already compressed.
// It takes a regular form post so the browser handles the download natively.
func (a *App) handleMediaZip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	platform, username := r.PostForm.Get("platform"), r.PostForm.Get("username")
	paths, err := targetFiles(platform, username, r.PostForm["file"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+username+"-"+strconv.Itoa(len(paths))+`-files.zip"`)
	zw := zip.NewWriter(w)
	for _, p := range paths {
		if err := addZipFile(zw, p); err != nil {
			// headers are gone already; a truncated archive is the only signal left
			slog.Warn("Zip export aborted", "file", p, "error", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		slog.Warn("Zip export failed to finish", "error", err)
	}
}

func addZipFile(zw *zip.Writer, p string) error {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Method = zip.Store
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, f)
	return err
}

type mediaDeleteRequest struct {
	Platform string   `json:"platform"`
	Username string   `json:"username"`
	Files    []string `json:"files"`
}

// handleMediaDelete removes selected files and their thumbnails. A later full resync may fetch them again.
func (a *App) handleMediaDelete(w http.ResponseWriter, r *http.Request) {
	var req mediaDeleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	paths, err := targetFiles(req.Platform, req.Username, req.Files)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	deleted := 0
	for i, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("Failed to delete media file", "file", p, "error", err)
			continue
		}
		deleted++
		base := strings.TrimSuffix(req.Files[i], filepath.Ext(req.Files[i]))
		thumbRel := path.Join(req.Platform, req.Username, "thumbnails", base+".jpg")
		for _, t := range []string{a.thumbnailFilePath(thumbRel), filepath.Join("downloads", filepath.FromSlash(thumbRel))} {
			_ = os.Remove(t)
		}
	}
	slog.Info("Deleted selected media", "user", req.Username, "platform", req.Platform, "count", deleted)
	a.mediaIndex.Invalidate(req.Platform, req.Username)

	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}
