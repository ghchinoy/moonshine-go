#!/usr/bin/env python3
"""Tier 0: the smallest possible Python consumer of `moonshine serve`'s live
transcript feed.

Connects to the WebSocket endpoint, decodes the {"kind", "payload"} wire
envelope, and prints each newly-finalized line as it arrives. No
moonshine-go dependency of any kind -- this is exactly what an external,
un-privileged subscriber in any language sees.

Usage:
    moonshine serve --transport ws --addr :8765
    python3 listen.py --addr ws://localhost:8765/ws
"""
import argparse
import asyncio
import json

import websockets


async def listen(addr: str) -> None:
    print(f"connected to {addr} -- listening for finalized transcript lines (Ctrl-C to stop)\n")
    seen_finalized: set[int] = set()

    async with websockets.connect(addr) as ws:
        async for message in ws:
            env = json.loads(message)
            if env.get("kind") != "transcript":
                continue  # display / action_result frames -- not this sample's concern

            payload = env["payload"]
            # "or []" guards against a JSON null for lines/finalized_line_ids
            # (e.g. the moment before any speech has been transcribed);
            # cheap defensive coding even now that the server always omits
            # empty slices rather than sending null.
            lines_by_id = {line["id"]: line for line in (payload.get("lines") or [])}

            # finalized_line_ids names the lines that newly finalized on THIS
            # event; look them up in lines for their text. This is the
            # idempotency contract every subscriber must honor (see
            # docs/serve-sidecar.md / samples/README.md): interim frames may
            # be dropped under backpressure, but a finalized line ID is only
            # ever new once.
            for line_id in (payload.get("finalized_line_ids") or []):
                if line_id in seen_finalized:
                    continue
                seen_finalized.add(line_id)
                line = lines_by_id.get(line_id)
                if line:
                    print(f"[FINAL] {line['text']}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--addr", default="ws://localhost:8765/ws", help="moonshine serve WebSocket URL")
    args = parser.parse_args()
    try:
        asyncio.run(listen(args.addr))
    except KeyboardInterrupt:
        print("\nstopped.")


if __name__ == "__main__":
    main()
