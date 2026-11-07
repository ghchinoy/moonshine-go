package main

import (
	"encoding/json"
	"testing"
)

func TestTranscriptEventUnmarshalFloatTimestamps(t *testing.T) {
	raw := `{
		"lines": [
			{
				"id": 10270326165232604498,
				"text": "Ever tried.",
				"is_complete": true,
				"start_time": 0.095999956,
				"duration": 1.376,
				"last_latency_ms": 21
			}
		],
		"finalized_line_ids": [10270326165232604498],
		"ttft_ms": 480,
		"poll_latency_ms": 21,
		"elapsed_ms": 1500
	}`

	var ev transcriptEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("json.Unmarshal failed on fractional start_time/duration: %v", err)
	}
	if len(ev.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(ev.Lines))
	}
	if ev.Lines[0].Text != "Ever tried." || !ev.Lines[0].IsComplete {
		t.Errorf("unexpected line: %+v", ev.Lines[0])
	}
}
