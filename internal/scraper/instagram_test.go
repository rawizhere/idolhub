package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"idolhub/internal/download"
	"idolhub/internal/store"
)

// igFeedJSON is a timeline node as the graphql feed returns it, with the video under a given CDN token.
func igFeedJSON(t *testing.T, cdn, token string) igFeedItem {
	t.Helper()
	raw := `{"pk":"3925744544839203694","taken_at":1782205201,"media_type":2,
	  "image_versions2":{"candidates":[{"url":"` + cdn + `/v/t51/111_222_n.jpg","width":1080,"height":1920}]},
	  "video_versions":[{"url":"` + cdn + `/o1/v/t16/f2/m86/` + token + `.mp4?efg=x&oh=y","width":720,"height":1280}]}`
	var item igFeedItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestInstagramResyncOfRotatedVideoURLDownloadsNothing(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("video bytes"))
	}))
	defer srv.Close()
	dir := t.TempDir()

	sync := func(token string) download.Result {
		entries := igPostEntries(igFeedJSON(t, srv.URL, token))
		if len(entries) != 1 || !entries[0].SoleVideo || entries[0].MediaID != "3925744544839203694" {
			t.Fatalf("entries = %+v", entries)
		}
		return downloadInstagramDirectMedia(context.Background(), entries[0], dir, "u", newIgFileIndex(dir), srv.Client())
	}

	if got := sync("AQOldToken"); got != download.Downloaded {
		t.Fatalf("first sync = %v, want Downloaded", got)
	}
	if got := sync("AQRotatedToken"); got != download.Skipped {
		t.Fatalf("resync = %v, want Skipped", got)
	}
	if hits.Load() != 1 {
		t.Errorf("CDN hit %d times, want 1", hits.Load())
	}
	names := dirNames(t, dir)
	if want := []string{"2026-06-23_09-00-01_3925744544839203694.mp4"}; !slices.Equal(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
}

