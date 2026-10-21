package scraper

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"idolhub/internal/browser"
	"idolhub/internal/download"
	"idolhub/internal/store"
)

// igFeedItem is one media node from the graphql user timeline response.
type igFeedItem struct {
	ID      string `json:"pk"`
	Caption *struct {
		Text string `json:"text"`
	} `json:"caption"`
	TakenAt int64 `json:"taken_at"`
	igMediaNode
	CarouselMedia []igMediaNode `json:"carousel_media,omitempty"`
}

// igMediaNode is a single photo or video: a post, or one slide of a carousel.
type igMediaNode struct {
	ID            string `json:"pk"`
	MediaType     int    `json:"media_type"`
	ImageVersions struct {
		Candidates []igRendition `json:"candidates"`
	} `json:"image_versions2"`
	VideoVersions []igRendition `json:"video_versions,omitempty"`
}

type igDirectItem struct {
	URL     string
	Date    time.Time
	MediaID string
	IsVideo bool
}

// igRendition is one size of an image or video.
type igRendition struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// bestURL picks the highest-resolution rendition.
func bestURL(rs []igRendition) string {
	if len(rs) == 0 {
		return ""
	}
	return slices.MaxFunc(rs, func(a, b igRendition) int { return cmp.Compare(a.Width*a.Height, b.Width*b.Height) }).URL
}

