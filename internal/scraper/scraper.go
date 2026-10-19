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
	LastSync            time.Time
	ForceFull           bool
	OnProgress          func(pct int, msg string)
	TwitterAuthToken    string
	TwitterCookies      string
	InstagramSessionID  string
	InstagramCookies    string
	InstagramSidecarURL string
	TikTokCookies       string
	Posts               *store.PostStore
	// Since is the oldest post date a sync fetches; zero means unbounded.
	Since time.Time
	// MaxPosts caps the number of posts taken; the orchestrator only sets it when there is no watermark.
	MaxPosts int
}

// cutoff is the date before which a scrape stops: the later of the watermark and the account's Since bound.
func (o Options) cutoff() time.Time {
	if o.Since.After(o.LastSync) {
		return o.Since
	}
	return o.LastSync
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
