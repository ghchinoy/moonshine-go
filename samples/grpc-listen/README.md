# samples/grpc-listen — Tier 0: gRPC streaming transcript subscriber

The smallest possible gRPC consumer of `moonshine serve`'s live transcript feed:
dials the gRPC server (:9090 by default), opens a `VoiceSidecar.Stream`
bidirectional stream, and prints each newly-finalized line as it arrives.

Compare with [../go-listen](../go-listen), which speaks WebSocket/JSON instead.
Both do the exact same Tier 0 job (subscribe to live finalized lines), but
`grpc-listen` uses `github.com/ghchinoy/moonshine-go/pkg/servepb`'s typed,
codegen'd protobuf contract (`servepb.NewVoiceSidecarClient`) rather than
decoding plain JSON envelopes off a WebSocket.

## Run it

Build/fetch `libmoonshine` first if you haven't (see repo root [README](../../README.md)).
Then, in one terminal, start the sidecar with the gRPC transport enabled:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport grpc --grpc-addr :9090
```

In another terminal:

```sh
cd samples/grpc-listen
go run . -addr :9090
```

Speak into your microphone. Finalized lines print as `[FINAL] <text>`.

## What it demonstrates

- **Transport-agnostic sidecar** — proof that `moonshine serve` speaks gRPC and
  WebSocket equally well. The server-side STT engine and event pipeline are
  identical regardless of transport.
- **Typed Go extension surface** — uses `pkg/servepb` (`#sz3`), the public
  package containing generated `VoiceSidecarClient` stubs and protobuf types,
  ready for external Go modules to import with zero manual `protoc` setup.
- **Idempotent line deduplication** — honors the same `FinalizedLineIDs`
  lookup contract as WebSocket subscribers, ensuring finalized lines are
  printed exactly once even if intermediate events drop under backpressure.

See [../README.md](../README.md) for the full Tier 0/1/2 walkthrough this sample
is part of.
