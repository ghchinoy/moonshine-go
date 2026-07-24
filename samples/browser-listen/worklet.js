// AudioWorkletProcessor that runs on the audio rendering thread: receives
// Float32 PCM from the microphone in fixed-size blocks (128 frames per the
// Web Audio spec), converts to 16-bit signed little-endian PCM (the
// simplest wire format pkg/serveapi.RemoteAudioSource understands -- see
// decodePCM in ../../pkg/serveapi/remote_audio.go), and posts the raw
// bytes to the main thread. Runs at whatever sample rate the browser's
// AudioContext is using (typically 44100 or 48000 Hz) -- no resampling
// happens here; the server resamples to 16kHz itself (see
// RemoteAudioSource.WritePCMBytes), so this stays simple and correct
// regardless of the device's native rate.
class PCMCaptureProcessor extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs[0];
    if (!input || input.length === 0) {
      return true; // keep the processor alive even with no input yet
    }
    const channel = input[0]; // mono: just the first channel
    if (!channel || channel.length === 0) {
      return true;
    }

    const int16 = new Int16Array(channel.length);
    for (let i = 0; i < channel.length; i++) {
      // Clamp to [-1, 1] before scaling to avoid wraparound on clipped input.
      const s = Math.max(-1, Math.min(1, channel[i]));
      int16[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
    }

    // Transfer the underlying buffer (zero-copy) to the main thread.
    this.port.postMessage(int16.buffer, [int16.buffer]);
    return true;
  }
}

registerProcessor("pcm-capture-processor", PCMCaptureProcessor);
