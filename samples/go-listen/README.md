# samples/go-listen — Tier 0: minimal live transcript client

The smallest possible consumer of `moonshine serve`'s live transcript feed:
dial the WebSocket endpoint, decode the wire envelope, print finalized
lines. ~90 lines, one dependency (a WebSocket client — Go's stdlib doesn't
ship one), **no moonshine-go import at all**.

That's the point: `moonshine serve`'s primary extension surface is JSON over
a WebSocket/gRPC connection, not a Go API. Anything with a WebSocket client
and a JSON decoder — Python, JS, Rust, curl+jq with a bit of glue — can do
what this program does (see [../python-listen](../python-listen) for the
same idea in Python). This is the "composability" pillar from
[docs/MISSION.md](../../docs/MISSION.md): the transcript is a bus other
processes attach to, in any language.

This sample is its own Go module (see `go.mod`) so it can't accidentally
import `internal/*` from the parent module — it consumes exactly what an
external, un-privileged client would have access to.

## Run it

In one terminal, start the sidecar:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"  # see repo README if you haven't built/fetched libmoonshine yet
./bin/moonshine serve --transport ws --addr :8765
```

In another terminal:

```sh
cd samples/go-listen
go run . -addr ws://localhost:8765/ws
```

Speak into your microphone. Finalized lines print as `[FINAL] <text>`.

## What it demonstrates

- The WS wire format (`{"kind": "transcript", "payload": {...}}`) is a
  stable, documented contract (see `docs/serve-sidecar.md` and
  `internal/serve/ws.go`'s `wireEnvelope`) — this client hand-decodes it
  with plain `encoding/json`, no shared types with the server.
- The **finalized-once idempotency** invariant every subscriber must honor:
  dedupe on `finalized_line_ids`, not by scanning every line in every frame
  for `is_complete` (which would double-count lines finalized on earlier
  polls — the snapshot is cumulative).

See [../README.md](../README.md) for the full Tier 0/1/2 walkthrough this
sample is part of.
