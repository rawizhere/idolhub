package orchestrator

import (
	"fmt"
	"net/http"
	"sync"
)

// sseHub fans dashboard events out to every open EventSource stream.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	done    chan struct{}
	closed  bool
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan []byte]struct{}), done: make(chan struct{})}
}

func (h *sseHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// the client treats "hello" as the moment the stream is live and reloads the full state
	_, _ = fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	ch := make(chan []byte, 256)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case data := <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// publish never blocks: a client too slow to drain its buffer misses events and catches up on its next full reload.
func (h *sseHub) publish(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

func (h *sseHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}
