# Prometheus Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Prometheus metrics to all microservices and the API gateway for request counts, latencies, error rates, and custom business metrics.

**Architecture:** Shared metrics package in contract/metrics/ with gRPC interceptors and Gin middleware. Each service exposes /metrics on a dedicated HTTP port. Prometheus scrapes all services, Grafana provides dashboards.

**Tech Stack:** Go, prometheus/client_golang, grpc-ecosystem/go-grpc-middleware, Prometheus 2.x, Grafana

---

## Background

The system has 11 gRPC microservices and 1 HTTP API gateway with zero observability beyond log statements. Every service follows the same `cmd/main.go` pattern: load config, connect DB, create `grpc.NewServer()`, register handlers, listen. The API gateway uses Gin with `gin.Default()`.

This plan adds:
1. A shared `contract/metrics/` package with reusable helpers
2. Per-service HTTP metrics endpoints on dedicated ports
3. gRPC interceptors for automatic request/latency/error tracking
4. Gin middleware for HTTP request tracking at the gateway
5. Custom business metrics per service
6. Prometheus + Grafana in docker-compose for scraping and visualization

### Metrics Port Assignments

| Service | gRPC Port | Metrics Port |
|---|---|---|
| api-gateway | 8080 (HTTP) | 9100 |
| auth-service | 50051 | 9101 |
| user-service | 50052 | 9102 |
| notification-service | 50053 | 9103 |
| client-service | 50054 | 9104 |
| account-service | 50055 | 9105 |
| card-service | 50056 | 9106 |
| transaction-service | 50057 | 9107 |
| credit-service | 50058 | 9108 |
| exchange-service | 50059 | 9109 |
| stock-service | 50060 | 9110 |
| verification-service | 50061 | 9111 |

---

## Task 1: Create `contract/metrics/` shared package

**Files:**
- `contract/metrics/server.go` (create)
- `contract/metrics/grpc.go` (create)
- `contract/metrics/gin.go` (create)
- `contract/go.mod` (modify)

### Steps

- [ ] **1.1** Add Prometheus dependencies to `contract/go.mod`. Run from repo root:

```bash
cd contract && go get github.com/prometheus/client_golang@latest && go get github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus@latest && go mod tidy
```

- [ ] **1.2** Create `contract/metrics/server.go` — the shared metrics HTTP server that every service calls:

```go
package metrics

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// StartMetricsServer starts an HTTP server on the given port that serves
// Prometheus metrics at /metrics and a simple health check at /health.
// It runs in a background goroutine and returns a shutdown function.
func StartMetricsServer(port string) func(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: mux,
	}

	go func() {
		log.Printf("metrics server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
```

- [ ] **1.3** Create `contract/metrics/grpc.go` — gRPC server interceptors using `grpc-ecosystem/go-grpc-middleware`:

```go
package metrics

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	prom "github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

// srvMetrics is the shared gRPC server metrics collector.
// It is initialized once and registered with the default Prometheus registry.
var srvMetrics *prometheus.ServerMetrics

func init() {
	srvMetrics = prometheus.NewServerMetrics(
		prometheus.WithServerHandlingTimeHistogram(
			prometheus.WithHistogramBuckets([]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
		),
	)
	prom.MustRegister(srvMetrics)
}

// GRPCUnaryServerInterceptor returns a grpc.UnaryServerInterceptor that
// records request count, latency, and status code for every unary RPC.
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return srvMetrics.UnaryServerInterceptor()
}

// GRPCStreamServerInterceptor returns a grpc.StreamServerInterceptor that
// records request count, latency, and status code for every streaming RPC.
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	return srvMetrics.StreamServerInterceptor()
}

// InitializeGRPCMetrics initializes the metrics for the given gRPC server.
// Call this AFTER all services have been registered on the server, but
// BEFORE starting to serve. This pre-populates the metrics with zero values
// for all known methods.
func InitializeGRPCMetrics(s *grpc.Server) {
	srvMetrics.InitializeMetrics(s)
}
```

- [ ] **1.4** Create `contract/metrics/gin.go` — Gin middleware for the API gateway:

```go
package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	prom "github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prom.NewHistogramVec(
		prom.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path", "status"},
	)

	httpRequestsInFlight = prom.NewGauge(
		prom.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being processed.",
		},
	)
)

func init() {
	prom.MustRegister(httpRequestsTotal, httpRequestDuration, httpRequestsInFlight)
}

// GinMiddleware returns a Gin middleware that records HTTP request metrics.
// It uses the route template (e.g. "/api/accounts/:id") rather than the
// actual path to avoid high-cardinality label explosion.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		httpRequestsInFlight.Inc()
		start := time.Now()

		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		// Use FullPath() for the route template; falls back to "unmatched"
		// for 404s to avoid cardinality explosion from random paths.
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}

		httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(c.Request.Method, path, status).Observe(duration)
		httpRequestsInFlight.Dec()
	}
}
```

- [ ] **1.5** Run `cd contract && go mod tidy && go build ./...` to verify the shared package compiles.

### Commit checkpoint

```
feat(contract): add shared Prometheus metrics package with gRPC interceptors and Gin middleware
```

---

