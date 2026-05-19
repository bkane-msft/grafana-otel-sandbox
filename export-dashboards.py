#!/usr/bin/env python3
"""Export every Grafana dashboard as JSON into grafana/dashboards/.

The output is shaped for file-based provisioning: top-level is the dashboard
object (not the {dashboard, meta} API wrapper) and `id` is nulled so Grafana
won't try to reuse an instance-local primary key on import.
"""

import json
import os
import sys
import urllib.request
from pathlib import Path

GRAFANA_URL = os.environ.get("GRAFANA_URL", "http://localhost:3000")
OUT_DIR = Path(os.environ.get("OUT_DIR", Path(__file__).parent / "grafana" / "dashboards"))


def fetch_json(path: str):
    with urllib.request.urlopen(f"{GRAFANA_URL}{path}") as resp:
        return json.load(resp)


def main() -> int:
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    dashboards = fetch_json("/api/search?type=dash-db")
    if not dashboards:
        print(f"No dashboards found at {GRAFANA_URL}.", file=sys.stderr)
        return 1

    include_provisioned = os.environ.get("INCLUDE_PROVISIONED") == "1"

    for d in dashboards:
        uid = d["uid"]
        slug = d["uri"].removeprefix("db/")
        payload = fetch_json(f"/api/dashboards/uid/{uid}")

        if payload.get("meta", {}).get("provisioned") and not include_provisioned:
            print(f"skipped {uid} ({slug}) — provisioned by image")
            continue

        dashboard = payload["dashboard"]
        dashboard["id"] = None

        out = OUT_DIR / f"{slug}.json"
        out.write_text(json.dumps(dashboard, indent=2) + "\n")
        print(f"exported {uid} -> {out}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
