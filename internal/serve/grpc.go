package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/serve/servepb"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// GRPCTransport is a gRPC Transport implementation: each call to the
// VoiceSidecar.Stream bidirectional RPC (see serve.proto) registers as a
// Hub subscriber, converts published events to servepb.Event messages, and
// decodes inbound servepb.ActionRequest messages back into
// event.ActionRequest, merged onto Actions().
//
// Unlike WSTransport (plain JSON), GRPCTransport requires client-side
// codegen from serve.proto -- appropriate for typed, multi-service
// consumers; see docs/serve-sidecar.md for when to prefer one transport
// over the other.
type GRPCTransport struct {
	servepb.UnimplementedVoiceSidecarServer

	hub    *Hub
	addr   string // e.g. ":9090"; ignored if Listener is set (see NewGRPCTransportWithListener, used by tests)
	lis    net.Listener
	server *grpc.Server

	actions chan event.ActionRequest

	mu     sync.Mutex
	closed bool
}

// NewGRPCTransport creates a gRPC transport that will listen on addr
// (host:port, e.g. ":9090") when Start is called.
func NewGRPCTransport(hub *Hub, addr string) *GRPCTransport {
	return &GRPCTransport{
		hub:     hub,
		addr:    addr,
		actions: make(chan event.ActionRequest, subscriberBufferSize),
	}
}

// NewGRPCTransportWithListener creates a gRPC transport that serves on a
// caller-provided listener instead of binding addr itself -- used by tests
// with an in-memory bufconn.Listener, and available to any caller that
// wants control over the listener (e.g. a pre-bound socket, TLS
// wrapping).
func NewGRPCTransportWithListener(hub *Hub, lis net.Listener) *GRPCTransport {
	return &GRPCTransport{
		hub:     hub,
		lis:     lis,
		actions: make(chan event.ActionRequest, subscriberBufferSize),
	}
}

// Start begins listening (binding t.addr if no listener was already
// provided) and serving the VoiceSidecar gRPC service in a background
// goroutine.
func (t *GRPCTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.lis == nil {
		lis, err := net.Listen("tcp", t.addr)
		if err != nil {
			t.mu.Unlock()
			return fmt.Errorf("serve/grpc: listening on %s: %w", t.addr, err)
		}
		t.lis = lis
	}
	t.server = grpc.NewServer()
	servepb.RegisterVoiceSidecarServer(t.server, t)
	srv := t.server
	lis := t.lis
	t.mu.Unlock()

	go func() {
		_ = srv.Serve(lis)
	}()
	return nil
}

// Addr returns the actual listening address. Returns "" if Start has not
// been called yet (or a non-TCP listener, e.g. bufconn, was supplied).
func (t *GRPCTransport) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lis == nil {
		return ""
	}
	return t.lis.Addr().String()
}

// Stream implements servepb.VoiceSidecarServer: the single bidirectional
// RPC. Each call registers a new Hub subscription for its lifetime.
func (t *GRPCTransport) Stream(stream servepb.VoiceSidecar_StreamServer) error {
	ctx := stream.Context()
	subID, events := t.hub.Subscribe()
	defer t.hub.Unsubscribe(subID)

	done := make(chan struct{})
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
				pbEvent, ok := toProtoEvent(ev)
				if !ok {
					continue // unsupported event type for this transport; skip
				}
				if err := stream.Send(pbEvent); err != nil {
					return
				}
			}
		}
	}()

	for {
		req, err := stream.Recv()
		if err != nil {
			break // EOF (client closed send side) or transport error
		}
		select {
		case t.actions <- fromProtoActionRequest(req):
		case <-ctx.Done():
		}
	}

	<-done
	return nil
}

// Publish forwards ev to every currently-subscribed Hub connection. Like
// WSTransport, GRPCTransport doesn't fan out directly -- each RPC call
// already subscribes to t.hub in Stream -- so this delegates to
// t.hub.Publish, where the backpressure/finalized-line guarantee lives.
func (t *GRPCTransport) Publish(ev any) error {
	t.hub.Publish(ev)
	return nil
}

