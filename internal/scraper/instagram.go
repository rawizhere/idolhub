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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"idolhub/internal/config"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func ScrapeInstagramUser(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
	c := config.GetConfig()
	if c.InstagramSessionID == "" {
		return fmt.Errorf("instagram session ID is not configured")
	}
	return scrapeInstagramDirect(ctx, username, saveText, lastSync)
}

// --- Direct Instagram backend ---

// igDirectItem holds a media URL and metadata
type igDirectItem struct {
	URL     string
	Date    time.Time
	MediaID string
	IsVideo bool
	Caption string
}

// igImageVersions wraps candidates returned by the API
type igImageVersions struct {
	Candidates []igCandidate `json:"candidates"`
}

type igCandidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type igVideoVersion struct {
	URL    string `json:"url"`
	Type   int    `json:"type"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// bestImageURL picks the highest-resolution image candidate
func bestImageURL(iv *igImageVersions) string {
	if iv == nil || len(iv.Candidates) == 0 {
		return ""
	}
	return slices.MaxFunc(iv.Candidates, func(a, b igCandidate) int {
		areaA := a.Width * a.Height
		areaB := b.Width * b.Height
		return cmp.Compare(areaA, areaB)
	}).URL
}

// bestVideoURL picks the highest-resolution video version
func bestVideoURL(vs []igVideoVersion) string {
	if len(vs) == 0 {
		return ""
	}
	return slices.MaxFunc(vs, func(a, b igVideoVersion) int {
		areaA := a.Width * a.Height
		areaB := b.Width * b.Height
		return cmp.Compare(areaA, areaB)
	}).URL
}

// scrapeInstagramDirect pulls timeline media via the private Instagram web API
func scrapeInstagramDirect(ctx context.Context, username string, saveText bool, lastSync time.Time) error {
	c := config.GetConfig()
	sessionID := c.InstagramSessionID
	if sessionID == "" {
		return fmt.Errorf("instagram session ID is not set")
	}

	numWorkers := cmp.Or(c.Concurrency, 5)

	slog.Info("Scraping Instagram target user via direct API", "user", username, "platform", "instagram", "last_sync", lastSync)

	outputDir := filepath.Join("downloads", "instagram", username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		slog.Error("Failed to create output folder", "user", username, "error", err)
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 900),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	dpCtx, cancelDp := chromedp.NewContext(allocCtx)
	defer cancelDp()

	dpCtx, cancelTimeout := context.WithTimeout(dpCtx, 2*time.Hour)
	defer cancelTimeout()

	if err := chromedp.Run(dpCtx, network.Enable()); err != nil {
		return fmt.Errorf("failed to enable network: %w", err)
	}

	navSuccess := false
	for navAttempt := 1; navAttempt <= 3; navAttempt++ {
		navErr := chromedp.Run(dpCtx,
			navigateNoWait("https://www.instagram.com/"),
			chromedp.Sleep(2*time.Second),
		)
		if navErr == nil {
			navSuccess = true
			break
		}
		slog.Warn("Instagram initial navigation failed, retrying", "user", username, "attempt", navAttempt, "error", navErr)
		if navAttempt < 3 {
			time.Sleep(time.Duration(navAttempt*2) * time.Second)
		}
	}
	if !navSuccess {
		slog.Warn("Instagram initial navigation failed after retries, trying cookie injection anyway", "user", username)
	}

	if err := chromedp.Run(dpCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
			return network.SetCookie("sessionid", sessionID).
				WithDomain(".instagram.com").
				WithPath("/").
				WithExpires(&expr).
				WithHTTPOnly(true).
				WithSecure(true).
				Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("failed to set Instagram session cookie: %w", err)
	}

	var isLoggedIn bool
	if err := chromedp.Run(dpCtx,
		navigateNoWait("https://www.instagram.com/accounts/edit/"),
		igJitterAction(2*time.Second, 2*time.Second),
		chromedp.Evaluate(`window.location.pathname !== '/accounts/login/' && !document.querySelector('input[name="username"]')`, &isLoggedIn),
	); err != nil {
		return fmt.Errorf("failed to verify Instagram session: %w", err)
	}
	if !isLoggedIn {
		slog.Error("Instagram session ID is invalid or expired", "user", username)
		return fmt.Errorf("instagram session ID is invalid or expired — please log in and copy a fresh sessionid cookie: %w", ErrAuthExpired)
	}
	slog.Info("Instagram session is valid", "user", username)

	userID, err := resolveInstagramUserID(dpCtx, username)
	if err != nil {
		return fmt.Errorf("failed to resolve Instagram user ID: %w", err)
	}
	slog.Info("Resolved Instagram user ID", "user", username, "user_id", userID)

	jobs := make(chan igDirectItem, 1000)
	var wg sync.WaitGroup
	var downloadedCount int32
	var skippedCount int32
	var postsJSON []map[string]interface{}
	var postsMu sync.Mutex

	slog.Info("Spawning concurrent download workers for Instagram direct", "user", username, "workers_count", numWorkers)
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if downloadInstagramDirectMedia(item, outputDir, &postsMu, &postsJSON, username) {
					atomic.AddInt32(&downloadedCount, 1)
				} else {
					atomic.AddInt32(&skippedCount, 1)
				}
			}
		}()
	}

	var nextMaxID string
	page := 0
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		default:
		}
		page++

		var feedJSON string
		apiURL := fmt.Sprintf("https://www.instagram.com/api/v1/feed/user/%s/?count=50", userID)
		if nextMaxID != "" {
			apiURL += "&max_id=" + nextMaxID
		}

		slog.Info("Fetching Instagram media feed page", "user", username, "page", page, "url", apiURL)

		if err := chromedp.Run(dpCtx,
			chromedp.Evaluate(fmt.Sprintf(`
				fetch(%q, {
					credentials: "include",
					headers: {
						"accept": "*/*",
						"x-ig-app-id": "936619743392459",
						"x-requested-with": "XMLHttpRequest",
						"referer": "https://www.instagram.com/%s/"
					}
				}).then(r => r.text()).then(t => window.__igFeed = t).catch(e => window.__igFeed = "ERROR:" + e.message)
			`, apiURL, url.PathEscape(username)), nil),
			igJitterAction(2*time.Second, 2*time.Second),
			chromedp.Evaluate(`window.__igFeed || ""`, &feedJSON),
		); err != nil {
			slog.Error("Failed to fetch Instagram feed page", "user", username, "page", page, "error", err)
			break
		}

		if strings.HasPrefix(feedJSON, "ERROR:") {
			slog.Error("Instagram feed fetch returned error", "user", username, "error", feedJSON)
			break
		}
		if feedJSON == "" {
			slog.Info("Empty Instagram feed response, stopping", "user", username)
			break
		}

		var feed struct {
			Items []struct {
				ID      string `json:"pk"`
				Caption *struct {
					Text string `json:"text"`
				} `json:"caption"`
				TakenAt       int64            `json:"taken_at"`
				MediaType     int              `json:"media_type"`
				ImageVersions *igImageVersions `json:"image_versions2,omitempty"`
				CarouselMedia []struct {
					ID            string           `json:"pk"`
					MediaType     int              `json:"media_type"`
					ImageVersions *igImageVersions `json:"image_versions2,omitempty"`
					VideoVersions []igVideoVersion `json:"video_versions,omitempty"`
				} `json:"carousel_media,omitempty"`
				VideoVersions []igVideoVersion `json:"video_versions,omitempty"`
			} `json:"items"`
			MoreAvailable bool   `json:"more_available"`
			NextMaxID     string `json:"next_max_id"`
		}

		if err := json.Unmarshal([]byte(feedJSON), &feed); err != nil {
			slog.Error("Failed to parse Instagram feed JSON", "user", username, "error", err)
			break
		}

		slog.Info("Instagram feed page parsed", "user", username, "page", page, "items", len(feed.Items), "more_available", feed.MoreAvailable)

		reachedOldPosts := false
		for _, item := range feed.Items {
			itemTime := time.Unix(item.TakenAt, 0).UTC()

			if !lastSync.IsZero() && itemTime.Before(lastSync) {
				reachedOldPosts = true
				break
			}

			caption := ""
			if item.Caption != nil {
				caption = item.Caption.Text
			}
			dateStr := itemTime.Format("2006-01-02_15-04-05")

			var mediaURLs []string
			var postEntries []igDirectItem

			if item.MediaType == 8 && len(item.CarouselMedia) > 0 {
				for _, cm := range item.CarouselMedia {
					if cm.MediaType == 2 {
						if v := bestVideoURL(cm.VideoVersions); v != "" {
							postEntries = append(postEntries, igDirectItem{
								URL: v, Date: itemTime, MediaID: cm.ID, IsVideo: true, Caption: caption,
							})
						}
					} else if u := bestImageURL(cm.ImageVersions); u != "" {
						postEntries = append(postEntries, igDirectItem{
							URL: u, Date: itemTime, MediaID: cm.ID, IsVideo: false, Caption: caption,
						})
					}
				}
			} else if item.MediaType == 2 {
				if v := bestVideoURL(item.VideoVersions); v != "" {
					postEntries = append(postEntries, igDirectItem{
						URL: v, Date: itemTime, MediaID: item.ID, IsVideo: true, Caption: caption,
					})
				}
			} else if u := bestImageURL(item.ImageVersions); u != "" {
				postEntries = append(postEntries, igDirectItem{
					URL: u, Date: itemTime, MediaID: item.ID, IsVideo: false, Caption: caption,
				})
			}

			for _, pe := range postEntries {
				mediaURLs = append(mediaURLs, pe.URL)
			}

			for _, entry := range postEntries {
				select {
				case jobs <- entry:
				default:
					slog.Warn("Instagram download queue full, dropping item", "user", username, "media_id", entry.MediaID)
				}
			}

			if len(postEntries) > 0 {
				postsMu.Lock()
				postsJSON = append(postsJSON, map[string]interface{}{
					"tweet_id":   item.ID,
					"date":       dateStr,
					"text":       caption,
					"media_urls": mediaURLs,
				})
				postsMu.Unlock()
			}
		}

		if reachedOldPosts {
			slog.Info("Reached previously scraped Instagram posts, stopping pagination", "user", username)
			break
		}

		if !feed.MoreAvailable || feed.NextMaxID == "" {
			slog.Info("Reached end of Instagram media feed", "user", username)
			break
		}
		nextMaxID = feed.NextMaxID

		// Rate-limit with jitter between pages
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case <-time.After(time.Duration(2000+rand.IntN(2000)) * time.Millisecond):
		}
	}

	close(jobs)
	wg.Wait()

	if saveText && len(postsJSON) > 0 {
		postsPath := filepath.Join("downloads", "instagram", username, "posts.json")
		data, err := json.MarshalIndent(postsJSON, "", "  ")
		if err == nil {
			if err := os.WriteFile(postsPath, data, 0644); err != nil {
				slog.Warn("Failed to write Instagram posts.json", "user", username, "error", err)
			}
		}
	}

	slog.Info("Instagram direct sync progress summary", "user", username, "platform", "instagram", "downloaded", downloadedCount, "skipped_existing", skippedCount)
	return nil
}

// igJitterAction returns a random chromedp.Sleep action
func igJitterAction(min, span time.Duration) chromedp.Action {
	return chromedp.Sleep(min + time.Duration(rand.Int64N(int64(span))))
}

// resolveInstagramUserID resolves numeric user ID from username
func resolveInstagramUserID(ctx context.Context, username string) (string, error) {
	time.Sleep(1*time.Second + time.Duration(rand.Int64N(2*int64(time.Second))))

	profileAPI := fmt.Sprintf("https://www.instagram.com/api/v1/users/web_profile_info/?username=%s", url.PathEscape(username))
	var profileJSON string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`
			fetch(%q, {
				credentials: "include",
				headers: {
					"accept": "*/*",
					"x-ig-app-id": "936619743392459",
					"x-requested-with": "XMLHttpRequest",
					"referer": "https://www.instagram.com/%s/"
				}
			}).then(r => r.text()).then(t => window.__igProfile = t).catch(e => window.__igProfile = "ERROR:" + e.message)
		`, profileAPI, url.PathEscape(username)), nil),
		igJitterAction(2*time.Second, 2*time.Second),
		chromedp.Evaluate(`window.__igProfile || ""`, &profileJSON),
	); err != nil {
		return "", fmt.Errorf("failed to query Instagram web_profile_info: %w", err)
	}

	if strings.HasPrefix(profileJSON, "ERROR:") {
		slog.Warn("Instagram web_profile_info returned error, falling back to search", "username", username, "error", profileJSON)
	} else if profileJSON != "" {
		var profile struct {
			Data struct {
				User struct {
					ID string `json:"id"`
					Pk int64  `json:"pk"`
				} `json:"user"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(profileJSON), &profile); err == nil {
			if profile.Data.User.ID != "" {
				return profile.Data.User.ID, nil
			}
		}
	}

	// Fall back to the topsearch API
	time.Sleep(1*time.Second + time.Duration(rand.Int64N(2*int64(time.Second))))
	var searchJSON string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`
			fetch("https://www.instagram.com/api/v1/web/search/topsearch/?query=%s", {
				credentials: "include",
				headers: {
					"accept": "*/*",
					"x-ig-app-id": "936619743392459",
					"x-requested-with": "XMLHttpRequest"
				}
			}).then(r => r.text()).then(t => window.__igSearch = t).catch(e => window.__igSearch = "")
		`, url.PathEscape(username)), nil),
		igJitterAction(2*time.Second, 2*time.Second),
		chromedp.Evaluate(`window.__igSearch || ""`, &searchJSON),
	); err != nil {
		return "", fmt.Errorf("failed to query Instagram search API: %w", err)
	}

	if searchJSON != "" {
		var search struct {
			Users []struct {
				User struct {
					ID       string `json:"pk"`
					Username string `json:"username"`
				} `json:"user"`
			} `json:"users"`
		}
		if err := json.Unmarshal([]byte(searchJSON), &search); err == nil {
			for _, u := range search.Users {
				if strings.EqualFold(u.User.Username, username) {
					return u.User.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not resolve Instagram user ID for @%s", username)
}

// downloadInstagramDirectMedia downloads a media item from CDN
func downloadInstagramDirectMedia(item igDirectItem, outputDir string, postsMu *sync.Mutex, postsJSON *[]map[string]interface{}, username string) bool {
	ext := ".jpg"
	if item.IsVideo {
		ext = ".mp4"
	}

	parsed, err := url.Parse(item.URL)
	if err != nil {
		slog.Warn("Failed to parse Instagram CDN URL", "user", username, "url", item.URL, "error", err)
		return false
	}
	pathParts := strings.Split(parsed.Path, "/")
	baseName := pathParts[len(pathParts)-1]
	if baseName == "" {
		baseName = item.MediaID
	}
	if idx := strings.Index(baseName, "?"); idx >= 0 {
		baseName = baseName[:idx]
	}
	if filepath.Ext(baseName) == "" {
		baseName += ext
	}

	datePrefix := item.Date.Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("%s_%s", datePrefix, baseName)

	filePath := filepath.Join(outputDir, filename)

	if _, err := os.Stat(filePath); err == nil {
		return false
	}

	req, err := http.NewRequest("GET", item.URL, nil)
	if err != nil {
		slog.Warn("Failed to build Instagram CDN request", "user", username, "filename", filename, "error", err)
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.instagram.com/")
	req.Header.Set("Accept", "image/avif,image/webp,*/*;q=0.8")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("Failed to download Instagram direct media", "user", username, "filename", filename, "error", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Instagram CDN returned error code", "user", username, "filename", filename, "status", resp.StatusCode)
		return false
	}

	out, err := os.Create(filePath)
	if err != nil {
		slog.Warn("Failed to create file on disk", "user", username, "filename", filename, "error", err)
		return false
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		slog.Warn("Failed to write data to file", "user", username, "filename", filename, "error", err)
		return false
	}

	label := "photo"
	if item.IsVideo {
		label = "video"
	}
	slog.Info("Instagram file downloaded", "user", username, "filename", filename, "type", label)

	go func() {
		thumbDir := filepath.Join(outputDir, "thumbnails")
		thumbFilename := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".jpg"
		_ = GenerateThumbnail(filePath, filepath.Join(thumbDir, thumbFilename))
	}()
	return true
}
