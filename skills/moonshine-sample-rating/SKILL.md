---
name: moonshine-sample-rating
description: Scores a moonshine-go sample against the Tier, Complexity, Setup Cost, Pillars, Industry, and Appeal rating schema and produces the Sample Rating markdown block for its README. Use when creating a new sample under samples/ or updating an existing sample's rating.
compatibility: Requires a moonshine-go repository checkout containing samples/CONTRIBUTING.md.
---

# Scoring a moonshine-go sample

This skill assists contributors and agents in evaluating a sample under `samples/` against the multi-axis rating schema defined in `samples/CONTRIBUTING.md`.

Follow these step-by-step instructions whenever you create or review a sample README.

## 1. Inspect the sample codebase

Examine the target sample directory under `samples/`:
- Read `main.go` / `agent.py` / `index.html` to understand the code structure and line count.
- Check dependencies in `go.mod` or `requirements.txt`.
- Identify how the sample connects to `moonshine serve` (WebSocket, gRPC, or CLI).

## 2. Review the rating schema

Read `samples/CONTRIBUTING.md` (and [references/rubric.md](references/rubric.md)) for the latest schema definitions:

| Axis | Format | Criteria |
|---|---|---|
| **Tier** | `Tier 0` / `Tier 1` / `Tier 2` | `Tier 0`: subscribe only; `Tier 1`: external IPC agent; `Tier 2`: `pkg/serveapi` Go extension. |
| **Complexity** | `1/5` to `5/5` | `1/5`: ~40 lines zero-SDK socket read; `3/5`: composite fast-path + RAG; `5/5`: multi-tool LLM loop. |
| **Setup Cost** | `Low` / `Medium` / `High` | `Low`: plain socket client; `Medium`: local mic/tmux/browser; `High`: external service or API key required. |
| **Pillars** | List from `MISSION.md` | `Control`, `Observability`, `Privacy`, `Composability`. |
| **Industry / Use Case** | Freeform tags | Primary application domain (e.g. `Developer Tooling`, `Healthcare`, `Customer Service`). |
| **Appeal** | `1/5` to `5/5` | Demoability and developer interest (1 = internal test utility; 5 = flagship showcase). |

## 3. Evaluate each axis

Score each axis based on the evidence in the sample:

1. **Tier**: If the sample uses `pkg/serveapi` types (`AgentRunner`, `CompositeHandler`), it is `Tier 2`. If it sends action JSON over WebSocket without Go types, it is `Tier 1`. If it only receives transcript frames, it is `Tier 0`.
2. **Complexity**: Count lines of code and logic branching.
3. **Setup Cost**: Note external prerequisites (e.g. API keys, browser audio permissions, tmux session).
4. **Pillars**: Match demonstrated features to `MISSION.md` pillars.
5. **Industry**: Identify the target real-world vertical.
6. **Appeal**: Rate overall demo impact.

## 4. Format the Markdown block

Generate the exact Markdown block to paste into the sample's `README.md` (placed immediately after the main header):

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

## 5. Update sample indexes

Once the `README.md` is updated:
1. Paste the row into `samples/GUIDE.md`'s **Sample Catalog Rating Matrix**.
2. Verify `samples/CONTRIBUTING.md` verification bar item 5 passes.
