package scraper

import (
	"context"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idolhub/internal/store"
)

// igDuplicatesDir holds Instagram videos that earlier builds downloaded again under a fresh CDN token. It sits under
// downloads/ like the integrity quarantine, so the gallery and the other migrations skip it; nothing is deleted.
const igDuplicatesDir = ".duplicates"

// igDateLen is the length of the <date>_ prefix of every Instagram file name.
var igDateLen = len(igDatePrefix(time.Time{}))

// MigrateInstagramVideoNames moves the Instagram library to media-id video names. For every stored post with a single
// video, the file its post_media row points at (or, when that url was never downloaded, the newest CDN-token copy of
// the post) is renamed <date>_<post id>.mp4 and the row follows; then every
// CDN-token video that no row points at, but that shares its date with one a row does point at, is a re-download of
// that post and moves to downloads/.duplicates. Carousels with several videos keep their names (unlinked rows are
// pointed at the post's copies when the counts match) until a sync fetches
// them under slide ids, after which the next run moves their old copies away. Safe to run on every start.
// It returns the accounts whose files changed, for the gallery cache.
func MigrateInstagramVideoNames(ctx context.Context, posts *store.PostStore, thumbsDir string) []string {
	root := filepath.Join(mediaRoot, "instagram")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var changed []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fixed, moved, err := migrateIgAccount(ctx, posts, thumbsDir, e.Name())
		if err != nil {
			slog.Warn("Instagram video name migration failed", "user", e.Name(), "error", err)
		}
		if fixed+moved > 0 {
			changed = append(changed, e.Name())
			slog.Info("Instagram video name migration", "user", e.Name(), "fixed", fixed, "duplicates_moved", moved)
		}
	}
	return changed
}

func migrateIgAccount(ctx context.Context, posts *store.PostStore, thumbsDir, username string) (fixed, moved int, err error) {
	dir := filepath.Join(mediaRoot, "instagram", username)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	files := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			files[e.Name()] = true
		}
	}
	rows, err := posts.ListByAccount(ctx, "instagram", username, 100000)
	if err != nil {
		return 0, 0, err
	}

	referenced := map[string]bool{}
	for _, p := range rows {
		prefix := igDatePrefix(p.PostedAt)
		var videos []store.PostMedia
		for _, m := range p.Media {
			if m.Kind == "video" {
				videos = append(videos, m)
			}
		}
		resolved := make([]string, len(videos))
		allMissing := true
		for i, m := range videos {
			resolved[i] = igMediaFile(files, prefix, m.URL)
			allMissing = allMissing && resolved[i] == ""
		}
		// Rows whose rotated url was never fetched leave the post's older copies unlinked. Pair them up when nothing
		// else can be meant: the newest copy for a lone video, one copy per row when the counts match.
		if allMissing {
			legacy := igLegacyVideos(dir, slices.Collect(maps.Keys(files)), prefix)
			switch {
			case len(videos) == 1 && len(legacy) > 0:
				resolved[0] = legacy[len(legacy)-1]
			case len(videos) > 1 && len(legacy) == len(videos):
				copy(resolved, legacy)
			}
		}
		for i, m := range videos {
			name := resolved[i]
			if name == "" {
				continue
			}
			newURL := ""
			if want := prefix + p.ExternalID + ".mp4"; len(videos) == 1 && name != want && !files[want] {
				if err := renameIgVideo(dir, thumbsDir, username, name, want); err != nil {
					slog.Warn("Failed to rename Instagram video", "file", name, "error", err)
				} else {
					delete(files, name)
					files[want] = true
					name, newURL = want, p.ExternalID+".mp4"
				}
			} else if allMissing {
				newURL = name[igDateLen:]
			}
			if newURL != "" && newURL != m.URL {
				if err := posts.RenameMedia(ctx, p.ID, m.URL, newURL); err != nil {
					return fixed, moved, err
				}
				fixed++
			}
			referenced[name] = true
		}
	}

	twinDates := map[string]bool{}
	for name := range referenced {
		if len(name) > igDateLen {
			twinDates[name[:igDateLen]] = true
		}
	}
	dupDir := filepath.Join(mediaRoot, igDuplicatesDir, "instagram", username)
	for name := range files {
		if referenced[name] || len(name) <= igDateLen {
			continue
		}
		prefix := name[:igDateLen]
		if !twinDates[prefix] || len(igLegacyVideos(dir, []string{name}, prefix)) == 0 {
			continue
		}
		if err := os.MkdirAll(dupDir, 0755); err != nil {
			return fixed, moved, err
		}
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dupDir, name)); err != nil {
			slog.Warn("Failed to move duplicate Instagram video", "file", name, "error", err)
			continue
		}
		for _, t := range igThumbPaths(thumbsDir, username, name) {
			_ = os.Remove(t)
		}
		moved++
	}
	return fixed, moved, nil
}

// igMediaFile finds the local file of a post_media url the way the gallery does: by the url's base name.
func igMediaFile(files map[string]bool, prefix, mediaURL string) string {
	parsed, err := url.Parse(mediaURL)
	if err != nil || parsed.Path == "" {
		return ""
	}
	base := path.Base(parsed.Path)
	if files[prefix+base] {
		return prefix + base
	}
	for name := range files {
		if strings.Contains(name, base) {
			return name
		}
	}
	return ""
}

func renameIgVideo(dir, thumbsDir, username, from, to string) error {
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		return err
	}
	// carry the previews along; a missing one is generated on demand
	newThumbs := igThumbPaths(thumbsDir, username, to)
	for i, t := range igThumbPaths(thumbsDir, username, from) {
		_ = os.Rename(t, newThumbs[i])
	}
	return nil
}

// igThumbPaths lists the preview locations of a media file: the fast thumbnails dir and the legacy in-tree one.
func igThumbPaths(thumbsDir, username, name string) []string {
	thumb := strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
	paths := []string{filepath.Join(mediaRoot, "instagram", username, "thumbnails", thumb)}
	if thumbsDir != "" {
		paths = append(paths, filepath.Join(thumbsDir, "instagram", username, "thumbnails", thumb))
	}
	return paths
}
