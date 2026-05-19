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
