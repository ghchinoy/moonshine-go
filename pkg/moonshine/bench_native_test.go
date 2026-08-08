//go:build native_bench

package moonshine

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// readPCM16MonoWAV parses a 16-bit PCM mono WAV file into []float32 samples.
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

// resolveLibDir returns the native lib directory from MOONSHINE_LIB_DIR
// or checks standard build locations (./.moonshine/lib).
func resolveLibDir(t *testing.T) string {
	t.Helper()
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if libDir != "" {
		return libDir
	}
	candidates := []string{
		"../../.moonshine/lib",
		"../.moonshine/lib",
		"./.moonshine/lib",
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Skip("MOONSHINE_LIB_DIR not set and ./.moonshine/lib not found")
	return ""
}

// resolveAudioAssets ensures bench test audio files exist or fetches them.
func resolveAudioAssets(t *testing.T) (twoCitiesPath, beckettPath string) {
	t.Helper()
	candidates := []string{
		"../../bench/testdata",
		"../bench/testdata",
		"./bench/testdata",
	}
	var base string
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				base = abs
				break
			}
		}
	}
	if base == "" {
		t.Skip("bench/testdata directory not found -- run ./scripts/fetch-bench-assets.sh first")
	}

	twoCitiesPath = filepath.Join(base, "two_cities_16k.wav")
	beckettPath = filepath.Join(base, "beckett.wav")

	if _, err := os.Stat(twoCitiesPath); err != nil {
		t.Skipf("missing test audio %s -- run ./scripts/fetch-bench-assets.sh", twoCitiesPath)
	}
	if _, err := os.Stat(beckettPath); err != nil {
		t.Skipf("missing test audio %s -- run ./scripts/fetch-bench-assets.sh", beckettPath)
	}

	return twoCitiesPath, beckettPath
}

