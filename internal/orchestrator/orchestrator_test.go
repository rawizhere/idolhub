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