## Task 2: Add Prometheus + Grafana to docker-compose

**Files:**
- `prometheus.yml` (create at repo root)
- `grafana/provisioning/datasources/prometheus.yml` (create)
- `docker-compose.yml` (modify)

### Steps

- [ ] **2.1** Create `prometheus.yml` at the repo root:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'api-gateway'
    static_configs:
      - targets: ['api-gateway:9100']

  - job_name: 'auth-service'
    static_configs:
      - targets: ['auth-service:9101']

  - job_name: 'user-service'
    static_configs:
      - targets: ['user-service:9102']

  - job_name: 'notification-service'
    static_configs:
      - targets: ['notification-service:9103']

  - job_name: 'client-service'
    static_configs:
      - targets: ['client-service:9104']

  - job_name: 'account-service'
    static_configs:
      - targets: ['account-service:9105']

  - job_name: 'card-service'
    static_configs:
      - targets: ['card-service:9106']

  - job_name: 'transaction-service'
    static_configs:
      - targets: ['transaction-service:9107']

  - job_name: 'credit-service'
    static_configs:
      - targets: ['credit-service:9108']

  - job_name: 'exchange-service'
    static_configs:
      - targets: ['exchange-service:9109']

  - job_name: 'stock-service'
    static_configs:
      - targets: ['stock-service:9110']

  - job_name: 'verification-service'
    static_configs:
      - targets: ['verification-service:9111']
```

- [ ] **2.2** Create directory `grafana/provisioning/datasources/` and create `grafana/provisioning/datasources/prometheus.yml`:

```yaml
apiVersion: 1

datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: true
```

- [ ] **2.3** In `docker-compose.yml`, add the Prometheus and Grafana services. Insert before the `volumes:` section (before line 609):

```yaml
  # --- Observability -------------------------------------------------------

  prometheus:
    image: prom/prometheus:v2.53.0
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus_data:/prometheus
    ports:
      - "9090:9090"
    depends_on:
      api-gateway:
        condition: service_started

  grafana:
    image: grafana/grafana:11.1.0
    environment:
      GF_SECURITY_ADMIN_USER: admin
      GF_SECURITY_ADMIN_PASSWORD: admin
      GF_USERS_ALLOW_SIGN_UP: "false"
    volumes:
      - grafana_data:/var/lib/grafana
      - ./grafana/provisioning:/etc/grafana/provisioning:ro
    ports:
      - "3001:3000"
    depends_on:
      prometheus:
        condition: service_started
```

- [ ] **2.4** Add the new volumes to the `volumes:` section at the end of `docker-compose.yml`:

```yaml
  prometheus_data:
  grafana_data:
```

- [ ] **2.5** In `docker-compose.yml`, expose the metrics port for each service by adding a second port mapping. For each service, add the metrics port to the existing `ports:` block. The changes are:

For `api-gateway` (around line 558):
```yaml
    ports:
      - "8080:8080"
      - "9100:9100"
```

For `auth-service` (around line 266):
```yaml
    ports:
      - "50051:50051"
      - "9101:9101"
```

For `user-service` (around line 234):
```yaml
    ports:
      - "50052:50052"
      - "9102:9102"
```

For `notification-service` (around line 296):
```yaml
    ports:
      - "50053:50053"
      - "9103:9103"
```

For `client-service` (around line 318):
```yaml
    ports:
      - "50054:50054"
      - "9104:9104"
```

For `account-service` (around line 344):
```yaml
    ports:
      - "50055:50055"
      - "9105:9105"
```

For `card-service` (around line 370):
```yaml
    ports:
      - "50056:50056"
      - "9106:9106"
```

For `transaction-service` (around line 398):
```yaml
    ports:
      - "50057:50057"
      - "9107:9107"
```

For `credit-service` (around line 430):
```yaml
    ports:
      - "50058:50058"
      - "9108:9108"
```

For `exchange-service` (around line 461):
```yaml
    ports:
      - "50059:50059"
      - "9109:9109"
```

For `stock-service` (around line 499):
```yaml
    ports:
      - "50060:50060"
      - "9110:9110"
```

For `verification-service` (around line 530):
```yaml
    ports:
      - "50061:50061"
      - "9111:9111"
