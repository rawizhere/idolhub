package main

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"idolhub/internal/config"
)

const quarantineDir = "downloads/.quarantine"

type dirUsage struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

type targetUsage struct {
	Platform   string `json:"platform"`
	Username   string `json:"username"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	ThumbBytes int64  `json:"thumb_bytes"`
}

// usage sums regular files under dir; thumbnails are reported separately so media size stays meaningful.
func usage(dir string) (media, thumbs dirUsage) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "thumbnails" {
			thumbs.Files++
			thumbs.Bytes += info.Size()
			return nil
		}
		media.Files++
		media.Bytes += info.Size()
		return nil
	})
	return media, thumbs
}

func (a *App) handleStorage(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()
	resp := struct {
		Targets       []targetUsage `json:"targets"`
		TotalBytes    int64         `json:"total_bytes"`
		FreeBytes     int64         `json:"free_bytes,omitempty"`
		Quarantine    dirUsage      `json:"quarantine"`
		ThumbnailsDir string        `json:"thumbnails_dir"`
		SidecarURL    string        `json:"instagram_sidecar_url"`
	}{Targets: []targetUsage{}, ThumbnailsDir: cfg.ThumbnailsDir, SidecarURL: cfg.InstagramSidecarURL}

	for _, acc := range cfg.Accounts {
		media, thumbs := usage(filepath.Join("downloads", acc.Platform, acc.Username))
		if cfg.ThumbnailsDir != "" {
			_, fast := usage(filepath.Join(cfg.ThumbnailsDir, acc.Platform, acc.Username))
			thumbs.Bytes += fast.Bytes
		}
		resp.Targets = append(resp.Targets, targetUsage{Platform: acc.Platform, Username: acc.Username, Files: media.Files, Bytes: media.Bytes, ThumbBytes: thumbs.Bytes})
		resp.TotalBytes += media.Bytes + thumbs.Bytes
	}
	q, qt := usage(quarantineDir)
	resp.Quarantine = dirUsage{Files: q.Files + qt.Files, Bytes: q.Bytes + qt.Bytes}

	var st syscall.Statfs_t
	if err := syscall.Statfs("downloads", &st); err == nil {
		resp.FreeBytes = int64(st.Bavail) * int64(st.Bsize)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleQuarantineClear deletes media set aside by the integrity check; its targets were already re-queued for a full sync.
func (a *App) handleQuarantineClear(w http.ResponseWriter, r *http.Request) {
	if err := os.RemoveAll(quarantineDir); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("Cleared quarantined media")
	w.WriteHeader(http.StatusNoContent)
}
