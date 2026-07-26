import { StartStream, PushPCMChunk, StopStream, TranscribeFile, TranscribePCM, SelectAudioFile } from '../wailsjs/go/main/App';

const startBtn = document.getElementById("startBtn");
const stopBtn = document.getElementById("stopBtn");
const browseBtn = document.getElementById("browseBtn");
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

function renderBatchResult(res, fileName) {
  let text = "";
  for (const line of res.lines) {
    text += (text ? "\n" : "") + `[${formatTime(line.start_time)}] ${line.text} (conf: ${Math.round(line.mean_confidence * 100)}%)`;
  }
  transcriptEl.textContent = text || `No speech text detected in ${fileName}.`;
  fileStatus.textContent = `Done in ${Math.round(res.inference_ms)}ms (${res.real_time_factor.toFixed(1)}x RTF).`;
}

function formatTime(secs) {
  const m = Math.floor(secs / 60);
  const s = Math.floor(secs % 60);
  return `${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
}

async function startDictation() {
  try {
    micStatus.textContent = "Status: Starting stream...";
    await StartStream("en", "tiny-streaming");

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

async function processFilePath(filePath) {
  const fileName = filePath.split('/').pop().split('\\').pop();
  fileStatus.textContent = `Transcribing ${fileName}...`;
  transcriptEl.textContent = "Processing audio file in-process...";

  try {
    const res = await TranscribeFile(filePath, "en", "tiny");
    renderBatchResult(res, fileName);
  } catch (err) {
    fileStatus.textContent = `Error: ${err.message || err}`;
    transcriptEl.textContent = `Error transcribing file: ${err.message || err}`;
  }
}

async function processAudioFile(file) {
  fileStatus.textContent = `Decoding and transcribing ${file.name}...`;
  transcriptEl.textContent = "Processing audio file in-process...";

  try {
    const arrayBuffer = await file.arrayBuffer();
    const offlineCtx = new (window.AudioContext || window.webkitAudioContext)();
    const audioBuffer = await offlineCtx.decodeAudioData(arrayBuffer);

    const sampleRate = audioBuffer.sampleRate;
    const numChannels = audioBuffer.numberOfChannels;
    const length = audioBuffer.length;

    let monoPCM = new Float32Array(length);
    if (numChannels === 1) {
      monoPCM.set(audioBuffer.getChannelData(0));
    } else {
      const left = audioBuffer.getChannelData(0);
      const right = audioBuffer.getChannelData(1);
      for (let i = 0; i < length; i++) {
        monoPCM[i] = (left[i] + right[i]) / 2.0;
      }
    }

    let targetPCM = monoPCM;
    let targetRate = sampleRate;
    if (sampleRate !== 16000) {
      const offlineResampleCtx = new OfflineAudioContext(1, Math.ceil(length * 16000 / sampleRate), 16000);
      const bufferSource = offlineResampleCtx.createBufferSource();
      bufferSource.buffer = audioBuffer;
      bufferSource.connect(offlineResampleCtx.destination);
      bufferSource.start(0);
      const resampledBuffer = await offlineResampleCtx.startRendering();
      targetPCM = resampledBuffer.getChannelData(0);
      targetRate = 16000;
    }

    const res = await TranscribePCM(Array.from(targetPCM), targetRate, "en", "tiny");
    renderBatchResult(res, file.name);
  } catch (err) {
    console.warn("In-browser Web Audio decode failed, falling back to path transcription:", err);
    if (file.path || file.name) {
      await processFilePath(file.path || file.name);
    } else {
      fileStatus.textContent = `Error decoding audio: ${err.message || err}`;
      transcriptEl.textContent = `Error: ${err.message || err}`;
    }
  }
}

if (browseBtn) {
  browseBtn.addEventListener("click", async () => {
    try {
      const selectedPath = await SelectAudioFile();
      if (!selectedPath) return;
      await processFilePath(selectedPath);
    } catch (err) {
      fileStatus.textContent = `Error selecting file: ${err.message || err}`;
    }
  });
}

fileInput.addEventListener("change", async (e) => {
  const file = e.target.files[0];
  if (!file) return;
  await processAudioFile(file);
});

startBtn.addEventListener("click", startDictation);
stopBtn.addEventListener("click", stopDictation);
