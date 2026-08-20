#!/usr/bin/env python3
"""Provision a Grafana service account + token for mcp-grafana.

Idempotent: re-running reuses the existing service account and creates a fresh
token (Grafana token keys can only be read at creation time, so we can't
recover a prior one). The token is written into .env under
GRAFANA_SERVICE_ACCOUNT_TOKEN, which .mcp.json references.

Defaults assume the local sandbox: admin/admin at http://localhost:3000.
Override via GRAFANA_URL, GRAFANA_ADMIN_USER, GRAFANA_ADMIN_PASSWORD.
"""

import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

GRAFANA_URL = os.environ.get("GRAFANA_URL", "http://localhost:3000").rstrip("/")
ADMIN_USER = os.environ.get("GRAFANA_ADMIN_USER", "admin")
ADMIN_PASSWORD = os.environ.get("GRAFANA_ADMIN_PASSWORD", "admin")
SA_NAME = "mcp-grafana-sandbox"
SA_ROLE = "Editor"
ENV_FILE = Path(__file__).parent / ".env"
ENV_KEY = "GRAFANA_SERVICE_ACCOUNT_TOKEN"


def auth_header() -> str:
    creds = f"{ADMIN_USER}:{ADMIN_PASSWORD}".encode()
    return "Basic " + base64.b64encode(creds).decode()


def request(method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"{GRAFANA_URL}{path}",
        data=data,
        method=method,
        headers={
            "Authorization": auth_header(),
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.load(resp)
    except urllib.error.HTTPError as e:
        payload = e.read().decode()
        try:
            return e.code, json.loads(payload)
        except json.JSONDecodeError:
            return e.code, {"raw": payload}


def wait_for_grafana(timeout: int = 60) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{GRAFANA_URL}/api/health", timeout=2) as resp:
                if resp.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(1)
    print(f"Grafana at {GRAFANA_URL} did not become healthy within {timeout}s.", file=sys.stderr)
    sys.exit(1)


def get_or_create_service_account() -> int:
    status, body = request("POST", "/api/serviceaccounts", {"name": SA_NAME, "role": SA_ROLE})
    if status in (200, 201):
        print(f"created service account {SA_NAME!r} (id={body['id']})")
        return body["id"]
    if status == 400 and "already exists" in json.dumps(body).lower():
        status, body = request("GET", f"/api/serviceaccounts/search?query={SA_NAME}")
        for sa in body.get("serviceAccounts", []):
            if sa["name"] == SA_NAME:
                print(f"reusing service account {SA_NAME!r} (id={sa['id']})")
                return sa["id"]
    print(f"failed to create or find service account: {status} {body}", file=sys.stderr)
    sys.exit(1)


def create_token(sa_id: int) -> str:
    name = f"mcp-{int(time.time())}"
    status, body = request("POST", f"/api/serviceaccounts/{sa_id}/tokens", {"name": name})
    if status not in (200, 201):
        print(f"failed to create token: {status} {body}", file=sys.stderr)
        sys.exit(1)
    print(f"created token {name!r}")
    return body["key"]


def write_env(token: str) -> None:
    lines = ENV_FILE.read_text().splitlines() if ENV_FILE.exists() else []
    lines = [ln for ln in lines if not ln.startswith(f"{ENV_KEY}=")]
    lines.append(f"{ENV_KEY}={token}")
    ENV_FILE.write_text("\n".join(lines) + "\n")
    print(f"wrote {ENV_KEY} to {ENV_FILE}")


def main() -> int:
    wait_for_grafana()
    sa_id = get_or_create_service_account()
    token = create_token(sa_id)
    write_env(token)
    print()
    print("The repo ships a .envrc that loads .env via direnv. If you haven't yet:")
    print("  direnv allow")
    print("After that, cd into this directory and the token is in your shell env,")
    print("so Claude Code's .mcp.json will pick it up automatically.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
