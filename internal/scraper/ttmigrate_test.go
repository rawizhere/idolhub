package scraper

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestMigrateTikTokNames(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join("downloads", "tiktok", "u")
	thumbs := filepath.Join(dir, "thumbnails")
	if err := os.MkdirAll(thumbs, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(dir, "7699367800736845063.mp4"),
		filepath.Join(dir, "2026-08-25_12-02-23_7677938415496662293.mp4"), // already dated: left alone
		filepath.Join(dir, "notes.txt"),
		filepath.Join(thumbs, "7699367800736845063.jpg"),
	} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if got := MigrateTikTokNames(""); !slices.Equal(got, []string{"u"}) {
		t.Errorf("changed = %v", got)
	}
	if want := []string{
		"2026-08-25_12-02-23_7677938415496662293.mp4", "2026-10-22_05-59-20_7699367800736845063.mp4", "notes.txt",
	}; !slices.Equal(dirNames(t, dir), want) {
		t.Errorf("files = %v, want %v", dirNames(t, dir), want)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "2026-10-22_05-59-20_7699367800736845063.jpg")); err != nil {
		t.Errorf("thumbnail not carried over: %v", err)
	}

	// a second start finds nothing to do
	if got := MigrateTikTokNames(""); len(got) != 0 {
		t.Errorf("rerun changed = %v", got)
	}
}
