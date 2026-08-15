// Command stream-audio demonstrates bidirectional audio streaming and
// live transcript receiving over a single WebSocket connection to
// `moonshine serve --audio-source remote`.
//
// Unlike text-only subscribers (such as `samples/go-listen`), this program
// acts as both the audio source (streaming raw 16kHz 16-bit mono PCM chunks
// over binary WebSocket frames) and the transcript subscriber (decoding
// `TranscriptEvent` JSON envelopes concurrently over the same connection).
//
// It demonstrates key operational practices for remote audio streaming:
//  1. Chunk Sizing & Pacing: Streaming 100ms (3,200 bytes) PCM chunks at
//     1x real-time cadence so audio arrives smoothly aligned with the
//     server's internal polling loop.
//  2. VAD Endpointing via Trailing Silence: Appending ~1.5s of zero-PCM silence
//     after the audio payload so the streaming model's Voice Activity Detector
//     (VAD) detects the utterance boundary and marks the final line as complete.
//  3. Uint64 Line ID Tracking: Correctly decoding 64-bit unsigned line IDs
//     without signed integer overflow.
//  4. Interim Fallback: Retaining the latest interim text for each line ID so
//     unfinalized utterances are never lost if a stream closes before endpointing.
//
// Usage:
//
//	# 1. Start moonshine serve with a remote audio source:
//	moonshine serve --transport ws --addr :8765 \
//	  --audio-source remote \
//	  --remote-audio-encoding int16 \
//	  --remote-audio-rate 16000 \
//	  --remote-audio-channels 1
//
//	# 2. Run this sample to stream a WAV file:
//	go run . -addr ws://localhost:8765/ws -input path/to/recording.wav
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var debug bool

func ts() string {
	return time.Now().Format("15:04:05.000")
}

type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type transcriptEvent struct {
	Lines            []line   `json:"lines"`
	FinalizedLineIDs []uint64 `json:"finalized_line_ids"`
	TTFTms           int64    `json:"ttft_ms"`
	PollLatencyMs    int64    `json:"poll_latency_ms"`
	ElapsedMs        int64    `json:"elapsed_ms"`
}

