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
	LastSync           time.Time
	ForceFull          bool
	OnProgress         func(pct int, msg string)
	TwitterAuthToken   string
	InstagramSessionID string
	TikTokCookies      string
	Posts              *store.PostStore
}

// Scraper is a scrape function for a platform.
type Scraper func(ctx context.Context, t Target, opts Options) error

// Scrape runs the scrape function.
func (s Scraper) Scrape(ctx context.Context, t Target, opts Options) error {
	return s(ctx, t, opts)
}

var registry = map[string]Scraper{
	"twitter":   ScrapeTwitterUser,
	"instagram": ScrapeInstagramUser,
	"tiktok":    ScrapeYTDLP,
}

func Register(platform string, s Scraper) {
	registry[platform] = s
}

func Get(platform string) (Scraper, bool) {
	s, ok := registry[platform]
	return s, ok
}
