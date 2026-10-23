package scraper

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MigrateTikTokNames renames every video of every account to the canonical <date>_<id> name and returns the accounts
// whose files changed, for the gallery cache. Safe to run on every start.
func MigrateTikTokNames(thumbsDir string) []string {
	root := filepath.Join(mediaRoot, "tiktok")
	accounts, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var changed []string
	for _, a := range accounts {
		if !a.IsDir() || strings.HasPrefix(a.Name(), ".") {
			continue
		}
		SeedArchiveFromFiles("tiktok", a.Name())
		if n := NormalizeTikTokNames(filepath.Join(root, a.Name()), thumbsDir, a.Name()); n > 0 {
			changed = append(changed, a.Name())
			slog.Info("TikTok file name migration", "user", a.Name(), "renamed", n)
		}
	}
	return changed
}

// NormalizeTikTokNames gives every video in one account dir the canonical <date>_<id> name derived from the id itself:
// the upper 32 bits carry creation time. Names built from TikTok's reported timestamp drift between requests (seconds),
// so yt-dlp can miss an existing file and download it again; canonical names never change. A file whose canonical name
// already exists is a duplicate and is left in place. Returns the number of renames.
func NormalizeTikTokNames(dir, thumbsDir, username string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	renamed := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.Contains(e.Name(), ".transcoding.") {
			continue
		}
		id, ext, ok := ttVideoID(e.Name())
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(id, 10, 64)
		want := igDatePrefix(time.Unix(int64(n>>32), 0)) + id + "." + ext
		if want == e.Name() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, want)); err == nil {
			continue
		}
		if err := renameMedia(dir, thumbsDir, "tiktok", username, e.Name(), want); err != nil {
			slog.Warn("Failed to rename TikTok video", "file", e.Name(), "error", err)
			continue
		}
		renamed++
	}
	return renamed
}

// ttVideoID extracts the numeric video id and extension from an id-only (7699367800736845063.mp4) or dated
// (2025-02-10_16-05-29_7699367800736845063.mp4) file name. Returns ok=false for anything else.
func ttVideoID(name string) (id, ext string, ok bool) {
	id, ext, _ = strings.Cut(name, ".")
	if i := strings.LastIndex(id, "_"); i >= 0 {
		id = id[i+1:]
	}
	if len(id) < 15 || ext == "" {
		return "", "", false
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return "", "", false
	}
	return id, ext, true
}

// MediaArchivePath is the yt-dlp download archive of one target: entries are skipped by id, so a metadata timestamp
// drift cannot cause a re-download the way a name mismatch does. Dot-prefixed so media walks skip it.
func MediaArchivePath(platform, username string) string {
	return filepath.Join(mediaRoot, platform, "."+username+".archive.txt")
}

// SeedArchiveFromFiles records every id-named video already on disk in the target's download archive. Without it the
// first sync after the archive was introduced would re-download the whole library once. TikTok only: its numeric ids
// are recoverable from the file name; other platforms seed from yt-dlp's own listing.
func SeedArchiveFromFiles(platform, username string) {
	dir := filepath.Join(mediaRoot, platform, username)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	if b, err := os.ReadFile(MediaArchivePath(platform, username)); err == nil {
		for _, line := range strings.Fields(string(b)) {
			seen[strings.TrimPrefix(line, platform+" ")] = true
		}
	}
	var add []string
	for _, e := range entries {
		if id, _, ok := ttVideoID(e.Name()); ok && !seen[id] {
			seen[id] = true
			add = append(add, platform+" "+id)
		}
	}
	writeArchiveSeed(platform, username, add)
}

// seedArchiveIDs appends yt-dlp archive lines for ids the extractor actually saw.
func seedArchiveIDs(platform, username string, ids []string) {
	seen := map[string]bool{}
	if b, err := os.ReadFile(MediaArchivePath(platform, username)); err == nil {
		for _, line := range strings.Fields(string(b)) {
			seen[strings.TrimPrefix(line, platform+" ")] = true
		}
	}
	var add []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			add = append(add, platform+" "+id)
		}
	}
	writeArchiveSeed(platform, username, add)
}

func writeArchiveSeed(platform, username string, add []string) {
	if len(add) == 0 {
		return
	}
	f, err := os.OpenFile(MediaArchivePath(platform, username), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("Failed to open download archive", "platform", platform, "user", username, "error", err)
		return
	}
	_, err = f.WriteString(strings.Join(add, "\n") + "\n")
	if err2 := f.Close(); err == nil {
		err = err2
	}
	if err != nil {
		slog.Warn("Failed to write download archive", "platform", platform, "user", username, "error", err)
		return
	}
	slog.Info("Seeded download archive", "platform", platform, "user", username, "ids", len(add))
}
