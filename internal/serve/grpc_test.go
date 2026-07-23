package serve

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/serve/servepb"
)

const bufconnBufSize = 1024 * 1024

// startTestGRPCTransport spins up a GRPCTransport over an in-memory
// bufconn listener (no real network/TCP port), returning it plus a dial
// function tests can use to connect a client.
func startTestGRPCTransport(t *testing.T) (*Hub, *GRPCTransport, func(context.Context) (*grpc.ClientConn, error)) {
	t.Helper()
	hub := NewHub()
	lis := bufconn.Listen(bufconnBufSize)
	tr := NewGRPCTransportWithListener(hub, lis)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = tr.Close()
	})
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	dial := func(dialCtx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
	return hub, tr, dial
}

func TestGRPCTransport_ClientReceivesPublishedTranscriptEvent(t *testing.T) {
	hub, _, dial := startTestGRPCTransport(t)

	conn, err := dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := servepb.NewVoiceSidecarClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}

	// Give the server a moment to register the Hub subscription (the
	// server-side Stream handler runs asynchronously relative to the
	// client's Stream() call returning).
	time.Sleep(50 * time.Millisecond)

	hub.Publish(event.TranscriptEvent{
		Lines:            []moonshine.Line{{ID: 1, Text: "hello", IsComplete: true}},
		FinalizedLineIDs: []uint64{1},
		ElapsedMs:        123,
	})

	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(): %v", err)
	}
	te := ev.GetTranscript()
	if te == nil {
		t.Fatalf("got %+v, want a TranscriptEvent payload", ev)
	}
	if te.ElapsedMs != 123 {
		t.Errorf("ElapsedMs = %d, want 123", te.ElapsedMs)
	}
	if len(te.Lines) != 1 || te.Lines[0].Text != "hello" {
		t.Fatalf("Lines = %+v, want one line with text 'hello'", te.Lines)
	}
	if len(te.FinalizedLineIds) != 1 || te.FinalizedLineIds[0] != 1 {
		t.Errorf("FinalizedLineIds = %v, want [1]", te.FinalizedLineIds)
	}
}

func TestGRPCTransport_ClientSentActionReachesActionsChannel(t *testing.T) {
	_, tr, dial := startTestGRPCTransport(t)

	conn, err := dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := servepb.NewVoiceSidecarClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}

	req := &servepb.ActionRequest{Id: "abc", Verb: "speak", Args: []byte(`{"text":"hi"}`)}
	if err := stream.Send(req); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	select {
	case got := <-tr.Actions():
		if got.ID != "abc" || got.Verb != "speak" {
			t.Fatalf("got %+v, want ID=abc Verb=speak", got)
		}
		var args map[string]string
		if err := json.Unmarshal(got.Args, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		if args["text"] != "hi" {
			t.Errorf("args[text] = %q, want %q", args["text"], "hi")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for action on tr.Actions()")
	}
}

func TestGRPCTransport_Publish_DisplayCard(t *testing.T) {
	hub, _, dial := startTestGRPCTransport(t)

	conn, err := dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := servepb.NewVoiceSidecarClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	hub.Publish(event.DisplayCard{Title: "Weather", Body: "sunny"})

	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(): %v", err)
	}
	card := ev.GetDisplay()
	if card == nil || card.Title != "Weather" {
		t.Fatalf("got %+v, want DisplayCard{Title:Weather}", ev)
	}
}

func TestGRPCTransport_Close_IsIdempotentAndClosesActions(t *testing.T) {
	_, tr, _ := startTestGRPCTransport(t)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	select {
	case _, open := <-tr.Actions():
		if open {
			t.Error("Actions() channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Actions() to close")
	}
}

func TestToProtoEvent_UnsupportedType(t *testing.T) {
	_, ok := toProtoEvent(42)
	if ok {
		t.Fatal("expected ok=false for an unsupported event type")
	}
}

func TestFromProtoActionRequest_RoundTrip(t *testing.T) {
	req := &servepb.ActionRequest{Id: "x", Verb: "display", Args: []byte(`{"title":"t"}`)}
	got := fromProtoActionRequest(req)
	if got.ID != "x" || got.Verb != "display" {
		t.Fatalf("got %+v, want ID=x Verb=display", got)
	}
	var m map[string]string
	if err := json.Unmarshal(got.Args, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["title"] != "t" {
		t.Errorf("title = %q, want %q", m["title"], "t")
	}
}
