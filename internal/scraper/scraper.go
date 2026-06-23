package scraper

import (
	"context"
	"strings"
	"time"

	"idolhub/internal/config"
)

// Scraper scrapes a user's media for a specific platform
type Scraper interface {
	Scrape(ctx context.Context, username string, saveText bool, lastSync time.Time) error
}

type InstagramScraper struct {
	Config config.Store
}

func (s InstagramScraper) Scrape(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
	return ScrapeInstagramUser(ctx, s.Config, username, saveText, lastSync)
}

type TwitterScraper struct {
	Config config.Store
}

func (s TwitterScraper) Scrape(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
	c := s.Config.Get()
	var skipRetweets bool
	var filters []string
	downloadPhotos := true
	downloadVideos := true
	for _, acc := range c.Accounts {
		if strings.EqualFold(acc.Username, username) {
			skipRetweets = acc.SkipRetweets
			filters = acc.Filters
			downloadPhotos = acc.ShouldDownloadPhotos()
			downloadVideos = acc.ShouldDownloadVideos()
			break
		}
	}
	return ScrapeTwitterUser(ctx, s.Config, username, saveText, skipRetweets, filters, downloadPhotos, downloadVideos, lastSync)
}

func init() {
	Register("instagram", func(cfg config.Store) Scraper {
		return InstagramScraper{Config: cfg}
	})
	Register("twitter", func(cfg config.Store) Scraper {
		return TwitterScraper{Config: cfg}
	})
}
