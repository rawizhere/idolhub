package scraper

import (
	"context"
	"slices"
	"strings"
	"time"

	"idolhub/internal/store"
)

type Target struct {
	Username       string
	Platform       string
	SaveText       bool
	SkipRetweets   bool
	Filters        []string
	DownloadPhotos bool
	DownloadVideos bool
}

// MatchesFilters reports whether a post is kept: no filters keep everything, otherwise the text must contain one, case-insensitively.
func MatchesFilters(text string, filters []string) bool {
	text = strings.ToLower(text)
	return len(filters) == 0 || slices.ContainsFunc(filters, func(f string) bool { return strings.Contains(text, strings.ToLower(f)) })
}

type Options struct {
	LastSync            time.Time
	ForceFull           bool
	OnProgress          func(pct int)
	TwitterCookies      string
	InstagramCookies    string
	InstagramSidecarURL string
	TikTokCookies       string
	YouTubeCookies      string
	// ThumbnailsDir is the fast-preview root; renames carry previews along to keep them findable.
	ThumbnailsDir string
	Posts         *store.PostStore
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
	"youtube":   ScrapeYTDLP,
}

func Get(platform string) (scrapeFunc, bool) {
	s, ok := registry[platform]
	return s, ok
}
