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

`make smoke` additionally honors:
- `MOONSHINE_SMOKE_WAV`: a 16kHz mono wav (e.g. moonshine's own `test-assets/two_cities_16k.wav`) for real-speech non-streaming and streaming transcription tests.
- `MOONSHINE_SMOKE_TTS_ROOT`: a `core/moonshine-tts/data`-shaped directory with voice assets (downloaded via `moonshine setup --tts <voice>` or `scripts/fetch-voice-assets.sh tts` in an upstream checkout) for TTS synthesis and streaming tests. Note: upstream no longer hosts voice models in Git LFS.
- `MOONSHINE_SMOKE_EMBEDDING=1`: opt-in flag to run the Gemma-300M text embedding model download, inference, and semantic matching smoke tests (downloads ~300MB on first run).

A moonshine checkout ships several files as Git LFS pointers that aren't
needed for the normal `moonshine-voice` app but ARE needed to build
`libmoonshine` (embedded C++ sources) and to run it (vendored onnxruntime
binaries). If `scripts/build-libmoonshine.sh` fails with compiler errors
mentioning `git-lfs.github.com`, run `git lfs pull` in that checkout (see
README.md's "Build libmoonshine" section for exactly which paths matter if
you want to avoid pulling the entire LFS payload).

## Agent Personae & Ownership Separation

Two dedicated agent personae coordinate development in this repository:

- **Core Agent (`moonshine-go-core`)**:
  - **Ownership:** `pkg/moonshine` (purego C bindings), `cmd/moonshine` (flagship CLI), `internal/` (serve sidecar daemon, session orchestration, audio capture/playback, TUI), build/smoke tooling (`Makefile`, `scripts/`), performance benchmarks (`bench/`, `BENCHMARKS.md`), official releases (`CHANGELOG.md`, `docs/RELEASING.md`), and upstream synchronization with `~/projects/github/moonshine`.
  - **Responsibilities:** Maintains `main` branch stability, reviews DevRel pull requests, verifies C ABI parity, and tags semver releases.
- **DevRel Agent (`moonshine-go-dev`)**:
  - **Ownership:** `samples/` (runnable Tier 0/1/2 reference applications, sample READMEs, `samples/CONTRIBUTING.md`, `samples/GUIDE.md`, `samples/TUTORIAL.md`), documentation website (`site/`), tutorial and onboarding guides (`docs/quickstart.md`, `docs/troubleshooting.md`, `docs/MISSION.md`), and developer UX workflows.
  - **Responsibilities:** Delivers clean developer onboarding, verifies samples against live `moonshine serve` processes, and submits shared doc/core updates via reviewable pull requests.

## Architecture notes for future changes

- `pkg/moonshine` is purego-based (no cgo to *build*) and must stay that
  way -- it's the whole point of this project over reimplementing the model
  pipeline. `internal/audio`'s mic capture (`gen2brain/malgo`) is a
  deliberate, separate exception that does require cgo.
- C struct layouts in `pkg/moonshine/ctypes.go` are hand-mirrored from
  `moonshine-c-api.h` with explicit padding. If that header's structs change
  upstream, re-verify offsets (see the throwaway `offsetof`/`sizeof` C
  program used during initial development, not checked in -- rewrite it
  against the new header if needed) before touching `ctypes.go`.
- STT model downloads are namespaced per (language, arch) under
  `GroupDir()`/`PrimaryModelDir()` in `pkg/moonshine/download.go`
  precisely because different models share filenames
  (`encoder_model.ort`, etc.) -- don't "simplify" this back to a flat
  directory.
- `internal/serve` implements the `moonshine serve` agentic voice sidecar:
  - **Hub/Dispatcher/Transport/Agent Separation:** Hub handles live event fan-out (`session.Update` -> `TranscriptEvent`); Dispatcher routes inbound actions (`speak`, `display`, `session.pause/resume/stop`, `run_command`); Transport Manager merges WebSocket (`ws.go`) and gRPC (`grpc.go` / `serve.proto`) connections; Agent layer handles fast-path intent matching (`intent.go`) and Gemini LLM function-calling (`gemini.go`).
  - **Backpressure & Idempotency:** Hub uses drop-oldest for interim updates, but guarantees every finalized line (`Line.ID`) is delivered to subscribers/agents exactly once.
  - **Barge-in Guard:** `TTSSpeaker` exposes `Speaking()`, which mutes microphone input during TTS playback to prevent the sidecar from transcribing its own voice output.
  - **Native-Free Unit Tests:** All logic in `internal/serve` must be testable with fakes (`fakeLLMClient`, `fakeTransport`, `fakeSpeaker`) without requiring `libmoonshine` or network calls.
  - **Daemon assembly is importable** as `internal/serve.Server`/`ServerConfig` (`server.go`), extracted from `cmd/moonshine/serve.go` so code *inside this module* can embed the daemon with a custom `AgentHandler`/`AudioSource`. `cmd/moonshine/serve.go` itself is reduced to flag parsing + calling it.
- `pkg/serveapi` is the **public, Go-native extension surface** for `moonshine serve`: `AgentHandler`, `Retriever`, `LLMClient`, `AudioSource`, and shadow structs for every wire type (`Line`, `TranscriptEvent`, `ActionRequest`, ...). It's a leaf package -- stdlib only, `CGO_ENABLED=0`-buildable, never imports `internal/session` or `internal/audio` -- so external Go modules can build against it without a C toolchain. `internal/serve` consumes it as the source of truth for these types (no duplicated definitions). It does **not** yet expose a public daemon-embedding wrapper (only `internal/serve.Server` does, which is `internal/`-only); a true external module can drive the sidecar today only as a separate process talking `pkg/serveapi` over WS/gRPC -- see `samples/go-cascade-faq` for that shape.
- `samples/` holds runnable, live-verified Tier 0/1/2 examples against `moonshine serve` (Go and Python), replacing what used to be a docs-only quickstart. See [samples/CONTRIBUTING.md](samples/CONTRIBUTING.md) for conventions before adding one -- the short version: a sample isn't done until it's been run against a real `moonshine serve` process, not just compiled.
- **TTS G2P Root Precedence (`resolveG2PRoot()`):** Never read `viper.GetString("tts.g2p_root")` directly. Always call `resolveG2PRoot()` in `cmd/moonshine/lib.go`. It enforces the strict 4-tier precedence: (1) explicit `--g2p-root` CLI flag; (2) explicit `tts.g2p_root` in `config.yaml`; (3) `<moonshine.src_dir>/core/moonshine-tts/data`; (4) downloaded cache `<model.dir>/download.moonshine.ai/tts`. `moonshine setup --tts` intentionally never mutates `config.yaml` so higher-precedence overrides remain active.
- **Embedding Model Vector Deallocation (`pkg/moonshine.EmbeddingModel`):** Memory allocated by `moonshine_calculate_embedding` must be freed with `moonshine_free_embedding` (C `std::free`), NOT `moonshine_free_buffer`. `EmbeddingModel` directly satisfies `agentflow.EmbeddingBackend`.
- **Streaming TTS Barge-In State (`TTSStream.Cancel()`):** Calling `stream.Cancel()` cancels active C++ computation, and the immediate subsequent call to `stream.NextChunk()` returns sentinel `ErrCancelled` once. This error must be consumed before pushing a new utterance to reset the synthesizer to idle.
- **Concurrent Stream VAD Isolation Limitation (Upstream #229):** All transcription streams in a process share a single static `SileroVad*` pointer in C++ (`VoiceActivityDetector::silero_vad`). Because Silero VAD carries recurrent RNN state (`_state`, `_context`), simultaneous live streams in a single `moonshine serve` process will corrupt each other's speech detection boundaries. For multi-channel live audio, deploy separate `moonshine serve` worker processes (process-level isolation).

## Documentation, site, and media conventions

The project documentation is deployed as a static site to GitHub Pages at [https://ghchinoy.github.io/moonshine-go/](https://ghchinoy.github.io/moonshine-go/) via `.github/workflows/pages.yml`.

- **Single Source of Truth (Never edit `site/src/content/docs/`):**
  The documentation site (`site/`) is built using **Astro 7** and **Starlight**. The content collection directory `site/src/content/docs/` is **gitignored** and auto-generated by `site/scripts/sync-content.mjs` during `npm run build` or `make site-build`. **Never edit or create files in `site/src/content/docs/` directly.** Always edit the authoritative source markdown in `docs/*.md`, `samples/*/README.md`, `README.md`, `BENCHMARKS.md`, or `CHANGELOG.md`.
- **Diagrams (Mermaid vs ASCII vs Graphviz):**
  - **Mermaid (` ```mermaid `):** Standard for sequence diagrams, component data flows, and architecture charts across all source markdown. Renders natively on GitHub and as interactive SVGs on Starlight.
  - **ASCII / Plain text (` ```text `):** Reserved for filesystem directory trees (e.g., macOS `.app` bundle structure in `bundling-libmoonshine.md`) and verbatim CLI terminal sessions.
  - **Graphviz WebP:** For dense architectural state machines where Graphviz's layout engine is required (e.g., VAD gate state machine), keep source `.dot` in `docs/proposals/images/` and host the rendered WebP in GCS.
- **Binary Media & Storage Policy (Zero Git Bloat):**
  - **Do not commit large binary media (WAV, MP3, PNG, GIF) to git.**
  - All public media assets (TTS voice comparison clips, streaming TTFA benchmarks, STT demos, raster hero demo GIFs) are hosted in the public Google Cloud Storage bucket:
    `gs://moonshine-ports-site-assets/moonshine-go/` (HTTPS URL: `https://storage.googleapis.com/moonshine-ports-site-assets/moonshine-go/...`).
  - The bucket is configured with `allUsers: objectViewer`, 1-year immutable caching (`public, max-age=31536000, immutable`), and CORS origins for `https://ghchinoy.github.io`. Note: This bucket is shared with `moonshine-rs` (under `moonshine-rs/`).
  - Audio clips are generated locally via `site/scripts/gen-audio.sh` and tracked in `site/src/data/audio-manifest.json`.
- **Link Rewriting & Build-Time Validation:**
  - `site/scripts/sync-content.mjs` validates all relative markdown links at build time. Internal docs/samples resolve to clean Starlight URLs (`/moonshine-go/guides/...`, `/moonshine-go/samples/...`). Code files (`.go`, `.py`, `.js`, configs) resolve to GitHub blob URLs. Broken local links trigger fatal build failures (`errors++`), ensuring broken references never reach production.
- **Sample Rating Tables:**
  - Every sample under `samples/` must include a self-reported `Sample Rating` table formatted per `samples/CONTRIBUTING.md`. The sync script parses these tables into `site/src/data/samples.json` to generate the interactive catalog at `/moonshine-go/samples/`.
- **Useful Make Targets:**
  - `make site-sync` — Runs the content synchronization script.
  - `make site-build` — Syncs content and builds the static Starlight site into `site/dist/`.
  - `make site-dev` — Runs the local Astro development server.

## Operational lessons and gotchas for coding agents

Reflections and hard-earned patterns discovered across past coding sessions in this repository:

- **MDX-Safe Markdown Prose:**
  The documentation site builds `README.md` and `BENCHMARKS.md` as `.mdx` files to embed live audio players and SVG charts. In MDX, bare `<` signs (e.g. `< 2,000ms`), LaTeX-style curly braces (e.g. `\text{s}`), and unescaped brackets are parsed as JSX expressions and cause build-time syntax errors. Always write plain prose (e.g., "is under 2,000ms") or wrap expressions inside backticks.
- **Component Injection Heading Anchors:**
  `site/scripts/sync-content.mjs` injects Astro components (such as `<AudioShowcase />` and `<BenchmarkCharts />`) using heading text matching (e.g., `## Contents`, `## 2. In-Process Micro-Benchmarks`). If you rename or edit these headings in the source markdown, update the corresponding string literals in `site/scripts/sync-content.mjs` in the same commit. The sync script asserts anchor existence and will fail the build if an anchor is missing.
- **Never Use Ephemeral `github.com/user-attachments/...` URLs:**
  GitHub user-attachment URLs can expire or return 404 outside GitHub issue comments. Always host public images, GIFs, and media in `gs://moonshine-ports-site-assets/moonshine-go/` with 1-year immutable caching.
- **All Numbers in Documentation Must Be Measured, Never Typed:**
  Any metric, latency number, speedup ratio, or benchmark score displayed in documentation (`docs/`, `README.md`, `BENCHMARKS.md`) must be parsed directly from the benchmark or synthesis run that produced the output (e.g. via `site/scripts/gen-audio.sh` or benchmark test logs), never hardcoded or estimated. If a diagram is conceptual, explicitly label it `(Illustrative)`.
- **Assert Real Failure Symptoms in Concurrency Tests:**
  When authoring concurrency tests, do not only assert that transcripts don't interleave words; assert line counts and finalization latency against a solo baseline (e.g. `TestConcurrentStreamVADCorruption`). Upstream bugs often manifest as dropped lines or latency inflation rather than word corruption.
- **`make bench` Excludes Expected Regression Probes:**
  `make bench` passes `-run '^$'` to run exclusively `Benchmark*` functions and skip regression probe tests that intentionally fail on current upstream library pins until upstream fixes land.
- **Wire Contract Changes Break Downstream Samples:**
  When modifying `internal/serve` event payloads or emission sequences (e.g. streaming TTS changing `TTSAudioEvent` from 1 chunk to N chunks), immediately audit `samples/` for consumers (such as `browser-cascade-faq/app.js`) that assume legacy sequence shapes, and file follow-up issues.
- **Exhaustive Documentation Audit on Breaking UX Changes:**
  When a command's fundamental behavior changes (e.g. `setup --tts` automating voice downloads), grep `docs/`, `README.md`, `samples/*/README.md`, and `cmd/moonshine` help text to purge obsolete instructions (e.g. manual Git LFS pulls).
- **Pre-Planning Dependency Checks:**
  Before proposing plans or choosing toolchains, verify currently installed versions (`node -v`, `npm view <pkg> version`, `go version`). For example, verify Starlight peer dependencies before assuming older Astro majors.
- **Worktree Synchronization via Git (No File Copying):**
  When working across multiple worktrees (e.g. `moonshine-go` and `moonshine-go-samples`), synchronize branches exclusively via git (`git fetch`, `git pull --ff-only`, `git checkout`). Never copy files or run `rsync` between worktrees; doing so copies build artifacts (`node_modules`, binaries) and dirties worktree tracking.
- **`bd close --force` for Role Mismatches:**
  The `bd` issue tracker requires `--force` when your active actor identity differs from the issue's assignee.
- **Explicit User Approval for Direct Pushes:**
  While purely additive files in owned sample subdirectories can fast-forward push once verified, shared files (`README.md`, `docs/`, `AGENTS.md`) require a reviewable PR unless the user explicitly authorizes a direct push for that specific task.

## Multi-agent coordination in this repo

Multiple agents (and the human maintainer) commit to `main` concurrently and
somewhat continuously -- this is a normal, expected working mode here, not
an edge case. `main` can move between the start and end of your session,
sometimes within seconds of you checking it. Releases are tagged and
published intentionally following [docs/RELEASING.md](docs/RELEASING.md)
(semver minor `v0.X.0` for new subsystems/features, patch `v0.X.Y` for bug
fixes; pushing a `v*` tag triggers `.github/workflows/release.yml`). Don't
assume the latest tag or commit you saw earlier is still the tip of `main`.

**Practical consequences:**

- **Upstream Synchronization Routine:**
  When evaluating upstream changes in `~/projects/github/moonshine`:
  1. Inspect `upstream/main` and active `upstream/dev-v*` branches (`git fetch upstream --tags`). Ignore benign "would clobber" warnings on historical tags.
  2. Check C API header diff: `git diff <current_tag>..upstream/dev-v* -- core/moonshine-c-api.h`. If empty, zero Go binding changes are needed.
  3. Inspect active upstream PRs and issues for known crashes, races, or concurrency bugs.
  4. Only bump `MOONSHINE_RELEASE_TAG` on tagged releases, validating prebuilts with `scripts/check-release-asset.sh`.
  5. Probe C API capabilities empirically with a minimal script rather than assuming older documentation is accurate.

- **Before any push that could move a shared branch (`main` especially),
  `git fetch origin <branch>` *immediately* beforehand and check the
  ancestor relationship against that fresh fetch, not a ref you fetched
  earlier in the session.** A locally-cached `origin/main` that's gone
  stale between your check and your push can make a genuinely unsafe push
  look like a safe fast-forward -- this has actually happened (see
  `moonshine-go-q7j`, self-caught and fixed the same session, but it did
  briefly drop a released commit off `main`). If you're not certain,
  `git push` (a plain branch push, which git itself refuses on a
  non-fast-forward) is safer than a `git push origin <ref>:<branch>` form
  that can silently succeed on stale data.
- **Use a dedicated worktree for any substantial parallel initiative**
  (`git worktree add -b <branch> <path> main`), rather than working
  directly in the same checkout another agent is actively committing to.
  This doesn't eliminate merge-time conflicts, but it eliminates the
  *concurrent-uncommitted-edit* class of collision, which is the more
  dangerous one (silent corruption vs. a visible merge conflict). See
  `samples/CONTRIBUTING.md`'s "Git workflow" section for a worked example.
- **Shared files (`README.md`, anything under `docs/`) land via PR; changes
  additive-only to a clearly-owned area (e.g. new files under `samples/`)
  can land via a direct fast-forward push once verified.** This split was
  agreed as the general convention for this repo, not just a one-off rule
  -- shared files are the real collision surface and benefit from a visible
  diff to review against; purely additive work in an owned area doesn't
  need that ceremony.
- **Active Agent Profile (Team-Maintainer):** Agents operate under the **Team-maintainer profile** within the boundaries defined above: agents may run quality gates, close beads, commit, and push directly to `main` for purely additive changes in owned areas and `.beads/` tracker exports; shared files (`README.md`, `docs/`, `AGENTS.md`) must land via reviewable PR unless explicitly authorized by the user for that specific task.
- **Untracked file collisions during `git pull` / `git rebase`**: A `git pull --ff-only` or `git rebase` can abort if an untracked file or directory in your working tree collides with an incoming path from another agent's merged commit. Before assuming a git merge conflict, `diff` the untracked path against the incoming commit (`git show origin/main:<path>`). If identical or stale, safe to remove (`rm -rf <path>`) and retry the pull.
- Before starting `internal/serve` work specifically, skim
  [docs/serve-sidecar.md](docs/serve-sidecar.md) -- it's largely a
  historical record of the original two-track parallel build (epic
  `moonshine-go-6nb`, now functionally complete) plus the invariants
  (backpressure, idempotency, barge-in) every change to that package must
  still honor. It's slated to be distilled into a shorter developer-facing
  doc and retired (`moonshine-go-bhx`); don't be surprised if it looks
  different by the time you read it.


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

**Pre-commit hook auto-export gotcha:** This repository's `bd` git hook (`core.hooksPath` → `.beads/hooks/prepare-commit-msg`) auto-re-exports `.beads/issues.jsonl` whenever a git commit is made (`export.auto: true`). If you commit and immediately attempt `git rebase` / `git pull --rebase`, git will reject it with *"cannot rebase: you have unstaged changes"* because `.beads/issues.jsonl` was re-dirtied during the commit itself. To proceed cleanly, stage the auto-exported `.beads/issues.jsonl` and amend (`git add .beads/issues.jsonl && git commit --amend --no-edit`) before running the rebase.

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