// ScrapeInstagramUser pulls timeline media via the private Instagram web API.
func ScrapeInstagramUser(ctx context.Context, t Target, opts Options) error {
	username, lastSync, maxPosts := t.Username, opts.cutoff(), opts.MaxPosts
	numWorkers := 5

	slog.Info("Scraping Instagram target user via direct API", "user", username, "platform", "instagram", "last_sync", lastSync)

	outputDir := filepath.Join("downloads", "instagram", username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		slog.Error("Failed to create output folder", "user", username, "error", err)
		return err
	}

	fetcher, err := instagramFetcher(ctx, opts)
	if fetcher == nil {
		return err
	}
	if err != nil {
		slog.Warn("Instagram client bootstrap failed, graphql requests may be rejected", "user", username, "error", err)
	}
	userID, err := fetcher.resolveUserID(ctx, username)
	if err != nil {
		return err
	}
	slog.Info("Resolved Instagram user ID", "user", username, "user_id", userID)
	opts.OnProgress(35)

	fileIdx := newIgFileIndex(outputDir)
	cdnClient := &http.Client{Timeout: 5 * time.Minute}

	jobs := make(chan igDirectItem, 1000)
	postsSaved := 0

	pool := download.Start(ctx, jobs, numWorkers, func(ctx context.Context, item igDirectItem) download.Result {
		return downloadInstagramDirectMedia(ctx, item, outputDir, username, fileIdx, cdnClient)
	})
	// abort drains the workers; a feed that stopped early is never a success, or the caller advances the watermark past unseen posts.
	abort := func(err error) error {
		close(jobs)
		pool.Wait()
		return err
	}

	var nextMaxID string
	page := 0
	reachedOldPosts := false
	consecutiveExistingPosts := 0
	taken := 0
	for {
		if ctx.Err() != nil {
			return abort(ctx.Err())
		}
		page++

		slog.Info("Fetching Instagram media feed page via graphql", "user", username, "page", page)

		feedBytes, ferr := fetcher.doGraphQL(ctx, username, userID, nextMaxID, 33)
		if ferr != nil {
			if ctx.Err() != nil {
				return abort(ctx.Err())
			}
			slog.Error("Failed to fetch Instagram feed page", "user", username, "page", page, "error", ferr)
			return abort(fmt.Errorf("instagram feed page %d: %w", page, ferr))
		}
		if len(feedBytes) == 0 {
			return abort(fmt.Errorf("instagram feed page %d: empty response", page))
		}

		var feed struct {
			Data struct {
				Feed struct {
					Edges []struct {
						Node igFeedItem `json:"node"`
					} `json:"edges"`
					PageInfo struct {
						EndCursor   string `json:"end_cursor"`
						HasNextPage bool   `json:"has_next_page"`
					} `json:"page_info"`
				} `json:"xdt_api__v1__feed__user_timeline_graphql_connection"`
			} `json:"data"`
		}

		if err := json.Unmarshal(feedBytes, &feed); err != nil {
			slog.Error("Failed to parse Instagram feed JSON", "user", username, "error", err)
			return abort(fmt.Errorf("instagram feed page %d: parse: %w", page, err))
		}

		slog.Info("Instagram feed page parsed", "user", username, "page", page, "items", len(feed.Data.Feed.Edges), "has_next_page", feed.Data.Feed.PageInfo.HasNextPage)
		opts.OnProgress(50)

		for _, edge := range feed.Data.Feed.Edges {
			item := edge.Node
			itemTime := time.Unix(item.TakenAt, 0).UTC()
			caption := ""
			if item.Caption != nil {
				caption = item.Caption.Text
			}
			nodes := []igMediaNode{item.igMediaNode}
			if item.MediaType == 8 && len(item.CarouselMedia) > 0 {
				nodes = item.CarouselMedia
			}
			var postEntries []igDirectItem
			for _, n := range nodes {
				// media_type 2 is a video; anything else is shown by its image
				isVideo := n.MediaType == 2
				u := bestURL(n.ImageVersions.Candidates)
				if isVideo {
					u = bestURL(n.VideoVersions)
				}
				if u != "" {
					postEntries = append(postEntries, igDirectItem{URL: u, Date: itemTime, MediaID: n.ID, IsVideo: isVideo})
				}
			}

			// Check if this post is older than lastSync or already exists on disk
			isOlderThanLastSync := !lastSync.IsZero() && itemTime.Before(lastSync)

			allExisting := false
			if !opts.ForceFull {
				allExisting = len(postEntries) > 0
				for _, pe := range postEntries {
					if !fileIdx.exists(pe) {
						allExisting = false
						break
					}
				}
			}

			if maxPosts > 0 && taken >= maxPosts {
				slog.Info("Reached the post limit for this sync, stopping pagination", "user", username, "max_posts", maxPosts)
				reachedOldPosts = true
				break
			}

			if isOlderThanLastSync || allExisting {
				consecutiveExistingPosts++
				// Require 3 consecutive existing/older posts to safely bypass pinned posts
				if consecutiveExistingPosts >= 3 {
					reachedOldPosts = true
					break
				}
			} else {
				consecutiveExistingPosts = 0
			}
			// posts past the cutoff only count towards the pinned-post allowance above; they are never fetched
			if isOlderThanLastSync {
				continue
			}
			taken++

			for _, entry := range postEntries {
				select {
				case jobs <- entry:
				case <-ctx.Done():
					return abort(ctx.Err())
				}
			}

			if len(postEntries) > 0 && t.SaveText {
				var media []store.PostMedia
				for _, pe := range postEntries {
					kind := "photo"
					if pe.IsVideo {
						kind = "video"
					}
					media = append(media, store.PostMedia{URL: pe.URL, Kind: kind})
				}
				if err := opts.Posts.UpsertPost(ctx, store.Post{
					Platform:   "instagram",
					Username:   username,
					ExternalID: item.ID,
					PostedAt:   itemTime,
					Text:       caption,
					Media:      media,
				}); err != nil {
					slog.Warn("Failed to save post to store", "user", username, "media_id", item.ID, "error", err)
				} else {
					postsSaved++
				}
			}
		}

		if reachedOldPosts {
			slog.Info("Reached previously scraped Instagram posts (older than last sync date or existing on disk), stopping pagination", "user", username)
			break
		}

		pageInfo := feed.Data.Feed.PageInfo
		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			slog.Info("Reached end of Instagram media feed", "user", username)
			break
		}
		nextMaxID = pageInfo.EndCursor

		// Rate-limit with jitter between pages
		if err := igLimiter.Wait(ctx); err != nil {
			return abort(ctx.Err())
		}
	}

	close(jobs)
	counts := pool.Wait()
	opts.OnProgress(90)
	if err := ctx.Err(); err != nil {
		return err
	}
	if counts.Failed > 0 {
		return fmt.Errorf("instagram: %d media downloads failed, keeping the sync watermark so they are retried", counts.Failed)
	}

	slog.Info("Instagram direct sync progress summary", "user", username, "platform", "instagram", "downloaded", counts.Downloaded, "skipped_existing", counts.Skipped, "posts_saved", postsSaved)
	return nil
}

