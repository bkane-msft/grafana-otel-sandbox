# grafana-otel-sandbox

Pared-down copy of https://github.com/grafana/docker-otel-lgtm — just enough
to run the Grafana LGTM backend in Docker and point a Go app at it.

# Quick start

Requires [direnv](https://direnv.net/) — see [Environment](#environment) below.

```bash
direnv allow                        # one-time, after each .envrc change
direnv allow examples/go

# Terminal 1: start the backend
./run-lgtm.py

# Terminal 2: run the Go app
cd examples/go && ./run.sh

# Alternative, emit everything to stdout
OTEL_TRACES_EXPORTER=console OTEL_METRICS_EXPORTER=console OTEL_LOGS_EXPORTER=console ./run.sh

# Terminal 3: hit it
while true; do curl localhost:8081/rolldice; done

# kill server (if Ctrl-C hangs)
docker kill lgtm
```

Then open Grafana at http://localhost:3000 to see traces, metrics, and logs.

# Ports

| Service    | Port |
|------------|------|
| Grafana    | 3000 |
| OTLP gRPC  | 4317 |
| OTLP HTTP  | 4318 |
| Pyroscope  | 4040 |
| Prometheus | 9090 |
| Go app     | 8081 |

`run-lgtm.py` pulls `docker.io/grafana/otel-lgtm:latest` and creates
`./container/{grafana,prometheus,loki}` for persistent data on first run.

# Environment

Env config splits across two files, loaded by direnv:

- **`.envrc`** (committed) — non-secret defaults: `GRAFANA_URL`,
  `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD`. The Go example has its own
  `examples/go/.envrc` with OTEL config (`OTEL_RESOURCE_ATTRIBUTES`,
  `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_METRIC_EXPORT_INTERVAL`).
- **`.env`** (gitignored) — secrets; currently just
  `GRAFANA_SERVICE_ACCOUNT_TOKEN`. Loaded last by `.envrc`, so values here
  override the committed defaults.

Run `direnv allow` after cloning, and again whenever an `.envrc` changes. The
nested `examples/go/.envrc` needs its own `direnv allow examples/go`.

# Dashboards

Dashboards are version-controlled via Grafana's file-based provisioning. On
startup `run-lgtm.py` mounts:

- `grafana/provisioning/` → `/etc/grafana/provisioning` (provider config)
- `grafana/dashboards/` → `/var/lib/grafana/dashboards` (the JSON files)

The provider runs with `allowUiUpdates: true`, so you can edit in the UI and
re-export with:

```bash
./export-dashboards.py
```

The script writes every user-authored dashboard into `grafana/dashboards/` as
`<slug>.json`. Dashboards that ship pre-provisioned with the `otel-lgtm` image
(JVM Overview, RED Metrics) are skipped — set `INCLUDE_PROVISIONED=1` to
override. `GRAFANA_URL` and `OUT_DIR` are also env-configurable.

Commit `grafana/` to keep dashboards in git; `container/grafana/` stays the
runtime data dir and is gitignored.

# Claude Code / VS Code via mcp-grafana

[mcp-grafana](https://github.com/grafana/mcp-grafana) exposes Grafana's API as
an MCP server. It runs as an on-demand stdio subprocess.

Requires: [`uv`](https://docs.astral.sh/uv/) (for `uvx`, which launches mcp-grafana).

Setup (one-time):

```bash
./run-lgtm.py                       # start Grafana
./bootstrap-mcp.py                  # create a service account + token, write to .env
```

Both clients pick up `GRAFANA_URL` and `GRAFANA_SERVICE_ACCOUNT_TOKEN` from
the shell env (loaded by direnv). Two config files live in the repo, one per
client — the formats differ enough that they can't share a file:

- **Claude Code** — `.mcp.json` (top-level `mcpServers`, `${VAR}` substitution).
  Launch `claude` from this directory. On first launch Claude treats
  project-scoped `.mcp.json` as untrusted — run `/mcp` and approve the
  `grafana` server. `mcp__grafana__*` tools become available immediately
  after approval.
- **VS Code** — `.vscode/mcp.json` (top-level `servers`, `${env:VAR}`
  substitution, requires VS Code 1.102+ with GitHub Copilot / Agent Mode).
  Launch `code .` from this directory so direnv's env is inherited. Open the
  Chat view, switch to **Agent** mode, and the `grafana` server appears in the
  tool picker; click to start it. Manage it any time via the **MCP: List
  Servers** command.

If the server isn't listed in either client, direnv likely hadn't loaded the
token when you launched the editor — exit, `direnv reload`, and re-launch.

`bootstrap-mcp.py` is idempotent — re-run it any time. It reuses the existing
`mcp-grafana-sandbox` service account (Editor role) and provisions a fresh
token. Override defaults with `GRAFANA_URL`, `GRAFANA_ADMIN_USER`,
`GRAFANA_ADMIN_PASSWORD`.

To verify the server works end-to-end:

```bash
./test-mcp.py
```

It spawns `uvx mcp-grafana` the same way Claude Code would, drives an MCP
handshake (`initialize` → `tools/list` → `search_dashboards`), and reports
PASS/FAIL. Useful for sanity-checking after `bootstrap-mcp.py` or after
changes to `.mcp.json`. Loads `.env` directly, so it works with or without
direnv.

To enable optional tool categories (e.g. `runpanelquery`, `examples`,
`clickhouse`), add `--enabled-tools` args in `.mcp.json` — see the
[mcp-grafana README](https://github.com/grafana/mcp-grafana#tools) for the full
list.

# TODO

- Make scripts use flags instead of env vars (with ability to set from env var)
- tweak the code to start up how I like it (optionally emit to stdout etc.)