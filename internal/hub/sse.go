package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"vpspulse/internal/store"
)

const maxSSEClients = 64

// SSEEvent represents a single Server-Sent Event pushed to connected dashboard clients.
type SSEEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker fans out real-time hub events to connected browser SSE clients.
type Broker struct {
	mu      sync.RWMutex
	closed  bool
	clients map[chan SSEEvent]struct{}
	store   *store.Store
}

// NewBroker constructs a Broker.
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// SetStore attaches a store so long-lived SSE streams can re-verify session validity on keepalive ticks.
func (b *Broker) SetStore(st *store.Store) {
	b.mu.Lock()
	b.store = st
	b.mu.Unlock()
}

// Subscribe registers a new client channel and returns a cleanup function.
func (b *Broker) Subscribe() (chan SSEEvent, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.clients) >= maxSSEClients {
		return nil, func() {}, false
	}
	ch := make(chan SSEEvent, 32)
	b.clients[ch] = struct{}{}

	return ch, func() {
		b.mu.Lock()
		if _, ok := b.clients[ch]; ok {
			delete(b.clients, ch)
			close(ch)
		}
		b.mu.Unlock()
	}, true
}

// Close closes all active SSE client channels so HTTP graceful shutdown exits immediately.
func (b *Broker) Close() {
	b.mu.Lock()
	b.closed = true
	for ch := range b.clients {
		delete(b.clients, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers without blocking slow clients.
func (b *Broker) Publish(eventType string, data any) {
	ev := SSEEvent{Type: eventType, Data: data}
	b.mu.RLock()
	for ch := range b.clients {
		select {
		case ch <- ev:
		default:
		}
	}
	b.mu.RUnlock()
}

// ServeHTTP streams events to an authenticated client.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, unsub, ok := b.Subscribe()
	if !ok {
		http.Error(w, "too many active streams", http.StatusServiceUnavailable)
		return
	}
	defer unsub()

	var sessionToken string
	if c, err := r.Cookie("vpspulse_session"); err == nil {
		sessionToken = c.Value
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			b.mu.RLock()
			st := b.store
			b.mu.RUnlock()
			if st != nil && sessionToken != "" {
				if _, err := st.GetSession(sessionToken); err != nil {
					return
				}
			}
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}
