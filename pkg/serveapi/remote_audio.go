package serveapi

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

// AudioEncoding specifies the sample format for binary PCM audio.
type AudioEncoding string

const (
	AudioEncodingFloat32 AudioEncoding = "float32" // 32-bit IEEE float (little-endian)
	AudioEncodingInt16   AudioEncoding = "int16"   // 16-bit signed integer (little-endian)
)

// AudioFormat defines the wire parameters for remote PCM audio.
type AudioFormat struct {
	SampleRate int           // e.g. 16000, 48000; default 16000
	Channels   int           // e.g. 1 (mono), 2 (stereo); default 1
	Encoding   AudioEncoding // float32 or int16; default float32
}

// RemoteAudioSource implements AudioSource for remote PCM streaming
// over WebSocket or gRPC connections without a local microphone.
type RemoteAudioSource struct {
	mu     sync.Mutex
	format AudioFormat
	chunks chan []float32
	err    error
	closed bool
}

// NewRemoteAudioSource creates a RemoteAudioSource with a bounded chunk channel.
// bufferSize defaults to 100 if <= 0 (holding ~10s of audio chunks).
func NewRemoteAudioSource(format AudioFormat, bufferSize int) *RemoteAudioSource {
	if format.SampleRate <= 0 {
		format.SampleRate = 16000
	}
	if format.Channels <= 0 {
		format.Channels = 1
	}
	if format.Encoding == "" {
		format.Encoding = AudioEncodingFloat32
	}
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &RemoteAudioSource{
		format: format,
		chunks: make(chan []float32, bufferSize),
	}
}

// Chunks returns the channel emitting []float32 16kHz mono PCM chunks.
func (r *RemoteAudioSource) Chunks() <-chan []float32 {
	return r.chunks
}

// Err returns any fatal error encountered during ingestion.
func (r *RemoteAudioSource) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// WriteSamples pushes pre-decoded float32 samples into the source.
// Blocks if the chunk channel buffer is full, exerting backpressure on the sender.
func (r *RemoteAudioSource) WriteSamples(ctx context.Context, samples []float32) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("serveapi: RemoteAudioSource is closed")
	}
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.chunks <- samples:
		return nil
	}
}

// WritePCMBytes decodes binary PCM data according to format and writes it.
func (r *RemoteAudioSource) WritePCMBytes(ctx context.Context, data []byte, fmtOpt ...AudioFormat) error {
	f := r.format
	if len(fmtOpt) > 0 {
		if fmtOpt[0].SampleRate > 0 {
			f.SampleRate = fmtOpt[0].SampleRate
		}
		if fmtOpt[0].Channels > 0 {
			f.Channels = fmtOpt[0].Channels
		}
		if fmtOpt[0].Encoding != "" {
			f.Encoding = fmtOpt[0].Encoding
		}
	}

	samples, err := decodePCM(data, f)
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return nil
	}

	// Resample to 16kHz if source rate differs
	if f.SampleRate != 16000 {
		samples = resample(samples, f.SampleRate, 16000)
	}

	return r.WriteSamples(ctx, samples)
}

// Close closes the chunk channel, signaling clean EOF to the session.
func (r *RemoteAudioSource) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.chunks)
	}
	return nil
}

// decodePCM converts raw PCM bytes to mono float32 samples.
func decodePCM(b []byte, f AudioFormat) ([]float32, error) {
	channels := f.Channels
	if channels <= 0 {
		channels = 1
	}

	switch f.Encoding {
	case AudioEncodingInt16:
		if len(b)%2 != 0 {
			return nil, fmt.Errorf("serveapi: int16 PCM data length (%d) is not 2-byte aligned", len(b))
		}
		numSamples := len(b) / 2
		frames := numSamples / channels
		out := make([]float32, frames)

		for i := 0; i < frames; i++ {
			var sum float64
			for c := 0; c < channels; c++ {
				idx := (i*channels + c) * 2
				val := int16(binary.LittleEndian.Uint16(b[idx : idx+2]))
				sum += float64(val) / 32768.0
			}
			out[i] = float32(sum / float64(channels))
		}
		return out, nil

	case AudioEncodingFloat32:
		if len(b)%4 != 0 {
			return nil, fmt.Errorf("serveapi: float32 PCM data length (%d) is not 4-byte aligned", len(b))
		}
		numSamples := len(b) / 4
		frames := numSamples / channels
		out := make([]float32, frames)

		for i := 0; i < frames; i++ {
			var sum float64
			for c := 0; c < channels; c++ {
				idx := (i*channels + c) * 4
				bits := binary.LittleEndian.Uint32(b[idx : idx+4])
				val := math.Float32frombits(bits)
				sum += float64(val)
			}
			out[i] = float32(sum / float64(channels))
		}
		return out, nil

	default:
		return nil, fmt.Errorf("serveapi: unsupported audio encoding %q", f.Encoding)
	}
}

// resample converts samples from srcRate to dstRate using linear
// interpolation. This is sufficient for speech recognition input.
func resample(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate || len(samples) == 0 {
		return samples
	}
	ratio := float64(dstRate) / float64(srcRate)
	outLen := int(math.Round(float64(len(samples)) * ratio))
	if outLen <= 0 {
		return nil
	}
	out := make([]float32, outLen)
	for i := range out {
		srcPos := float64(i) / ratio
		i0 := int(math.Floor(srcPos))
		i1 := i0 + 1
		frac := srcPos - float64(i0)
		if i0 < 0 {
			i0 = 0
		}
		if i0 >= len(samples) {
			i0 = len(samples) - 1
		}
		if i1 >= len(samples) {
			i1 = len(samples) - 1
		}
		out[i] = samples[i0] + float32(frac)*(samples[i1]-samples[i0])
	}
	return out
}

// Compile-time check that RemoteAudioSource implements AudioSource
var _ AudioSource = (*RemoteAudioSource)(nil)
