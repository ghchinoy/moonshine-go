package audio

import (
	"fmt"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// MicCapture streams mono float32 PCM at TargetSampleRate from the default
// input device. This is the one part of moonshine-go that requires cgo
// (via github.com/gen2brain/malgo, a Go wrapper around the header-only
// miniaudio library) -- a separate, unavoidable concern from the purego-based
// libmoonshine bindings in internal/moonshine, which remain cgo-free.
type MicCapture struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device
	chunks chan []float32
}

// StartMicCapture opens the default microphone at TargetSampleRate, mono,
// float32, and begins streaming audio chunks immediately.
func StartMicCapture() (*MicCapture, error) {
	maCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio: initializing audio context: %w", err)
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = 1
	cfg.SampleRate = TargetSampleRate

	mc := &MicCapture{ctx: maCtx, chunks: make(chan []float32, 64)}

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, in []byte, _ uint32) {
			if len(in) < 4 {
				return
			}
			src := unsafe.Slice((*float32)(unsafe.Pointer(&in[0])), len(in)/4)
			cp := make([]float32, len(src))
			copy(cp, src)
			select {
			case mc.chunks <- cp:
			default:
				// Consumer is falling behind; drop this chunk rather than
				// block the realtime audio callback.
			}
		},
	}

	dev, err := malgo.InitDevice(maCtx.Context, cfg, callbacks)
	if err != nil {
		_ = maCtx.Uninit()
		maCtx.Free()
		return nil, fmt.Errorf("audio: opening microphone: %w", err)
	}
	mc.device = dev

	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = maCtx.Uninit()
		maCtx.Free()
		return nil, fmt.Errorf("audio: starting microphone: %w", err)
	}
	return mc, nil
}

// Chunks returns the channel mic audio chunks arrive on. Closed by Close.
func (m *MicCapture) Chunks() <-chan []float32 { return m.chunks }

// Close stops capture and releases the device/context. Safe to call once.
func (m *MicCapture) Close() {
	if m.device != nil {
		m.device.Uninit()
	}
	if m.ctx != nil {
		_ = m.ctx.Uninit()
		m.ctx.Free()
	}
	close(m.chunks)
}
