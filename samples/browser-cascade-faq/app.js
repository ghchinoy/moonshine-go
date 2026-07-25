// browser-cascade-faq: browser tab as a full Tier 1 voice FAQ agent.
//
// Demonstrates:
//   - Capture mic audio via AudioWorklet -> stream binary PCM to moonshine serve
//   - Receive live transcript events over WebSocket
//   - Spot control commands ("stop listening", "resume listening") and FAQ keywords
//   - Dispatch ActionRequest JSON frames ("speak", "session.pause", "session.resume")
//   - Receive TTSAudioEvent frames back over WebSocket and play spoken answers via Web Audio API
//
// Zero install, zero build step -- the browser tab IS the agent.

const SAMPLE_RATE = 16000;

const addrInput = document.getElementById("addr");
const connectBtn = document.getElementById("connectBtn");
const startBtn = document.getElementById("startBtn");
const stopBtn = document.getElementById("stopBtn");
const statusEl = document.getElementById("status");
const transcriptEl = document.getElementById("transcript");
const logEl = document.getElementById("agentLog");

let ws = null;
let audioCtx = null;
let workletNode = null;
let micStream = null;
const seenFinalized = new Set();
let finalizedText = "";
let actionCount = 0;

const FAQ_ENTRIES = [
  {
    keyword: "mission",
    title: "Mission",
    snippet: "moonshine go is bringing back the classic voice cascade: speech to text, to a language model, to speech synthesis - because streaming transcription is finally fast enough to make it viable again.",
  },
  {
    keyword: "cascade",
    title: "The cascade",
    snippet: "The cascade never lost on capability. It lost on milliseconds. And the milliseconds are no longer the problem.",
  },
  {
    keyword: "privacy",
    title: "Privacy",
    snippet: "Audio can die at the microphone. Only the text you choose ever needs to leave the box.",
  },
  {
    keyword: "control",
    title: "Control",
    snippet: "Every stage of the cascade is yours to gate, swap, and reason about.",
  },
  {
    keyword: "observability",
    title: "Observability",
    snippet: "Every utterance is an inspectable event you can log, diff, and replay.",
  },
  {
    keyword: "composability",
    title: "Composability",
    snippet: "The transcript is a bus other processes attach to, in any language. This very browser tab is one of those processes.",
  },
];

const STOP_RE = /^\s*(stop|pause)\s+listening\s*\.?\s*$/i;
const RESUME_RE = /^\s*(resume|start)\s+listening\s*\.?\s*$/i;

