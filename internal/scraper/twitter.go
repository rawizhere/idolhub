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
	lastSync := opts.LastSync
	report := func(pct int, msg string) {
		if opts.OnProgress != nil {
			opts.OnProgress(pct, msg)
		}
	}
	slog.Info("Scraping Twitter target user", "user", username, "platform", "twitter", "save_text", saveText, "skip_retweets", skipRetweets, "filters", filters, "download_photos", downloadPhotos, "download_videos", downloadVideos, "last_sync", lastSync)

	if opts.TwitterAuthToken == "" {
		err := fmt.Errorf("twitter auth token is not set in settings")
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

	jobs := make(chan TwitterDownloadItem, 10000)
	client := &http.Client{Timeout: 45 * time.Second}
	pool := download.Start(ctx, jobs, numWorkers, func(ctx context.Context, item TwitterDownloadItem) bool {
		if item.IsVideo {
			return downloadTwitterVideo(ctx, item, outputDir, client, username)
		}
		return downloadTwitterImage(ctx, item, outputDir, client, username)
	})

	s, err := xscraper.New(opts.TwitterAuthToken)
	if err != nil {
		return fmt.Errorf("failed to init twitter client: %w", err)
	}

	tlCtx, cancelTimeline := context.WithCancel(ctx)
	defer cancelTimeline()
	var ch <-chan *xscraper.TweetResult
	if saveText {
		ch = s.GetTweets(tlCtx, username, 5000)
	} else {
		ch = s.GetMediaTweets(tlCtx, username, 5000)
	}
	report(30, "timeline fetch")

	type queuedMedia struct {
		url  string
		kind string
	}
	queuedURLs := make(map[string]bool)

	for result := range ch {
		if result.Error != nil {
			cancelTimeline()
			for range ch {
			}
			err := result.Error
			if errors.Is(err, context.Canceled) {
				break
			}
			slog.Error("Twitter timeline fetch failed", "user", username, "error", err)
			if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
				return fmt.Errorf("%w: twitter rejected auth_token: %v", ErrAuthExpired, err)
			}
			return fmt.Errorf("twitter timeline fetch failed: %w", err)
		}
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
			}
		}
	}

	close(jobs)
	report(90, "downloads done")
	downloadedCount, skippedCount := pool.Wait()

	slog.Info("Twitter sync completed successfully", "user", username, "platform", "twitter", "downloaded", downloadedCount, "skipped_existing", skippedCount)
	return nil
}

func downloadTwitterImage(ctx context.Context, item TwitterDownloadItem, outputDir string, client *http.Client, username string) bool {
	parsedURL, err := url.Parse(item.URL)
	if err != nil {
		return false
	}

	parts := strings.Split(parsedURL.Path, "/")
	originalName := parts[len(parts)-1]

	filename := fmt.Sprintf("%s_%s", item.DateStr, originalName)
	filePath := filepath.Join(outputDir, filename)

	downloaded, err := download.File(ctx, client, item.URL, filePath, download.FileOpts{
		Header: http.Header{"User-Agent": []string{desktopUA}},
		Jitter: 2 * time.Second,
	})
	if err != nil {
		slog.Warn("Failed to download image", "user", username, "filename", filename, "error", err)
		return false
	}
	if !downloaded {
		return false
	}

	slog.Info("Twitter image downloaded", "user", username, "filename", filename)
	download.ThumbnailAsync(filePath)
	return true
}

func downloadTwitterVideo(ctx context.Context, item TwitterDownloadItem, outputDir string, client *http.Client, username string) bool {
	filename := fmt.Sprintf("%s_%s_video.mp4", item.DateStr, item.TweetID)
	filePath := filepath.Join(outputDir, filename)

	slog.Info("Starting Twitter video download", "user", username, "tweet_id", item.TweetID)
	downloaded, err := download.File(ctx, client, item.URL, filePath, download.FileOpts{
		Header: http.Header{"User-Agent": []string{desktopUA}},
		Jitter: 2 * time.Second,
	})
	if err != nil {
		slog.Warn("Failed to download video", "user", username, "tweet_id", item.TweetID, "error", err)
		return false
	}
	if !downloaded {
		return false
	}

	slog.Info("Twitter video downloaded successfully", "user", username, "filename", filename)
	download.ThumbnailAsync(filePath)
	return true
}
