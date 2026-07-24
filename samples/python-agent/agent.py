#!/usr/bin/env python3
"""Tier 1: an external agent for `moonshine serve`, in Python.

Connects to the WebSocket endpoint, watches finalized transcript lines for
a couple of deterministic voice commands, and sends ActionRequest JSON back
over the same connection to trigger real effects in the sidecar: speaking
the current time, or pausing/resuming the session. No moonshine-go
dependency of any kind -- just `websockets` and `json`, the same contract
any language can use.

Usage:
    moonshine serve --transport ws --allow-actions --agent external
    python3 agent.py --addr ws://localhost:8765/ws

Then say "what time is it", "stop listening", or "resume listening".
"""
import argparse
import asyncio
import datetime
import json
import re

import websockets

STOP_RE = re.compile(r"^\s*(stop|pause)\s+listening\s*\.?\s*$", re.IGNORECASE)
RESUME_RE = re.compile(r"^\s*(resume|start)\s+listening\s*\.?\s*$", re.IGNORECASE)
TIME_RE = re.compile(r"\b(what time is it|what's the time|current time)\b", re.IGNORECASE)


async def run_agent(addr: str) -> None:
    print(f"connected to {addr} -- say \"what time is it\", \"stop listening\", or \"resume listening\"\n"
          "(Ctrl-C to quit)\n")
    seen_finalized: set[int] = set()

    async with websockets.connect(addr) as ws:
        async for message in ws:
            env = json.loads(message)
            if env.get("kind") != "transcript":
                continue

            payload = env["payload"]
            lines_by_id = {line["id"]: line for line in (payload.get("lines") or [])}

            for line_id in (payload.get("finalized_line_ids") or []):
                if line_id in seen_finalized:
                    continue
                seen_finalized.add(line_id)
                line = lines_by_id.get(line_id)
                if not line:
                    continue

                text = line["text"]
                print(f"[you said] {text}")
                action = classify(text)
                if action is None:
                    continue
                print(f"[agent] -> {action['verb']}")
                await ws.send(json.dumps(action))


def classify(text: str) -> dict | None:
    """Maps a finalized line to an ActionRequest, or None for no match.

    This is intentionally simple pattern matching rather than an LLM call --
    the "control" pillar from docs/MISSION.md: deterministic, auditable,
    fully offline. See ../go-cascade-faq for the equivalent (plus a
    StaticRetriever-backed FAQ handler) in Go against pkg/serveapi.
    """
    if STOP_RE.match(text):
        return {"verb": "session.pause"}
    if RESUME_RE.match(text):
        return {"verb": "session.resume"}
    if TIME_RE.search(text):
        now = datetime.datetime.now().strftime("%-I:%M %p")
        return {"verb": "speak", "args": {"text": f"The current time is {now}."}}
    return None


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--addr", default="ws://localhost:8765/ws", help="moonshine serve WebSocket URL")
    args = parser.parse_args()
    try:
        asyncio.run(run_agent(args.addr))
    except KeyboardInterrupt:
        print("\nstopped.")


if __name__ == "__main__":
    main()
