package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"github.com/coder/websocket"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

type Config struct {
	Target       string
	Streams      int
	Duration     time.Duration
	Warmup       time.Duration
	ChunkMs      int
	WavFile      string
	Encoding     string
	SampleRate   int
	ReportFormat string
	ServerPID    int
}

type StreamStats struct {
	ID               int
	ChunksSent       int64
	BytesSent        int64
	TTFTMs           int64 // -1 if never seen
	InterimEvents    int64
	FinalizedLines   int64
	FirstChunkSentAt time.Time

	TTFTHistogram        *hdrhistogram.Histogram
	InterimLatencyHist   *hdrhistogram.Histogram
	FinalizedLatencyHist *hdrhistogram.Histogram
}

type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	var cfg Config
	flag.StringVar(&cfg.Target, "target", "ws://localhost:8765/ws", "moonshine serve WebSocket endpoint URL")
	flag.IntVar(&cfg.Streams, "streams", 10, "number of concurrent WebSocket audio streams")
	flag.DurationVar(&cfg.Duration, "duration", 30*time.Second, "test duration (excluding warmup)")
	flag.DurationVar(&cfg.Warmup, "warmup", 5*time.Second, "warmup duration before recording metrics")
	flag.IntVar(&cfg.ChunkMs, "chunk-ms", 100, "audio chunk duration in milliseconds (e.g. 100 for 100ms)")
	flag.StringVar(&cfg.WavFile, "wav-file", "bench/testdata/two_cities_16k.wav", "path to 16kHz PCM16 LE mono test WAV file")
	flag.StringVar(&cfg.Encoding, "encoding", "float32", "remote audio sample encoding: float32 or int16 (must match server --remote-audio-encoding)")
	flag.IntVar(&cfg.SampleRate, "sample-rate", 16000, "remote audio sample rate in Hz (must match server --remote-audio-rate)")
	flag.StringVar(&cfg.ReportFormat, "report", "md", "output report format: md (Markdown) or json")
	flag.IntVar(&cfg.ServerPID, "server-pid", 0, "optional PID of moonshine serve process to track OS RSS memory usage")
	flag.Parse()

	if cfg.Streams <= 0 {
		log.Fatalf("invalid --streams: must be > 0")
	}

	// Load WAV file
	pcmSamples, wavRate, err := loadWAVPCM(cfg.WavFile)
	if err != nil {
		log.Fatalf("failed to load WAV file %s: %v", cfg.WavFile, err)
	}
	if wavRate != int32(cfg.SampleRate) {
		log.Printf("warning: WAV sample rate (%dHz) differs from --sample-rate (%dHz)", wavRate, cfg.SampleRate)
	}

	// Prepare raw chunk bytes per chunk-ms
	samplesPerChunk := (cfg.SampleRate * cfg.ChunkMs) / 1000
	chunkBytes := encodeChunk(pcmSamples, samplesPerChunk, cfg.Encoding)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Initial RSS
	initialRSS := getProcessRSS(cfg.ServerPID)

	fmt.Fprintf(os.Stderr, "Starting load test against %s (%d concurrent streams, %v duration, %v warmup)...\n",
		cfg.Target, cfg.Streams, cfg.Duration, cfg.Warmup)
	fmt.Fprintf(os.Stderr, "Audio: %s (%dHz, %s encoding, %dms chunks)\n\n",
		cfg.WavFile, cfg.SampleRate, cfg.Encoding, cfg.ChunkMs)

	var wg sync.WaitGroup
	stats := make([]*StreamStats, cfg.Streams)
	var activeStreams int64
	startSignal := make(chan struct{})

	testStartTime := time.Now()

	for i := 0; i < cfg.Streams; i++ {
		i := i
		st := &StreamStats{
			ID:                   i,
			TTFTMs:               -1,
			TTFTHistogram:        hdrhistogram.New(1, 30000, 3),
			InterimLatencyHist:   hdrhistogram.New(1, 30000, 3),
			FinalizedLatencyHist: hdrhistogram.New(1, 30000, 3),
		}
		stats[i] = st
		wg.Add(1)

		go func() {
			defer wg.Done()
			runStreamWorker(ctx, cfg, i, chunkBytes, pcmSamples, samplesPerChunk, st, &activeStreams, startSignal)
		}()
	}

	close(startSignal)

	// Monitor RSS if PID provided
	var peakRSS int64 = initialRSS
	if cfg.ServerPID > 0 {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					rss := getProcessRSS(cfg.ServerPID)
					if rss > peakRSS {
						peakRSS = rss
					}
				}
			}
		}()
	}

	wg.Wait()
	testDuration := time.Since(testStartTime)
	finalRSS := getProcessRSS(cfg.ServerPID)

	// Render Report
	renderReport(cfg, stats, testDuration, initialRSS, peakRSS, finalRSS)
}

