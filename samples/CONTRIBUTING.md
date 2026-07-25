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
  to run it, and any *currently active* workaround it needs (with the `bd`
  issue ID if one was filed). Document workarounds only while they're still
  needed: once the referenced bug is fixed upstream, remove that note from
  the sample's own README in the same change (or a prompt follow-up) that
  drops the workaround code — `bd`'s close reason and git history are the
  changelog, not the sample's docs. A sample README should teach the
  pattern it demonstrates, not accumulate a list of bugs that no longer
  exist.
- Go samples are **their own Go module** (`go.mod`), not part of the root
  `moonshine-go` module. If the sample depends on `pkg/serveapi`, `pkg/servepb`,
  or `pkg/moonshine`, use a `replace github.com/ghchinoy/moonshine-go => ../..`
  directive pointed at the local checkout — see `samples/go-cascade-faq/go.mod`
  for the pattern. This matters structurally, not just cosmetically: a sample
  living inside the root module could `import "internal/serve"` by accident (same-module
  code isn't blocked by Go's `internal/` rule) and you'd never notice the
  mistake. A separate module makes importing `internal/*` packages a compile
  error for samples, the same as it would be for a real external consumer, while
  allowing imports of public packages (`pkg/serveapi`, `pkg/servepb`, `pkg/moonshine`).
- Python (or other non-Go) samples get a `requirements.txt` (or equivalent)
  and should have no dependency on any moonshine-go package — they exist to
  prove the wire contract (JSON over WebSocket/gRPC) works from any
  language, not to wrap a Go library.

### Sample Rating Schema

Every sample must include a **Sample Rating** table near the top of its own `README.md`. This self-reported rating makes sample complexity, setup cost, and capabilities transparent before a developer runs it:

| Axis | Format / Values | Description |
|---|---|---|
| **Tier** | `Tier 0` / `Tier 1` / `Tier 2` / `Native / in-process` | `Tier 0`: subscribe only; `Tier 1`: external IPC agent; `Tier 2`: `pkg/serveapi` Go extension; `Native / in-process`: direct `pkg/moonshine` C-API binding without a daemon. |
| **Complexity** | `1/5` to `5/5` | Implementation intricacy (1 = ~40 lines zero-SDK; 5 = full multi-tool LLM loop). |
| **Setup Cost** | `Low` / `Medium` / `High` | `Low`: plain socket client; `Medium`: local mic/tmux/browser/libmoonshine; `High`: multi-service or API key required. |
| **Pillars** | `Control`, `Observability`, `Privacy`, `Composability` | Subset of core pillars from `MISSION.md` demonstrated. |
| **Industry / Use Case** | Freeform tags | Primary application vertical (e.g. `Developer Tooling`, `Customer Service / Kiosk`). |
| **Appeal** | `1/5` to `5/5` | Demoability and developer appeal (1 = utility test; 5 = standalone showcase). |

#### Markdown Template for Sample READMEs

```markdown
## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Tier 2 (Go extension via `pkg/serveapi`) |
| **Complexity** | 3/5 |
| **Setup Cost** | Medium (requires local mic + moonshine serve) |
| **Pillars** | Control, Observability, Privacy, Composability |
| **Industry / Use Case** | Developer Tooling, Offline Voice Agent |
| **Appeal** | 4/5 |
```

## The verification bar

Before calling a sample done:

1. It builds/runs cleanly on its own (`go build`, `go vet`, `gofmt -l .` for
   Go; `python3 -m py_compile` at minimum for Python).
2. It has been run **live**:
   - For daemon IPC samples (Tiers 0–2): run against a real `./bin/moonshine serve` process — actually connect, receive transcript events, trigger actions, and observe effects.
   - For native-embedding samples (`pkg/moonshine`): run against a real `libmoonshine` shared library and downloaded model — load transcriber, process audio, and verify output lines and stats.
3. If a Go sample claims to be a genuine external `pkg/serveapi` or `pkg/moonshine` consumer,
   verify `CGO_ENABLED=0 go build ./...` passes in that sample's directory.
4. Real bugs found while doing the above get filed in `bd`, not silently
   worked around in the sample without a trace. Reference the sample's
   README so the workaround (if the sample still needs one) is explained,
   not just present.
5. **Self-reported rating included**: verify the sample's `README.md` includes
   the **Sample Rating** table formatted per the template under
   [Directory conventions](#directory-conventions).

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
