// Package serve implements the moonshine serve sidecar daemon: a Hub that
// fans out live transcript events to subscribers (over WebSocket/gRPC
// transports), a Dispatcher that routes inbound actions (speak, display,
// session control) from those subscribers or from an in-process agent, and
// the agent/tool-calling layer itself. See docs/serve-sidecar.md for the
// full interaction pattern and file-ownership map.
package serve

import (
	"context"
	"sync"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
)

// subscriberBufferSize is the per-subscriber outbound event buffer depth.
// Chosen to match session.Live's own internal update buffer capacity so
// the Hub doesn't become the bottleneck before the session itself would
// be.
const subscriberBufferSize = 8

// Publisher is the narrow interface Dispatcher (and anything else that only
// needs to push an event out, e.g. a display action) depends on, so it can
// be faked in tests without spinning up a real Hub.
type Publisher interface {
	// Publish fans ev out to all current subscribers. See Hub.Publish for
	// the exact backpressure contract.
	Publish(ev any)
}

// Hub fans out transcript/display events to any number of subscribers.
//
// Backpressure contract (see docs/serve-sidecar.md section 7): a slow
// subscriber must never block the publisher, and must never miss a
// finalized transcript line even if it misses interim ("still typing")
// frames. Hub implements this with a drop-oldest policy: Publish is always
// non-blocking, and if a subscriber's buffer is full, the oldest queued
// event for that subscriber is evicted to make room for the new one before
// it is sent. Because every event is either delivered or evicted (never
// silently refused), this trivially satisfies "never miss a finalized
// line": a finalized event might cause an older, now-stale interim event
// to be evicted, but the finalized event itself is always delivered.
// TranscriptEvent snapshots are cumulative (each carries the full current
// line list, see event.FromUpdate), so evicting a stale interim snapshot
// in favor of a fresher one never loses information a consumer needs.
//
// Hub is safe for concurrent use: Ingest normally runs in its own
// goroutine reading from a session.Live's Updates() channel, while
// Subscribe/Unsubscribe/Publish are called from transport goroutines.
type Hub struct {
	mu          sync.Mutex
	subscribers map[int]chan any
	nextID      int
}

// NewHub creates an empty Hub ready to accept subscribers and an Ingest
// call.
func NewHub() *Hub {
	return &Hub{subscribers: make(map[int]chan any)}
}

// Subscribe registers a new subscriber and returns its ID (for
// Unsubscribe) and a channel of events to forward to that subscriber
// (transcript events, display cards, action results). The channel is
// closed by Unsubscribe; callers must not close it themselves.
func (h *Hub) Subscribe() (id int, events <-chan any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id = h.nextID
	h.nextID++
	ch := make(chan any, subscriberBufferSize)
	h.subscribers[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber and closes its event channel. Safe to
// call more than once for the same id (second call is a no-op).
func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subscribers[id]; ok {
		close(ch)
		delete(h.subscribers, id)
	}
}

// Publish fans ev out to every current subscriber per the drop-oldest
// backpressure contract documented on Hub. Never blocks.
func (h *Hub) Publish(ev any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, ch := range h.subscribers {
		select {
		case ch <- ev:
			continue
		default:
		}
		// Buffer full: evict the oldest queued event to make room. Safe
		// without blocking because h.mu serializes all publishers, so no
		// other goroutine is racing us to send on ch; only subscriber
		// reads race here, and a concurrent read just means the
		// non-blocking send below succeeds immediately instead.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- ev:
		default:
			// A concurrent subscriber read drained faster than expected
			// and then the buffer refilled from elsewhere -- vanishingly
			// unlikely with a single publisher, but fall back to
			// dropping rather than blocking if it ever happens.
		}
	}
}

// Ingest reads session.Update values from updates (typically
// session.Live.Updates()), converts each to an event.TranscriptEvent via
// event.FromUpdate (omitting raw audio by default), and Publishes it. It
// returns when updates is closed or ctx is cancelled. Run it in its own
// goroutine:
//
//	go hub.Ingest(ctx, sess.Updates())
func (h *Hub) Ingest(ctx context.Context, updates <-chan session.Update) {
	h.IngestWithAudio(ctx, updates, false)
}

// IngestWithAudio reads session.Update values from updates, converts each to
// an event.TranscriptEvent (optionally preserving raw PCM AudioData on lines),
// and Publishes it.
func (h *Hub) IngestWithAudio(ctx context.Context, updates <-chan session.Update, includeAudio bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-updates:
			if !ok {
				return
			}
			h.Publish(event.FromUpdateWithAudio(u, includeAudio))
		}
	}
}