// resolveUserID resolves the numeric user ID from username, doubling as a session check.
func (c *igClient) resolveUserID(ctx context.Context, username string) (string, error) {
	return resolveUserIDShared(ctx, c, username)
}

type igFileIndex struct {
	mu    sync.RWMutex
	files map[string]bool
}

func newIgFileIndex(outputDir string) *igFileIndex {
	idx := &igFileIndex{files: make(map[string]bool)}
	if entries, err := os.ReadDir(outputDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				idx.files[e.Name()] = true
			}
		}
	}
	return idx
}

// igFilename names a downloaded item: <date>_<cdn file name>, falling back to the media id. baseName is the part after the date.
func igFilename(item igDirectItem) (filename, baseName string, ok bool) {
	parsed, err := url.Parse(item.URL)
	if err != nil {
		return "", "", false
	}
	baseName = parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
	if baseName == "" {
		baseName = item.MediaID
	}
	if filepath.Ext(baseName) == "" {
		if item.IsVideo {
			baseName += ".mp4"
		} else {
			baseName += ".jpg"
		}
	}
	return item.Date.Format("2006-01-02_15-04-05") + "_" + baseName, baseName, true
}

func (idx *igFileIndex) exists(item igDirectItem) bool {
	filename, baseName, ok := igFilename(item)
	if !ok {
		return false
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.files[filename] {
		return true
	}

	for existingName := range idx.files {
		if strings.Contains(existingName, baseName) || (item.MediaID != "" && strings.Contains(existingName, item.MediaID)) {
			return true
		}
	}
	return false
}

func (idx *igFileIndex) add(filename string) {
	idx.mu.Lock()
	idx.files[filename] = true
	idx.mu.Unlock()
}

// downloadInstagramDirectMedia downloads a media item from CDN
func downloadInstagramDirectMedia(ctx context.Context, item igDirectItem, outputDir string, username string, fileIdx *igFileIndex, client *http.Client) download.Result {
	if fileIdx.exists(item) {
		return download.Skipped
	}
	filename, _, ok := igFilename(item)
	if !ok {
		slog.Warn("Failed to parse Instagram CDN URL", "user", username, "url", item.URL)
		return download.Skipped
	}
	filePath := filepath.Join(outputDir, filename)

	header := http.Header{
		"User-Agent": []string{browser.UA},
		"Referer":    []string{"https://www.instagram.com/"},
		"Accept":     []string{"image/avif,image/webp,*/*;q=0.8"},
	}
	downloaded, err := download.File(ctx, client, item.URL, filePath, download.FileOpts{Header: header})
	if err != nil {
		slog.Warn("Failed to download Instagram direct media", "user", username, "filename", filename, "error", err)
		if download.Gone(err) {
			return download.Skipped
		}
		return download.Failed
	}
	if !downloaded {
		fileIdx.add(filename)
		return download.Skipped
	}

	label := "photo"
	if item.IsVideo {
		label = "video"
	}
	slog.Info("Instagram file downloaded", "user", username, "filename", filename, "type", label)

	download.ThumbnailAsync(filePath)
	return download.Downloaded
}
