package scraper

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// Previews must land where handleMedia serves them (<thumbs>/<platform>/<user>/thumbnails), and the ones earlier builds wrote under <thumbs>/downloads/ must be moved there, not regenerated.
func TestMigrateThumbnailsLayout(t *testing.T) {
	t.Chdir(t.TempDir())
	thumbs, _ := filepath.Abs("thumbs")

	mkfile := func(p string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create("src.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	jpg, _ := os.ReadFile("src.jpg")

	mkfile("downloads/twitter/idol/moved.jpg", jpg)
	mkfile("downloads/twitter/idol/fresh.jpg", jpg)
	mkfile(filepath.Join(thumbs, thumbVersionMarker), []byte("480p\n"))
	mkfile(filepath.Join(thumbs, "downloads/twitter/idol/thumbnails/moved.jpg"), []byte("MISPLACED"))

	migrateThumbnails(thumbs)

	got, err := os.ReadFile(filepath.Join(thumbs, "twitter/idol/thumbnails/moved.jpg"))
	if err != nil || string(got) != "MISPLACED" {
		t.Errorf("relocated preview = %q, %v; want the misplaced file moved into place", got, err)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "twitter/idol/thumbnails/fresh.jpg")); err != nil {
		t.Errorf("generated preview missing at served path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "downloads")); !os.IsNotExist(err) {
		t.Errorf("misplaced tree not pruned: %v", err)
	}
	if _, err := os.Stat("downloads/twitter/idol/moved.jpg"); err != nil {
		t.Errorf("source media touched: %v", err)
	}
}
