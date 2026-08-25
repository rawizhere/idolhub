package gallery

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type PostMediaFile struct {
	Filename     string `json:"filename"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type Post struct {
	TweetID     string          `json:"tweet_id"`
	Date        string          `json:"date"`
	Text        string          `json:"text"`
	MediaURLs   []string        `json:"media_urls"`
	LocalFiles  []PostMediaFile `json:"local_files,omitempty"`
	YoutubeURLs []string        `json:"youtube_urls,omitempty"`
}

// Posts returns cached parsed posts.json for a target.
func (idx *Index) Posts(platform, username string) []Post {
	key := keyOf(platform, username)
	v, _, _ := idx.sf.Do("posts/"+key, func() (any, error) {
		if posts, ok := idx.posts.Get(key); ok {
			return posts, nil
		}
		var posts []Post
		if data, err := os.ReadFile(filepath.Join(dirOf(platform, username), "posts.json")); err == nil {
			_ = json.Unmarshal(data, &posts)
		}
		if posts == nil {
			posts = []Post{}
		}
		idx.posts.Add(key, posts)
		return posts, nil
	})
	return v.([]Post)
}
