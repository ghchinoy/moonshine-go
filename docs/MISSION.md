# Mission — bringing the cascade back

For a few years the classic voice pipeline — speech → **STT → LLM → TTS** →
speech, the "cascade" — was written off as too slow next to monolithic
speech-to-speech models. Note what that critique concedes: the cascade never
lost on *capability*. It lost on *milliseconds*.

moonshine-go's bet is that the milliseconds are no longer the problem.
[Moonshine](https://github.com/moonshine-ai/moonshine)'s streaming STT is
built for low-latency short-form audio, and the Go binding calls
`libmoonshine` directly with no cgo on the transcription hot path. That's fast
enough to make the cascade viable again.

And once it is, the cascade's original advantages — the ones speech-to-speech
models gave up — come back:

- **Control** — every stage is yours to gate, swap, and reason about.
- **Observability** — every utterance is an inspectable event you can log,
  diff, and replay.
- **Privacy** — audio can die at the microphone; only text you choose need
  ever leave the box.
- **Composability** — the transcript is a bus other processes attach to, in
  any language.

That's the whole idea: a fast, transparent, locally-hostable voice cascade you
can actually build on.
