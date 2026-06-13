# Grafana Logging Stack (PLG) — Design Spec

**Date:** 2026-06-09
**Status:** Implemented

---

## Goal

Aggregate structured logs from all EXBanka microservices into Grafana so developers can query, filter, and correlate log lines across services in both local dev (docker-compose) and production (AKS).

---

## Architecture

```
Go services (slog JSON → stdout)
        │
        ▼
  [docker-compose]              [Kubernetes / AKS]
  Promtail                      Promtail DaemonSet
  (Docker socket)               (/var/log/pods on each node)
        │                               │
        └──────────────┬────────────────┘
                       ▼
                     Loki
           (labels: service / namespace / level)
                       │
                       ▼
                   Grafana
            (Prometheus datasource unchanged;
             Loki datasource added)
```

---

## Components

### 1. `contract/logger` — shared JSON logger

**File:** `contract/logger/logger.go`

`Init(service string)` sets `slog.SetDefault` to a `slog.NewJSONHandler(os.Stdout)` with a `"service"` attribute pre-populated. In Go 1.21+, `slog.SetDefault` also redirects the legacy `log` package through the same handler — so existing `log.Printf` calls throughout each service automatically emit JSON without any further changes.

Called once per service as the very first statement in `cmd/main.go`.

### 2. Per-service logger init

Each of the 13 services calls `logger.Init("<service-name>")` before any other setup. `fmt.Printf` startup messages are replaced with `slog.Info` to ensure they are included in the JSON log stream.

Services updated: api-gateway, auth-service, user-service, notification-service, client-service, account-service, card-service, transaction-service, credit-service, exchange-service, stock-service, verification-service, interbank-service.

### 3. Loki — log storage

- **docker-compose:** `grafana/loki:3.0.0`, default built-in config, data persisted in `loki_data` volume, port 3100.
- **Kubernetes:** `k8s/logging/loki.yml` — Deployment + Service + ConfigMap in `monitoring` namespace. Single-replica, filesystem storage, 30-day retention.

### 4. Promtail — log collector

- **docker-compose:** `grafana/promtail:3.0.0`, reads container logs via Docker socket (`/var/run/docker.sock`), config in `promtail-config.docker.yml`. Pipeline stage parses JSON lines and promotes `level` to a Loki label.
- **Kubernetes:** `k8s/logging/promtail.yml` — DaemonSet + ConfigMap + ServiceAccount + ClusterRole/Binding in `monitoring` namespace. Reads pod logs from `/var/log/pods` on each node. Relabels `app` pod label → `service` Loki label.

### 5. Grafana datasource

`grafana/provisioning/datasources/loki.yml` provisions the Loki datasource automatically on Grafana startup. `url: http://loki:3100` (docker-compose). For Kubernetes, configure `http://loki.monitoring.svc.cluster.local:3100` via UI or ConfigMap patch.

### 6. prometheus.yml

Added `interbank-service` scrape target on port 9112 (the correct metrics port after the `INTERBANK_METRICS_PORT` → `METRICS_PORT` fix).

---

## Log Format

Every log line emitted by any service is a JSON object on a single line:

```json
{"time":"2026-06-09T00:21:39Z","level":"INFO","msg":"http_request","service":"api-gateway","request_id":"d8a272a3","method":"GET","path":"/api/v3/peer-banks","status":200,"latency_ms":42}
```

Fields common to all log lines:
- `time` — RFC3339 timestamp
- `level` — DEBUG / INFO / WARN / ERROR
- `msg` — human-readable message
- `service` — service name (set by `logger.Init`)

Additional fields are passed as key-value pairs at each call site.

---

## Key LogQL Queries

```logql
# All errors across the cluster
{namespace=~"project-exbanka-instance."} | json | level="ERROR"

# Trace a single request end-to-end
{namespace=~"project-exbanka-instance."} | json | request_id="<uuid>"

# Peer-banks requests only
{service="api-gateway"} | json | path="/api/v3/peer-banks"

# Stock-service OOM events
{service="stock-service"} | json | level="ERROR"
```

---

## Deployment

### docker-compose

```bash
docker compose up -d loki promtail
# Grafana datasource is provisioned automatically on next restart
docker compose restart grafana
```

### Kubernetes

```bash
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f k8s/logging/loki.yml
kubectl apply -f k8s/logging/promtail.yml
# See k8s/logging/README.md for Grafana datasource setup
```
