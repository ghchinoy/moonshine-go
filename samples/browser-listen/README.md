# samples/browser-listen — a browser tab is a full `moonshine serve` client

No install, no build step, no framework: `getUserMedia` + `AudioWorklet`
captures your microphone in the browser, streams it to a running
`moonshine serve` over a WebSocket binary frame, and the same page renders
the live transcript coming back over that connection. Open `index.html`
and you're the whole client.

This is the "composability" pillar from
[docs/MISSION.md](../../docs/MISSION.md) taken to its logical extreme, and
the concrete realization of the "browser as the audio source" scenario in
[docs/hosting.md](../../docs/hosting.md) — it's what makes `moonshine
serve` a genuinely *hostable* cascade rather than a single-machine-only
CLI feature: audio never has to originate from the same box the daemon
runs on.

## How it works

```
browser mic ──AudioWorklet──▶ int16 PCM ──WS binary frame──▶ moonshine serve
                                                                    │
                                                        RemoteAudioSource
                                                                    │
                                                              STT pipeline
                                                                    │
              browser  ◀──WS JSON frame── {"kind":"transcript",...}
```

- `worklet.js` runs on the audio rendering thread, converts each
  128-frame Float32 block from the mic into 16-bit signed little-endian
  PCM, and posts the raw bytes to the main thread.
- `app.js` sends each block as a WebSocket **binary** frame (not text —
  the server's `WSTransport` reader loop dispatches by
  `websocket.MessageBinary` vs `MessageText`, see
  `internal/serve/ws.go`), and decodes inbound **text** frames as the same
  `{"kind": "transcript", "payload": {...}}` envelope every other sample in
  this repo uses (compare with `../go-listen/main.go` or
  `../python-listen/listen.py`).

## Run it

Start the sidecar with a **remote** audio source instead of the local mic:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --addr :8765 \
  --audio-source remote \
  --remote-audio-encoding int16 \
  --remote-audio-rate 16000 \
  --remote-audio-channels 1
```

The `--remote-audio-*` flags **must match** what `app.js`/`worklet.js`
send — `int16`, `16000`, `1` (mono) are this sample's fixed choices (see
`SAMPLE_RATE` in `app.js` and the encoding logic in `worklet.js`).

Then serve this directory over HTTP (browsers block microphone access on
`file://` origins for anything but `localhost`, so a plain static server is
enough — no build tooling required):

```sh
cd samples/browser-listen
python3 -m http.server 8080
```

Open `http://localhost:8080`, leave the WebSocket address as
`ws://localhost:8765/ws` (or point it at a remote host), click **Connect**,
then **Start speaking**. Finalized transcript lines accumulate in the
transcript box; the current in-progress line shows dimmed.

## Verifying the wire protocol without a browser

Browser automation isn't available in every environment, so this sample's
protocol was verified with a small non-browser harness that generates the
exact same wire format (int16 little-endian mono PCM binary WS frames) from
a real WAV file and confirmed the server transcribes it correctly — full
real-speech transcription of a public-domain 16kHz WAV came back correctly
over the WS connection, exercising the identical `--audio-source remote`
code path this sample's JS drives. The harness itself wasn't kept (it was
scratch code, not a sample), but the verification was real, not a
guess about the wire format from reading source alone.

## What it demonstrates

- **Composability, maximally** — no native binary, no language runtime
  beyond what's built into every browser. `moonshine serve` is genuinely
  a network service here, not just a local CLI feature.
- The full round trip of `moonshine-go-cl3`'s `--audio-source remote` flag
  and `pkg/serveapi.RemoteAudioSource`, driven by a real (non-Go, non-CLI)
  client for the first time.
- The same WS wire contract (`{"kind": "transcript", ...}`) as
  `../go-listen` and `../python-listen` — proof that adding a fourth
  language to this repo's samples required zero server-side changes.

## Known limitations

- No error handling for `getUserMedia` permission denial beyond the
  browser's own prompt — a production client would want a visible error
  state here.
- No reconnect-on-drop logic; if the WebSocket closes, click **Connect**
  again.
- Only tested against Chromium-family and WebKit-family
  `AudioWorklet`/`getUserMedia` implementations available in current
  browsers; no polyfill for older browsers without `AudioWorklet` support.