// loadSharedTranscriber loads native libmoonshine and returns a single
// Transcriber instance shared across all test streams.
func loadSharedTranscriber(t *testing.T) *Transcriber {
	t.Helper()
	libDir := resolveLibDir(t)
	if err := Load(libDir); err != nil {
		t.Fatalf("Load(%s): %v", libDir, err)
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
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// transcriptText concatenates all line texts from a Transcript.
func transcriptText(tx Transcript) string {
	var b strings.Builder
	for _, l := range tx.Lines {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(l.Text)
	}
	return strings.ToLower(b.String())
}

// TestSharedTranscriberConcurrencyCorrectness verifies Phase 0:
// Multiple concurrent streams running against a single shared Transcriber
// handle must produce correct transcripts without cross-contamination.
func TestSharedTranscriberConcurrencyCorrectness(t *testing.T) {
	twoCitiesPath, beckettPath := resolveAudioAssets(t)
	tr := loadSharedTranscriber(t)

	twoCitiesPCM, sr1 := readPCM16MonoWAV(t, twoCitiesPath)
	beckettPCM, sr2 := readPCM16MonoWAV(t, beckettPath)

	t.Logf("Loaded Stream A (two_cities): %d samples (%d Hz, %.1fs)",
		len(twoCitiesPCM), sr1, float64(len(twoCitiesPCM))/float64(sr1))
	t.Logf("Loaded Stream B (beckett): %d samples (%d Hz, %.1fs)",
		len(beckettPCM), sr2, float64(len(beckettPCM))/float64(sr2))

	// Concurrency levels to test
	levels := []int{2, 4, 8, 16}

	for _, k := range levels {
		t.Run(fmt.Sprintf("K=%d", k), func(t *testing.T) {
			var wg sync.WaitGroup
			results := make([]string, k)
			errors := make([]error, k)

			startSignal := make(chan struct{})

			for i := 0; i < k; i++ {
				i := i
				wg.Add(1)
				go func() {
					defer wg.Done()

					// Interleave: even workers get Stream A, odd workers get Stream B
					var pcm []float32
					var sr int32
					if i%2 == 0 {
						pcm = twoCitiesPCM
						sr = sr1
					} else {
						pcm = beckettPCM
						sr = sr2
					}

					stream, err := tr.NewStream(0)
					if err != nil {
						errors[i] = fmt.Errorf("NewStream: %w", err)
						return
					}
					defer stream.Close() //nolint:errcheck

					if err := stream.Start(); err != nil {
						errors[i] = fmt.Errorf("Start: %w", err)
						return
					}

					<-startSignal // sync start across all goroutines

					// Feed PCM in 100ms chunks (1600 samples at 16kHz)
					chunkSize := int(sr / 10) // 100ms
					for pos := 0; pos < len(pcm); pos += chunkSize {
						end := pos + chunkSize
						if end > len(pcm) {
							end = len(pcm)
						}
						chunk := pcm[pos:end]
						if err := stream.AddAudio(chunk, sr); err != nil {
							errors[i] = fmt.Errorf("AddAudio: %w", err)
							return
						}
						_, _ = stream.Transcribe(0)
					}

					_ = stream.Stop()
					finalTx, err := stream.Transcribe(0)
					if err != nil {
						errors[i] = fmt.Errorf("final Transcribe: %w", err)
						return
					}
					results[i] = transcriptText(finalTx)
				}()
			}

			// Release all workers simultaneously
			close(startSignal)
			wg.Wait()

			// Evaluate correctness & cross-contamination
			var contaminationFailures int
			var emptyFailures int

			for i := 0; i < k; i++ {
				if errors[i] != nil {
					t.Errorf("Worker %d failed with error: %v", i, errors[i])
					continue
				}
				text := results[i]
				if len(text) == 0 {
					t.Errorf("Worker %d returned empty transcript", i)
					emptyFailures++
					continue
				}

				if i%2 == 0 {
					// Even worker: expected Stream A (two_cities)
					// Must NOT contain beckett-specific words
					if strings.Contains(text, "beckett") {
						t.Errorf("CROSS-CONTAMINATION in Worker %d (Stream A): found 'beckett' in transcript: %q", i, text)
						contaminationFailures++
					}
					// Should contain two_cities keywords
					if !strings.Contains(text, "times") && !strings.Contains(text, "best") && !strings.Contains(text, "worst") {
						t.Logf("Worker %d (Stream A) warning: expected 'times/best/worst', got %q", i, text)
					}
				} else {
					// Odd worker: expected Stream B (beckett)
					// Must NOT contain two_cities-specific words
					if strings.Contains(text, "worst of times") || strings.Contains(text, "epoch of belief") {
						t.Errorf("CROSS-CONTAMINATION in Worker %d (Stream B): found 'worst of times' in transcript: %q", i, text)
						contaminationFailures++
					}
					// Should contain beckett keywords
					if !strings.Contains(text, "reason") && !strings.Contains(text, "beckett") && !strings.Contains(text, "ever tried") && !strings.Contains(text, "fail better") {
						t.Logf("Worker %d (Stream B) warning: expected 'beckett/ever tried/fail better', got %q", i, text)
					}
				}
			}

			if contaminationFailures > 0 {
				t.Fatalf("Phase 0 Concurrency Gate FAILED for K=%d: %d cross-contamination failures detected",
					k, contaminationFailures)
			}
			t.Logf("Phase 0 Concurrency Gate PASSED for K=%d: 0 cross-contamination failures", k)
		})
	}
}

// TestSharedTranscriberContentionProbe measures single-stream vs multi-stream RTF
// to determine whether the C library executes concurrently or serializes under a lock.
func TestSharedTranscriberContentionProbe(t *testing.T) {
	twoCitiesPath, _ := resolveAudioAssets(t)
	tr := loadSharedTranscriber(t)
	pcm, sr := readPCM16MonoWAV(t, twoCitiesPath)

	audioSec := float64(len(pcm)) / float64(sr)

	levels := []int{1, 2, 4, 8}

	for _, k := range levels {
		t.Run(fmt.Sprintf("Streams=%d", k), func(t *testing.T) {
			var wg sync.WaitGroup
			startSignal := make(chan struct{})
			start := time.Now()

			for i := 0; i < k; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					stream, err := tr.NewStream(0)
					if err != nil {
						return
					}
					defer stream.Close() //nolint:errcheck
					_ = stream.Start()

					<-startSignal

					chunkSize := int(sr / 10)
					for pos := 0; pos < len(pcm); pos += chunkSize {
						end := pos + chunkSize
						if end > len(pcm) {
							end = len(pcm)
						}
						_ = stream.AddAudio(pcm[pos:end], sr)
						_, _ = stream.Transcribe(0)
					}
					_ = stream.Stop()
					_, _ = stream.Transcribe(0)
				}()
			}

			close(startSignal)
			wg.Wait()
			elapsed := time.Since(start)

			totalAudioSec := audioSec * float64(k)
			rtf := elapsed.Seconds() / totalAudioSec

			t.Logf("K=%d streams: %d total audio-seconds processed in %v (Aggregate RTF = %.3f, Wall-clock/stream = %.2fs)",
				k, int(totalAudioSec), elapsed.Round(time.Millisecond), rtf, elapsed.Seconds())
		})
	}
}

