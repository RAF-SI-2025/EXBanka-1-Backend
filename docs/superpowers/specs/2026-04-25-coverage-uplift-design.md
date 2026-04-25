# Test Coverage Uplift — Design

## Goal

Raise statement coverage of the EXBanka backend monorepo to:

- **≥ 50% per microservice** on testable layers
- **≥ 80% project-wide** on testable layers (denominator excludes DB-dependent and non-logic code)

No tests may require a real database, Kafka broker, Redis instance, gRPC server, SMTP server, or other live infrastructure. All external dependencies are mocked.

## Current Baseline (2026-04-25)

Measured via `go test -covermode=set ./...` per service.

| Service | service/ | Total svc cov |
|---|---|---|
| api-gateway | n/a (uses handler/) | 4.73% |
| auth-service | 21.5% | 14.58% |
| account-service | 58.5% | 29.97% |
| card-service | 46.2% | 20.62% |
| client-service | 81.3% | 26.95% |
| credit-service | 49.2% | 23.13% |
| exchange-service | 79.0% | 38.30% |
| notification-service | 0% (no business logic in service/) | 19.69% |
| stock-service | 55.8% | 40.13% |
| transaction-service | 70.1% | 34.16% |
| user-service | 60.8% | 29.61% |
| verification-service | 14.7% | 11.69% |

Project total: **24.26%** (3,804 / 15,683 statements).

## Scope: Testable Layers

**In scope** (will write tests for):

- `internal/service/` — business logic, mock all external dependencies
- `internal/handler/` — gRPC and HTTP handlers, mock the services they call
- `internal/middleware/` — auth, logging, rate limiting (api-gateway)
- `internal/router/` — route registration tests
- `internal/grpc/` (api-gateway) — gRPC client wrappers, mock the underlying connection
- `internal/cache/` — Redis cache wrappers can be tested with `miniredis` (in-memory) — counts as no-DB
- `internal/kafka/producer.go` — message marshaling and topic selection (mock the writer)
- `internal/model/` — GORM hooks (`BeforeUpdate`, `BeforeCreate`), validation methods, computed fields
- `internal/source/`, `internal/provider/` (stock-service, exchange-service) — pure logic with HTTP/external clients mocked
- `internal/sender/` (notification-service) — SMTP composition logic with mocked transport
- `internal/consumer/` (notification-service, auth-service) — Kafka message handler logic with mocked dependencies
- `internal/push/` (notification-service) — push provider interface with mocked transport

**Out of scope** (excluded from numerator and denominator):

- `internal/repository/` — GORM queries against PostgreSQL; cannot be unit-tested without a DB
- `cmd/main.go` — process wiring, server startup, signal handling
- `internal/config/config.go` — env-var loading
- Generated code: `contract/*pb/`, `api-gateway/docs/`
- `internal/metrics/` — Prometheus registration boilerplate (zero logic)

## Mocking Strategy

Follow the existing pattern used in `client-service`, `transaction-service`, and `account-service`:

- **Hand-written mock structs** implementing each interface, defined in the same `_test.go` file or a `mocks_test.go` sibling. No new mock-generation tooling.
- For repository interfaces: each service's repository methods are accessed via interface in the service constructor; mocks return canned values and record calls.
- For gRPC clients: define a small interface around the methods the consumer uses, mock that interface. (Where services currently use the concrete generated client, refactor to an interface — minimal change.)
- For Kafka producers: define a `MessageWriter` interface; mock records messages.
- For SMTP: hand-written `Transport` interface; mock records sent emails.
- For HTTP-based providers (exchange API, stock data sources): mock with `httptest.NewServer`.
- For Redis: use `miniredis` (already in the dependency tree of `cache/` packages where present, otherwise add it).
- For time: inject `clock.Clock` interface where time-sensitive logic lives, default to `clock.New()`, use `clock.NewMock()` in tests.

## Execution Plan

Iterative, lowest-coverage-first. Each batch is one session and one or more commits.

### Pre-flight (Batch 0)

1. Fix failing test `TestCreateOrder_Forex_SellRejected` in `api-gateway/internal/handler/stock_order_handler_test.go:116` (validation order changed; expected message is now masked by an earlier required-field check). One commit.

### Batch 1 — worst three services

Targets: notification-service (0% in service/, focus on `consumer/`+`sender/`+`push/`), verification-service (14.7%), auth-service (21.5%).

Per service: read existing tests if any, identify uncovered logic functions via `go tool cover -func`, write table-driven tests with mocks, run `go test -cover` until ≥50%, commit.

### Batch 2 — middle band

Targets: card-service (46.2%), credit-service (49.2%), stock-service (55.8%, but huge — focus on uncovered service/handler files).

### Batch 3 — already-good services to push past 50% on testable totals

Targets: account-service, user-service, client-service, transaction-service, exchange-service. Most already exceed 50% in service/ but their handler/ and other layers are 0%. Add handler tests.

### Batch 4 — api-gateway

Largest single service (3,868 statements at 4.73%). Strategy:

- Test every handler with mocked gRPC clients.
- Test middleware (auth, request ID, CORS).
- Test router setup (route table assertions).
- Test gRPC client wrapper error mapping.

Realistic target: 50% gateway coverage. With 1,900+ statements covered, this single service moves the project total by ~12 percentage points.

### Batch 5 — top-up pass

Run final aggregate. If project total < 80% (excluding out-of-scope packages), identify the largest uncovered files and add targeted tests.

## Per-Batch Exit Criteria

- All affected tests pass (`make test` or per-service `go test ./...`).
- All affected packages pass lint (`make lint` or per-service `golangci-lint run ./...`).
- Each touched service is at ≥50% coverage on its testable packages.
- One commit per service inside the batch.

## Final Exit Criteria

- Every microservice ≥50% coverage on testable packages.
- Project aggregate ≥80% coverage on testable packages (denominator excludes the out-of-scope list above).
- Aggregated coverage report committed to `docs/superpowers/specs/2026-04-25-coverage-uplift-results.md` for traceability.

## Risks and Mitigations

- **Risk: refactoring services to use interfaces breaks production wiring.** Mitigation: every refactor preserves the existing concrete type as the default; interfaces are minimal (only the methods the consumer uses).
- **Risk: stock-service has the most code (~4,300 statements) and a complex InfluxDB writer + WebSocket sources.** Mitigation: focus on the higher-value pure-logic functions (forex fill, holding reservation, candle aggregation, listing service); leave WebSocket loops at lower coverage if they only contain network glue.
- **Risk: api-gateway 50% requires testing ~1,750 statements of handler code.** Mitigation: handler tests are highly templatable; one mock gateway-wide gRPC client struct can serve dozens of tests.
- **Risk: 80% project-wide may be infeasible if too many statements live in repository/cmd.** Mitigation: report after Batch 4; if infeasible, surface the gap to the user with concrete numbers rather than over-promising.

## Out of Scope

- Integration tests in `test-app/workflows/` (those exist separately and require docker-compose).
- New behaviour or bug fixes (other than the one failing test that blocks coverage runs).
- Lint cleanup of pre-existing issues.
- Changes to non-test production code beyond minimal interface extraction needed for mockability.