# samples/python-listen — Tier 0: minimal live transcript client (Python)

The Python twin of [../go-listen](../go-listen): dial `moonshine serve`'s
WebSocket endpoint, decode the wire envelope, print finalized lines. No
moonshine-go dependency of any kind — just `websockets` and `json`. Proves
the "composability" pillar from [docs/MISSION.md](../../docs/MISSION.md):
the transcript is a bus any language can attach to.

## Run it

In one terminal, start the sidecar:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"  # see repo README if you haven't built/fetched libmoonshine yet
./bin/moonshine serve --transport ws --addr :8765
```

In another terminal, with [uv](https://docs.astral.sh/uv/) (no manual venv or
`pip install` needed -- the script's inline metadata declares its one
dependency):

```sh
cd samples/python-listen
uv run listen.py --addr ws://localhost:8765/ws
```

Without uv:

```sh
cd samples/python-listen
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python3 listen.py --addr ws://localhost:8765/ws
```

Speak into your microphone. Finalized lines print as `[FINAL] <text>`.

## What it demonstrates

- The WS wire format (`{"kind": "transcript", "payload": {...}}`) — no SDK,
  no codegen, just `json.loads`.
- The **finalized-once idempotency** invariant every subscriber must honor:
  dedupe on `finalized_line_ids`, not by re-scanning `is_complete` on every
  frame (which would double-count lines finalized on earlier polls).
- A cross-language JSON gotcha worth knowing in general: a naive
  `payload.get("lines", [])` in Python does **not** protect against a JSON
  `null` value the way you'd expect — the key still has to be *absent* for
  the default to kick in, not just falsy. `listen.py`'s
  `(payload.get("lines") or [])` handles both "key absent" and "key present
  but null" defensively, regardless of what the server currently sends.

See [../README.md](../README.md) for the full Tier 0/1/2 walkthrough this
sample is part of.
