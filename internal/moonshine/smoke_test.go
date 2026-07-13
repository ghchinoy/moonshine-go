//go:build moonshinesmoke

// Smoke tests that exercise a real, built libmoonshine.dylib/.so plus
// downloaded model assets. Not run by default (needs native artifacts and
// network access); run explicitly with:
//
//	MOONSHINE_LIB_DIR=$(pwd)/.moonshine/lib go test -tags moonshinesmoke ./internal/moonshine/... -run Smoke -v
package moonshine

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
)

// readPCM16MonoWAV is a minimal, dependency-free WAV reader for smoke-test
// purposes only (assumes 16-bit PCM mono, which is what
// test-assets/two_cities_16k.wav in the moonshine checkout already is). The
// real internal/audio package (see bd task 3.2) handles the general case.
func readPCM16MonoWAV(t *testing.T, path string) (samples []float32, sampleRate int32) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("could not read %s: %v", path, err)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("%s is not a RIFF/WAVE file", path)
	}
	pos := 12
	var dataOff, dataLen int
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			sampleRate = int32(binary.LittleEndian.Uint32(data[body+4 : body+8]))
		case "data":
			dataOff, dataLen = body, size
		}
		pos = body + size + size%2
	}
	if dataOff == 0 {
		t.Fatalf("%s: no data chunk found", path)
	}
	n := dataLen / 2
	samples = make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[dataOff+i*2 : dataOff+i*2+2]))
		samples[i] = float32(v) / 32768.0
	}
	return samples, sampleRate
}

func TestSmokeSTTRoundTrip(t *testing.T) {
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if libDir == "" {
		t.Skip("set MOONSHINE_LIB_DIR to run this smoke test")
	}
	if err := Load(libDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Logf("loaded %s", LibPath())

	ver, err := Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	t.Logf("moonshine_get_version = %d", ver)

	manifest, err := GetSTTDependencies("en", Option{Name: "model_arch", Value: "0"})
	if err != nil {
		t.Fatalf("GetSTTDependencies: %v", err)
	}
	if len(manifest.Groups) == 0 {
		t.Fatal("expected at least one dependency group")
	}
	t.Logf("manifest: %+v", manifest)

	cacheRoot := t.TempDir()
	if err := Download(context.Background(), manifest, cacheRoot, false); err != nil {
		t.Fatalf("Download: %v", err)
	}
	modelDir, err := PrimaryModelDir(cacheRoot, manifest)
	if err != nil {
		t.Fatalf("PrimaryModelDir: %v", err)
	}

	tr, err := LoadTranscriber(modelDir, ModelArchTiny)
	if err != nil {
		t.Fatalf("LoadTranscriber: %v", err)
	}
	defer tr.Close()

	// A second or so of silence should transcribe to an empty/near-empty
	// result without error -- this exercises the full struct-marshaling
	// path (transcript_t / transcript_line_t) even without real speech
	// audio on hand in this environment.
	silence := make([]float32, SampleRate*1)
	transcript, err := tr.Transcribe(silence, SampleRate, 0)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	t.Logf("transcript on silence: %+v", transcript)
}

func TestSmokeSTTRealSpeech(t *testing.T) {
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	wavPath := os.Getenv("MOONSHINE_SMOKE_WAV")
	if libDir == "" || wavPath == "" {
		t.Skip("set MOONSHINE_LIB_DIR and MOONSHINE_SMOKE_WAV to run this smoke test")
	}
	if err := Load(libDir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	manifest, err := GetSTTDependencies("en", Option{Name: "model_arch", Value: "0"})
	if err != nil {
		t.Fatalf("GetSTTDependencies: %v", err)
	}
	cacheRoot := t.TempDir()
	if err := Download(context.Background(), manifest, cacheRoot, false); err != nil {
		t.Fatalf("Download: %v", err)
	}
	modelDir, err := PrimaryModelDir(cacheRoot, manifest)
	if err != nil {
		t.Fatalf("PrimaryModelDir: %v", err)
	}
	tr, err := LoadTranscriber(modelDir, ModelArchTiny)
	if err != nil {
		t.Fatalf("LoadTranscriber: %v", err)
	}
	defer tr.Close()

	samples, sampleRate := readPCM16MonoWAV(t, wavPath)
	t.Logf("loaded %d samples at %d Hz from %s", len(samples), sampleRate, wavPath)

	t.Run("non-streaming", func(t *testing.T) {
		transcript, err := tr.Transcribe(samples, sampleRate, 0)
		if err != nil {
			t.Fatalf("Transcribe: %v", err)
		}
		if len(transcript.Lines) == 0 {
			t.Fatal("expected at least one transcribed line")
		}
		for _, l := range transcript.Lines {
			t.Logf("line: complete=%v start=%.2f dur=%.2f text=%q", l.IsComplete, l.StartTime, l.Duration, l.Text)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		stream, err := tr.NewStream(0)
		if err != nil {
			t.Fatalf("NewStream: %v", err)
		}
		defer stream.Close()
		if err := stream.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		chunk := int(sampleRate) / 5 // 200ms chunks
		var firstNonEmpty string
		for off := 0; off < len(samples); off += chunk {
			end := off + chunk
			if end > len(samples) {
				end = len(samples)
			}
			if err := stream.AddAudio(samples[off:end], sampleRate); err != nil {
				t.Fatalf("AddAudio: %v", err)
			}
			transcript, err := stream.Transcribe(FlagForceUpdate)
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if firstNonEmpty == "" {
				if text, ok := transcript.FirstNonEmptyText(); ok {
					firstNonEmpty = text
					t.Logf("first non-empty interim text at offset %d: %q", off, text)
				}
			}
		}
		if err := stream.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		final, err := stream.Transcribe(FlagForceUpdate)
		if err != nil {
			t.Fatalf("final Transcribe: %v", err)
		}
		for _, l := range final.Lines {
			t.Logf("final line: complete=%v text=%q", l.IsComplete, l.Text)
		}
		if firstNonEmpty == "" {
			t.Error("streaming never produced a non-empty interim line")
		}
	})
}

func TestSmokeTTS(t *testing.T) {
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	g2pRoot := os.Getenv("MOONSHINE_SMOKE_TTS_ROOT") // e.g. .../moonshine/core/moonshine-tts/data
	if libDir == "" || g2pRoot == "" {
		t.Skip("set MOONSHINE_LIB_DIR and MOONSHINE_SMOKE_TTS_ROOT to run this smoke test")
	}
	if err := Load(libDir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	synth, err := NewSynthesizer("en_us",
		Option{Name: "g2p_root", Value: g2pRoot},
		Option{Name: "voice", Value: "piper_en_US-amy-low"},
	)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}
	defer synth.Close()

	audio, err := synth.Synthesize("Hello from moonshine dash go, built with pure Go bindings.")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(audio.Samples) == 0 {
		t.Fatal("expected non-empty audio")
	}
	t.Logf("synthesized %d samples at %d Hz (%.2fs)", len(audio.Samples), audio.SampleRate, audio.Duration().Seconds())

	voices, err := ListVoices("en_us", Option{Name: "g2p_root", Value: g2pRoot})
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}
	t.Logf("voices: %+v", voices)
}
