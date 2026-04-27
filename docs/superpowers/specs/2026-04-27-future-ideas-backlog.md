# Future Ideas Backlog (Spec F)

**Status:** Tracked, not planned
**Date:** 2026-04-27
**Scope:** This is NOT an implementation plan. It is a list of architectural improvements deferred from the 2026-04-27 cleanup round. Each entry has a title, observed evidence, severity, and rough effort. Pick items off this list deliberately when capacity allows.

## Items deferred from the original 7 recommendations

### F1. Test fixture pool for `setupActivatedClient`

**Originally:** Recommendation #6 in the systems-designer review.

**Evidence:** `setupActivatedClient` (`test-app/workflows/helpers_test.go:186-217`) does 4 sequential RPCs + a Kafka scan with up-to-15-second timeout = ~20s per test. Used in 41 test files. Suite has 332 tests, 262 marked `t.Parallel()`.

**Original objection (preserved from MEMORY):** "Production cannot have pre-seeded fake clients."

**Resolution path if revisited:** Pool lives in a test-only DB schema, never touches production seeders. The pool's lifecycle is bound to `TestMain`. Each pool entry is reserved by exactly one test at a time and returned at test end (or torn down on suite end).

**Severity:** Medium (developer ergonomics — slow CI cycle hurts iteration)
**Effort:** Medium

### F2. Cross-service contract test layer

**Originally:** Recommendation #7 in the systems-designer review.

**Evidence:** Phase 3's actuary-limit regression slipped past unit tests because `api-gateway` handler tests stubbed downstream gRPC services. The bug was in the *contract* between gateway and stock-service — the gateway sent the wrong identity, stock-service had no way to detect it from a stub.

**Original objection (preserved from MEMORY):** "gRPC integration tests don't validate REST routes."

**Resolution path if revisited:** The objection partially holds — `test-app/workflows/` already covers REST routes end-to-end. But it does not cover the api-gateway-handler-level contract surface in isolation. Use `testcontainers-go` to boot api-gateway + one downstream service (e.g., stock-service) and exercise the gRPC contract directly. Limit to high-risk paths (anything involving identity, permissions, or money movement).

**Severity:** Medium (catches a class of bug we have hit)
**Effort:** Large (new test infrastructure)

## Items surfaced by the codebase audit

### F3. Structured logging + tracing + correlation IDs

**Evidence:** Services use bare `log.Println` / `log.Printf` / `log.Fatalf` (`account-service/cmd/main.go:35-42` and every `cmd/main.go`). 942 `context.Context` parameters but no correlation IDs propagated. No OpenTelemetry. Prometheus metrics exist (`contract/metrics/grpc.go`) but each service redefines metrics independently.

