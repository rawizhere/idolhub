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
		filepath.Join(dir, "2026-08-25_12-02-23_7677938415496662293.mp4"), // already canonical: left alone
		// duplicate: the canonical name is taken, the drifted copy stays
		filepath.Join(dir, "2026-08-25_12-02-27_7677938415496662293.mp4"),
		// drifted prefix without a canonical twin: TikTok's reported timestamp is 4s late, the id decides
		filepath.Join(dir, "2026-08-25_12-02-27_7677938415496662294.mp4"),
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
		"2026-08-25_12-02-23_7677938415496662293.mp4",
		"2026-08-25_12-02-23_7677938415496662294.mp4",
		"2026-08-25_12-02-27_7677938415496662293.mp4",
		"2026-10-22_05-59-20_7699367800736845063.mp4",
		"notes.txt",
	}; !slices.Equal(dirNames(t, dir), want) {
		t.Errorf("files = %v, want %v", dirNames(t, dir), want)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "2026-10-22_05-59-20_7699367800736845063.jpg")); err != nil {
		t.Errorf("thumbnail not carried over: %v", err)
	}

	// the migration itself seeds the archive
	wantArchive := "tiktok 7677938415496662293\ntiktok 7677938415496662294\ntiktok 7699367800736845063\n"
	archive, err := os.ReadFile(filepath.Join("downloads", "tiktok", ".u.archive.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(archive) != wantArchive {
		t.Errorf("archive = %q, want %q", archive, wantArchive)
	}

	// a second start finds nothing to do and keeps the archive idempotent
	if got := MigrateTikTokNames(""); len(got) != 0 {
		t.Errorf("rerun changed = %v", got)
	}
	SeedTikTokArchive("u")
	if archive2, _ := os.ReadFile(filepath.Join("downloads", "tiktok", ".u.archive.txt")); string(archive2) != wantArchive {
		t.Errorf("archive after reseed = %q, want %q", archive2, wantArchive)
	}
}