function ts() {
  const d = new Date();
  const pad = (n, len = 2) => String(n).padStart(len, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

function appendLog(text, cls = "") {
  const div = document.createElement("div");
  if (cls) div.className = cls;
  div.textContent = `[${ts()}] ${text}`;
  logEl.appendChild(div);
  logEl.scrollTop = logEl.scrollHeight;
}

function setStatus(text, cls) {
  statusEl.textContent = text;
  statusEl.className = cls;
}

function renderTranscript(interimText) {
  transcriptEl.textContent = finalizedText;
  if (interimText) {
    const span = document.createElement("span");
    span.className = "interim";
    span.textContent = (finalizedText ? " " : "") + interimText;
    transcriptEl.appendChild(span);
  }
}

function processFinalizedLine(line) {
  const text = line.text || "";
  const sttMs = line.last_latency_ms;
  const sttSuffix = sttMs ? `  (stt: ${sttMs}ms)` : "";
  appendLog(`[you said] ${text}${sttSuffix}`, "user-said");

  const lowerText = text.toLowerCase();

  // 1. Fast-path regex control commands
  if (STOP_RE.test(text)) {
    appendLog('[agent] heard "stop listening" -- pausing session', "agent-act");
    sendAction("session.pause");
    return;
  }
  if (RESUME_RE.test(text)) {
    appendLog('[agent] heard "resume listening" -- resuming session', "agent-act");
    sendAction("session.resume");
    return;
  }

  // 2. Keyword FAQ entries
  for (const entry of FAQ_ENTRIES) {
    if (lowerText.includes(entry.keyword)) {
      appendLog(`[agent] matched "${entry.keyword}" -- speaking answer`, "agent-act");
      sendAction("speak", { text: entry.snippet });
      return;
    }
  }

  appendLog('[agent] no match -- try: mission, cascade, privacy, control, observability, composability, or "stop/resume listening"', "no-match");
}

function sendAction(verb, args = null) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  actionCount++;
  const req = {
    id: `browser-faq-${actionCount}`,
    verb: verb,
  };
  if (args) {
    req.args = args;
  }
  ws.send(JSON.stringify(req));
  appendLog(`[debug] sent ${verb} (id=${req.id})`, "debug-line");
}

function handleTranscriptPayload(payload) {
  const lines = payload.lines || [];
  const finalizedIds = new Set(payload.finalized_line_ids || []);
  const byId = new Map(lines.map((l) => [l.id, l]));

  for (const id of finalizedIds) {
    if (seenFinalized.has(id)) continue;
    seenFinalized.add(id);
    const line = byId.get(id);
    if (line) {
      finalizedText += (finalizedText ? "\n" : "") + line.text;
      processFinalizedLine(line);
    }
  }

  const interim = lines.find((l) => !l.is_complete);
  renderTranscript(interim ? interim.text : "");
}

function handleTTSAudioPayload(payload) {
  // TTSAudioEvent: text, audio_data ([]float32), sample_rate, state ("start"|"chunk"|"end")
  const state = payload.state || "chunk";
  if (state === "start") {
    appendLog(`[agent] TTS playback started: "${payload.text || ""}"`, "agent-act");
    return;
  }
  if (state === "end") {
    appendLog("[agent] TTS playback finished", "agent-act");
    return;
  }

  const samples = payload.audio_data;
  const sampleRate = payload.sample_rate || 24000;
  if (!samples || samples.length === 0) return;

  playPCMFloat32(samples, sampleRate);
}

function handleActionResultPayload(payload) {
  const status = payload.ok ? "ok" : `failed: ${payload.err || "unknown error"}`;
  appendLog(`[agent] action ${payload.id || ""} -> ${status}`, payload.ok ? "agent-act" : "error");
}

function playPCMFloat32(samples, sampleRate) {
  if (!audioCtx) {
    audioCtx = new AudioContext({ sampleRate: SAMPLE_RATE });
  }

  const buffer = audioCtx.createBuffer(1, samples.length, sampleRate);
  const channelData = buffer.getChannelData(0);
  for (let i = 0; i < samples.length; i++) {
    channelData[i] = samples[i];
  }

  const source = audioCtx.createBufferSource();
  source.buffer = buffer;
  source.connect(audioCtx.destination);
  source.start();
}

function connect() {
  const addr = addrInput.value.trim();
  ws = new WebSocket(addr);
  ws.binaryType = "arraybuffer";

  ws.onopen = () => {
    setStatus("connected", "connected");
    connectBtn.textContent = "Disconnect";
    startBtn.disabled = false;
    appendLog(`connected to ${addr}`, "connected");
  };

  ws.onclose = (e) => {
    setStatus("disconnected", "disconnected");
    connectBtn.textContent = "Connect";
    startBtn.disabled = true;
    stopBtn.disabled = true;
    stopCapture();
    appendLog(`disconnected (code ${e.code}${e.reason ? ": " + e.reason : ""})`, "error");
  };

  ws.onerror = () => {
    setStatus("error connecting", "error");
    appendLog("WebSocket connection error", "error");
  };

  ws.onmessage = (event) => {
    let env;
    try {
      env = JSON.parse(event.data);
    } catch {
      return; // binary/non-JSON frame -- ignore
    }

    if (env.kind === "transcript") {
      handleTranscriptPayload(env.payload);
    } else if (env.kind === "tts_audio") {
      handleTTSAudioPayload(env.payload);
    } else if (env.kind === "action_result") {
      handleActionResultPayload(env.payload);
    }
  };
}

function disconnect() {
  stopCapture();
  if (ws) {
    ws.close();
    ws = null;
  }
}

async function startCapture() {
  micStream = await navigator.mediaDevices.getUserMedia({
    audio: {
      channelCount: 1,
      sampleRate: SAMPLE_RATE,
      echoCancellation: true,
      noiseSuppression: true,
    },
  });

  if (!audioCtx) {
    audioCtx = new AudioContext({ sampleRate: SAMPLE_RATE });
  }
  await audioCtx.audioWorklet.addModule("worklet.js");

  const source = audioCtx.createMediaStreamSource(micStream);
  workletNode = new AudioWorkletNode(audioCtx, "pcm-capture-processor");

  workletNode.port.onmessage = (event) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(event.data); // ArrayBuffer -> sent as binary WS frame
    }
  };

  source.connect(workletNode);

  startBtn.disabled = true;
  stopBtn.disabled = false;
  appendLog("microphone capture started -- speak now", "connected");
}

function stopCapture() {
  if (workletNode) {
    workletNode.port.onmessage = null;
    workletNode.disconnect();
    workletNode = null;
  }
  if (micStream) {
    micStream.getTracks().forEach((t) => t.stop());
    micStream = null;
  }
  startBtn.disabled = ws ? false : true;
  stopBtn.disabled = true;
  appendLog("microphone capture stopped", "disconnected");
}

connectBtn.addEventListener("click", () => {
  if (ws) {
    disconnect();
  } else {
    connect();
  }
});
startBtn.addEventListener("click", () => startCapture());
stopBtn.addEventListener("click", () => stopCapture());
