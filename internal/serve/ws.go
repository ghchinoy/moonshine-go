package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// wireEnvelope is the single JSON shape sent over the WebSocket connection
// for outbound events, so a client can tell what it received without
// relying on JSON structural sniffing. Kind mirrors event.Kind; Payload is
// the corresponding typed value (event.TranscriptEvent, event.DisplayCard,
// or event.ActionResult).
type wireEnvelope struct {
	Kind    event.Kind      `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// envelopeFor builds the wire envelope for ev, or an error if ev is not one
// of the known outbound event types.
func envelopeFor(ev any) (wireEnvelope, error) {
	var kind event.Kind
	switch ev.(type) {
	case event.TranscriptEvent:
		kind = event.KindTranscript
	case event.DisplayCard:
		kind = event.KindDisplay
	case event.ActionResult:
		kind = event.KindActionResult
	case event.TTSAudioEvent:
		kind = event.KindTTSAudio
	default:
		return wireEnvelope{}, fmt.Errorf("serve/ws: unsupported event type %T", ev)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return wireEnvelope{}, fmt.Errorf("serve/ws: marshaling %s payload: %w", kind, err)
	}
	return wireEnvelope{Kind: kind, Payload: payload}, nil
}

// WSTransport is a WebSocket Transport implementation: each connection to
// its single endpoint is registered as a Hub subscriber, receives every
// published event as a JSON wireEnvelope text frame, and may send
// event.ActionRequest JSON frames back in, which are merged onto Actions().
//
// Browser clients (e.g. a display UI) can connect directly with the
// standard WebSocket API; any other language can use an off-the-shelf
// WebSocket client library and speak plain JSON, no protobuf/codegen
// required (contrast with the gRPC transport, P4b).
type WSTransport struct {
	hub  *Hub
	addr string // e.g. ":8765" or "127.0.0.1:8765"
	path string // e.g. "/ws", defaults to "/ws" if empty

	actions chan event.ActionRequest

	mu        sync.Mutex
	srv       *http.Server
	listener  net.Listener
	closed    bool
	conns     map[*websocket.Conn]struct{}
	audioSink *RemoteAudioSource
	sessMgr   *SessionManager
	audioFmt  AudioFormat
}

// SetSessionManager binds a SessionManager to WSTransport for per-connection
// sessions in remote audio mode.
func (t *WSTransport) SetSessionManager(mgr *SessionManager, fmtOpt ...AudioFormat) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessMgr = mgr
	if len(fmtOpt) > 0 {
		t.audioFmt = fmtOpt[0]
	}
}

// SetAudioSink binds a RemoteAudioSource to WSTransport for receiving
// client binary PCM audio streams over WebSocket.
func (t *WSTransport) SetAudioSink(sink *RemoteAudioSource) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.audioSink = sink
}

// NewWSTransport creates a WebSocket transport bound to addr (host:port,
// e.g. ":8765") serving the given path (default "/ws" if empty), fanning
// out events published to hub. Start must be called to actually begin
// listening.
func NewWSTransport(hub *Hub, addr, path string) *WSTransport {
	if path == "" {
		path = "/ws"
	}
	return &WSTransport{
		hub:     hub,
		addr:    addr,
		path:    path,
		actions: make(chan event.ActionRequest, subscriberBufferSize),
		conns:   make(map[*websocket.Conn]struct{}),
	}
}

// Start begins listening on t.addr and accepting WebSocket connections in a
// background goroutine. Returns once the listener is bound (so callers can
// rely on the address being ready, e.g. for tests using addr ":0"), but
// serving itself continues asynchronously until Close.
func (t *WSTransport) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(t.path, t.handleConn)

	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return fmt.Errorf("serve/ws: listening on %s: %w", t.addr, err)
	}

	t.mu.Lock()
	t.listener = ln
	t.srv = &http.Server{Handler: mux}
	t.mu.Unlock()

	go func() {
		_ = t.srv.Serve(ln)
	}()
	return nil
}

// Addr returns the actual listening address (useful when Start was called
// with a ":0" port, e.g. in tests). Returns "" if Start has not been
// called yet.
func (t *WSTransport) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener == nil {
		return ""
	}
	return t.listener.Addr().String()
}

func (t *WSTransport) handleConn(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow() //nolint:errcheck

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = conn.Close(websocket.StatusGoingAway, "server closing")
		return
	}
	t.conns[conn] = struct{}{}
	sessMgr := t.sessMgr
	audioFmt := t.audioFmt
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.conns, conn)
		t.mu.Unlock()
	}()

	var connHub *Hub
	var connAudioSink *RemoteAudioSource
	var connManagedSess *ManagedSession

	ctx := r.Context()

	if sessMgr != nil {
		connAudioSink = NewRemoteAudioSource(audioFmt, 100)
		defer connAudioSink.Close()

		var err error
		connManagedSess, err = sessMgr.CreateSession(ctx, connAudioSink)
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		defer connManagedSess.Close()

		connHub = connManagedSess.Hub()
	} else {
		connHub = t.hub
	}

	subID, events := connHub.Subscribe()
	defer connHub.Unsubscribe(subID)

	done := make(chan struct{})

	// Writer goroutine: pump Hub events out to the client.
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				envelope, err := envelopeFor(ev)
				if err != nil {
					continue // unsupported event type for this transport; skip
				}
				wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = wsjson.Write(wctx, conn, envelope)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	// Reader loop: pull inbound ActionRequest frames (text) or PCM audio (binary) from the client.
readLoop:
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) || ctx.Err() != nil {
				break readLoop // normal close or context cancellation
			}
			break readLoop // malformed frame or transport error: drop the connection
		}

		switch msgType {
		case websocket.MessageText:
			var req event.ActionRequest
			if err := json.Unmarshal(data, &req); err != nil {
				continue
			}
			if connManagedSess != nil {
				res := connManagedSess.Dispatcher().Handle(ctx, req)
				connHub.Publish(res)
			}
			select {
			case t.actions <- req:
			case <-ctx.Done():
				break readLoop
			default:
			}

		case websocket.MessageBinary:
			if connAudioSink != nil {
				if err := connAudioSink.WritePCMBytes(ctx, data); err != nil {
					continue
				}
			} else {
				t.mu.Lock()
				sink := t.audioSink
				t.mu.Unlock()

				if sink != nil {
					if err := sink.WritePCMBytes(ctx, data); err != nil {
						// Drop invalid PCM frames without terminating connection
						continue
					}
				}
			}
		}
	}

	connHub.Unsubscribe(subID)
	<-done
}

// Publish forwards ev to every currently-subscribed Hub connection.
// WSTransport doesn't fan out directly -- each connection already
// subscribes to t.hub in handleConn -- so this simply delegates to
// t.hub.Publish, which is where the drop-oldest backpressure/finalized-line
// guarantee actually lives (see Hub's doc comment).
func (t *WSTransport) Publish(ev any) error {
	t.hub.Publish(ev)
	return nil
}

// Actions returns the channel of inbound event.ActionRequest values
// received from any connected WebSocket client.
func (t *WSTransport) Actions() <-chan event.ActionRequest { return t.actions }

// Close stops accepting new connections, closes all open ones, and closes
// the Actions() channel. Safe to call more than once.
func (t *WSTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := make([]*websocket.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	srv := t.srv
	t.mu.Unlock()

	for _, c := range conns {
		_ = c.Close(websocket.StatusGoingAway, "server closing")
	}
	var err error
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = srv.Shutdown(ctx)
	}
	close(t.actions)
	return err
}

var _ Transport = (*WSTransport)(nil)
