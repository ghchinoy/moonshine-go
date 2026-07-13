// Package audio provides the minimal decode/resample support moonshine-go
// needs to get PCM audio into the shape libmoonshine expects: mono float32
// samples in [-1, 1] at 16kHz (moonshine.SampleRate).
package audio

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	goaudio "github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// TargetSampleRate is the sample rate moonshine's models are trained/run at.
// Feeding audio at this rate avoids any internal resampling in the library.
const TargetSampleRate = 16000

// LoadFile decodes a local audio file into mono float32 PCM at
// TargetSampleRate. Currently supports WAV (PCM or float); other extensions
// return an error naming the extension, since converting them (e.g. via
// ffmpeg) is left to the caller for now.
func LoadFile(path string) ([]float32, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".wav":
		return loadWAV(path)
	default:
		return nil, fmt.Errorf("audio: unsupported file extension %q (only .wav is supported directly; convert other formats with ffmpeg first, e.g. `ffmpeg -i in.mp3 -ar 16000 -ac 1 out.wav`)", ext)
	}
}

func loadWAV(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("audio: %s is not a valid WAV file", path)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("audio: decoding %s: %w", path, err)
	}

	channels := buf.Format.NumChannels
	if channels < 1 {
		channels = 1
	}
	sampleRate := buf.Format.SampleRate
	if sampleRate <= 0 {
		return nil, fmt.Errorf("audio: %s has an invalid sample rate", path)
	}

	maxVal := float64(int64(1) << (uint(dec.SampleBitDepth()) - 1))
	if maxVal <= 0 {
		maxVal = 32768 // sane fallback for 16-bit
	}

	frames := len(buf.Data) / channels
	mono := make([]float32, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			sum += float64(buf.Data[i*channels+c])
		}
		mono[i] = float32((sum / float64(channels)) / maxVal)
	}

	return Resample(mono, sampleRate, TargetSampleRate), nil
}

// SaveWAV writes mono float32 PCM samples ([-1, 1]) to path as a 16-bit PCM
// WAV file at the given sample rate.
func SaveWAV(path string, samples []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := wav.NewEncoder(f, sampleRate, 16, 1, 1)
	data := make([]int, len(samples))
	for i, s := range samples {
		v := int(math.Round(float64(s) * 32767))
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		data[i] = v
	}
	buf := &goaudio.IntBuffer{
		Format:         &goaudio.Format{NumChannels: 1, SampleRate: sampleRate},
		Data:           data,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		return err
	}
	return enc.Close()
}

// Resample converts samples from srcRate to dstRate using linear
// interpolation. This is not a high-fidelity resampler (no anti-aliasing
// filter), but it's more than sufficient for speech recognition input,
// which is comparatively tolerant of resampling artifacts. If the rates
// already match, samples is returned unchanged.
func Resample(samples []float32, srcRate, dstRate int) []float32 {
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
