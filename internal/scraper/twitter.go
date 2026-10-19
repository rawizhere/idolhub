package scraper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"idolhub/internal/download"
	"idolhub/internal/store"
	"idolhub/internal/xscraper"
)

type TwitterDownloadItem struct {
	URL     string
	DateStr string
	TweetID string
	IsVideo bool
}

func snowflakeToTime(tweetIDStr string) (time.Time, error) {
	tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	const epoch int64 = 1288834974657
	return time.UnixMilli((tweetID >> 22) + epoch).UTC(), nil
}

// twitterTimelineMax caps tweets read per sync. At ~8s per 50-tweet page this is about an hour, well inside the 2h task timeout; X itself stops serving user timelines after a few thousand tweets.
const twitterTimelineMax = 20000

func ScrapeTwitterUser(ctx context.Context, t Target, opts Options) error {
	username := t.Username
	saveText := t.SaveText
	skipRetweets := t.SkipRetweets
	filters := t.Filters
	downloadPhotos := true
	downloadVideos := true
	if t.DownloadPhotos != nil {
		downloadPhotos = *t.DownloadPhotos
	}
	if t.DownloadVideos != nil {
		downloadVideos = *t.DownloadVideos
	}
	lastSync := opts.cutoff()
	report := func(pct int, msg string) {
		if opts.OnProgress != nil {
			opts.OnProgress(pct, msg)
		}
	}
	slog.Info("Scraping Twitter target user", "user", username, "platform", "twitter", "save_text", saveText, "skip_retweets", skipRetweets, "filters", filters, "download_photos", downloadPhotos, "download_videos", downloadVideos, "last_sync", lastSync)

	// the auth_token normally comes from the cookie export; a bare token is the legacy setup
	authToken := opts.TwitterAuthToken
	if authToken == "" && opts.TwitterCookies == "" {
		err := fmt.Errorf("twitter session is not configured: paste an x.com cookie export in settings")
		slog.Error("Scraper aborted", "error", err)
		return err
	}

	if opts.Posts == nil {
		return fmt.Errorf("post store is not configured")
	}

	numWorkers := 5
	outputDir := filepath.Join("downloads", "twitter", username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		slog.Error("Failed to create output directory", "user", username, "error", err)
		return err
	}

	// xscraper generates the csrf token and falls back to built-in defaults; a full cookie export (twitter_cookies) is preferred when configured.
	s, err := xscraper.New(xscraper.Session{AuthToken: authToken, Cookies: opts.TwitterCookies})
	if err != nil {
		return fmt.Errorf("failed to init twitter client: %w", err)
	}

	jobs := make(chan TwitterDownloadItem, 10000)
	client := &http.Client{Timeout: 45 * time.Second}
	pool := download.Start(ctx, jobs, numWorkers, func(ctx context.Context, item TwitterDownloadItem) download.Result {
		return downloadTwitterMedia(ctx, item, outputDir, client, username)
	})

	tlCtx, cancelTimeline := context.WithCancel(ctx)
	defer cancelTimeline()
	var ch <-chan *xscraper.TweetResult
	if saveText {
		ch = s.GetTweets(tlCtx, username, twitterTimelineMax)
	} else {
		ch = s.GetMediaTweets(tlCtx, username, twitterTimelineMax)
	}
	received := 0
	// abort stops the timeline and the download workers; every early return goes through it so no goroutine outlives the scrape.
	abort := func(err error) error {
		cancelTimeline()
		for range ch {
		}
		close(jobs)
		pool.Wait()
		return err
	}
	report(30, "timeline fetch")

	type queuedMedia struct {
		url  string
		kind string
	}
	queuedURLs := make(map[string]bool)
	postsSaved := 0
	taken := 0

	for result := range ch {
		if result.Error != nil {
			err := result.Error
			// a cancelled or partial timeline is never a success: the caller would advance the watermark past unseen tweets
			if ctx.Err() != nil {
				return abort(ctx.Err())
			}
			slog.Error("Twitter timeline fetch failed", "user", username, "error", err)
			if errors.Is(err, xscraper.ErrAuthExpired) {
				return abort(fmt.Errorf("%w: twitter rejected the session: %w", ErrAuthExpired, err))
			}
			return abort(fmt.Errorf("twitter timeline fetch failed: %w", err))
		}
		received++
		tw := &result.Tweet

		dateStr := "unknown_date"
		tweetTime, terr := snowflakeToTime(tw.ID)
		if terr == nil {
			if !lastSync.IsZero() && tweetTime.Before(lastSync) {
				slog.Info("Reached tweets older than last sync, stopping timeline", "user", username)
				cancelTimeline()
				for range ch {
				}
				break
			}
			dateStr = tweetTime.Format("2006-01-02_15-04-05")
		}

		if tw.IsRetweet && skipRetweets {
			continue
		}

		text := tw.Text
		matched := len(filters) == 0
		lowerText := strings.ToLower(text)
		for _, filter := range filters {
			if strings.Contains(lowerText, strings.ToLower(filter)) {
				matched = true
				break
			}
		}
		if !matched || (tw.IsRetweet && !saveText) {
			continue
		}

		if opts.MaxPosts > 0 && taken >= opts.MaxPosts {
			slog.Info("Reached the post limit for this sync, stopping timeline", "user", username, "max_posts", opts.MaxPosts)
			cancelTimeline()
			for range ch {
			}
			break
		}
		taken++

		var tweetMedia []queuedMedia
		queueItem := func(rawURL string, kind string) {
			if rawURL == "" {
				return
			}
			isVideo := kind == "video" || kind == "gif"
			if isVideo && !downloadVideos {
				return
			}
			if !isVideo && !downloadPhotos {
				return
			}
			if queuedURLs[rawURL] {
				return
			}
			queuedURLs[rawURL] = true
			select {
			case jobs <- TwitterDownloadItem{
				URL:     rawURL,
				DateStr: dateStr,
				TweetID: tw.ID,
				IsVideo: isVideo,
			}:
			case <-ctx.Done():
			}
			tweetMedia = append(tweetMedia, queuedMedia{url: rawURL, kind: kind})
		}

		for _, ph := range tw.Photos {
			u := ph.URL
			if !strings.Contains(u, "?") {
				u += "?format=jpg&name=large"
			}
			queueItem(u, "photo")
		}
		for _, v := range tw.Videos {
			queueItem(v.URL, "video")
		}
		for _, g := range tw.GIFs {
			queueItem(g.URL, "gif")
		}

		if saveText {
			var media []store.PostMedia
			for _, m := range tweetMedia {
				media = append(media, store.PostMedia{URL: m.url, Kind: m.kind})
			}
			for _, u := range tw.URLs {
				if strings.Contains(u, "youtube.com") || strings.Contains(u, "youtu.be") {
					media = append(media, store.PostMedia{URL: u, Kind: "youtube"})
				}
			}
			postedAt, _ := time.Parse("2006-01-02_15-04-05", dateStr)
			if err := opts.Posts.UpsertPost(ctx, store.Post{
				Platform:   "twitter",
				Username:   username,
				ExternalID: tw.ID,
				PostedAt:   postedAt,
				Text:       text,
				Media:      media,
			}); err != nil {
				slog.Warn("Failed to save tweet to store", "user", username, "tweet_id", tw.ID, "error", err)
			} else {
				postsSaved++
			}
		}
	}

	if received >= twitterTimelineMax {
		slog.Warn("Twitter timeline hit the per-sync tweet cap; older tweets were not fetched", "user", username, "cap", twitterTimelineMax)
	}

	close(jobs)
	counts := pool.Wait()
	report(90, "downloads done")
	if err := ctx.Err(); err != nil {
		return err
	}
	if counts.Failed > 0 {
		return fmt.Errorf("twitter: %d media downloads failed, keeping the sync watermark so they are retried", counts.Failed)
	}

	slog.Info("Twitter sync completed successfully", "user", username, "platform", "twitter", "downloaded", counts.Downloaded, "skipped_existing", counts.Skipped, "posts_saved", postsSaved)
	return nil
}

