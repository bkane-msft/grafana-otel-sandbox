# grafana-otel-sandbox

Pared-down copy of https://github.com/grafana/docker-otel-lgtm — just enough
to run the Grafana LGTM backend in Docker and point a Go app and MCP server at it.

# Install

```bash
# install dependencies if needed
brew install direnv uv docker

# one-time, after each .envrc change
direnv allow
direnv allow examples/go-global-state
direnv allow examples/go-with-tests

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
cd examples/go-with-tests && ./run.sh

# Alternative, emit everything to stdout
OTEL_TRACES_EXPORTER=console OTEL_METRICS_EXPORTER=console OTEL_LOGS_EXPORTER=console ./run.sh

# Terminal 3: hit it
while true; do curl localhost:8081/rolldice; done

# kill server (if Ctrl-C hangs)
docker kill lgtm
```

Then open Grafana at http://localhost:3000 to see traces, metrics, and logs.
The rolldice dashboard has a **Service** dropdown to switch between the
`rolldice-with-tests` and `rolldice-global-state` example apps.

# Examples

Two variants of the same OpenTelemetry-instrumented dice roller live under
`examples/`:

- **`examples/go-global-state`** (`service.name=rolldice-global-state`) - the
  original, wired up with global OTel providers and package-level state.
- **`examples/go-with-tests`** (`service.name=rolldice-with-tests`) - refactored
  so request-time state (roller, logger, tracer, metric instruments) lives on a
  `RollDiceServer` struct built from explicit providers. This makes the handler
  unit-testable without touching global state; see `rolldice_test.go` and run
  `go test ./...` from that directory.

Both listen on port 8081, so run only one at a time.

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
- **`examples/go-global-state/.envrc`** / **`examples/go-with-tests/.envrc`** - env vars needed by each Go example app

# Dashboards

Edit dashboards in the UI and export/re-export with:

```bash
./export-dashboards.py
```

Dashboards are saved into `./grafana/dashboards/`.

# TODO

- Make scripts use flags instead of env vars (with ability to set from env var)
- exporting dashboards work but I don't ben dashboard in the menu?
- add Rust example