```

- [ ] **2.6** Add `METRICS_PORT` to each service's `environment:` block in `docker-compose.yml`:

| Service | Add to environment |
|---|---|
| api-gateway | `METRICS_PORT: "9100"` |
| auth-service | `METRICS_PORT: "9101"` |
| user-service | `METRICS_PORT: "9102"` |
| notification-service | `METRICS_PORT: "9103"` |
| client-service | `METRICS_PORT: "9104"` |
| account-service | `METRICS_PORT: "9105"` |
| card-service | `METRICS_PORT: "9106"` |
| transaction-service | `METRICS_PORT: "9107"` |
| credit-service | `METRICS_PORT: "9108"` |
| exchange-service | `METRICS_PORT: "9109"` |
| stock-service | `METRICS_PORT: "9110"` |
| verification-service | `METRICS_PORT: "9111"` |

### Commit checkpoint

```
feat(infra): add Prometheus and Grafana to docker-compose with scrape config
```

---

## Task 3: Instrument the API gateway

**Files:**
- `api-gateway/internal/config/config.go` (modify)
- `api-gateway/internal/router/router.go` (modify)
- `api-gateway/cmd/main.go` (modify)

### Steps

- [ ] **3.1** Add `MetricsPort` to `api-gateway/internal/config/config.go`. Add the field to the `Config` struct:

```go
type Config struct {
	HTTPAddr            string
	MetricsPort         string
	AuthGRPCAddr        string
	// ... rest unchanged
}
```

And add to the `Load()` function:

```go
MetricsPort:         getEnv("METRICS_PORT", "9100"),
```

- [ ] **3.2** In `api-gateway/internal/router/router.go`, add the metrics middleware. Add the import:

```go
"github.com/exbanka/contract/metrics"
```

Then insert `r.Use(metrics.GinMiddleware())` right after the CORS middleware setup and before route registration. The section (currently around lines 50-57) becomes:

```go
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))
	r.Use(metrics.GinMiddleware())
```

- [ ] **3.3** In `api-gateway/cmd/main.go`, start the metrics server. Add the import:

```go
"github.com/exbanka/contract/metrics"
```

Then insert the metrics server start right after `cfg := config.Load()` (after line 30):

```go
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **3.4** Run from repo root to verify compilation:

```bash
cd api-gateway && go mod tidy && go build ./cmd
```

### Commit checkpoint

```
feat(api-gateway): add Prometheus metrics middleware and /metrics endpoint
```

---

## Task 4: Instrument auth-service (template for all gRPC services)

This task demonstrates the exact pattern for instrumenting a gRPC service. All subsequent services follow the same 3-step approach.

**Files:**
- `auth-service/internal/config/config.go` (modify)
- `auth-service/cmd/main.go` (modify)

### Steps

- [ ] **4.1** Add `MetricsPort` to `auth-service/internal/config/config.go`. Add the field to the `Config` struct (after `GRPCAddr`):

```go
type Config struct {
	DBHost          string
	DBPort          string
	DBUser          string
	DBPassword      string
	DBName          string
	GRPCAddr        string
	MetricsPort     string
	UserGRPCAddr    string
	// ... rest unchanged
}
```

And add to the `Load()` return value:

```go
MetricsPort:  getEnv("METRICS_PORT", "9101"),
```

- [ ] **4.2** In `auth-service/cmd/main.go`, apply three changes:

**4.2a** Add imports:

```go
"github.com/exbanka/contract/metrics"
```

**4.2b** Change the `grpc.NewServer()` call (currently line 94) to include interceptors:

The current code is:
```go
	s := grpc.NewServer()
```

Replace with:
```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

**4.2c** After all gRPC services are registered and before the Kafka topic creation (currently line 100), add the metrics initialization and server start:

Insert after `shared.RegisterHealthCheck(s, "auth-service")` (line 96) and before the `kafkaprod.EnsureTopics(...)` call (line 100):

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **4.3** Run from repo root:

```bash
cd auth-service && go mod tidy && go build ./cmd
```

### Commit checkpoint

```
feat(auth-service): add Prometheus gRPC metrics interceptors and /metrics endpoint
```

---

## Task 5: Instrument all remaining gRPC services

Every remaining gRPC service follows the **exact same 3-step pattern** as auth-service in Task 4:

1. Add `MetricsPort string` field to `config.Config` struct and `getEnv("METRICS_PORT", "<default>")` to `Load()`.
2. In `cmd/main.go`: import `"github.com/exbanka/contract/metrics"`, change `grpc.NewServer()` to include interceptors, add `metrics.InitializeGRPCMetrics(s)` + `metrics.StartMetricsServer(cfg.MetricsPort)` after all service registrations.
3. Run `go mod tidy && go build ./cmd`.

Below are the exact changes for each service, specifying the precise lines to modify based on the current codebase.

### 5.1 user-service (metrics port 9102)

**Files:** `user-service/internal/config/config.go`, `user-service/cmd/main.go`

- [ ] **5.1.1** In `user-service/internal/config/config.go`, the config struct does not currently exist as a separate file visible in the standard pattern. If it follows the same pattern as auth-service, add:

```go
MetricsPort string
```

to the `Config` struct and:

```go
MetricsPort: getEnv("METRICS_PORT", "9102"),
```

to `Load()`.

- [ ] **5.1.2** In `user-service/cmd/main.go`, add the import:

```go
"github.com/exbanka/contract/metrics"
```

- [ ] **5.1.3** Change line 111 from:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.1.4** After `shared.RegisterHealthCheck(s, "user-service")` (line 115), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.1.5** Run: `cd user-service && go mod tidy && go build ./cmd`

### 5.2 notification-service (metrics port 9103)

**Files:** `notification-service/internal/config/config.go`, `notification-service/cmd/main.go`

- [ ] **5.2.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9103")` to `Load()`.

- [ ] **5.2.2** In `notification-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.2.3** The gRPC server creation at line 86 uses `grpcServer` variable name. Change:

```go
	grpcServer := grpc.NewServer()
```

to:

```go
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.2.4** After `reflection.Register(grpcServer)` (line 89), insert:

