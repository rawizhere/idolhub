package scraper

import (
	"context"
	"time"

	"idolhub/internal/store"
)

type Target struct {
	Username       string
	Platform       string
	SaveText       bool
	SkipRetweets   bool
	Filters        []string
	DownloadPhotos *bool
	DownloadVideos *bool
}

type Options struct {
	LastSync              time.Time
	ForceFull             bool
	OnProgress            func(pct int, msg string)
	TwitterAuthToken      string
	InstagramSessionID    string
	InstagramGraphQLDocID string
	TikTokCookies         string
	Posts                 *store.PostStore
}

// scrapeFunc is a scrape function for a platform.
type scrapeFunc func(ctx context.Context, t Target, opts Options) error

var registry = map[string]scrapeFunc{
	"twitter":   ScrapeTwitterUser,
	"instagram": ScrapeInstagramUser,
	"tiktok":    ScrapeYTDLP,
}

func Get(platform string) (scrapeFunc, bool) {
	s, ok := registry[platform]
	return s, ok
}
