package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idolhub/internal/download"
	"idolhub/internal/store"

	ytdlp "github.com/lrstanley/go-ytdlp"
)

// ytExecutable is the resolved yt-dlp binary; empty until EnsureYTDLP finds or installs one.
var ytExecutable string

// EnsureYTDLP resolves the yt-dlp binary, falling back to go-ytdlp install if missing from PATH.
func EnsureYTDLP(ctx context.Context) {
	if path, err := exec.LookPath("yt-dlp"); err == nil {
		ytExecutable = path
		slog.Info("yt-dlp binary resolved from PATH", "path", ytExecutable)
		return
	}
	slog.Info("yt-dlp not found in PATH, attempting auto-install...")
	resolved, err := ytdlp.Install(ctx, &ytdlp.InstallOptions{AllowVersionMismatch: true})
	if err != nil {
		slog.Warn("yt-dlp not found and auto-install failed; video sources will fail until yt-dlp is installed", "error", err)
		return
	}
	ytExecutable = resolved.Executable
	slog.Info("yt-dlp binary auto-installed successfully", "path", ytExecutable)
}

// StartYTDLPUpdateLoop self-updates yt-dlp at startup and then once per day via `--update-to nightly`.
func StartYTDLPUpdateLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		updateYTDLP(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updateYTDLP(ctx)
			}
		}
	}()
}

func updateYTDLP(parent context.Context) {
	if ytExecutable == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ytExecutable, "--update-to", "nightly")
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("yt-dlp self-update failed", "error", err, "output", strings.TrimSpace(string(out)))
		return
	}
	slog.Info("yt-dlp self-update done", "output", strings.TrimSpace(string(out)))
}

// ytdlpOutput names videos <UTC date>_<id> from TikTok's reported timestamp. The date drifts between requests, so
// NormalizeTikTokNames rewrites every download to the canonical id-derived date after the run; the gallery reads dates
// from this prefix layout.
const ytdlpOutput = "%(timestamp>%Y-%m-%d_%H-%M-%S,upload_date>%Y-%m-%d_00-00-00)s_%(id)s.%(ext)s"

// ScrapeYTDLP downloads videos from TikTok via yt-dlp. Optional Netscape cookies from config are written to a temp file and cleaned up after.
func ScrapeYTDLP(ctx context.Context, t Target, opts Options) error {
	platform := t.Platform
	username := t.Username
	lastSync := opts.LastSync
	if ytExecutable == "" {
		return fmt.Errorf("yt-dlp is not available; install yt-dlp or restart the server")
	}

	slog.Info("yt-dlp scrape starting", "platform", platform, "user", username, "last_sync", lastSync)

	outputDir := filepath.Join("downloads", platform, username)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// YouTube: only the /shorts tab of the channel, H.264/AAC first for browser playback (gemfactory's shorts selector).
	// TikTok: the profile, same H.264 preference. MatchFilters drops audio-only entries (photo-post soundtracks).
	targetURL := "https://www.tiktok.com/@" + username
	format := "bestvideo[vcodec^=avc]+bestaudio[ext=m4a]/bestvideo[vcodec^=avc]+bestaudio/best[vcodec^=avc]/bestvideo+bestaudio/best"
	if platform == "youtube" {
		targetURL = youtubeShortsURL(username)
		format = "bestvideo[vcodec^=avc1]+bestaudio[acodec^=mp4a]/bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best"
	}

	cmd := ytdlp.New().
		Format(format).
		MatchFilters("vcodec!=none").
		IgnoreErrors().
		PrintJSON().
		SocketTimeout(15).
		ExtractorRetries("1").
		Paths(outputDir).
		Output(ytdlpOutput).
		// skip by id: reported timestamps drift between requests, so name-based skipping re-downloads the whole
		// archive on a force sync
		DownloadArchive(MediaArchivePath(platform, username)).
		ProgressFunc(3*time.Second, func(u ytdlp.ProgressUpdate) {
			slog.Info("Downloading video stream", "platform", platform, "user", username, "filename", filepath.Base(u.Filename), "percent", u.PercentString(), "status", u.Status)
		})

	if platform == "youtube" {
		// mweb keeps DASH alive now that web is SABR'd; web_safari adds an HLS fallback (gemfactory's client set)
		cmd = cmd.ExtractorArgs("youtube:player_client=default,mweb,web_safari").
			AddHeaders("Accept-Language:en-US,en;q=0.9").
			ConcurrentFragments(4)
		if potURL := os.Getenv("BGUTIL_POT_URL"); potURL != "" {
			cmd = cmd.ExtractorArgs("youtubepot-bgutilhttp:base_url=" + potURL)
			slog.Info("yt-dlp using bgutil POT provider", "platform", platform, "user", username)
		}
	}

	if !lastSync.IsZero() {
		cmd = cmd.BreakOnExisting().PlaylistItems(":50")
	} else if opts.MaxPosts > 0 {
		cmd = cmd.PlaylistItems(fmt.Sprintf(":%d", opts.MaxPosts))
	}
	if cutoff := opts.cutoff(); !cutoff.IsZero() {
		cmd = cmd.DateAfter(cutoff.Format("20060102"))
	}

	cookies := opts.TikTokCookies
	if platform == "youtube" {
		cookies = opts.YouTubeCookies
	}
	if cookies != "" {
		cookiePath, err := writeCookieFile(cookies)
		if err != nil {
			slog.Warn("failed to write yt-dlp cookie file, continuing without cookies", "platform", platform, "error", err)
		} else {
			defer func() { _ = os.Remove(cookiePath) }()
			cmd = cmd.Cookies(cookiePath)
			slog.Info("yt-dlp using cookies", "platform", platform, "user", username)
			opts.OnProgress(20)
		}
	}

	result, err := cmd.Run(ctx, targetURL)
	if err != nil {
		if result != nil && result.ExitCode == 101 {
			slog.Info("yt-dlp reached previously downloaded or older videos, stopped early", "platform", platform, "user", username)
		} else {
			if result == nil {
				slog.Error("yt-dlp run failed", "platform", platform, "user", username, "error", err)
				return fmt.Errorf("yt-dlp run failed: %w", err)
			}
			// err and result.Stderr carry the whole yt-dlp output, progress lines with the session cookies included
			reason := ytdlpFailure(result.Stderr)
			slog.Error("yt-dlp run failed", "platform", platform, "user", username, "code", result.ExitCode, "reason", reason)
			return fmt.Errorf("yt-dlp exited with code %d: %s", result.ExitCode, reason)
		}
	}

	if t.SaveText {
		infos, perr := result.GetExtractedInfo()
		if perr != nil {
			slog.Warn("failed to parse yt-dlp JSON output", "platform", platform, "user", username, "error", perr)
		} else if posts := collectYTDLPPosts(infos, platform); len(posts) > 0 {
			for _, p := range posts {
				p.Username = username
				if err := opts.Posts.UpsertPost(ctx, p); err != nil {
					slog.Warn("Failed to save post to store", "platform", platform, "user", username, "external_id", p.ExternalID, "error", err)
				}
			}
			slog.Info("Saved video posts metadata", "platform", platform, "user", username, "count", len(posts))
		}
	}

	// canonical names before thumbnails, so previews are generated under the final name (TikTok only: the id carries
	// the date; YouTube ids are opaque and their names are stable)
	if platform == "tiktok" {
		if n := NormalizeTikTokNames(outputDir, opts.ThumbnailsDir, username); n > 0 {
			slog.Info("Normalized TikTok file names after sync", "user", username, "renamed", n)
		}
	}
	seedArchiveFromResult(platform, username, result)

	download.GenerateVideoThumbnails(outputDir)

	// Ensure video codecs are browser-compatible (H.264)
	EnsureDirectoryVideoCodecs(ctx, outputDir)

	slog.Info("yt-dlp video scraping completed successfully", "platform", platform, "user", username)
	opts.OnProgress(90)
	return nil
}