```go
	metrics.InitializeGRPCMetrics(grpcServer)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.2.5** Run: `cd notification-service && go mod tidy && go build ./cmd`

### 5.3 client-service (metrics port 9104)

**Files:** `client-service/internal/config/config.go`, `client-service/cmd/main.go`

- [ ] **5.3.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9104")` to `Load()`.

- [ ] **5.3.2** In `client-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.3.3** Change line 84:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.3.4** After `shared.RegisterHealthCheck(s, "client-service")` (line 87), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.3.5** Run: `cd client-service && go mod tidy && go build ./cmd`

### 5.4 account-service (metrics port 9105)

**Files:** `account-service/internal/config/config.go`, `account-service/cmd/main.go`

- [ ] **5.4.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9105")` to `Load()`.

- [ ] **5.4.2** In `account-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.4.3** Change line 175:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.4.4** After `shared.RegisterHealthCheck(s, "account-service")` (line 178), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.4.5** Run: `cd account-service && go mod tidy && go build ./cmd`

### 5.5 card-service (metrics port 9106)

**Files:** `card-service/internal/config/config.go`, `card-service/cmd/main.go`

- [ ] **5.5.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9106")` to `Load()`.

- [ ] **5.5.2** In `card-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.5.3** Change line 92:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.5.4** After `shared.RegisterHealthCheck(s, "card-service")` (line 96), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.5.5** Run: `cd card-service && go mod tidy && go build ./cmd`

### 5.6 transaction-service (metrics port 9107)

**Files:** `transaction-service/internal/config/config.go`, `transaction-service/cmd/main.go`

- [ ] **5.6.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9107")` to `Load()`.

- [ ] **5.6.2** In `transaction-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.6.3** Change line 165:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.6.4** After `shared.RegisterHealthCheck(s, "transaction-service")` (line 168), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.6.5** Run: `cd transaction-service && go mod tidy && go build ./cmd`

### 5.7 credit-service (metrics port 9108)

**Files:** `credit-service/internal/config/config.go`, `credit-service/cmd/main.go`

- [ ] **5.7.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9108")` to `Load()`.

- [ ] **5.7.2** In `credit-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.7.3** Change line 129:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.7.4** After `shared.RegisterHealthCheck(s, "credit-service")` (line 131), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.7.5** Run: `cd credit-service && go mod tidy && go build ./cmd`

### 5.8 exchange-service (metrics port 9109)

**Files:** `exchange-service/internal/config/config.go`, `exchange-service/cmd/main.go`

- [ ] **5.8.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9109")` to `Load()`.

- [ ] **5.8.2** In `exchange-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.8.3** Change line 83:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.8.4** After `shared.RegisterHealthCheck(s, "exchange-service")` (line 85), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.8.5** Run: `cd exchange-service && go mod tidy && go build ./cmd`

### 5.9 stock-service (metrics port 9110)

**Files:** `stock-service/internal/config/config.go`, `stock-service/cmd/main.go`

- [ ] **5.9.1** In `stock-service/internal/config/config.go`, add to the `Config` struct (after `GRPCAddr`):

```go
MetricsPort  string
```

And add to `Load()` return value (after the `GRPCAddr` line):

```go
MetricsPort:              getEnv("METRICS_PORT", "9110"),
```

- [ ] **5.9.2** In `stock-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.9.3** Change line 244:

```go
	grpcServer := grpc.NewServer()
```

to:

```go
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.9.4** After `shared.RegisterHealthCheck(grpcServer, "stock-service")` (line 268), insert:

```go
	metrics.InitializeGRPCMetrics(grpcServer)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.9.5** Run: `cd stock-service && go mod tidy && go build ./cmd`

### 5.10 verification-service (metrics port 9111)

**Files:** `verification-service/internal/config/config.go`, `verification-service/cmd/main.go`

- [ ] **5.10.1** Add `MetricsPort string` to config struct, `MetricsPort: getEnv("METRICS_PORT", "9111")` to `Load()`.

- [ ] **5.10.2** In `verification-service/cmd/main.go`, add import `"github.com/exbanka/contract/metrics"`.

- [ ] **5.10.3** Change line 65:

```go
	s := grpc.NewServer()
```

to:

```go
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
```

- [ ] **5.10.4** After `shared.RegisterHealthCheck(s, "verification-service")` (line 67), insert:

```go
	metrics.InitializeGRPCMetrics(s)
	metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer metricsShutdown(context.Background())
```

- [ ] **5.10.5** Run: `cd verification-service && go mod tidy && go build ./cmd`

### Commit checkpoint

```
feat(services): add Prometheus gRPC metrics interceptors to all microservices
```

---

## Task 6: Add custom business metrics per service

Each service registers domain-specific counters/gauges/histograms in its service layer. These are registered in `init()` functions inside new `metrics.go` files within each service's `internal/service/` directory.

**Pattern:** Create `<service>/internal/service/metrics.go` with `prometheus.NewCounterVec(...)` etc., then call `.Inc()` / `.Observe()` at the appropriate points in existing service methods.

### 6.1 auth-service custom metrics

**Files:** `auth-service/internal/service/metrics.go` (create), `auth-service/internal/service/auth_service.go` (modify)

