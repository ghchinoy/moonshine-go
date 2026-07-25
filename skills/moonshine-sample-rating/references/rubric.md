# Sample Rating Schema Reference

This reference points to the single source of truth for the `moonshine-go` sample rating schema.

## Schema Source of Truth

Read `samples/CONTRIBUTING.md` under the section **Sample Rating Schema** for the authoritative definitions, allowed values, and markdown template for each rating axis:

- **Tier**: `Tier 0` (subscribe only), `Tier 1` (external IPC agent), `Tier 2` (`pkg/serveapi` Go extension)
- **Complexity**: `1/5` to `5/5` (implementation intricacy)
- **Setup Cost**: `Low` / `Medium` / `High` (infrastructure and dependency requirements)
- **Pillars**: subset of `{Control, Observability, Privacy, Composability}` from `docs/MISSION.md`
- **Industry / Use Case**: application vertical tags (e.g. `Developer Tooling`, `Customer Service / Kiosk`)
- **Appeal**: `1/5` to `5/5` (demoability and developer interest)

Always inspect `samples/CONTRIBUTING.md` before scoring a new sample to ensure compatibility with updated schema definitions.
