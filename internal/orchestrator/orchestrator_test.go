package orchestrator

import (
	"context"
	"testing"
	"time"
)

// StartScrape must not deadlock when the circuit breaker check runs (it once double-locked o.mu).
func TestStartScrapeBreakerNoDeadlock(t *testing.T) {
	o := &Orchestrator{
		progress:     make(map[string]*TaskProgress),
		cancels:      make(map[string]context.CancelFunc),
		igBlockUntil: time.Now().Add(time.Hour),
	}

	done := make(chan struct{})
	go func() {
		o.StartScrape("user1", "instagram", false, false)
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
	o := &Orchestrator{
		progress: make(map[string]*TaskProgress),
		cancels:  make(map[string]context.CancelFunc),
		jobCh:    make(chan scrapeJob, 4),
	}
	o.StartScrape("idol", "instagram", false, false)
	o.StartScrape("idol", "twitter", false, false)

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
