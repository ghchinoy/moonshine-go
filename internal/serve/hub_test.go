package serve

import (
	"context"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func TestHub_FanOutToMultipleSubscribers(t *testing.T) {
	h := NewHub()
	_, ch1 := h.Subscribe()
	_, ch2 := h.Subscribe()

	h.Publish(event.TranscriptEvent{ElapsedMs: 100})

	select {
	case got := <-ch1:
		if te, ok := got.(event.TranscriptEvent); !ok || te.ElapsedMs != 100 {
			t.Fatalf("ch1 got %#v, want TranscriptEvent{ElapsedMs:100}", got)
		}
	default:
		t.Fatal("ch1: expected an event, got none")
	}
	select {
	case got := <-ch2:
		if te, ok := got.(event.TranscriptEvent); !ok || te.ElapsedMs != 100 {
			t.Fatalf("ch2 got %#v, want TranscriptEvent{ElapsedMs:100}", got)
		}
	default:
		t.Fatal("ch2: expected an event, got none")
	}
}

func TestHub_Unsubscribe_ClosesChannelAndStopsDelivery(t *testing.T) {
	h := NewHub()
	id, ch := h.Subscribe()
	h.Unsubscribe(id)

	if _, open := <-ch; open {
		t.Fatal("channel should be closed after Unsubscribe")
	}

	// Publishing after unsubscribe must not panic (no subscribers left).
	h.Publish(event.TranscriptEvent{ElapsedMs: 1})

	// Unsubscribing twice must not panic.
	h.Unsubscribe(id)
}

func TestHub_InterimEvent_DroppedUnderBackpressure(t *testing.T) {
	h := NewHub()
	_, ch := h.Subscribe()

	// Fill the subscriber's buffer with interim (non-finalizing) events
	// without draining it.
	for i := 0; i < subscriberBufferSize+5; i++ {
		h.Publish(event.TranscriptEvent{ElapsedMs: int64(i)})
	}

	if got := len(ch); got != subscriberBufferSize {
		t.Fatalf("buffered events = %d, want %d (buffer full, excess dropped)", got, subscriberBufferSize)
	}
	// Hub uses a drop-oldest policy: once the buffer is full, the oldest
	// queued event is evicted to make room for each new one. So after
	// publishing subscriberBufferSize+5 events, the buffer holds the most
	// recent subscriberBufferSize of them (events 5..7+5), and the first
	// one drained should be event index 5 (the oldest surviving one).
	first := <-ch
	te, ok := first.(event.TranscriptEvent)
	if !ok {
		t.Fatalf("got %#v, want TranscriptEvent", first)
	}
	wantFirst := int64(5)
	if te.ElapsedMs != wantFirst {
		t.Errorf("first buffered event ElapsedMs = %d, want %d", te.ElapsedMs, wantFirst)
	}
}

func TestHub_FinalizedEvent_NeverDroppedUnderBackpressure(t *testing.T) {
	h := NewHub()
	_, ch := h.Subscribe()

	// Fill the buffer with interim events (subscriber not draining).
	for i := 0; i < subscriberBufferSize; i++ {
		h.Publish(event.TranscriptEvent{ElapsedMs: int64(i)})
	}
	if got := len(ch); got != subscriberBufferSize {
		t.Fatalf("buffer len = %d, want %d (precondition: buffer full)", got, subscriberBufferSize)
	}

	// Now publish an event carrying a newly-finalized line. It must be
	// delivered even though the buffer was full.
	finalEv := event.TranscriptEvent{
		Lines:            []serveapi.Line{{ID: 42, Text: "done", IsComplete: true}},
		FinalizedLineIDs: []uint64{42},
	}
	h.Publish(finalEv)

	// Drain the whole buffer and confirm the finalized event is present
	// (it must have evicted exactly one stale interim event to fit).
	found := false
	drained := 0
	for {
		select {
		case got := <-ch:
			drained++
			if te, ok := got.(event.TranscriptEvent); ok && len(te.FinalizedLineIDs) == 1 && te.FinalizedLineIDs[0] == 42 {
				found = true
			}
		default:
			goto done
		}
	}
done:
	if !found {
		t.Fatalf("finalized event (line 42) was dropped under backpressure; drained %d events", drained)
	}
	if drained != subscriberBufferSize {
		t.Errorf("drained %d events, want %d (buffer capacity preserved, one interim evicted)", drained, subscriberBufferSize)
	}
}

func TestHub_Ingest_ConvertsAndPublishesUpdates(t *testing.T) {
	h := NewHub()
	_, ch := h.Subscribe()

	updates := make(chan session.Update, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.Ingest(ctx, updates)
		close(done)
	}()

	updates <- session.Update{
		Transcript: moonshine.Transcript{Lines: []moonshine.Line{{ID: 1, Text: "hi", IsComplete: true}}},
		FinalizedLines: []session.LineTiming{
			{ID: 1},
		},
	}

	select {
	case got := <-ch:
		te, ok := got.(event.TranscriptEvent)
		if !ok {
			t.Fatalf("got %#v, want TranscriptEvent", got)
		}
		if len(te.Lines) != 1 || te.Lines[0].Text != "hi" {
			t.Fatalf("Lines = %+v, want one line with text 'hi'", te.Lines)
		}
		if len(te.FinalizedLineIDs) != 1 || te.FinalizedLineIDs[0] != 1 {
			t.Fatalf("FinalizedLineIDs = %v, want [1]", te.FinalizedLineIDs)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingested event")
	}

	close(updates)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Ingest did not return after updates channel closed")
	}
}

func TestHub_IngestWithAudio_PreservesAudioData(t *testing.T) {
	h := NewHub()
	_, ch := h.Subscribe()

	updates := make(chan session.Update, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.IngestWithAudio(ctx, updates, true)
		close(done)
	}()

	updates <- session.Update{
		Transcript: moonshine.Transcript{Lines: []moonshine.Line{
			{ID: 1, Text: "hi", AudioData: []float32{0.5, 0.6}, IsComplete: true},
		}},
	}

	select {
	case got := <-ch:
		te, ok := got.(event.TranscriptEvent)
		if !ok {
			t.Fatalf("got %#v, want TranscriptEvent", got)
		}
		if len(te.Lines) != 1 || len(te.Lines[0].AudioData) != 2 {
			t.Fatalf("Lines = %+v, want audio data preserved when includeAudio=true", te.Lines)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingested event")
	}

	close(updates)
	<-done
}

func TestHub_Ingest_StopsOnContextCancel(t *testing.T) {
	h := NewHub()
	updates := make(chan session.Update) // never closed
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Ingest(ctx, updates)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Ingest did not return after context cancellation")
	}
}

// Publisher interface satisfaction check (compile-time).
var _ Publisher = (*Hub)(nil)
