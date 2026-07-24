// browser-listen: mic -> AudioWorklet -> WebSocket binary frames -> moonshine
// serve, and the live transcript back over the same connection. No
// framework, no build step -- this is the whole client.
//
// Wire contract this depends on (see ../README.md for the full writeup):
//   - Outbound: raw int16 little-endian mono PCM, one WebSocket *binary*
//     frame per AudioWorklet callback. Must match the server's
//     --remote-audio-encoding/--remote-audio-rate/--remote-audio-channels
//     flags (see pkg/serveapi/remote_audio.go's decodePCM).
//   - Inbound: the same {"kind": "transcript", "payload": {...}} JSON text
//     envelope every other sample in this repo decodes (see
//     ../go-listen/main.go or ../python-listen/listen.py for the
//     equivalent).

const SAMPLE_RATE = 16000; // must match --remote-audio-rate on the server

const addrInput = document.getElementById("addr");
const connectBtn = document.getElementById("connectBtn");
const startBtn = document.getElementById("startBtn");
const stopBtn = document.getElementById("stopBtn");
const statusEl = document.getElementById("status");
const transcriptEl = document.getElementById("transcript");

let ws = null;
let audioCtx = null;
let workletNode = null;
let micStream = null;
const seenFinalized = new Set();
let finalizedText = "";

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

function handleTranscriptPayload(payload) {
  const lines = payload.lines || [];
  const finalizedIds = new Set(payload.finalized_line_ids || []);
  const byId = new Map(lines.map((l) => [l.id, l]));

  // Newly-finalized lines: dedupe on ID (the idempotency contract every
  // subscriber must honor -- see ../../docs/serve-sidecar.md).
  for (const id of finalizedIds) {
    if (seenFinalized.has(id)) continue;
    seenFinalized.add(id);
    const line = byId.get(id);
    if (line) {
      finalizedText += (finalizedText ? "\n" : "") + line.text;
    }
  }

  // The (at most one) trailing in-progress line, for live feedback.
  const interim = lines.find((l) => !l.is_complete);
  renderTranscript(interim ? interim.text : "");
}

function connect() {
  const addr = addrInput.value.trim();
  ws = new WebSocket(addr);
  ws.binaryType = "arraybuffer";

  ws.onopen = () => {
    setStatus("connected", "connected");
    connectBtn.textContent = "Disconnect";
    startBtn.disabled = false;
  };

  ws.onclose = () => {
    setStatus("disconnected", "disconnected");
    connectBtn.textContent = "Connect";
    startBtn.disabled = true;
    stopBtn.disabled = true;
    stopCapture();
  };

  ws.onerror = () => {
    setStatus("error connecting", "error");
  };

  ws.onmessage = (event) => {
    let env;
    try {
      env = JSON.parse(event.data);
    } catch {
      return; // binary/non-JSON frame -- not expected inbound, ignore
    }
    if (env.kind !== "transcript") return;
    handleTranscriptPayload(env.payload);
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

  // Requesting sampleRate on the AudioContext keeps the whole pipeline at
  // 16kHz end to end, matching --remote-audio-rate exactly and avoiding
  // any ambiguity about what rate actually left the browser. Not every
  // browser/device honors this exactly; the server resamples defensively
  // regardless (see RemoteAudioSource.WritePCMBytes), so a mismatch here
  // degrades gracefully rather than breaking.
  audioCtx = new AudioContext({ sampleRate: SAMPLE_RATE });
  await audioCtx.audioWorklet.addModule("worklet.js");

  const source = audioCtx.createMediaStreamSource(micStream);
  workletNode = new AudioWorkletNode(audioCtx, "pcm-capture-processor");

  workletNode.port.onmessage = (event) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(event.data); // ArrayBuffer -> sent as a binary WS frame
    }
  };

  source.connect(workletNode);
  // Not connecting workletNode to audioCtx.destination: we don't want to
  // hear our own mic input played back locally.

  startBtn.disabled = true;
  stopBtn.disabled = false;
}

function stopCapture() {
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
  startBtn.disabled = ws ? false : true;
  stopBtn.disabled = true;
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
