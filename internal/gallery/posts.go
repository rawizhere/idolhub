package gallery

import (
	"context"
	"log/slog"
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

const postListLimit = 10000

// Posts returns cached store posts for a target.
func (idx *Index) Posts(platform, username string) []Post {
	key := keyOf(platform, username)
	v, _, _ := idx.sf.Do("posts/"+key, func() (any, error) {
		if posts, ok := idx.posts.Get(key); ok {
			return posts, nil
		}
		posts := idx.loadPosts(platform, username)
		idx.posts.Add(key, posts)
		return posts, nil
	})
	return v.([]Post)
}

func (idx *Index) loadPosts(platform, username string) []Post {
	if idx.postsStore == nil {
		return []Post{}
	}
	ctx := context.Background()
	rows, err := idx.postsStore.ListByAccount(ctx, platform, username, postListLimit)
	if err != nil {
		slog.Warn("Failed to list posts from store", "platform", platform, "user", username, "error", err)
		return []Post{}
	}
	posts := make([]Post, 0, len(rows))
	for _, row := range rows {
		p := Post{TweetID: row.ExternalID, Text: row.Text}
		if !row.PostedAt.IsZero() {
			p.Date = row.PostedAt.Format("2006-01-02_15-04-05")
		}
		for _, m := range row.Media {
			if m.Kind == "youtube" {
				p.YoutubeURLs = append(p.YoutubeURLs, m.URL)
			} else {
				p.MediaURLs = append(p.MediaURLs, m.URL)
			}
		}
		posts = append(posts, p)
	}
	return posts
}