func downloadTwitterMedia(ctx context.Context, item TwitterDownloadItem, outputDir string, client *http.Client, username string) download.Result {
	var filename string
	if item.IsVideo {
		filename = fmt.Sprintf("%s_%s_video.mp4", item.DateStr, item.TweetID)
	} else {
		parsedURL, err := url.Parse(item.URL)
		if err != nil {
			return download.Skipped
		}
		parts := strings.Split(parsedURL.Path, "/")
		filename = fmt.Sprintf("%s_%s", item.DateStr, parts[len(parts)-1])
	}
	filePath := filepath.Join(outputDir, filename)

	kind := "image"
	if item.IsVideo {
		kind = "video"
	}
	slog.Info("Starting Twitter media download", "user", username, "kind", kind, "tweet_id", item.TweetID)
	downloaded, err := download.File(ctx, client, item.URL, filePath, download.FileOpts{
		Header: http.Header{"User-Agent": []string{desktopUA}},
		Jitter: 2 * time.Second,
	})
	if err != nil {
		slog.Warn("Failed to download media", "user", username, "kind", kind, "tweet_id", item.TweetID, "filename", filename, "error", err)
		if download.Gone(err) {
			return download.Skipped
		}
		return download.Failed
	}
	if !downloaded {
		return download.Skipped
	}

	slog.Info("Twitter media downloaded successfully", "user", username, "kind", kind, "filename", filename)
	download.ThumbnailAsync(filePath)
	return download.Downloaded
}
