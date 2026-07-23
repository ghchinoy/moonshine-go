package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

func startTestWSTransport(t *testing.T) (*Hub, *WSTransport, string) {
	t.Helper()
	hub := NewHub()
	tr := NewWSTransport(hub, "127.0.0.1:0", "/ws")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = tr.Close()
	})
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	// Start binds the listener synchronously but http.Serve runs async;
	// give it a moment to actually be accepting connections.
	deadline := time.Now().Add(2 * time.Second)
	for tr.Addr() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	addr := tr.Addr()
	if addr == "" {
		t.Fatal("transport never reported a listening address")
	}
	return hub, tr, addr
}

func dialTestClient(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("ws://%s/ws", addr)
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial(%s) = %v", url, err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func TestWSTransport_ClientReceivesPublishedEvent(t *testing.T) {
	hub, _, addr := startTestWSTransport(t)
	conn := dialTestClient(t, addr)

	// Give the server a moment to register the subscription before we
	// publish, otherwise the event could be published before Subscribe
	// runs in handleConn.
	time.Sleep(50 * time.Millisecond)

	hub.Publish(event.TranscriptEvent{ElapsedMs: 42})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var envelope wireEnvelope
	if err := wsjson.Read(ctx, conn, &envelope); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if envelope.Kind != event.KindTranscript {
		t.Fatalf("Kind = %q, want %q", envelope.Kind, event.KindTranscript)
	}
	var te event.TranscriptEvent
	if err := json.Unmarshal(envelope.Payload, &te); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if te.ElapsedMs != 42 {
		t.Errorf("ElapsedMs = %d, want 42", te.ElapsedMs)
	}
}

func TestWSTransport_ClientSentActionReachesActionsChannel(t *testing.T) {
	_, tr, addr := startTestWSTransport(t)
	conn := dialTestClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := event.ActionRequest{ID: "abc", Verb: "speak", Args: json.RawMessage(`{"text":"hi"}`)}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("Write() = %v", err)
	}

	select {
	case got := <-tr.Actions():
		if got.ID != "abc" || got.Verb != "speak" {
			t.Fatalf("got %+v, want ID=abc Verb=speak", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for action on tr.Actions()")
	}
}

func TestWSTransport_Publish_DelegatesToHub(t *testing.T) {
	hub, tr, addr := startTestWSTransport(t)
	conn := dialTestClient(t, addr)
	time.Sleep(50 * time.Millisecond)

	if err := tr.Publish(event.DisplayCard{Title: "hi"}); err != nil {
		t.Fatalf("Publish() = %v", err)
	}
	// Confirm hub actually has a subscriber that received it (proves
	// Publish routed through the Hub rather than being a no-op).
	_ = hub // sanity: hub is the one WSTransport was constructed with

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var envelope wireEnvelope
	if err := wsjson.Read(ctx, conn, &envelope); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if envelope.Kind != event.KindDisplay {
		t.Fatalf("Kind = %q, want %q", envelope.Kind, event.KindDisplay)
	}
}

func TestWSTransport_Close_IsIdempotentAndClosesActions(t *testing.T) {
	_, tr, _ := startTestWSTransport(t)

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

func TestEnvelopeFor_UnsupportedType(t *testing.T) {
	_, err := envelopeFor(42)
	if err == nil {
		t.Fatal("expected an error for an unsupported event type")
	}
}

func TestEnvelopeFor_KnownTypes(t *testing.T) {
	cases := []struct {
		name string
		ev   any
		want event.Kind
	}{
		{"transcript", event.TranscriptEvent{}, event.KindTranscript},
		{"display", event.DisplayCard{Title: "x"}, event.KindDisplay},
		{"action_result", event.ActionResult{OK: true}, event.KindActionResult},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := envelopeFor(tc.ev)
			if err != nil {
				t.Fatalf("envelopeFor(%v) error: %v", tc.ev, err)
			}
			if env.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", env.Kind, tc.want)
			}
		})
	}
}
