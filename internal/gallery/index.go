package gallery

import (
	"cmp"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"idolhub/internal/store"
)

type MediaEntry struct {
	Filename, Type string
	Size           int64
}

type Index struct {
	files      ttlCache[[]MediaEntry]
	posts      ttlCache[[]Post]
	urlFiles   ttlCache[map[string]string]
	fileTexts  ttlCache[map[string]string]
	postsStore *store.PostStore
	sf         singleflight.Group
}

// cacheTTL bounds how long a view can miss changes made outside the app, such as files copied into downloads/.
const cacheTTL = 10 * time.Minute

func NewIndex(posts *store.PostStore) *Index {
	return &Index{postsStore: posts}
}

// ttlCache is a per-target map whose entries expire after cacheTTL.
type ttlCache[V any] struct {
	mu sync.Mutex
	m  map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	v  V
	at time.Time
}

func (c *ttlCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.at) > cacheTTL {
		var zero V
		return zero, false
	}
	return e.v, true
}

func (c *ttlCache[V]) Add(key string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]ttlEntry[V])
	}
	c.m[key] = ttlEntry[V]{v, time.Now()}
}

func (c *ttlCache[V]) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

func keyOf(platform, username string) string {
	return platform + "/" + username
}

func dirOf(platform, username string) string {
	return filepath.Join("downloads", platform, username)
}

// isMediaFile skips dot files and the posts.json(.bak) left by old builds.
func isMediaFile(name string) bool {
	return !strings.HasPrefix(name, ".") && name != "posts.json" && !strings.HasSuffix(name, ".bak")
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
	v, _, _ := idx.sf.Do("files/"+key, func() (any, error) {
		if files, ok := idx.files.Get(key); ok {
			return files, nil
		}
		files := scan(dirOf(platform, username))
		if files == nil {
			files = []MediaEntry{}
		}
		idx.files.Add(key, files)
		return files, nil
	})
	return v.([]MediaEntry)
}

// Count returns the number of media files for a target
func (idx *Index) Count(platform, username string) int {
	return len(idx.Get(platform, username))
}

// Invalidate removes cached entries for a target
func (idx *Index) Invalidate(platform, username string) {
	key := keyOf(platform, username)
	idx.files.Remove(key)
	idx.posts.Remove(key)
	idx.urlFiles.Remove(key)
	idx.fileTexts.Remove(key)
}

// URLFiles returns a cached map of post media URLs to local filenames for a target.
func (idx *Index) URLFiles(platform, username string) map[string]string {
	key := keyOf(platform, username)
	v, _, _ := idx.sf.Do("urls/"+key, func() (any, error) {
		if m, ok := idx.urlFiles.Get(key); ok {
			return m, nil
		}
		m := idx.buildURLFiles(platform, username)
		idx.urlFiles.Add(key, m)
		return m, nil
	})
	return v.(map[string]string)
}

func (idx *Index) buildURLFiles(platform, username string) map[string]string {
	files := idx.Get(platform, username)
	byName := make(map[string]struct{}, len(files))
	names := make([]string, 0, len(files))
	for _, f := range files {
		byName[f.Filename] = struct{}{}
		names = append(names, f.Filename)
	}
	res := make(map[string]string)
	for _, p := range idx.Posts(platform, username) {
		for _, mu := range p.MediaURLs {
			base := urlBase(mu)
			if base == "" {
				continue
			}
			if _, ok := byName[base]; ok {
				res[mu] = base
				continue
			}
			for _, name := range names {
				if strings.Contains(name, base) {
					res[mu] = name
					break
				}
			}
		}
		if p.TweetID != "" {
			videoName := p.TweetID + "_video.mp4"
			if _, ok := byName[videoName]; ok {
				res["tweet:"+p.TweetID] = videoName
			}
		}
	}
	return res
}

func urlBase(mediaURL string) string {
	parsed, err := url.Parse(mediaURL)
	if err != nil || parsed.Path == "" || strings.HasSuffix(parsed.Path, "/") {
		return ""
	}
	return path.Base(parsed.Path)
}

// FindLocalFile resolves a media URL to a cached local filename for a target.
func (idx *Index) FindLocalFile(platform, username, mediaURL string) (string, bool) {
	name, ok := idx.URLFiles(platform, username)[mediaURL]
	return name, ok && !strings.HasPrefix(name, "tweet:")
}

// FilePostText maps each local filename of a target to its concatenated post captions.
func (idx *Index) FilePostText(platform, username string) map[string]string {
	key := keyOf(platform, username)
	v, _, _ := idx.sf.Do("texts/"+key, func() (any, error) {
		if m, ok := idx.fileTexts.Get(key); ok {
			return m, nil
		}
		m := idx.buildFilePostText(platform, username)
		idx.fileTexts.Add(key, m)
		return m, nil
	})
	return v.(map[string]string)
}

func (idx *Index) buildFilePostText(platform, username string) map[string]string {
	urls := idx.URLFiles(platform, username)
	res := make(map[string]string)
	for _, p := range idx.Posts(platform, username) {
		for _, mu := range p.MediaURLs {
			if name, ok := urls[mu]; ok {
				res[name] = appendWithSpace(res[name], p.Text)
			}
		}
		if videoName, ok := urls["tweet:"+p.TweetID]; ok {
			res[videoName] = appendWithSpace(res[videoName], p.Text)
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
