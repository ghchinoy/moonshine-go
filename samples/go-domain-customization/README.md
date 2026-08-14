# samples/go-domain-customization — runtime vocabulary & domain biasing

A Tier 1/2 external Go agent demonstrating Moonshine v0.1.2's runtime domain customization capabilities (`session.set_keyterms` and `session.set_context` ActionRequests over WebSocket) to dynamically bias speech-to-text recognition toward specialized jargon mid-stream — with zero model reloads and zero cgo.

## What it demonstrates

- **Keyterm Biasing (`session.set_keyterms`):** Dynamically applies logit bonuses to specialized terminology (e.g. `Kubernetes`, `Ceph`, `Atorvastatin`, `Echocardiogram`, `EBITDA`) while audio is actively streaming.
- **Context Passage Extraction (`session.set_context`):** Submits a block of free-form document text (e.g. engineering notes, ticket descriptions); the sidecar's tokenizer automatically extracts, ranks, and caps the rarest subwords (up to 200 terms by default).
- **Live Mid-Stream Switching:** Saying *"switch to cloud"*, *"switch to medical"*, *"switch to finance"*, or *"load context passage"* sends `session.set_keyterms` or `session.set_context` ActionRequests back to the sidecar, updating the transcriber on the fly without interrupting mic capture or restarting the session.
- **AgentFlow DSL Integration:** Built on `pkg/agentflow`, combining trigger phrase matching (`flow.ListenFor`) and global session control (`flow.Always` with `d.PauseListening()`/`d.ResumeListening()`).

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Tier 1 / Tier 2 (Go extension) |
| **Complexity** | 3/5 |
| **Setup Cost** | Low (offline, local `moonshine serve`) |
| **Demonstrated Pillars** | Control, Composability, Observability, Privacy |
| **Primary Vertical / Use Case** | Medical Jargon, Cloud Infra, Financial Tickers, Dynamic Vocabulary Switching |
| **Appeal** | 5/5 |

## Architecture

```
mic → moonshine serve → WebSocket (TranscriptEvent JSON) → this program
                                                                  │
                                             serveapi.AgentRunner
                                                                  │
                                              agentflow.HandlerAdapter
                                                                  │
                                              agentflow.AgentFlow
                                              ├── "switch to cloud" ──> session.set_keyterms (K8s/Ceph/etcd)
                                              ├── "switch to medical" ──> session.set_keyterms (Atorvastatin...)
                                              ├── "load context" ──> session.set_context (passage text)
                                              ├── "clear domain" ──> session.set_keyterms ([])
                                              └── "stop/resume listening" ──> session.pause/resume
                                                                  │
                                                           ActionRequest
                                                                  │
                                                    WebSocket (back to sidecar)
                                                                  │
                                                    Transcriber.SetKeyterms / SetContext
```

## Run it

Build/fetch `libmoonshine` first if you haven't (see the repo root [README](../../README.md)). Then, in one terminal:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --allow-actions --agent external
```

`--allow-actions` is required so the sidecar permits `session.set_keyterms`, `session.set_context`, `speak`, and `session.pause/resume` actions.

In another terminal:

```sh
cd samples/go-domain-customization
go run . -addr ws://localhost:8765/ws
```

### Try these voice commands:

- **`"switch to cloud"`** / **`"cloud domain"`** — loads Kubernetes, Ceph, etcd, Istio, Kustomize, Prometheus, Grafana keyterms.
- **`"switch to medical"`** / **`"clinical domain"`** — loads Atorvastatin, Lisinopril, Echocardiogram, Hydrochlorothiazide, Metformin, Amlodipine keyterms.
- **`"switch to finance"`** / **`"financial domain"`** — loads NASDAQ, S&P 500, EBITDA, Amortization, Liquidity, Derivative keyterms.
- **`"load context passage"`** — submits an engineering migration note; sidecar automatically extracts platform terminology.
- **`"clear domain"`** / **`"reset vocabulary"`** — resets keyterm biasing to standard unbiased decoding.
- **`"stop listening"`** / **`"resume listening"`** — pauses or resumes speech recognition.

Pass `-debug` to inspect match traces, poll latency, and WebSocket dispatch round trips:

```sh
go run . -addr ws://localhost:8765/ws -debug
```
