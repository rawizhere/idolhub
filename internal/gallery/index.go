package gallery

import (
	"cmp"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// MediaIndex provides access to downloaded media files
type MediaIndex interface {
	Get(platform, username string) []FileEntry
	Count(platform, username string) int
	Invalidate(platform, username string)
	InvalidateAll()
	Close()
}

// FileEntry describes a downloaded media file
type FileEntry struct {
	Filename string
	Type     string // "image" or "video"
	Size     int64
}

type Index struct {
	mu      sync.RWMutex
	files   map[string][]FileEntry // key: "platform/username"
	watcher *fsnotify.Watcher
	dirs    map[string]string // dir path → key
	stop    chan struct{}
}

var GlobalIndex *Index

func Init() {
	idx := &Index{
		files: make(map[string][]FileEntry),
		dirs:  make(map[string]string),
		stop:  make(chan struct{}),
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("fsnotify watcher unavailable, falling back to directory scans", "error", err)
	} else {
		idx.watcher = w
		go idx.watchLoop()
	}
	GlobalIndex = idx
}

func keyOf(platform, username string) string {
	return platform + "/" + username
}

func dirOf(platform, username string) string {
	return filepath.Join("downloads", platform, username)
}

func isMediaFile(name string) bool {
	if name == "posts.json" || name == ".DS_Store" {
		return false
	}
	return !strings.HasPrefix(name, ".")
}

func mediaType(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".mp4") {
		return "video"
	}
	return "image"
}

func scan(dir string) []FileEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []FileEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isMediaFile(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, FileEntry{
			Filename: name,
			Type:     mediaType(name),
			Size:     info.Size(),
		})
	}
	slices.SortFunc(files, func(a, b FileEntry) int {
		return cmp.Compare(b.Filename, a.Filename)
	})
	return files
}

func (idx *Index) ensureWatched(dir, key string) {
	if idx.watcher == nil {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.dirs[dir] != "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := idx.watcher.Add(dir); err != nil {
		slog.Debug("fsnotify watch add failed", "dir", dir, "error", err)
		return
	}
	idx.dirs[dir] = key
}

// Get returns cached file entries for a target
func (idx *Index) Get(platform, username string) []FileEntry {
	key := keyOf(platform, username)
	dir := dirOf(platform, username)

	idx.mu.RLock()
	if files, ok := idx.files[key]; ok {
		idx.mu.RUnlock()
		return files
	}
	idx.mu.RUnlock()

	files := scan(dir)
	if files == nil {
		files = []FileEntry{}
	}

	idx.mu.Lock()
	idx.files[key] = files
	idx.mu.Unlock()

	idx.ensureWatched(dir, key)
	return files
}

// Count returns the number of media files for a target
func (idx *Index) Count(platform, username string) int {
	return len(idx.Get(platform, username))
}

// Invalidate removes the cached file list for a target
func (idx *Index) Invalidate(platform, username string) {
	idx.mu.Lock()
	delete(idx.files, keyOf(platform, username))
	idx.mu.Unlock()
}

// InvalidateAll clears all cached entries
func (idx *Index) InvalidateAll() {
	idx.mu.Lock()
	clear(idx.files)
	idx.mu.Unlock()
}

// Close stops the watcher
func (idx *Index) Close() {
	select {
	case <-idx.stop:
		return
	default:
	}
	close(idx.stop)
	if idx.watcher != nil {
		_ = idx.watcher.Close()
	}
}

func (idx *Index) watchLoop() {
	for {
		select {
		case <-idx.stop:
			return
		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			dir := filepath.Dir(event.Name)
			idx.mu.RLock()
			key := idx.dirs[dir]
			idx.mu.RUnlock()
			if key == "" {
				continue
			}
			idx.mu.Lock()
			delete(idx.files, key)
			idx.mu.Unlock()
		case err, ok := <-idx.watcher.Errors:
			if !ok {
				return
			}
			slog.Debug("fsnotify watcher error", "error", err)
		}
	}
}
