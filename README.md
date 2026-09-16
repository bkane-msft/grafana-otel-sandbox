# grafana-otel-sandbox

Pared-down copy of https://github.com/grafana/docker-otel-lgtm — just enough
to run the Grafana LGTM backend in Docker and point Go and Rust apps and an MCP
server at it.

# Install

```bash
# install dependencies if needed
brew install direnv uv docker rust

# one-time, after each .envrc change
direnv allow
direnv allow examples/go-rolldice-global-state
direnv allow examples/go-rolldice-with-tests
direnv allow examples/rust-rolldice

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
(cd examples/go-rolldice-with-tests && direnv exec . ./run.sh)

# Or run the Rust app instead
(cd examples/rust-rolldice && direnv exec . ./run.sh)

# Emit everything to stdout instead of OTLP (works for either app)
(cd examples/go-rolldice-with-tests && \
  direnv exec . env OTEL_TRACES_EXPORTER=console OTEL_METRICS_EXPORTER=console \
  OTEL_LOGS_EXPORTER=console ./run.sh)

# Terminal 3: hit it
while true; do curl localhost:8081/rolldice; done

# kill server (if Ctrl-C hangs)
docker kill lgtm
```

Then open Grafana at http://localhost:3000 to see traces, metrics, and logs.
The rolldice dashboard has a **Service** dropdown to switch between the example
apps.

# Examples

Three variants of the same OpenTelemetry-instrumented dice roller live under
`examples/`:

- **`examples/go-rolldice-global-state`** (`service.name=go-rolldice-global-state`) - the
  original, wired up with global OTel providers and package-level state.
- **`examples/go-rolldice-with-tests`** (`service.name=go-rolldice-with-tests`) - refactored
  so request-time state (roller, logger, tracer, metric instruments) lives on a
  `RollDiceServer` struct built from explicit providers. This makes the handler
  unit-testable without touching global state; see `rolldice_test.go` and run
  `go test ./...` from that directory.
- **`examples/rust-rolldice`** (`service.name=rust-rolldice`) - an idiomatic
  Axum version. Business dependencies and metric instruments live in
  `AppState`; `tracing` subscribers provide spans and logs. Because
  `tower-http` only emits spans, a small Axum middleware records the standard
  `http.server.request.duration` histogram so the dashboard's HTTP panels
  (request rate, error ratio, P95 latency) populate too. Its parallel tests
  use per-test in-memory OTel exporters; run `cargo test` from that directory.

All three listen on port 8081, so run only one at a time.

# Ports

| Service      | Port |
|--------------|------|
| Grafana      | 3000 |
| OTLP gRPC    | 4317 |
| OTLP HTTP    | 4318 |
| Pyroscope    | 4040 |
| Prometheus   | 9090 |
| Rolldice app | 8081 |

# Environment

Env config splits across two files, loaded by direnv:

- **`.envrc`** (committed): unchanging defaults
- **`.env`** (gitignored - generate with `./create-grafana-token.py`): `GRAFANA_SERVICE_ACCOUNT_TOKEN`. Loaded by `.envrc`
- **`examples/go-rolldice-global-state/.envrc`** / **`examples/go-rolldice-with-tests/.envrc`** - env vars needed by each Go example app
- **`examples/rust-rolldice/.envrc`** - Rust app defaults. Both OTLP
  HTTP/protobuf and gRPC are compiled in; set `OTEL_EXPORTER_OTLP_PROTOCOL` and
  the matching endpoint to switch transports. The official Rust exporters read
  standard OTLP/resource/metric-interval variables directly. Per-signal exporter
  selection via `OTEL_TRACES_EXPORTER` / `OTEL_METRICS_EXPORTER` /
  `OTEL_LOGS_EXPORTER` (`otlp` | `console` | `none`) matches the Go example, as
  does distributed-context propagation via `OTEL_PROPAGATORS` (`tracecontext`,
  `baggage`, or `none`; defaults to `tracecontext,baggage`).

# Dashboards

Edit dashboards in the UI and export/re-export with:

```bash
./export-dashboards.py
```

Dashboards are saved into `./grafana/dashboards/`.

# TODO

- Make scripts use flags instead of env vars (with ability to set from env var)
- exporting dashboards work but I don't ben dashboard in the menu?