func runStreamWorker(ctx context.Context, cfg Config, id int, chunkBytes [][]byte, pcmSamples []float32, samplesPerChunk int, st *StreamStats, activeStreams *int64, startSignal chan struct{}) {
	<-startSignal

	conn, _, err := websocket.Dial(ctx, cfg.Target, nil)
	if err != nil {
		log.Printf("[stream %d] dial failed: %v", id, err)
		return
	}
	defer conn.CloseNow() //nolint:errcheck

	atomic.AddInt64(activeStreams, 1)
	defer atomic.AddInt64(activeStreams, -1)

	workerCtx, cancel := context.WithTimeout(ctx, cfg.Duration+cfg.Warmup+10*time.Second)
	defer cancel()

	chunkInterval := time.Duration(cfg.ChunkMs) * time.Millisecond
	numChunks := len(chunkBytes)
	chunkIdx := 0

	st.FirstChunkSentAt = time.Now()
	warmupEndTime := st.FirstChunkSentAt.Add(cfg.Warmup)
	testEndTime := warmupEndTime.Add(cfg.Duration)

	var mu sync.Mutex
	lineStartTimes := make(map[uint64]time.Time)

	// Reader goroutine: listen for KindTranscript envelopes
	go func() {
		for {
			_, data, err := conn.Read(workerCtx)
			if err != nil {
				return
			}
			now := time.Now()
			if now.Before(warmupEndTime) {
				continue // skip warmup recording
			}
			if now.After(testEndTime) {
				return
			}

			var env envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue
			}
			if env.Kind != string(serveapi.KindTranscript) {
				continue
			}

			var te serveapi.TranscriptEvent
			if err := json.Unmarshal(env.Payload, &te); err != nil {
				continue
			}

			mu.Lock()
			// Record TTFT
			if st.TTFTMs < 0 && te.TTFTms > 0 {
				st.TTFTMs = te.TTFTms
				_ = st.TTFTHistogram.RecordValue(te.TTFTms)
			}

			// Record interim events
			if len(te.Lines) > 0 {
				st.InterimEvents++
				if te.PollLatencyMs > 0 {
					_ = st.InterimLatencyHist.RecordValue(te.PollLatencyMs)
				}
				for _, l := range te.Lines {
					if _, exists := lineStartTimes[l.ID]; !exists {
						lineStartTimes[l.ID] = now
					}
				}
			}

			// Record finalized lines
			for _, id := range te.FinalizedLineIDs {
				st.FinalizedLines++
				if start, exists := lineStartTimes[id]; exists {
					latMs := now.Sub(start).Milliseconds()
					if latMs > 0 {
						_ = st.FinalizedLatencyHist.RecordValue(latMs)
					}
					delete(lineStartTimes, id)
				}
			}
			mu.Unlock()
		}
	}()

	// Writer loop: send binary PCM chunks every chunkInterval
	ticker := time.NewTicker(chunkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-workerCtx.Done():
			return
		case now := <-ticker.C:
			if now.After(testEndTime) {
				return
			}

			b := chunkBytes[chunkIdx%numChunks]
			chunkIdx++

			err := conn.Write(workerCtx, websocket.MessageBinary, b)
			if err != nil {
				log.Printf("[stream %d] write error: %v", id, err)
				return
			}

			atomic.AddInt64(&st.ChunksSent, 1)
			atomic.AddInt64(&st.BytesSent, int64(len(b)))
		}
	}
}

func loadWAVPCM(path string) ([]float32, int32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	pos := 12
	var dataOff, dataLen int
	var sampleRate int32
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
		return nil, 0, fmt.Errorf("no data chunk found")
	}
	n := dataLen / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[dataOff+i*2 : dataOff+i*2+2]))
		samples[i] = float32(v) / 32768.0
	}
	return samples, sampleRate, nil
}

func encodeChunk(pcm []float32, samplesPerChunk int, encoding string) [][]byte {
	numChunks := len(pcm) / samplesPerChunk
	if numChunks == 0 {
		numChunks = 1
	}
	chunks := make([][]byte, numChunks)

	for c := 0; c < numChunks; c++ {
		start := c * samplesPerChunk
		end := start + samplesPerChunk
		if end > len(pcm) {
			end = len(pcm)
		}
		sub := pcm[start:end]

		if strings.ToLower(encoding) == "int16" {
			b := make([]byte, len(sub)*2)
			for i, v := range sub {
				val := int16(v * 32767.0)
				binary.LittleEndian.PutUint16(b[i*2:i*2+2], uint16(val))
			}
			chunks[c] = b
		} else { // default float32
			b := make([]byte, len(sub)*4)
			for i, v := range sub {
				bits := math.Float32bits(v)
				binary.LittleEndian.PutUint32(b[i*4:i*4+4], bits)
			}
			chunks[c] = b
		}
	}
	return chunks
}

func getProcessRSS(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
		if err == nil {
			rssKB, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
			return rssKB * 1024
		}
	} else if runtime.GOOS == "linux" {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				pages, _ := strconv.ParseInt(fields[1], 10, 64)
				return pages * int64(os.Getpagesize())
			}
		}
	}
	return 0
}
