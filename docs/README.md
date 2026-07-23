# docs

Findings, best practices, and answers to questions that came up while
building and using moonshine-go, beyond what's in the top-level
[README.md](../README.md).

- [user-guide.md](user-guide.md) -- full command/flag reference and worked
  examples for every command, plus troubleshooting.
- [quickstart-voice-agent.md](quickstart-voice-agent.md) -- build your first
  voice agent against `moonshine serve` (Tier 0/1/2 walkthrough).
- [serve-sidecar.md](serve-sidecar.md) -- `moonshine serve` architecture contract,
  file ownership map, and IPC protocol.
- [hosting.md](hosting.md) -- hosting `moonshine serve` beyond your laptop:
  serve-in-a-box and bring-your-own-cloud deployment.
- [RELEASING.md](RELEASING.md) -- versioning policy, tagging guidelines, and GitHub release automation.
- [testing-with-container.md](testing-with-container.md) -- verifying Linux
  `dlopen()` behavior locally on Apple Silicon with Apple's `container`
  CLI, no Docker required.
- [MISSION.md](MISSION.md) -- why this project exists: bringing the
  STT → LLM → TTS cascade back.
- [faq.md](faq.md) -- timestamps, transcription speed (RTF), progress
  indicators, model caching, saving output.
- [hardware-acceleration.md](hardware-acceleration.md) -- `--providers` /
  CoreML: measured results and why CPU is the default.

If you run an experiment that changes or adds to these findings (different
hardware, a newer onnxruntime, a different model), update the relevant doc
rather than starting a new one -- these are meant to stay current, not be a
changelog.
