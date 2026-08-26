package scraper

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
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

	"idolhub/internal/download"
	"idolhub/internal/store"
)

func ScrapeInstagramUser(ctx context.Context, t Target, opts Options) error {
	if opts.InstagramSessionID == "" {
		return fmt.Errorf("instagram session ID is not configured")
	}
	if opts.Posts == nil {
		return fmt.Errorf("post store is not configured")
	}
	return scrapeInstagramDirect(ctx, t.Username, t.SaveText, opts.LastSync, opts.ForceFull, opts.InstagramSessionID, opts.Posts, opts.OnProgress)
}

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
func scrapeInstagramDirect(ctx context.Context, username string, saveText bool, lastSync time.Time, forceFull bool, sessionID string, posts *store.PostStore, onProgress func(pct int, msg string)) error {
	if sessionID == "" {
		return fmt.Errorf("instagram session ID is not set")
	}

	numWorkers := 5

	report := func(pct int, msg string) {
		if onProgress != nil {
			onProgress(pct, msg)
		}
	}

	slog.Info("Scraping Instagram target user via direct API", "user", username, "platform", "instagram", "last_sync", lastSync)

	outputDir := filepath.Join("downloads", "instagram", username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		slog.Error("Failed to create output folder", "user", username, "error", err)
		return err
	}

	client := newIGClient(sessionID)
	userID, err := client.resolveUserID(ctx, username)
	if err != nil {
		return err
	}
	slog.Info("Resolved Instagram user ID", "user", username, "user_id", userID)
	report(30, "session valid")
	report(35, "resolved user id")

	fileIdx := newIgFileIndex(outputDir)
	cdnClient := &http.Client{Timeout: 5 * time.Minute}

	jobs := make(chan igDirectItem, 1000)
	var downloadedCount int32
	var skippedCount int32
	postsSaved := 0

	pool := download.Start(ctx, jobs, numWorkers, func(ctx context.Context, item igDirectItem) bool {
		return downloadInstagramDirectMedia(ctx, item, outputDir, username, fileIdx, cdnClient)
	})

	var nextMaxID string
	page := 0
	reachedOldPosts := false
	consecutiveExistingPosts := 0
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			pool.Wait()
			return ctx.Err()
		default:
		}
		page++

		apiURL := fmt.Sprintf("https://www.instagram.com/api/v1/feed/user/%s/?count=50", userID)
		if nextMaxID != "" {
			apiURL += "&max_id=" + nextMaxID
		}

		slog.Info("Fetching Instagram media feed page", "user", username, "page", page, "url", apiURL)

		feedBytes, ferr := client.doGet(ctx, apiURL, username)
		if ferr != nil {
			if errors.Is(ferr, download.ErrRateLimited) || errors.Is(ferr, ErrAuthExpired) {
				close(jobs)
				pool.Wait()
				return ferr
			}
			slog.Error("Failed to fetch Instagram feed page", "user", username, "page", page, "error", ferr)
			break
		}
		if len(feedBytes) == 0 {
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

		if err := json.Unmarshal(feedBytes, &feed); err != nil {
			slog.Error("Failed to parse Instagram feed JSON", "user", username, "error", err)
			break
		}

		slog.Info("Instagram feed page parsed", "user", username, "page", page, "items", len(feed.Items), "more_available", feed.MoreAvailable)
		report(50, "feed parsed")

		for _, item := range feed.Items {
			itemTime := time.Unix(item.TakenAt, 0).UTC()
			caption := ""
			if item.Caption != nil {
				caption = item.Caption.Text
			}
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

			// Check if this post is older than lastSync or already exists on disk
			isOlderThanLastSync := !lastSync.IsZero() && itemTime.Before(lastSync)

			allExisting := false
			if !forceFull {
				allExisting = len(postEntries) > 0
				for _, pe := range postEntries {
					if !fileIdx.exists(pe) {
						allExisting = false
						break
					}
				}
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

			for _, entry := range postEntries {
				select {
				case jobs <- entry:
				default:
					slog.Warn("Instagram download queue full, dropping item", "user", username, "media_id", entry.MediaID)
				}
			}

			if len(postEntries) > 0 && saveText {
				var media []store.PostMedia
				for _, pe := range postEntries {
					kind := "photo"
					if pe.IsVideo {
						kind = "video"
					}
					media = append(media, store.PostMedia{URL: pe.URL, Kind: kind})
				}
				if err := posts.UpsertPost(ctx, store.Post{
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

		if !feed.MoreAvailable || feed.NextMaxID == "" {
			slog.Info("Reached end of Instagram media feed", "user", username)
			break
		}
		nextMaxID = feed.NextMaxID

		// Rate-limit with jitter between pages
		if err := igLimiter.Wait(ctx); err != nil {
			close(jobs)
			pool.Wait()
			return ctx.Err()
		}
	}

	close(jobs)
	downloadedCount, skippedCount = pool.Wait()
	report(90, "downloads done")

	slog.Info("Instagram direct sync progress summary", "user", username, "platform", "instagram", "downloaded", downloadedCount, "skipped_existing", skippedCount, "posts_saved", postsSaved)
	return nil
}

// resolveUserID resolves the numeric user ID from username, doubling as a session check.
func (c *igClient) resolveUserID(ctx context.Context, username string) (string, error) {
	profileAPI := fmt.Sprintf("https://www.instagram.com/api/v1/users/web_profile_info/?username=%s", url.PathEscape(username))
	profileBytes, err := c.doGet(ctx, profileAPI, username)
	if err != nil {
		slog.Warn("Instagram web_profile_info returned error, falling back to search", "username", username, "error", err)
	} else {
		var profile struct {
			Data struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			} `json:"data"`
		}
		if err := json.Unmarshal(profileBytes, &profile); err == nil && profile.Data.User.ID != "" {
			return profile.Data.User.ID, nil
		}
	}

	searchAPI := fmt.Sprintf("https://www.instagram.com/api/v1/web/search/topsearch/?query=%s", url.PathEscape(username))
	searchBytes, err := c.doGet(ctx, searchAPI, username)
	if err != nil {
		return "", fmt.Errorf("failed to query Instagram search API: %w", err)
	}

	var search struct {
		Users []struct {
			User struct {
				ID       string `json:"pk"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"users"`
	}
	if err := json.Unmarshal(searchBytes, &search); err == nil {
		for _, u := range search.Users {
			if strings.EqualFold(u.User.Username, username) {
				return u.User.ID, nil
			}
		}
	}

	return "", fmt.Errorf("%w: could not resolve Instagram user ID for @%s (session may be expired or rate-limited)", ErrAuthExpired, username)
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

func (idx *igFileIndex) exists(item igDirectItem) bool {
	ext := ".jpg"
	if item.IsVideo {
		ext = ".mp4"
	}
	parsed, err := url.Parse(item.URL)
	if err != nil {
		return false
	}
	pathParts := strings.Split(parsed.Path, "/")
	baseName := pathParts[len(pathParts)-1]
	if baseName == "" {
		baseName = item.MediaID
	}
	if qIdx := strings.Index(baseName, "?"); qIdx >= 0 {
		baseName = baseName[:qIdx]
	}
	if filepath.Ext(baseName) == "" {
		baseName += ext
	}
	datePrefix := item.Date.Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("%s_%s", datePrefix, baseName)

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
func downloadInstagramDirectMedia(ctx context.Context, item igDirectItem, outputDir string, username string, fileIdx *igFileIndex, client *http.Client) bool {
	if fileIdx != nil && fileIdx.exists(item) {
		return false
	}

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

	header := http.Header{
		"User-Agent": []string{desktopUA},
		"Referer":    []string{"https://www.instagram.com/"},
		"Accept":     []string{"image/avif,image/webp,*/*;q=0.8"},
	}
	downloaded, err := download.File(ctx, client, item.URL, filePath, download.FileOpts{Header: header})
	if err != nil {
		slog.Warn("Failed to download Instagram direct media", "user", username, "filename", filename, "error", err)
		return false
	}
	if !downloaded {
		if fileIdx != nil {
			fileIdx.add(filename)
		}
		return false
	}

	label := "photo"
	if item.IsVideo {
		label = "video"
	}
	slog.Info("Instagram file downloaded", "user", username, "filename", filename, "type", label)

	download.ThumbnailAsync(filePath)
	return true
}