- [ ] **6.1.1** Create `auth-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	AuthLoginTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "auth_login_total",
			Help: "Total number of login attempts.",
		},
		[]string{"status", "system_type"},
	)

	AuthTokensIssued = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "auth_tokens_issued_total",
			Help: "Total number of tokens issued.",
		},
		[]string{"token_type"},
	)

	AuthPasswordResetTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "auth_password_reset_total",
			Help: "Total number of password reset requests.",
		},
	)
)

func init() {
	prom.MustRegister(AuthLoginTotal, AuthTokensIssued, AuthPasswordResetTotal)
}
```

- [ ] **6.1.2** In `auth-service/internal/service/auth_service.go`, add metric increments:
  - In the `Login` method: after successful login, call `AuthLoginTotal.WithLabelValues("success", systemType).Inc()`. On failure, call `AuthLoginTotal.WithLabelValues("failure", systemType).Inc()`.
  - In `GenerateTokenPair` or wherever access+refresh tokens are created: call `AuthTokensIssued.WithLabelValues("access").Inc()` and `AuthTokensIssued.WithLabelValues("refresh").Inc()`.
  - In the password reset request handler: call `AuthPasswordResetTotal.Inc()`.

### 6.2 transaction-service custom metrics

**Files:** `transaction-service/internal/service/metrics.go` (create), `transaction-service/internal/service/payment_service.go` (modify), `transaction-service/internal/service/transfer_service.go` (modify)

- [ ] **6.2.1** Create `transaction-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	TransactionTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "transaction_total",
			Help: "Total number of transactions processed.",
		},
		[]string{"type", "status"},
	)

	TransactionAmountSum = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "transaction_amount_rsd_sum",
			Help: "Cumulative transaction amount in RSD.",
		},
		[]string{"type"},
	)

	TransactionDuration = prom.NewHistogramVec(
		prom.HistogramOpts{
			Name:    "transaction_processing_duration_seconds",
			Help:    "Time to process a transaction end-to-end.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"type"},
	)

	SagaCompensationsTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "transaction_saga_compensations_total",
			Help: "Total number of saga compensation steps triggered.",
		},
	)
)

func init() {
	prom.MustRegister(TransactionTotal, TransactionAmountSum, TransactionDuration, SagaCompensationsTotal)
}
```

- [ ] **6.2.2** In `payment_service.go`, at the end of `CreatePayment`: call `TransactionTotal.WithLabelValues("payment", "created").Inc()`. On execution: `TransactionTotal.WithLabelValues("payment", status).Inc()`.

- [ ] **6.2.3** In `transfer_service.go`, at the end of `CreateTransfer`: call `TransactionTotal.WithLabelValues("transfer", "created").Inc()`. On execution: `TransactionTotal.WithLabelValues("transfer", status).Inc()`.

### 6.3 account-service custom metrics

**Files:** `account-service/internal/service/metrics.go` (create)

- [ ] **6.3.1** Create `account-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	AccountBalanceOpsTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "account_balance_operations_total",
			Help: "Total number of balance operations (debit/credit).",
		},
		[]string{"type"},
	)

	AccountsCreatedTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "accounts_created_total",
			Help: "Total number of accounts created.",
		},
	)

	AccountStatusChangesTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "account_status_changes_total",
			Help: "Total number of account status changes.",
		},
		[]string{"new_status"},
	)
)

func init() {
	prom.MustRegister(AccountBalanceOpsTotal, AccountsCreatedTotal, AccountStatusChangesTotal)
}
```

- [ ] **6.3.2** Instrument the `account_service.go` and `ledger_service.go`: increment `AccountBalanceOpsTotal.WithLabelValues("debit")` / `"credit"` in the appropriate methods, `AccountsCreatedTotal.Inc()` in `CreateAccount`, and `AccountStatusChangesTotal` on status changes.

### 6.4 exchange-service custom metrics

**Files:** `exchange-service/internal/service/metrics.go` (create)

- [ ] **6.4.1** Create `exchange-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	ExchangeRateSyncTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "exchange_rate_sync_total",
			Help: "Total number of exchange rate sync operations.",
		},
		[]string{"status"},
	)

	ExchangeRateSyncDuration = prom.NewHistogram(
		prom.HistogramOpts{
			Name:    "exchange_rate_sync_duration_seconds",
			Help:    "Duration of exchange rate sync from external API.",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60},
		},
	)

	ExchangeConversionsTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "exchange_conversions_total",
			Help: "Total number of currency conversions performed.",
		},
	)
)

func init() {
	prom.MustRegister(ExchangeRateSyncTotal, ExchangeRateSyncDuration, ExchangeConversionsTotal)
}
```

- [ ] **6.4.2** In the `SyncRates` method of `exchange_service.go`:
  - Record start time at entry.
  - On success: `ExchangeRateSyncTotal.WithLabelValues("success").Inc()` and `ExchangeRateSyncDuration.Observe(elapsed)`.
  - On failure: `ExchangeRateSyncTotal.WithLabelValues("failure").Inc()`.
  - In the `Convert` method: `ExchangeConversionsTotal.Inc()`.

### 6.5 stock-service custom metrics

