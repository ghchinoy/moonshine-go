package serve

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestRemoteAudioSource_Float32_16k_Mono(t *testing.T) {
	src := NewRemoteAudioSource(AudioFormat{
		SampleRate: 16000,
		Channels:   1,
		Encoding:   AudioEncodingFloat32,
	}, 10)

	// Build 4 float32 samples in little-endian
	samplesIn := []float32{0.0, 0.5, -0.5, 1.0}
	buf := make([]byte, len(samplesIn)*4)
	for i, s := range samplesIn {
		binary.LittleEndian.PutUint32(buf[i*4:(i+1)*4], math.Float32bits(s))
	}

	ctx := context.Background()
	if err := src.WritePCMBytes(ctx, buf); err != nil {
		t.Fatalf("WritePCMBytes error: %v", err)
	}

	select {
	case got := <-src.Chunks():
		if len(got) != len(samplesIn) {
			t.Fatalf("chunk len = %d, want %d", len(got), len(samplesIn))
		}
		for i, want := range samplesIn {
			if math.Abs(float64(got[i]-want)) > 1e-5 {
				t.Errorf("sample[%d] = %f, want %f", i, got[i], want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chunk")
	}

	if err := src.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Verify channel is closed
	_, open := <-src.Chunks()
	if open {
		t.Error("expected Chunks channel to be closed after Close()")
	}
}

func TestRemoteAudioSource_Int16_48k_Stereo_Resample(t *testing.T) {
	src := NewRemoteAudioSource(AudioFormat{
		SampleRate: 48000,
		Channels:   2,
		Encoding:   AudioEncodingInt16,
	}, 10)

	// Build 480 stereo frame pairs (1/100th second at 48kHz)
	// 480 frames * 2 channels = 960 int16 samples
	numFrames := 480
	buf := make([]byte, numFrames*2*2)
	for i := 0; i < numFrames; i++ {
		val := int16(16384) // ~0.5 float32
		binary.LittleEndian.PutUint16(buf[(i*2)*2:(i*2+1)*2], uint16(val))
		binary.LittleEndian.PutUint16(buf[(i*2+1)*2:(i*2+2)*2], uint16(val))
	}

	ctx := context.Background()
	if err := src.WritePCMBytes(ctx, buf); err != nil {
		t.Fatalf("WritePCMBytes error: %v", err)
	}

	select {
	case got := <-src.Chunks():
		// Resampled from 48kHz to 16kHz: ratio is 1/3, so 480 frames -> ~160 frames
		wantFrames := 160
		if len(got) != wantFrames {
			t.Fatalf("resampled chunk len = %d, want %d", len(got), wantFrames)
		}
		if math.Abs(float64(got[0]-0.5)) > 1e-2 {
			t.Errorf("resampled sample[0] = %f, want ~0.5", got[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resampled chunk")
	}
}

func TestRemoteAudioSource_BackpressureAndClose(t *testing.T) {
	// Small buffer of size 1
	src := NewRemoteAudioSource(AudioFormat{}, 1)

	ctx := context.Background()
	if err := src.WriteSamples(ctx, []float32{0.1}); err != nil {
		t.Fatalf("WriteSamples 1 error: %v", err)
	}

	// Channel buffer is now full (1/1). Second write should block until context is canceled.
	writeCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	err := src.WriteSamples(writeCtx, []float32{0.2})
	if err == nil {
		t.Error("expected timeout error when writing to full buffer, got nil")
	}

	_ = src.Close()
	// Write after close should return error
	if err := src.WriteSamples(ctx, []float32{0.3}); err == nil {
		t.Error("expected error when writing to closed source, got nil")
	}
}

func TestRemoteAudioSource_InvalidAlignment(t *testing.T) {
	src := NewRemoteAudioSource(AudioFormat{Encoding: AudioEncodingFloat32}, 10)
	ctx := context.Background()

	// 3 bytes is invalid for Float32 (4-byte alignment required)
	err := src.WritePCMBytes(ctx, []byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for unaligned float32 bytes, got nil")
	}
}
