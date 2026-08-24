package gallery

import (
	"cmp"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fsnotify/fsnotify"
	lru "github.com/hashicorp/golang-lru/v2"
)

type MediaEntry struct {
	Filename, Type string
	Size           int64
}

type Index struct {
	files *lru.Cache[string, []MediaEntry]
	posts *lru.Cache[string, []Post]
}

var GlobalIndex *Index

func Init() {
	cache, err := lru.New[string, []MediaEntry](512)
	if err != nil {
		panic(err)
	}
	posts, err := lru.New[string, []Post](512)
	if err != nil {
		panic(err)
	}
	GlobalIndex = &Index{files: cache, posts: posts}
	GlobalIndex.watch()
}

func (idx *Index) watch() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add("downloads"); err != nil {
		_ = w.Close()
		return
	}
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				idx.invalidateFromPath(ev.Name)
				if ev.Op.Has(fsnotify.Create) {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						_ = w.Add(ev.Name)
					}
				}
			case <-w.Errors:
			}
		}
	}()
}

func (idx *Index) invalidateFromPath(path string) {
	rel, err := filepath.Rel("downloads", path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) >= 2 {
		idx.Invalidate(parts[0], parts[1])
	}
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
	if files, ok := idx.files.Get(key); ok {
		return files
	}

	files := scan(dirOf(platform, username))
	if files == nil {
		files = []MediaEntry{}
	}
	idx.files.Add(key, files)
	return files
}

// Count returns the number of media files for a target
func (idx *Index) Count(platform, username string) int {
	return len(idx.Get(platform, username))
}

// Invalidate removes the cached file list for a target
func (idx *Index) Invalidate(platform, username string) {
	key := keyOf(platform, username)
	idx.files.Remove(key)
	idx.posts.Remove(key)
}

// InvalidateAll clears all cached entries
func (idx *Index) InvalidateAll() {
	idx.files.Purge()
	idx.posts.Purge()
}

// FindLocalFile resolves a media URL to a cached local filename for a target.
func (idx *Index) FindLocalFile(platform, username, mediaURL string) (string, bool) {
	parsed, err := url.Parse(mediaURL)
	if err != nil {
		return "", false
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) == 0 {
		return "", false
	}
	base := parts[len(parts)-1]
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	files := idx.Get(platform, username)
	for _, f := range files {
		if f.Filename == base {
			return f.Filename, true
		}
	}
	if base != "" {
		for _, f := range files {
			if strings.Contains(f.Filename, base) {
				return f.Filename, true
			}
		}
	}
	return "", false
}

// FilePostText maps each local filename of a target to its concatenated post captions.
func (idx *Index) FilePostText(platform, username string) map[string]string {
	res := make(map[string]string)
	for _, p := range idx.Posts(platform, username) {
		for _, mu := range p.MediaURLs {
			if name, ok := idx.FindLocalFile(platform, username, mu); ok {
				res[name] = appendWithSpace(res[name], p.Text)
			}
		}
		if p.TweetID != "" {
			videoName := p.TweetID + "_video.mp4"
			for _, f := range idx.Get(platform, username) {
				if f.Filename == videoName {
					res[videoName] = appendWithSpace(res[videoName], p.Text)
					break
				}
			}
		}
	}
	return res
}

func appendWithSpace(existing, txt string) string {
	if existing == "" {
		return txt
	}
	return existing + " " + txt
}
