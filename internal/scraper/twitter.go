package scraper

import (
	"context"
	"encoding/json"
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

	twitterscraper "github.com/imperatrona/twitter-scraper"
)

type TwitterDownloadItem struct {
	URL     string
	DateStr string
	TweetID string
	IsVideo bool
}

type TweetPost struct {
	TweetID     string   `json:"tweet_id"`
	Date        string   `json:"date"`
	Text        string   `json:"text"`
	MediaURLs   []string `json:"media_urls"`
	YoutubeURLs []string `json:"youtube_urls,omitempty"`
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

	s := twitterscraper.New()
	s.SetAuthToken(twitterscraper.AuthToken{Token: opts.TwitterAuthToken})
	s.WithDelay(5)

	tlCtx, cancelTimeline := context.WithCancel(ctx)
	defer cancelTimeline()
	var ch <-chan *twitterscraper.TweetResult
	if saveText {
		ch = s.GetTweetsAndReplies(tlCtx, username, 5000)
	} else {
		ch = s.GetMediaTweets(tlCtx, username, 5000)
	}
	report(30, "timeline fetch")

	queuedURLs := make(map[string]bool)
	var scrapedPosts []TweetPost

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
			break
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

		var tweetMediaURLs []string
		queueItem := func(rawURL string, isVideo bool) {
			if rawURL == "" {
				return
			}
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
			tweetMediaURLs = append(tweetMediaURLs, rawURL)
		}

		for _, ph := range tw.Photos {
			u := ph.URL
			if !strings.Contains(u, "?") {
				u += "?format=jpg&name=large"
			}
			queueItem(u, false)
		}
		for _, v := range tw.Videos {
			queueItem(v.URL, true)
		}
		for _, g := range tw.GIFs {
			queueItem(g.URL, true)
		}

		var youtubeURLs []string
		for _, u := range tw.URLs {
			if strings.Contains(u, "youtube.com") || strings.Contains(u, "youtu.be") {
				youtubeURLs = append(youtubeURLs, u)
			}
		}

		if saveText {
			scrapedPosts = append(scrapedPosts, TweetPost{
				TweetID:     tw.ID,
				Date:        dateStr,
				Text:        text,
				MediaURLs:   tweetMediaURLs,
				YoutubeURLs: youtubeURLs,
			})
		}
	}

	close(jobs)
	report(90, "downloads done")
	downloadedCount, skippedCount := pool.Wait()

	if saveText && len(scrapedPosts) > 0 {
		slog.Info("Saving tweet texts to single JSON", "user", username)
		postsFilePath := filepath.Join(outputDir, "posts.json")

		var maps []map[string]interface{}
		if raw, err := json.Marshal(scrapedPosts); err == nil {
			_ = json.Unmarshal(raw, &maps)
		}
		if len(maps) > 0 {
			mergePostsJSON(postsFilePath, maps)
			slog.Info("Tweet texts saved successfully", "user", username, "file", postsFilePath)
		}
	}

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