// Actions returns the channel of inbound event.ActionRequest values
// received from any connected gRPC stream.
func (t *GRPCTransport) Actions() <-chan event.ActionRequest { return t.actions }

// Close stops the gRPC server (gracefully, then forcefully after any
// in-flight calls are given a chance to finish) and closes the Actions()
// channel. Safe to call more than once.
func (t *GRPCTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	srv := t.server
	t.mu.Unlock()

	if srv != nil {
		srv.GracefulStop()
	}
	close(t.actions)
	return nil
}

// toProtoEvent converts a Hub-published event (event.TranscriptEvent,
// event.DisplayCard, or event.ActionResult) into the corresponding
// servepb.Event oneof. ok is false for any other/unsupported type.
func toProtoEvent(ev any) (*servepb.Event, bool) {
	switch v := ev.(type) {
	case event.TranscriptEvent:
		return &servepb.Event{Payload: &servepb.Event_Transcript{Transcript: toProtoTranscriptEvent(v)}}, true
	case event.DisplayCard:
		return &servepb.Event{Payload: &servepb.Event_Display{Display: toProtoDisplayCard(v)}}, true
	case event.ActionResult:
		return &servepb.Event{Payload: &servepb.Event_ActionResult{ActionResult: toProtoActionResult(v)}}, true
	default:
		return nil, false
	}
}

func toProtoTranscriptEvent(te event.TranscriptEvent) *servepb.TranscriptEvent {
	lines := make([]*servepb.Line, len(te.Lines))
	for i, l := range te.Lines {
		lines[i] = toProtoLine(l)
	}
	out := &servepb.TranscriptEvent{
		Lines:            lines,
		FinalizedLineIds: te.FinalizedLineIDs,
		TtftMs:           te.TTFTms,
		ElapsedMs:        te.ElapsedMs,
		PollLatencyMs:    te.PollLatencyMs,
		Done:             te.Done,
		Err:              te.Err,
	}
	if te.Summary != nil {
		out.Summary = &servepb.SessionSummary{
			LinesFinalized:    int32(te.Summary.LinesFinalized),
			AvgTimeToFinalMs:  te.Summary.AvgTimeToFinalMs,
			MaxTimeToFinalMs:  te.Summary.MaxTimeToFinalMs,
			AvgRevisions:      te.Summary.AvgRevisions,
			MaxRevisions:      int32(te.Summary.MaxRevisions),
			AvgStabilityRatio: te.Summary.AvgStabilityRatio,
		}
	}
	return out
}

func toProtoLine(l serveapi.Line) *servepb.Line {
	return &servepb.Line{
		Text:           l.Text,
		StartTime:      l.StartTime,
		Duration:       l.Duration,
		Id:             l.ID,
		IsComplete:     l.IsComplete,
		IsUpdated:      l.IsUpdated,
		IsNew:          l.IsNew,
		HasTextChanged: l.HasTextChanged,
		SpeakerLabel:   l.SpeakerLabel(),
	}
}

func toProtoDisplayCard(c event.DisplayCard) *servepb.DisplayCard {
	return &servepb.DisplayCard{
		Title: c.Title,
		Body:  c.Body,
		Kind:  c.Kind,
		Data:  []byte(c.Data),
	}
}

func toProtoActionResult(r event.ActionResult) *servepb.ActionResult {
	return &servepb.ActionResult{Id: r.ID, Ok: r.OK, Err: r.Err}
}

// fromProtoActionRequest converts an inbound servepb.ActionRequest into the
// internal event.ActionRequest type Dispatcher consumes.
func fromProtoActionRequest(req *servepb.ActionRequest) event.ActionRequest {
	return event.ActionRequest{
		ID:   req.GetId(),
		Verb: req.GetVerb(),
		Args: json.RawMessage(req.GetArgs()),
	}
}

var _ Transport = (*GRPCTransport)(nil)
