package gcsfetch

import "testing"

func TestIsGCSURI(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		{"gs://bucket/object.wav", true},
		{"gs://bucket/folder/", true},
		{"http://example.com/audio.wav", false},
		{"local/file.wav", false},
	}
	for _, tt := range tests {
		if got := IsGCSURI(tt.uri); got != tt.want {
			t.Errorf("IsGCSURI(%q) = %v, want %v", tt.uri, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	b, o, err := parse("gs://my-bucket/audio/test.wav")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if b != "my-bucket" || o != "audio/test.wav" {
		t.Errorf("got (%q, %q), want (my-bucket, audio/test.wav)", b, o)
	}
}

func TestParsePrefix(t *testing.T) {
	b, p, err := parsePrefix("gs://my-bucket/audio/")
	if err != nil {
		t.Fatalf("parsePrefix failed: %v", err)
	}
	if b != "my-bucket" || p != "audio/" {
		t.Errorf("got (%q, %q), want (my-bucket, audio/)", b, p)
	}
}
