package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEEvent represents a single Server-Sent Event pushed to connected dashboard clients.
type SSEEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker fans out real-time hub events to connected browser SSE clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

// NewBroker constructs a Broker.
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe registers a new client channel and returns a cleanup function.
func (b *Broker) Subscribe() (chan SSEEvent, func()) {
	ch := make(chan SSEEvent, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if _, ok := b.clients[ch]; ok {
			delete(b.clients, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := b.Subscribe()
	defer unsub()

	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
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
