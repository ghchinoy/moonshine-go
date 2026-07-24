# samples/python-agent — Tier 1: external voice agent (Python)

A Python external agent for `moonshine serve`: watches finalized transcript
lines for a couple of deterministic voice commands and sends
`ActionRequest` JSON back over the same WebSocket connection to trigger
real effects — speaking the current time, or pausing/resuming the session.
No moonshine-go dependency of any kind, just `websockets` + `json` — the
same wire contract [../go-cascade-faq](../go-cascade-faq) uses from Go.

This demonstrates the **"control"** pillar from
[docs/MISSION.md](../../docs/MISSION.md): deterministic regex matching, not
an LLM call — fully auditable, fully offline, and a fast-path pattern any
language can implement without an SDK.

## Run it

In one terminal, start the sidecar:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --allow-actions --agent external
```

`--allow-actions` is required — without it the sidecar rejects `speak` and
`session.*` actions.

In another terminal:

```sh
cd samples/python-agent
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python3 agent.py --addr ws://localhost:8765/ws
```

Then say **"what time is it"**, **"stop listening"**, or **"resume
listening"**.

## What it demonstrates

- The full Tier 1 round trip: read a `TranscriptEvent` frame, decide on an
  action, send an `ActionRequest` frame back, all with plain
  `json.dumps`/`json.loads` — no code generation, no shared types with the
  server.
- `classify()` is the whole "agent" — a couple of `re.match`/`re.search`
  calls. Compare with [../go-cascade-faq](../go-cascade-faq)'s
  `controlHandler`, which does the same match-then-`ActionRequest` pattern
  in Go against `pkg/serveapi`'s typed interfaces instead of hand-rolled
  dicts.

See [../README.md](../README.md) for the full Tier 0/1/2 walkthrough this
sample is part of.
