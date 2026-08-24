package scraper

import (
	"context"
	"time"
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
	LastSync   time.Time
	ForceFull  bool
	OnProgress func(pct int, msg string)
}

type Scraper interface {
	Name() string
	Scrape(ctx context.Context, t Target, opts Options) error
}

var registry = map[string]Scraper{}

func Register(s Scraper) {
	registry[s.Name()] = s
}

func Get(platform string) (Scraper, bool) {
	s, ok := registry[platform]
	return s, ok
}

func init() {
	Register(twitterScraper{})
	Register(instagramScraper{})
	Register(tiktokScraper{})
}

type twitterScraper struct{}

func (twitterScraper) Name() string { return "twitter" }

func (twitterScraper) Scrape(ctx context.Context, t Target, opts Options) error {
	return ScrapeTwitterUser(ctx, t, opts)
}

type instagramScraper struct{}

func (instagramScraper) Name() string { return "instagram" }

func (instagramScraper) Scrape(ctx context.Context, t Target, opts Options) error {
	return ScrapeInstagramUser(ctx, t, opts)
}

type tiktokScraper struct{}

func (tiktokScraper) Name() string { return "tiktok" }

func (tiktokScraper) Scrape(ctx context.Context, t Target, opts Options) error {
	return ScrapeYTDLP(ctx, t, opts)
}
