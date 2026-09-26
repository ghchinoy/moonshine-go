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
- `MOONSHINE_SMOKE_WAV`: 16kHz mono WAV (e.g. `test-assets/two_cities_16k.wav`) for real-speech tests.
- `MOONSHINE_SMOKE_TTS_ROOT`: voice asset root (downloaded via `moonshine setup --tts <voice>` or `scripts/fetch-voice-assets.sh tts`).
- `MOONSHINE_SMOKE_EMBEDDING=1`: opt-in flag to run the Gemma-300M embedding download, inference, and semantic matching smoke tests (~300MB download on first run).

A moonshine checkout ships several files as Git LFS pointers needed to build `libmoonshine` (embedded C++ sources) and run it (vendored onnxruntime binaries). If `scripts/build-libmoonshine.sh` fails with compiler errors mentioning `git-lfs.github.com`, run `git lfs pull` in that checkout.

## Agent Personae & Progressive Disclosure

Two dedicated agent personae coordinate development in this repository. Detailed operational checklists, gotchas, and architectural invariants are split into focused guides:

| Persona | Primary Ownership | Progressive Disclosure Guide |
|---|---|---|
| **Core Agent (`moonshine-go-core`)** | `pkg/moonshine`, `cmd/moonshine`, `internal/`, `bench/`, `BENCHMARKS.md`, `Makefile`, releases, upstream sync | **[agents/core.md](agents/core.md)** |
| **DevRel Agent (`moonshine-go-dev`)** | `samples/`, documentation site (`site/`), tutorials, user-facing documentation (`docs/`, `README.md`, CLI help wording) | **[agents/devrel.md](agents/devrel.md)** |

> **Trigger Rule for Coding Agents:**
> - **Before reading or modifying `pkg/moonshine/`, `cmd/moonshine/`, `internal/`, `bench/`, or release tooling:** Read **[agents/core.md](agents/core.md)** for C ABI rules, `resolveG2PRoot()` precedence, streaming TTS cancellation gotchas, upstream synchronization routines, and semver release guidelines.
> - **Before reading or modifying `samples/`, `site/`, or documentation:** Read **[agents/devrel.md](agents/devrel.md)** for Starlight/Astro sync rules, heading anchor invariants, MDX prose escaping, GCS immutable media storage, and sample rating tables.

## Critical Cross-Cutting Rules (All Agents)

- **All Numbers in Documentation Must Be Measured, Never Typed:** Any metric, latency number, speedup ratio, or benchmark score displayed in documentation (`docs/`, `README.md`, `BENCHMARKS.md`) must be parsed directly from the benchmark or synthesis run that produced the output (e.g. via `site/scripts/gen-audio.sh` or benchmark test logs), never hardcoded or estimated. If a diagram is conceptual, explicitly label it `(Illustrative)`.
- **Wire Contract Changes Break Downstream Samples:** When modifying `internal/serve` event payloads or emission sequences (e.g. streaming TTS changing `TTSAudioEvent` from 1 chunk to N chunks), immediately audit `samples/` for consumers (such as `browser-cascade-faq/app.js`) that assume legacy sequence shapes, and file follow-up issues.
- **Exhaustive Documentation Audit on Breaking UX Changes:** When a command's fundamental behavior changes (e.g. `setup --tts` automating voice downloads), grep `docs/`, `README.md`, `samples/*/README.md`, and `cmd/moonshine` help text to purge obsolete instructions (e.g. manual Git LFS pulls).

## Multi-Agent Coordination & Git Safety

Multiple agents (and the human maintainer) commit to `main` concurrently and continuously.

- **Active Agent Profile (Team-Maintainer):** Agents operate under the **Team-maintainer profile**: agents may run quality gates, close beads, commit, and push directly to `main` for purely additive changes in owned areas (`samples/`, `agents/`, and `.beads/` tracker exports); shared files (`README.md`, `docs/`, `AGENTS.md`) must land via reviewable PR unless explicitly authorized by the user for that specific task.
- **Always Fetch Immediately Before Push:** Run `git fetch origin <branch>` *immediately* beforehand and verify `git merge-base --is-ancestor origin/<branch> HEAD`. Never rely on a fetch from earlier in the session.
- **Dedicated Worktrees for Parallel Work:** Use a dedicated worktree (`git worktree add -b <branch> <path> main`) for substantial initiatives to avoid concurrent-uncommitted-edit collisions.
- **Untracked File Collisions on Pull/Rebase:** If `git pull --ff-only` or `git rebase` aborts on an untracked file, diff it against the incoming commit (`git show origin/main:<path>`). If identical or stale, remove it (`rm -rf <path>`) and retry the pull.
- **Historical Reference:** Skim `docs/serve-sidecar.md` for historical context on the original sidecar build and invariants (backpressure, idempotency, barge-in).

## Issue Tracking with bd (beads)

This repository uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs or external trackers. Run `bd prime` for complete AI-optimized workflow context and operational commands.

**Quick Reference:**
```bash
bd prime                              # Load complete workflow context (SSOT)
bd ready                              # Show issues ready to work (no blockers)
bd list --status=open                 # List open issues
bd create "title" -t task -p 2        # Create new issue
bd update <id> --claim                # Claim work atomically
bd close <id> --reason "Done"         # Mark complete
```

**Repository Gotchas:**
- **Pre-commit hook auto-export gotcha:** The git hook auto-re-exports `.beads/issues.jsonl` upon commit (`export.auto: true`). If attempting `git rebase` / `git pull --rebase` immediately after committing, stage the auto-exported `.beads/issues.jsonl` and amend (`git add .beads/issues.jsonl && git commit --amend --no-edit`) before running the rebase.
- **`bd close --force` for Role Mismatches:** The `bd` tracker requires `--force` when your active actor identity differs from the issue's assignee.
- **Link Discovered Work:** Always link discovered bugs or tasks with `discovered-from:<parent-id>`.
