class PCMCaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.bufferSize = 1600; // ~100ms at 16kHz
    this.buffer = new Float32Array(this.bufferSize);
    this.bufferIndex = 0;
  }

  process(inputs) {
    const input = inputs[0];
    if (!input || input.length === 0) return true;

    const channel = input[0];
    for (let i = 0; i < channel.length; i++) {
      this.buffer[this.bufferIndex++] = channel[i];
      if (this.bufferIndex >= this.bufferSize) {
        this.port.postMessage(this.buffer.slice(0, this.bufferSize));
        this.bufferIndex = 0;
      }
    }
    return true;
  }
}

registerProcessor("pcm-capture-processor", PCMCaptureProcessor);
