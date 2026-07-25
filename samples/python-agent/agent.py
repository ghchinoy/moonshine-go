#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["websockets>=12.0"]
# ///
"""Tier 1: an external agent for `moonshine serve`, in Python.

Connects to the WebSocket endpoint, watches finalized transcript lines for
a couple of deterministic voice commands, and sends ActionRequest JSON back
over the same connection to trigger real effects in the sidecar: speaking
the current time, or pausing/resuming the session. No moonshine-go
dependency of any kind -- just `websockets` and `json`, the same contract
any language can use.

Every finalized line is logged with a wall-clock timestamp and the STT
engine's own reported per-line latency (Line.LastLatencyMs on the wire) --
real numbers from the server, not a client-side stopwatch -- so you can see
how fast the local cascade actually is. The session's time-to-first-token
(ttft_ms) is logged once, the moment it's known. A "no match" line is
logged when a transcript doesn't trigger anything, so silence never looks
like the agent being stuck. Pass --debug for the underlying per-command
matching trace (which pattern was tried, hit or miss), per-poll latency
(poll_latency_ms), and action round-trip timing.

Usage:
    moonshine serve --transport ws --allow-actions --agent external
    uv run agent.py --addr ws://localhost:8765/ws
    # or, without uv: pip install -r requirements.txt && python3 agent.py ...

Then say "what time is it", "stop listening", or "resume listening".
"""
import argparse
import asyncio
import datetime
import json
import re
import time

import websockets

STOP_RE = re.compile(r"^\s*(stop|pause)\s+listening\s*\.?\s*$", re.IGNORECASE)
RESUME_RE = re.compile(r"^\s*(resume|start)\s+listening\s*\.?\s*$", re.IGNORECASE)
TIME_RE = re.compile(r"\b(what time is it|what's the time|current time)\b", re.IGNORECASE)

# Named so a --debug trace can report which pattern matched (or didn't),
# rather than just the final verdict from classify().
RULES = [
    ("stop_listening", STOP_RE, "match", lambda m: {"verb": "session.pause"}),
    ("resume_listening", RESUME_RE, "match", lambda m: {"verb": "session.resume"}),
    ("what_time_is_it", TIME_RE, "search", lambda m: {
        "verb": "speak",
        "args": {"text": f"The current time is {datetime.datetime.now().strftime('%-I:%M %p')}."},
    }),
]

debug = False  # set from --debug in main()


def ts() -> str:
    """Wall-clock timestamp with millisecond precision, for log lines."""
    return datetime.datetime.now().strftime("%H:%M:%S.%f")[:-3]


def classify(text: str) -> dict | None:
    """Maps a finalized line to an ActionRequest, or None for no match.

    This is intentionally simple pattern matching rather than an LLM call --
    the "control" pillar from docs/MISSION.md: deterministic, auditable,
    fully offline. See ../go-cascade-faq for the equivalent (plus a
    StaticRetriever-backed FAQ handler) in Go against pkg/serveapi.
    """
    for name, pattern, mode, build in RULES:
        m = pattern.match(text) if mode == "match" else pattern.search(text)
        if debug:
            print(f"[{ts()}] [debug] rule {name}: {'HIT' if m else 'miss'}")
        if m:
            return build(m)
    return None


async def run_agent(addr: str) -> None:
    print(f"connected to {addr} -- say \"what time is it\", \"stop listening\", or \"resume listening\"\n"
          "(Ctrl-C to quit)\n")
    seen_finalized: set[int] = set()
    # Tracks in-flight ActionRequest IDs -> send time, so the matching
    # action_result frame can report a real round-trip latency.
    pending: dict[str, float] = {}
    next_id = 0
    # Latches once the session's time-to-first-token is known, so it's
    # reported exactly once instead of on every event that still carries it
    # (ttft_ms holds its value for the whole session once set).
    ttft_logged = False

    async with websockets.connect(addr) as ws:
        async for message in ws:
            env = json.loads(message)
            kind = env.get("kind")

            if kind == "action_result":
                payload = env["payload"]
                req_id = payload.get("id")
                sent_at = pending.pop(req_id, None)
                elapsed_ms = (time.monotonic() - sent_at) * 1000 if sent_at else None
                status = "ok" if payload.get("ok") else f"failed: {payload.get('err')}"
                suffix = f" ({elapsed_ms:.0f}ms round trip)" if elapsed_ms is not None else ""
                print(f"[{ts()}] [agent] action {status}{suffix}")
                continue

            if kind != "transcript":
                continue

            payload = env["payload"]
            lines_by_id = {line["id"]: line for line in (payload.get("lines") or [])}

            ttft_ms = payload.get("ttft_ms")
            if not ttft_logged and ttft_ms:
                ttft_logged = True
                print(f"[{ts()}] [stats] time to first token: {ttft_ms}ms")

            if debug:
                poll_ms = payload.get("poll_latency_ms")
                if poll_ms:
                    print(f"[{ts()}] [debug] poll latency: {poll_ms}ms "
                          f"(elapsed: {payload.get('elapsed_ms')}ms)")

            for line_id in (payload.get("finalized_line_ids") or []):
                if line_id in seen_finalized:
                    continue
                seen_finalized.add(line_id)
                line = lines_by_id.get(line_id)
                if not line:
                    continue

                text = line["text"]
                stt_ms = line.get("last_latency_ms")
                stt_suffix = f"  (stt: {stt_ms}ms)" if stt_ms is not None else ""
                print(f"[{ts()}] [you said] {text}{stt_suffix}")

                action = classify(text)
                if action is None:
                    print(f"[{ts()}] [agent] no match -- try: "
                          "\"what time is it\", \"stop listening\", \"resume listening\"")
                    continue

                next_id += 1
                req_id = f"python-agent-{next_id}"
                action["id"] = req_id
                pending[req_id] = time.monotonic()
                print(f"[{ts()}] [agent] -> {action['verb']}")
                await ws.send(json.dumps(action))


def main() -> None:
    global debug
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--addr", default="ws://localhost:8765/ws", help="moonshine serve WebSocket URL")
    parser.add_argument("--debug", action="store_true", help="print per-rule matching trace")
    args = parser.parse_args()
    debug = args.debug
    try:
        asyncio.run(run_agent(args.addr))
    except KeyboardInterrupt:
        print("\nstopped.")


if __name__ == "__main__":
    main()
