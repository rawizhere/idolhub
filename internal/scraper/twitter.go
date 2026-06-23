package scraper

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"idolhub/internal/config"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
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

func parseTwitterSnowflakeDate(tweetIDStr string) string {
	tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
	if err != nil {
		return "unknown_date"
	}
	const epoch int64 = 1288834974657
	timestampMS := (tweetID >> 22) + epoch
	t := time.UnixMilli(timestampMS).UTC()
	return t.Format("2006-01-02_15-04-05")
}

func getTweetTime(tweetIDStr string) (time.Time, error) {
	tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	const epoch int64 = 1288834974657
	timestampMS := (tweetID >> 22) + epoch
	return time.UnixMilli(timestampMS).UTC(), nil
}

func navigateNoWait(urlStr string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, err := page.Navigate(urlStr).Do(ctx)
		return err
	})
}

func ScrapeTwitterUser(ctx context.Context, cfg config.Store, username string, saveText bool, skipRetweets bool, filters []string, downloadPhotos bool, downloadVideos bool, lastSync time.Time) error {
	slog.Info("Scraping Twitter target user", "user", username, "platform", "twitter", "save_text", saveText, "skip_retweets", skipRetweets, "filters", filters, "download_photos", downloadPhotos, "download_videos", downloadVideos, "last_sync", lastSync)

	c := cfg.Get()
	twitterAuthToken := c.TwitterAuthToken
	if twitterAuthToken == "" {
		err := fmt.Errorf("twitter auth token is not set in settings")
		slog.Error("Scraper aborted", "error", err)
		return err
	}

	numWorkers := cmp.Or(c.Concurrency, 5)

	outputDir := filepath.Join("downloads", "twitter", username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		slog.Error("Failed to create output directory", "user", username, "error", err)
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1920, 1080),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	dpCtx, cancelDp := chromedp.NewContext(allocCtx)
	defer cancelDp()

	dpCtx, cancelTimeout := context.WithTimeout(dpCtx, 2*time.Hour)
	defer cancelTimeout()

	jobs := make(chan TwitterDownloadItem, 10000)
	var wg sync.WaitGroup
	var downloadedCount int32
	var skippedCount int32
	client := &http.Client{Timeout: 45 * time.Second}

	slog.Info("Spawning concurrent download workers for Twitter", "user", username, "workers_count", numWorkers)
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
					var downloaded bool
					if item.IsVideo {
						downloaded = downloadTwitterVideo(ctx, item, outputDir, client, username)
					} else {
						downloaded = downloadTwitterImage(ctx, item, outputDir, client, username)
					}
					if downloaded {
						atomic.AddInt32(&downloadedCount, 1)
					} else {
						atomic.AddInt32(&skippedCount, 1)
					}
				}
			}
		}()
	}

	queuedURLs := make(map[string]bool)
	var scrapedPosts []TweetPost
	var postsMu sync.Mutex
	var scrollCounter int32 = 0
	reachedOldTweets := false

	// done is closed before jobs to prevent races on send
	done := make(chan struct{})
	var listenerWg sync.WaitGroup

	seenTweets := make(map[string]map[string]interface{})
	repliesIndex := make(map[string][]string)
	matchedIDs := make(map[string]bool)

	var findScreenName func(node interface{}) string
	findScreenName = func(node interface{}) string {
		if m, ok := node.(map[string]interface{}); ok {
			if sn, ok := m["screen_name"].(string); ok && sn != "" {
				return sn
			}
			for _, val := range m {
				if sn := findScreenName(val); sn != "" {
					return sn
				}
			}
		} else if arr, ok := node.([]interface{}); ok {
			for _, val := range arr {
				if sn := findScreenName(val); sn != "" {
					return sn
				}
			}
		}
		return ""
	}

	getTweetAuthor := func(tweet map[string]interface{}) string {
		if core, ok := tweet["core"].(map[string]interface{}); ok {
			if sn := findScreenName(core); sn != "" {
				return sn
			}
		}
		if userResults, ok := tweet["user_results"].(map[string]interface{}); ok {
			if sn := findScreenName(userResults); sn != "" {
				return sn
			}
		}
		return ""
	}

	// Must be called with postsMu held
	queueTweetLocked := func(tweetID string) {
		tweet, ok := seenTweets[tweetID]
		if !ok {
			return
		}
		// Only download media from the target user's own tweets
		author := getTweetAuthor(tweet)
		if !strings.EqualFold(author, username) {
			return
		}
		legacy, ok := tweet["legacy"].(map[string]interface{})
		if !ok {
			return
		}
		text, _ := legacy["full_text"].(string)
		if text == "" {
			text, _ = legacy["text"].(string)
		}
		dateStr := parseTwitterSnowflakeDate(tweetID)

		var tweetMediaURLs []string
		if extEntities, ok := legacy["extended_entities"].(map[string]interface{}); ok {
			if mediaList, ok := extEntities["media"].([]interface{}); ok {
				for _, m := range mediaList {
					itemMap, ok := m.(map[string]interface{})
					if !ok {
						continue
					}
					downloadURL, _ := itemMap["media_url_https"].(string)
					isVideo := false

					// Check for video variants
					if videoInfo, ok := itemMap["video_info"].(map[string]interface{}); ok {
						if variants, ok := videoInfo["variants"].([]interface{}); ok {
							bestVideoURL := ""
							maxBitrate := -1
							for _, v := range variants {
								vMap, ok := v.(map[string]interface{})
								if !ok {
									continue
								}
								cType, _ := vMap["content_type"].(string)
								if cType == "video/mp4" {
									bitrateVal := vMap["bitrate"]
									var bitrate int
									if bFloat, ok := bitrateVal.(float64); ok {
										bitrate = int(bFloat)
									} else if bInt, ok := bitrateVal.(int); ok {
										bitrate = bInt
									}
									if bitrate > maxBitrate {
										maxBitrate = bitrate
										bestVideoURL, _ = vMap["url"].(string)
									}
								}
							}
							if bestVideoURL != "" {
								downloadURL = bestVideoURL
								isVideo = true
							}
						}
					}

					if downloadURL != "" {
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
						}
						tweetMediaURLs = append(tweetMediaURLs, downloadURL)
					}
				}
			}
		}
		var youtubeURLs []string
		if entities, ok := legacy["entities"].(map[string]interface{}); ok {
			if urlsList, ok := entities["urls"].([]interface{}); ok {
				for _, u := range urlsList {
					uMap, ok := u.(map[string]interface{})
					if !ok {
						continue
					}
					expanded, _ := uMap["expanded_url"].(string)
					if expanded != "" {
						if strings.Contains(expanded, "youtube.com") || strings.Contains(expanded, "youtu.be") {
							youtubeURLs = append(youtubeURLs, expanded)
						}
					}
				}
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
					return
				}

				var raw interface{}
				if err := json.Unmarshal(rb, &raw); err != nil {
					return
				}

				// Parse tweets from intercepted responses
				findTweets(raw, func(tweet map[string]interface{}) {
					legacy, ok := tweet["legacy"].(map[string]interface{})
					if !ok {
						return
					}
					tweetID, _ := legacy["id_str"].(string)
					if tweetID == "" {
						return
					}

					if !lastSync.IsZero() {
						tweetTime, err := getTweetTime(tweetID)
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

					text, _ := legacy["full_text"].(string)
					if text == "" {
						text, _ = legacy["text"].(string)
					}

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

					seenTweets[tweetID] = tweet
					inReplyToStatusID, _ := legacy["in_reply_to_status_id_str"].(string)
					if inReplyToStatusID != "" {
						repliesIndex[inReplyToStatusID] = append(repliesIndex[inReplyToStatusID], tweetID)
					}

					// Check if this tweet is matched
					isMatched := false
					if len(filters) > 0 {
						lowerText := strings.ToLower(text)
						for _, filter := range filters {
							if strings.Contains(lowerText, strings.ToLower(filter)) {
								isMatched = true
								break
							}
						}
					} else {
						// No filters -> matches everything
						isMatched = true
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

	err := chromedp.Run(dpCtx,
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

	err = chromedp.Run(dpCtx,
		navigateNoWait(targetURL),
		chromedp.Sleep(5*time.Second),
		// Scroll timeline loop
		chromedp.ActionFunc(func(ctx context.Context) error {
			lastCount := 0
			retries := 0
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

				sleepSec := 5 + rand.IntN(5) // Sleep between 5 and 9 seconds
				err := chromedp.Run(ctx,
					chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
					chromedp.Sleep(time.Duration(sleepSec)*time.Second),
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
	wg.Wait()

	if err != nil {
		slog.Error("Twitter chromedp scraping task failed", "user", username, "error", err)
		return err
	}

	// Save posts.json if saveText is enabled
	if saveText && len(scrapedPosts) > 0 {
		slog.Info("Saving tweet texts to single JSON", "user", username)
		postsFilePath := filepath.Join(outputDir, "posts.json")

		// Load existing posts
		var existingPosts []TweetPost
		if data, err := os.ReadFile(postsFilePath); err == nil {
			_ = json.Unmarshal(data, &existingPosts)
		}

		// Merge posts (avoiding duplicates by tweet_id)
		postMap := make(map[string]TweetPost)
		for _, p := range existingPosts {
			postMap[p.TweetID] = p
		}
		for _, p := range scrapedPosts {
			postMap[p.TweetID] = p
		}

		var mergedPosts []TweetPost
		for _, p := range postMap {
			mergedPosts = append(mergedPosts, p)
		}

		jsonData, err := json.MarshalIndent(mergedPosts, "", "  ")
		if err == nil {
			_ = os.WriteFile(postsFilePath, jsonData, 0644)
			slog.Info("Tweet texts saved successfully", "user", username, "file", postsFilePath)
		} else {
			slog.Warn("Failed to marshal tweet posts JSON", "user", username, "error", err)
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

	if _, err := os.Stat(filePath); err == nil {
		return false // File already exists
	}

	// Jitter delay
	time.Sleep(time.Duration(500+rand.IntN(1500)) * time.Millisecond)

	req, err := http.NewRequestWithContext(ctx, "GET", formatURL, nil)
	if err != nil {
		slog.Error("Failed to create image download request", "user", username, "url", formatURL, "error", err)
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	var resp *http.Response
	downloadSuccess := false

	for attempt := 1; attempt <= 3; attempt++ {
		resp, err = client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			downloadSuccess = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}

	if !downloadSuccess {
		slog.Warn("Failed to download image", "user", username, "filename", filename, "error", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	out, err := os.Create(filePath)
	if err != nil {
		slog.Error("Failed to create image file", "user", username, "path", filePath, "error", err)
		return false
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		slog.Warn("Failed to save image bytes to disk", "user", username, "filename", filename, "error", err)
		return false
	}

	slog.Info("Twitter image downloaded", "user", username, "filename", filename)
	go func() {
		thumbDir := filepath.Join(outputDir, "thumbnails")
		thumbFilename := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".jpg"
		_ = GenerateThumbnail(filePath, filepath.Join(thumbDir, thumbFilename))
	}()
	return true
}

func downloadTwitterVideo(ctx context.Context, item TwitterDownloadItem, outputDir string, client *http.Client, username string) bool {
	filename := fmt.Sprintf("%s_%s_video.mp4", item.DateStr, item.TweetID)
	filePath := filepath.Join(outputDir, filename)

	if _, err := os.Stat(filePath); err == nil {
		return false // File already exists
	}

	// Jitter delay
	time.Sleep(time.Duration(500+rand.IntN(1500)) * time.Millisecond)

	slog.Info("Starting Twitter video download", "user", username, "tweet_id", item.TweetID)
	req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
	if err != nil {
		slog.Error("Failed to create video download request", "user", username, "url", item.URL, "error", err)
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("Failed to download video", "user", username, "tweet_id", item.TweetID, "error", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Failed to download video: server status error", "user", username, "tweet_id", item.TweetID, "status", resp.StatusCode)
		return false
	}

	out, err := os.Create(filePath)
	if err != nil {
		slog.Error("Failed to create video file", "user", username, "path", filePath, "error", err)
		return false
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		slog.Warn("Failed to write video file", "user", username, "tweet_id", item.TweetID, "error", err)
		return false
	}

	slog.Info("Twitter video downloaded successfully", "user", username, "filename", filename)
	go func() {
		thumbDir := filepath.Join(outputDir, "thumbnails")
		thumbFilename := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".jpg"
		_ = GenerateThumbnail(filePath, filepath.Join(thumbDir, thumbFilename))
	}()
	return true
}