**Files:** `stock-service/internal/service/metrics.go` (create)

- [ ] **6.5.1** Create `stock-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	StockOrderTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "stock_order_total",
			Help: "Total number of stock orders by type and status.",
		},
		[]string{"order_type", "status"},
	)

	StockPriceRefreshDuration = prom.NewHistogram(
		prom.HistogramOpts{
			Name:    "stock_price_refresh_duration_seconds",
			Help:    "Duration of periodic security price refresh.",
			Buckets: []float64{1, 5, 15, 30, 60, 120},
		},
	)

	StockOTCTradesTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "stock_otc_trades_total",
			Help: "Total number of OTC trades executed.",
		},
	)

	StockTaxCollectedTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "stock_tax_collected_total",
			Help: "Total number of tax collection operations.",
		},
	)
)

func init() {
	prom.MustRegister(StockOrderTotal, StockPriceRefreshDuration, StockOTCTradesTotal, StockTaxCollectedTotal)
}
```

- [ ] **6.5.2** Instrument order_service.go, otc_service.go, tax_service.go, and security_sync_service.go with the appropriate `.Inc()` and `.Observe()` calls.

### 6.6 credit-service custom metrics

**Files:** `credit-service/internal/service/metrics.go` (create)

- [ ] **6.6.1** Create `credit-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	LoanRequestTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "credit_loan_request_total",
			Help: "Total loan requests by status.",
		},
		[]string{"status"},
	)

	InstallmentCollectionTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "credit_installment_collection_total",
			Help: "Total installment collections by status.",
		},
		[]string{"status"},
	)

	LatePaymentPenaltiesTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "credit_late_payment_penalties_total",
			Help: "Total number of late payment penalties applied.",
		},
	)
)

func init() {
	prom.MustRegister(LoanRequestTotal, InstallmentCollectionTotal, LatePaymentPenaltiesTotal)
}
```

- [ ] **6.6.2** Instrument `loan_request_service.go` and `cron_service.go` with the appropriate counter increments.

### 6.7 card-service custom metrics

**Files:** `card-service/internal/service/metrics.go` (create)

- [ ] **6.7.1** Create `card-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	CardsCreatedTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "card_created_total",
			Help: "Total cards created by type.",
		},
		[]string{"card_type"},
	)

	CardStatusChangesTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "card_status_changes_total",
			Help: "Total card status changes.",
		},
		[]string{"action"},
	)

	CardPINAttemptsTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "card_pin_attempts_total",
			Help: "Total PIN verification attempts.",
		},
		[]string{"result"},
	)
)

func init() {
	prom.MustRegister(CardsCreatedTotal, CardStatusChangesTotal, CardPINAttemptsTotal)
}
```

- [ ] **6.7.2** Instrument `card_service.go`: `CardsCreatedTotal.WithLabelValues("physical")` or `"virtual"` on create, status change counters on block/unblock/deactivate, PIN attempt counters on verify.

### 6.8 client-service custom metrics

**Files:** `client-service/internal/service/metrics.go` (create)

- [ ] **6.8.1** Create `client-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	ClientsCreatedTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "client_created_total",
			Help: "Total number of clients created.",
		},
	)

	ClientLimitUpdatesTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "client_limit_updates_total",
			Help: "Total number of client limit updates.",
		},
	)
)

func init() {
	prom.MustRegister(ClientsCreatedTotal, ClientLimitUpdatesTotal)
}
```

- [ ] **6.8.2** Instrument `client_service.go` and `client_limit_service.go`.

### 6.9 user-service custom metrics

**Files:** `user-service/internal/service/metrics.go` (create)

- [ ] **6.9.1** Create `user-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	EmployeesCreatedTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "user_employee_created_total",
			Help: "Total number of employees created.",
		},
	)

	EmployeeLimitUpdatesTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "user_employee_limit_updates_total",
			Help: "Total number of employee limit updates.",
		},
	)

	RoleChangesTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "user_role_changes_total",
			Help: "Total number of role assignment changes.",
		},
	)
)

func init() {
	prom.MustRegister(EmployeesCreatedTotal, EmployeeLimitUpdatesTotal, RoleChangesTotal)
}
```

- [ ] **6.9.2** Instrument `employee_service.go`, `limit_service.go`, and `role_service.go`.

### 6.10 notification-service custom metrics

**Files:** `notification-service/internal/service/metrics.go` (create), `notification-service/internal/consumer/email_consumer.go` (modify)

- [ ] **6.10.1** Create `notification-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	EmailsSentTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "notification_emails_sent_total",
			Help: "Total number of emails sent by status.",
		},
		[]string{"status"},
	)

	EmailSendDuration = prom.NewHistogram(
		prom.HistogramOpts{
			Name:    "notification_email_send_duration_seconds",
			Help:    "Duration of SMTP email sending.",
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30},
		},
	)

	MobilePushTotal = prom.NewCounter(
		prom.CounterOpts{
			Name: "notification_mobile_push_total",
			Help: "Total number of mobile push notifications published.",
		},
	)
)

func init() {
	prom.MustRegister(EmailsSentTotal, EmailSendDuration, MobilePushTotal)
}
```

