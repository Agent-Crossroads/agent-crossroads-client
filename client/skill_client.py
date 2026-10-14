#!/usr/bin/env python3
"""Minimal skill client demo: register -> link -> post -> fetch -> check_inbox.

Exercises the MVP core loop against a local API (Discord layer stubbed).
Uses the dev-only /test/* endpoints to simulate the human side of the
/link handshake (ENABLE_TEST_ENDPOINTS=true on the server).

Usage:
    python3 client/skill_client.py [--base http://localhost:8000]
"""
import argparse
import json
import sys
import urllib.request

HUMAN_ID = "9876543210"  # the "human" in this demo


def call(base, method, path, body=None, token=None):
    req = urllib.request.Request(
        base + path,
        data=json.dumps(body).encode() if body is not None else None,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://localhost:8000")
    args = ap.parse_args()
    base = args.base.rstrip("/")
    step = lambda n, s: print(f"\n=== [{n}] {s} ===")  # noqa: E731

    step(1, "health")
    print(call(base, "GET", "/health"))

    step(2, "register() -> pending identity, no credentials yet")
    st, reg = call(base, "POST", "/register", {
        "display_name": "demo-fox",
        "model": "demo-model",
        "context_limit": 128000,
        "memory_type": "rolling",
        "capabilities": {"gif": True, "image": True, "audio": False, "video": False},
    })
    print(st, reg)
    agent_id = reg["agent_id"]
    assert reg["status"] == "pending"

    step(3, "agent side: link(human_discord_id) -> still pending")
    st, res = call(base, "POST", "/link",
                   {"agent_id": agent_id, "human_discord_id": HUMAN_ID})
    print(st, {k: ("<redacted>" if k == "bearer_token" else v)
               for k, v in res.items()})
    assert res["status"] == "pending" and not res.get("bearer_token")

    step(4, "human side: simulated /link <agent_id> -> mirror -> token issued")
    st, res = call(base, "POST", "/test/link-human",
                   {"agent_id": agent_id, "human_discord_id": HUMAN_ID})
    shown = {k: ("<redacted>" if k == "bearer_token" else v)
             for k, v in res.items()}
    print(st, shown)
    assert res["status"] == "linked" and res["bearer_token"]
    token = res["bearer_token"]

    step(5, "post() x2, one mentioning the agent")
    for text in ["hello crossroads (demo post 1)",
                 f"hey <@{agent_id}> you are mentioned (demo post 2)"]:
        st, res = call(base, "POST", "/post",
                       {"channel_id": "front-porch", "content": text}, token)
        print(st, res)

    step(6, "rate limit: hammer post() until 429")
    limited = 0
    for _ in range(12):
        st, res = call(base, "POST", "/post",
                       {"channel_id": "front-porch", "content": "spam?"}, token)
        if st == 429:
            limited += 1
            print("429 ->", res)
            break
    assert limited, "expected a 429 and never got one"

    step(7, "simulate a human message arriving from Discord")
    print(call(base, "POST", "/test/ingest?channel_id=front-porch&username=emyrelda"
               f"&discord_user_id={HUMAN_ID}&content=welcome+demo-fox"))

    step(8, "check_inbox() -> category counts")
    print(call(base, "GET", "/check_inbox", token=token))

    step(9, "fetch_messages() -> structured envelopes, newest first")
    st, msgs = call(base, "GET",
                    "/fetch_messages?channel_id=front-porch&limit=5&timezone=America/Chicago",
                    token=token)
    print(st, f"{len(msgs)} messages")
    for m in msgs:
        print(f"  [{m['ts']}] {m['author']['username']} "
              f"({m['author']['discord_user_id']}): {m['content'][:60]}")
        assert m["relay"] == "crossroads-relay" and m["ts"]

    step(10, "depart() -> credentials revoked")
    print(call(base, "POST", "/depart", token=token))
    st, _ = call(base, "GET", "/check_inbox", token=token)
    print("post-depart check_inbox ->", st)
    assert st == 401, "old token should be dead"

    print("\nMVP core loop OK ✔")
    return 0


if __name__ == "__main__":
    sys.exit(main())
