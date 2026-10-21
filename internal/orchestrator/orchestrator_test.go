package orchestrator

import (
	"path/filepath"
	"testing"
	"time"

	"idolhub/internal/gallery"
	"idolhub/internal/store"
)

// newTestOrchestrator builds an orchestrator on a throwaway store without starting its workers.
func newTestOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	t.Chdir(t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(gallery.NewIndex(st.Posts), st)
}

// StartScrape must not deadlock when the circuit breaker check runs (it once double-locked o.mu).
func TestStartScrapeBreakerNoDeadlock(t *testing.T) {
	o := newTestOrchestrator(t)
	o.blockUntil["instagram"] = time.Now().Add(time.Hour)

	done := make(chan struct{})
	go func() {
		o.StartScrape("user1", "instagram", false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartScrape deadlocked on the breaker check")
	}
}

// The same handle on two platforms must be two targets: a shared entry made the second StartScrape a no-op and mixed their watermarks.
func TestStartScrapeKeysByPlatform(t *testing.T) {
	o := newTestOrchestrator(t)
	o.StartScrape("idol", "instagram", false)
	o.StartScrape("idol", "twitter", false)

	if len(o.jobCh) != 2 {
		t.Fatalf("queued jobs = %d, want 2", len(o.jobCh))
	}
	if len(o.progress) != 2 {
		t.Fatalf("progress entries = %d, want 2", len(o.progress))
	}

	if !o.CancelScrape("twitter", "idol") {
		t.Fatal("cancel twitter/idol reported nothing to cancel")
	}
	if got := o.progress[taskKey("instagram", "idol")].Status; got != "queued" {
		t.Errorf("instagram status = %q, want queued after cancelling twitter", got)
	}
	if got := o.progress[taskKey("twitter", "idol")].Status; got != "idle" {
		t.Errorf("twitter status = %q, want idle", got)
	}
}

// A pending full resync turns the next scrape into a full one, even when the caller asked for an incremental run.
func TestRequestFullResyncForcesFullScrape(t *testing.T) {
	o := newTestOrchestrator(t)
	o.RequestFullResync("twitter", "Idol")
	o.StartScrape("idol", "twitter", false)

	job := <-o.jobCh
	if !job.forceFullSync || !job.lastSync.IsZero() {
		t.Errorf("job = %+v, want forced full sync from zero watermark", job)
	}
}