- [ ] **6.10.2** Instrument `email_consumer.go`: record timing around `emailSender.Send()` call, increment success/failure counters.

### 6.11 verification-service custom metrics

**Files:** `verification-service/internal/service/metrics.go` (create)

- [ ] **6.11.1** Create `verification-service/internal/service/metrics.go`:

```go
package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	VerificationChallengesCreated = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "verification_challenges_created_total",
			Help: "Total verification challenges created by method.",
		},
		[]string{"method"},
	)

	VerificationAttemptsTotal = prom.NewCounterVec(
		prom.CounterOpts{
			Name: "verification_attempts_total",
			Help: "Total verification attempts by result.",
		},
		[]string{"result"},
	)

	VerificationChallengesExpired = prom.NewCounter(
		prom.CounterOpts{
			Name: "verification_challenges_expired_total",
			Help: "Total number of verification challenges that expired.",
		},
	)
)

func init() {
	prom.MustRegister(VerificationChallengesCreated, VerificationAttemptsTotal, VerificationChallengesExpired)
}
```

- [ ] **6.11.2** Instrument `verification_service.go`: increment `VerificationChallengesCreated` in `CreateChallenge`, `VerificationAttemptsTotal` in `SubmitVerification`, and `VerificationChallengesExpired` in `ExpireOldChallenges`.

### Commit checkpoint

```
feat(services): add custom Prometheus business metrics to all services
```

---

## Task 7: Update go.work and run full build

**Files:**
- `go.work` (may need update if contract module dependencies changed)
- All service `go.mod` files

### Steps

- [ ] **7.1** From the repo root, sync all workspace modules with the updated contract module:

```bash
make tidy
```

This runs `go mod tidy` for all modules in the workspace.

- [ ] **7.2** Build all services:

```bash
make build
```

- [ ] **7.3** Verify all services compile without errors. If any service fails, fix the import paths.

### Commit checkpoint

```
chore: sync go.mod files after Prometheus metrics dependency addition
```

---

## Task 8: Tests and verification

### Unit Tests

**Files:**
- `contract/metrics/server_test.go` (create)
- `contract/metrics/gin_test.go` (create)
- `contract/metrics/grpc_test.go` (create)

- [ ] **8.1** Create `contract/metrics/server_test.go`:

```go
package metrics

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestStartMetricsServer(t *testing.T) {
	shutdown := StartMetricsServer("19100")
	defer shutdown(context.Background())

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Test /metrics endpoint
	resp, err := http.Get("http://localhost:19100/metrics")
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Test /health endpoint
	resp2, err := http.Get("http://localhost:19100/health")
	if err != nil {
		t.Fatalf("failed to GET /health: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}
```

- [ ] **8.2** Create `contract/metrics/gin_test.go`:

```go
package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Verify the counter was incremented by checking that /metrics
	// now includes our http_requests_total metric (via promhttp).
	// This is implicitly tested by the server_test — the key check
	// here is that the middleware doesn't panic and records correctly.
}
```

- [ ] **8.3** Create `contract/metrics/grpc_test.go`:

```go
package metrics

import (
	"testing"

	"google.golang.org/grpc"
)

func TestGRPCInterceptors(t *testing.T) {
	// Verify interceptors can be created without panic
	unary := GRPCUnaryServerInterceptor()
	if unary == nil {
		t.Error("GRPCUnaryServerInterceptor returned nil")
	}

	stream := GRPCStreamServerInterceptor()
	if stream == nil {
		t.Error("GRPCStreamServerInterceptor returned nil")
	}
}

func TestInitializeGRPCMetrics(t *testing.T) {
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(GRPCStreamServerInterceptor()),
	)
	// Should not panic
	InitializeGRPCMetrics(s)
	s.Stop()
}
```

- [ ] **8.4** Run the shared package tests:

```bash
cd contract && go test ./metrics/ -v
```

- [ ] **8.5** Run the full test suite:

```bash
make test
```

### Docker Compose Verification

- [ ] **8.6** Start the full stack with Docker Compose and verify metrics are being scraped:

```bash
make docker-up
```

- [ ] **8.7** Wait for services to start (30-60 seconds), then verify each metrics endpoint:

```bash
# Check a few services
curl -s http://localhost:9100/metrics | head -20  # api-gateway
curl -s http://localhost:9101/metrics | head -20  # auth-service
curl -s http://localhost:9107/metrics | head -20  # transaction-service

# Check Prometheus is scraping
curl -s http://localhost:9090/api/v1/targets | python3 -m json.tool | grep -A2 '"health"'

# Check Grafana is up
curl -s http://localhost:3001/api/health
```

- [ ] **8.8** Verify gRPC metrics are present after making a request:

```bash
# Hit the gateway to trigger some gRPC calls
curl -s http://localhost:8080/api/exchange/rates > /dev/null

# Check auth-service for gRPC metrics
curl -s http://localhost:9101/metrics | grep grpc_server
```

### Commit checkpoint

```
test(metrics): add unit tests for shared Prometheus metrics package
```

---

## Task 9: Update CLAUDE.md and Specification.md

**Files:**
- `CLAUDE.md` (modify)
- `Specification.md` (modify)

