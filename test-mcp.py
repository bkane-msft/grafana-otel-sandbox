#!/usr/bin/env python3
"""Smoke-test the mcp-grafana server end-to-end.

Spawns `uvx mcp-grafana` exactly as Claude Code would, drives an MCP stdio
handshake (initialize -> tools/list -> tools/call search_dashboards), and
verifies the server starts, advertises tools, and successfully authenticates
against Grafana.

Loads .env from the script directory if GRAFANA_SERVICE_ACCOUNT_TOKEN isn't
already in the environment (so the script works whether or not direnv is set
up). Exits non-zero on any failure.
"""

import json
import os
import subprocess
import sys
import time
from pathlib import Path

REPO = Path(__file__).parent
ENV_FILE = REPO / ".env"
TIMEOUT = 60


def load_dotenv() -> None:
    if "GRAFANA_SERVICE_ACCOUNT_TOKEN" in os.environ:
        return
    if not ENV_FILE.exists():
        return
    for line in ENV_FILE.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        os.environ.setdefault(k.strip(), v.strip())


def request(req_id: int, method: str, params: dict | None = None) -> str:
    msg = {"jsonrpc": "2.0", "id": req_id, "method": method}
    if params is not None:
        msg["params"] = params
    return json.dumps(msg)


def notification(method: str) -> str:
    return json.dumps({"jsonrpc": "2.0", "method": method})


def run() -> int:
    load_dotenv()
    if not os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        print("GRAFANA_SERVICE_ACCOUNT_TOKEN not set. Run ./create-grafana-token.py first.", file=sys.stderr)
        return 1
    os.environ.setdefault("GRAFANA_URL", "http://localhost:3000")

    messages = [
        request(1, "initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "test-mcp", "version": "0.0.1"},
        }),
        notification("notifications/initialized"),
        request(2, "tools/list"),
        request(3, "tools/call", {"name": "search_dashboards", "arguments": {}}),
    ]

    start = time.time()
    proc = subprocess.run(
        ["uvx", "mcp-grafana"],
        input="\n".join(messages) + "\n",
        capture_output=True,
        text=True,
        timeout=TIMEOUT,
    )
    elapsed = time.time() - start

    if proc.returncode != 0 and not proc.stdout:
        print(f"mcp-grafana exited {proc.returncode} with no output", file=sys.stderr)
        print(proc.stderr, file=sys.stderr)
        return 1

    responses = {}
    for line in proc.stdout.splitlines():
        if not line.strip():
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "id" in msg:
            responses[msg["id"]] = msg

    failed = False

    init = responses.get(1)
    if init and "result" in init:
        srv = init["result"]["serverInfo"]
        print(f"  initialize        ok ({srv['name']} v{srv.get('version', '?')})")
    else:
        print(f"  initialize        FAILED: {init}")
        failed = True

    tools_resp = responses.get(2)
    if tools_resp and "result" in tools_resp:
        n = len(tools_resp["result"]["tools"])
        print(f"  tools/list        ok ({n} tools)")
    else:
        print(f"  tools/list        FAILED: {tools_resp}")
        failed = True

    search = responses.get(3)
    if search and "result" in search:
        content = search["result"]["content"][0]["text"]
        data = json.loads(content)
        dashboards = data.get("dashboards", data) if isinstance(data, dict) else data
        print(f"  search_dashboards ok ({len(dashboards)} dashboards)")
        for d in dashboards:
            print(f"    - {d['title']} (uid={d['uid']})")
    else:
        print(f"  search_dashboards FAILED: {search}")
        failed = True

    print(f"\n{'FAIL' if failed else 'PASS'} in {elapsed:.1f}s")
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        sys.exit(run())
    except subprocess.TimeoutExpired:
        print(f"mcp-grafana didn't respond within {TIMEOUT}s", file=sys.stderr)
        sys.exit(1)