**Severity:** Medium (debugging is hard; cross-service tracing is impossible)
**Effort:** Medium — consolidate to `contract/shared/logging`, add request-ID propagation through gRPC metadata (Spec B2's saga context interceptor is a precedent), unify metrics registry.

### F4. AutoMigrate → explicit migration tool

**Evidence:** All 10 services call `db.AutoMigrate(...)` on startup (`account-service/cmd/main.go:37`, every other service). CLAUDE.md acknowledges this. In production, concurrent service restarts race on schema changes. No rollback, no audit trail, no explicit schema versioning.

**Severity:** **High** (banking compliance and operational risk for any non-pre-prod environment)
**Effort:** Large — integrate `golang-migrate` or `atlas`, add migration runner per service, define migration generation workflow, audit each service's existing schema and capture as v1 migrations.

### F5. Configuration centralization

**Evidence:** Each service implements its own `config.go` with identical `getEnv()` helper (~11 copies). gRPC addresses duplicated: `ClientGRPCAddr` referenced in 6 services, `AccountGRPCAddr` in 4, `AuthGRPCAddr` in 2. api-gateway manually wires 11 gRPC addresses. No config validation at load time.

**Severity:** Low (annoying, not buggy)
**Effort:** Medium — move base config to `contract/shared/config`, implement startup validator, generate gRPC client registry from a single source.

### F6. Repository CRUD boilerplate extraction

**Evidence:** 15+ repositories follow identical CRUD patterns (`Create`, `GetByID`, `List`, `Update`) across 81 model files. Gateway handlers `transaction_handler.go` (1214 LOC), `card_handler.go` (971 LOC), `credit_handler.go` (897 LOC) repeat error mapping and request validation.

**Severity:** Low (maintenance burden, not bugs)
**Effort:** Large — generic repository pattern requires careful design (Go generics or codegen). Splitting handlers is straightforward but mechanical.

### F7. Saga step Prometheus metrics

**Evidence:** `shared.Saga` has no built-in observability. Operators have no view of `saga_step_duration_seconds` or `saga_failures_total{step="..."}`.

**Severity:** Low (Spec B's typed StepKind makes this trivial to add later)
**Effort:** Small — `shared.Saga` records metrics around each step's `Forward` and `Backward` execution.

### F8. Kafka consumer DLQ + retry policy

**Evidence:** Each service's Kafka consumer implements error handling independently. No Dead Letter Queue topic, no retry policy abstraction, no poison-pill detection. Producer side is unified (`shared.EnsureTopics`); consumer side is not.

**Severity:** Medium (silent message loss is possible today)
**Effort:** Medium — add DLQ topic pattern to `contract/kafka/`, implement consumer error handler with exponential backoff and DLQ overflow.

### F9. Composite gateway health endpoint

**Evidence:** `contract/shared/health.go` is a 4-LOC stub. Services implement health checks ad-hoc. Docker Compose healthchecks are hardcoded per service. Gateway does not aggregate downstream health.

**Severity:** Low
**Effort:** Small — extend `contract/shared/health` with a `CheckerRegistry`, add a composite `/health` endpoint to api-gateway that calls every downstream gRPC health probe.

### F10. Business observability dashboards

**Evidence:** `prometheus.yml` and `grafana/provisioning/` exist but no documented business dashboards. No transaction volume tracking, no per-operation latency percentiles, no cross-service call latency.

**Severity:** Low (developer/ops insight, not correctness)
**Effort:** Medium — define business metric catalog, instrument key operations, build Grafana dashboards.

## Items deferred from spec D (typed permissions)

### F11. Permission inheritance at runtime

**Evidence:** All permissions are flat. If admin wants a user to have both `clients.read.all` and `clients.read.assigned`, admin grants both explicitly. There is no runtime hierarchy ("`.all` implies `.assigned`").

**Why deferred:** Bundles in YAML give us declarative composition at codegen time. Runtime hierarchy is a different bug class (rule changes silently change effective permissions).

**Severity:** Low
**Effort:** Medium — define hierarchy semantics, update permission-check middleware, write admin-UI affordances.

### F12. Web admin UI for catalog management

**Evidence:** YAML catalog is engineering-managed. Adding a permission requires a developer + redeploy.

**Why deferred:** Catalog changes are infrequent and tied to product features. Engineering ownership is the right scope today.

**Severity:** Low
**Effort:** Medium — assumes a frontend admin app exists; add screens for catalog editing.

## Items deferred from spec B2 (cross-service saga coordination)

### F13. Outbox row TTL / archival

**Evidence:** Outbox rows accumulate forever after publishing. Spec B2 implements the publish flow but not cleanup.

**Severity:** Low (storage cost only)
**Effort:** Small — background job that archives or deletes `published_at IS NOT NULL AND published_at < now() - 30d`.

### F14. Idempotency record TTL / archival

**Evidence:** `idempotency_records` table grows forever. Spec B2 implements the contract but not cleanup.

**Severity:** Low
**Effort:** Small — same shape as F13.

## How to prioritize this list

When capacity becomes available:

1. **F4 (AutoMigrate → explicit tool)** is the only High-severity item. It blocks any move to production.
2. **F2 (cross-service contract tests)** and **F3 (structured logging)** pay back quickly on any further refactor.
3. **F1 (fixture pool)** pays back per CI cycle but requires careful test-isolation design.
4. **F8 (Kafka DLQ)** matters if/when message loss appears in incident reports.
5. The rest are quality-of-life — pick when convenient.
