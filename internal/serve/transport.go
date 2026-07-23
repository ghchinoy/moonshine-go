package serve

import (
	"context"
	"sync"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// Transport is a single wire protocol for the sidecar: it accepts
// connections from subscribers, forwards Publish'd events out to them, and
// surfaces whatever event.ActionRequest values those subscribers send back
// in on its Actions() channel. Concrete implementations (WebSocket, gRPC --
// see docs/serve-sidecar.md's file-ownership map for ws.go/grpc.go) each
// live in their own file and are otherwise interchangeable from the
// Manager's point of view.
//
// A Transport is expected to internally register each of its connections
// as a Hub subscriber (via a Publisher/Hub reference given to it at
// construction time, not part of this interface -- see ws.go/grpc.go for
// the concrete wiring) and forward whatever it receives from that
// subscription out over the wire. This interface only covers the
// lifecycle and the inbound-action surface the Manager needs to drive.
type Transport interface {
	// Start begins accepting connections/streams. It must not block
	// beyond whatever setup is needed to start listening; long-running
	// serve loops belong in goroutines spawned by Start.
	Start(ctx context.Context) error
	// Publish forwards ev to every subscriber currently connected to this
	// transport. Implementations should apply the same non-blocking,
	// never-lose-a-finalized-line semantics as Hub.Publish (indeed, the
	// straightforward way to implement this is to register one Hub
	// subscription per connection and pump its channel out over the
	// wire, in which case this method is just Hub.Publish under the
	// hood -- see ws.go/grpc.go).
	Publish(ev any) error
	// Actions returns the channel of inbound action requests received
	// from any connected subscriber, merged across all of that
	// transport's connections. Closed when the transport is Closed.
	Actions() <-chan event.ActionRequest
	// Close stops accepting new connections, closes existing ones, and
	// closes the Actions() channel. Safe to call more than once.
	Close() error
}

// Manager runs zero or more Transports concurrently (e.g. WebSocket and
// gRPC at once, per --transport ws,grpc), merges their Actions() channels
// into a single stream, and fans Publish out to all of them. This is what
// cmd/moonshine/serve.go (P6) wires the Hub and Dispatcher to, instead of
// depending on any single transport implementation directly.
type Manager struct {
	mu         sync.RWMutex
	transports []Transport

	actions chan event.ActionRequest
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewManager creates a Manager over the given transports (may be empty,
// though a serve process with zero transports has no way to reach
// subscribers). actionsBuffer sizes the merged output channel; pass 0 for
// a reasonable default (matching subscriberBufferSize).
func NewManager(transports ...Transport) *Manager {
	m := &Manager{
		transports: transports,
		actions:    make(chan event.ActionRequest, subscriberBufferSize),
		done:       make(chan struct{}),
	}
	return m
}

// Start starts every managed transport and begins merging their Actions()
// channels into Manager.Actions(). Returns the first error encountered
// starting any transport (subsequent transports are still started on a
// best-effort basis so one misconfigured transport doesn't prevent others
// from serving).
func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	transports := append([]Transport(nil), m.transports...)
	m.mu.RUnlock()

	var firstErr error
	for _, t := range transports {
		if err := t.Start(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, t := range transports {
		m.wg.Add(1)
		go m.pump(ctx, t)
	}
	return firstErr
}

// pump forwards t's Actions() into the Manager's merged channel until t's
// channel closes, ctx is cancelled, or the Manager is closed.
func (m *Manager) pump(ctx context.Context, t Transport) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.done:
			return
		case req, ok := <-t.Actions():
			if !ok {
				return
			}
			select {
			case m.actions <- req:
			case <-ctx.Done():
				return
			case <-m.done:
				return
			}
		}
	}
}

// Publish forwards ev to every managed transport. Errors from individual
// transports are not aggregated (a Publish failure on one transport, e.g.
// a transient write error to one connection, should not prevent delivery
// to the others) -- implementations are expected to log/handle their own
// per-connection failures internally.
func (m *Manager) Publish(ev any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.transports {
		_ = t.Publish(ev)
	}
}

// Actions returns the merged stream of inbound action requests from every
// managed transport.
func (m *Manager) Actions() <-chan event.ActionRequest { return m.actions }

// Close closes every managed transport and stops the merge goroutines.
// Safe to call more than once.
func (m *Manager) Close() error {
	select {
	case <-m.done:
		return nil // already closed
	default:
		close(m.done)
	}

	m.mu.RLock()
	transports := append([]Transport(nil), m.transports...)
	m.mu.RUnlock()

	var firstErr error
	for _, t := range transports {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.wg.Wait()
	close(m.actions)
	return firstErr
}

var _ Publisher = (*Manager)(nil)
