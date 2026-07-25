package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

func TestStatsLine_DisplaysSTTLatencyAndConfidence(t *testing.T) {
	u := session.Update{
		TTFT:        150 * time.Millisecond,
		Elapsed:     2000 * time.Millisecond,
		PollLatency: 80 * time.Millisecond,
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{
					Text:          "hello world",
					LastLatencyMs: 45,
					Confidence:    0.92,
				},
			},
		},
	}

	got := statsLine(u)
	if !strings.Contains(got, "stt_lat=45ms") {
		t.Errorf("statsLine missing stt_lat=45ms, got %q", got)
	}
	if !strings.Contains(got, "conf=92%") {
		t.Errorf("statsLine missing conf=92%%, got %q", got)
	}
}
