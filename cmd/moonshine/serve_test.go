package main

import (
	"testing"
)

func TestServeCmd_Flags(t *testing.T) {
	cmd := serveCmd

	tests := []struct {
		name     string
		flag     string
		defValue string
	}{
		{"addr", "addr", ":8765"},
		{"ws-path", "ws-path", "/ws"},
		{"grpc-addr", "grpc-addr", ":9090"},
		{"transport", "transport", "ws"},
		{"agent", "agent", "external"},
		{"gemini-model", "gemini-model", "gemini-2.5-flash"},
		{"allow-actions", "allow-actions", "false"},
		{"audio-source", "audio-source", "local"},
		{"remote-audio-rate", "remote-audio-rate", "16000"},
		{"remote-audio-encoding", "remote-audio-encoding", "float32"},
		{"remote-audio-channels", "remote-audio-channels", "1"},
		{"arch", "arch", "tiny-streaming"},
		{"language", "language", "en"},
		{"tts-play-local", "tts-play-local", "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("flag --%s not found on serveCmd", tt.flag)
			}
			if f.DefValue != tt.defValue {
				t.Errorf("expected default for --%s to be %q, got %q", tt.flag, tt.defValue, f.DefValue)
			}
		})
	}
}
