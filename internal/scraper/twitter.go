package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"idolhub/internal/download"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/time/rate"

	pcookiejar "github.com/juju/persistent-cookiejar"
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

type tweetMedia struct {
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     struct {
		Variants []struct {
			ContentType string `json:"content_type"`
			Bitrate     int    `json:"bitrate"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}

type tweetLegacy struct {
	FullText  string `json:"full_text"`
	Text      string `json:"text"`
	IDStr     string `json:"id_str"`
	InReplyTo string `json:"in_reply_to_status_id_str"`
	User      struct {
		ScreenName string `json:"screen_name"`
	} `json:"user"`
	ExtendedEntities struct {
		Media []tweetMedia `json:"media"`
	} `json:"extended_entities"`
	Entities struct {
		URLs []struct {
			ExpandedURL string `json:"expanded_url"`
		} `json:"urls"`
	} `json:"entities"`
}

func (l tweetLegacy) body() string {
	if l.FullText != "" {
		return l.FullText
	}
	return l.Text
}

type tweet struct {
	Legacy tweetLegacy `json:"legacy"`
	User   struct {
		ScreenName string `json:"screen_name"`
	} `json:"user"`
}

func (t *tweet) author() string {
	if t.Legacy.User.ScreenName != "" {
		return t.Legacy.User.ScreenName
	}
	return t.User.ScreenName
}

func decodeTweet(m map[string]interface{}) (*tweet, string, bool) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, "", false
	}
	var tw tweet
	if err := json.Unmarshal(b, &tw); err != nil || tw.Legacy.IDStr == "" {
		return nil, "", false
	}
	return &tw, tw.Legacy.IDStr, true
}

func snowflakeToTime(tweetIDStr string) (time.Time, error) {
	tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	const epoch int64 = 1288834974657
	return time.UnixMilli((tweetID >> 22) + epoch).UTC(), nil
}

func parseTwitterSnowflakeDate(tweetIDStr string) string {
	t, err := snowflakeToTime(tweetIDStr)
	if err != nil {
		return "unknown_date"
	}
	return t.Format("2006-01-02_15-04-05")
}

func navigateNoWait(urlStr string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, err := page.Navigate(urlStr).Do(ctx)
		return err
	})
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

	twitterAuthToken := opts.TwitterAuthToken
	if twitterAuthToken == "" {
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

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1920, 1080),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	dpCtx, cancelDp := chromedp.NewContext(allocCtx)
	defer cancelDp()

	dpCtx, cancelTimeout := context.WithTimeout(dpCtx, 2*time.Hour)
	defer cancelTimeout()

	jobs := make(chan TwitterDownloadItem, 10000)
	jar, err := pcookiejar.New(&pcookiejar.Options{
		Filename: filepath.Join(outputDir, "cookies.json"),
	})
	if err != nil {
		slog.Error("Failed to create cookie jar", "user", username, "error", err)
		return err
	}
	defer func() { _ = jar.Save() }()

	// Seed the session cookie so the HTTP client stays authed across runs.
	jar.SetCookies(&url.URL{Scheme: "https", Host: "x.com"}, []*http.Cookie{{
		Name:     "auth_token",
		Value:    twitterAuthToken,
		Domain:   ".x.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		Expires:  time.Now().Add(180 * 24 * time.Hour),
	}})

	client := &http.Client{Timeout: 45 * time.Second, Jar: jar}
	pool := download.Start(ctx, jobs, numWorkers, func(ctx context.Context, item TwitterDownloadItem) bool {
		if item.IsVideo {
			return downloadTwitterVideo(ctx, item, outputDir, client, username)
		}
		return downloadTwitterImage(ctx, item, outputDir, client, username)
	})

	queuedURLs := make(map[string]bool)
	var scrapedPosts []TweetPost
	var postsMu sync.Mutex
	var scrollCounter int32 = 0
	reachedOldTweets := false

	// done is closed before jobs to prevent races on send
	done := make(chan struct{})
	var listenerWg sync.WaitGroup

	seenTweets := make(map[string]*tweet)
	repliesIndex := make(map[string][]string)
	matchedIDs := make(map[string]bool)

	// Must be called with postsMu held
	queueTweetLocked := func(tweetID string) {
		tw, ok := seenTweets[tweetID]
		if !ok {
			slog.Debug("Tweet not found in seenTweets", "tweet_id", tweetID)
			return
		}
		author := tw.author()
		if author == "" {
			slog.Debug("No author found for tweet", "tweet_id", tweetID)
			return
		}
		if !strings.EqualFold(author, username) {
			slog.Debug("Skipping non-author tweet", "author", author, "target", username, "tweet_id", tweetID)
			return
		}
		text := tw.Legacy.body()
		dateStr := parseTwitterSnowflakeDate(tweetID)

		var tweetMediaURLs []string
		for _, m := range tw.Legacy.ExtendedEntities.Media {
			downloadURL := m.MediaURLHTTPS
			isVideo := false

			bestVideoURL := ""
			maxBitrate := -1
			for _, v := range m.VideoInfo.Variants {
				if v.ContentType == "video/mp4" && v.Bitrate > maxBitrate {
					maxBitrate = v.Bitrate
					bestVideoURL = v.URL
				}
			}
			if bestVideoURL != "" {
				downloadURL = bestVideoURL
				isVideo = true
			}

			if downloadURL == "" {
				continue
			}
			slog.Debug("Queued URL", "url", downloadURL, "video", isVideo)
			if isVideo && !downloadVideos {
				continue
			}
			if !isVideo && !downloadPhotos {
				continue
			}

			if !queuedURLs[downloadURL] {
				queuedURLs[downloadURL] = true
				select {
				case jobs <- TwitterDownloadItem{
					URL:     downloadURL,
					DateStr: dateStr,
					TweetID: tweetID,
					IsVideo: isVideo,
				}:
				case <-done:
					return
				}
				slog.Debug("Added to download queue", "tweet_id", tweetID, "url", downloadURL, "video", isVideo)
			}
			tweetMediaURLs = append(tweetMediaURLs, downloadURL)
		}

		var youtubeURLs []string
		for _, u := range tw.Legacy.Entities.URLs {
			if expanded := u.ExpandedURL; expanded != "" &&
				(strings.Contains(expanded, "youtube.com") || strings.Contains(expanded, "youtu.be")) {
				youtubeURLs = append(youtubeURLs, expanded)
			}
		}

		if saveText {
			scrapedPosts = append(scrapedPosts, TweetPost{
				TweetID:     tweetID,
				Date:        dateStr,
				Text:        text,
				MediaURLs:   tweetMediaURLs,
				YoutubeURLs: youtubeURLs,
			})
		}
	}

	var propagateMatchLocked func(id string)
	propagateMatchLocked = func(id string) {
		if matchedIDs[id] {
			return
		}
		matchedIDs[id] = true
		queueTweetLocked(id)
		for _, replyID := range repliesIndex[id] {
			propagateMatchLocked(replyID)
		}
	}

	chromedp.ListenTarget(dpCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventLoadingFinished:
			listenerWg.Add(1)
			go func(reqID network.RequestID) {
				defer listenerWg.Done()
				select {
				case <-done:
					return
				default:
				}

				c := chromedp.FromContext(dpCtx)
				rb, err := network.GetResponseBody(reqID).Do(cdp.WithExecutor(dpCtx, c.Target))
				if err != nil {
					slog.Debug("Failed to get response body", "error", err)
					return
				}

				var raw interface{}
				if err := json.Unmarshal(rb, &raw); err != nil {
					slog.Debug("Failed to unmarshal response body", "error", err)
					return
				}

				// Parse tweets from intercepted responses
				findTweets(raw, func(candidate map[string]interface{}) {
					tw, tweetID, ok := decodeTweet(candidate)
					if !ok {
						return
					}

					if !lastSync.IsZero() {
						tweetTime, err := snowflakeToTime(tweetID)
						if err == nil {
							currentScroll := atomic.LoadInt32(&scrollCounter)
							if tweetTime.Before(lastSync) && currentScroll > 1 {
								postsMu.Lock()
								reachedOldTweets = true
								postsMu.Unlock()
								return
							}
						}
					}

					text := tw.Legacy.body()

					// Skip retweets if configured
					if skipRetweets && strings.HasPrefix(text, "RT @") {
						return
					}

					postsMu.Lock()
					defer postsMu.Unlock()

					// Skip if we already processed this tweet
					if _, seen := seenTweets[tweetID]; seen {
						return
					}

					seenTweets[tweetID] = tw
					slog.Debug("Found tweet", "tweet_id", tweetID, "text", text)
					inReplyToStatusID := tw.Legacy.InReplyTo
					if inReplyToStatusID != "" {
						repliesIndex[inReplyToStatusID] = append(repliesIndex[inReplyToStatusID], tweetID)
					}

					isMatched := len(filters) == 0
					lowerText := strings.ToLower(text)
					for _, filter := range filters {
						if strings.Contains(lowerText, strings.ToLower(filter)) {
							isMatched = true
							break
						}
					}

					// If matched directly OR its parent is matched, trigger propagation
					if isMatched || (inReplyToStatusID != "" && matchedIDs[inReplyToStatusID]) {
						propagateMatchLocked(tweetID)
					}
				})
			}(ev.RequestID)
		}
	})

	var targetURL string
	if saveText {
		targetURL = fmt.Sprintf("https://x.com/%s/with_replies", username)
	} else {
		targetURL = fmt.Sprintf("https://x.com/%s/media", username)
	}

	slog.Info("Navigating to Twitter/X", "user", username, "url", targetURL)
	report(20, "navigating")

	err = chromedp.Run(dpCtx,
		network.Enable(),
		navigateNoWait("https://x.com"),
		chromedp.Sleep(1*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
			return network.SetCookie("auth_token", twitterAuthToken).
				WithDomain(".x.com").
				WithPath("/").
				WithExpires(&expr).
				WithHTTPOnly(true).
				WithSecure(true).
				Do(ctx)
		}),
	)

	var currentPath string
	if err == nil {
		err = chromedp.Run(dpCtx,
			navigateNoWait("https://x.com/home"),
			chromedp.Sleep(3*time.Second),
			chromedp.Evaluate(`window.location.pathname`, &currentPath),
		)
	}
	if err != nil {
		slog.Error("Failed to verify Twitter session", "user", username, "error", err)
		return fmt.Errorf("failed to verify Twitter session: %w", err)
	}
	// Check login redirects
	isLoginRedirect := strings.Contains(currentPath, "login") || strings.Contains(currentPath, "flow")
	if isLoginRedirect {
		slog.Error("Twitter auth token is invalid or expired", "user", username, "path", currentPath)
		return fmt.Errorf("twitter auth token is invalid or expired — please log in to x.com and copy a fresh auth_token cookie: %w", ErrAuthExpired)
	}
	slog.Info("Twitter session is valid", "user", username, "path", currentPath)
	report(30, "session valid")

	err = chromedp.Run(dpCtx,
		navigateNoWait(targetURL),
		chromedp.Sleep(5*time.Second),
		// Scroll timeline loop
		chromedp.ActionFunc(func(ctx context.Context) error {
			lastCount := 0
			retries := 0
			limiter := rate.NewLimiter(rate.Every(7*time.Second), 1)
			for i := 1; ; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				atomic.StoreInt32(&scrollCounter, int32(i))

				postsMu.Lock()
				hasReachedOld := reachedOldTweets
				postsMu.Unlock()

				if hasReachedOld {
					slog.Info("Reached previously scraped tweets (older than last sync date), stopping scroll", "user", username)
					break
				}

				slog.Info("Scrolling Twitter feed", "user", username, "scroll", i)

				if err := limiter.Wait(ctx); err != nil {
					return err
				}
				err := chromedp.Run(ctx,
					chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
				)
				if err != nil {
					return err
				}

				postsMu.Lock()
				currentCount := len(queuedURLs)
				postsMu.Unlock()

				slog.Info("Twitter media count after scroll", "user", username, "count", currentCount)

				if currentCount == lastCount {
					retries++
					if retries >= 6 { // Allow some retries for slow content load
						slog.Info("Reached end of Twitter feed or no new media found", "user", username)
						break
					}
				} else {
					retries = 0
					lastCount = currentCount
				}
			}
			return nil
		}),
	)

	// Stop the browser context, signal listeners, then close jobs
	cancelAlloc()
	close(done)
	listenerWg.Wait()

	close(jobs)
	report(50, "feed collected")
	downloadedCount, skippedCount := pool.Wait()
	report(90, "downloads done")

	if err != nil {
		slog.Error("Twitter chromedp scraping task failed", "user", username, "error", err)
		return err
	}

	// Save posts.json if saveText is enabled
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

func findTweets(node interface{}, callback func(tweet map[string]interface{})) {
	if m, ok := node.(map[string]interface{}); ok {
		if legacy, ok := m["legacy"].(map[string]interface{}); ok {
			if _, hasId := legacy["id_str"].(string); hasId {
				callback(m)
			}
		}
		for _, val := range m {
			findTweets(val, callback)
		}
	} else if arr, ok := node.([]interface{}); ok {
		for _, val := range arr {
			findTweets(val, callback)
		}
	}
}

func downloadTwitterImage(ctx context.Context, item TwitterDownloadItem, outputDir string, client *http.Client, username string) bool {
	parsedURL, err := url.Parse(item.URL)
	if err != nil {
		return false
	}

	parts := strings.Split(parsedURL.Path, "/")
	originalName := parts[len(parts)-1]

	if strings.Contains(originalName, "?") {
		originalName = strings.Split(originalName, "?")[0]
	}

	formatURL := item.URL
	if !strings.Contains(formatURL, "?") {
		formatURL += "?format=jpg&name=large"
	}

	filename := fmt.Sprintf("%s_%s", item.DateStr, originalName)
	if !strings.Contains(filename, ".") {
		filename += ".jpg"
	}
	filePath := filepath.Join(outputDir, filename)

	downloaded, err := download.File(ctx, client, formatURL, filePath, download.FileOpts{
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
