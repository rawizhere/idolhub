package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A truncated body must leave nothing at dstPath, or the exists check skips the file forever.
func TestFileTruncatedBodyLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "a.jpg")
	if _, err := File(context.Background(), srv.Client(), srv.URL, dst, FileOpts{}); err == nil {
		t.Fatal("want error for truncated body")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("dir has %d leftovers, want none", len(entries))
	}
}

func TestFileWritesCompleteBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "a.jpg")
	ok, err := File(context.Background(), srv.Client(), srv.URL, dst, FileOpts{})
	if err != nil || !ok {
		t.Fatalf("File = %v, %v", ok, err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "hello" {
		t.Fatalf("content = %q, %v", b, err)
	}
}
