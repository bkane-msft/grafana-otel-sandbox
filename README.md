# grafana-otel-sandbox

Pared-down copy of https://github.com/grafana/docker-otel-lgtm — just enough
to run the Grafana LGTM backend in Docker and point a Go app and MCP server at it.

# Install

```bash
# install dependencies if needed
brew install direnv uv docker

# one-time, after each .envrc change
direnv allow
direnv allow examples/go

# Set up .env needed for MCP server
./run-lgtm.py                       # start Grafana
./create-grafana-token.py           # create a service account + token, write to .env
./test-mcp.py
```

# Quick start

```bash
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

# Environment

Env config splits across two files, loaded by direnv:

- **`.envrc`** (committed): unchanging defaults
- **`.env`** (gitignored - generate with `./create-grafana-token.py`): `GRAFANA_SERVICE_ACCOUNT_TOKEN`. Loaded by `.envrc`
- **`examples/go/.envrc`** - env vars needed by the Go example app

# Dashboards

Edit dashboards in the UI and export/re-export with:

```bash
./export-dashboards.py
```

Dashboards are saved into `./grafana/dashboards/`.

# TODO

- Make scripts use flags instead of env vars (with ability to set from env var)
- make an actually nice dashboard with good metrics
- add Rust example