// BenchmarkInProcessTranscribe measures non-streaming Transcribe performance.
func BenchmarkInProcessTranscribe(b *testing.B) {
	twoCitiesPath, _ := resolveAudioAssetsBench(b)
	tr := loadSharedTranscriberBench(b)
	pcm, sr := readPCM16MonoWAVBench(b, twoCitiesPath)

	audioSec := float64(len(pcm)) / float64(sr)

	b.ResetTimer()
	b.ReportAllocs()

	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, err := tr.Transcribe(pcm, sr, 0)
		if err != nil {
			b.Fatalf("Transcribe failed: %v", err)
		}
	}
	elapsed := time.Since(start)

	totalAudioSec := audioSec * float64(b.N)
	if totalAudioSec > 0 {
		rtf := elapsed.Seconds() / totalAudioSec
		b.ReportMetric(rtf, "RTF")
		b.ReportMetric(totalAudioSec/elapsed.Seconds(), "xRealtime")
	}
}

// BenchmarkInProcessStreamingSession measures streaming AddAudio + Transcribe performance.
func BenchmarkInProcessStreamingSession(b *testing.B) {
	twoCitiesPath, _ := resolveAudioAssetsBench(b)
	tr := loadSharedTranscriberBench(b)
	pcm, sr := readPCM16MonoWAVBench(b, twoCitiesPath)

	audioSec := float64(len(pcm)) / float64(sr)
	chunkSize := int(sr / 10) // 100ms

	b.ResetTimer()
	b.ReportAllocs()

	start := time.Now()
	for i := 0; i < b.N; i++ {
		stream, err := tr.NewStream(0)
		if err != nil {
			b.Fatalf("NewStream failed: %v", err)
		}
		if err := stream.Start(); err != nil {
			stream.Close() //nolint:errcheck
			b.Fatalf("Start failed: %v", err)
		}

		for pos := 0; pos < len(pcm); pos += chunkSize {
			end := pos + chunkSize
			if end > len(pcm) {
				end = len(pcm)
			}
			_ = stream.AddAudio(pcm[pos:end], sr)
			_, _ = stream.Transcribe(0)
		}
		_ = stream.Stop()
		_, _ = stream.Transcribe(0)
		_ = stream.Close()
	}
	elapsed := time.Since(start)

	totalAudioSec := audioSec * float64(b.N)
	if totalAudioSec > 0 {
		rtf := elapsed.Seconds() / totalAudioSec
		b.ReportMetric(rtf, "RTF")
		b.ReportMetric(totalAudioSec/elapsed.Seconds(), "xRealtime")
	}
}

// Helper wrappers for *testing.B
func resolveAudioAssetsBench(b *testing.B) (string, string) {
	b.Helper()
	candidates := []string{
		"../../bench/testdata",
		"../bench/testdata",
		"./bench/testdata",
	}
	var base string
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				base = abs
				break
			}
		}
	}
	if base == "" {
		b.Skip("bench/testdata directory not found -- run ./scripts/fetch-bench-assets.sh first")
	}
	p1 := filepath.Join(base, "two_cities_16k.wav")
	p2 := filepath.Join(base, "beckett.wav")
	return p1, p2
}

func loadSharedTranscriberBench(b *testing.B) *Transcriber {
	b.Helper()
	libDir := resolveLibDirBench(b)
	if err := Load(libDir); err != nil {
		b.Fatalf("Load(%s): %v", libDir, err)
	}
	manifest, err := GetSTTDependencies("en", Option{Name: "model_arch", Value: "0"})
	if err != nil {
		b.Fatalf("GetSTTDependencies: %v", err)
	}
	cacheRoot := b.TempDir()
	if err := Download(context.Background(), manifest, cacheRoot, false); err != nil {
		b.Fatalf("Download: %v", err)
	}
	modelDir, err := PrimaryModelDir(cacheRoot, manifest)
	if err != nil {
		b.Fatalf("PrimaryModelDir: %v", err)
	}
	tr, err := LoadTranscriber(modelDir, ModelArchTiny)
	if err != nil {
		b.Fatalf("LoadTranscriber: %v", err)
	}
	b.Cleanup(func() { _ = tr.Close() })
	return tr
}

func readPCM16MonoWAVBench(b *testing.B, path string) (samples []float32, sampleRate int32) {
	b.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("could not read %s: %v", path, err)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		b.Fatalf("%s is not a RIFF/WAVE file", path)
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
		b.Fatalf("%s: no data chunk found", path)
	}
	n := dataLen / 2
	samples = make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[dataOff+i*2 : dataOff+i*2+2]))
		samples[i] = float32(v) / 32768.0
	}
	return samples, sampleRate
}

func resolveLibDirBench(b *testing.B) string {
	b.Helper()
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if libDir != "" {
		return libDir
	}
	candidates := []string{
		"../../.moonshine/lib",
		"../.moonshine/lib",
		"./.moonshine/lib",
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	b.Skip("MOONSHINE_LIB_DIR not set and ./.moonshine/lib not found")
	return ""
}