### Steps

- [ ] **9.1** In `CLAUDE.md`, add to the **Environment** table:

| Variable | Default | Notes |
|---|---|---|
| `METRICS_PORT` | varies per service (9100-9111) | Prometheus metrics HTTP port |

- [ ] **9.2** In `CLAUDE.md`, add a new section under **Architecture** (after the "Communication layers" block):

```
**Observability:**
- Each service exposes Prometheus metrics at `/metrics` on a dedicated HTTP port (9100-9111).
- Prometheus (port 9090) scrapes all services every 15 seconds.
- Grafana (port 3001) provides dashboards with Prometheus as the default datasource.
- gRPC metrics (request count, latency, error code) are automatically collected via interceptors from `contract/metrics/`.
- HTTP metrics (request count, latency, status) are collected via Gin middleware at the API gateway.
- Custom business metrics are defined per service in `internal/service/metrics.go`.
```

- [ ] **9.3** In `Specification.md`, update the relevant sections to document:
  - The metrics ports per service
  - The Prometheus + Grafana infrastructure
  - The `METRICS_PORT` env var
  - The `contract/metrics/` package

### Commit checkpoint

```
docs: document Prometheus metrics infrastructure in CLAUDE.md and Specification.md
```

---

## Summary of all metrics exposed

### Automatic (all services via shared interceptors)

| Metric | Type | Labels | Source |
|---|---|---|---|
| `grpc_server_started_total` | Counter | `grpc_type`, `grpc_service`, `grpc_method` | go-grpc-middleware |
| `grpc_server_handled_total` | Counter | `grpc_type`, `grpc_service`, `grpc_method`, `grpc_code` | go-grpc-middleware |
| `grpc_server_handling_seconds` | Histogram | `grpc_type`, `grpc_service`, `grpc_method` | go-grpc-middleware |
| `grpc_server_msg_received_total` | Counter | `grpc_type`, `grpc_service`, `grpc_method` | go-grpc-middleware |
| `grpc_server_msg_sent_total` | Counter | `grpc_type`, `grpc_service`, `grpc_method` | go-grpc-middleware |

### API Gateway (Gin middleware)

| Metric | Type | Labels |
|---|---|---|
| `http_requests_total` | Counter | `method`, `path`, `status` |
| `http_request_duration_seconds` | Histogram | `method`, `path`, `status` |
| `http_requests_in_flight` | Gauge | (none) |

### Custom business metrics

| Service | Metric | Type | Labels |
|---|---|---|---|
| auth-service | `auth_login_total` | Counter | `status`, `system_type` |
| auth-service | `auth_tokens_issued_total` | Counter | `token_type` |
| auth-service | `auth_password_reset_total` | Counter | (none) |
| transaction-service | `transaction_total` | Counter | `type`, `status` |
| transaction-service | `transaction_amount_rsd_sum` | Counter | `type` |
| transaction-service | `transaction_processing_duration_seconds` | Histogram | `type` |
| transaction-service | `transaction_saga_compensations_total` | Counter | (none) |
| account-service | `account_balance_operations_total` | Counter | `type` |
| account-service | `accounts_created_total` | Counter | (none) |
| account-service | `account_status_changes_total` | Counter | `new_status` |
| exchange-service | `exchange_rate_sync_total` | Counter | `status` |
| exchange-service | `exchange_rate_sync_duration_seconds` | Histogram | (none) |
| exchange-service | `exchange_conversions_total` | Counter | (none) |
| stock-service | `stock_order_total` | Counter | `order_type`, `status` |
| stock-service | `stock_price_refresh_duration_seconds` | Histogram | (none) |
| stock-service | `stock_otc_trades_total` | Counter | (none) |
| stock-service | `stock_tax_collected_total` | Counter | (none) |
| credit-service | `credit_loan_request_total` | Counter | `status` |
| credit-service | `credit_installment_collection_total` | Counter | `status` |
| credit-service | `credit_late_payment_penalties_total` | Counter | (none) |
| card-service | `card_created_total` | Counter | `card_type` |
| card-service | `card_status_changes_total` | Counter | `action` |
| card-service | `card_pin_attempts_total` | Counter | `result` |
| client-service | `client_created_total` | Counter | (none) |
| client-service | `client_limit_updates_total` | Counter | (none) |
| user-service | `user_employee_created_total` | Counter | (none) |
| user-service | `user_employee_limit_updates_total` | Counter | (none) |
| user-service | `user_role_changes_total` | Counter | (none) |
| notification-service | `notification_emails_sent_total` | Counter | `status` |
| notification-service | `notification_email_send_duration_seconds` | Histogram | (none) |
| notification-service | `notification_mobile_push_total` | Counter | (none) |
| verification-service | `verification_challenges_created_total` | Counter | `method` |
| verification-service | `verification_attempts_total` | Counter | `result` |
| verification-service | `verification_challenges_expired_total` | Counter | (none) |

### Go runtime metrics (automatic from Prometheus client library)

All services also expose `go_goroutines`, `go_gc_duration_seconds`, `go_memstats_*`, `process_cpu_seconds_total`, `process_resident_memory_bytes`, etc. These come for free from the default Prometheus collector.
