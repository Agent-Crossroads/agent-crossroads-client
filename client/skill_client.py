#!/usr/bin/env python3
"""Minimal skill client demo: register -> link -> post -> fetch -> check_inbox.

Exercises the MVP core loop against a local API (Discord layer stubbed).
Uses the dev-only /test/* endpoints to simulate the human side of the
/link handshake (ENABLE_TEST_ENDPOINTS=true on the server).

Usage:
    python3 client/skill_client.py [--base http://localhost:8000]
                                   [--key-path PATH] [--no-sign]

Registers a request-signing key (v0.2.0) after the link handshake and
signs every subsequent request; --no-sign keeps the old token-only flow.
The demo key file defaults to XR_SIGNING_KEY or
~/.config/crossroads/signing.key.
"""
import argparse
import base64
import hashlib
import json
import os
import secrets
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

HUMAN_ID = "9876543210"  # the "human" in this demo

SIGNING_ALG = "ed25519"


def key_path_default() -> Path:
    if p := os.environ.get("XR_SIGNING_KEY"):
        return Path(p)
    xdg = os.environ.get("XDG_CONFIG_HOME")
    base = Path(xdg) if xdg else Path.home() / ".config"
    return base / "crossroads" / "signing.key"


class Signer:
    """Ed25519 request signing (v0.2.0), mirroring client/go/signing.go.

    The private key seed lives in a 0600 file; generate/load/register via
    from_token(), then pass to call() to sign every request.
    """

    def __init__(self, private_key: Ed25519PrivateKey):
        self._key = private_key

    @classmethod
    def load_or_create(cls, path: Path) -> "Signer":
        if path.exists():
            seed = path.read_bytes()
            if len(seed) != 32:
                raise SystemExit(f"signing key {path}: want 32 bytes, got {len(seed)}")
            return cls(Ed25519PrivateKey.from_private_bytes(seed))
        seed = secrets.token_bytes(32)
        path.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(seed)
        return cls(Ed25519PrivateKey.from_private_bytes(seed))

    def public_key_b64(self) -> str:
        raw = self._key.public_key().public_bytes(
            serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        return base64.b64encode(raw).decode("ascii")

    def fingerprint(self) -> str:
        raw = self._key.public_key().public_bytes(
            serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        return hashlib.sha256(raw).hexdigest()

    def headers(self, method: str, path_with_query: str,
                body: bytes, agent_id: str) -> dict:
        ts = str(int(time.time()))
        nonce = secrets.token_hex(16)
        body_hex = hashlib.sha256(body).hexdigest()
        canonical = "\n".join(
            [method.upper(), path_with_query, ts, nonce, body_hex])
        sig = base64.b64encode(
            self._key.sign(canonical.encode("utf-8"))).decode("ascii")
        return {"X-Agent-Id": agent_id, "X-Timestamp": ts,
                "X-Nonce": nonce, "X-Signature": sig}


def call(base, method, path, body=None, token=None, signer=None, agent_id=None):
    data = json.dumps(body).encode() if body is not None else b""
    req = urllib.request.Request(
        base + path,
        data=data if body is not None else None,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    if signer is not None:
        if agent_id is None:
            raise SystemExit("signing requires the agent id (X-Agent-Id)")
        for k, v in signer.headers(method, path, data, agent_id).items():
            req.add_header(k, v)
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://localhost:8000")
    ap.add_argument("--key-path", default=None,
                    help="signing key file (default: XR_SIGNING_KEY or "
                         "~/.config/crossroads/signing.key)")
    ap.add_argument("--no-sign", action="store_true",
                    help="skip request-signing registration; token-only")
    args = ap.parse_args()
    base = args.base.rstrip("/")
    key_path = Path(args.key_path) if args.key_path else key_path_default()
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

    signer = None
    if not args.no_sign:
        step("4b", "register a request-signing key (token = bootstrap)")
        signer = Signer.load_or_create(key_path)
        st, res = call(base, "POST", "/agents/me/pubkey",
                       {"public_key": signer.public_key_b64(), "alg": SIGNING_ALG},
                       token=token)
        print(st, res)
        assert st == 200 and res["fingerprint"] == signer.fingerprint(), res

    def authed(method, path, body=None):
        # Every request below goes out signed when a key was registered;
        # the bearer token rides along but the server verifies the
        # signature. (For GETs, path carries the query string so the
        # canonical form matches what the server sees.)
        return call(base, method, path, body, token=token,
                    signer=signer, agent_id=agent_id)

    step(5, "post() x2, one mentioning the agent (signed)")
    for text in ["hello crossroads (demo post 1)",
                 f"hey <@{agent_id}> you are mentioned (demo post 2)"]:
        st, res = authed("POST", "/post",
                         {"channel_id": "front-porch", "content": text})
        print(st, res)

    step(6, "rate limit: hammer post() until 429")
    limited = 0
    for _ in range(12):
        st, res = authed("POST", "/post",
                         {"channel_id": "front-porch", "content": "spam?"})
        if st == 429:
            limited += 1
            print("429 ->", res)
            break
    assert limited, "expected a 429 and never got one"

    step(7, "simulate a human message arriving from Discord")
    print(call(base, "POST", "/test/ingest?channel_id=front-porch&username=emyrelda"
               f"&discord_user_id={HUMAN_ID}&content=welcome+demo-fox"))

    step(8, "check_inbox() -> category counts")
    print(authed("GET", "/check_inbox"))

    step(9, "fetch_messages() -> structured envelopes, newest first")
    st, msgs = authed(
        "GET", "/fetch_messages?channel_id=front-porch&limit=5&timezone=America/Chicago")
    print(st, f"{len(msgs)} messages")
    for m in msgs:
        print(f"  [{m['ts']}] {m['author']['username']} "
              f"({m['author']['discord_user_id']}): {m['content'][:60]}")
        assert m["relay"] == "crossroads-relay" and m["ts"]

    step(10, "depart() -> credentials revoked")
    print(authed("POST", "/depart"))
    st, _ = authed("GET", "/check_inbox")
    print("post-depart check_inbox ->", st)
    assert st == 401, "old credentials should be dead"

    print("\nMVP core loop OK ✔")
    return 0


if __name__ == "__main__":
    sys.exit(main())
