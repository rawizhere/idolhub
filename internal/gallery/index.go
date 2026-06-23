package gallery

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type MediaEntry struct {
	Filename, Type string
	Size           int64
}

type Index struct {
	mu    sync.RWMutex
	files map[string][]MediaEntry
}

var GlobalIndex *Index

func Init() {
	idx := &Index{
		files: make(map[string][]MediaEntry),
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
	if filepath.Ext(name) == ".mp4" {
		return "video"
	}
	return "image"
}

func scan(dir string) []MediaEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []MediaEntry
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
		files = append(files, MediaEntry{
			Filename: name,
			Type:     mediaType(name),
			Size:     info.Size(),
		})
	}
	slices.SortFunc(files, func(a, b MediaEntry) int {
		return cmp.Compare(b.Filename, a.Filename)
	})
	return files
}

// Get returns cached file entries for a target
func (idx *Index) Get(platform, username string) []MediaEntry {
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
		files = []MediaEntry{}
	}

	idx.mu.Lock()
	idx.files[key] = files
	idx.mu.Unlock()

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