// youtubeShortsURL points yt-dlp at the channel's shorts tab only. Accepts @handles and /channel/UC… links; a bare
// handle becomes a handle URL.
func youtubeShortsURL(username string) string {
	u := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if i := strings.Index(u, "youtube.com/"); i >= 0 {
		u = u[i+len("youtube.com/"):]
		if rest, ok := strings.CutPrefix(u, "channel/"); ok {
			return "https://www.youtube.com/channel/" + strings.Trim(rest, "/") + "/shorts"
		}
	}
	return "https://www.youtube.com/@" + strings.Trim(u, "/") + "/shorts"
}

// seedArchiveFromResult records the ids yt-dlp listed, so the archive covers files whose names carry no recoverable id
// (YouTube) without a fresh probe of the library.
func seedArchiveFromResult(platform, username string, result *ytdlp.Result) {
	if result == nil {
		return
	}
	infos, err := result.GetExtractedInfo()
	if err != nil {
		slog.Warn("failed to parse yt-dlp JSON output", "platform", platform, "user", username, "error", err)
		return
	}
	var ids []string
	for _, info := range infos {
		entries := info.Entries
		if entries == nil {
			entries = []*ytdlp.ExtractedInfo{info}
		}
		for _, v := range entries {
			if v != nil && v.ID != "" {
				ids = append(ids, v.ID)
			}
		}
	}
	seedArchiveIDs(platform, username, ids)
}

// collectYTDLPPosts flattens yt-dlp playlist entries into store posts.
func collectYTDLPPosts(infos []*ytdlp.ExtractedInfo, platform string) []store.Post {
	var posts []store.Post
	for _, info := range infos {
		videos := info.Entries
		if videos == nil {
			videos = []*ytdlp.ExtractedInfo{info}
		}
		for _, v := range videos {
			if v == nil || v.ID == "" {
				continue
			}
			var postedAt time.Time
			if v.UploadDate != nil && len(*v.UploadDate) == 8 {
				if t, err := time.Parse("20060102", *v.UploadDate); err == nil {
					postedAt = t
				}
			}
			title := ""
			if v.Title != nil {
				title = *v.Title
			}
			description := ""
			if v.Description != nil {
				description = *v.Description
			}
			text := description
			if title != "" && description != "" && title != description {
				text = title + "\n\n" + description
			} else if title != "" {
				text = title
			}
			posts = append(posts, store.Post{
				Platform:   platform,
				ExternalID: v.ID,
				PostedAt:   postedAt,
				Text:       text,
				Media:      []store.PostMedia{{URL: v.ID, Kind: "video"}},
			})
		}
	}
	return posts
}

// writeCookieFile writes raw Netscape cookies (Cookie-Editor export format) to a temp file for yt-dlp.
func writeCookieFile(raw string) (string, error) {
	f, err := os.CreateTemp("", "ytdlp-cookies-*.txt")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(raw); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// ytdlpFailure picks the ERROR lines out of yt-dlp's stderr, or else its last plain line, capped in length. Progress
// lines are never returned: they embed the info dict, request headers and cookies included.
func ytdlpFailure(stderr string) string {
	var errs []string
	last := ""
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "progress:") {
			continue
		}
		last = line
		if strings.HasPrefix(line, "ERROR:") && !slices.Contains(errs, line) {
			errs = append(errs, line)
		}
	}
	reason := strings.Join(errs, "; ")
	if reason == "" {
		reason = last
	}
	if r := []rune(reason); len(r) > 500 {
		reason = string(r[:500]) + "…"
	}
	return reason
}