type line struct {
	ID            uint64 `json:"id"`
	Text          string `json:"text"`
	IsComplete    bool   `json:"is_complete"`
	StartTime     int64  `json:"start_time"`
	Duration      int64  `json:"duration"`
	LastLatencyMs int64  `json:"last_latency_ms"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
	inputPath := flag.String("input", "", "Path to 16kHz mono 16-bit WAV audio file to stream (optional)")
	chunkMs := flag.Int("chunk-ms", 100, "Duration of each streamed PCM chunk in milliseconds")
	silenceMs := flag.Int("silence-ms", 1500, "Duration of trailing zero-PCM silence in milliseconds for VAD endpointing")
	pace := flag.Float64("pace", 1.0, "Streaming pace multiplier (1.0 = real-time, 1.25 = 25% faster)")
	flag.BoolVar(&debug, "debug", false, "Print verbose chunk-by-chunk send and poll diagnostic traces")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 1. Load or synthesize audio PCM bytes (16kHz 16-bit signed LE mono)
	pcmBytes, err := loadOrSynthesizeAudio(*inputPath)
	if err != nil {
		log.Fatalf("loading audio: %v", err)
	}

	durationSec := float64(len(pcmBytes)) / (16000 * 2) // 2 bytes per sample @ 16kHz
	fmt.Printf("[%s] Loaded %d bytes of PCM audio (%.2fs duration)\n", ts(), len(pcmBytes), durationSec)

	// 2. Connect to moonshine serve WebSocket endpoint
	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("connecting to %s: %v\n  • Is `moonshine serve --audio-source remote` running?", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck

	conn.SetReadLimit(10 << 20) // 10 MiB

	fmt.Printf("[%s] Connected to %s -- starting bidirectional audio stream\n\n", ts(), *addr)

	var (
		mu           sync.Mutex
		order        []uint64
		latestByID   = make(map[uint64]string)
		completeByID = make(map[uint64]bool)
		ttftLogged   bool
	)

	streamDone := make(chan struct{})
	readDone := make(chan struct{})

	// 3. Reader goroutine: decodes inbound TranscriptEvents concurrently over the same connection
	go func() {
		defer close(readDone)
		for {
			var env envelope
			if err := wsjson.Read(ctx, conn, &env); err != nil {
				if ctx.Err() != nil || errors.Is(err, context.Canceled) {
					return
				}
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					return
				}
				if debug {
					log.Printf("read frame: %v", err)
				}
				return
			}

			if env.Kind != "transcript" {
				continue
			}

			var ev transcriptEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				if debug {
					log.Printf("decoding transcript payload: %v", err)
				}
				continue
			}

			mu.Lock()
			if !ttftLogged && ev.TTFTms > 0 {
				ttftLogged = true
				fmt.Printf("[%s] [stats] Time-to-first-token (TTFT): %dms\n", ts(), ev.TTFTms)
			}

			for _, l := range ev.Lines {
				if _, seen := latestByID[l.ID]; !seen {
					order = append(order, l.ID)
				}
				latestByID[l.ID] = l.Text
				if l.IsComplete {
					if !completeByID[l.ID] {
						completeByID[l.ID] = true
						fmt.Printf("[%s] [FINAL line %d] %s  (stt: %dms)\n", ts(), l.ID, l.Text, l.LastLatencyMs)
					}
				} else if debug {
					fmt.Printf("[%s] [interim line %d] %s...\n", ts(), l.ID, l.Text)
				}
			}
			mu.Unlock()
		}
	}()

	// 4. Writer goroutine: streams PCM chunks at real-time cadence
	go func() {
		defer close(streamDone)

		// Calculate chunk byte size: 16000 samples/sec * 2 bytes/sample * (chunkMs / 1000)
		bytesPerMs := (16000 * 2) / 1000 // 32 bytes per ms
		chunkSize := (*chunkMs) * bytesPerMs
		if chunkSize <= 0 {
			chunkSize = 3200 // default 100ms
		}

		interval := time.Duration(float64(*chunkMs)/(*pace)) * time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		totalChunks := (len(pcmBytes) + chunkSize - 1) / chunkSize
		chunkIdx := 0

		// Stream audio payload
		for offset := 0; offset < len(pcmBytes); offset += chunkSize {
			end := offset + chunkSize
			if end > len(pcmBytes) {
				end = len(pcmBytes)
			}
			chunk := pcmBytes[offset:end]

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			chunkIdx++
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				log.Printf("writing audio chunk %d/%d: %v", chunkIdx, totalChunks, err)
				return
			}

			if debug && (chunkIdx%10 == 0 || chunkIdx == totalChunks) {
				fmt.Printf("[%s] [stream] Sent audio chunk %d/%d (%.1f%%)\n",
					ts(), chunkIdx, totalChunks, float64(chunkIdx)/float64(totalChunks)*100.0)
			}
		}

		fmt.Printf("[%s] [stream] Finished audio payload. Streaming trailing silence (%dms) for VAD endpointing...\n", ts(), *silenceMs)

		// Stream trailing zero-PCM silence to trigger VAD endpointing
		silenceBytes := (*silenceMs) * bytesPerMs
		silenceChunk := make([]byte, chunkSize)
		for offset := 0; offset < silenceBytes; offset += chunkSize {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			if err := conn.Write(ctx, websocket.MessageBinary, silenceChunk); err != nil {
				log.Printf("writing silence chunk: %v", err)
				return
			}
		}

		fmt.Printf("[%s] [stream] Audio streaming complete. Awaiting final transcript settlement...\n", ts())
	}()

	// 5. Wait for audio stream to complete
	select {
	case <-streamDone:
	case <-ctx.Done():
		fmt.Println("\nInterrupted.")
		return
	}

	// 6. Give sidecar a grace period to finalize any trailing lines
	time.Sleep(1500 * time.Millisecond)

	// 7. Cleanly close WebSocket connection
	_ = conn.Close(websocket.StatusNormalClosure, "streaming complete")
	<-readDone

	// 8. Assemble and print full transcript summary
	mu.Lock()
	defer mu.Unlock()

	var assembled []string
	for _, id := range order {
		text := strings.TrimSpace(latestByID[id])
		if text != "" {
			assembled = append(assembled, text)
		}
	}

	fmt.Println("\n==================================================")
	fmt.Println("             FINAL ASSEMBLED TRANSCRIPT           ")
	fmt.Println("==================================================")
	if len(assembled) > 0 {
		fmt.Println(strings.Join(assembled, " "))
	} else {
		fmt.Println("(No speech detected in audio stream)")
	}
	fmt.Println("==================================================")
}

// loadOrSynthesizeAudio loads audio samples from path, or generates a synthetic
// spoken-frequency tone sequence if path is empty or does not exist.
func loadOrSynthesizeAudio(path string) ([]byte, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading WAV file %s: %w", path, err)
		}
		return parseWAVPCM(data)
	}

	// Generate a 4-second synthetic audio pattern (440Hz tone with modulated speech-range harmonics)
	const (
		sampleRate = 16000
		duration   = 4.0
		numSamples = int(sampleRate * duration)
	)

	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		// 440Hz fundamental with speech-like 220Hz envelope modulation
		env := 0.5 * (1.0 + math.Sin(2*math.Pi*2.0*t))
		val := math.Sin(2*math.Pi*440.0*t) * env * 0.3
		sample := int16(val * 32767.0)
		binary.LittleEndian.PutUint16(pcm[i*2:(i+1)*2], uint16(sample))
	}
	return pcm, nil
}

// parseWAVPCM extracts linear PCM bytes from standard RIFF WAV files,
// skipping metadata and headers.
func parseWAVPCM(data []byte) ([]byte, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		// Not a RIFF WAV container -- treat input as raw PCM bytes
		return data, nil
	}

	// Find the "data" subchunk
	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkID == "data" {
			dataStart := offset + 8
			dataEnd := dataStart + chunkSize
			if dataEnd > len(data) {
				dataEnd = len(data)
			}
			return data[dataStart:dataEnd], nil
		}
		offset += 8 + chunkSize
	}

	// Fallback to standard 44-byte header offset
	return data[44:], nil
}
