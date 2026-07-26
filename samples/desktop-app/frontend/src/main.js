import { StartStream, PushPCMChunk, StopStream, TranscribeFile } from '../wailsjs/go/main/App';

const startBtn = document.getElementById("startBtn");
const stopBtn = document.getElementById("stopBtn");
const fileInput = document.getElementById("fileInput");
const micStatus = document.getElementById("micStatus");
const fileStatus = document.getElementById("fileStatus");
const transcriptEl = document.getElementById("transcript");

let audioCtx = null;
let workletNode = null;
let micStream = null;
let isStreaming = false;

function renderLines(lines) {
  if (!lines || lines.length === 0) return;

  let text = "";
  let interimText = "";

  for (const line of lines) {
    if (line.is_complete) {
      text += (text ? "\n" : "") + `[${formatTime(line.start_time)}] ${line.text}`;
    } else {
      interimText = line.text;
    }
  }

  transcriptEl.textContent = text;
  if (interimText) {
    const span = document.createElement("span");
    span.className = "interim";
    span.textContent = (text ? "\n" : "") + `[interim] ${interimText}`;
    transcriptEl.appendChild(span);
  }
}

function formatTime(secs) {
  const m = Math.floor(secs / 60);
  const s = Math.floor(secs % 60);
  return `${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
}

async function startDictation() {
  try {
    micStatus.textContent = "Status: Starting stream...";
    await StartStream("en_us", "tiny-streaming");

    micStream = await navigator.mediaDevices.getUserMedia({
      audio: {
        channelCount: 1,
        sampleRate: 16000,
        echoCancellation: true,
        noiseSuppression: true,
      }
    });

    audioCtx = new AudioContext({ sampleRate: 16000 });
    await audioCtx.audioWorklet.addModule("/worklet.js");

    const source = audioCtx.createMediaStreamSource(micStream);
    workletNode = new AudioWorkletNode(audioCtx, "pcm-capture-processor");

    isStreaming = true;

    workletNode.port.onmessage = async (event) => {
      if (!isStreaming) return;
      const float32Array = Array.from(event.data);
      try {
        const lines = await PushPCMChunk(float32Array);
        renderLines(lines);
      } catch (err) {
        console.error("PushPCMChunk error:", err);
      }
    };

    source.connect(workletNode);

    startBtn.disabled = true;
    stopBtn.disabled = false;
    micStatus.textContent = "Status: Dictating (listening)...";
  } catch (err) {
    micStatus.textContent = `Status Error: ${err.message || err}`;
    console.error("startDictation error:", err);
  }
}

async function stopDictation() {
  isStreaming = false;
  micStatus.textContent = "Status: Stopping stream...";

  if (workletNode) {
    workletNode.port.onmessage = null;
    workletNode.disconnect();
    workletNode = null;
  }
  if (audioCtx) {
    audioCtx.close();
    audioCtx = null;
  }
  if (micStream) {
    micStream.getTracks().forEach((t) => t.stop());
    micStream = null;
  }

  try {
    await StopStream();
    micStatus.textContent = "Status: Stopped.";
  } catch (err) {
    micStatus.textContent = `Status Error: ${err.message || err}`;
  }

  startBtn.disabled = false;
  stopBtn.disabled = true;
}

fileInput.addEventListener("change", async (e) => {
  const file = e.target.files[0];
  if (!file) return;

  fileStatus.textContent = `Transcribing ${file.name}...`;
  transcriptEl.textContent = "Processing audio file in-process...";

  try {
    const res = await TranscribeFile(file.path || file.name, "en_us", "tiny");
    let text = "";
    for (const line of res.lines) {
      text += (text ? "\n" : "") + `[${formatTime(line.start_time)}] ${line.text} (conf: ${Math.round(line.mean_confidence * 100)}%)`;
    }
    transcriptEl.textContent = text || "No text detected in WAV file.";
    fileStatus.textContent = `Done in ${Math.round(res.inference_ms)}ms (${res.real_time_factor.toFixed(1)}x RTF).`;
  } catch (err) {
    fileStatus.textContent = `Error: ${err.message || err}`;
    transcriptEl.textContent = `Error transcribing file: ${err.message || err}`;
  }
});

startBtn.addEventListener("click", startDictation);
stopBtn.addEventListener("click", stopDictation);
