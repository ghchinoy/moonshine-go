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
		{"endpoint-post-final-delay", "endpoint-post-final-delay", "0s"},
		{"endpoint-min-utterance-chars", "endpoint-min-utterance-chars", "0"},
		{"endpoint-max-utterance-duration", "endpoint-max-utterance-duration", "0s"},
		{"keyterms", "keyterms", ""},
		{"keyterm-boost", "keyterm-boost", "2"},
		{"context", "context", ""},
		{"context-file", "context-file", ""},
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

func TestDomainCustomizationOptions(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		opts, err := domainCustomizationOptions(nil, "", 2.0, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(opts) != 0 {
			t.Errorf("expected 0 options, got %v", opts)
		}
	})

	t.Run("keyterms and context text", func(t *testing.T) {
		opts, err := domainCustomizationOptions(nil, "Kubernetes,Ceph", 2.0, "Platform migration context", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(opts) != 2 {
			t.Fatalf("expected 2 options, got %d: %v", len(opts), opts)
		}
		if opts[0].Name != "keyterms" || opts[0].Value != "Kubernetes,Ceph" {
			t.Errorf("unexpected opt[0]: %v", opts[0])
		}
		if opts[1].Name != "context" || opts[1].Value != "Platform migration context" {
			t.Errorf("unexpected opt[1]: %v", opts[1])
		}
	})
}
