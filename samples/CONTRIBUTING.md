# Contributing to samples/

`samples/` exists to make `moonshine serve`'s extension points provable, not
just describable. If a sample can't run and can't fail loudly, it isn't
finished — that's the whole reason `docs/quickstart-voice-agent.md` got
replaced with this directory: an inline doc snippet drifted out of sync with
a real refactor for a full epic cycle before anyone noticed, because nothing
ever tried to compile or run it.

## What belongs here

A sample earns a place in `samples/` if it:

- **Runs against a real `moonshine serve` daemon.** Not a mock, not a fake
  transport. If you can't verify it live (real mic or a real WS/gRPC
  connection, real TTS where relevant), it's not ready.
- **Demonstrates something specific**, ideally traceable to a pillar in
  [`../docs/MISSION.md`](../docs/MISSION.md) (control, observability,
  privacy, composability) or a specific integration tier (Tier 0 subscribe,
  Tier 1 external agent, Tier 2 Go extension via `pkg/serveapi`).
- **Is small.** If a sample needs an explanation longer than its own code,
  something is probably wrong with the sample, not the explanation.

## Directory conventions

- One sample = one directory under `samples/`, named `<language>-<what-it-does>`
  (e.g. `go-listen`, `python-agent`, `go-cascade-faq`). This keeps the same
  concept in different languages visually paired in a directory listing.
- Every sample directory has its own `README.md`: what it demonstrates, how
  to run it, and any real rough edges hit while building it (with the `bd`
  issue ID if one was filed).
- Go samples are **their own Go module** (`go.mod`), not part of the root
  `moonshine-go` module. If the sample depends on `pkg/serveapi`, use a
  `replace github.com/ghchinoy/moonshine-go => ../..` directive pointed at
  the local checkout — see `samples/go-cascade-faq/go.mod` for the pattern.
  This matters structurally, not just cosmetically: a sample living inside
  the root module could `import "internal/serve"` by accident (same-module
  code isn't blocked by Go's `internal/` rule) and you'd never notice the
  mistake. A separate module makes that a compile error, the same as it
  would be for a real external consumer.
- Python (or other non-Go) samples get a `requirements.txt` (or equivalent)
  and should have no dependency on any moonshine-go package — they exist to
  prove the wire contract (JSON over WebSocket/gRPC) works from any
  language, not to wrap a Go library.

## The verification bar

Before calling a sample done:

1. It builds/runs cleanly on its own (`go build`, `go vet`, `gofmt -l .` for
   Go; `python3 -m py_compile` at minimum for Python).
2. It has been run **live** against a real `./bin/moonshine serve` process
   — actually connect, actually see transcript events, actually trigger
   whatever action the sample sends, and actually observe the effect (TTS
   audio, a paused session, whatever it claims to do). Mic-loopback test
   harnesses (e.g. macOS `say` piped into a live mic) are fine for
   exercising the wire path even if transcription accuracy is poor in that
   setup — the point is verifying the round trip, not benchmarking STT.
3. If a Go sample claims to be a genuine external `pkg/serveapi` consumer,
   verify `CGO_ENABLED=0 go build ./...` passes in that sample's directory.
4. Real bugs found while doing the above get filed in `bd`, not silently
   worked around in the sample without a trace. Reference the sample's
   README so the workaround (if the sample still needs one) is explained,
   not just present.

## `bd` conventions for this directory

- File an epic per initiative (e.g. "part 2: quickstart consolidation",
  "part 3: browser client"), not one epic per sample.
- One task per sample (or logical group of samples built together).
- Bugs found while building a sample: `bd create ... --deps
  discovered-from:<the epic or task you were working on>`. Don't fold a
  found bug silently into the sample's own close reason without a separate
  issue — someone fixing it later needs a thing to close.
- If a sample needs a change to `moonshine serve` itself (a new flag, a new
  capability) that doesn't exist yet, file that as its own feature request
  against the relevant hosting/serve epic, and mark the sample task
  `blocked-by` it rather than building a workaround that misrepresents what
  the shipped CLI can actually do.

## Git workflow: the `samples` branch

Samples work happens on a long-lived `samples` branch (checked out in its
own worktree, conventionally `~/projects/moonshine-go-samples`, so it never
collides with concurrent work happening in the main `moonshine-go` checkout
— see `../docs/serve-sidecar.md` for why concurrent-edit collisions are a
real, previously-hit problem, not a hypothetical one).

**Set up the worktree once:**

```sh
cd /path/to/moonshine-go
git worktree add -b samples ~/projects/moonshine-go-samples main
```

Point the worktree's `MOONSHINE_LIB_DIR` at the *main* checkout's
`.moonshine/lib` rather than re-fetching/re-building a second copy — it's a
plain directory path, no reason to duplicate 100MB+ of native libs per
worktree:

```sh
export MOONSHINE_LIB_DIR="/path/to/moonshine-go/.moonshine/lib"
```

The downloaded STT/TTS model cache (`~/Library/Caches/moonshine_voice` or
platform equivalent) is already OS-level shared across any checkout — no
action needed there.

**Push regularly**, not just when a batch of work is "done" — the point of
a dedicated branch is that this work is visible on GitHub throughout, the
way a good DevRel workflow should be: others can watch it happen, not just
see a finished result appear.

**Merging back to `main`:**

- Changes that touch **shared files** outside `samples/` (`README.md`,
  anything under `docs/`) land via a pull request. These are real
  collision surfaces with the core agent's own work, and a PR gives both
  sides a concrete diff to review against, not just a hope that nobody
  edited the same paragraph.
- Changes that are **purely additive under `samples/`** (a new sample
  directory, an update to an existing sample's own files) — nothing outside
  `samples/` that could conflict with concurrent work elsewhere — can land
  via a direct fast-forward merge + push once verified, without a PR's
  ceremony.

If in doubt about which category a change falls into, treat it as the PR
case.
