# AGENTS.md

Operational context for coding agents working in this repo. End-user/CLI
docs live in [README.md](README.md).

## Building and verifying

```sh
make buildlib MOONSHINE_SRC=~/projects/github/moonshine   # one-time native build
make build                                                 # go build -> bin/moonshine
make test                                                  # go test ./... (no native deps)
make smoke                                                 # exercises a real libmoonshine.dylib/.so
```

`make smoke` additionally honors `MOONSHINE_SMOKE_WAV` (a 16kHz mono wav,
e.g. moonshine's own `test-assets/two_cities_16k.wav`) and
`MOONSHINE_SMOKE_TTS_ROOT` (a `core/moonshine-tts/data`-shaped directory with
at least one Piper voice's `.onnx`/`.onnx.json` pulled via `git lfs pull`) to
run real-speech and TTS smoke tests, not just the always-on silence
round-trip.

A moonshine checkout ships several files as Git LFS pointers that aren't
needed for the normal `moonshine-voice` app but ARE needed to build
`libmoonshine` (embedded C++ sources) and to run it (vendored onnxruntime
binaries, TTS voice assets). If `scripts/build-libmoonshine.sh` fails with
compiler errors mentioning `git-lfs.github.com`, run `git lfs pull` in that
checkout (see README.md's "Build libmoonshine" section for exactly which
paths matter if you want to avoid pulling the entire LFS payload).

## Architecture notes for future changes

- `internal/moonshine` is purego-based (no cgo to *build*) and must stay that
  way -- it's the whole point of this project over reimplementing the model
  pipeline. `internal/audio`'s mic capture (`gen2brain/malgo`) is a
  deliberate, separate exception that does require cgo.
- C struct layouts in `internal/moonshine/ctypes.go` are hand-mirrored from
  `moonshine-c-api.h` with explicit padding. If that header's structs change
  upstream, re-verify offsets (see the throwaway `offsetof`/`sizeof` C
  program used during initial development, not checked in -- rewrite it
  against the new header if needed) before touching `ctypes.go`.
- STT model downloads are namespaced per (language, arch) under
  `GroupDir()`/`PrimaryModelDir()` in `internal/moonshine/download.go`
  precisely because different models share filenames
  (`encoder_model.ort`, etc.) -- don't "simplify" this back to a flat
  directory.
- `internal/serve` implements the `moonshine serve` agentic voice sidecar:
  - **Hub/Dispatcher/Transport/Agent Separation:** Hub handles live event fan-out (`session.Update` -> `TranscriptEvent`); Dispatcher routes inbound actions (`speak`, `display`, `session.pause/resume/stop`, `run_command`); Transport Manager merges WebSocket (`ws.go`) and gRPC (`grpc.go` / `serve.proto`) connections; Agent layer handles fast-path intent matching (`intent.go`) and Gemini LLM function-calling (`gemini.go`).
  - **Backpressure & Idempotency:** Hub uses drop-oldest for interim updates, but guarantees every finalized line (`Line.ID`) is delivered to subscribers/agents exactly once.
  - **Barge-in Guard:** `TTSSpeaker` exposes `Speaking()`, which mutes microphone input during TTS playback to prevent the sidecar from transcribing its own voice output.
  - **Native-Free Unit Tests:** All logic in `internal/serve` must be testable with fakes (`fakeLLMClient`, `fakeTransport`, `fakeSpeaker`) without requiring `libmoonshine` or network calls.

## Active multi-agent work: `moonshine serve` (agentic voice sidecar)

There is an in-progress feature (bd epic `moonshine-go-6nb`) that adds a
`moonshine serve` daemon streaming live transcripts over IPC (WebSocket +
gRPC) with a built-in Gemini agent. It is designed to be built by **two
agents in parallel**.

**Before starting any `serve` work, read
[docs/serve-sidecar.md](docs/serve-sidecar.md).** It defines the interaction
pattern, the one-file-per-task ownership map (so two agents never edit the
same file), the two coordination points (`go.mod` and one line in
`root.go`), and the backpressure/idempotency/barge-in contracts every task
must honor. `bd ready` + the dependency graph enforce the ordering; the doc
explains *why* each edge exists.


<!-- headroom:rtk-instructions -->
# RTK (Rust Token Killer) - Token-Optimized Commands

When running shell commands, **always prefix with `rtk`**. This reduces context
usage by 60-90% with zero behavior change. If rtk has no filter for a command,
it passes through unchanged — so it is always safe to use.

## Key Commands
```bash
# Git (59-80% savings)
rtk git status          rtk git diff            rtk git log

# Files & Search (60-75% savings)
rtk ls <path>           rtk read <file>         rtk grep <pattern>
rtk find <pattern>      rtk diff <file>

# Test (90-99% savings) — shows failures only
rtk pytest tests/       rtk cargo test          rtk test <cmd>

# Build & Lint (80-90% savings) — shows errors only
rtk tsc                 rtk lint                rtk cargo build
rtk prettier --check    rtk mypy                rtk ruff check

# Analysis (70-90% savings)
rtk err <cmd>           rtk log <file>          rtk json <file>
rtk summary <cmd>       rtk deps                rtk env

# GitHub (26-87% savings)
rtk gh pr view <n>      rtk gh run list         rtk gh issue list

# Infrastructure (85% savings)
rtk docker ps           rtk kubectl get         rtk docker logs <c>

# Package managers (70-90% savings)
rtk pip list            rtk pnpm install        rtk npm run <script>
```

## Rules
- In command chains, prefix each segment: `rtk git add . && rtk git commit -m "msg"`
- For debugging, use raw command without rtk prefix
- `rtk proxy <cmd>` runs command without filtering but tracks usage
<!-- /headroom:rtk-instructions -->


<!-- BEGIN BEADS INTEGRATION v:1 profile:full hash:f2c52d34 -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Dolt-powered version control with native sync
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update <id> --claim --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task atomically**: `bd update <id> --claim`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Quality
- Use `--acceptance` and `--design` fields when creating issues
- Use `--validate` to check description completeness

### Lifecycle
- `bd defer <id>` / `bd supersede <id>` for issue management
- `bd stale` / `bd orphans` / `bd lint` for hygiene
- `bd human <id>` to flag for human decisions
- `bd formula list` / `bd mol pour <name>` for structured workflows

### Sync

bd stores issue history in Dolt:

- Each write auto-commits to Dolt history
- Do not treat `.beads/issues.jsonl` as the sync protocol

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.

<!-- END BEADS INTEGRATION -->
