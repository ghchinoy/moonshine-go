package audio

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// PlayFloat32 plays mono float32 PCM samples ([-1, 1]) through the default
// output device at sampleRate, blocking until playback finishes. It reuses
// the same miniaudio backend (via malgo) as MicCapture -- CoreAudio on
// macOS, WASAPI on Windows, ALSA/PulseAudio on Linux -- so it's genuinely
// cross-platform without adding a new dependency. This is the playback
// counterpart to MicCapture, and (together with it) the only part of
// internal/audio that requires cgo.
func PlayFloat32(samples []float32, sampleRate int32) error {
	if len(samples) == 0 {
		return nil
	}

	maCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("audio: initializing audio context: %w", err)
	}
	defer func() {
		_ = maCtx.Uninit()
		maCtx.Free()
	}()

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = 1
	cfg.SampleRate = uint32(sampleRate)

	// pos is only ever touched from the (single-threaded) audio callback,
	// so no synchronization is needed -- the calling goroutine below never
	// reads it, it just sleeps for the clip's duration.
	pos := 0
	callbacks := malgo.DeviceCallbacks{
		Data: func(out, _ []byte, _ uint32) {
			if len(out) < 4 {
				return
			}
			dst := unsafe.Slice((*float32)(unsafe.Pointer(&out[0])), len(out)/4)
			n := copy(dst, samples[pos:])
			pos += n
			for i := n; i < len(dst); i++ {
				dst[i] = 0 // silence once the clip is exhausted
			}
		},
	}

	dev, err := malgo.InitDevice(maCtx.Context, cfg, callbacks)
	if err != nil {
		return fmt.Errorf("audio: opening audio output device: %w", err)
	}
	defer dev.Uninit()

	if err := dev.Start(); err != nil {
		return fmt.Errorf("audio: starting playback: %w", err)
	}

	// miniaudio's data callback only tells us when audio has been *queued*,
	// not when it has actually finished playing out through the device's
	// own internal buffering, so block for the clip's real duration plus a
	// fixed drain margin rather than racing dev.Uninit() against the
	// backend still draining its buffer.
	duration := time.Duration(float64(len(samples)) / float64(sampleRate) * float64(time.Second))
	time.Sleep(duration + 200*time.Millisecond)

	return nil
}