func TestInstagramResyncAdoptsLegacyTokenVideo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected CDN request %s", r.URL)
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, name := range []string{"2026-06-23_09-00-01_AQaaa.mp4", "2026-06-23_09-00-01_AQbbb.mp4", "2026-06-23_09-00-01_111_222_n.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	entries := igPostEntries(igFeedJSON(t, srv.URL, "AQccc"))
	if got := downloadInstagramDirectMedia(context.Background(), entries[0], dir, "u", newIgFileIndex(dir), srv.Client()); got != download.Skipped {
		t.Fatalf("resync = %v, want Skipped", got)
	}
	want := []string{"2026-06-23_09-00-01_111_222_n.jpg", "2026-06-23_09-00-01_3925744544839203694.mp4", "2026-06-23_09-00-01_AQaaa.mp4"}
	if names := dirNames(t, dir); !slices.Equal(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
}

func TestIgPostEntriesCarouselUsesSlideIDs(t *testing.T) {
	raw := `{"pk":"9","taken_at":1,"media_type":8,"carousel_media":[
	  {"pk":"91","media_type":2,"video_versions":[{"url":"https://cdn/a/AQx.mp4","width":1,"height":1}]},
	  {"pk":"92","media_type":2,"video_versions":[{"url":"https://cdn/a/AQy.mp4","width":1,"height":1}]},
	  {"pk":"93","media_type":1,"image_versions2":{"candidates":[{"url":"https://cdn/a/5_6_n.jpg","width":1,"height":1}]}}]}`
	var item igFeedItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range igPostEntries(item) {
		name, _, _ := igFilename(e)
		got = append(got, name)
		if e.SoleVideo {
			t.Errorf("%s marked sole video", name)
		}
	}
	want := []string{"1970-01-01_00-00-01_91.mp4", "1970-01-01_00-00-01_92.mp4", "1970-01-01_00-00-01_5_6_n.jpg"}
	if !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestMigrateInstagramVideoNames(t *testing.T) {
	t.Chdir(t.TempDir())
	st, err := store.Open("test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	at := time.Date(2026, 6, 23, 9, 0, 1, 0, time.UTC)
	for _, p := range []store.Post{
		{ExternalID: "3925744544839203694", PostedAt: at, Media: []store.PostMedia{{URL: "https://cdn/o1/v/t2/f2/m367/AQnew", Kind: "video"}}},
		// rotated url that was never downloaded: the post's older copies are all there is
		{ExternalID: "1", PostedAt: at.Add(time.Hour), Media: []store.PostMedia{{URL: "https://cdn/o1/v/AQneverFetched", Kind: "video"}}},
		{ExternalID: "2", PostedAt: at.Add(2 * time.Hour), Media: []store.PostMedia{
			{URL: "https://cdn/o1/v/AQgone1", Kind: "video"}, {URL: "https://cdn/o1/v/AQgone2", Kind: "video"}}},
	} {
		p.Platform, p.Username = "instagram", "u"
		if err := st.Posts.UpsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join("downloads", "instagram", "u")
	thumbs := filepath.Join(dir, "thumbnails")
	if err := os.MkdirAll(thumbs, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(dir, "2026-06-23_09-00-01_AQnew.mp4"),
		filepath.Join(dir, "2026-06-23_09-00-01_AQold.mp4"),
		filepath.Join(dir, "2026-05-01_10-00-00_AQlonely.mp4"), // no stored post: left alone
		filepath.Join(dir, "2026-06-23_10-00-01_AQa.mp4"),
		filepath.Join(dir, "2026-06-23_10-00-01_AQb.mp4"),
		filepath.Join(dir, "2026-06-23_11-00-01_AQs1.mp4"),
		filepath.Join(dir, "2026-06-23_11-00-01_AQs2.mp4"),
		filepath.Join(thumbs, "2026-06-23_09-00-01_AQnew.jpg"),
	} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// the older copy of post 1 goes aside even though its name sorts last
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "2026-06-23_10-00-01_AQb.mp4"), old, old); err != nil {
		t.Fatal(err)
	}
	if got := MigrateInstagramVideoNames(ctx, st.Posts, ""); !slices.Equal(got, []string{"u"}) {
		t.Errorf("changed = %v", got)
	}
	if want := []string{
		"2026-05-01_10-00-00_AQlonely.mp4", "2026-06-23_09-00-01_3925744544839203694.mp4",
		"2026-06-23_10-00-01_1.mp4", "2026-06-23_11-00-01_AQs1.mp4", "2026-06-23_11-00-01_AQs2.mp4",
	}; !slices.Equal(dirNames(t, dir), want) {
		t.Errorf("files = %v, want %v", dirNames(t, dir), want)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "2026-06-23_09-00-01_3925744544839203694.jpg")); err != nil {
		t.Errorf("thumbnail not carried over: %v", err)
	}
	for _, name := range []string{"2026-06-23_09-00-01_AQold.mp4", "2026-06-23_10-00-01_AQb.mp4"} {
		if _, err := os.Stat(filepath.Join("downloads", ".duplicates", "instagram", "u", name)); err != nil {
			t.Errorf("duplicate not moved aside: %v", err)
		}
	}
	posts, err := st.Posts.ListByAccount(ctx, "instagram", "u", 10)
	if err != nil {
		t.Fatal(err)
	}
	media := map[string][]string{}
	for _, p := range posts {
		for _, m := range p.Media {
			media[p.ExternalID] = append(media[p.ExternalID], m.URL)
		}
	}
	want := map[string][]string{
		"3925744544839203694": {"3925744544839203694.mp4"},
		"1":                   {"1.mp4"},
		"2":                   {"AQs1.mp4", "AQs2.mp4"},
	}
	for id, urls := range want {
		if !slices.Equal(media[id], urls) {
			t.Errorf("post %s media = %v, want %v", id, media[id], urls)
		}
	}

	// a second start finds nothing to do
	if got := MigrateInstagramVideoNames(ctx, st.Posts, ""); len(got) != 0 {
		t.Errorf("rerun changed = %v", got)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
