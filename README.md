# grafana-otel-sandbox

Pared-down copy of https://github.com/grafana/docker-otel-lgtm — just enough
to run the Grafana LGTM backend in Docker and point a Go app at it.

## Quick start

```bash
# Terminal 1: start the backend
./run-lgtm.sh

# Terminal 2: run the Go app
cd examples/go && ./run.sh

# Terminal 3: hit it
curl localhost:8081/rolldice
```

Then open Grafana at http://localhost:3000 to see traces, metrics, and logs.

## Ports

| Service    | Port |
|------------|------|
| Grafana    | 3000 |
| OTLP gRPC  | 4317 |
| OTLP HTTP  | 4318 |
| Pyroscope  | 4040 |
| Prometheus | 9090 |
| Go app     | 8081 |

`run-lgtm.sh` pulls `docker.io/grafana/otel-lgtm:latest` and creates
`./container/{grafana,prometheus,loki}` for persistent data on first run.

## Dashboards

Dashboards are version-controlled via Grafana's file-based provisioning. On
startup `run-lgtm.sh` mounts:

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
