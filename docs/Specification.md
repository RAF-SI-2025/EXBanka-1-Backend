# EXBanka Backend - System Specification

> **Purpose:** This file is the single source of truth for Claude agents implementing new features. Read this instead of scanning the entire codebase. It describes every pattern, convention, entity, API route, and integration point needed to add functionality correctly.

---

## Table of Contents

1. [Technology Stack](#1-technology-stack)
2. [Project Structure](#2-project-structure) (incl. go.work, Makefile)
3. [Service Architecture](#3-service-architecture) (incl. Shared Utilities, Gateway Client Wiring)
4. [Adding a New Feature Checklist](#4-adding-a-new-feature-checklist)
5. [API Gateway Patterns](#5-api-gateway-patterns)
6. [Authentication & Authorization](#6-authentication--authorization)
7. [Database Entities & Relationships](#7-database-entities--relationships)
8. [Repository Patterns](#8-repository-patterns)
9. [Service Layer Patterns](#9-service-layer-patterns)
10. [gRPC Handler Patterns](#10-grpc-handler-patterns)
11. [Protobuf Contract Patterns](#11-protobuf-contract-patterns) (incl. Existing gRPC Service Definitions)
12. [Kafka Event System](#12-kafka-event-system)
13. [Configuration Patterns](#13-configuration-patterns)
14. [Error Handling](#14-error-handling)
15. [Validation Rules](#15-validation-rules)
16. [Docker Compose](#16-docker-compose)
17. [Complete API Route Reference](#17-complete-api-route-reference)
18. [Complete Entity Reference](#18-complete-entity-reference)
19. [Complete Kafka Topic Reference](#19-complete-kafka-topic-reference) (incl. EmailType enum, Message Structs)
20. [Known Enum Values](#20-known-enum-values)
21. [Sentinel Values & Business Rules](#21-sentinel-values--business-rules)
22. [Concurrency & Transaction Safety](#22-concurrency--transaction-safety) (incl. Optimistic Locking, Saga Pattern, Spending Limits, Anti-Patterns)

---

## 1. Technology Stack

| Component | Technology | Version |
|---|---|---|
| Language | Go | 1.25.0 |
| Workspace | `go.work` | monorepo |
| HTTP Framework | Gin | 1.12.0 |
| Inter-service | gRPC + Protobuf | 1.79.2 / 1.36.11 |
| Database | PostgreSQL | 16 |
| ORM | GORM | 1.31.1 |
| Cache | Redis | 7-alpine |
| Message Queue | Apache Kafka | 3.7.0 |
| Kafka Client | segmentio/kafka-go | 0.4.50 |
| JWT | golang-jwt/jwt/v5 | - |
| Password Hashing | bcrypt (golang.org/x/crypto) | - |
| 2FA/TOTP | pquerna/otp | - |
| Decimal Math | shopspring/decimal | 1.4.0 |
| API Docs | swag / swaggo/gin-swagger | 1.16.6 |
| Testing | testify | 1.11.1 |

---

## 2. Project Structure

```
EXBanka-1-Backend/
├── contract/                    # Shared protobuf + Kafka message types
│   ├── proto/{service}/         # .proto source files
│   ├── authpb/, userpb/, ...   # Generated Go code (make proto)
│   ├── kafka/                   # Kafka message structs (messages.go)
│   └── shared/                  # Shared utilities (see Section 3.1)
├── api-gateway/                 # HTTP REST entry point (port 8080)
├── auth-service/                # JWT + password lifecycle (gRPC :50051)
├── user-service/                # Employee CRUD + roles (gRPC :50052)
├── notification-service/        # Email via SMTP + Kafka consumer (gRPC :50053)
├── client-service/              # Bank client CRUD (gRPC :50054)
├── account-service/             # Accounts + currencies + companies (gRPC :50055)
├── card-service/                # Cards + virtual cards + requests (gRPC :50056)
├── transaction-service/         # Payments + transfers + fees (gRPC :50057)
├── credit-service/              # Loans + installments + rates (gRPC :50058)
├── exchange-service/            # Currency exchange rates (gRPC :50059)
├── verification-service/        # Mobile verification challenges (gRPC :50061)
├── seeder/                      # Database seeding tool
├── test-app/                    # Integration tests
├── docs/                        # API docs + implementation plans
├── docker-compose.yml
├── Makefile
└── go.work
```

### Per-Service Internal Structure

Every gRPC microservice follows this layout:

```
{service}/
├── cmd/main.go              # Wires dependencies, starts gRPC server
└── internal/
    ├── config/config.go     # Loads env vars, exposes DSN()
    ├── model/               # GORM-tagged structs (= DB schema)
    ├── repository/          # Raw DB queries via GORM
    ├── service/             # Business logic + Kafka publishing
    ├── handler/             # gRPC handler (protobuf <-> service calls)
    ├── kafka/               # producer.go + topics.go (EnsureTopics)
    └── cache/               # Redis wrapper (auth, user, client only)
```

**API Gateway** has no DB. Instead: `internal/grpc/` (clients), `internal/handler/` (HTTP handlers), `internal/middleware/` (auth), `internal/router/` (route definitions).

**Notification Service** has no DB. Instead: `internal/consumer/` (Kafka), `internal/sender/` (SMTP), `internal/push/` (future).

### go.work (Workspace File)

When adding a new service, it **must** be added to `go.work` or it won't compile:

```
go 1.25.0

use (
    ./account-service
    ./api-gateway
    ./auth-service
    ./card-service
    ./client-service
    ./contract
    ./credit-service
    ./exchange-service
    ./notification-service
    ./test-app
    ./seeder
    ./transaction-service
    ./user-service
)
```

### Makefile

When adding a new service, add its proto generation, build, tidy, and test commands to the Makefile. The `proto` target must include a block for each service's `.proto` file that generates Go code into `contract/{service}pb/`.

---

## 3. Service Architecture

### Communication Flow

```
Client (HTTP/JSON) → API Gateway (Gin, :8080)
    → gRPC → Microservice (business logic)
        → PostgreSQL (GORM, auto-migrated)
        → Redis (optional cache, graceful degradation)
        → Kafka (async events → notification-service)
```

### Service Dependencies (gRPC calls)

| Caller | Calls |
|---|---|
| api-gateway | auth, user, client, account, card, transaction, credit, exchange, verification, notification, stock (StockExchange / Security / Order / Portfolio / OTC / Tax / SourceAdmin / **InvestmentFund** (Celina 4) / **OTCOptions** (Spec 2)), **interbank-service** (2026-06-07 cutover: the whole `/cross-bank-protocol` surface — `PeerTxService`, `PeerBankAdminService` registry, `PeerOTCService` forwarder, `PeerEgressService`, `PeerUserService`) |
| stock-service | account-service (debit/credit/reservations/bank-account), exchange-service (FX), user-service (employee names + actuary limits), client-service (client name resolution), **interbank-service** (2026-06-07 cutover: `PeerTxService.InitiateOutboundTxWithPostings` for OTC settlement, `PeerBankAdminService.ListPeerBanks` for the discovery poll, and `PeerEgressService.ProxyToPeer` for all outbound OTC HTTP — peer resolution + signing live in interbank-service, not here) |
| auth-service | user-service (employee lookup). NOTE: auth owns **all** credentials in its own `accounts` table (one row per principal, `principal_type` ∈ {employee, client}); it does **not** gRPC-call client-service for login. Client Accounts are provisioned by auth consuming the `client.client-created` Kafka event. |
| user-service | auth-service (activation tokens). NOTE: user-service does **not** dial client-service — `CLIENT_GRPC_ADDR` was removed from user-service (SP-4 2026-06-08). Client-type limit blueprint application is orchestrated by the api-gateway directly (gateway → client-service). |
| client-service | auth-service (activation tokens) |
| card-service | account-service (account validation), client-service (client validation) |
| transaction-service | account-service (balance ops), exchange-service (currency conversion), verification-service (challenge status) |
| credit-service | account-service (disbursement), user-service (employee limits), client-service (client validation) |

### Shared Utilities (`contract/shared/`)

These are available to all services. Use them instead of reimplementing.

| File | Functions | Purpose |
|---|---|---|
| `health.go` | `RegisterHealthCheck(s, name)` | Registers gRPC health check on a server |
| `grpc_dial.go` | `DialGRPC(addr)`, `MustDialGRPC(addr)` | gRPC client dial with retry policy (5 attempts, UNAVAILABLE/DEADLINE_EXCEEDED), exponential backoff, and keepalive — suitable for Kubernetes |
| `idempotency.go` | `GenerateIdempotencyKey()`, `ValidateIdempotencyKey(key)` | UUID v4 generation and validation |
| `money.go` | `ParseAmount(s)`, `FormatAmount(d)`, `FormatAmountDisplay(d)`, `AmountIsPositive(d)` | Decimal string parsing, formatting (4dp internal, 2dp display) |
| `optimistic_lock.go` | `ErrOptimisticLock` | Sentinel error for concurrent modification detection |
| `retry.go` | `Retry(ctx, cfg, fn)`, `DefaultRetryConfig` (3 attempts, 500ms) | Exponential backoff retry for transient failures |

### API Gateway gRPC Client Wiring

The API Gateway creates gRPC clients in `api-gateway/internal/grpc/` and passes them to `router.Setup()`. Key pattern: **multiple gRPC services on the same proto file share the same connection address.**

| Client Variable | gRPC Service | Connection Address |
|---|---|---|
| `authClient` | AuthService | `AUTH_GRPC_ADDR` |
| `userClient` | UserService | `USER_GRPC_ADDR` |
| `empLimitClient` | EmployeeLimitService | `USER_GRPC_ADDR` (shared) |
| `clientClient` | ClientService | `CLIENT_GRPC_ADDR` |
| `clientLimitClient` | ClientLimitService | `CLIENT_GRPC_ADDR` (shared) — also injected into `BlueprintHandler` to apply client-type blueprints directly (SP-4: gateway→client-service, bypassing user-service) |
| `accountClient` | AccountService | `ACCOUNT_GRPC_ADDR` |
| `bankAccountClient` | BankAccountService | `ACCOUNT_GRPC_ADDR` (shared) |
| `cardClient` | CardService | `CARD_GRPC_ADDR` |
| `virtualCardClient` | VirtualCardService | `CARD_GRPC_ADDR` (shared) |
| `cardRequestClient` | CardRequestService | `CARD_GRPC_ADDR` (shared) |
| `txClient` | TransactionService | `TRANSACTION_GRPC_ADDR` |
| `feeClient` | FeeService | `TRANSACTION_GRPC_ADDR` (shared) |
| `creditClient` | CreditService | `CREDIT_GRPC_ADDR` |
| `exchangeClient` | ExchangeService | `EXCHANGE_GRPC_ADDR` |
| `verificationClient` | VerificationGRPCService | `VERIFICATION_GRPC_ADDR` |
| `notificationClient` | NotificationService | `NOTIFICATION_GRPC_ADDR` |

**When adding a new gRPC service to an existing proto:** Create a new client constructor in `api-gateway/internal/grpc/`, create the client in `api-gateway/cmd/main.go` using the existing `*_GRPC_ADDR`, add it as a parameter to `router.Setup()`, and inject it into the relevant handler.

**When adding an entirely new microservice:** Also add its `*_GRPC_ADDR` to the gateway config, create a new gRPC client constructor, and wire everything through `router.Setup()`.

### Database Isolation

Each service has its own PostgreSQL database. No cross-DB queries.

| Service | Database | Port |
|---|---|---|
| auth-service | auth_db | 5433 |
| user-service | user_db | 5432 |
| client-service | client_db | 5434 |
| account-service | account_db | 5435 |
| card-service | card_db | 5436 |
| transaction-service | transaction_db | 5437 |
| credit-service | credit_db | 5438 |
| exchange-service | exchange_db | 5439 |
| verification-service | verification_db | 5440 |
| notification-service | notification_db | 5441 |

### Health Probes (Kubernetes Readiness)

Every service exposes HTTP health probes on its metrics port (default `9090`) via `contract/metrics/server.go`:

| Endpoint | Purpose | Behavior |
|---|---|---|
| `/livez` | Liveness probe | Always returns 200 if the process is running |
| `/readyz` | Readiness probe | Returns 503 until `markReady()` is called AND all registered dependency checks pass (e.g., DB ping) |
| `/startupz` | Startup probe | Returns 503 until `markReady()` is called, then always 200 (no dependency checks) |
| `/health` | Legacy liveness | Same as `/livez`, kept for backwards compatibility with Prometheus |
| `/metrics` | Prometheus scrape | Standard Prometheus metrics endpoint |

**Usage in `cmd/main.go`:**
```go
markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
// Register DB ping check
sqlDB, _ := db.DB()
addReadinessCheck(func(ctx context.Context) error { return sqlDB.PingContext(ctx) })
// ... start gRPC server ...
markReady()
```

### gRPC Client Retry Policy

All API Gateway gRPC clients use `shared.DialGRPC()` which configures:
- **Retry policy:** 5 attempts with exponential backoff (0.5s initial, 5s max) for `UNAVAILABLE` and `DEADLINE_EXCEEDED` status codes
- **Connection backoff:** 500ms base delay, 2x multiplier, 10s max delay
- **Keepalive:** 30s ping interval, 10s timeout, permit without active streams

### Seeder Configuration

The seeder supports a `SEEDER_COOLDOWN` environment variable (default `30s`) that adds an initial delay before attempting to connect to services. Set to `10s` in Docker Compose (where `depends_on` handles ordering) and `30s+` in Kubernetes.

---

## 4. Adding a New Feature Checklist

When adding a new feature that spans the full stack, touch these files in order:

### Step 0: Workspace & Build (if adding a new microservice)
- [ ] Add service directory to `go.work`
- [ ] Add proto generation block to `Makefile` `proto` target
- [ ] Add build, tidy, and test commands to `Makefile`

### Step 1: Protobuf Contract
- [ ] Edit or create `.proto` file in `contract/proto/{service}/`
- [ ] Define request/response messages and RPC methods
- [ ] If adding a new gRPC service to an existing proto, note that gateway reuses the same connection address
- [ ] Run `make proto` to regenerate Go code

### Step 2: Database Model
- [ ] Add/modify GORM model in `{service}/internal/model/`
- [ ] Use `decimal.Decimal` for all financial fields (`gorm:"type:numeric(18,4)"`)
- [ ] Add `Version int64` field if optimistic locking needed
- [ ] Add appropriate indexes and unique constraints via struct tags

### Step 3: Repository
- [ ] Add/modify repository in `{service}/internal/repository/`
- [ ] Constructor takes `*gorm.DB`
- [ ] Return `(*model.X, error)` from all methods
- [ ] Use `gorm.ErrRecordNotFound` for not-found checks
- [ ] Support pagination pattern: `func List(..., page, pageSize int) ([]model.X, int64, error)`

### Step 4: Kafka Messages (if events needed)
- [ ] Define message struct in `contract/kafka/messages.go`
- [ ] Add publish method to `{service}/internal/kafka/producer.go`
- [ ] Add topic to `EnsureTopics()` in `{service}/internal/kafka/topics.go`
- [ ] Add topic to `EnsureTopics()` in every service that consumes it

### Step 5: Service Layer
- [ ] Add business logic in `{service}/internal/service/`
- [ ] Validate business rules, return `status.Error(codes.X, "message")`
- [ ] Publish Kafka events after successful operations
- [ ] Log Kafka failures as warnings, don't fail the main operation

### Step 6: gRPC Handler
- [ ] Implement RPC in `{service}/internal/handler/grpc_handler.go`
- [ ] Map between protobuf messages and service-layer types
- [ ] Register handler in `cmd/main.go`

### Step 7: API Gateway Handler
- [ ] Create/modify handler in `api-gateway/internal/handler/`
- [ ] Validate ALL inputs BEFORE gRPC call using `validation.go` helpers
- [ ] Use `apiError()` for validation errors, `handleGRPCError()` for gRPC errors
- [ ] Add Swagger annotations (`@Summary`, `@Tags`, `@Param`, `@Success`, `@Failure`, `@Router`)

### Step 7b: API Gateway gRPC Client (if new gRPC service)
- [ ] Create client constructor in `api-gateway/internal/grpc/` (or reuse existing address for same-service proto)
- [ ] Instantiate client in `api-gateway/cmd/main.go`
- [ ] Pass client to `router.Setup()` and inject into handler

### Step 8: API Gateway Router
- [ ] Add route in `api-gateway/internal/router/router.go`
- [ ] Apply correct middleware (see Section 6)
- [ ] Apply correct permission (see Section 6)

### Step 9: Configuration
- [ ] Add new env vars to `{service}/internal/config/config.go`
- [ ] Add to `docker-compose.yml` environment block (use service names, not localhost)
- [ ] Add `depends_on` if new service dependency

### Step 10: Documentation & Build
- [ ] Run `make swagger` (or `cd api-gateway && swag init -g cmd/main.go --output docs`)
- [ ] Update `docs/api/REST_API.md`
- [ ] Add integration tests in `test-app/`

**Saga step naming**: When adding a new saga step, declare the constant in `contract/shared/saga/steps.go` (and add to the `allSteps` registry map). Then add a case to the recovery switch in `stock-service/internal/service/saga_recovery.go` — the switch's panicking `default` will crash startup if you forget.

---

## 5. API Gateway Patterns

### Request Handling Pattern

```go
func (h *SomeHandler) CreateSomething(c *gin.Context) {
    // 1. Bind JSON
    var req createSomethingRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        apiError(c, 400, ErrValidation, "invalid request body")
        return
    }

    // 2. Validate ALL inputs before gRPC call
    kind, err := oneOf("account_kind", req.AccountKind, "current", "foreign")
    if err != nil {
        apiError(c, 400, ErrValidation, err.Error())
        return
    }
    if err := positive("amount", req.Amount); err != nil {
        apiError(c, 400, ErrValidation, err.Error())
        return
    }

    // 3. Extract auth context (if needed)
    userID := c.GetInt64("user_id")
    systemType := c.GetString("system_type")

    // 4. Call gRPC service
    resp, err := h.client.CreateSomething(c.Request.Context(), &pb.CreateSomethingRequest{
        // ... map fields
    })
    if err != nil {
        handleGRPCError(c, err)
        return
    }

    // 5. Return success response
    c.JSON(http.StatusOK, gin.H{
        "id":   resp.Id,
        "name": resp.Name,
    })
}
```

### `/api/me/*` Handler Pattern (User's Own Resources)

```go
func (h *SomeHandler) ListMyResources(c *gin.Context) {
    // Ownership from JWT, NEVER from URL params
    userID := c.GetInt64("user_id")
    systemType := c.GetString("system_type")

    // For clients: user_id IS the client_id
    // For employees: user_id IS the employee_id
    resp, err := h.client.ListByOwner(c.Request.Context(), &pb.ListRequest{
        OwnerId: userID,
    })
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    c.JSON(http.StatusOK, resp)
}
```

### Pagination Pattern (Query Params)

```go
page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
```

### Collection Filtering Pattern

- Use query params: `?client_id=X`, `?account_number=X`, `?status=active`
- Only one filter at a time — if multiple provided, return 400
- Never use path segments for filtering collections

### Response Format

**Success:**
```json
{
  "id": 1,
  "name": "value",
  "items": [...],
  "total_count": 100
}
```

**Error:**
```json
{
  "error": {
    "code": "validation_error",
    "message": "amount must be positive",
    "details": {}
  }
}
```

### Swagger Annotation Template

```go
// CreateSomething creates a new something
// @Summary Create something
// @Tags Something
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param request body createSomethingRequest true "Request body"
// @Success 200 {object} createSomethingResponse
// @Failure 400 {object} errorResponse
// @Failure 401 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /api/something [post]
func (h *SomeHandler) CreateSomething(c *gin.Context) { ... }
```

---

## 6. Authentication & Authorization

### Token Types

| Token | Duration | Storage | Purpose |
|---|---|---|---|
| Access (JWT) | 15 min | Stateless | API authentication |
| Refresh | 168h (7d) | auth_db | Token renewal |
| Activation | 24h | auth_db | New account activation |
| Password Reset | 1h | auth_db | Password recovery |

### JWT Claims

```json
{
  "principal_id": 123,
  "principal_type": "employee",
  "email": "user@example.com",
  "roles": ["EmployeeBasic"],
  "permissions": ["clients.read", "accounts.read"],
  "device_type": "",
  "device_id": "",
  "jti": "uuid",
  "iat": 1234567890,
  "exp": 1234567890
}
```

Field rename history (plan 2026-04-27-owner-type-schema.md, Tasks 1-2): `user_id` → `principal_id`, `system_type` → `principal_type`. The names align with the system-wide *principal* concept (the authenticated subject of the token, distinct from the *owner* of any resource it touches — see §6.X Identity Model below).

Mobile JWTs additionally include `device_type: "mobile"` and `device_id: "<uuid>"`. Mobile refresh tokens have a 90-day expiry (configurable via `MOBILE_REFRESH_EXPIRY`).

### Middleware Selection Guide

| Route Pattern | Middleware | Who Can Access |
|---|---|---|
| `/api/auth/*` | None (public) | Anyone |
| `/api/exchange/*` | None (public) | Anyone |
| `/api/me/*` | `AnyAuthMiddleware` | Both employees and clients |
| `/api/me/*/pin`, etc. | `AnyAuthMiddleware` + `RequireClientToken()` | Clients only |
| `/api/{resource}` | `AuthMiddleware` + `RequirePermission("x.y")` | Employees with permission |
| `/api/mobile/auth/*` | None (public) | Anyone |
| `/api/mobile/device/*` | `MobileAuthMiddleware` | Mobile device with valid JWT |
| `/api/mobile/verifications/*` | `MobileAuthMiddleware` + `RequireDeviceSignature` | Mobile device with valid JWT + HMAC |
| `/api/verify/*` | `MobileAuthMiddleware` + `RequireDeviceSignature` | Mobile device with valid JWT + HMAC |

### Permission Catalog (codegened, Plan D)

Permissions are defined in `contract/permissions/catalog.yaml` and code-generated to `contract/permissions/perms.gen.go`. Naming convention: `<resource>.<verb>.<scope>` — three dotted segments, snake_case lowercase (e.g. `clients.read.all`, `roles.permissions.assign`).

The codegen tool (`tools/perm-codegen/`) is invoked via `make permissions` and produces typed Go constants like `perms.Clients.Read.All`. Router gates use these constants directly: `middleware.RequirePermission(perms.Clients.Read.All)`. Drift between handler code and the catalog is caught at `go build` (unknown constant ⇒ compile error).

The catalog is the source of truth for what permissions EXIST. Default role-permission mappings (also in `catalog.yaml` under `default_roles`) are seeded into the `role_permissions` DB table on FIRST startup only — when `role_permissions` is empty. After first startup, the DB is authoritative; admins manage role grants via the runtime API and the seed never re-runs.

**Admin runtime API (granular, one permission per call):**

| Method | Path | Required Permission |
|---|---|---|
| `POST` | `/api/v3/roles/:id/permissions` | `roles.permissions.assign` |
| `DELETE` | `/api/v3/roles/:id/permissions/:permission` | `roles.permissions.revoke` |

The `POST` body is `{"permission": "<code>"}`. The handler validates `<code>` against the catalog — unknown codes return HTTP 400 (`InvalidArgument`). A missing role returns HTTP 404. Both verbs are idempotent and return HTTP 204 No Content on success. Both publish `RolePermissionsChangedMessage` to Kafka so auth-service can revoke active sessions for affected employees.

For bulk replacement (set all permissions on a role at once) the legacy `PUT /api/v3/roles/:id/permissions` endpoint remains available (gated by `roles.update.any`).

**Catalog drift check:** at startup, user-service scans `role_permissions` and logs `WARN: orphan permission in role_permissions: role_id=… perm=…` for any DB row referencing a permission no longer in the catalog. Orphans are NOT auto-cleaned (silent revocation of admin grants would be unsafe); operators clean them manually after reviewing the warning.

**Permission category overview** (truncated — see `contract/permissions/catalog.yaml` for the full 140-permission list):

| Category | Example Permissions |
|---|---|
| clients | `clients.create.any`, `clients.read.all`, `clients.update.profile`, `clients.update.contact`, `clients.update.limits` |
| accounts | `accounts.create.current`, `accounts.create.foreign`, `accounts.read.all`, `accounts.update.name`, `accounts.update.limits`, `accounts.deactivate.any` |
| cards | `cards.create.physical`, `cards.create.virtual`, `cards.read.all`, `cards.block.any`, `cards.unblock.any`, `cards.approve.physical`, `cards.approve.virtual` |
| credits | `credits.read.all`, `credits.approve.cash`, `credits.approve.housing`, `credits.disburse.any` |
| securities | `securities.trade.any`, `securities.read.holdings_all`, `securities.manage.catalog` |
| employees | `employees.create.any`, `employees.update.any`, `employees.read.all`, `employees.roles.assign`, `employees.permissions.assign` |
| roles | `roles.read.all`, `roles.update.any`, `roles.permissions.assign`, `roles.permissions.revoke` |
| limit_templates | `limit_templates.create.any`, `limit_templates.update.any` |
| limits | `limits.employee.read`, `limits.employee.update` |
| bank_accounts | `bank_accounts.manage.any` |
| fees | `fees.create.any`, `fees.update.any` |
| otc | `otc.trade.accept`, `otc.trade.on_behalf` |
| funds | `funds.read.all`, `funds.manage.catalog` |
| orders | `orders.place.on_behalf_client`, `orders.place.on_behalf_bank`, `orders.read.all`, `orders.cancel.all` |
| verification | `verification.skip`, `verification.manage` |
| peer_banks | `peer_banks.manage.any` (Phase 2 SI-TX — admin CRUD on the `peer_banks` registry; `EmployeeAdmin` only via the wildcard `*` grant) |
| notifications | `notifications.templates.manage` — allows `EmployeeAdmin` to customize notification template subject/body text |
| portfolio | `portfolio.view.client` — allows an employee to read any client's portfolio via the unified portfolio routes; `portfolio.view.fund` — allows reading any investment-fund's portfolio. Both granted to `EmployeeSupervisor` (and via inheritance to `EmployeeAdmin`). |
| admin | `admin.crons.view` — list/read all cron jobs across services; `admin.crons.trigger` — manually trigger a cron execution; `admin.crons.manage` — pause and resume crons. All three granted to `EmployeeAdmin` via the wildcard `*` grant. (C5 — 2026-05-28) |
| admin | `admin.audit.view` — read the global changelog and cron-action audit tables without specifying an entity (D1 — 2026-05-28). Granted to `EmployeeAdmin` via the wildcard `*` grant. |

### Role Definitions

| Role | Inherits Permissions |
|---|---|
| EmployeeBasic | clients.*, accounts.*, cards.*, payments.read, credits.read |
| EmployeeAgent | EmployeeBasic + securities.*, otc.read.all, otc.trade.*, otc.trade.expire, bank_accounts.manage.any, orders.place-on-behalf, orders.place.on-behalf-client, orders.place.for-bank |
| EmployeeSupervisor | EmployeeAgent + agents.manage, otc.manage, funds.manage, funds.bank-position-read, verification.skip, verification.manage, portfolio.view.client, portfolio.view.fund |
| EmployeeAdmin | All permissions (including `securities.manage`) |

### Context Values Set by Middleware

After middleware runs, these are available via `c.GetXxx()`:

```go
c.GetInt64("principal_id")      // Authenticated subject ID (employee or client)
c.GetString("principal_type")   // "employee" or "client"
c.GetString("email")            // Email
c.GetString("role")             // Primary role name
// "roles" and "permissions" are set as string slices
```

After `ResolveIdentity` runs (per-route, see §6.X) the resolved owner is also available:

```go
identity := middleware.IdentityFromContext(c) // *ResolvedIdentity
// identity.PrincipalType / PrincipalID  — JWT subject
// identity.OwnerType / OwnerID          — resource owner per route policy
// identity.ActingEmployeeID             — employee id when an employee acts
//                                         on behalf of bank/client (else nil)
```

### 6.X Identity Model (plan 2026-04-27-owner-type-schema.md)

The system distinguishes two concepts:

- **Principal** — the authenticated caller. JWT carries `principal_type` (`client | employee`) + `principal_id`. Set by `AuthMiddleware` / `AnyAuthMiddleware`.
- **Owner** — the holder of a resource row. Stock-service models use `(owner_type, owner_id)` where `owner_type ∈ {client, bank}` (employees never own trading resources; bank-owned rows have `owner_id IS NULL`).

The mapping from principal to owner is a per-route policy enforced by `api-gateway/internal/middleware.ResolveIdentity(rule)`:

| Rule | Used by | Mapping |
|---|---|---|
| `OwnerIsPrincipal` | `/api/me/profile`, `/api/me/cards`, etc. | Owner == Principal verbatim. |
| `OwnerIsBankIfEmployee` | `/api/me/orders`, `/api/me/holdings`, `/api/me/funds`, `/api/me/otc/*` | Employee → bank ownership (`OwnerType=bank`, `OwnerID=nil`). Client → self ownership. |
| `OwnerFromURLParam("client_id")` | Admin-acts-on-client routes | Owner is the URL-named client. Requires the principal to be an employee with the relevant `*.on_behalf.*` permission. |

`ActingEmployeeID` is set on every side-effect row whenever the principal is an employee, regardless of the resolved owner. The actuary-limit gate in stock-service keys on this field — `OwnerIsBankIfEmployee` for an employee correctly resolves Owner=bank but still tags the row with `acting_employee_id=<emp>` so the limit applies.

Helper: `middleware.IdentityFromContext(c) → *ResolvedIdentity`. Handlers should call this once and use the resolved owner — never re-derive ownership from the JWT.

---

## 7. Database Entities & Relationships

### Entity Relationship Diagram (Logical)

```
User Service (user_db):
  Employee 1──N employee_roles N──1 Role 1──N role_permissions N──1 Permission
  Employee 1──N employee_additional_permissions N──1 Permission
  Employee 1──1 EmployeeLimit
  LimitTemplate (standalone)

Auth Service (auth_db):
  Account ──1 RefreshToken (1:N)
  Account ──1 ActivationToken (1:N)
  Account ──1 PasswordResetToken (1:N)
  Account ──1 TOTPSecret (1:1)
  Account ──1 LoginAttempt (1:N)
  Account ──1 AccountLock (1:N)
  Account ──1 ActiveSession (1:N)

Client Service (client_db):
  Client 1──1 ClientLimit

Account Service (account_db):
  Account (bank) ──1 LedgerEntry (1:N)
  Account N──1 Company (optional)
  Currency (standalone)

Card Service (card_db):
  Card 1──N CardBlock
  CardRequest (standalone, references client + account)
  AuthorizedPerson (standalone, references card/account)

Transaction Service (transaction_db):
  Payment 1──1 VerificationCode
  Transfer (standalone)
  TransferFee (standalone)
  PaymentRecipient (standalone, references client)

Credit Service (credit_db):
  LoanRequest (standalone)
  Loan 1──N Installment
  InterestRateTier (standalone)
  BankMargin (standalone)

Exchange Service (exchange_db):
  ExchangeRate (standalone, from/to pairs)
```

### Cross-Service References (by ID, not FK)

- `Account.OwnerID` → Client.ID (account-service references client-service)
- `Account.EmployeeID` → Employee.ID (account-service references user-service)
- `Card.AccountNumber` → Account.AccountNumber (card-service references account-service)
- `Card.OwnerID` → Client.ID or AuthorizedPerson.ID
- `Payment/Transfer.FromAccountNumber/ToAccountNumber` → Account.AccountNumber
- `Loan.AccountNumber` → Account.AccountNumber
- `Loan.ClientID` → Client.ID
- `auth.Account.PrincipalID` → Employee.ID or Client.ID (based on PrincipalType)

---

## 8. Repository Patterns

### Constructor Pattern

```go
type SomeRepository struct {
    db *gorm.DB
}

func NewSomeRepository(db *gorm.DB) *SomeRepository {
    return &SomeRepository{db: db}
}
```

### Standard CRUD Methods

```go
func (r *SomeRepository) Create(entity *model.Something) error {
    return r.db.Create(entity).Error
}

func (r *SomeRepository) GetByID(id uint64) (*model.Something, error) {
    var entity model.Something
    if err := r.db.First(&entity, id).Error; err != nil {
        return nil, err // Caller checks gorm.ErrRecordNotFound
    }
    return &entity, nil
}

func (r *SomeRepository) Update(entity *model.Something) error {
    return r.db.Save(entity).Error
}
```

### Pagination Pattern

```go
func (r *SomeRepository) List(filter string, page, pageSize int) ([]model.Something, int64, error) {
    var items []model.Something
    var total int64

    q := r.db.Model(&model.Something{})
    if filter != "" {
        q = q.Where("name ILIKE ?", "%"+filter+"%")
    }

    q.Count(&total)
    q.Offset((page - 1) * pageSize).Limit(pageSize).Order("created_at DESC").Find(&items)
    return items, total, q.Error
}
```

### Financial Operations (Ledger Pattern)

```go
// Uses SELECT ... FOR UPDATE to prevent race conditions
func (r *LedgerRepository) DebitWithLock(tx *gorm.DB, accountNumber string, amount decimal.Decimal, ...) (*model.LedgerEntry, error) {
    var account model.Account
    if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
        Where("account_number = ?", accountNumber).First(&account).Error; err != nil {
        return nil, err
    }
    if account.Balance.LessThan(amount) {
        return nil, errors.New("insufficient funds")
    }
    // Update balance, create ledger entry...
}
```

---

## 9. Service Layer Patterns

### Service Constructor

```go
type SomeService struct {
    repo     *repository.SomeRepository
    producer *kafka.Producer
    cache    *cache.RedisCache // optional
}

func NewSomeService(repo *repository.SomeRepository, producer *kafka.Producer) *SomeService {
    return &SomeService{repo: repo, producer: producer}
}
```

### Business Logic + Kafka Publishing

```go
func (s *SomeService) CreateSomething(req *CreateRequest) (*model.Something, error) {
    // 1. Validate business rules
    if err := s.validateBusinessRule(req); err != nil {
        return nil, status.Error(codes.InvalidArgument, err.Error())
    }

    // 2. Check uniqueness
    existing, _ := s.repo.GetByEmail(req.Email)
    if existing != nil {
        return nil, status.Error(codes.AlreadyExists, "email already registered")
    }

    // 3. Create entity
    entity := &model.Something{...}
    if err := s.repo.Create(entity); err != nil {
        return nil, status.Error(codes.Internal, "failed to create record")
    }

    // 4. Publish Kafka event (log failure, don't fail operation)
    msg := &kafkamsg.SomethingCreatedMessage{...}
    if err := s.producer.PublishSomethingCreated(context.Background(), msg); err != nil {
        log.Printf("WARN: failed to publish event: %v", err)
    }

    return entity, nil
}
```

### gRPC Error Codes (Use in Service Layer)

```go
import "google.golang.org/grpc/status"
import "google.golang.org/grpc/codes"

status.Error(codes.InvalidArgument, "email format invalid")      // → 400
status.Error(codes.Unauthenticated, "invalid credentials")       // → 401
status.Error(codes.PermissionDenied, "insufficient permissions") // → 403
status.Error(codes.NotFound, "account not found")                // → 404
status.Error(codes.AlreadyExists, "email already registered")    // → 409
status.Error(codes.FailedPrecondition, "insufficient balance")   // → 409
status.Error(codes.ResourceExhausted, "rate limited")            // → 429
status.Error(codes.Internal, "database error")                   // → 500
```

---

## 10. gRPC Handler Patterns

### Handler Registration (cmd/main.go)

```go
grpcServer := grpc.NewServer()
pb.RegisterSomeServiceServer(grpcServer, handler.NewSomeHandler(service))
// + health check
grpc_health_v1.RegisterHealthServer(grpcServer, health.NewServer())
```

### Handler Method Pattern

```go
func (h *SomeGRPCHandler) CreateSomething(ctx context.Context, req *pb.CreateSomethingRequest) (*pb.CreateSomethingResponse, error) {
    result, err := h.service.CreateSomething(&service.CreateRequest{
        Name:   req.Name,
        Amount: decimal.NewFromFloat(req.Amount),
    })
    if err != nil {
        return nil, err // gRPC status errors pass through directly
    }
    return &pb.CreateSomethingResponse{
        Id:   result.ID,
        Name: result.Name,
    }, nil
}
```

---

## 11. Protobuf Contract Patterns

### Proto File Location

`contract/proto/{service}/{service}.proto`

### Standard Proto Structure

```protobuf
syntax = "proto3";
package servicepb;
option go_package = "github.com/exbanka/contract/servicepb";

service SomeService {
  rpc CreateSomething(CreateSomethingRequest) returns (CreateSomethingResponse);
  rpc GetSomething(GetSomethingRequest) returns (GetSomethingResponse);
  rpc ListSomethings(ListSomethingsRequest) returns (ListSomethingsResponse);
}

message CreateSomethingRequest {
  string name = 1;
  double amount = 2;
}

message CreateSomethingResponse {
  uint64 id = 1;
  string name = 2;
}

message ListSomethingsResponse {
  repeated SomethingItem items = 1;
  int64 total_count = 2;
}
```

### Regeneration

```bash
make proto
# Generates: contract/{service}pb/*.pb.go and *_grpc.pb.go
```

### Existing gRPC Service Definitions (17 services across 10 proto files)

An agent extending an existing service needs to know which gRPC services already exist:

| Proto File | gRPC Services | RPC Count |
|---|---|---|
| `auth/auth.proto` | `AuthService` | 11 |
| `user/user.proto` | `UserService`, `EmployeeLimitService` | 11 + 5 |
| `client/client.proto` | `ClientService`, `ClientLimitService` | 5 + 2 |
| `account/account.proto` | `AccountService`, `BankAccountService` | 27 + 6 |
| `card/card.proto` | `CardService`, `VirtualCardService`, `CardRequestService` | 9 + 5 + 6 |
| `transaction/transaction.proto` | `TransactionService`, `FeeService`, `InterBankService` (Spec 3 + Spec 4 `ReverseInterBankTransfer`) | 13 + 5 + 5 |
| `credit/credit.proto` | `CreditService` | 16 |
| `exchange/exchange.proto` | `ExchangeService` | 4 |
| `notification/notification.proto` | `NotificationService` (incl. template management: `ListTemplates`, `GetTemplate`, `SetTemplate`, `ResetTemplate`) | 6 |
| `stock/stock.proto` | `SecurityGRPCService`, `OrderGRPCService`, `PortfolioGRPCService`, `OTCGRPCService`, `SourceAdminService`, **`InvestmentFundService`** (Celina 4, 9 RPCs), **`OTCOptionsService`** (Spec 2, 9 RPCs), **`CrossBankOTCService`** (Spec 4, 12 RPCs) | (see below) |

**account-service BankAccountService additions:**

`BankAccountService` — two new RPCs for loan disbursement saga:
- `DebitBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse)` — atomically debits the bank sentinel account for a given currency, with idempotency keyed on `reference + direction`.
- `CreditBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse)` — atomically credits the bank sentinel account for a given currency, with idempotency keyed on `reference + direction`.

**account-service AccountService reservation RPCs (Phase 2 securities settlement):**

Four new RPCs on `AccountService` back the securities-order reservation system. All run inside `SELECT FOR UPDATE` transactions on the target account.

- `ReserveFunds(account_id, order_id, amount, currency_code, idempotency_key, order_kind) returns (reservation_id, reserved_balance, available_balance)` — creates an `AccountReservation`, increments `Account.ReservedBalance`, decrements `Account.AvailableBalance`. Idempotent on `(order_id, order_kind)`: a retry with the same pair returns the existing reservation. Returns `FailedPrecondition` on currency mismatch or insufficient available balance.
- `ReleaseReservation(order_id, idempotency_key, order_kind) returns (released_amount, reserved_balance)` — transitions the reservation to `released`, rolls `ReservedBalance` back, restores `AvailableBalance`. No-op (with empty response) if the reservation is missing, already released, or already settled.
- `PartialSettleReservation(order_id, order_transaction_id, amount, memo, idempotency_key, order_kind) returns (settled_amount, remaining_reserved, balance_after, ledger_entry_id)` — settles part (or all) of a reservation against a specific fill. Writes a `LedgerEntry` so the fill appears in transaction history, debits `Balance`, decrements `ReservedBalance`, and when the reservation is fully consumed transitions status to `settled`. Idempotent on `order_transaction_id` via a unique index on `AccountReservationSettlement.order_transaction_id`.
- `GetReservation(order_id, order_kind) returns (exists, status, amount, settled_total, settled_transaction_ids)` — read-only; used by stock-service saga recovery to determine which fill saga steps already committed on account-service.

**account-service AccountService incoming/outgoing reservation RPCs (Celina-5 cross-bank, string-keyed):** distinct from the `order_id`-keyed reservation RPCs above — these back the SI-TX two-phase money legs and are keyed by a string `reservation_key`.
- Incoming (credit side): `ReserveIncoming` (pending row, no balance change), `CommitIncoming` (Balance += amount), `ReleaseIncoming`.
- Outgoing (debit side, reserve-then-settle): `ReserveOutgoing(account_number, amount, currency, reservation_key, idempotency_key) returns (reservation_key, available_after)` — HOLD: AvailableBalance -= amount, Balance untouched; `FailedPrecondition` on insufficient available / inactive / currency mismatch. `SettleOutgoing(reservation_key, idempotency_key) returns (balance_after)` — Balance -= amount + debit ledger entry; idempotent; refuses non-pending rows. `ReleaseOutgoing(reservation_key, idempotency_key) returns (released)` — AvailableBalance += amount, no Balance movement; idempotent no-op on non-pending. All three run in `SELECT FOR UPDATE` transactions and are wrapped in the saga-step idempotency contract.

**`order_kind` discriminator (added 2026-05-16):** every reservation RPC carries an `order_kind` string that disambiguates which caller-namespace the `order_id` belongs to. Without it, two different callers using auto-increment IDs from different stock-service tables (e.g. `Order.ID` for stock placement vs `OptionContract.ID` for OTC accept, both starting at 1) would silently collide on the single-column `order_id` unique index, leading to the second arrival reusing the first's released reservation and seeing `reservation status=released` from the settle step. The unique index on `account_reservations` is now composite `(order_id, order_kind)`. Empty `order_kind` defaults to `"stock_order"` for one-version-behind callers. Current values (constants in `contract/shared/orderkind` and `account-service/internal/model`):
- `stock_order` — stock placement / forex fill / portfolio fill (Order.ID)
- `otc_premium` — OTC option accept saga, premium reservation (OptionContract.ID)
- `otc_strike` — OTC option exercise saga, strike reservation (OptionContract.ID)
- `otc_stock_buy` — OTC stock buy-offer cash reservation (OTCStockBuyOffer.AccountReservationOrderID)

**stock-service gRPC additions:**

`PortfolioGRPCService` — portfolio operations including option exercise:
- `ExerciseOptionByOptionID(ExerciseOptionByOptionIDRequest) returns (ExerciseResult)` — exercises an option by option ID instead of holding ID. Fields: `option_id uint64` (required), `user_id uint64` (required), `holding_id uint64` (optional; 0 means auto-resolve to the user's most recent unexpired holding for that option).
- `GetUnifiedPortfolio(GetUnifiedPortfolioRequest) returns (UnifiedPortfolioResponse)` — returns all holdings and fund positions for a given owner, grouped by asset type, with per-position P/L (unrealised) and per-fund percentage-of-fund stats. Request fields: `owner_type string` (`client`|`bank`|`investment_fund`), `owner_id uint64` (0 for bank). Response: `repeated PortfolioGroup groups` where each `PortfolioGroup` has `asset_type string` and `repeated PortfolioPosition positions`. Each `PortfolioPosition` carries `symbol`, `quantity`, `avg_cost_rsd`, `current_price_rsd`, `current_value_rsd`, `p_l_rsd`, `p_l_pct`, plus option-specific fields (`strike_rsd`, `premium_paid_rsd`, `intrinsic_value_rsd`, `settlement_date`) and fund-specific fields (`fund_id`, `fund_name`, `amount_invested_rsd`, `pct_of_fund`). Fund NAV is computed as Σ(fund_holding.qty × current_listing_price) — does not include the fund's liquid RSD balance from account-service.

`OrderGRPCService` — `CreateOrder` and `BuyOTCOffer` RPCs accept two new optional fields: `acting_employee_id` (uint64, employee placing the trade on behalf of a client; 0 means the caller is the client) and `on_behalf_of_client_id` (uint64, the client being traded for; 0 means the caller is trading for themselves). The gateway sets these fields when an employee uses the `POST /api/v3/orders` or `POST /api/v3/otc/offers/:id/buy-on-behalf` endpoints.

`OTCOptionsService` — `CreateOTCOfferRequest` gained a `ticker` field (proto field 12): a human-readable underlying-stock ticker threaded from the api-gateway create-offer handler through to the persisted `OTCOffer.Ticker` (and onto the resulting `OptionContract.Ticker`), used for in-app notification rendering (Plan B1).

`OTCOptionsService.GetOffer` — service-layer local↔remote convergence (SP-1, 2026-06-04). `GetOTCOfferRequest` gained `acting_owner_type` (field 4, `client`|`bank`) and `acting_owner_id` (field 5, `0` when bank). `OTCOfferResponse` gained `kind` (field 18, `local`|`remote`), `routing_number` (19), `bank_code` (20), and `me_owner` (21). The stock-service handler is now the first layer that distinguishes local from remote: a local `OTCOffer` is returned with `kind="local"`, this bank's routing/bank-code, and `me_owner` computed from the acting identity vs. the offer's initiator seller id (`"bank"` | `"client-<id>"`). When the id is not a local offer, GetOffer falls back to the folded-in remote `OTCOffer` rows (`OTCOfferRepository.GetRemoteByID`, SP-2a) and returns a `kind="remote"` projection (`me_owner=false`, no revisions). NotFound only when neither a local nor a remote row exists. The separate `GetRemoteOTCOffer` RPC (and its request/response messages) on `OTCGRPCService` was removed — `GetOffer` now resolves both. The api-gateway is a uniform pass-through; provenance + ownership are no longer computed gateway-side. **SP-2b (2026-06-05):** `OTCOfferResponse` additionally gained `my_negotiation_id` (field 22, uint64) + `my_negotiation_status` (field 23, string). When the authenticated caller has an own (as BIDDER) negotiation chain against the resolved offer, GetOffer stamps that chain's surrogate id + status so the FE can jump straight to its chain; `0`/`""` when the caller has no chain (a poster who never bid is `me_owner=true` but has no `my_negotiation_id` — the two are independent). LOCAL chains key on `parent_offer_id` == the local offer id; REMOTE chains key on `(RemoteParentRouting, RemoteParentNativeID)` == the remote offer's `(routing_number, native_id)`. When multiple chains exist on one offer the active one wins (accepted > live `open`/`countered`/`ongoing` > terminal; ties → most recently created). Chains sourced from `OTCNegotiationRepository.ListByBidder` (local) + `ListRemoteNegByClient` (remote), wired via `OTCOptionsHandler.WithMyNegotiations`.

`OTCOptionsService.ListMyNegotiations` — service-layer local↔remote convergence for negotiation chains (SP-1 Task 7; remote source updated SP-2a 2026-06-05). `OTCNegotiationResponse` gained `kind` (field 18, `local`|`remote`), `routing_number` (19), `bank_code` (20), and `me_owner` (21). The stock-service handler merges the caller's LOCAL bidder chains (from `OTCNegotiationRepository.ListByBidder`) with the caller's REMOTE peer chains (from `OTCNegotiationRepository.ListRemoteNegByClient` — remote rows in the unified `OTCNegotiation` table, SP-2a; the retired `PeerOtcNegotiationRepository.ListByClient` no longer exists). LOCAL items are stamped `kind="local"`, this bank's routing/bank-code, and `me_owner=false` (the list returns only bidder chains, and a bidder is never the listing owner). REMOTE items are stamped `kind="remote"`, with `id` = the autoincrement `OTCNegotiation.id` of the remote row (the stable local surrogate), terms projected from the parsed `sitx.OtcOffer` in `RemoteOfferJSON` (amount→quantity, pricePerStock→strike_price, premium→premium, settlementDate→settlement_date), `status` = the remote row's `Status`, `routing_number`/`bank_code` = the COUNTERPARTY peer bank (the side we do not host), and `me_owner` = (`RemoteSellerRouting == own_routing`) — true iff we host the seller/poster side. The `?statuses=` filter applies to both sets. Paging applies to the LOCAL set; REMOTE chains are appended in full and never truncated; `total` reflects the local total only. Remote merge is only performed for client principals (cross-bank party ids are `client-<N>`); a bank/employee caller gets local chains only. The api-gateway is a uniform pass-through; the new fields flow through automatically.

`SourceAdminService` — destructive data-source management:
- `SwitchSource(SwitchSourceRequest) returns (SwitchSourceResponse)` — switches the active stock data source. Request field: `source string` (one of `external`, `generated`, `simulator`). Response wraps a `SourceStatus` message.
- `GetSourceStatus(GetSourceStatusRequest) returns (SourceStatus)` — returns the current source name and switch status. `SourceStatus` fields: `source string`, `status string` (`idle` | `reseeding` | `failed`), `started_at string` (RFC3339), `last_error string`.

`OTCGRPCService` — OTC offer discovery and acceptance, including the unified local + cross-bank market view:
- `ListOffers(ListOTCOffersRequest) returns (ListOTCOffersResponse)` — local-only OTC offers built from this bank's holdings (`security_type`, `ticker`, pagination filters).
- `BuyOffer(BuyOTCOfferRequest) returns (OTCTransaction)` — buyer-side acceptance for a local OTC offer; settles via the standard OTC saga.
- `ListUnifiedOffers(ListUnifiedOTCOffersRequest) returns (ListUnifiedOTCOffersResponse)` — unified local + cross-bank view, backed by an in-process ~5 s cache that fans out to every active peer bank's `GET /api/v3/public-stock`. Request fields: `security_type`, `ticker`, `kind` (`""` | `local` | `remote`), `bank_code`, `page`, `page_size`. The cache (and the peer fan-out goroutine) live entirely in stock-service; the api-gateway's `GET /api/v3/otc/offers` is a thin pass-through over this RPC.
- `ListUnifiedOptionOffers(ListUnifiedOptionOffersRequest) returns (ListUnifiedOptionOffersResponse)` — Phase-6 unified cross-bank OTC **option** discovery. SP-1 (2026-06-04): the request gained `acting_owner_type` (field 8) + `acting_owner_id` (field 9), and each `UnifiedOptionOffer` gained `me_owner` (field 20), stamped in the stock-service handler from the acting identity vs. the row's `kind`+`seller_id` (always false for remote rows). The previously-added standalone `GetRemoteOTCOffer` RPC was removed; single-offer reads now go through `OTCOptionsService.GetOffer`, which resolves local→remote itself. **SP-2b (2026-06-05):** each `UnifiedOptionOffer` additionally gained `my_negotiation_id` (field 21, uint64) + `my_negotiation_status` (field 22, string) — the caller's own (as BIDDER) negotiation chain on that offer (`0`/`""` when none). Same source/keying/active-chain tie-break as `GetOffer` above; LOCAL offers key on `LocalID` == `parent_offer_id`, REMOTE offers key on `(routing_number, offer_id-native)` == the chain's `(RemoteParentRouting, RemoteParentNativeID)`. Wired via `OTCHandler.WithMyNegotiations`. Gateway hand-shapes the two fields into the offer rows (omitted when 0).

**Key pattern:** When a proto file has multiple services (e.g., `CardService` + `VirtualCardService` + `CardRequestService`), they all run in the same microservice process on the same port but are registered as separate gRPC services. The API Gateway creates separate client instances that share the same connection address.

**`admin.AdminCron` — Cron registry gRPC interface (C5 — 2026-05-28):**

Every service that runs background cron jobs registers those jobs in a `cronreg.Registry` and exposes the `admin.AdminCron` gRPC service on its existing service port. The proto is defined in `contract/proto/admin/admin_cron.proto` and generated to `contract/adminpb/`. The api-gateway fan-outs to all services via a pool of `AdminCronClient` instances (one per service).

| RPC | Request | Response | Description |
|---|---|---|---|
| `ListCrons` | `ListCronsRequest` (empty) | `ListCronsResponse{crons: [CronInfoMsg]}` | Returns all registered crons |
| `GetCron` | `GetCronRequest{name}` | `CronInfoMsg` | Returns one named cron |
| `TriggerCron` | `TriggerRequest{name, force, triggered_by}` | `CronCtrlResponse{status}` | Fires cron immediately |
| `PauseCron` | `PauseRequest{name, paused_by}` | `CronCtrlResponse{status}` | Pauses scheduling |
| `ResumeCron` | `ResumeRequest{name, resumed_by}` | `CronCtrlResponse{status}` | Resumes paused cron |

Services that currently expose `AdminCron`: `stock-service`, `credit-service`, `account-service`, `card-service`, `transaction-service`, `notification-service`, `user-service`.

---

## 12. Kafka Event System

### Topic Naming

`<service>.<action>` (e.g., `user.employee-created`, `transaction.payment-completed`)

### Message Definition (contract/kafka/messages.go)

```go
type SomethingCreatedMessage struct {
    ID        uint64 `json:"id"`
    Name      string `json:"name"`
    Email     string `json:"email"`
    CreatedAt string `json:"created_at"`
}
```

### Producer Pattern (internal/kafka/producer.go)

```go
type Producer struct {
    writer *kafka.Writer
}

func (p *Producer) PublishSomethingCreated(ctx context.Context, msg *kafkamsg.SomethingCreatedMessage) error {
    data, _ := json.Marshal(msg)
    return p.writer.WriteMessages(ctx, kafka.Message{
        Topic: "service.something-created",
        Key:   []byte(fmt.Sprintf("%d", msg.ID)),
        Value: data,
    })
}
```

### Topic Pre-Creation (internal/kafka/topics.go)

```go
func EnsureTopics(broker string, topics ...string) {
    // Idempotently creates topics on Kafka controller
    // Retries 10 times, 2s apart (handles Docker startup ordering)
}
```

Call in `cmd/main.go`:
```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers,
    "service.something-created",
    "service.something-updated",
    "notification.send-email", // If consuming too
)
```

### Email Notification Pattern

To send an email from any service:

```go
msg := &kafkamsg.SendEmailMessage{
    To:        recipientEmail,
    EmailType: "ACTIVATION", // or PASSWORD_RESET, CONFIRMATION, VERIFICATION_CODE, etc.
    Data: map[string]string{
        "firstName": firstName,
        "link":      activationLink,
    },
}
producer.PublishSendEmail(ctx, msg)
```

Notification service consumes `notification.send-email` and sends via SMTP.

### Notification Template Registry & DB Overrides

Notification template **types** (e.g. `CONFIRMATION`, `ACTIVATION`, `PASSWORD_RESET`) and the `{{variable}}` placeholders each type supports are **code-defined** in a registry inside notification-service — they cannot be changed at runtime. Each registry entry carries a description, a default subject, a default body, and the list of supported variables (name, description, example).

Admins customize only the **text** of a template (subject/body) via the `notifications.templates.manage` REST endpoints, which write a `NotificationTemplate` row keyed on `(type, channel)`. When notification-service renders a message it looks up the DB override first; if no override exists it falls back to the registry default. Reverting (DELETE) removes the override row.

Placeholder substitution syntax is `{{variable_name}}`. At send time each `{{token}}` is replaced with the matching value from the publisher's `Data` map; an unknown or absent token renders as an empty string. Customization is validated against the registry — a subject/body that references a `{{variable}}` the template type does not declare is rejected (HTTP 400), as is an unknown template type (HTTP 404).

---

## 13. Configuration Patterns

### Config Struct Pattern

```go
// internal/config/config.go
type Config struct {
    DBHost     string
    DBPort     string
    DBUser     string
    DBPassword string
    DBName     string
    DBSslmode  string
    GRPCAddr   string
    // Service dependencies
    AuthGRPCAddr string
    // Kafka
    KafkaBrokers string
    // Redis (optional)
    RedisAddr string
}

func Load() *Config {
    return &Config{
        DBHost:       getEnv("SERVICE_DB_HOST", "localhost"),
        DBPort:       getEnv("SERVICE_DB_PORT", "5432"),
        DBUser:       getEnv("SERVICE_DB_USER", "postgres"),
        DBPassword:   getEnv("SERVICE_DB_PASSWORD", "postgres"),
        DBName:       getEnv("SERVICE_DB_NAME", "service_db"),
        DBSslmode:    getEnv("SERVICE_DB_SSLMODE", "require"),
        GRPCAddr:     getEnv("SERVICE_GRPC_ADDR", ":50060"),
        AuthGRPCAddr: getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
        KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
        RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
    }
}

func (c *Config) DSN() string {
    return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
        c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSslmode)
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

### cmd/main.go Startup Pattern

```go
func main() {
    cfg := config.Load()

    // 1. Connect to database
    db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
    // Auto-migrate
    db.AutoMigrate(&model.Entity1{}, &model.Entity2{})

    // 2. Seed default data (if needed)
    model.SeedDefaults(db)

    // 3. Initialize Kafka producer + ensure topics
    producer := kafka.NewProducer(cfg.KafkaBrokers)
    kafka.EnsureTopics(cfg.KafkaBrokers, "service.event-name", ...)

    // 4. Initialize Redis cache (optional, graceful if unavailable)
    redisCache := cache.NewRedisCache(cfg.RedisAddr)

    // 5. Wire repository → service → handler
    repo := repository.NewSomeRepository(db)
    svc := service.NewSomeService(repo, producer)
    handler := handler.NewSomeGRPCHandler(svc)

    // 6. Start gRPC server
    lis, _ := net.Listen("tcp", cfg.GRPCAddr)
    grpcServer := grpc.NewServer()
    pb.RegisterSomeServiceServer(grpcServer, handler)
    grpc_health_v1.RegisterHealthServer(grpcServer, health.NewServer())
    grpcServer.Serve(lis)
}
```

---

## 14. Error Handling

### API Gateway Error Helpers (validation.go)

```go
// Standard error response
apiError(c *gin.Context, httpStatus int, code string, message string, details ...interface{})

// Abort middleware chain with error
apiErrorAbort(c *gin.Context, httpStatus int, code string, message string)

// Map gRPC error to HTTP response
handleGRPCError(c *gin.Context, err error)
```

### Error Code Constants

```go
const (
    ErrValidation   = "validation_error"       // 400
    ErrUnauthorized = "unauthorized"            // 401
    ErrForbidden    = "forbidden"               // 403
    ErrNotFound     = "not_found"               // 404
    ErrConflict     = "conflict"                // 409
    ErrBusinessRule = "business_rule_violation"  // 409
    ErrRateLimited  = "rate_limited"            // 429
    ErrInternal     = "internal_error"          // 500
)
```

### gRPC → HTTP Status Mapping

| gRPC Code | HTTP Status | Error Code |
|---|---|---|
| InvalidArgument | 400 | validation_error |
| Unauthenticated | 401 | unauthorized |
| PermissionDenied | 403 | forbidden |
| NotFound | 404 | not_found |
| AlreadyExists | 409 | conflict |
| FailedPrecondition | 409 | business_rule_violation |
| ResourceExhausted | 429 | rate_limited |
| (default) | 500 | internal_error |

---

## 15. Validation Rules

### API Gateway Validation Helpers

```go
// Enum (case-insensitive, returns normalized lowercase)
oneOf(field, value string, allowed ...string) (string, error)

// Numeric
positive(field string, value float64) error      // > 0
nonNegative(field string, value float64) error   // >= 0
inRange(field string, value, min, max int32) error

// Format
validatePin(pin string) error                    // Exactly 4 digits
validatePaymentCode(code string) error           // 3 digits, starts with 2
validateActivityCode(code string) error          // Format: xx.xx
notEqual(field1, val1, field2, val2 string) error

// Authorization (for /api/me/* routes where clients access their own resources)
enforceClientSelf(c *gin.Context, pathClientID uint64) error  // Checks system_type=="client" && user_id==pathClientID

// Error extraction
grpcMessage(err error) string                    // Extracts message string from gRPC status error
```

### Service Layer Validation

**JMBG:** Exactly 13 digits, numeric only.

**Password:** 8-32 chars, at least 2 digits, 1 uppercase, 1 lowercase. Validated imperatively (no regex).

**Account Number:** Generated server-side, 18-char format.

**Card Number:** Generated server-side, brand-prefixed (Visa: 4xxx, MC: 5xxx, Dina: 6xxx, AmEx: 3xxx).

---

## 16. Docker Compose

### Adding a New Service to docker-compose.yml

```yaml
# 1. Add the database
new-service-db:
  image: postgres:16-alpine
  environment:
    POSTGRES_DB: new_service_db
    POSTGRES_USER: postgres
    POSTGRES_PASSWORD: postgres
  ports:
    - "5440:5432"
  volumes:
    - new-service-db-data:/var/lib/postgresql/data
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U postgres"]
    interval: 5s
    timeout: 5s
    retries: 5

# 2. Add the service
new-service:
  build:
    context: .
    dockerfile: new-service/Dockerfile
  environment:
    NEW_SERVICE_DB_HOST: new-service-db    # Docker service name!
    NEW_SERVICE_DB_PORT: 5432              # Internal port!
    NEW_SERVICE_DB_USER: postgres
    NEW_SERVICE_DB_PASSWORD: postgres
    NEW_SERVICE_DB_NAME: new_service_db
    NEW_SERVICE_GRPC_ADDR: ":50060"
    AUTH_GRPC_ADDR: auth-service:50051     # Docker service names!
    KAFKA_BROKERS: kafka:9092
    REDIS_ADDR: redis:6379
  depends_on:
    new-service-db:
      condition: service_healthy
    kafka:
      condition: service_started
    auth-service:
      condition: service_started

# 3. Add volume
volumes:
  new-service-db-data:

# 4. Wire into api-gateway environment
api-gateway:
  environment:
    NEW_SERVICE_GRPC_ADDR: new-service:50060
  depends_on:
    new-service:
      condition: service_started
```

**Critical:** Use Docker service names (e.g., `auth-service:50051`), not `localhost`.

---

## 17. Complete API Route Reference

### Public Routes (No Auth)

| Method | Path | Handler | Description |
|---|---|---|---|
| POST | `/api/auth/login` | authHandler.Login | Employee login |
| POST | `/api/auth/refresh` | authHandler.RefreshToken | Refresh access token |
| POST | `/api/auth/logout` | authHandler.Logout | Revoke refresh token |
| POST | `/api/auth/password/reset-request` | authHandler.RequestPasswordReset | Request password reset email |
| POST | `/api/auth/password/reset` | authHandler.ResetPassword | Reset password with token |
| POST | `/api/auth/activate` | authHandler.ActivateAccount | Activate new account |
| GET | `/api/exchange/rates` | exchangeHandler.ListExchangeRates | List all exchange rates |
| GET | `/api/exchange/rates/:from/:to` | exchangeHandler.GetExchangeRate | Get specific rate pair |
| POST | `/api/exchange/calculate` | exchangeHandler.CalculateExchange | Calculate conversion |
| POST | `/api/mobile/auth/request-activation` | mobileAuthHandler.RequestActivation | Request mobile activation code |
| POST | `/api/mobile/auth/activate` | mobileAuthHandler.ActivateDevice | Activate mobile device |
| POST | `/api/mobile/auth/refresh` | mobileAuthHandler.RefreshMobileToken | Refresh mobile token |

### User's Own Resources (/api/me/* — AnyAuthMiddleware)

> **Ownership lockdown (as of 2026-04-13):** The following `/api/me/*` routes enforce that the requested resource belongs to the JWT caller. Mismatches return `404 not_found` to avoid leaking existence: `GET /api/me/loans/:id`, `GET /api/me/payments/:id`, `GET /api/me/transfers/:id`, `POST /api/me/cards/:id/pin`, `POST /api/me/cards/:id/verify-pin`, `POST /api/me/cards/:id/temporary-block`, `PUT /api/me/payment-recipients/:id`, `DELETE /api/me/payment-recipients/:id`, `POST /api/me/loan-requests` (body `client_id` is ignored; JWT `user_id` is used), `POST /api/me/cards/virtual` (owner derived from JWT), `POST /api/me/orders`, `POST /api/me/otc/offers/:id/buy` (account ownership verified against JWT caller).

> **Securities reservation flow (Phase 2, 2026-04-22):**
> - `POST /api/v3/me/orders` accepts an optional `security_type` (`stock`|`futures`|`forex`|`option`) and — required when `security_type=forex` — a `base_account_id` (must differ from `account_id` and be owned by the JWT caller). Forex orders must be `direction=buy`. New 400 cases: `forex orders must be direction=buy`, `forex orders require base_account_id`, `base_account_id must differ from account_id`. New 409 case: insufficient available balance on the reservation account.
> - `GET /api/v3/me/orders/:id` responses include `reservation_amount`, `reservation_currency`, `reservation_account_id`, `base_account_id` (forex), `placement_rate`, and `saga_id`. OrderTransaction rows additionally expose `native_amount`, `native_currency`, `converted_amount`, `account_currency`, `fx_rate` for cross-currency fills.
> - `GET /api/v3/me/accounts` and `/api/v3/me/accounts/:id` responses include `reserved_balance` and `available_balance` (stored; `available_balance = balance - reserved_balance`).
> - Settled fills post `LedgerEntry` rows so they appear in `/api/v3/me/accounts/:id/transactions`.

| Method | Path | Middleware Extra | Handler | Description |
|---|---|---|---|---|
| GET | `/api/me` | RequireClientToken | meHandler.GetMe | Get own profile |
| GET | `/api/me/accounts` | - | accountHandler.ListMyAccounts | List own accounts |
| GET | `/api/me/accounts/:id` | - | accountHandler.GetMyAccount | Get own account |
| GET | `/api/me/cards` | - | cardHandler.ListMyCards | List own cards |
| GET | `/api/me/cards/:id` | - | cardHandler.GetMyCard | Get own card |
| POST | `/api/me/cards/:id/pin` | RequireClientToken | cardHandler.SetCardPin | Set card PIN |
| POST | `/api/me/cards/:id/verify-pin` | RequireClientToken | cardHandler.VerifyCardPin | Verify card PIN |
| POST | `/api/me/cards/:id/temporary-block` | RequireClientToken | cardHandler.TemporaryBlockCard | Temp block card |
| POST | `/api/me/cards/virtual` | - | cardHandler.CreateVirtualCard | Create virtual card |
| POST | `/api/me/cards/requests` | RequireClientToken | cardHandler.CreateCardRequest | Request new card |
| GET | `/api/me/cards/requests` | RequireClientToken | cardHandler.ListMyCardRequests | List own card requests |
| POST | `/api/me/payments` | - | txHandler.CreatePayment | Create payment |
| GET | `/api/me/payments` | - | txHandler.ListMyPayments | List own payments |
| GET | `/api/me/payments/:id` | - | txHandler.GetMyPayment | Get own payment |
| POST | `/api/me/payments/:id/execute` | - | txHandler.ExecutePayment | Execute payment with code |
| POST | `/api/me/transfers` | - | txHandler.CreateTransfer | Create transfer |
| POST | `/api/me/transfers/preview` | - | txHandler.PreviewTransfer | Preview transfer fees + exchange rate |
| GET | `/api/me/transfers` | - | txHandler.ListMyTransfers | List own transfers |
| GET | `/api/me/transfers/:id` | - | txHandler.GetMyTransfer | Get own transfer |
| POST | `/api/me/transfers/:id/execute` | - | txHandler.ExecuteTransfer | Execute transfer |
| POST | `/api/me/payment-recipients` | - | txHandler.CreateMyPaymentRecipient | Save recipient |
| GET | `/api/me/payment-recipients` | - | txHandler.ListMyPaymentRecipients | List saved recipients |
| PUT | `/api/me/payment-recipients/:id` | - | txHandler.UpdatePaymentRecipient | Update recipient |
| DELETE | `/api/me/payment-recipients/:id` | - | txHandler.DeletePaymentRecipient | Delete recipient |
| POST | `/api/me/loan-requests` | - | creditHandler.CreateLoanRequest | Submit loan request |
| GET | `/api/me/loan-requests` | - | creditHandler.ListMyLoanRequests | List own loan requests |
| GET | `/api/me/loans` | - | creditHandler.ListMyLoans | List own loans |
| GET | `/api/me/loans/:id` | - | creditHandler.GetMyLoan | Get own loan |
| GET | `/api/me/loans/:id/installments` | - | creditHandler.GetMyInstallments | Get loan installments |
| GET | `/api/me/tax` | - | taxHandler.ListMyTaxRecords | List own capital gains tax records + balance |
| GET | `/api/v3/me/otc/history` | - | OTCOptionsHandler.ListNegotiationHistory | Terminal OTC negotiations (ACCEPTED/REJECTED/EXPIRED/FAILED) for caller; filterable by status / date / counterparty (Celina 3) |
| POST | `/api/v3/me/otc/ratings` | - | OTCOptionsHandler.SubmitRating | Submit a 1..5 score + optional comment for the counterparty of an ACCEPTED OTC offer (Celina 3) |
| GET | `/api/v3/me/otc/ratings/received` | - | OTCOptionsHandler.ListMyReceivedRatings | List ratings the caller has received from OTC counterparties (Celina 3) |
| GET | `/api/v3/otc/traders/:owner_type/:owner_id/rating` | - | OTCOptionsHandler.GetTraderProfile | Public aggregate rating (avg + count) + recent comments for any trader (Celina 3) |
| GET | `/api/v3/me/transfers/:id/status` | - | TransactionHandler.GetMyTransferStatus | Client-facing transfer status (INITIATED/PENDING/COMPLETED/FAILED) mapped from internal lifecycle (Celina 4) |
| GET | `/api/v3/me/recurring-orders` | - | RecurringOrderHandler.ListMy | List caller's recurring securities-order templates (Celina 3) |
| POST | `/api/v3/me/recurring-orders` | - | RecurringOrderHandler.Create | Create weekly/monthly Market-order template (Celina 3) |
| GET | `/api/v3/me/recurring-orders/:id` | - | RecurringOrderHandler.Get | Read one recurring order (Celina 3) |
| POST | `/api/v3/me/recurring-orders/:id/pause` | - | RecurringOrderHandler.Pause | Pause a recurring order (Celina 3) |
| POST | `/api/v3/me/recurring-orders/:id/resume` | - | RecurringOrderHandler.Resume | Resume a paused recurring order (Celina 3) |
| POST | `/api/v3/me/recurring-orders/:id/cancel` | - | RecurringOrderHandler.Cancel | Cancel a recurring order (Celina 3) |
| GET | `/api/v3/me/recurring-funds` | - | RecurringFundHandler.ListMy | List caller's monthly DCA fund-investment templates (Celina 4) |
| POST | `/api/v3/me/recurring-funds` | - | RecurringFundHandler.Create | Create a monthly DCA fund-investment template (Celina 4) |
| GET | `/api/v3/me/recurring-funds/:id` | - | RecurringFundHandler.Get | Read one recurring fund investment (Celina 4) |
| POST | `/api/v3/me/recurring-funds/:id/pause` | - | RecurringFundHandler.Pause | Pause a recurring fund investment (Celina 4) |
| POST | `/api/v3/me/recurring-funds/:id/resume` | - | RecurringFundHandler.Resume | Resume a paused recurring fund investment (Celina 4) |
| DELETE | `/api/v3/me/recurring-funds/:id` | - | RecurringFundHandler.Cancel | Cancel a recurring fund investment (Celina 4) |
| GET | `/api/v3/me/price-alerts` | - | PriceAlertHandler.ListMy | List caller's price alerts (Celina 3) |
| POST | `/api/v3/me/price-alerts` | - | PriceAlertHandler.Create | Create a price alert (gte/lte/daily_change_pct_*) (Celina 3) |
| GET | `/api/v3/me/price-alerts/:id` | - | PriceAlertHandler.Get | Read one alert (Celina 3) |
| PUT | `/api/v3/me/price-alerts/:id` | - | PriceAlertHandler.Update | Update an alert (Celina 3) |
| DELETE | `/api/v3/me/price-alerts/:id` | - | PriceAlertHandler.Delete | Delete an alert (Celina 3) |
| GET | `/api/v3/me/watchlist` | - | WatchlistHandler.ListMy | List tracked listings with current prices + daily change (Celina 3) |
| POST | `/api/v3/me/watchlist` | - | WatchlistHandler.AddItem | Add a listing to the watchlist (idempotent on duplicates) |
| DELETE | `/api/v3/me/watchlist/:listing_id` | - | WatchlistHandler.RemoveItem | Remove a listing from the watchlist |
| GET | `/api/v3/me/notifications` | - | notifHandler.ListNotifications | List general notifications (v1 only) |
| GET | `/api/v3/me/notifications/unread-count` | - | notifHandler.GetUnreadCount | Get unread notification count (v1 only) |
| POST | `/api/v3/me/notifications/:id/read` | - | notifHandler.MarkRead | Mark notification as read (v1 only) |
| POST | `/api/v3/me/notifications/read-all` | - | notifHandler.MarkAllRead | Mark all as read (v1 only) |

### Employee/Admin Routes (AuthMiddleware + RequirePermission)

| Method | Path | Permission | Handler | Description |
|---|---|---|---|---|
| GET | `/api/employees` | employees.read | empHandler.ListEmployees | List employees |
| GET | `/api/employees/:id` | employees.read | empHandler.GetEmployee | Get employee |
| POST | `/api/employees` | employees.create | empHandler.CreateEmployee | Create employee |
| PUT | `/api/employees/:id` | employees.update | empHandler.UpdateEmployee | Update employee |
| GET | `/api/roles` | employees.permissions | roleHandler.ListRoles | List roles |
| GET | `/api/roles/:id` | employees.permissions | roleHandler.GetRole | Get role |
| POST | `/api/roles` | employees.permissions | roleHandler.CreateRole | Create role |
| PUT | `/api/roles/:id/permissions` | employees.permissions | roleHandler.UpdateRolePermissions | Set role permissions |
| GET | `/api/permissions` | employees.permissions | roleHandler.ListPermissions | List all permissions |
| PUT | `/api/employees/:id/roles` | employees.permissions | roleHandler.SetEmployeeRoles | Assign roles |
| PUT | `/api/employees/:id/permissions` | employees.permissions | roleHandler.SetEmployeeAdditionalPermissions | Set extra perms |
| GET | `/api/employees/:id/limits` | limits.manage | limitHandler.GetEmployeeLimits | Get employee limits |
| PUT | `/api/employees/:id/limits` | limits.manage | limitHandler.SetEmployeeLimits | Set employee limits |
| POST | `/api/employees/:id/limits/template` | limits.manage | limitHandler.ApplyLimitTemplate | Apply limit template |
| GET | `/api/limits/templates` | limits.manage | limitHandler.ListLimitTemplates | List templates |
| POST | `/api/limits/templates` | limits.manage | limitHandler.CreateLimitTemplate | Create template |
| GET | `/api/clients/:id/limits` | limits.manage | limitHandler.GetClientLimits | Get client limits |
| PUT | `/api/clients/:id/limits` | limits.manage | limitHandler.SetClientLimits | Set client limits |
| GET | `/api/clients` | clients.read | clientHandler.ListClients | List clients |
| GET | `/api/clients/:id` | clients.read | clientHandler.GetClient | Get client |
| POST | `/api/clients` | clients.create | clientHandler.CreateClient | Create client |
| PUT | `/api/clients/:id` | clients.update | clientHandler.UpdateClient | Update client |
| GET | `/api/currencies` | (any employee) | accountHandler.ListCurrencies | List currencies |
| GET | `/api/accounts` | accounts.read | accountHandler.ListAllAccounts | List accounts |
| GET | `/api/accounts/:id` | accounts.read | accountHandler.GetAccount | Get account |
| GET | `/api/accounts?account_number=X` | accounts.read | accountHandler.ListAllAccounts | Look up by number (returns array of 0-1 items) |
| POST | `/api/accounts` | accounts.create | accountHandler.CreateAccount | Create account |
| PUT | `/api/accounts/:id/name` | accounts.update | accountHandler.UpdateAccountName | Update name |
| PUT | `/api/accounts/:id/limits` | accounts.update | accountHandler.UpdateAccountLimits | Update limits |
| POST | `/api/accounts/:id/activate` | accounts.deactivate.any | accountHandler.ActivateAccount | Activate account |
| POST | `/api/accounts/:id/deactivate` | accounts.deactivate.any | accountHandler.DeactivateAccount | Deactivate account |
| POST | `/api/companies` | accounts.create | accountHandler.CreateCompany | Create company |
| GET | `/api/bank-accounts` | bank-accounts.manage | accountHandler.ListBankAccounts | List bank accounts |
| GET | `/api/v3/bank-accounts/:id/activity` | bank-accounts.manage | accountHandler.GetBankAccountActivity | Ledger activity for a bank-owned account; non-bank account id → 404 |
| POST | `/api/bank-accounts` | bank-accounts.manage | accountHandler.CreateBankAccount | Create bank account |
| DELETE | `/api/bank-accounts/:id` | bank-accounts.manage | accountHandler.DeleteBankAccount | Delete bank account |
| GET | `/api/clients/:id/cards` | cards.read | cardHandler.ListCardsByClientPath | List cards by client |
| GET | `/api/accounts/:id/cards` | cards.read | cardHandler.ListCardsByAccountPath | List cards by account |
| GET | `/api/cards/:id` | cards.read | cardHandler.GetCard | Get card |
| POST | `/api/cards` | cards.create | cardHandler.CreateCard | Create card |
| POST | `/api/cards/authorized-persons` | cards.create | cardHandler.CreateAuthorizedPerson | Add auth person |
| POST | `/api/cards/:id/block` | cards.update | cardHandler.BlockCard | Block card |
| POST | `/api/cards/:id/unblock` | cards.update | cardHandler.UnblockCard | Unblock card |
| POST | `/api/cards/:id/deactivate` | cards.update | cardHandler.DeactivateCard | Deactivate card |
| GET | `/api/cards/requests` | cards.approve | cardHandler.ListCardRequests | List card requests |
| GET | `/api/cards/requests/:id` | cards.approve | cardHandler.GetCardRequest | Get card request |
| POST | `/api/cards/requests/:id/approve` | cards.approve | cardHandler.ApproveCardRequest | Approve request |
| POST | `/api/cards/requests/:id/reject` | cards.approve | cardHandler.RejectCardRequest | Reject request |
| GET | `/api/clients/:id/payments` | accounts.read | txHandler.ListPaymentsByClientPath | List payments by client |
| GET | `/api/accounts/:id/payments` | accounts.read | txHandler.ListPaymentsByAccountPath | List payments by account |
| GET | `/api/payments/:id` | payments.read | txHandler.GetPayment | Get payment |
| GET | `/api/clients/:id/transfers` | accounts.read | txHandler.ListTransfersByClientPath | List transfers by client |
| GET | `/api/transfers/:id` | payments.read | txHandler.GetTransfer | Get transfer |
| GET | `/api/fees` | fees.manage | txHandler.ListFees | List fee rules |
| POST | `/api/fees` | fees.manage | txHandler.CreateFee | Create fee rule |
| PUT | `/api/fees/:id` | fees.manage | txHandler.UpdateFee | Update fee rule |
| DELETE | `/api/fees/:id` | fees.manage | txHandler.DeleteFee | Delete fee rule |
| GET | `/api/loans` | credits.read | creditHandler.ListAllLoans | List all loans |
| GET | `/api/loans/:id` | credits.read | creditHandler.GetLoan | Get loan |
| GET | `/api/loans/:id/installments` | credits.read | creditHandler.GetInstallmentsByLoan | Get installments |
| GET | `/api/loan-requests` | credits.read | creditHandler.ListLoanRequests | List loan requests |
| GET | `/api/loan-requests/:id` | credits.read | creditHandler.GetLoanRequest | Get loan request |
| POST | `/api/loan-requests/:id/approve` | credits.approve | creditHandler.ApproveLoanRequest | Approve loan |
| POST | `/api/loan-requests/:id/reject` | credits.approve | creditHandler.RejectLoanRequest | Reject loan |
| GET | `/api/interest-rate-tiers` | interest-rates.manage | creditHandler.ListInterestRateTiers | List rate tiers |
| POST | `/api/interest-rate-tiers` | interest-rates.manage | creditHandler.CreateInterestRateTier | Create tier |
| PUT | `/api/interest-rate-tiers/:id` | interest-rates.manage | creditHandler.UpdateInterestRateTier | Update tier |
| DELETE | `/api/interest-rate-tiers/:id` | interest-rates.manage | creditHandler.DeleteInterestRateTier | Delete tier |
| POST | `/api/interest-rate-tiers/:id/apply` | interest-rates.manage | creditHandler.ApplyVariableRateUpdate | Apply rate update |
| GET | `/api/bank-margins` | interest-rates.manage | creditHandler.ListBankMargins | List margins |
| PUT | `/api/bank-margins/:id` | interest-rates.manage | creditHandler.UpdateBankMargin | Update margin |
| POST | `/api/v3/stock-sources` | securities.manage.catalog | stockSourceHandler.SwitchSource | Switch active stock data source (destructive) |
| GET | `/api/v3/stock-sources/active` | securities.manage.catalog | stockSourceHandler.GetSourceStatus | Get current stock data source and status |
| POST | `/api/v3/orders` | orders.place-on-behalf | stockHandler.CreateOrderOnBehalf | Employee places stock order on behalf of a named client; gateway verifies account belongs to client (mismatch → 403) |
| POST | `/api/v3/otc/offers/:id/buy-on-behalf` | otc.trade.accept or otc.trade.on_behalf | otcHandler.BuyOTCOfferOnBehalf | Employee buys OTC offer on behalf of a named client; gateway verifies account belongs to client (mismatch → 403) |
| GET | `/api/v3/clients/:id/accounts` | accounts.read | accountHandler.ListAccountsByClientPath | List accounts by client |
| GET | `/api/v3/clients/:id/loans` | credits.read | creditHandler.ListLoansByClientPath | List loans by client |
| GET | `/api/v3/accounts/:id/changelog` | accounts.read.all | changelogHandler.GetAccountChangelog | Account audit log |
| GET | `/api/v3/cards/:id/changelog` | cards.read.all | changelogHandler.GetCardChangelog | Card audit log |
| GET | `/api/v3/clients/:id/changelog` | clients.read.all | changelogHandler.GetClientChangelog | Client audit log |
| GET | `/api/v3/loans/:id/changelog` | credits.read.all | changelogHandler.GetLoanChangelog | Loan audit log |
| GET | `/api/v3/employees/:id/changelog` | employees.read.all | changelogHandler.GetEmployeeChangelog | Employee audit log |
| DELETE | `/api/v3/me/sessions/:id` | (any auth) | sessionHandler.RevokeSession | Revoke a session by ID |
| POST | `/api/v3/actuaries/:id/require-approval` | employees.update.any | actuaryHandler.RequireApproval | Require supervisor approval for actuary |
| POST | `/api/v3/actuaries/:id/skip-approval` | employees.update.any | actuaryHandler.SkipApproval | Remove approval requirement for actuary |
| POST | `/api/v3/orders/:id/reject` | orders.cancel.all | stockOrderHandler.RejectOrder | Reject a pending order (renamed from /decline) |
| GET | `/api/v3/peer-banks` | peer_banks.manage.any | PeerBankAdminHandler.List | Admin: list peers. Phase 2 SI-TX. |
| GET | `/api/v3/peer-banks/:id` | peer_banks.manage.any | PeerBankAdminHandler.Get | Admin: read one. Phase 2 SI-TX. |
| POST | `/api/v3/peer-banks` | peer_banks.manage.any | PeerBankAdminHandler.Create | Admin: register a peer. Phase 2 SI-TX. |
| PUT | `/api/v3/peer-banks/:id` | peer_banks.manage.any | PeerBankAdminHandler.Update | Admin: update mutable fields. Phase 2 SI-TX. |
| DELETE | `/api/v3/peer-banks/:id` | peer_banks.manage.any | PeerBankAdminHandler.Delete | Admin: remove a peer. Phase 2 SI-TX. |
| GET | `/api/v3/notification-templates` | notifications.templates.manage | NotificationHandler.ListNotificationTemplates | List all notification template types with supported `{{variables}}`, defaults, and current text. Optional `channel` query param (`email`/`push`). Discovery endpoint. |
| GET | `/api/v3/notification-templates/:channel/:type` | notifications.templates.manage | NotificationHandler.GetNotificationTemplate | Get a single notification template; unknown type → 404 |
| PUT | `/api/v3/notification-templates/:channel/:type` | notifications.templates.manage | NotificationHandler.SetNotificationTemplate | Customize a template's subject/body; placeholder referencing an unknown variable or empty subject/body → 400; unknown type → 404 |
| DELETE | `/api/v3/notification-templates/:channel/:type` | notifications.templates.manage | NotificationHandler.ResetNotificationTemplate | Revert a template to its code-defined default; unknown type → 404 |

### Securities Market Data (/api/v3/securities — AnyAuthMiddleware)

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/api/v3/securities/stocks` | SecuritiesHandler.ListStocks | List all stock listings |
| GET | `/api/v3/securities/stocks/:id` | SecuritiesHandler.GetStock | Get one stock listing |
| GET | `/api/v3/securities/stocks/:id/history` | SecuritiesHandler.GetStockHistory | Returns OHLC-bucketed price history. On a freshly-seeded DB the response is non-empty for every period — `stock-service` writes 5 years of deterministic synthetic daily OHLC per listing during `SeedAll`. Live intraday snapshots (1-minute interval) accumulate on top of synthetic history. |
| GET | `/api/v3/securities/futures` | SecuritiesHandler.ListFutures | List all futures listings |
| GET | `/api/v3/securities/futures/:id` | SecuritiesHandler.GetFutures | Get one futures listing |
| GET | `/api/v3/securities/futures/:id/history` | SecuritiesHandler.GetFuturesHistory | OHLC-bucketed price history for a futures listing — same backfill and accumulation behaviour as `/stocks/:id/history`. |
| GET | `/api/v3/securities/forex` | SecuritiesHandler.ListForexPairs | List all forex pair listings. Actuary-only (`DenyClientToken`) — clients get 403. |
| GET | `/api/v3/securities/forex/:id` | SecuritiesHandler.GetForexPair | Get one forex pair listing. Actuary-only — clients get 403. |
| GET | `/api/v3/securities/forex/:id/history` | SecuritiesHandler.GetForexPairHistory | OHLC-bucketed price history for a forex pair — same backfill and accumulation behaviour as `/stocks/:id/history`. Actuary-only — clients get 403. |
| GET | `/api/v3/securities/options` | SecuritiesHandler.ListOptions | List all options listings. Actuary-only (`DenyClientToken`) — clients get 403. |
| GET | `/api/v3/securities/options/:id` | SecuritiesHandler.GetOption | Get one options listing. Actuary-only — clients get 403. |
| GET | `/api/v3/securities/candles` | SecuritiesHandler.GetCandles | Get intraday OHLC candles (1-minute snapshots); query params `listing_id`, `period` |

### Peer-Bank Protocol (Celina 5 SI-TX — PeerAuth)

These routes are reached by other banks in the SI-TX cohort, not by employees or clients. Authentication is via `middleware.PeerAuth` (hybrid `X-Api-Key` or HMAC headers — see [§25](#25-inter-bank-cross-bank-communication-celina-5--si-tx)).

**Cross-bank protocol routes are served exclusively at `/api/v3/cross-bank-protocol/...`. Cohort banks MUST register this bank's `base_url` ending in `/api/v3/cross-bank-protocol` to interoperate. Legacy paths (`/api/v3/interbank`, `/api/v3/public-stock`, `/api/v3/negotiations/*`, `/api/v3/user/*`) were removed on 2026-05-29.**

| Method | Path | Middleware | Handler | Description |
|---|---|---|---|---|
| POST | `/api/v3/cross-bank-protocol/interbank` | PeerAuth | PeerTxHandler.PostInterbank | SI-TX `Message<Type>` envelope. Phase 3. |
| GET | `/api/v3/cross-bank-protocol/interbank/:transaction_id/status` | PeerAuth | PeerTxStatusHandler.GetTxStatus | Celina-5 CHECK_STATUS: peer queries cross-bank TX state. |
| GET | `/api/v3/cross-bank-protocol/public-stock` | PeerAuth | PeerOTCHandler.GetPublicStocks | Lists own bank's OTC option offers — the sole cross-bank option-discovery surface. Phase 4. |
| POST | `/api/v3/cross-bank-protocol/negotiations` | PeerAuth | PeerOTCHandler.CreateNegotiation | Peer-initiated cross-bank OTC offer. Phase 4. |
| PUT | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | PeerAuth | PeerOTCHandler.UpdateNegotiation | Counter-offer. Phase 4. SI-TX §3.3 turn/closed guards: **409** if closed or out of turn (2.9.2). |
| GET | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | PeerAuth | PeerOTCHandler.GetNegotiation | Read negotiation state. Phase 4. |
| DELETE | `/api/v3/cross-bank-protocol/negotiations/:rid/:id` | PeerAuth | PeerOTCHandler.DeleteNegotiation | Cancel. Phase 4. |
| GET | `/api/v3/cross-bank-protocol/negotiations/:rid/:id/accept` | PeerAuth | PeerOTCHandler.AcceptNegotiation | Triggers 4-posting TX via PeerTxService. Phase 4. |
| GET | `/api/v3/cross-bank-protocol/user/:rid/:id` | PeerAuth | PeerUserHandler.GetUser | Counterparty user info lookup. Phase 4. |

### Browser Verification (/api/verifications — AnyAuthMiddleware)

| Method | Path | Handler | Description |
|---|---|---|---|
| POST | `/api/verifications` | verifyHandler.CreateVerification | Create verification challenge |
| GET | `/api/verifications/:id/status` | verifyHandler.GetVerificationStatus | Poll challenge status |
| POST | `/api/verifications/:id/code` | verifyHandler.SubmitVerificationCode | Submit code (browser) |

### Mobile Device Management (/api/mobile/device — MobileAuthMiddleware)

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/api/mobile/device` | mobileAuthHandler.GetDeviceInfo | Get device info |
| POST | `/api/mobile/device/deactivate` | mobileAuthHandler.DeactivateDevice | Deactivate device |
| POST | `/api/mobile/device/transfer` | mobileAuthHandler.TransferDevice | Transfer to new device |

### Mobile Verification (/api/mobile/verifications — MobileAuth + DeviceSignature)

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/api/mobile/verifications/pending` | verifyHandler.GetPendingVerifications | Poll pending items |
| POST | `/api/mobile/verifications/:challenge_id/submit` | verifyHandler.SubmitMobileVerification | Submit mobile response |
| POST | `/api/verify/:challenge_id` | verifyHandler.VerifyQR | QR code verification |

### WebSocket

**Removed 2026-06-11 (VERSION 4.2.0).** There is no WebSocket endpoint. The
`wsHandler.HandleConnect` handler was never wired to a route, so `/ws/mobile` never
existed; the dead handler, its `notification.mobile-push` Kafka feed, the
`MobilePushMessage` contract type, and the `gorilla/websocket` dependency were all
removed. The mobile app receives verification challenges by polling
`GET /api/v3/mobile/verifications/pending` (the `mobile_inbox_items` row is the sole
source of truth) and general notifications via `GET /api/v3/me/notifications`.

### Swagger

| Method | Path | Description |
|---|---|---|
| GET | `/swagger/*any` | Swagger UI |

### OTC Stocks Marketplace — REMOVED (2026-06-11)

The in-bank OTC stocks marketplace (`/api/v3/otc/stocks/*`, `/api/v3/me/otc/stocks/*`, `OTCStockMarketGRPCService`, `OTCStockService`, `OTCStockBuyOffer`, `make-public`, `Holding.public_quantity`) was deleted — the frontend's "market tab" was retired. Option offers (§ OTC Options) now serve as the cross-bank "stock" inventory on `/public-stock` (MAJOR bump 4.0.0).

### OTC Options Marketplace — parallel negotiation chains (Phase 2 / 6)

Many bidders can each open their own negotiation chain against the same listing; first-to-accept wins atomically (cascade-cancels siblings). See [REST_API_v3 §47.2](api/REST_API_v3.md#472-options-marketplace--parallel-negotiation-chains). Replaces the deleted `/otc/offers/*` option routes.

| Method | Path | Handler | Description |
|---|---|---|---|
| GET    | `/api/v3/otc/options`                                  | PortfolioHandler.ListOTCOptions             | Unified local + cross-bank discovery of OPEN option listings |
| GET    | `/api/v3/otc/options/:id`                              | OTCOptionsHandler.GetOffer                  | Detail (single listing) |
| GET    | `/api/v3/otc/options/:id/negotiations`                 | OTCOptionsHandler.ListNegotiationsOnListing | Every chain on a listing — visible to all parties |
| POST   | `/api/v3/otc/options/:id/bid`                          | OTCOptionsHandler.OpenNegotiationChain      | Place a bid — opens a new negotiation chain |
| GET    | `/api/v3/me/otc/options`                               | PortfolioHandler.ListMyOTCOptions           | Marketplace shape, scoped to caller's open listings (owner_only_seller_id filter on the unified cache) |
| GET    | `/api/v3/me/otc/options/posted`                        | OTCOptionsHandler.ListMyPostedOffers        | Full history — every listing the caller posted, any status; raw `OTCOfferResponse` rows |
| POST   | `/api/v3/me/otc/options`                               | OTCOptionsHandler.CreateOffer               | Create a **termless** option listing `(owner, ticker, quantity)` — no strike/premium/settlement (terms negotiated per chain); one open offer per `(owner, ticker, direction)` (duplicate ⇒ 409) |
| PUT    | `/api/v3/me/otc/options/:id`                           | OTCOptionsHandler.UpdateMyOption            | Owner-only — SET the offer's total quantity (>0, ≥ committed shares, ≤ owner's holding) |
| DELETE | `/api/v3/me/otc/options/:id`                           | OTCOptionsHandler.CancelMyListing           | Initiator-only — flips parent to `cancelled` and cascade-cancels all open child chains in one TX |
| GET    | `/api/v3/me/otc/options/negotiations`                  | OTCOptionsHandler.ListMyNegotiations        | Caller's LOCAL + REMOTE chains, merged with `kind`/provenance/`me_owner` (SP-1) |
| POST   | `/api/v3/me/otc/options/:id/negotiations/:nid/counter` | OTCOptionsHandler.CounterMyNegotiation      | Counter current terms |
| POST   | `/api/v3/me/otc/options/:id/negotiations/:nid/accept`  | OTCOptionsHandler.AcceptMyNegotiation       | Accept — first-accept-wins atomic TX |
| POST   | `/api/v3/me/otc/options/:id/negotiations/:nid/reject`  | OTCOptionsHandler.RejectMyNegotiation       | Reject one chain only |
| DELETE | `/api/v3/me/otc/options/:id/negotiations/:nid`         | OTCOptionsHandler.CancelMyNegotiation       | Bidder withdraws their own chain |
| GET    | `/api/v3/me/otc/contracts`                             | OTCOptionsHandler.ListMyContracts           | Caller's LOCAL + REMOTE contracts, merged; each item has `kind`/`routing_number`/`bank_code`/`me_owner` (SP-1 Task 8) |
| GET    | `/api/v3/otc/contracts/:id`                            | OTCOptionsHandler.GetContract               | Single contract — resolves local→remote; `kind`/`me_owner` stamped in service layer (SP-1 Task 8) |

**SP-1 unified-read semantics (2026-06-04):** All OTC option read endpoints (offers, negotiations, contracts) return items with four provenance/ownership fields:

| Field | Meaning |
|---|---|
| `kind` | `"local"` — this bank hosts the record; `"remote"` — sourced from a peer-bank mirror. |
| `routing_number` | Owning bank's routing number (own for local; the COUNTERPARTY peer for remote). |
| `bank_code` | 3-digit bank code matching `routing_number`. |
| `me_owner` | Ownership flag. Semantics differ by resource: **offers + negotiations**: `true` when the caller is the poster/seller (originator); bidders are always `false`. **contracts**: `true` ONLY when the caller is the **buyer/holder** (the seller/writer is always `false`). For remote rows: computed from whether this bank hosts the relevant side. `me_owner` is omitted (falsy proto3 omitempty) when not owned. |

The gateway is a **uniform passthrough** — all provenance and ownership computation happens in the stock-service handler, never in the gateway.

### Unified Portfolio Routes (B1–B8, 2026-05-28)

All routes call `GetUnifiedPortfolio` on `PortfolioGRPCService` and return a grouped response with per-position unrealised P/L. See [REST_API_v3 §48](api/REST_API_v3.md#48-unified-portfolio-routes).

**Portfolio identity encoding** — the `portfolio_id` path parameter is a URL-safe string:

| Value | Decoded as |
|---|---|
| `client-<n>` | client owner, id = n |
| `bank` | bank owner (no id) |
| `fund-<n>` | investment_fund owner, id = n |

**`/api/v3/me/portfolio` — AnyAuthMiddleware + `OwnerIsBankIfEmployee`**

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/api/v3/me/portfolio` | UnifiedPortfolioHandler.GetMy | Caller's unified portfolio — employee sees bank, client sees own |

**`/api/v3/portfolio/*` — AnyAuthMiddleware + `OwnerIsBankIfEmployee`**

| Method | Path | Permission Required | Handler | Description |
|---|---|---|---|---|
| GET | `/api/v3/portfolio/bank` | (employee, any) | UnifiedPortfolioHandler.GetBank | Bank's unified portfolio |
| GET | `/api/v3/portfolio/client/:client_id` | `portfolio.view.client` | UnifiedPortfolioHandler.GetByClientID | Any client's portfolio (employee only) |
| GET | `/api/v3/portfolio/investment-fund/:fund_id` | `portfolio.view.fund` | UnifiedPortfolioHandler.GetByFundID | Any fund's portfolio |
| GET | `/api/v3/portfolio/:portfolio_id` | varies (see encoding) | UnifiedPortfolioHandler.GetByPortfolioID | Generic portfolio by encoded id |

**`/api/v3/watchlist/:portfolio_id` — AnyAuthMiddleware + `OwnerIsBankIfEmployee`**

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/api/v3/watchlist/:portfolio_id` | WatchlistHandler.GetByPortfolioID | Watchlist for any owner identified by encoded portfolio_id |

**Access control summary:**
- A client principal may only fetch their own portfolio (`client-<own_id>`).
- An employee principal may always fetch the bank portfolio; fetching a client or fund portfolio requires `portfolio.view.client` or `portfolio.view.fund` respectively.
- `EmployeeSupervisor` and `EmployeeAdmin` hold both permissions by default.

**`/api/v3/admin/crons/*` — Admin Cron Viewer (C10 — 2026-05-28)**

Protected by `AuthMiddleware` (employee JWT). Each sub-group has a distinct permission:

| Method | Path | Permission | Handler | Description |
|---|---|---|---|---|
| GET | `/api/v3/admin/crons` | `admin.crons.view` | AdminCronHandler.List | Fan-out list of all crons across every service |
| GET | `/api/v3/admin/crons/:service/:name` | `admin.crons.view` | AdminCronHandler.Get | One cron's detail from the named service |
| POST | `/api/v3/admin/crons/:service/:name/trigger` | `admin.crons.trigger` | AdminCronHandler.Trigger | Manually fire a cron; optional body `{"force": bool, "reason": string}` |
| POST | `/api/v3/admin/crons/:service/:name/pause` | `admin.crons.manage` | AdminCronHandler.Pause | Pause a cron; optional body `{"reason": string}` |
| POST | `/api/v3/admin/crons/:service/:name/resume` | `admin.crons.manage` | AdminCronHandler.Resume | Resume a paused cron; optional body `{"reason": string}` |

`GET /api/v3/admin/crons` fans out in parallel (`errgroup`) to all configured services. Each service appears as a result entry with `status: "ok"` or `status: "unreachable"`. An unreachable service does NOT fail the whole response. After a successful Trigger/Pause/Resume the gateway publishes an `AdminCronActionMessage` to `admin.cron-action` (see §19). `:service` must match an exact label (e.g. `stock-service`, `credit-service`).

**`/api/v3/admin/audit/*` — Admin Audit Log Reader (D4 — 2026-05-28)**

Six global changelog read endpoints. All require `admin.audit.view` (EmployeeAdmin only). Common query params: `page` (default 1), `page_size` (default 50, max 200), `since=YYYY-MM-DD`, `until=YYYY-MM-DD`, `actor_id` (employee ID), `action` (exact string match).

| Method | Path | Permission | Handler | Description |
|---|---|---|---|---|
| GET | `/api/v3/admin/audit/clients-changelog` | `admin.audit.view` | AdminAuditHandler.ListClientsChangelog | Global changelog from client-service |
| GET | `/api/v3/admin/audit/accounts-changelog` | `admin.audit.view` | AdminAuditHandler.ListAccountsChangelog | Global changelog from account-service |
| GET | `/api/v3/admin/audit/cards-changelog` | `admin.audit.view` | AdminAuditHandler.ListCardsChangelog | Global changelog from card-service |
| GET | `/api/v3/admin/audit/loans-changelog` | `admin.audit.view` | AdminAuditHandler.ListLoansChangelog | Global changelog from credit-service |
| GET | `/api/v3/admin/audit/employees-changelog` | `admin.audit.view` | AdminAuditHandler.ListEmployeesChangelog | Global changelog from user-service |
| GET | `/api/v3/admin/audit/cron-actions` | `admin.audit.view` | AdminAuditHandler.ListCronActions | Admin cron-action audit log from notification-service |
| GET | `/api/v3/admin/audit/business-actions` | `admin.audit.view` | AdminAuditHandler.ListBusinessActions | Business-action audit log from notification-service (SP2 — 2026-06-04) |

Response shape: `{entries: [...], total, page, page_size}`. Changelog entries carry `{id, entity_type, entity_id, action, field_name, old_value, new_value, actor_id, timestamp, reason}`. Cron-action entries carry `{id, action, service, cron_name, employee_id, reason, timestamp}`. Business-action entries carry `{id, action, actor_id, target_type, target_id, detail, timestamp}` and filter by `action` (`limit.set`|`limit.used_reset`|`order.approve`|`order.decline`|`permissions.set`|`tax.collect`), `target_type` (`employee`|`order`|`role`|`tax`), `actor_id`, and date range. The gateway publishes a `BusinessAuditActionMessage` to `admin.business-action` (actor from JWT) after each audited action succeeds (best-effort); notification-service records it into `business_audit_logs`.

---

## 18. Complete Entity Reference

> **New feature entities:** Investment-fund entities are catalogued in [§24](#24-investment-funds-celina-4). Intra-bank OTC option entities (`OTCOffer`, `OTCOfferRevision`, `OptionContract`, `OTCOfferReadReceipt`) are in [§26](#26-intra-bank-otc-options-celina-4--spec-2). Cross-bank OTC additions (`InterBankSagaLog`; `OTCOffer.Public/Private`; `OptionContract.CrossbankTxID/CrossbankExerciseTxID`; `HoldingReservation.OTCContractID`) are in [§27](#27-cross-bank-otc-options-celina-5--spec-4--foundation). The `Order` model gained a `FundID *uint64` column for on-behalf-of-fund order placement.
>
> **OTC marketplace refactor (Phases 1B / 3 / 3B) entities:**
> - `OTCStockBuyOffer` — **REMOVED 2026-06-11** (in-bank OTC stock marketplace deletion). The model, its `otc_stock_buy_offers` table, and the `otc_stock_buy_offer_res_seq` sequence are dropped on startup.
> - `OTCNegotiation` — one bidder's negotiation chain against a parent `OTCOffer` listing (Phase 2 parallel-chains model). Unique index `(parent_offer_id, bidder_owner_type, bidder_owner_id)` enforces one chain per bidder per listing. Status enum `open|countered|accepted|rejected|cancelled|expired`. New field `minted_contract_id *uint64` (indexed, nullable): set after contract-formation saga succeeds on a `status=accepted` row, so the list endpoint can return a direct link from negotiation → contract.
> - `OTCNegotiationRevision` — append-only history row for one move (BID, COUNTER, ACCEPT, REJECT) within an `OTCNegotiation`. Unique index `(negotiation_id, revision_number)` enforces monotonic ordering. Exposed via `GET /api/v3/me/otc/options/negotiations/:nid/revisions` (authorization: bidder or listing poster only).
> - `OTCOffer` (existing model) — a **termless** marketplace LISTING: `(initiator owner, ticker, quantity, direction)` with status `open|consumed|cancelled` (legacy `PENDING|COUNTERED` aliased as "open" via `IsOpenListing()` helper). The preset-term columns (strike/premium/settlement_date) were **dropped** — terms are negotiated per chain. Per-bidder chains live in `OTCNegotiation` rows above. **One open offer per `(initiator owner, ticker, direction)`** is enforced by a partial unique index over open rows (a merge-duplicate startup migration collapses any pre-existing duplicates); resizing the lot total goes through `UpdateOTCOfferQuantity` / `PUT /api/v3/me/otc/options/:id`. The legacy single-chain Accept/Counter/Reject path that mutated the offer in place was **deleted** (offers now end only via cancel/accept; they no longer auto-expire — contract expiry is unchanged). A **cross-bank** accept (Direction 2: our user is the seller and a peer bidder's bid is accepted) also consumes the local listing — but because the termless `/public-stock` wire carries no offer id, the listing cannot be reached by `parent_offer_id`; it is resolved + flipped `consumed` by its `(owner, ticker, sell_initiated)` unique key (`OTCNegotiationService.ConsumeLocalSellOfferForSeller` → `OTCOfferRepository.ConsumeOpenByOwnerTickerDirection`; best-effort so a consume failure never reverses an already-formed contract). BOTH cross-bank accept paths apply this symmetrically when `sellerRouting == OwnRouting()`: the **outbound** `acceptRemoteNegotiation` (our seller accepts the buyer's last bid) and the **inbound** `PeerOTCGRPCHandler.AcceptNegotiation` (the peer buyer accepts the seller's counter). Each ALSO gates with `LocalSellOfferOpenForSeller` BEFORE forming the contract, rejecting (`FailedPrecondition`) an accept against a listing already `consumed`/`cancelled` — the key-based analogue of `LocalParentIsOpen`/`ErrOTCParentNotOpen`, so one listing backs exactly one accepted contract (without it, every still-`ongoing` sibling bid could form another contract and over-commit the seller's shares). **Cross-currency premium/strike (2026-06-11, VERSION 4.1.0):** the cross-bank settlement denominates the premium in the *buyer's* bound-account currency. When the seller holds no account in that currency, interbank-service's `PostingExecutor` (seller's bank) FX-converts the seller-CREDIT leg via exchange-service so it lands in the seller's own account currency (`reserveIncomingCredit` → `fxReserveCredit`; requires `EXCHANGE_GRPC_ADDR`) instead of voting `NO_SUCH_ACCOUNT`. The conversion is additive — with no converter wired, the pre-FX `NO_SUCH_ACCOUNT`/`NO_SUCH_ASSET` fail-closed behaviour is preserved. **Settlement-rollback safety:** `InitiateOutboundTxWithPostings` now returns the SI-TX row's real terminal status (`committed`/`committing`/`rolled_back`/`pending`) instead of a blanket `"pending"`; the inbound `PeerOTCGRPCHandler.AcceptNegotiation` fails the accept (reverts the claim, does NOT consume the listing) on a `rolled_back`/`failed` status, so a settlement that aborts no longer destroys the seller's listing with no contract. **Cross-bank contract-created notifications (2026-06-11, VERSION 4.2.0):** an accept now notifies BOTH local client parties with `OTC_CONTRACT_CREATED` (parity with the local accept's dual-party notify) — the **acceptor** via the outbound `acceptRemoteNegotiation` (`OTCNegotiationService.NotifyOTCParticipant`), and the **counterparty** via the inbound `AcceptNegotiation` (which now notifies whichever of buyer/seller is local, not only the seller). Previously the cross-bank **bidder** got no notification when their bid/counter was accepted. Each side is notified exactly once (the acceptor by their bank, the counterparty by theirs). **Cross-bank EXERCISE rollback safety (2026-06-11, VERSION 4.3.0):** `InitiateOptionExercise` claims the contract `active → exercising` then dispatches the exercise SI-TX. A peer NO vote is a valid protocol outcome (not a transport error), so the `err==nil` branch did not catch it and the claim was never reverted — leaving the contract permanently stuck in `exercising` (`"contract status \"exercising\" is not exercisable"`, the user's bug). It now also checks the returned settlement status and reverts `exercising → active` on `rolled_back`/`failed` (mirroring the accept path), returning a clean 409 so the buyer can retry. The exercise STRIKE seller-credit is FX-covered by the same `reserveIncomingCredit`→`fxReserveCredit` path as the premium (verified live: a CHF-strike exercise credited the seller's RSD account the FX-equivalent). The exercise itself is correct cross-bank — a stuck/rolled-back exercise against a **foreign cohort bank** is the counterparty voting NO, not a local fault.
> - `Holding` (existing model) — the `PublicQuantity`/`PublicPrice` columns + the `OTCSafeAvailable()` helper were **REMOVED 2026-06-11** with the in-bank OTC stock marketplace (the `public_quantity`/`public_price` columns are dropped on startup).
>
> **SP-1 (Unified OTC Read, 2026-06-04) entities:**
> - **Remote OTC offers** — peer-bank OTC option listings discovered via the peer `GET /api/v3/cross-bank-protocol/public-stock` catalog (the proprietary `/public-option-offers` discovery endpoint was removed 2026-06-11; `/public-stock` is the sole cross-bank option-discovery surface). **SP-2a (2026-06-05): folded into the unified `OTCOffer` table as remote rows; the standalone `RemoteOTCOffer` / `remote_otc_offers` table + `RemoteOTCOfferRepository` are retired.** A remote offer is an `OTCOffer` with `routing_number=<peer routing>` and `native_id=<peer foreign offer id>` (the `(routing_number, native_id)` UNIQUE is the natural key; local rows have `routing_number=OwnRouting()`, `native_id=NULL`). The autoincrement `OTCOffer.id` is the **stable local surrogate id** surfaced to the frontend on all SP-1 read routes. Remote-only nullable columns on `OTCOffer`: `remote_seller_id` (size 128 — the SI-TX wire seller id `"client-<N>"` | `"bank"`), `last_seen_at` (indexed; last successful peer poll that listed it). *(2026-06-11: the preset-term columns `strike_price`/`premium`/`settlement_date`/`strike_currency`/`premium_currency`/`has_preset_terms` were DROPPED from `OTCOffer` — option offers became termless "optionable inventory" (`(owner, ticker, quantity)`); terms are buyer-negotiated and live on the negotiation chain, and a remote `/public-stock` shell's currency is derived from the bidder's bound account at bid time.)* A remote row satisfies the model's NOT-NULL / CHECK / `ValidateOwner` constraints with `initiator_owner_type="bank"` + nil `initiator_owner_id` (the only owner pair `ValidateOwner` accepts without a concrete local owner; the actual remote seller lives in `remote_seller_id`/`initiator_bank_code`), `last_modified_by_principal_type="system"`/`id=0`. `OTCOfferRepository` gained three remote-scoped methods: `UpsertRemote(o, seenAt)` (ON CONFLICT on the natural key, reopens `status=open`, stamps `last_seen_at`, returns the stable surrogate id), `ReconcileRemoteNotSeen(peerRouting, seenNativeIDs)` (SkipHooks bulk-flip of open peer rows not in the latest poll to `status=cancelled`; never touches local or other-peer rows — guaranteed because `peerRouting != OwnRouting()`), and `GetRemoteByID(id)` (returns the remote row; `gorm.ErrRecordNotFound` for a local id so a local id never resolves through the remote path). The routing-guarded local-only queries (accept/cascade/expiry/exercise) exclude these remote rows via their `routing_number == OwnRouting()` filter.
> - `OTCNegotiationResponse.kind / routing_number / bank_code / me_owner` (SP-1 Task 7) — four new proto fields stamped in the service layer when building the `ListMyNegotiations` response. `kind` = `"local"` | `"remote"`. `me_owner` = true only when the caller is the parent listing's poster/seller; a chain the caller opened as bidder is always false.
> - `UnifiedOptionOffer.local_id / me_owner` (SP-1) — `local_id` (proto field 19) is the stable local surrogate id (= the folded-in remote `OTCOffer.id` for remote rows, SP-2a; numeric `offer_id` for local). `me_owner` (field 20) is true only when the acting caller posted the listing (always false for remote rows).
> - `OptionContractResponse.kind / routing_number / bank_code / me_owner` (SP-1 Task 8) — provenance fields on the unified contract read. `me_owner` = true ONLY when the caller is the contract's **buyer/holder** (DIFFERENT from offers/negotiations where the poster/seller is the owner). For remote: true iff `direction == "CREDIT"` (this bank holds the buyer side).

**InvestmentFund extension** (Celina 4 / closed-end funds) — `investment_funds` table gains:

| Field | Type | Notes |
|---|---|---|
| `FundType` | `varchar(16)` | `open` (default) or `closed` |
| `FundraisingStart` | `*time.Time` | closed-only |
| `FundraisingEnd` | `*time.Time` | closed-only |
| `MaturityDate` | `*time.Time` | closed-only |
| `TargetAmountRSD` | `numeric(20,4)` | closed-only; positive |
| `FundStatus` | `varchar(16)` | open / fundraising / active / matured / liquidated |
| `MaturityGraceEnd` | `*time.Time` | computed = MaturityDate + 7d when transitioning to matured |

Closed-end invariants enforced in `model.InvestmentFund.BeforeSave`. `FundService.Invest` rejects closed funds outside `fundraising` status; `FundService.Redeem` rejects closed funds outside `open` status. `FundLifecycleCron` walks closed funds every 15 min and transitions `fundraising → active → matured → liquidated` per the calendar, firing `FUND_FUNDRAISING_STARTED/CLOSED/MATURED/LIQUIDATED` in-app notifications to the fund manager. Auto-liquidation money movement (sell remaining holdings + pro-rata distribution) is deferred to a follow-up.

**Watchlist** (SP6 — `watchlists` table in stock-service `stock_db`) — a named collection of tracked listings owned by a client or the bank. `{id, owner_type, owner_id, name, created_at, updated_at}`, unique `(owner_type, owner_id, name)`. A user may keep several (e.g. "tech stocks"). The legacy single-list endpoints operate on a lazily-created default **"My Watchlist"**. New routes (`/api/v3/me/watchlists*`) provide named-list CRUD + per-list item add/remove; a list is owner-scoped and the same listing may appear in multiple lists. On startup, `MigrateWatchlistsToNamedLists` (idempotent) drops the legacy `(owner, listing)` unique index and assigns any pre-existing items to their owner's default list. **Uniqueness fix (2026-06-08):** the composite unique index could not enforce `(owner, name)` for **bank/employee-owned lists** because their `owner_id` is `NULL` and SQL treats NULLs as distinct — so bank-owned lists (incl. the lazily-created default) duplicated on every create/`GetOrCreateDefault`. Fixed by (a) `CreateWatchlist` checking existence by `(owner, name)` before insert (idempotent for NULL owners), (b) a partial unique index `idx_watchlist_bank_name (owner_type, name) WHERE owner_id IS NULL` enforcing it at the DB level, and (c) an idempotent startup dedup `DedupeWatchlistsAndEnforceUniqueness` that collapses existing duplicates (keep oldest, merge items, delete rest) before creating the index. Net: one owner cannot hold two same-named lists; different owners may reuse a name.

**WatchlistItem** (Celina 3 / SP6 — `watchlist_items` table in stock-service `stock_db`) — one tracked listing inside a `Watchlist`. Gains `watchlist_id` (FK; unique `(watchlist_id, listing_id)`); retains denormalised `owner_type`/`owner_id` so the daily price-move notification cron scans per-owner unchanged.

| Field | Type | Notes |
|---|---|---|
| `ID` | uint64 PK | |
| `OwnerType` | varchar(16) | `client` or `bank`, part of unique index |
| `OwnerID` | *uint64 nullable | NULL iff `OwnerType=bank`, part of unique index |
| `ListingID` | uint64 | references `listings`, part of unique index |
| `AddedAt` | time.Time | wall-clock insert time |

Unique `(OwnerType, OwnerID, ListingID)` enforces "one tracked entry per owner+listing"; `WatchlistRepository.Add` issues `ON CONFLICT DO NOTHING` so double-adds are idempotent. No version column — append/delete only.

**RecurringOrder** (Celina 3 — `recurring_orders` table in stock-service `stock_db`) — per-owner weekly/monthly Market-order template (`OwnerType`/`OwnerID`, `ListingID`, `Side` buy|sell, `Quantity`, `AccountID`, `Interval` weekly|monthly with `DayOfWeek`/`DayOfMonth`, `StartDate`/`EndDate`, `Status` active|paused|cancelled|finished, `NextRun`). `RecurringOrderCron` ticks hourly and calls `RunDue`, which materialises every due template into a real Market order via `OrderService.CreateOrder` (the full placement saga: reserve funds → persist → approve). The cron is wired through `recurringOrderPlacerAdapter` (stock-service `cmd/`), which maps each tick's `(owner_type, owner_id)` back onto the legacy `(user_id, system_type)` pair CreateOrder consumes (`bank`→system_type=bank/no user id; `client`→system_type=client/client id). A failed tick (insufficient funds, validation) does not abort the loop: it fires a `RECURRING_ORDER_SKIPPED` in-app notification (vs. `RECURRING_ORDER_EXECUTED` on success) and still advances `NextRun` so the template never gets stuck. Past `EndDate` flips `Status=finished`.

### Auth Service (auth_db)

**Account** — Unified login record for employees and clients
```
ID(int64), Email(unique), PasswordHash, Status(pending|active|disabled),
PrincipalType(employee|client), PrincipalID(int64), MFAEnabled(bool), CreatedAt
```

**RefreshToken** — Revocable refresh tokens
```
ID, AccountID, Token(unique), ExpiresAt, Revoked(bool), SystemType(employee|client), CreatedAt
```

**ActivationToken** — One-time account activation
```
ID, AccountID, Token(unique), ExpiresAt, Used(bool), CreatedAt
```

**PasswordResetToken** — One-time password reset
```
ID, AccountID, Token(unique), ExpiresAt, Used(bool), CreatedAt
```

**LoginAttempt** — Failed login tracking
```
ID, Email, IPAddress, Success(bool), CreatedAt
```

**AccountLock** — Brute-force lockout
```
ID, Email, Reason, LockedAt, ExpiresAt, UnlockedAt(nullable)
```

**TOTPSecret** — 2FA secrets
```
ID, UserID(unique), Secret, Enabled(bool), CreatedAt, UpdatedAt
```

**ActiveSession** — Session tracking
```
ID, UserID, UserRole, IPAddress, UserAgent, LastActiveAt, CreatedAt, RevokedAt(nullable)
```

**MobileDevice** — One active device per user for mobile verification
```
ID(uint64), UserID(indexed), SystemType(client|employee), DeviceID(unique,UUID),
DeviceSecret(HMAC-SHA256 key,32 bytes hex), DeviceName, Status(pending|active|deactivated),
ActivatedAt(nullable), DeactivatedAt(nullable), LastSeenAt, Version(int64), CreatedAt, UpdatedAt
```

**MobileActivationCode** — 6-digit codes for mobile device activation
```
ID(uint64), Email(indexed), Code(6-digit), ExpiresAt(15min), Attempts(max 3), Used(bool), CreatedAt
```

### User Service (user_db)

**Employee**
```
ID(int64), FirstName, LastName, DateOfBirth(time.Time), Gender, Email(unique),
Phone, Address, JMBG(unique,13), Username(unique), Position, Department,
Roles(m2m), AdditionalPermissions(m2m), CreatedAt, UpdatedAt
```

**Role**
```
ID(int64), Name(unique), Description, Permissions(m2m), CreatedAt, UpdatedAt
```

**Permission**
```
ID(int64), Code(unique), Description, Category, CreatedAt
```

**EmployeeLimit**
```
ID, EmployeeID(unique), MaxLoanApprovalAmount(decimal), MaxSingleTransaction(decimal),
MaxDailyTransaction(decimal), MaxClientDailyLimit(decimal), MaxClientMonthlyLimit(decimal),
CreatedAt, UpdatedAt
```

**LimitTemplate**
```
ID, Name(unique), Description, MaxLoanApprovalAmount, MaxSingleTransaction,
MaxDailyTransaction, MaxClientDailyLimit, MaxClientMonthlyLimit, CreatedAt, UpdatedAt
```

### Client Service (client_db)

**Client**
```
ID(uint64), FirstName, LastName, DateOfBirth(int64/unix), Gender, Email(unique),
Phone, Address, JMBG(unique,13), CreatedAt, UpdatedAt
```

**ClientLimit**
```
ID, ClientID(unique), DailyLimit(decimal,default:100000), MonthlyLimit(decimal,default:1000000),
TransferLimit(decimal,default:50000), SetByEmployee(int64), CreatedAt, UpdatedAt
```

### Account Service (account_db)

**Account**
```
ID(uint64), AccountNumber(unique,18), AccountName, OwnerID(uint64,indexed), OwnerName,
Balance(numeric18,4), AvailableBalance(numeric18,4), ReservedBalance(numeric18,4,default:0),
EmployeeID(uint64), ExpiresAt, CurrencyCode(3,indexed), Status(active|inactive,indexed),
AccountKind(current|foreign), AccountType(standard|premium|student|youth|pension),
AccountCategory, MaintenanceFee(numeric18,4), DailyLimit(numeric18,4,default:1000000),
MonthlyLimit(numeric18,4,default:10000000), DailySpending(numeric18,4),
MonthlySpending(numeric18,4), CompanyID(nullable), IsBankAccount(bool,indexed),
Version(int64), CreatedAt, UpdatedAt, DeletedAt(soft delete)
```
`ReservedBalance` is the running total of amounts held by active securities-order reservations. Maintained atomically by the reservation RPCs (`ReserveFunds`, `ReleaseReservation`, `PartialSettleReservation`). `AvailableBalance = Balance - ReservedBalance` (logical invariant; the stored `AvailableBalance` column mirrors this after every reservation mutation).

**AccountReservation** — Idempotency + state ledger for an order's hold on an account. Immutable except for `Status`/`Version`.
```
ID(uint64), AccountID(uint64,indexed), OrderID(uint64), OrderKind(size:32,default:'stock_order'),
Amount(numeric18,4), CurrencyCode(3), Status(active|released|settled,indexed),
CreatedAt, UpdatedAt, Version(int64), UNIQUE(OrderID, OrderKind)
```
`(OrderID, OrderKind)` is the composite idempotency key — retrying `ReserveFunds` with the same pair is a safe no-op. `OrderKind` is the caller-namespace discriminator (see RPC section above for current values). `Amount` is immutable after insert; only `Status` transitions.

**AccountReservationSettlement** — Append-only; one row per partial settle. The `OrderTransactionID` comes from stock-service's `OrderTransaction.ID` and is the cross-service idempotency key.
```
ID(uint64), ReservationID(uint64,indexed), OrderTransactionID(uint64,unique),
Amount(numeric18,4), CreatedAt
```

**Company**
```
ID(uint64), CompanyName, RegistrationNumber(unique,8), TaxNumber(unique,9),
ActivityCode(5), Address, OwnerID(uint64), Version(int64), CreatedAt, UpdatedAt
```

**Currency**
```
ID(uint64), Name, Code(unique,3), Symbol, Country, Description, Active(bool)
Seeded: RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD
```

**LedgerEntry**
```
ID(uint64), AccountNumber(indexed), EntryType(debit|credit), Amount(numeric18,4),
BalanceBefore(numeric18,4), BalanceAfter(numeric18,4), Description,
ReferenceID(indexed), ReferenceType(payment|transfer|fee|interest), CreatedAt(indexed)
```

**BankOperation** — idempotency log for bank sentinel debit/credit operations used by loan disbursement saga
```
ID(uint64), Reference(string,indexed), Direction(debit|credit), Currency(3),
Amount(numeric18,4), AccountNumber, NewBalance(numeric18,4), Reason, CreatedAt
UniqueIndex: (reference, direction)
```

### Card Service (card_db)

**ClientReplica** (SP-1 service-decoupling, 2026-06-08) — NON-AUTHORITATIVE local read-model of a client's profile (id, email, first/last name, jmbg, version), fed by `client.created`/`client.updated` events with a version-guarded upsert and a synchronous `GetClient` fallback+backfill on miss. Now maintained in **card-service** (card-status notification email), **credit-service** (installment-failure email; group `credit-service-client-replica`), **account-service** (account-created email; group `account-service-client-replica`), and **stock-service** (local OTC-seller existence validation; group `stock-service-client-replica`) — each replacing a synchronous `GetClient` read. (interbank-service's `GetClient` is intentionally left as-is: it serves the frozen cross-bank-protocol `/user` endpoint.)
```
ID(uint64, PK, no autoincrement — == client-service Client.ID),
Email, FirstName, LastName, JMBG(size:13),
Version(int64) — source Client.Version; ordering guard (stale events are ignored),
UpdatedAt
```

**Card**
```
ID(uint64), CardNumber(unique,masked), CardNumberFull, CVV,
CardType(debit|credit), CardName, CardBrand(visa|mastercard|dinacard|amex),
AccountNumber(indexed), OwnerID(uint64), OwnerType(client|authorized_person),
CardLimit(numeric18,4,default:1000000), Status(active|blocked|deactivated),
Version(int64), ExpiresAt, IsVirtual(bool),
UsageType(single_use|multi_use|unlimited), MaxUses(int), UsesRemaining(int),
PinHash, PinAttempts(int,0-3), CreatedAt, UpdatedAt
```

**CardBlock**
```
ID(uint64), CardID(indexed), Reason, BlockedAt, ExpiresAt(nullable), Active(bool), CreatedAt
```

**CardRequest**
```
ID(uint64), ClientID(indexed), AccountNumber, CardBrand, CardType(debit|credit),
CardName, Status(pending|approved|rejected), Reason, ApprovedBy(uint64), CreatedAt, UpdatedAt
```

**AuthorizedPerson**
```
ID(uint64), FirstName, LastName, DateOfBirth(int64/unix), Gender, Email, Phone,
Address, AccountID(uint64), CreatedAt, UpdatedAt
```

### Transaction Service (transaction_db)

**Payment**
```
ID(uint64), IdempotencyKey(unique,36), FromAccountNumber(indexed), ToAccountNumber(indexed),
InitialAmount(numeric18,4), FinalAmount(numeric18,4), Commission(numeric18,4),
CurrencyCode(3,default:RSD), RecipientName, PaymentCode(10), ReferenceNumber(50),
PaymentPurpose, Status(pending|completed|failed,indexed), FailureReason,
Version(int64), Timestamp(indexed), CompletedAt(nullable)
```

**Transfer**
```
ID(uint64), IdempotencyKey(unique,36), FromAccountNumber(indexed), ToAccountNumber(indexed),
InitialAmount(numeric18,4), FinalAmount(numeric18,4), ExchangeRate(numeric18,8,default:1),
Commission(numeric18,4), FromCurrency(3,default:RSD), ToCurrency(3,default:RSD),
Status(pending|completed|failed), FailureReason, Version(int64), Timestamp, CompletedAt(nullable)
```

> **Proto additions (ownership lockdown):** `LoanResponse`, `PaymentResponse`, and `TransferResponse` proto messages each gained a new `client_id` (uint64) field, used by the gateway to verify resource ownership for `/api/me/*` routes.

**TransferFee**
```
ID(uint64), Name, FeeType(percentage|fixed), FeeValue(numeric18,4),
MinAmount(numeric18,4,default:0), MaxFee(numeric18,4,default:0=uncapped),
TransactionType(payment|transfer|all), CurrencyCode(3,nullable=all),
Active(bool), CreatedAt, UpdatedAt
```

**PaymentRecipient**
```
ID(uint64), ClientID(indexed), RecipientName, AccountNumber, CreatedAt, UpdatedAt
```

**VerificationCode**
```
ID(uint64), ClientID(indexed), TransactionID, TransactionType(payment|transfer),
Code(6), ExpiresAt, Attempts(int), Used(bool)
```

**PeerBank** (Phase 2 SI-TX) — Runtime-editable peer-bank registry
```
ID(uint64), BankCode(unique), RoutingNumber(unique,3), BaseURL,
APITokenBcrypt, APITokenPlaintext, HmacInboundKey, HmacOutboundKey,
Active(bool,indexed), CreatedAt, UpdatedAt
```
`APITokenPlaintext` is only readable via the internal `ResolvePeerByAPIToken` RPC (never exposed via REST). Admins manage via `/api/v3/peer-banks` (gated by `peer_banks.manage.any`).

**PeerIdempotenceRecord** (Phase 2 SI-TX) — Receiver-side replay cache for inbound TXs
```
ID(uint64), PeerBankCode(indexed), LocallyGeneratedKey, MessageType(NEW_TX|COMMIT_TX|ROLLBACK_TX),
ResponsePayloadJSON, CreatedAt
Composite-unique: (peer_bank_code, locally_generated_key)
```
Cached `response_payload_json` is returned verbatim on retries so receivers vote consistently.

**OutboundPeerTx** (Phase 3 SI-TX) — Sender-side state for outbound SI-TX TXs
```
ID(uint64), IdempotenceKey(unique,36), PeerBankCode(indexed), TxKind(transfer|otc-accept|otc-exercise),
PostingsJSON, Status(pending|committing|committed|rolled_back|failed,indexed),
AttemptCount(int,default:0), LastAttemptAt(nullable,indexed), LastError(text),
CreatedAt, UpdatedAt
```
`OutboundReplayCron` (30s tick, 4-attempt cap) resumes rows in `pending` whose `last_attempt_at` is older than 60s.

### Credit Service (credit_db)

**LoanRequest**
```
ID(uint64), ClientID(indexed), LoanType(cash|housing|auto|refinancing|student),
InterestType(fixed|variable), Amount(numeric18,4), CurrencyCode(3), Purpose,
MonthlySalary(numeric18,4), EmploymentStatus, EmploymentPeriod(months),
RepaymentPeriod(months), Phone, AccountNumber, Status(pending|approved|rejected),
Version(int64), CreatedAt, UpdatedAt
```

**Loan**
```
ID(uint64), LoanNumber(unique), LoanType, AccountNumber(indexed), Amount(numeric18,4),
RepaymentPeriod(months), NominalInterestRate(numeric8,4), EffectiveInterestRate(numeric8,4),
ContractDate, MaturityDate, NextInstallmentAmount(numeric18,4), NextInstallmentDate,
RemainingDebt(numeric18,4), CurrencyCode(3), Status(approved|defaulted),
InterestType(fixed|variable), BaseRate(numeric10,4), BankMargin(numeric10,4),
CurrentRate(numeric10,4), ClientID(indexed), Version(int64), CreatedAt, UpdatedAt
```

**Installment**
```
ID(uint64), LoanID(indexed), SequenceNumber, Amount(numeric18,4),
InterestRate(numeric8,4), CurrencyCode(3), ExpectedDate, ActualDate(nullable),
Status(unpaid|paid|overdue), Version(int64)
```

**InterestRateTier**
```
ID(uint64), AmountFrom(decimal20,4), AmountTo(decimal20,4,0=unlimited),
FixedRate(decimal10,4), VariableBase(decimal10,4), Active(bool), CreatedAt, UpdatedAt
```

**BankMargin**
```
ID(uint64), LoanType(unique), Margin(decimal10,4), Active(bool), CreatedAt, UpdatedAt
```

**EmployeeLimitReplica** (SP-2 service-decoupling, 2026-06-08) — NON-AUTHORITATIVE local read-model of an employee's approval limits, maintained by credit-service to avoid synchronous `GetEmployeeLimits` RPCs on the loan-approval gate. Fed by `user.employee-limits-updated` events (consumer group `credit-service-employee-limit-replica`). Upsert is version-guarded: a message whose `Version` is ≤ the stored row's `Version` is silently dropped to prevent stale overwrites. On a cache miss the approval gate falls back to a synchronous `GetEmployeeLimits` gRPC call and backfills the replica at `Version=0` (a later versioned event will overwrite it). Only `MaxLoanApprovalAmount` is actively enforced; the other four limit fields are stored for future gate expansion.
```
EmployeeID(uint64, PK — no autoincrement; == user-service EmployeeLimit.EmployeeID),
MaxLoanApprovalAmount(numeric18,4), MaxSingleTransaction(numeric18,4),
MaxDailyTransaction(numeric18,4), MaxClientDailyLimit(numeric18,4),
MaxClientMonthlyLimit(numeric18,4),
Version(int64) — source EmployeeLimit.Version; ordering guard (stale events are dropped),
UpdatedAt
```

### Exchange Service (exchange_db)

**ExchangeRate**
```
ID(uint64), FromCurrency(3), ToCurrency(3), BuyRate(numeric18,8), SellRate(numeric18,8),
Version(int64), UpdatedAt
Unique constraint: (from_currency, to_currency)
Both directions stored: EUR/RSD and RSD/EUR
```

### Verification Service (verification_db)

**VerificationChallenge** — Mobile/email verification challenges for transactions
```
ID(uint64), UserID(indexed), SourceService(transaction|payment|transfer),
SourceID(uint64), Method(code_pull|email; qr_scan and number_match planned but not yet active),
Code(6-digit), ChallengeData(JSONB), Status(pending|verified|expired|failed),
Attempts(max 3), ExpiresAt(5min), VerifiedAt(nullable), DeviceID(nullable),
Version(int64), CreatedAt, UpdatedAt
Note: Code "111111" is accepted as a universal bypass code for development convenience.
```

### Notification Service (notification_db)

**MobileInboxItem** — Pending verification items for mobile delivery
```
ID(uint64), UserID(indexed), DeviceID(indexed), ChallengeID(uint64),
Method(code_pull; qr_scan and number_match planned), DisplayData(JSONB),
Status(pending|delivered|expired), ExpiresAt, DeliveredAt(nullable), CreatedAt
```

**NotificationTemplate** — Admin override of a registry template's subject/body
```
ID(uint64), Type(string), Channel(email|push), Subject(string), Body(string),
CreatedAt, UpdatedAt
```
Unique on `(type, channel)`. The absence of a row ⇒ the code-defined registry default is used for that template type/channel. The set of template types and the `{{variables}}` each supports is code-defined (the registry); only the subject/body text is customizable via this table.

### Stock Service (stock_db)

> **Phase 2 complete (2026-04-22):** The securities fill path is now bank-safe. Funds are reserved at placement (`AccountReservation` + `Account.ReservedBalance`), holdings are reserved for sells (`HoldingReservation` + `Holding.ReservedQuantity`), every fill runs through a saga log with idempotent settlement, and Kafka events publish only after the fill saga commits. See `docs/superpowers/plans/2026-04-22-bank-safe-settlement.md`.

**StockExchange** — A stock exchange (e.g. NYSE, NASDAQ)
```
ID(uint64), Name, Acronym(unique), MicCode(unique), Country, Currency, TimeZone,
OpenTime, CloseTime, CreatedAt, UpdatedAt
```

**Stock** — An individual stock security
```
ID(uint64), Ticker(unique), Name, ExchangeID(→StockExchange), Price(numeric 18,8),
High, Low, Change, Volume, OutstandingShares, DividendYield, LastRefresh,
Version(int64), CreatedAt, UpdatedAt
```

**Option** — A stock option contract (call or put). Contract size = 100 shares.
```
ID(uint64), Ticker(unique), Name, StockID(→Stock, indexed), OptionType(call|put),
StrikePrice(numeric 18,4), ImpliedVolatility(numeric 10,6), Premium(numeric 18,4),
OpenInterest(int64), SettlementDate(indexed), ListingID(*uint64, nullable, indexed),
Version(int64), CreatedAt, UpdatedAt
```
`ListingID` is nullable. When set, the option has a corresponding `Listing` row with
`security_type='option'` on the same exchange as the underlying stock, allowing orders to
reference the option via the unified listings table.

**Listing** — Bridge between a security and the exchange it trades on. Orders reference ListingID.
```
ID(uint64), SecurityID(indexed), SecurityType(stock|futures|forex|option, indexed),
ExchangeID(→StockExchange, indexed), Price(numeric 18,8), High, Low, Change,
Volume(int64), LastRefresh, Version(int64), CreatedAt, UpdatedAt
```
`SecurityType` values: `stock`, `futures`, `forex`, `option` (option added for v2 option orders).

**ForexPair** — A currency pair traded on an exchange
```
ID(uint64), BaseCurrency, QuoteCurrency, ExchangeID, Price, High, Low, Change,
Volume, LastRefresh, Version(int64), CreatedAt, UpdatedAt
```

**FuturesContract** — A futures contract
```
ID(uint64), Ticker(unique), Name, ExchangeID, Price(numeric 18,8), High, Low, Change,
Volume, SettlementDate, ContractSize(int64), MaintenanceMarginRate(numeric 10,6),
LastRefresh, Version(int64), CreatedAt, UpdatedAt
```

**Holding** — A current position in a security, owned by a client or by the bank.
```
ID(uint64), OwnerType(client|bank,indexed), OwnerID(*uint64,indexed),
SecurityType(stock|futures|forex|option), SecurityID(indexed), Quantity(int64),
AveragePrice(numeric 18,8), ReservedQuantity(int64,default:0),
AccountID(uint64), Version(int64), CreatedAt, UpdatedAt
```
`OwnerType`+`OwnerID` replaces the pre-Task-4 (UserID, SystemType) pair (plan 2026-04-27-owner-type-schema.md). Bank-owned holdings have `OwnerType="bank"` with `OwnerID IS NULL`; client-owned holdings have `OwnerType="client"` with a non-null `OwnerID`. The `BeforeSave` hook calls `model.ValidateOwner` to enforce the invariant. Unique index `idx_holding_per_owner_security` keys on `(owner_type, COALESCE(owner_id, 0), security_type, security_id)` so each (owner, security) pair rolls up to a single row.

`ReservedQuantity` is the running total of units locked by active sell-side `HoldingReservation` rows. `AvailableQuantity = Quantity - ReservedQuantity`. Sell orders are rejected at placement if `AvailableQuantity` is insufficient; filled sells decrement both `Quantity` and `ReservedQuantity` atomically.

**HoldingReservation** — Quantity-based mirror of `AccountReservation`. Locks shares on a holding for the duration of a sell order. Immutable except for `Status`/`Version`. `FundHoldingID` (nullable) re-targets the reservation at a `fund_holdings` row for on-behalf-of-fund sells: when set, `HoldingID` is `0` (sentinel) and the reserve/settle/release lifecycle mutates `fund_holdings` instead of `holdings` (the fund-sell mirror of the buy fill that credits `fund_holdings`). The "exactly one of OrderID/OTCContractID/PeerOptionContractID/CrossbankTxID" CHECK is unchanged — a fund sell still carries `OrderID` as its key; `FundHoldingID` is only the holding-table discriminator.
```
ID(uint64), HoldingID(uint64,indexed), FundHoldingID(*uint64,indexed,nullable),
OrderID(uint64,unique), Quantity(int64),
Status(active|released|settled,indexed), CreatedAt, UpdatedAt, Version(int64)
```

**HoldingReservationSettlement** — Append-only; one row per partial sell fill.
```
ID(uint64), HoldingReservationID(uint64,indexed), OrderTransactionID(uint64,unique),
Quantity(int64), CreatedAt
```

**Order** — A buy/sell order placed against a listing on behalf of a client or the bank.
```
ID(uint64), OwnerType(client|bank,indexed), OwnerID(*uint64,indexed),
ListingID(→Listing), Direction(buy|sell), OrderType(market|limit|stop|stop_limit),
Quantity(int64), FilledQuantity(int64), Price(nullable), StopPrice(nullable),
Status(pending|executed|cancelled|rejected), AccountID, ActingEmployeeID(*uint64,indexed),
ReservationAmount(numeric18,4,nullable), ReservationCurrency(3,nullable),
ReservationAccountID(uint64,nullable), BaseAccountID(uint64,nullable,forex-only),
PlacementRate(numeric18,8,nullable), SagaID(string,36,indexed),
Version(int64), CreatedAt, UpdatedAt
```
`OwnerType`+`OwnerID` describe the owner of the resulting holding (plan 2026-04-27-owner-type-schema.md). Bank-owned orders have `OwnerType="bank"`, `OwnerID IS NULL`. Client-owned orders have `OwnerType="client"`, `OwnerID = client_id`.
`ActingEmployeeID` — nullable audit column set whenever the *principal* who placed the order is an employee. The actuary-limit gate keys on this field, so `OwnerIsBankIfEmployee` (an employee placing through `/api/me/orders`) correctly resolves Owner=bank but still records the employee for limit enforcement.
`ReservationAmount`/`ReservationCurrency`/`ReservationAccountID` — populated by the placement saga's `reserve_funds` step; read on cancellation and recovery. Nullable for historical orders pre-dating Phase 2.
`BaseAccountID` — forex orders only; the user's base-currency account credited on fill. Must differ from `AccountID` (the quote-currency account where funds are reserved).
`PlacementRate` — audit snapshot of the FX rate used at placement time for cross-currency securities orders. Nullable for same-currency orders.
`SagaID` — UUID linking the order to its placement-saga + fill-saga rows in `saga_logs`.

**OrderTransaction** — One executed portion of an order (an order may have multiple partial fills).
```
ID(uint64), OrderID(uint64,indexed), Quantity(int64), PricePerUnit(numeric18,4),
TotalPrice(numeric18,4), NativeAmount(numeric18,4,nullable), NativeCurrency(3,nullable),
ConvertedAmount(numeric18,4,nullable), AccountCurrency(3,nullable),
FxRate(numeric18,8,nullable), ExecutedAt
```
Currency-conversion audit fields (`NativeAmount`, `NativeCurrency`, `ConvertedAmount`, `AccountCurrency`, `FxRate`) are populated by the fill saga's `convert_amount` step. For same-currency fills `NativeAmount` mirrors `TotalPrice` and `FxRate`/`ConvertedAmount` may be empty. `OrderTransaction.ID` is the cross-service idempotency key for `PartialSettleReservation` and the holding decrement step.

**SagaLog** (stock-service) — Mirrors `transaction-service/internal/model/saga_log.go`. One row per saga step. Stock-service runs two saga types: the placement saga (scoped to `order_id`) and the fill saga (one per partial fill, scoped to `order_id` + `order_transaction_id`).
```
ID(uint64), SagaID(uuid,36,indexed), OrderID(uint64,indexed),
OrderTransactionID(uint64,nullable,indexed), StepNumber(int), StepName(size:64),
Status(pending|completed|failed|compensating|compensated,indexed),
IsCompensation(bool,default:false), CompensationOf(uint64,nullable),
Amount(numeric18,4,nullable), CurrencyCode(3,nullable), Payload(JSONB),
ErrorMessage(text), RetryCount(int,default:0),
CreatedAt, UpdatedAt, Version(int64)
```
Placement saga steps: `validate_listing` → ... → `reserve_funds` → `persist_order`. Fill saga steps: `record_transaction` → `convert_amount` → `settle_reservation` → `update_holding` → `credit_commission` → `publish_kafka`. Compensating rows set `IsCompensation=true` and `CompensationOf` pointing at the forward step.

**Stuck-compensation escalation (recovery correctness):** when a step's `Backward` cannot complete (e.g. a seller-debit compensation fails because the seller already spent a credited amount), `saga.Compensate` now returns `saga.ErrCompensationStuck` (previously it returned `nil`, so the recovery reconciler force-marked the row `compensated` and permanently hid money in limbo). The recovery recoverers propagate it, so the stuck row keeps failing, its `retry_count` climbs, and it is eventually dead-lettered (`stock.saga-dead-letter`) for human review. To keep that escalation bounded, `Recorder.RecordCompensation` is **idempotent across recovery ticks** — it reuses the existing compensation row for a `(saga_id, step_name)` (via `SagaLogRepository.FindLatestCompensationRow`) instead of inserting a new one each time the loop re-drives the rollback, so a persistently-stuck compensation stays on ONE row rather than spawning unbounded `saga_logs` rows. Safe because every `Backward` in stock-service is idempotent.

**SystemSetting** — Global key-value configuration (key = primary key)
```
Key(string, PK, size:64), Value(string)
```
`system_settings.active_stock_source` — persists the currently active stock data source
(`external`, `generated`, or `simulator`) across service restarts.

**PeerOtcNegotiation** — **RETIRED (SP-2a, 2026-06-05): folded into the unified `OTCNegotiation` table as REMOTE rows.** A remote negotiation is an `OTCNegotiation` with `routing_number=<peer routing>` (the peer bank that owns the canonical side of the chain) and `native_id=<peer-assigned negotiation id>` (the `ForeignBankId.id` returned by the peer's inbound POST handler); the `(routing_number, native_id)` UNIQUE index is the natural key. Local rows have `routing_number=OwnRouting()`, `native_id=NULL`. The autoincrement `OTCNegotiation.id` is the stable local surrogate id surfaced to the frontend. Remote-only nullable columns added to `OTCNegotiation`:

| Column | Purpose |
|---|---|
| `remote_offer_json` (`text`) | Serialised `contract/sitx.OtcOffer` — authoritative terms for a remote chain; `Quantity`/`StrikePrice`/`Premium`/`SettlementDate` are best-effort parses kept to satisfy NOT-NULL schema. |
| `remote_buyer_routing` (`int64`) | SI-TX wire buyer routing number. |
| `remote_buyer_id` (`size:128`) | SI-TX wire buyer participant id (`"client-<N>"` \| `"bank"`). |
| `remote_seller_routing` (`int64`) | SI-TX wire seller routing number. |
| `remote_seller_id` (`size:128`) | SI-TX wire seller participant id. |
| `remote_parent_routing` (`int64`, indexed with `remote_parent_native_id`) | Phase-10 cascade-cancel grouping key — the peer routing of the parent listing. |
| `remote_parent_native_id` (`size:128`, indexed with `remote_parent_routing`) | Phase-10 cascade-cancel grouping key — the peer-assigned native id of the parent listing's lot. |

The shared `Status` column carries the peer status vocabulary on remote rows (`"ongoing"` \| `"accepted"` \| `"cancelled"`). `OTCNegotiationRepository` gained remote-scoped methods: `UpsertRemoteNeg`, `GetRemoteNegByRoutingAndNative`, `GetRemoteNegByNative`, `UpdateRemoteNegOffer`, `UpdateRemoteNegStatus`, `CompareAndSetRemoteNegStatus`, `ListRemoteNegBySellerAndParent`, `ListRemoteNegByClient`, and `ListRemoteNegOngoing`. The standalone `PeerOtcNegotiation` model (`stock-service/internal/model/peer_otc_negotiation.go`) and `PeerOtcNegotiationRepository` (`stock-service/internal/repository/peer_otc_negotiation_repository.go`) are deleted; the inbound peer webhooks, the negotiation reconciler, and the SP-1 read-merge all use `OTCNegotiationRepository`'s remote methods. No data migration (fresh start).

**CronPauseState** (C5 — 2026-05-28) — Persists pause/resume admin decisions for each cron, shared by every service's cron registry.
```
Name(string,PK,size:128), IsPaused(bool,not null), PausedBy(int64), PausedAt(time.Time,nullable)
TableName: cron_pause_states
```
One row per named cron per service. Queried by the cron runner on every tick to decide whether to skip execution. Written by `AdminCron.PauseCron` / `AdminCron.ResumeCron` gRPC calls. The model lives in `contract/cronreg/model.go` and is auto-migrated in every service that uses `cronreg.NewRegistry`.

**AdminAuditLog** (C11 — 2026-05-28) — Audit trail for admin cron control actions, stored in `notification-service`'s `notification_db`.
```
ID(uint64,PK,autoIncrement),
Action(string,size:32,not null,indexed),      -- "trigger"|"pause"|"resume"
Service(string,size:64,not null,indexed),     -- e.g. "stock-service"
CronName(string,size:100,not null,indexed),
EmployeeID(int64,not null,indexed),
Reason(string,size:512),
Timestamp(time.Time,not null,indexed)
TableName: admin_audit_logs
```

**BusinessAuditLog** (SP2 — 2026-06-04) — Audit trail for high-value business actions (limit changes, usedLimit resets, order approve/reject, permission changes, manual tax collection), stored in `notification-service`'s `notification_db`. Fed by the `admin.business-action` Kafka topic published by the api-gateway (actor known from JWT).
```
ID(uint64,PK,autoIncrement),
Action(string,size:32,not null,indexed),      -- limit.set|limit.used_reset|order.approve|order.decline|permissions.set|tax.collect
ActorID(int64,not null,indexed),              -- employee who performed the action
TargetType(string,size:32,not null,indexed),  -- employee|order|role|tax
TargetID(string,size:64,not null,indexed),
Detail(string,size:512),                      -- human-readable new value / outcome
Timestamp(time.Time,not null,indexed)
TableName: business_audit_logs
```
Written by the notification-service `admin_audit_consumer` consuming `admin.cron-action` Kafka events published by the api-gateway after each Trigger/Pause/Resume action.

**ClientLimitPolicy** (SP-5 — 2026-06-08) — Per-client spending-cap read-model stored in `account_db`. Fed by `client.limits-updated` events; drives `UpdateAccountLimits` propagation to all non-bank accounts the client owns. Non-authoritative replica (source of truth is `client_db.client_limits`); kept monotonically up-to-date via Version comparison on every upsert.
```
ClientID(uint64,PK),              -- the client whose limits were set
DailyLimit(decimal(18,4)),        -- mirrors client_limits.DailyLimit
MonthlyLimit(decimal(18,4)),      -- mirrors client_limits.MonthlyLimit
Version(int64,not null),          -- monotonic; upsert is a no-op when incoming Version <= stored Version
UpdatedAt(time.Time,not null)
TableName: client_limit_policies
```
Written by `account-service/internal/consumer.ClientLimitConsumer` (consumer group `account-service-client-limit`). After a successful upsert the consumer calls `AccountService.ApplyClientLimitPolicy`, which sets the `daily_limit` and `monthly_limit` columns on every `Account` row where `owner_id = ClientID AND is_bank_account = false`.

---

## 19. Complete Kafka Topic Reference

> **New feature topics:** Investment-fund topics are catalogued in [§24](#24-investment-funds-celina-4). Intra-bank OTC topics (`otc.offer-created/-countered/-rejected/-expired`, `otc.contract-created/-exercised/-expired/-failed`) live in [§26](#26-intra-bank-otc-options-celina-4--spec-2). Cross-bank topics (`otc.crossbank-saga-started/-committed/-rolled-back/-stuck-rollback`, `otc.contract-{exercised,expired}-crossbank`, `otc.contract-expiry-stuck`, `otc.local-offer-changed`) live in [§27](#27-cross-bank-otc-options-celina-5--spec-4--foundation).

### All Topics

| Topic | Producer | Consumer | Message Type |
|---|---|---|---|
| `notification.send-email` | All services | notification-service | SendEmailMessage |
| `notification.email-sent` | notification-service | (logging) | EmailSentMessage |
| `notification.send-push` | (future) | notification-service | (future) |
| `notification.push-sent` | notification-service | (logging) | (future) |
| `auth.account-status-changed` | auth-service | (consumers) | AuthAccountStatusChangedMessage |
| `auth.session-created` | auth-service | (audit/consumers) | `AuthSessionCreatedMessage` — payload carries `principal_type`/`principal_id` (renamed from `system_type`/`user_id` by Task 9 of plan 2026-04-27-owner-type-schema.md) plus session metadata (ip, user-agent, device type) |
| `auth.session-revoked` | auth-service | (audit/consumers) | `AuthSessionRevokedMessage` — `session_id`, `user_id`, `reason` |
| `auth.dead-letter` | auth-service | (monitoring) | (failed events) |
| `user.employee-created` | user-service | notification-service | EmployeeCreatedMessage |
| `user.employee-updated` | user-service | (consumers) | (generic) |
| `user.employee-limits-updated` | user-service | (consumers) | EmployeeLimitsUpdatedMessage |
| `user.limit-template-created` | user-service | (consumers) | LimitTemplateMessage |
| `user.limit-template-updated` | user-service | (consumers) | LimitTemplateMessage |
| `user.limit-template-deleted` | user-service | (consumers) | LimitTemplateMessage |
| `client.limits-updated` | client-service | account-service (group `account-service-client-limit`) | ClientLimitsUpdatedMessage — enriched (SP-5): carries DailyLimit, MonthlyLimit, TransferLimit as decimal strings + monotonic Version; Version increments on every upsert so account-service can apply idempotently. Consumer upserts ClientLimitPolicy then calls ApplyClientLimitPolicy on all client-owned accounts. |
| `user.role-permissions-changed` | user-service | auth-service | RolePermissionsChangedMessage |
| `client.created` | client-service | notification-service, card-service | ClientCreatedMessage |
| `client.updated` | client-service | card-service | ClientCreatedMessage (full snapshot) |
| `account.created` | account-service | notification-service | AccountCreatedMessage |
| `account.status-changed` | account-service | (consumers) | (generic) |
| `account.name-updated` | account-service | (consumers) | AccountNameUpdatedMessage |
| `account.limits-updated` | account-service | (consumers) | AccountLimitsUpdatedMessage |
| `account.maintenance-fee-charged` | account-service | (consumers) | MaintenanceFeeChargedMessage |
| `account.spending-reset` | account-service | (consumers) | SpendingResetMessage |
| `card.created` | card-service | notification-service | CardCreatedMessage |
| `card.status-changed` | card-service | notification-service | CardStatusChangedMessage |
| `card.temporary-blocked` | card-service | (consumers) | CardTemporaryBlockedMessage |
| `card.virtual-card-created` | card-service | notification-service | VirtualCardCreatedMessage |
| `card.request-created` | card-service | (consumers) | CardRequestCreatedMessage |
| `card.request-approved` | card-service | (consumers) | CardRequestApprovedMessage |
| `card.request-rejected` | card-service | (consumers) | CardRequestRejectedMessage |
| `transaction.payment-created` | transaction-service | notification-service | PaymentCreatedMessage |
| `transaction.payment-completed` | transaction-service | notification-service | PaymentCompletedMessage |
| `transaction.payment-failed` | transaction-service | (consumers) | PaymentFailedMessage |
| `transaction.transfer-created` | transaction-service | (consumers) | (generic) |
| `transaction.transfer-completed` | transaction-service | (consumers) | TransferCompletedMessage |
| `transaction.transfer-failed` | transaction-service | (consumers) | TransferFailedMessage |
| `credit.loan-requested` | credit-service | (consumers) | LoanStatusMessage |
| `credit.loan-approved` | credit-service | notification-service | LoanStatusMessage |
| `credit.loan-rejected` | credit-service | notification-service | LoanStatusMessage |
| `credit.loan-disbursed` | credit-service | (consumers) | LoanDisbursedMessage |
| `credit.installment-collected` | credit-service | (consumers) | InstallmentResultMessage |
| `credit.installment-failed` | credit-service | (consumers) | InstallmentResultMessage |
| `credit.variable-rate-adjusted` | credit-service | (consumers) | VariableRateAdjustedMessage |
| `credit.late-penalty-applied` | credit-service | (consumers) | LatePenaltyAppliedMessage |
| `exchange.rates-updated` | exchange-service | (consumers) | ExchangeRatesUpdatedMessage |
| `verification.challenge-created` | verification-service | notification-service | VerificationChallengeCreatedMessage |
| `verification.challenge-verified` | verification-service | transaction-service | VerificationChallengeVerifiedMessage |
| `verification.challenge-failed` | verification-service | transaction-service | VerificationChallengeFailedMessage |
| `notification.general` | account/card/credit/auth/transaction/stock-service | notification-service | GeneralNotificationMessage |
| `stock.order-created` | stock-service | (consumers) | OrderCreatedMessage |
| `stock.order-approved` | stock-service | (consumers) | OrderApprovedMessage |
| `stock.order-declined` | stock-service | (consumers) | OrderDeclinedMessage |
| `stock.order-filled` | stock-service | (consumers) | OrderFilledMessage (payload below) |
| `stock.order-cancelled` | stock-service | (consumers) | OrderCancelledMessage |
| `transaction.saga-dead-letter` | transaction-service | (monitoring/alerting) | Failed saga events that exceeded all retries (cross-bank transfers, compensation failures). |
| `credit.saga-dead-letter` | credit-service | (monitoring/alerting) | Failed loan saga events that exceeded all retries (disbursement, installment failures). |
| `stock.saga-dead-letter` | stock-service | (monitoring/alerting) | Failed stock/OTC saga events that exceeded all retries (OTC exercises, recurring-order failures). |
| `admin.cron-action` | api-gateway | notification-service | `AdminCronActionMessage` — published after each Trigger/Pause/Resume admin cron action; consumed by notification-service to persist audit log rows (C6/C10/C11 — 2026-05-28) |
| `admin.business-action` | api-gateway | notification-service | `BusinessAuditActionMessage` — published (best-effort, actor from JWT) after a limit change, usedLimit reset, order approve/reject, permission change, or manual tax collection; consumed by notification-service to persist `business_audit_logs` rows (SP2 — 2026-06-04) |

### General Notification Types

Published to `notification.general` by various services. notification-service consumes and stores as persistent user notifications (no email, no expiry).

| Type | Source | Trigger |
|---|---|---|
| `ACCOUNT_OPENED` | account-service | Account created |
| `ACCOUNT_STATUS_CHANGED` | account-service | Account status updated (active/inactive) |
| `ACCOUNT_NAME_UPDATED` | account-service | Account renamed |
| `ACCOUNT_LIMITS_UPDATED` | account-service | Account limits (daily/monthly/transfer) changed |
| `MAINTENANCE_FEE_CHARGED` | account-service | Monthly maintenance fee charged (cron) |
| `card_issued` | card-service | Card created |
| `card_blocked` | card-service | Card blocked |
| `PAYMENT_SENT` | transaction-service | Payment completed (sender side) |
| `PAYMENT_RECEIVED` | transaction-service | Payment completed (receiver side) |
| `PAYMENT_FAILED` | transaction-service | Payment failed (sender side) |
| `TRANSFER_SENT` | transaction-service | Transfer completed (sender side) |
| `TRANSFER_RECEIVED` | transaction-service | Transfer completed (receiver side) |
| `TRANSFER_FAILED` | transaction-service | Transfer failed (sender side) |
| `LOAN_REQUEST_SUBMITTED` | credit-service | Loan request submitted by client |
| `LOAN_REQUEST_APPROVED` | credit-service | Loan request approved |
| `LOAN_REQUEST_REJECTED` | credit-service | Loan request rejected |
| `LOAN_DISBURSED` | credit-service | Loan disbursed to borrower account |
| `INSTALLMENT_COLLECTED` | credit-service | Installment successfully collected (cron) |
| `INSTALLMENT_FAILED` | credit-service | Installment collection failed (cron) |
| `password_changed` | auth-service | Password reset completed |

**stock-service in-app notifications (Plan B1):** stock-service emits `GeneralNotificationMessage` intents on `notification.general` for every significant securities/OTC action — order placed/approved/declined/cancelled, order partial-fill and full-fill, OTC offer received/countered/rejected/expired, and OTC contract created/exercised/expired. These are **client-owned only** (bank-owned orders and bank-owned OTC parties emit nothing), **best-effort** (published after the action commits; publish failures are swallowed and never block the action), and rendered by notification-service via the `push`-channel template registry.

**transaction-service in-app notifications (Plan B2):** transaction-service emits `GeneralNotificationMessage` intents on `notification.general` in the **`Data` form** for: `PAYMENT_SENT` and `PAYMENT_RECEIVED` (per side, on payment completion), `PAYMENT_FAILED` (sender, on payment failure), `TRANSFER_SENT` and `TRANSFER_RECEIVED` (per side, on transfer completion), and `TRANSFER_FAILED` (sender, on any failure branch). Sender/receiver are resolved via account-service `GetAccountByNumber`; bank-owned accounts (owner id `0` or `1_000_000_000`) are skipped. Best-effort, published after the status persist. `RefType`/`RefID`: `payment`/payment.ID or `transfer`/transfer.ID.

**credit-service in-app notifications (Plan B3):** credit-service emits `GeneralNotificationMessage` intents on `notification.general` in the **`Data` form** for: `LOAN_REQUEST_SUBMITTED`, `LOAN_REQUEST_APPROVED`, `LOAN_REQUEST_REJECTED`, `LOAN_DISBURSED` (from the gRPC handler), `INSTALLMENT_COLLECTED`, `INSTALLMENT_FAILED` (from the daily installment-collection cron). Recipient is always the loan's borrower (`Loan.ClientID` / `LoanRequest.ClientID`); no bank-side skip. Best-effort, after the action commits.

**account-service in-app notifications (Plan B4):** account-service now emits `GeneralNotificationMessage` intents on `notification.general` in the **`Data` form** for: `ACCOUNT_OPENED` (on create), `ACCOUNT_STATUS_CHANGED` (on status update), `ACCOUNT_NAME_UPDATED` (on rename), `ACCOUNT_LIMITS_UPDATED` (on limit change), `MAINTENANCE_FEE_CHARGED` (per monthly cron charge). Recipient is the account owner (`account.OwnerID`); bank-owned accounts (`is_bank_account == true` or owner id `1_000_000_000`) are skipped. **Plan B4 also closed three pre-existing publish-site gaps** in the same change: the `account.name-updated`, `account.limits-updated`, and `account.maintenance-charged` domain Kafka events are now published (the producer methods existed but were never called by the handlers / cron). Best-effort, after the action commits.

**SP5 notification coverage expansion (2026-06-04):**
- **D1 — client limit change:** `client-service.ClientLimitService.SetClientLimits` now emits a `LIMIT_CHANGED` in-app notification (and a best-effort `LIMIT_CHANGED` email via the client's address) to the affected client with the new daily/monthly/transfer limits. (client-service's producer gained `PublishGeneralNotification`; `notification.general` added to its `EnsureTopics`.) New `LIMIT_CHANGED` push + email templates.
- **E — OTC contract expiring soon:** the OTC expiry cron gained an expiring-soon pass (`OptionContractRepository.ListExpiringOn`) that warns both client parties `OTC_CONTRACT_EXPIRING_SOON` when a contract settles exactly `OTC_EXPIRY_WARNING_DAYS` (default 3) out. New `OTC_CONTRACT_EXPIRING_SOON` push template. Intra-bank contracts only.
- **D2 (card block) and D3 (loan created/approved)** were already covered by existing card-service / credit-service notifications — no change.
- **H — order auto-cancel-on-settlement-expiry: DEFERRED.** Stock orders have no `settlement_date` and there is no order-expiry mechanism to notify on; building one is a feature beyond notification scope. OTC offer/contract expiry already notify (`OTC_OFFER_EXPIRED`/`OTC_CONTRACT_EXPIRED`).

**SP-2b service-decoupling: client↔user cycle fully removed (2026-06-08):** client-service's `SetClientLimits` no longer calls `GetEmployeeLimits` synchronously to authorize a client limit against the employee's `MaxClientDailyLimit`/`MaxClientMonthlyLimit`. It now reads a local `EmployeeLimitReplica` (consumer group `client-service-employee-limit-replica`, fed by the enriched `user.employee-limits-updated`) with a gRPC fallback+backfill on miss — same version-guarded, eventual+fallback pattern as the credit-service slice. Combined with SP-4 (user-service no longer writes client limits), the bidirectional `client ↔ user` coupling is now fully event-driven/replica-backed.

**SP-1 service-decoupling: enriched `ClientCreatedMessage` + card-service `ClientReplica` (2026-06-08):**
`ClientCreatedMessage` (published on both `client.created` and `client.updated`) was enriched with two new fields: `jmbg` (string) and `version` (int64, source `Client.Version`), so it now carries the full client snapshot. card-service consumes both topics via consumer group `card-service-client-replica` and maintains the `ClientReplica` read-model (§18 Card Service). Upsert is version-guarded: a message with a lower version than the stored row is silently dropped to prevent stale overwrites. The card-status notification path resolves the owner email from the replica first; on a miss it falls back to a synchronous `GetClient` gRPC call and backfills the replica. This is the first slice of the service-decoupling program (SP-1, client-profile replica, card-service slice).

**SP-2 service-decoupling: `EmployeeLimitsUpdatedMessage` enrichment + credit-service `EmployeeLimitReplica` (2026-06-08):**
`EmployeeLimitsUpdatedMessage` (published on `user.employee-limits-updated` by user-service whenever `SetEmployeeLimits` or `ApplyLimitTemplate` is called) was enriched to carry the FULL limit snapshot (`MaxLoanApprovalAmount`, `MaxSingleTransaction`, `MaxDailyTransaction`, `MaxClientDailyLimit`, `MaxClientMonthlyLimit` as `StringFixed(4)` decimal strings) plus a monotonic `Version` field (source `EmployeeLimit.Version`, incremented on every upsert). `EmployeeLimit.Version` now increments on every `SetEmployeeLimits` / `ApplyLimitTemplate` call in user-service. credit-service consumes the topic via consumer group `credit-service-employee-limit-replica` and maintains the `EmployeeLimitReplica` read-model (§18 Credit Service). The loan-approval gate in `LoanRequestService.ApproveLoanRequest` reads `MaxLoanApprovalAmount` from the replica first; on a miss it falls back to a synchronous `GetEmployeeLimits` gRPC call and backfills the replica at `Version=0` (a later versioned event will overwrite it). An approval whose amount exceeds the employee's limit returns `ErrAmountExceedsApprovalLimit` (gRPC `FailedPrecondition` → HTTP 409 `business_rule_violation`). This is the second slice of the service-decoupling program (SP-2, employee-limit replica, credit-service slice).

### Email Types (SendEmailMessage.EmailType)

When publishing to `notification.send-email`, use one of these EmailType values:

```
ACTIVATION, PASSWORD_RESET, CONFIRMATION, ACCOUNT_CREATED,
CARD_VERIFICATION, CARD_STATUS_CHANGED, LOAN_APPROVED, LOAN_REJECTED,
INSTALLMENT_FAILED, TRANSACTION_VERIFICATION, PAYMENT_CONFIRMATION
```

### Key Message Struct Patterns

All message structs are defined in `contract/kafka/messages.go`. Common pattern:

```go
type SendEmailMessage struct {
    To        string            `json:"to"`
    EmailType string            `json:"email_type"`
    Data      map[string]string `json:"data"`
}

type PaymentCompletedMessage struct {
    PaymentID         uint64 `json:"payment_id"`
    FromAccountNumber string `json:"from_account_number"`
    ToAccountNumber   string `json:"to_account_number"`
    Amount            string `json:"amount"`
    Currency          string `json:"currency"`
    Status            string `json:"status"`
}
```

**LoanDisbursedMessage** — published to `credit.loan-disbursed` after successful loan disbursement saga:
```go
type LoanDisbursedMessage struct {
    LoanID       uint64 `json:"loan_id"`
    LoanNumber   string `json:"loan_number"`
    BorrowerID   uint64 `json:"borrower_id"`
    AccountNumber string `json:"account_number"`
    Amount       string `json:"amount"`
    CurrencyCode string `json:"currency_code"`
    DisbursedAt  string `json:"disbursed_at"` // RFC3339
}
```

**RolePermissionsChangedMessage** — published to `user.role-permissions-changed` after a role's permissions are updated; auth-service consumes and invalidates sessions for all affected employees:
```go
type RolePermissionsChangedMessage struct {
    RoleID              int64   `json:"role_id"`
    RoleName            string  `json:"role_name"`
    AffectedEmployeeIDs []int64 `json:"affected_employee_ids"`
    ChangedAt           int64   `json:"changed_at"`        // unix seconds
    Source              string  `json:"source"`            // "update_role_permissions" | "create_role"
}
```

**`stock.order-filled` payload** — published synchronously by stock-service after the fill saga's final step commits. A failed fill does NOT emit this event (stuck saga rows are reconciled on recovery). The payload is a JSON object (not a typed struct in `contract/kafka/messages.go`):

| Field | Type | Notes |
|---|---|---|
| `saga_id` | string (UUID) | Links the fill to its saga_logs rows |
| `order_id` | uint64 | |
| `order_txn_id` | uint64 | `OrderTransaction.ID`; idempotency key for downstream consumers |
| `owner_type` | string | `client` or `bank` (canonical owner — added by plan 2026-04-27-owner-type-schema.md, Task 9) |
| `owner_id` | uint64\|null | Client ID, or `null` for bank-owned orders |
| `user_id` | uint64 | Legacy compatibility shim — equals `owner_id` for client owners, `0` for bank-owned. Will be retired after one or two deploy cycles. |
| `direction` | string | `buy` or `sell` |
| `security_type` | string | `stock`, `futures`, `forex`, `option` |
| `ticker` | string | |
| `filled_qty` | int64 | Quantity filled in this partial |
| `remaining_qty` | int64 | Quantity left on the order |
| `price` | string (decimal) | Execution price per unit |
| `total_price` | string (decimal) | `filled_qty × price × contract_size` in native currency |
| `native_amount` | string (decimal) | May be empty for same-currency fills |
| `native_currency` | string (3) | May be empty for same-currency fills |
| `converted_amount` | string (decimal) | Populated for cross-currency fills |
| `account_currency` | string (3) | May be empty for same-currency fills |
| `fx_rate` | string (decimal) | May be empty for same-currency fills |
| `is_done` | bool | True when the order's remaining portions reach zero |
| `kafka_key` | string | Format `order-fill-{order_txn_id}` |
| `timestamp` | int64 | Unix seconds at publish time |

Note: Phase 2 intentionally did not introduce a `stock.order-failed` topic. Failed fills stay as stuck saga rows and are retried by the saga recovery reconciler on startup rather than emitting a failure event.

When adding a new message type: define the struct in `contract/kafka/messages.go`, add a topic constant string, and follow the existing naming pattern (`{Entity}{Action}Message`).

### Cross-Bank Saga Persistence

Cross-bank sagas (accept, exercise, expire) are orchestrated by `contract/shared/saga.Saga` with `stock-service/internal/saga.CrossBankRecorder` writing to the `inter_bank_saga_logs` table (keyed on `tx_id, phase, role`).

Step names are typed via `contract/shared/saga.StepKind`. The recovery switch in `stock-service/internal/service/saga_recovery.go` panics on any unknown `StepKind`, forcing every new step to be added explicitly.

Lifecycle Kafka events (`crossbank.{accept|exercise|expire}-{started|committed|rolled-back}`) are emitted via the per-saga `LifecyclePublisher` adapter wired through `Saga.WithPublisher(...)`.

Saga IDs are minted by `Saga.NewSaga(recorder)`. Sub-sagas derive a deterministic ≤36-char child ID from `sha256(parent_id + ":" + child_kind)` via `Saga.NewSubSaga(kind)`.

---

## 20. Known Enum Values

Keep these synchronized across API Gateway validation, protobuf definitions, and service-layer logic.

| Field | Allowed Values |
|---|---|
| `account_kind` | `current`, `foreign` |
| `account_status` | `active`, `inactive` |
| `account_type` | `standard`, `premium`, `student`, `youth`, `pension` |
| `card_type` | `debit`, `credit` |
| `card_brand` | `visa`, `mastercard`, `dinacard`, `amex` |
| `card_status` | `active`, `blocked`, `deactivated` |
| `owner_type` | `client`, `authorized_person` |
| `usage_type` | `single_use`, `multi_use`, `unlimited` |
| `fee_type` | `percentage`, `fixed` |
| `transaction_type` (fees) | `payment`, `transfer`, `all` |
| `loan_type` | `cash`, `housing`, `auto`, `refinancing`, `student` |
| `interest_type` | `fixed`, `variable` |
| `loan_status` | `pending`, `approved`, `active`, `disbursement_failed`, `defaulted` |
| `loan_request_status` | `pending`, `approved`, `rejected` |
| `installment_status` | `unpaid`, `paid`, `overdue` |
| `payment_status` | `pending`, `completed`, `failed` |
| `transfer_status` | `pending`, `completed`, `failed` |
| `auth_account_status` | `pending`, `active`, `disabled` |
| `principal_type` | `employee`, `client` |
| `system_type` (JWT) | `employee`, `client` |
| `entry_type` (ledger) | `debit`, `credit` |
| `reference_type` (ledger) | `payment`, `transfer`, `fee`, `interest` |
| `card_request_status` | `pending`, `approved`, `rejected` |
| `currency_code` | `RSD`, `EUR`, `CHF`, `USD`, `GBP`, `JPY`, `CAD`, `AUD` |
| `listing_security_type` | `stock`, `futures`, `forex`, `option` |
| `stock_source` | `external`, `generated`, `simulator` |
| `reservation_status` | `active`, `released`, `settled` |
| `saga_step_status` | `pending`, `completed`, `failed`, `compensating`, `compensated` |
| `verification_method` | `code_pull` (default), `email` — active; `qr_scan`, `number_match` — planned but not yet active |
| `verification_status` | `pending`, `verified`, `expired`, `failed` |
| `mobile_device_status` | `pending`, `active`, `deactivated` |
| `on_behalf_of_type` (funds invest/redeem) | `self`, `bank` |
| `on_behalf_of_type` (orders) | `self`, `bank`, `fund` (Celina 4) |
| `fund_contribution_direction` | `invest`, `redeem` |
| `fund_contribution_status` | `pending`, `completed`, `failed` |
| `otc_offer_status` | `PENDING`, `COUNTERED`, `ACCEPTED`, `REJECTED`, `EXPIRED`, `FAILED` |
| `otc_offer_direction` | `sell_initiated`, `buy_initiated` |
| `otc_offer_action` (revision history) | `CREATE`, `COUNTER`, `ACCEPT`, `REJECT` |
| `option_contract_status` | `ACTIVE`, `EXERCISED`, `EXPIRED`, `FAILED` |
| `inter_bank_saga_kind` | `accept`, `exercise`, `expire` |
| `inter_bank_saga_role` | `initiator`, `responder` |
| `inter_bank_saga_phase` | `reserve_buyer_funds`, `reserve_seller_shares`, `transfer_funds`, `transfer_ownership`, `finalize`, `expire_notify`, `expire_apply` |
| `inter_bank_saga_status` | `pending`, `completed`, `failed`, `compensating`, `compensated` |
| `mobile_inbox_status` | `pending`, `delivered`, `expired` |
| `device_type` (JWT) | `mobile` |

---

## 21. Sentinel Values & Business Rules

### Sentinel Values

| Value | Meaning |
|---|---|
| `account.owner_id = 1_000_000_000` | Bank-owned account (account-service only — see note below) |
| `account.owner_id = 2_000_000_000` | State-owned entity (account-service) |

**Stock-service** no longer uses the bank-owner sentinel. Per plan 2026-04-27-owner-type-schema.md (Tasks 4-11) every stock-service model that previously carried `(user_id=1_000_000_000, system_type="employee")` now uses `(OwnerType="bank", OwnerID IS NULL)` with a `BeforeSave` `model.ValidateOwner` hook enforcing the invariant. The api-gateway middleware `ResolveIdentity` (§6.X) computes the resolved owner per route and stock-service repositories filter on `(owner_type, owner_id)` directly; the legacy columns + the `BankSentinelUserID` constant have been removed. See §6.X (Identity Model) for the full principal-vs-owner separation.

### Key Business Rules

**Cross-bank protocol canonical prefix (2026-05-29):**
- Cross-bank wire-protocol routes are served EXCLUSIVELY at the canonical prefix `/api/v3/cross-bank-protocol/<route>`. Legacy paths (`/api/v3/interbank`, `/api/v3/public-stock`, `/api/v3/negotiations/*`, `/api/v3/user/*`) were removed on 2026-05-29 and return 404.
- Cohort banks MUST register this bank's `base_url` ending in `/api/v3/cross-bank-protocol` in their `peer_banks` table to interoperate. Any bank still registered with the old prefix will receive 404 and must update immediately.
- All routes use `PeerAuth` middleware (hybrid `X-Api-Key` or HMAC bundle). Protocol semantics are unchanged — same SI-TX envelopes, same idempotence keys, same status codes.
- To migrate our outbound calls to a peer's canonical prefix: update the peer's row in our `peer_banks` table via `PUT /api/v3/peer-banks/:id` with the new `base_url`.

**Stock & futures price oscillation (generated source — dev/demo default):**
- The `generated` source (`stock-service/internal/source/generated_source.go`) drives stock and futures prices on a deterministic 4-minute cycle keyed to wallclock UTC minutes. Each phase lasts exactly one minute.
- Multipliers per phase: `[0.90, 1.00, 1.10, 1.00]` applied to immutable seed prices. Phase index = `floor(unixSeconds / 60) mod 4`.
- Cycle repeats indefinitely with zero drift — base seeds are never mutated.
- Forex pairs are NOT oscillated; cross-currency conversion via `exchange-service.Convert` depends on stable rates for fill pricing and fee math.
- The security price refresh interval default is `1` minute (env `SECURITY_SYNC_INTERVAL_MINUTES`, default in `stock-service/internal/config/config.go`) so the DB visibly steps to the new phase each minute. The `external` and `simulator` sources are unaffected by the oscillation.

**Fund RSD Account Outflow Restriction (E0 — Celina-4, 2026-05-28):**
- Money in an investment fund's RSD account may ONLY leave via three permitted paths: (a) a buy order placed on behalf of the fund (`on_behalf_of_fund_id` in a stock order or OTC accept), (b) a dividend payout to fund investors (E4 — not yet implemented), or (c) a redemption by an investor of their position (`FundService.Redeem`).
- **FORBIDDEN:** An employee CANNOT transfer money from a fund's RSD account to any arbitrary other account via the generic transfer/payment routes (`POST /api/v3/me/payments`, `POST /api/v3/me/transfers`, or any employee transfer/payment route).
- Enforcement: fund RSD accounts are tagged `account_category = "investment_fund"` in account-service at creation time. `transaction-service`'s `PaymentService.CreatePayment` and `TransferService.CreateTransfer` reject any source account with `account_category == "investment_fund"` with `ErrFundAccountRestricted` (codes.PermissionDenied).
- **`GET /api/v3/investment-funds/:id` enrichment (E1 — 2026-05-28):** Returns `investor_count`, `total_contributed_rsd`, `liquid_rsd_balance`, `total_holdings_value_rsd`, `total_value_rsd`, `total_dividends_paid_rsd` (real sum from `fund_dividend_payments` as of E4), `profit_rsd`, `profit_pct`, and `holdings[].current_value_rsd`.
- **OTC buy on behalf of fund (E2 — 2026-05-28):** `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` and `POST /api/v3/otc/contracts/:id/exercise` now accept `on_behalf_of_fund_id`. When set, debit comes from fund's RSD account; resulting holding lands in `fund_holdings`. Manager-only (`acting_employee_id == fund.manager_employee_id`).
- **Sell from a fund's portfolio (2026-06-12, VERSION 4.6.0):** `POST /api/v3/me/orders` and `POST /api/v3/orders` with `on_behalf_of_fund_id` + `direction=sell` now correctly draw the shares from the fund's `fund_holdings` position — the mirror of the buy fill that credits `fund_holdings`. Previously the sell-side reserve/settle ran against the bank-sentinel `holdings` table (the order's resolved owner), so a fund sell either failed with "insufficient holding" or drew down the bank's own book; both are fixed. Implementation: the placement `reserve_holding` step calls `HoldingReservationService.ReserveFund` (locks `fund_holdings.ReservedQuantity` under a row lock, recording a `HoldingReservation` with `FundHoldingID` set); the fill's `PartialSettle` and the cancel/compensation `Release` branch on that `FundHoldingID` to mutate `fund_holdings`. Proceeds credit the fund's RSD account. No `CapitalGain` row is written for a fund sell (funds are CG tax-deferred, and the `CapitalGain` owner CHECK admits only `client`/`bank`). This also repairs `FundService.LiquidateAndAwait` (fund-redemption liquidation), which places fund sell orders through the same path.
- **Fund-order account auto-resolve (2026-06-12, VERSION 4.6.0):** For any order with `on_behalf_of_fund_id`, `account_id` is OPTIONAL. When omitted (0) stock-service resolves it to the fund's RSD account; when supplied it must equal that account (else `InvalidArgument`). The api-gateway skips the caller-account ownership check for fund orders (the account is the fund's, bound and re-validated in stock-service against the manager). The employee-only gate still fires before the gRPC call, so a client passing `on_behalf_of_fund_id` is rejected `403 forbidden`.
- **Dividend pass-through (E4 — 2026-05-28):** Dividends from securities held directly by clients go to the client's RSD account (15% tax withheld, net = 85% of gross). Dividends from securities held by the bank go to the bank's RSD account (no tax). Dividends from securities held by an investment fund flow into the fund's RSD account (no tax at payout time); a `FundDividendPayment` snapshot records the per-investor share at the moment of payout so that each investor's `dividends_received_rsd` in the portfolio response can be computed correctly. Tax on fund-dividend pass-through is realized at the investor's redemption time, not at payout time.
- **Portfolio dividend visibility (E3 — 2026-05-28):** `GET /api/v3/me/portfolio` (and all portfolio routes) return two new fields on each `PortfolioPosition`: `dividends_received_rsd` (the caller's pro-rata share of dividends paid, based on the per-investor snapshot for fund positions or direct `dividend_payouts.net_amount_rsd` for security positions) and `fund_status` (the fund's lifecycle status for `investment_fund` type positions).

**Client Limits Ownership (SP-4, 2026-06-08):**
- Client limits are written ONLY by client-service (`ClientLimitService.SetClientLimits`).
- Client-type limit blueprints are orchestrated by the api-gateway: `BlueprintHandler.ApplyBlueprint` reads the blueprint values from user-service, then calls `ClientLimitService.SetClientLimits` on client-service directly. This path never goes through user-service's `BlueprintService.ApplyBlueprint`.
- user-service's `BlueprintService` rejects client-type apply calls with `ErrClientBlueprintNotApplicable` (gRPC `FailedPrecondition`) to guard against incorrect direct calls. user-service holds no gRPC connection to client-service.

**Client Limit → Account Cap Propagation (SP-5, DONE 2026-06-08):**
- Client limits (DailyLimit, MonthlyLimit) are now ENFORCED at the account level, not merely stored.
- When `PUT /api/v3/clients/{id}/limits` is called, client-service persists the limit and publishes `client.limits-updated` to Kafka with the full post-write snapshot (DailyLimit, MonthlyLimit, TransferLimit as decimal strings) and a monotonically incrementing `Version` (`ClientLimit.Version` increments on every upsert).
- account-service's `ClientLimitConsumer` (group `account-service-client-limit`) receives the event, upserts a `ClientLimitPolicy` replica row (skips stale events via Version comparison), and calls `AccountService.ApplyClientLimitPolicy` which writes the new DailyLimit/MonthlyLimit to every non-bank account owned by the client.
- account-service is the authoritative enforcement point for spending limits (via `DebitWithLock`). The per-account `daily_limit`/`monthly_limit` columns set by this propagation are what the ledger repository enforces on every debit.
- TransferLimit is carried in the Kafka message but is NOT stored in `ClientLimitPolicy` (transfer limits are enforced by client-service at request time, not by account-service). Only DailyLimit and MonthlyLimit propagate to account caps.

**Accounts:**
- `current` accounts → RSD only
- `foreign` accounts → EUR, CHF, USD, GBP, JPY, CAD, AUD
- Bank must always have >= 1 RSD + >= 1 foreign currency account
- Account expires 5 years after creation
- Maintenance fees: premium=500, student=0, youth=0, pension=100, default=220 RSD

**Cards:**
- Physical cards: max 2 per account (owner_type=client), max 1 per authorized person per account
- Virtual cards: single_use (1 use), multi_use (N uses), unlimited
- PIN: 4 digits, bcrypt-hashed, locked after 3 failed attempts
- Temporary blocks: auto-unblocked by background goroutine every 1 min

**Transactions:**
- Transfers: between same client's accounts only, no fee for same-currency
- Payments: between different clients' accounts, requires verification code
- Fee rules are cumulative (multiple matching rules stack)
- Fee lookup failure rejects the transaction
- Collected fees → bank's RSD account
- Idempotency keys prevent duplicate transactions
- Default seeded fees: (1) 0.1% for all transactions >= 1000 RSD, (2) 5% commission for all transactions >= 5000 RSD

**Loans:**
- Repayment periods vary by type (cash: 12-84mo, housing: 60-360mo, etc.)
- Employee approval limited by `MaxLoanApprovalAmount`
- Interest calculated from `InterestRateTier` + `BankMargin`
- Variable-rate loans recalculate when tiers change
- Loan currency must match account currency
- Bank must have sufficient liquidity in the loan currency for approval to succeed; insufficient liquidity returns 409 `business_rule_violation`.
- Loan approval is atomic: bank sentinel is debited and borrower is credited, or neither — the saga compensates on partial failure.
- On saga compensation failure the loan is marked `disbursement_failed` and the `BankOperation` idempotency log prevents double-debits on retry.

**Auth:**
- 5 failed login attempts → 30-min lockout
- A successful password reset unlocks a brute-force-locked account and clears the failed-attempt counter (`AccountService.ResetPassword` calls `LoginAttemptRepository.UnlockAccount`).
- Password: 8-32 chars, 2+ digits, 1 uppercase, 1 lowercase
- JMBG: exactly 13 digits
- Role permission updates revoke active sessions for affected employees within seconds via the `user.role-permissions-changed` Kafka event; auth-service rejects access tokens whose `iat` predates the per-user revocation epoch (`user_revoked_at:<id>` Redis key, TTL = `JWT_ACCESS_EXPIRY`) and revokes their refresh tokens to force a full re-login.

**Exchange Rates:**
- Synced every 6 hours from open.er-api.com
- Cross-currency conversion: two-leg via RSD (X→RSD→Y)
- Commission: 0.5% per leg (configurable)
- Spread: 0.3% buy/sell (configurable)
- Sync failure: keeps stale/seed rates, logs warning

**Decimal Precision:**
- All financial values: `shopspring/decimal` in Go, `numeric(18,4)` in PostgreSQL
- Exchange rates: `numeric(18,8)` for higher precision

**Graceful Degradation:**
- Redis unavailable → log warning, continue without cache
- Kafka publish failure → log warning, don't fail main operation
- Exchange rate sync failure → log warning, keep seed rates

**Ownership & On-Behalf Trading:**
- All `/api/me/*` routes derive resource ownership from the JWT through the `ResolveIdentity` middleware (§6.X). The middleware applies a per-route policy:
  - `OwnerIsPrincipal` — owner == authenticated principal (used by `/me/profile`, `/me/cards`, etc.).
  - `OwnerIsBankIfEmployee` — employee principal → owner=bank; client principal → owner=self (used by `/me/orders`, `/me/holdings`, `/me/funds`, `/me/otc/*`).
- Any resource ID from URL, query, or body is verified against the resolved owner before any read or write. Mismatches return `404 not_found` to avoid leaking existence.
- The acting employee's id is recorded on every side-effect row (`acting_employee_id`) regardless of resolved owner; stock-service's actuary-limit gate keys on this column so an employee placing a /me/order is correctly rate-limited even though the order is bank-owned.
- Employee on-behalf trading routes (`POST /api/v3/orders`, `POST /api/v3/otc/offers/:id/buy-on-behalf`) use `OwnerFromURLParam("client_id")` and verify that the specified `account_id` belongs to the specified `client_id` before forwarding to stock-service. Mismatch returns 403.

**Stock Data Sources:**
- Three sources supported: `external` (live API), `generated` (deterministic synthetic data), `simulator` (simulated market prices backed by the Market Simulator Service).
- A source switch is **destructive**: it wipes all stock-service tables AND all associated trading state (orders, holdings, capital gains, tax collections, order transactions). User history is lost across switches. Intended for demo/dev environments, not production.
- On startup, stock-service reads `system_settings.active_stock_source` and restores that source automatically. Default source when no setting exists is `external`.
- When the active source is `simulator`, a background goroutine refreshes prices every 3 seconds. Switching away from `simulator` cancels this goroutine via `context.Context` cancellation.
- The `SourceAdminService.SwitchSource` RPC rejects unknown source names with `codes.InvalidArgument`.

**Stock-service synthetic history backfill.** During `SeedAll` (initial seed and `SwitchSource`), `stock-service` writes 5 years (1825 days) of deterministic synthetic OHLC rows per listing to `listing_daily_price_infos`. Random walk is seeded by `listing.ID`, anchors the newest row at the listing's current price, and is idempotent on reseed via `INSERT … ON CONFLICT (listing_id, date) DO UPDATE`. Implemented in `stock-service/internal/service/listing_history_backfill.go`.

**No saga can leave the system stuck.** Every cross-bank (SI-TX), OTC, loan disbursement, and recurring-order operation has automatic compensation. Compensations are retried up to 10 times by per-service recovery workers, then escalated to a service-scoped dead-letter Kafka topic (`transaction.saga-dead-letter`, `credit.saga-dead-letter`, `stock.saga-dead-letter`). No path requires admin manual reconciliation under normal failure modes. Cross-bank TX stuck-state is additionally resolved by the Celina-5 CHECK_STATUS mechanism: `PeerTxReconciler` (sender side) polls peers every 10 minutes via `GET /api/v3/cross-bank-protocol/interbank/:txID/status`; peers that have committed locally will report `committed` so the sender can close its row without a re-send loop.

**Securities & Trading (Phase 2 bank-safe settlement):**
- Buy orders for securities reserve funds at placement (converted to the account currency via exchange-service for cross-currency listings). Reservations are released on cancellation; released partially when an order completes under the reserved amount due to market slippage.
- Sell orders for securities reserve holdings at placement; sells are rejected if `AvailableQuantity = Quantity - ReservedQuantity` is insufficient. Filling decrements both `Quantity` and `ReservedQuantity` atomically via the holding reservation ledger.
- Forex orders are **buy-only**, reserve on the quote-currency account, and credit the base-currency account on fill. No holdings are created; exchange-service is NOT called — stock-service computes the debit/credit using the forex listing's own price.
- Forex `listing.Price`/`High`/`Low` for a pair represent the price of 1 base-currency unit denominated in quote-currency. Changing this convention breaks forex settlement math.
- Securities order commissions are charged to the bank's commission account as a separate saga step per fill. Commission failures are logged and retried by the saga recovery reconciler; the underlying trade remains valid.
- Kafka fill events (`stock.order-filled`) are published synchronously, only after the fill saga's final step commits. Failed fills do NOT emit events.
- Fill saga steps are idempotent: account settlements are keyed on `order_transaction_id` (`AccountReservationSettlement.order_transaction_id` unique); holding decrements are keyed on `order_transaction_id` (`HoldingReservationSettlement.order_transaction_id` unique). Recovery retries after crashes are safe no-ops if the step already committed on the target service.
- `GetReservation` returns the authoritative list of `settled_transaction_ids` so stock-service's saga recovery can distinguish "step already committed remotely" from "step never ran."
- Only whole-remaining-order cancellation is supported; partial-cancel-during-fill is out of scope.
- **Order matching honours the user's price condition.** The execution engine in `stock-service/internal/service/order_execution.go` enforces, per portion: market orders fill at the live quote (ask for buy, bid for sell); limit orders fill only when the live quote satisfies `LimitValue` (`ask ≤ LimitValue` for buy, `bid ≥ LimitValue` for sell); stop orders fill only after the trigger price has been crossed (`High ≥ StopValue` for buy, `Low ≤ StopValue` for sell); stop-limit orders require BOTH the stop trigger AND the limit condition each tick. The fill price for limit / stop-limit orders is the live quote (never clamped to `LimitValue`) — the live quote is what a real counterparty would accept. A defensive pre-fill check (`execPriceAllowed`) rejects any computed execution price that would violate `LimitValue`, even if the trigger check above missed it (e.g. a quote moved during the per-portion wait).
- **Agent order approval is a disjunction (Celina 3).** An employee-placed BUY order requires supervisor approval if ANY of: the acting agent's `need_approval` flag is set, OR the order would push the agent's `used_limit + amount` over their configured daily `limit` (which also covers the "limit already fully used" case). Implemented by `decideNeedsApproval` in `stock-service/internal/service/order_service.go` and wired into the placement saga's finalize step. NB: agents default to `need_approval=true` (`user-service ... actuary_service.go`: `!isSupervisor`), so a fresh agent's orders are approval-gated until a supervisor grants `skip-approval`; a configured limit is enforced regardless of the flag (closes the prior conjunction money-hole where a non-flagged agent could auto-approve an over-limit order). Limit=0 means "no limit configured" → the over-limit branch never fires, so a non-flagged agent with no limit auto-approves.
- **Forex pairs and options market data are actuary-only.** Clients may browse/trade only stocks and futures (Celina 3 portal matrix). The gateway guards `/securities/forex*` and `/securities/options*` with `middleware.DenyClientToken()` → `403 forbidden` for client principals; `/securities/candles` stays open (clients need it for stocks/futures).
- **Capital gains are recorded for every realised sale**, including OTC option exercise. On exercise, `stock-service/internal/service/otc_exercise_saga.go` snapshots the seller's `Holding.AveragePrice` before the consume step and writes a `CapitalGain` row post-pivot with `BuyPricePerUnit = AveragePrice`, `SellPricePerUnit = StrikePrice`, `TotalGain = (Strike - AveragePrice) × Quantity`, `OTC = true`, `Currency = StrikeCurrency`, `AccountID = SellerAccountID`. Best-effort — a CG write failure logs `WARN` but does NOT reverse the strike/shares movement (shares and money have already moved). Mirrors the existing CG writes in `PortfolioService.recordCapitalGain` (sell-order fill) and `OTCService.BuyOffer` (direct OTC stock sale). Wired via `OTCOfferService.WithCapitalGain(repo)` in `stock-service/cmd/main.go`; tests that don't wire it see degraded (no-CG) behaviour, identical to pre-fix.
- **Every stock realisation path records a CapitalGain row.** In addition to the order-fill sell (`PortfolioService.recordCapitalGain`), the direct OTC stock sale (`OTCService.BuyOffer`), and the local OTC option exercise (`otc_exercise_saga.go`), realisation rows are now also written by:
  - `OTCStockService.FillBuyOffer` — when a seller fills a buyer's standing buy-offer; uses the holding snapshot captured in step 2 of the fill saga as cost basis and `offer.PricePerUnit` as sell price.
  - `PeerOTCGRPCHandler.recordOptionExercise` (DEBIT branch) — when a cross-bank OTC option exercise lands on the seller's bank; cost basis is snapshotted under the row lock inside `HoldingReservationService.ConsumeForPeerOptionContract` (exposed on `PartialSettleHoldingResult.AveragePriceBefore`), sell price is `contract.StrikePrice`. Wired via `PeerOTCGRPCHandler.WithCapitalGain(repo)` in `cmd/main.go`.
- **Option premium tax — resolution-month model (2026-06-04, `docs/superpowers/specs/2026-06-04-options-premium-tax-design.md`).** The OTC option premium and exercise are taxed as follows:
  - **Seller (writer) — at accept.** `OTCOfferService.Accept` writes one `SecurityType="option"`, `OTC=true` row: `+premium` for the seller, `Currency=PremiumCurrency`. The premium income is taxable when received (15%).
  - **Buyer — at resolution, NOT at accept.** The buyer's premium is no longer booked at accept (the `record_buyer_premium_cost` accept-saga step is now a no-op, kept in place so the saga shape — hence crash-recovery — is unchanged).
    - **On exercise:** `otc_exercise_saga.go` (`record_buyer_exercise_cost` step) writes the buyer's `SecurityType="option"`, `OTC=true` row with `TotalGain = (market − strike) × qty − premium`, in the exercise month, where `market` is the underlying `Listing.Price` snapshotted pre-saga. The row may be negative (premium > bargain), correctly reducing the buyer's monthly gain. Best-effort: skipped (and basis kept at strike) when the market price is unknown — never blocks the exercise.
    - **On expiry:** the daily `OTCExpiryCron.expireContract` writes the buyer's `−premium` loss row in the expiry month (idempotent on `expire-contract-<id>-buyer-premium-loss`, written before the status flip). The seller adds nothing (already taxed at accept).
  - **Buyer's cost basis steps up to market on exercise.** `otc_exercise_saga.go` sets the credited `Holding.AveragePrice` to the snapshotted **market** price (was strike). This prevents double taxation: since `(market − strike)` is taxed at exercise, a later sale at market produces zero stock gain. Equivalence: `((market−strike)×qty − premium)` [exercise] `+ (S−market)×qty` [sale] `= (S−strike)×qty − premium`. Falls back to strike basis when the market price is unknown.
- **Bank (Profit Banke) exemption.** `TaxCollectionRepository.ListOwnersWithGains` filters to `owner_type='client'`, so bank-owned capital gains (actuary trading on behalf of the bank — option premiums, exercise gains, dividends, stock) are never collected; the profit stays with the bank. Same rule as dividends.
- **Cross-bank OTC buyer taxation is deferred** (`docs/Bugs.txt` §"Cohort-dependent TODOs" item 5): the frozen SI-TX exercise flow carries neither premium nor market price, so `(market−strike)×qty − premium` is uncomputable on the buyer's bank. Cross-bank **sellers** are still taxed (the strike-gain write in `PeerOTCGRPCHandler.recordOptionExercise` DEBIT branch is unchanged); cross-bank buyers are taxed via their eventual stock sale (shares credited at strike basis), as before.
- **Total P/L = sum of all CG rows regardless of `SecurityType`.** `CapitalGainRepository.SumByOwner*` methods do NOT filter on `security_type`, so portfolio-summary totals already cover stock and option realisations together. Future per-security-type breakdown fields (e.g. `realized_profit_stock_rsd`, `realized_profit_options_rsd`) can be added on top without changing the totals.

### 21.1 gRPC Error Sentinels

Service errors carry typed sentinels defined in `<service>/internal/service/errors.go`. Each sentinel embeds a gRPC code via `contract/shared/svcerr.SentinelError`, so wrapping with `fmt.Errorf("Op: %w", sentinel)` automatically resolves to the correct wire status code via `status.FromError`.

Handlers do NOT map errors — they return wrapped service errors directly. The `contract/shared/grpcmw.UnaryLoggingInterceptor` records the full wrap chain (with the original underlying error) for any non-OK response, before the wire status is sent.

The api-gateway maps gRPC status codes to HTTP via `api-gateway/internal/handler/validation.go:grpcToHTTPError`. Distinct sentinels surface as distinct HTTP error codes, so a client can distinguish "wrong password" (401 unauthorized) from "account locked" (403 forbidden) from "account pending" (409 business_rule_violation), etc.

Email-not-found and bcrypt-mismatch deliberately collapse to the same `ErrInvalidCredentials` sentinel for security (prevents email enumeration). All other failure modes are distinct.

**Standardization audit (2026-06-08):** a sweep confirmed all services return `svcerr`-coded errors across the gRPC boundary. stock-service was brought into full conformance — bare `errors.New` returns that were mapping to HTTP 500 now use the correct codes: listing-not-found → `NotFound` (404); OTC business-rule rejections (terminal-state, self-counter/accept, settlement-not-future, accounts-not-bound) → `FailedPrecondition` (409); and `otc_service` debit/credit failures now wrap the account-service error with `%w` so its code (e.g. insufficient-balance `FailedPrecondition`) is preserved instead of being flattened to 500. Gateway websocket and rate-limit responses were standardized onto `apiError`/`abortWithError`. (cross-bank-protocol error responses are intentionally exempt and unchanged.)

### 21.2 Cross-Service Saga Coordination

Sagas that span multiple services (most stock-service crossbank/OTC/fund sagas, transaction-service inter-bank transfers) coordinate via three guarantees. Together they make distributed steps safe to retry on transient failure, debuggable via single-key joins across services, and durable across crashes between business commit and Kafka publish.

**Idempotency contract** — every saga-callee gRPC method (marked `// idempotent` in its proto) accepts a `string idempotency_key` field. Callees enforce the contract via a per-service `idempotency_records` table plus the `repository.Run[T]` wrapper, which atomically reserves the key inside the same transaction as the business write and caches the response payload. Saga callers populate the key as `saga.IdempotencyKey(saga_id, step_kind)` (deterministic). Retried saga steps return the cached response without re-executing — no double-debit, no double-credit, no double-create. Renaming the `idempotency_key` field or weakening the cache semantics is a wire-protocol breaking change and requires explicit user authorization.

**Saga context propagation** — outbound gRPC calls from inside a saga step carry `x-saga-id`, `x-saga-step`, and `x-acting-employee-id` metadata via `contract/shared/grpcmw.UnaryClientSagaContextInterceptor`. The callee's `UnarySagaContextInterceptor` extracts these into `context.Context`. Side-effect tables (`account_ledger_entries`, `stock_holdings`, plus the reservation/settlement debit ledger row) stamp `saga_id` + `saga_step` from the context, enabling cross-service auditing via a single SQL JOIN:

```sql
SELECT * FROM account_ledger_entries WHERE saga_id = '<id>';
SELECT * FROM stock_holdings        WHERE saga_id = '<id>';
```

Non-saga writes (REST handlers, crons that don't run a saga) leave both columns NULL. The metadata interceptors are part of the wire protocol and may not be removed without explicit user authorization.

**Outbox pattern** — saga-published Kafka events route through `contract/shared/outbox`. The `Enqueue(tx, topic, payload, saga_id)` write goes into the same DB transaction as the saga step's business action, so the event row commits atomically with the side effect or doesn't commit at all. A per-service `OutboxDrainer` goroutine reads pending rows (ticks every 500ms, batch up to 100), publishes to Kafka, marks `published_at`. Failures bump `attempt` + capture `last_error` and leave the row pending for the next tick. Crash between commit and publish is safe: the drainer picks up unpublished rows on restart. Stock-service routes every saga publisher (cross-bank accept/exercise/expire lifecycle, OTC offer create/counter/reject/accept/exercise, OTC contract/offer expiry, fund create/update/invest/redeem) through the outbox; services without an outbox fall back to direct best-effort `producer.PublishRaw`.

## 22. Concurrency & Transaction Safety

This is a banking system. All code must be concurrency-safe with proper transaction isolation, optimistic locking, and rollback guarantees.

### 22.1 Optimistic Locking

Every mutable model with a `Version int64` field uses a GORM `BeforeUpdate` hook to enforce optimistic locking:

```go
func (m *MyModel) BeforeUpdate(tx *gorm.DB) error {
    tx.Statement.Where("version = ?", m.Version)
    m.Version++
    return nil
}
```

**Rules:**
- Every `db.Save()` on a versioned model must check `result.RowsAffected == 0` → return `shared.ErrOptimisticLock`
- Never use `db.Model(&Struct{}).Updates(map...)` on versioned models — the zero-value struct has `Version=0`, so the hook adds `WHERE version = 0` (matches nothing). Always load the struct first, modify fields, then `db.Save()`.
- For bulk updates that intentionally skip version checks (spending resets, overdue marking), use `db.Session(&gorm.Session{SkipHooks: true})`.

**Versioned models:** Account, Company, Card, Loan, LoanRequest, Installment, Payment, Transfer, ExchangeRate, Client, ClientLimit, Employee, EmployeeLimit.

### 22.2 Transaction Requirements

| Pattern | Required Protection |
|---------|-------------------|
| Read → check condition → write (read-modify-write) | `SELECT FOR UPDATE` inside `db.Transaction()` |
| Multiple writes to same DB (create + update, debit + credit) | `db.Transaction()` wrapping all writes |
| Upsert (check existence → create or update) | PostgreSQL `ON CONFLICT` via `clause.OnConflict{}` |
| Bulk write (reset counters, mark overdue) | Single `UPDATE ... WHERE` statement (inherently atomic) |
| Cross-service gRPC multi-step (debit A → credit B) | Saga log pattern with persistent compensation |

**SELECT FOR UPDATE pattern:**
```go
db.Transaction(func(tx *gorm.DB) error {
    tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entity, id)
    // ... check conditions, modify ...
    return tx.Save(&entity).Error
})
```

**ON CONFLICT upsert pattern:**
```go
tx.Clauses(clause.OnConflict{
    Columns:   []clause.Column{{Name: "unique_field"}},
    DoUpdates: clause.AssignmentColumns([]string{"field1", "field2", "updated_at"}),
}).Create(&entity)
```

### 22.3 Saga Log Pattern (Cross-Service)

When a business operation spans multiple gRPC calls (e.g., 4-step cross-currency transfer), use the persistent saga log (`transaction-service/internal/model/saga_log.go`):

1. Record each step as `pending` in `saga_logs` table BEFORE executing the gRPC call
2. On success: mark step `completed`
3. On failure: mark step `failed`, record compensation steps as `compensating`, execute compensations
4. If compensation fails: leave in `compensating` status for background recovery goroutine
5. Kafka events published AFTER transaction commits (never inside the TX)

**Saga log fields:** `SagaID`, `TransactionID`, `TransactionType`, `StepNumber`, `StepName`, `Status`, `IsCompensation`, `AccountNumber`, `Amount` (decimal), `CompensationOf`, `ErrorMessage`

### 22.4 Spending Limits

Spending limits (daily/monthly) are enforced **atomically** inside `account-service`'s `UpdateBalance` repository method:

1. `SELECT FOR UPDATE` locks the account row
2. Check `daily_spending + debit <= daily_limit` inside the lock
3. Check `monthly_spending + debit <= monthly_limit` inside the lock
4. Check sufficient funds
5. Update balance + spending counters in same transaction

Transaction-service/payment-service may perform advisory pre-checks via gRPC reads, but these are NOT authoritative.

### 22.5 Background Goroutines

All cron/background goroutines must:
- Accept `context.Context` and honor `ctx.Done()` for graceful shutdown
- Use `defer ticker.Stop()` for tickers
- Use `select { case <-time.After(...): ... case <-ctx.Done(): return }` instead of `time.Sleep()`
- Wrap multi-step operations in transactions (e.g., unblock card = deactivate block + update card status)

### 22.6 Exemplary Implementations

Reference these when adding new concurrent code:
- **Exchange rate upsert:** `exchange-service/internal/repository/exchange_rate_repository.go` — TX + FOR UPDATE + version increment
- **Ledger debit/credit:** `account-service/internal/repository/ledger_repository.go` — FOR UPDATE + balance check + ledger entry + spending update (non-bank accounts) in single TX
- **Ledger transfer:** `account-service/internal/service/ledger_service.go` — atomic debit+credit in single TX

### 22.7 Anti-Patterns (NEVER Do These)

| Anti-Pattern | What To Do Instead |
|---|---|
| `db.Model(&Struct{}).Update("field", val)` on versioned model | Load struct, modify field, `db.Save()` |
| Read-then-write without transaction | Wrap in `db.Transaction()` with FOR UPDATE |
| SELECT → INSERT (upsert) | Use `clause.OnConflict{}` |
| Separate debit + credit calls (not in TX) | Use `LedgerService.Transfer()` or single TX |
| `time.Sleep()` in goroutine | `select { case <-time.After(): ... case <-ctx.Done(): }` |
| Ignore `RowsAffected == 0` after Save | Check and return `ErrOptimisticLock` |
| Kafka publish inside DB transaction | Publish AFTER `db.Transaction()` returns nil |
| Best-effort commission/fee collection | Use saga log; commission must be guaranteed |

## 23. API Versioning

The API gateway exposes a single live version: `/api/v3/`. v1 and v2 were retired by plan E (2026-04-27, route consolidation). Any request to `/api/v1/*` or `/api/v2/*` returns HTTP 404.

| Prefix | Status |
|---|---|
| `/api/v3/` | **Live.** The only supported API version. Hosts every route the gateway serves. |
| `/api/v1/`, `/api/v2/` | **Retired.** Returns 404. Removed in plan E. |
| `/api/` (unversioned) | **Removed.** Use `/api/v3/`. |
| `/api/latest/` | **Removed.** Use `/api/v3/` directly; aliases hide version drift. |

**Implementation files:**
- `api-gateway/internal/router/router_v3.go` — defines `SetupV3(r *gin.Engine, h *Handlers)`; registers every route grouped by identity rule.
- `api-gateway/internal/router/handlers.go` — `Deps` (gRPC client bundle) and `Handlers` (HTTP handler bundle); shared by every router version.
- `api-gateway/internal/router/router_versioning.md` — pattern documentation for adding a v4 (or any future version) and the sunset policy.

**Per-version pattern:** each `/api/vN` is its own explicit, self-contained router file. There is **no transparent fallback** between versions — adding a v4 means creating a new `router_v4.go` with its own `SetupV4` and wiring it side-by-side in `cmd/main.go`. Routes that don't change shape between versions call the same `h.X.Y` handler from the bundle. Routes that change shape bind to a new handler variant. v3 keeps working untouched.

**Why no fallback?** The previous v1 → v2 setup transparently delegated unknown v2 routes to v1 via `HandleContext`. This led to silent identity bugs (e.g., the actuary-limit regression fixed in spec C) when v2 added new identity rules but v1's handler kept v1 assumptions. Explicit per-version registration prevents that class of bug.

**Identity middleware** (spec C, plan 2026-04-27 part C) — every route group declares an identity rule via `middleware.ResolveIdentity`. The three rules in use:

- `OwnerIsPrincipal` — owner = JWT principal. Used for `/me/profile`, `/me/cards`, etc.
- `OwnerIsBankIfEmployee` — if the JWT principal is an employee, owner = bank (`OwnerType="bank"`, `OwnerID=nil`); the JWT id is carried as `ActingEmployeeID` for per-actuary limits. If the principal is a client, owner = principal. Used for trading routes (`/me/orders`, OTC, funds).
- `OwnerFromURLParam` — owner = client identified by a URL path parameter (`:client_id`). Used for employee-on-behalf-of-client endpoints.

Identity is read by handlers via the bound `ResolvedIdentity` context key. Handlers must not derive owner identity from request bodies or invent ad-hoc per-handler logic.

**API versioning contract** (going forward): newer versions must not break older versions unless the user has explicitly permitted it. Adding optional fields to v3 response bodies is allowed and does not count as a breaking change, provided existing clients that ignore unknown fields continue to work.

### Notable v3 endpoint groups

The full endpoint reference is in `docs/api/REST_API_v1.md` (kept under that filename per the project's REST-doc-naming rule even though it now describes v3 routes). Highlights:

| Method | Path | Middleware | Handler | Description |
|---|---|---|---|---|
| POST | `/api/v3/investment-funds` | AuthMiddleware + RequirePermission(`funds.manage`) | InvestmentFundHandler.CreateFund | Create a new investment fund (provisions a bank-side RSD account) |
| GET | `/api/v3/investment-funds` | AnyAuthMiddleware | InvestmentFundHandler.ListFunds | List funds (page / page_size / search / active_only) |
| GET | `/api/v3/investment-funds/:id` | AnyAuthMiddleware | InvestmentFundHandler.GetFund | Fund detail |
| PUT | `/api/v3/investment-funds/:id` | AuthMiddleware + RequirePermission(`funds.manage`) | InvestmentFundHandler.UpdateFund | Update fund (name/description/minimum/active) |
| POST | `/api/v3/investment-funds/:id/invest` | AnyAuthMiddleware | InvestmentFundHandler.Invest | Invest in fund (RSD or cross-currency via exchange-service) |
| POST | `/api/v3/investment-funds/:id/redeem` | AnyAuthMiddleware | InvestmentFundHandler.Redeem | Redeem from fund (rejects with `insufficient_fund_cash` when fund cash short — liquidation TODO) |
| GET | `/api/v3/me/investment-funds` | AnyAuthMiddleware | InvestmentFundHandler.ListMyPositions | Caller's fund positions |
| GET | `/api/v3/investment-funds/positions` | AuthMiddleware + RequirePermission(`funds.bank-position-read`) | InvestmentFundHandler.ListBankPositions | Bank-owned positions |
| GET | `/api/v3/actuaries/performance` | AuthMiddleware + RequirePermission(`funds.bank-position-read`) | InvestmentFundHandler.ActuaryPerformance | Realised profit per acting employee |
| POST | `/api/v3/admin/dividends` | AuthMiddleware + RequirePermission(`securities.manage.catalog`) | DividendHandler.DeclareDividend | Declare a dividend for a security (E4) |
| POST | `/api/v3/admin/dividends/:id/payout` | AuthMiddleware + RequirePermission(`securities.manage.catalog`) | DividendHandler.PayoutDividend | Fan out dividend credits to all holders (E4) |
| GET | `/api/v3/me/dividends` | AnyAuthMiddleware | DividendHandler.ListMyDividends | Caller's dividend payout history (E4) |
| GET | `/api/v3/investment-funds/:id/dividends` | AnyAuthMiddleware | DividendHandler.ListFundDividends | Fund's dividend history (E4) |
| POST | `/api/v3/me/otc/options` | AnyAuthMiddleware + ResolveIdentity | OTCOptionsHandler.CreateOffer | Create a **termless** OTC option listing `(owner, ticker, quantity)` — no preset strike/premium/settlement (terms are negotiated per chain); ticker-keyed; `account_id` required; one open offer per `(owner, ticker, direction)` (duplicate ⇒ 409); optional `on_behalf_of_client_id`. (The legacy single-chain `POST /api/v3/otc/offers` + its `/counter`,`/accept`,`/reject` routes were **deleted** — bidding goes through `POST /api/v3/otc/options/:id/bid` and the per-chain `/me/otc/options/:id/negotiations/*` routes.) |
| POST | `/api/v3/otc/contracts/:id/exercise` | AnyAuthMiddleware + RequirePermissionOrClient(All, `securities.trade`,`otc.trade`) | OTCOptionsHandler.ExerciseContract | Exercise option (cross-bank dispatches via Spec 4). Accounts read from the contract; optional `on_behalf_of_client_id` |
| GET | `/api/v3/otc/options/:id` | AnyAuthMiddleware | OTCOptionsHandler.GetOffer | Offer detail with revisions (terms viewer-contextual) |
| GET | `/api/v3/otc/contracts/:id` | AnyAuthMiddleware | OTCOptionsHandler.GetContract | Contract detail |
| GET | `/api/v3/me/otc/options/posted` | AnyAuthMiddleware | OTCOptionsHandler.ListMyPostedOffers | Caller's posted OTC option listings (any status) |
| GET | `/api/v3/me/otc/contracts` | AnyAuthMiddleware | OTCOptionsHandler.ListMyContracts | Caller's OTC contracts, LOCAL + REMOTE merged into one `contracts[]` array (each row carries `kind`/`routing_number`/`bank_code`/`me_owner`). **SP-2b (2026-06-05):** the legacy `peer_contracts`/`peer_total` response fields were removed — remote contracts now appear in `contracts[]` with `kind=remote`. |
| GET | `/api/v3/me/otc/options/negotiations` | AnyAuthMiddleware + ResolveIdentity | OTCOptionsHandler.ListMyNegotiations | Caller's LOCAL (intra-bank bidder) + REMOTE (cross-bank peer) negotiation chains, merged into one list (SP-1 Task 7). All statuses (open/countered/accepted/rejected/cancelled/expired) with optional `?statuses=` filter applied to both sets. Each item carries `kind` (`local`\|`remote`), `routing_number`/`bank_code` provenance, and `me_owner` (true only when the caller is the parent listing's poster/seller — never for a bidder; for remote, true iff we host the seller side). Remote `id` is the local peer-negotiation surrogate key; remote terms are projected from the mirrored offer. Local response also includes `minted_contract_id` (non-zero on `status=accepted` rows). Paging applies to the local set; remote chains are appended in full; `total` is the local total. The gateway is a uniform pass-through. |
| GET | `/api/v3/me/otc/options/negotiations/:nid/revisions` | AnyAuthMiddleware + ResolveIdentity | OTCOptionsHandler.ListMyNegotiationRevisions | Full revision chain (BID/COUNTER/ACCEPT/REJECT) for a negotiation. Caller must be the bidder or the listing's poster; returns 403 otherwise. |

## 24. Investment Funds (Celina 4)

### Entities (stock-service)

| Entity | Table | Purpose |
|---|---|---|
| `InvestmentFund` | `investment_funds` | Supervisor-managed pool. One bank-owned RSD account, manager_employee_id, minimum contribution. Optimistic locking via Version. `dividend_mode` (`payout`\|`reinvest`, default `payout` — SP4 2026-06-04): in `reinvest` mode `DividendService.Payout` buys `floor(grossRSD/priceRSD)` more shares of the dividend-paying stock on behalf of the fund (DRIP, best-effort — cash retained on failure) instead of leaving the dividend as cash. Settable on create/update; surfaced on `FundResponse`. |
| `FundValueSnapshot` | `fund_value_snapshots` | Daily point-in-time NAV per fund (SP3 — 2026-06-04). Unique `(fund_id, date)`; columns `total_value_rsd`, `liquid_rsd_bal`, `holdings_value_rsd`, `investor_count`. Written by the `fund-snapshot-cron` (daily, default 23:50 UTC, `FUND_SNAPSHOT_CRON_UTC`). Feeds the discovery/detail statistics (annualized return, volatility, reward-to-variability, max drawdown — std-dev/Sharpe use monthly-resampled returns; drawdown uses the daily series) and the detail `history`/`average_history` charts. Metrics are computed on demand and shown only once there are ≥ `FUND_METRICS_MIN_MONTHLY_RETURNS` (default 2) monthly returns; otherwise `metrics_available=false`. `ListFunds` supports `sort_by`/`sort_order` over the metrics (`FundResponse`/`FundDetailResponse` extended). |
| `ClientFundPosition` | `client_fund_positions` | One row per (fund, owner). Owner identified by (`OwnerType`, `OwnerID`) — `bank` with `OwnerID IS NULL` for the bank's own stake, `client` with non-null `OwnerID` for clients. (Renamed from the pre-Task-4 `(UserID=1_000_000_000, SystemType="employee")` sentinel pattern by plan 2026-04-27-owner-type-schema.md.) TotalContributedRSD accumulates contributions and decrements on redeem. |
| `FundContribution` | `fund_contributions` | Append-mostly history of every invest/redeem event. Owner identified by (`OwnerType`, `OwnerID`); status pending → completed/failed under the saga that produced it. SagaID is a UUID string referencing saga_logs. |
| `FundHolding` | `fund_holdings` | Fund-side analogue of Holding. Increments on on-behalf-of-fund order fills, decrements on liquidation. FIFO order-by created_at for liquidation. |
| `Order.FundID` | `orders.fund_id` | New optional column. Non-nil when the order was placed on behalf of a fund — `OwnerType="bank"`/`OwnerID IS NULL` and fills credit `fund_holdings` instead of `holdings`. |
| `DividendPayment` | `dividend_payments` | One declared dividend per `(security_id, payment_date)`. Status: `declared → paid_out` (or `cancelled`). Created by `DividendService.Declare` (E4). UNIQUE on `(security_id, payment_date)`. |
| `DividendPayout` | `dividend_payouts` | One row per `(dividend_payment_id, holding_id)` — the actual account credit record. `holding_owner_type`: `client`/`bank`/`investment_fund`. `tax_amount_rsd` = 15% of gross for clients, 0 for bank/fund. `idempotency_key` is UNIQUE (`"dividend-<payment_id>-<holding_id>"`). (E4) |
| `FundDividendPayment` | `fund_dividend_payments` | Snapshot of the fund-level dividend event plus per-investor shares at payout time (`per_investor_snapshot` JSONB). UNIQUE on `(dividend_payment_id, fund_id)`. Used to compute `dividends_received_rsd` in portfolio positions (E3/E4). |

### Kafka topics

| Topic | Producer | Consumer | Payload |
|---|---|---|---|
| `stock.fund-created` | stock-service | (none yet) | StockFundCreatedMessage |
| `stock.fund-updated` | stock-service | (none yet) | StockFundUpdatedMessage |
| `stock.fund-invested` | stock-service | (none yet) | `StockFundInvestedMessage` — payload carries `owner_type` (`client`\|`bank`) + `owner_id` (`*uint64`, null when `owner_type=bank`); renamed from `(user_id, system_type)` by Task 9 of plan 2026-04-27-owner-type-schema.md |
| `stock.fund-redeemed` | stock-service | (none yet) | `StockFundRedeemedMessage` — same owner_type/owner_id rename |
| `stock.funds-reassigned` | stock-service | (none yet) | StockFundsReassignedMessage |
| `user.supervisor-demoted` | user-service (via outbox relay) | stock-service (SupervisorDemotedConsumer) | UserSupervisorDemotedMessage |

### Permissions

- `funds.manage` (existing) — create / update funds. Granted to `EmployeeSupervisor` and `EmployeeAdmin`.
- `funds.bank-position-read` (new) — view the bank's positions and actuary performance. Granted to `EmployeeSupervisor` and `EmployeeAdmin`.

### gRPC service: `InvestmentFundService`

Defined in `contract/proto/stock/stock.proto`. RPCs:
- `CreateFund` / `ListFunds` / `GetFund` / `UpdateFund` (CRUD)
- `InvestInFund` / `RedeemFromFund` (saga-orchestrated money flow)
- `ListMyPositions` / `ListBankPositions` (per-owner reads)
- `GetActuaryPerformance` (aggregated realised gains per acting employee)

Shared message: `OnBehalfOf { type: "self"|"bank"|"fund"; fund_id: uint64 }` — used both by InvestInFund/RedeemFromFund (self vs bank) and by `Order.OnBehalfOf` (Task 18, follow-up) for placing orders on behalf of a fund.

### Settings

| Key | Default | Set by | Used by |
|---|---|---|---|
| `fund_redemption_fee_pct` | `0.005` (0.5%) | stock-service main.go on first boot | Redeem saga; bank redeems pay 0 |

### Saga shapes

**Invest:** `debit_source` → `credit_fund` → `upsert_position`. Cross-currency invest converts via exchange-service.Convert before the debit. Failure of step 2 reverses step 1; failure of step 3 reverses both.

**Redeem:** `debit_fund` (amount + fee) → `credit_target` → optional `credit_bank_fee` → `decrement_position`. When fund cash is short, returns `ErrInsufficientFundCash` (HTTP 409). Liquidation sub-saga that sells securities to free cash is a follow-up.

### Outbox + cross-service event flow

Permission revoke that drops `funds.manage` → user-service writes a `user.supervisor-demoted` row to its `outbox_events` table inside the same TX → relay goroutine drains to Kafka → stock-service's SupervisorDemotedConsumer reassigns every fund managed by that supervisor to the demoting admin in a single TX, then publishes `stock.funds-reassigned`.

### Open follow-ups

- Task 14: invest-saga compensation matrix tests
- Tasks 16–17: liquidation sub-saga (FIFO sell-orders + fill polling) wired into Redeem
- Task 18: extend `POST /me/orders` with `on_behalf_of=fund` (routes order through fund's RSD account; fills credit `fund_holdings`)
- Task 20: position-reads service (mark-to-market value, profit, percentage_fund)
- Task 21: actuary-performance aggregation
- Task 25: integration tests in test-app/workflows

## 25. Inter-Bank Cross-Bank Communication (Celina 5 / SI-TX)

Cross-bank money movement and OTC trading conform to the SI-TX cohort wire protocol (https://arsen.srht.site/si-tx-proto/) referenced by Celina 5. The implementation landed in Phases 2-4 of the SI-TX refactor; design doc: `docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md`. Phase plans: `docs/superpowers/plans/2026-04-29-celina5-sitx-phase{1,2,3,4}-*.md`.

§25 covers the **transfer-side** SI-TX implementation. Cross-bank OTC (negotiations + acceptance) is in §27.

### Public peer-facing routes (api-gateway)

Hosted on api-gateway, gated by `middleware.PeerAuth` (hybrid auth — see "Authentication" below).

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v3/interbank` | Receives `Message<Type>` envelope. Decodes by `messageType` and dispatches to `interbank-service.PeerTxService` via gRPC (2026-06-07 cutover; was transaction-service). |

### Admin REST routes (api-gateway, employee JWT)

Hosted on api-gateway, gated by employee JWT + `peer_banks.manage.any` permission. Allows ops to add/update/remove peer banks at runtime without redeploys.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v3/peer-banks` | List registered peers (optional `?active_only=true`). |
| GET | `/api/v3/peer-banks/:id` | Read one. |
| POST | `/api/v3/peer-banks` | Register a new peer (bank_code, routing_number, base_url, api_token, optional HMAC keys, active flag). **Returns 400 when `bank_code` or `routing_number` equals this bank's own** (peer-collision invariant, SP-2a). |
| PUT | `/api/v3/peer-banks/:id` | Update mutable fields. |
| DELETE | `/api/v3/peer-banks/:id` | Remove. |

**Peer-collision invariant (SP-2a, 2026-06-05):** A peer may never share this bank's own `bank_code` or `routing_number`. The check is enforced at three layers: (1) `POST /api/v3/peer-banks` returns 400 if either field matches own; (2) the `OTCOfferRepository.UpsertRemote` and `OptionContractRepository.UpsertRemoteContract` ingestion paths reject rows where `routing_number == OwnRouting()`; (3) `stock-service` startup aborts if a peer-bank row with own routing is found in the DB (invariant never silently violated at runtime). This ensures that `routing_number == OwnRouting()` is a reliable discriminator between local rows and remote (folded-in) rows in the unified tables.

API tokens are bcrypt-hashed before persist. The plaintext `api_token` is also stored alongside (only readable via the internal `ResolvePeerByAPIToken` RPC, never via REST) so the api-gateway middleware can resolve incoming tokens to peer-bank records.

### Authentication

`middleware.PeerAuth` accepts either:

1. **`X-Api-Key: <token>`** — looked up via `interbank-service.PeerBankAdminService.ResolvePeerByAPIToken` (internal gRPC; constant-time compare against `peer_banks.api_token_plaintext` for active peers only). *(2026-06-07 cutover: the gateway dials interbank-service for this; was transaction-service.)*
2. **`X-Bank-Code: <code>` + `X-Bank-Signature: <hex SHA-256>` + `X-Timestamp: <RFC3339>` + `X-Nonce: <single-use>`** — looked up via `ResolvePeerByBankCode`. Signature verified against `peer_banks.hmac_inbound_key`; timestamp window ±5 min; nonce dedup window 10 min in Redis (`cache.PeerNonceStore`).

On success the middleware sets `peer_bank_code` and `peer_routing_number` on the gin context. On any failure: 401 with empty body (no info leak; constant-time compare).

### gRPC services

> **2026-06-07 cutover:** the entire SI-TX engine + peer_banks registry below was
> **moved out of transaction-service into `interbank-service`** (gRPC `:50062`,
> its own `interbank_db`). The proto definitions, RPCs, wire protocol, and
> execution logic are byte-for-byte unchanged — only the hosting service changed.
> transaction-service no longer registers `PeerTxService`/`PeerBankAdminService`
> and is now pure local payments/transfers/fees. interbank-service additionally
> hosts `PeerEgressService` (the single outbound HTTP egress to peers),
> `PeerUserService` (peer `/user` resolver), and a `PeerOTCService` forwarder→stock.

- **`PeerTxService`** (interbank-service): 4 RPCs — `HandleNewTx`, `HandleCommitTx`, `HandleRollbackTx`, `InitiateOutboundTx`, plus `InitiateOutboundTxWithPostings` (Phase 4) and `GetTxStatus`.
- **`PeerBankAdminService`** (interbank-service): 5 admin RPCs (List/Get/Create/Update/Delete) + 2 internal-resolve RPCs (`ResolvePeerByAPIToken`, `ResolvePeerByBankCode`) returning `PeerBankFull` (with HMAC keys + plaintext token, never exposed via REST).

### TX execution

**Receiver side** (`HandleNewTx`):
1. Replay-cache lookup on `(peer_bank_code, locally_generated_key)` in `peer_idempotence_records`. Hit → return cached vote.
2. `vote_builder.BuildPrelimVote(postings)` — cheap balance check (UNBALANCED_TX if Σ debits ≠ Σ credits per `assetId`).
3. `posting_executor.Reserve(...)` — per-posting checks: `NO_SUCH_ACCOUNT` (account not found), `UNACCEPTABLE_ASSET` (inactive account), `NO_SUCH_ASSET` (currency mismatch), `INSUFFICIENT_ASSET` (reserve fails). On YES: CREDIT postings → `account-service.ReserveIncoming(reservation_key="<peer>:<idem>")`; DEBIT (money) postings → `account-service.ReserveOutgoing(reservation_key="<peer>:<idem>:<i>")` (reserve-then-settle HOLD on AvailableBalance, tracked in `DebitsJSON`).
4. Record cached response in `peer_idempotence_records` (same DB tx as the local commit per SI-TX §"R must record the idempotence key").
5. Return 200 + `TransactionVote`.

**Receiver side** (`HandleCommitTx` / `HandleRollbackTx`): look up idem record. CREDIT side → `account-service.CommitIncoming` / `ReleaseIncoming` on the reservation key. DEBIT (money) side → `account-service.SettleOutgoing` (COMMIT, money leaves) / `ReleaseOutgoing` (ROLLBACK, hold lifted) on each `DebitsJSON` per-posting key. Return 204.

**Sender side** (`InitiateOutboundTx`):
1. Detect peer routing from receiver-account 3-digit prefix. `peerLookup` reads `peer_banks` table.
2. Generate UUID idempotence key. Persist `outbound_peer_txs` row in `pending`.
3. **Reserve-then-settle**: `account-service.ReserveOutgoing(reservation_key="peer-out:<idem>")` to HOLD the sender's funds (AvailableBalance dips, Balance untouched). A failed reserve marks the row `rolled_back` (so the replay cron can't later commit an unfunded transfer).
4. Best-effort dispatch via `sitx.PeerHTTPClient` to peer's `/interbank` (`Message<NEW_TX>`); on YES, follow up with `Message<COMMIT_TX>` then `SettleOutgoing` (money leaves); on NO, `ReleaseOutgoing` (hold lifted). On any error, leave row `pending` and `OutboundReplayCron` resumes (its `LocalCommitFunc`/`LocalReversalFunc` settle/release with the same idempotent keys).

**Time-safety backstop**: `OutgoingReservationTimeoutCron` (account-service, TTL `OUTGOING_RESERVATION_TTL`, default 10m) releases any pending `OutgoingReservation` whose peer never sent COMMIT/ROLLBACK. `SettleOutgoing` refuses a non-pending row, so a late COMMIT racing the timeout cannot re-debit.

`InitiateOutboundTxWithPostings` is the same flow but accepts a pre-composed `[]Posting` (used by cross-bank OTC accept — see §27).

### Database tables

> **2026-06-07 cutover:** `peer_banks`, `peer_idempotence_records`, and
> `outbound_peer_txs` now live in **`interbank_db`** (interbank-service's own
> Postgres, host port `5443`), not `transaction_db`. `outgoing_reservations`
> stays in `account_db` (account-service owns it). The schemas are unchanged.

- **`peer_banks`** — runtime-editable registry. Columns: `id`, `bank_code`, `routing_number`, `base_url`, `api_token_bcrypt`, `api_token_plaintext`, `hmac_inbound_key`, `hmac_outbound_key`, `active`, timestamps.
- **`peer_idempotence_records`** — receiver-side replay cache. Composite-unique on `(peer_bank_code, locally_generated_key)`. Stores `response_payload_json`, `debits_json` (DEBIT-leg list: per-posting `accountNumber`/`amount`/`idempotencyTag`, used to settle outgoing holds at COMMIT_TX and release them at ROLLBACK_TX), and `options_json` (option-leg list for COMMIT_TX materialisation).
- **`outgoing_reservations`** (account-service DB) — debit-side reserve-then-settle table for cross-bank money DEBIT legs (mirror of `incoming_reservations`). Columns: `id`, `account_number`, `amount`, `currency`, `reservation_key` (unique; SI-TX per-posting tag `"<peer>:<idem>:<i>"` or simple-transfer `"peer-out:<idem>"`), `status` (`pending` → `settled` | `released`), `created_at` (indexed for the timeout sweep), `updated_at`, `version`. `ReserveOutgoing` dips AvailableBalance; `SettleOutgoing` debits Balance + ledger entry; `ReleaseOutgoing` restores AvailableBalance. `OutgoingReservationTimeoutCron` releases pending rows older than `OUTGOING_RESERVATION_TTL`.
- **`outbound_peer_txs`** — sender-side state. Columns: `id`, `idempotence_key`, `peer_bank_code`, `tx_kind` (`transfer` | `otc-accept` | `otc-exercise`), `postings_json`, `status` (`pending` | `committing` | `committed` | `rolled_back` | `failed`), `attempt_count`, `last_attempt_at`, `last_error`, timestamps.
- **cross-bank option contracts** (stock-service DB, Celina 5) — **SP-2a (2026-06-05): folded into the unified `option_contracts` table as REMOTE rows; the standalone `peer_option_contracts` table is retired.** See §27 for the full column mapping. Lifecycle (on the shared `status` column, PEER vocabulary): `active` → `exercised` (via exercise SI-TX) or `expired` (via daily cron after settlement_date passes); buyer-side claim transits `active` → `exercising`.

### Retry / replay policy

`OutboundReplayCron` (interbank-service; 2026-06-07 cutover — was transaction-service): 30s tick. Scans `outbound_peer_txs` rows in `pending` whose `last_attempt_at` is older than 60s (or NULL — never attempted). 4-attempt cap; rows that exceed get marked `failed`. Receiver returns the same cached vote on every retry due to idempotence-key dedup.

**Release on terminal failure (cron + inline parity):** because the sender's funds are HELD (reserve-then-settle) at initiation, every terminal non-committed outcome must lift that hold (no money ever left). On a peer **NO vote** *and* on **max-attempts-exceeded**, the cron first reverses the local effects (via `PeerTxGRPCHandler.ReverseOutboundLocal`, wired as the cron's `LocalReversalFunc`) before marking the row `rolled_back` / `failed`. The reversal dispatches by `tx_kind`: `payment` (the simple-transfer kind `InitiateOutboundTx` actually sets) releases the single local outgoing hold with key `peer-out-release-<idem>`; OTC kinds (`transfer`/`otc-accept`/`otc-exercise` from `InitiateOutboundTxWithPostings`) delegate to `PostingExecutor.ReverseLocal`, which releases the local CREDIT reservation (`sitx-localrelease-<own>:<idem>`) and releases each local DEBIT hold (`sitx-localrelease-out-<own>:<idem>:<i>`). On peer **YES**, the commit path (inline, or the cron's `LocalCommitFunc` = `PeerTxGRPCHandler.CommitOutboundLocal`) settles the holds (`peer-out-settle-<idem>` / `sitx-localsettle-out-<own>:<idem>:<i>`). All keys match the inline dispatch path so the two never double-act. If a reversal/settle itself fails, the row is kept `pending` (via `MarkAttempt`) so a later tick retries it — money is never stranded in a terminal row.

**ROLLBACK_TX to the peer on abandonment.** Whenever the sender terminally abandons a row it had already dispatched NEW_TX for (max-attempts `failed`, NO-vote `rolled_back`, inline NO-vote, or `PeerTxReconciler` resolving to `rolled_back`), it also sends a `ROLLBACK_TX` to the peer (shared `dispatchPeerRollback` / `PeerTxGRPCHandler.rollbackPeer`). This releases any reservation the peer placed when it voted YES — a benign incoming-credit hold on the transfer path, or a **real seller-share lock** on the OTC path. `HandleRollbackTx` is idempotent (release by key, no-op when there's no record or it already rolled back), so it is safe to send on every terminal non-committed transition and safe to retry. Best-effort: a dispatch failure is logged and the peer's `OutgoingReservationTimeoutCron` (money) is the final backstop for holds whose peer never answers at all.

### NoVote reason codes

Verbatim from SI-TX (each emitted with optional posting index for posting-scoped reasons):

| Reason | Trigger |
|---|---|
| `UNBALANCED_TX` | Σ debits ≠ Σ credits per `assetId` |
| `NO_SUCH_ACCOUNT` | account_id not found locally |
| `NO_SUCH_ASSET` | account currency ≠ posting `assetId` |
| `UNACCEPTABLE_ASSET` | inactive account, or debit-posting on our routing (peer can't order us to debit) |
| `INSUFFICIENT_ASSET` | balance check / reserve failed |
| `OPTION_AMOUNT_INCORRECT` | OTC posting amount drift (Phase 4) |
| `OPTION_USED_OR_EXPIRED` | OTC contract already exercised / past settlement (Phase 4) |
| `OPTION_NEGOTIATION_NOT_FOUND` | OTC negotiation reference invalid (Phase 4) |

### Permission

- **`peer_banks.manage.any`** — admin CRUD on `peer_banks`. Granted to `EmployeeAdmin` only (via the wildcard `*` grant).

### Authentication failure semantics

All `PeerAuth` failures return 401 with empty body. Constant-time comparison via `crypto/hmac.Equal` for the HMAC path; the API-token path uses a custom constant-time string compare. No info leak about which header failed, whether the bank is registered, or whether the timestamp/nonce is out-of-window.

### Out of scope

- TLS termination is the deployment platform's responsibility (gateway accepts plaintext HTTP on the internal network).
- HMAC key rotation: admins use `PUT /api/v3/peer-banks/:id` to rotate keys; the old key is invalidated immediately on commit. Mid-flight requests using the old key fail 401 — peer banks must coordinate rotations.

## 26. Intra-bank OTC Options (Celina 4 / Spec 2)

### Entities (stock-service)

| Entity | Table | Purpose |
|---|---|---|
| `OTCOffer` | `otc_offers` | A **termless** marketplace LISTING ("optionable inventory"). Carries direction, stock_id, qty (total), status — the preset-term columns (strike/premium/settlement_date) were **dropped**; terms are negotiated per chain (`OTCNegotiation`). One open offer per `(initiator owner, ticker, direction)` is enforced by a partial unique index over open rows (with a merge-duplicate startup migration). Initiator + counterparty identified by (`InitiatorOwnerType`, `InitiatorOwnerID`) / (`CounterpartyOwnerType`, `CounterpartyOwnerID`); `LastModifiedByPrincipalType`/`LastModifiedByPrincipalID` records the actor (principal) of the latest revision. `InitiatorAccountID` is the initiator's account bound at offer creation (pays the premium on `buy_initiated`, receives it on `sell_initiated`). `Ticker` (string, size 16, not null, default `''`) carries a human-readable underlying-stock ticker for in-app notification rendering (Plan B1). (Renamed from the pre-Task-4 `(user_id, system_type)` triples by plan 2026-04-27-owner-type-schema.md.) Optimistic-locked. |
| `OTCOfferRevision` | `otc_offer_revisions` | Append-only history of every CREATE/COUNTER/ACCEPT/REJECT action on an offer. Carries `ModifiedByPrincipalType`/`ModifiedByPrincipalID` (the principal who issued the revision, not the resource owner). (offer_id, revision_number) is unique. |
| `OptionContract` | `option_contracts` | The premium-paid executed option produced by the accept saga. Buyer + seller identified by (`BuyerOwnerType`, `BuyerOwnerID`) / (`SellerOwnerType`, `SellerOwnerID`); `BuyerAccountID`/`SellerAccountID` are bound at accept time and read straight off the contract on exercise; status ∈ {ACTIVE, EXERCISED, EXPIRED, FAILED}. `Ticker` (string, size 16, not null, default `''`) carries a human-readable underlying-stock ticker for in-app notification rendering (Plan B1). |
| `OTCOfferReadReceipt` | `otc_offer_read_receipts` | Composite-PK row tracking the most recent updated_at the owner has seen for an offer. PK is (`OwnerType`, `OwnerID`, `OfferID`); bank readers materialise as `OwnerID=0` because Postgres disallows NULL in primary keys. Drives the `unread` flag. |
| `HoldingReservation.OTCContractID` | `holding_reservations.otc_contract_id` | New nullable column. Either OrderID or OTCContractID is set; CHECK constraint enforces the XOR. |

### Permissions

- `otc.trade` — required for create/counter/accept/reject/exercise by **employees**. Granted to `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin` (which already have `securities.trade`). **Clients** are not permission-gated on these routes — they are allowed by `RequirePermissionOrClient` and constrained instead by resource-ownership checks (`ResolveAndCheckAccount`): a client may only supply their own accounts.
- `otc.trade.on_behalf` (new) — lets an employee act on behalf of a client on the OTC option routes (and `/otc/offers/:id/buy-on-behalf`) by setting `on_behalf_of_client_id`. Granted to `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin`. An employee with no `on_behalf_of_client_id` acts as the bank and must use a bank account.

### gRPC service: `OTCOptionsService`

Defined in `contract/proto/stock/stock.proto`. RPCs: CreateOffer, ListMyOffers, GetOffer, **UpdateOTCOfferQuantity**, ListMyContracts, GetContract, ExerciseContract, OpenNegotiation, CounterNegotiation, AcceptNegotiationChain, RejectNegotiation, CancelNegotiation, **CancelListing**, ListMyNegotiations, ListNegotiationsByListing. (The legacy single-chain `CounterOffer` / `AcceptOffer` / `RejectOffer` RPCs — unrouted since Phase 8 — were **removed**; their HTTP routes are gone and negotiation runs through the parallel-chain RPCs.)

`UpdateOTCOfferQuantity(UpdateOTCOfferQuantityRequest) → OTCOfferResponse` — SETs an offer's TOTAL quantity (backs `PUT /api/v3/me/otc/options/:id`). Owner-only; the offer must be local + open. New quantity must be `> 0`, not below the shares already committed to formed/forming contracts on the offer, and not above the owner's holding for the ticker (net of other active commitments). Optimistic-lock safe (`SELECT FOR UPDATE` + version check). Reflects the **termless optionable-inventory** model: an `OTCOffer` is `(owner, ticker, quantity, direction)` with NO own strike/premium/settlement — those are negotiated per chain, so resizing the lot is the offer's only mutable term.

`CancelListing(CancelListingRequest) → CancelListingResponse` — closes a parent `OTCOffer` listing posted by the caller; cascade-cancels every still-open child `OTCNegotiation` in the same DB transaction. Authorization: caller's (owner_type, owner_id) must match the offer's `initiator_owner_*`. Listing status must be open. Returns the cancelled parent + the list of cascade-cancelled chain rows (so the gateway can publish per-chain `OTC_OFFER_CASCADE_CANCELLED` notifications). No fund/share unwinding — listings hold no reservations at the parent level; reservations only exist inside the accept saga and are guarded by the parent-status check there.

`ListUnifiedOptionOffersRequest` gained an `owner_only_seller_id` string field: when non-empty the in-memory snapshot is filtered to `kind=local` entries whose `SellerID` exactly matches (SI-TX form: `"client-<principal_id>"` or `"bank"`). Used by `GET /api/v3/me/otc/options` to render the marketplace view scoped to the caller's own open listings without changing the response shape.

### Kafka topics

All OTC payloads embed one or more `OTCParty { owner_type, owner_id, bank_code? }` structs (renamed from `(user_id, system_type)` by plan 2026-04-27-owner-type-schema.md, Task 9). `owner_id` is `*uint64` and is `null` when `owner_type == "bank"`. For events whose semantic field is the *actor* of an action (e.g. `ModifiedBy`/`RejectedBy`), employee actors materialise as `owner_type="bank"`/`owner_id=null` because employees never own resources in this domain.

| Topic | Producer | Payload |
|---|---|---|
| `otc.offer-created` | stock-service | OTCOfferCreatedMessage |
| `otc.offer-countered` | stock-service | OTCOfferCounteredMessage |
| `otc.offer-rejected` | stock-service | OTCOfferRejectedMessage |
| `otc.offer-expired` | stock-service (cron) | OTCOfferExpiredMessage |
| `otc.contract-created` | stock-service | OTCContractCreatedMessage |
| `otc.contract-exercised` | stock-service | OTCContractExercisedMessage |
| `otc.contract-expired` | stock-service (cron) | OTCContractExpiredMessage |
| `otc.contract-failed` | stock-service | OTCContractFailedMessage |

### Sagas

**Accept saga** (premium-payment, §6.1 of design): reserve_seller_shares + create OptionContract → ReserveFunds(buyer) → PartialSettle(buyer) → CreditAccount(seller) → mark_offer_accepted → kafka. On post-step-1 failure compensations reverse the prior side effects.

**Exercise saga** (§6.2 of design): ReserveFunds(buyer, strike) → settle → credit seller → ConsumeForOTCContract → upsert buyer's holding → mark EXERCISED + kafka.

**Expiry cron**: daily 02:00 UTC. Pass A: ACTIVE contracts past settlement_date → release seller's reservation, mark EXPIRED, publish event. Pass B: PENDING/COUNTERED offers past settlement_date → mark EXPIRED, publish event.

### Cross-currency support

Both Accept and Exercise convert through `exchange-service.Convert` when buyer + seller account currencies differ:

- Premium / strike are denominated in the seller's currency
- Buyer-side reserve, settle, and compensation legs run in the buyer's currency at the live rate
- Seller is credited in their currency

Same-currency flows skip the conversion call entirely.

## 27. Cross-Bank OTC Options (Celina 5 / SI-TX)

Full cross-bank OTC option lifecycle: discovery → initiation → counter-offer → accept → exercise → expiry. The negotiation surface (`/api/v3/cross-bank-protocol/negotiations/...`) and the option-formation / exercise transactions ride on the §25 SI-TX wire (`POST /api/v3/cross-bank-protocol/interbank` + `Message<Type>` envelopes).

**SP-2b clean-cut (2026-06-05) — unified client write surface.** There is **no separate `/me/peer-otc/*` client surface** any more, and `POST /me/otc/contracts/peer/:id/exercise` is gone. Cross-bank negotiation initiation/counter/accept/cancel and cross-bank exercise are now dispatched **inside stock-service** behind the **same unified client routes** used for local OTC, selected by the listing's routing (`routing_number == OwnRouting()` ⇒ local, else remote):

- initiate (bid): `POST /api/v3/otc/options/:id/bid`
- counter / accept / reject: `POST /api/v3/me/otc/options/:id/negotiations/:nid/{counter,accept,reject}`
- cancel: `DELETE /api/v3/me/otc/options/:id/negotiations/:nid`
- list own chains (local + remote merged): `GET /api/v3/me/otc/options/negotiations`
- exercise: `POST /api/v3/otc/contracts/:id/exercise`

The api-gateway is a uniform pass-through for these; stock-service composes/forwards the SI-TX envelopes when the target is remote.

### Peer-facing routes (api-gateway, behind `PeerAuth`)

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v3/cross-bank-protocol/public-stock` | Returns this bank's open sell-initiated option offers as termless `/public-stock` seller inventory (reads `otc_offers` via `ListPublicOptionOffersForPeer` — no longer holdings/`public_quantity`, which were removed 2026-06-11). |
| POST | `/api/v3/negotiations` | Inbound from a peer. Body is a flat SI-TX `OtcOffer` (with `buyerId`/`sellerId` nested inside, per spec). Persists as a REMOTE row in the unified `otc_negotiations` table (via `OTCNegotiationRepository.UpsertRemoteNeg`); returns a fresh `ForeignBankId` directly (`{routingNumber, id}`), not wrapped. |
| PUT | `/api/v3/negotiations/:rid/:id` | Counter-offer. Body is the same flat `OtcOffer`. Updates the offer JSON. Per SI-TX §3.3, before persisting: returns **409** if the negotiation is closed (status ≠ `ongoing`) or if it is not the calling peer's turn (the stored `lastModifiedBy.routingNumber`, derived from the authenticated sender, must equal *this* bank's routing — i.e. we last proposed). Rejected counters persist no mutation. |
| GET | `/api/v3/negotiations/:rid/:id` | Returns SI-TX `OtcNegotiation` (= `OtcOffer & {isOngoing: boolean}`). `isOngoing` is `true` iff this bank's row has `status="ongoing"`. |
| DELETE | `/api/v3/negotiations/:rid/:id` | Soft-cancel: row status flips to `cancelled` (NOT physically deleted, per spec §3.5: "DELETE … sets isOngoing to false"). Subsequent `GET` returns 200 with `isOngoing=false`. |
| GET | `/api/v3/negotiations/:rid/:id/accept` | Accept — composes the 4-posting option-formation `Transaction` and dispatches via `PeerTxService.InitiateOutboundTxWithPostings`. Returns `{transaction_id, status}`. |
| GET | `/api/v3/user/:rid/:id` | Counterparty user info lookup. Returns 404 if `rid` ≠ this bank's routing. For own routing, dispatches to `client-service.GetClient` for `client-N` ids and `user-service.GetEmployee` for `employee-N` ids. |

### Client-facing routes (api-gateway, behind `AnyAuthMiddleware`)

**SP-2b clean-cut (2026-06-05):** the dedicated client-facing `/me/peer-otc/*` routes
(`POST`/`GET`/`PUT`/`POST …/accept`/`DELETE` `…/me/peer-otc/negotiations[/:rid/:id]`)
and `POST /me/otc/contracts/peer/:id/exercise` were **DELETED**. The behaviours they
provided are now dispatched inside stock-service behind the unified client routes
listed at the top of this section. The behavioural notes below (account-ownership
pre-check on initiate, the buyer-side remote mirror, the `seller_id` prefixed-form
requirement, and the inbound cross-bank routing assertions) still apply — they now
fire on the unified routes' remote-dispatch path:

- **Initiate (bid) on a remote listing.** `POST /api/v3/otc/options/:id/bid` with the
  listing's remote surrogate `:id`. **Body REQUIRES `bidder_account_id`**: gateway
  validates ownership + active status + currency match (account currency must equal the
  premium currency; no cross-bank FX). The account number is pinned into the SI-TX
  `OtcOffer.BuyerAccountNumber` so the seller's bank's posting executor uses this exact
  account on accept. stock-service resolves the seller's bank, composes the SI-TX
  `OtcOffer`, and POSTs it to the peer's `/cross-bank-protocol/negotiations`.
- **Contracts list.** `GET /api/v3/me/otc/contracts` returns LOCAL + REMOTE rows merged
  into one `contracts[]` array (`kind=remote` for cross-bank rows); the legacy
  `peer_contracts`/`peer_total` fields were removed.
- **Exercise a remote contract.** `POST /api/v3/otc/contracts/:id/exercise`. Buyer-only
  (rejects when this bank's row is `direction=DEBIT`); the strike account
  (`buyer_account_number`) is the only client-supplied money-path resource and is
  gated **authoritatively for ALL principals** gateway-side via
  `ResolveAndCheckAccountByNumber` (client → must own it; employee acting as the bank →
  must be a BANK account; employee on-behalf → that client's account; `403` on
  mismatch). **SP-3 Task 5 security fix (2026-06-05):** this replaced an
  `enforceOwnership` call that returned nil for any non-client principal, which had
  let a bank-acting employee pay the bank's strike obligation from an **arbitrary
  account** (including a client's) of the matching currency. stock-service's
  `exerciseRemoteContract` additionally re-asserts the same predicate
  (`isBankAccount` for a bank buyer; owner match for a client buyer; active +
  strike-currency match) **before** dispatching `InitiateOptionExercise`, as
  defense-in-depth — mirroring the already-hardened bid path (`openRemoteNegotiation`).
  Dispatches the 4-posting exercise SI-TX using the OPTION-pseudo-account form (see
  Exercise lifecycle below).
- **Own-chain list / counter / accept / cancel on a remote chain.** Use the unified
  `GET /api/v3/me/otc/options/negotiations` (remote rows carry `kind="remote"` and a
  `role`) and the per-chain `…/:nid/{counter,accept}` + `DELETE …/:nid`; stock-service
  forwards to the counterparty over SI-TX and mirrors the local row.

The buyer-side mirror is persisted by stock-service's unified outbound-negotiation flow (`OTCOptionsHandler`'s bid path) right after the outbound POST to the seller's bank succeeds — it calls `OTCNegotiationRepository.UpsertRemoteNeg` directly. Without that mirror the buyer-side list would be empty (only the seller's bank receives the inbound POST and persists the remote row). The mirror is stored as a REMOTE row in the unified `otc_negotiations` table. On natural-key `(routing_number, native_id)` conflicts the upsert overwrites, so retried inits are idempotent. (SP-2b clean-cut, 2026-06-05: the former `PeerOTCService.RecordOutboundNegotiation` / `ListMyPeerNegotiations` / `MarkNegotiationAccepted` / `CascadeCancelSiblings` gRPC RPCs were deleted — the unified `OTCOptionsHandler` + `otc_negotiation_remote_action.go` flow re-implements accept/cascade via repos directly.)

The auto-mirroring of counter/cancel onto the caller's local row is best-effort: failure logs but does not roll back the authoritative state on the counterparty's bank. If a mirror update fails the caller can re-pull via `GET /api/v3/negotiations/:rid/:id` on the counterparty later.

**Important — `seller_id` / `buyer_id` format.** The SI-TX wire spec requires `ForeignBankId.id` to be the prefixed form `client-<N>` or `employee-<N>`. When the unified bid route (`POST /api/v3/otc/options/:id/bid`) dispatches to a remote listing, stock-service composes the SI-TX `OtcOffer` from the resolved remote listing's seller id (already in prefixed form on the discovered row), so a malformed `"1"` seller id can't be smuggled through the client request. Otherwise the seller's bank would persist a row whose `seller_id` doesn't match any of its clients' principal ids and the chain would be invisible to the seller side's unified `GET /api/v3/me/otc/options/negotiations` list (remote rows).

**Cross-bank routing assertions (Fix #7/#8/#9, 2026-05-16).** The inbound `PeerOTCGRPCHandler.CreateNegotiation` rejects:
- buyer-routing spoofing: `buyer_id.routing_number` must equal the authenticated peer's routing (security — without this, peer A could submit a bid claiming peer C as buyer, causing the cross-bank accept to debit a third bank's user)
- foreign-seller bids: `seller_id.routing_number` must equal this bank's `OWN_BANK_CODE`-derived routing (inbound bids by definition target a local seller)

`CheckSellerCanDeliver` also asserts `seller_id.routing_number == ownRouting` and returns `ok=false` otherwise (defense — its only intended caller pre-filters, but the assertion is a safety net for future callers).

**Cross-bank FX limitation (Fix #2, 2026-05-16).** SI-TX postings must balance per `asset_id` across banks. The buyer's bank therefore cannot convert at execution time — the buyer must already hold an account in the offer's currency. The intra-bank accept saga's `exchange-service.Convert` path does NOT extend to the cross-bank flow. Bids with currency mismatch are rejected at the gateway with HTTP 400 and an explanatory error.

**Seller-centric discovery limitation — `buy_initiated` offers are intra-bank only (2.9.1).** The SI-TX OTC discovery + negotiation model is structurally **seller-centric**, by spec: a bank publishes only its **sellers'** public stock (§3.1 `PublicStock` lists `sellers`); a negotiation is created `POST /negotiations` "from a Buyer's bank to a Seller's bank" (§3.2), so the receiving bank is **always** the seller's bank; and "the option pseudo-account is always in the bank of the seller" (§3.6.1). The symmetric `OtcOffer` wire (`{buyerId, sellerId, …}`) carries no `direction` field — direction is a local-only concept of *this* implementation. A `buy_initiated` listing's poster is a **BUYER** wanting to acquire shares, which has **no conformant cross-bank representation**: publishing it would mislabel the buyer-poster as a `sellerId`, and a peer bidding on it would invert the economic roles on accept/exercise. Therefore cross-bank `buy_initiated` bidding is **out of scope of the protocol**, enforced end-to-end:
- **Publish.** `PeerOTCGRPCHandler.GetPublicStocks` (serving `/public-stock`, the sole cross-bank option-discovery surface) reads `ListPublicOptionOffersForPeer`, which returns only `sell_initiated` rows — `buy_initiated` listings are never exposed cross-bank (the local `ListOpenForCache` still returns both for the bank's own marketplace UI). The proprietary `/public-option-offers` serving endpoint + its `GetPublicOptionOffers` RPC were removed 2026-06-11.
- **Ingest.** The discovery poll (`otccache.OptionRefresher.buildAndMirrorRemoteOffers`) drops any peer offer with `Direction == buy_initiated` at the poll boundary (defense vs a non-conformant peer that emits the proprietary `direction` field), so it never becomes a biddable remote listing.
- **Bid.** `openRemoteNegotiation` fails closed (`FailedPrecondition` → HTTP 409) on a remote `buy_initiated` listing with a spec-grounded message; now effectively unreachable because ingest drops such offers, retained as defense-in-depth.

LOCAL `buy_initiated` offers/bids are **fully supported and unaffected** — they never traverse the cross-bank path. The `OtcOffer` wire is unchanged; cohort peers see fewer (seller-only) discovery rows, which is spec-conformant.

### gRPC services

- **`PeerOTCService`** (stock-service): 13 RPCs. (SP-2b clean-cut, 2026-06-05: the 4 dead `RecordOutboundNegotiation` / `ListMyPeerNegotiations` / `MarkNegotiationAccepted` / `CascadeCancelSiblings` RPCs were removed. 2026-06-11: the proprietary `GetPublicOptionOffers` RPC was removed — cross-bank option discovery is `GetPublicStocks` / `/public-stock` only.)
  - Discovery + negotiation lifecycle: `GetPublicStocks`, `CreateNegotiation`, `UpdateNegotiation`, `GetNegotiation`, `DeleteNegotiation`, `AcceptNegotiation`.
  - Seller-share reservation hooks (NEW_TX/rollback): `ReserveSellerSharesForNewTx`, `ReleaseSellerSharesForNewTx`.
  - Money-leg validation / contract lookup: `ValidatePeerOptionMoneyLeg`, `LookupPeerOptionContract` — the latter's response gained `seller_account_number` (field 9, 2.9.0): the seller's nominated 18-digit account number stored on the seller-side contract, used by `posting_executor.reserveExercisePseudoLeg` to credit the strike to the bound account (empty ⇒ first-active fallback).
  - SI-TX option leg materialisation (called by interbank-service; 2026-06-07 cutover — was transaction-service): `RecordOptionContract` — dispatches on transaction SHAPE (OPTION-as-asset → accept; OPTION-as-pseudo-account with STOCK legs → exercise), creates a remote `option_contracts` row (routing_number != own) + locks seller's holdings on accept, transitions to `exercised` + runs role-specific stock ops on exercise. Idempotent on `(crossbank_tx_id, posting_index)`.
  - SI-TX validation hooks (called by interbank-service; 2026-06-07 cutover — was transaction-service): `CheckSellerCanDeliver` — NEW_TX-time pre-check that the seller has enough unreserved shares, drives `INSUFFICIENT_ASSET` `NoVote` so money never moves on a contract the seller can't fulfil.
  - Exercise dispatch (called by gateway): `InitiateOptionExercise` — composes the 4-posting exercise TX from a contract row and dispatches via `interbank-service.PeerTxService.InitiateOutboundTxWithPostings` (2026-06-07 cutover — was transaction-service).

### Unified OTC offer discovery

The unified OTC offer view (local + cross-bank) is served by `stock-service`'s `OTCGRPCService.ListUnifiedOffers`. An in-process refresher goroutine in stock-service rebuilds the cache every ~5 s by reading local offers from `OTCService.ListOffers` and fetching each active peer bank's `/public-stock` (the sole cross-bank option-discovery surface; the proprietary `/public-option-offers` fetch was removed 2026-06-11). As of the 2026-06-07 interbank cutover, stock-service no longer does that HTTP itself: it enumerates peers via `interbank-service.PeerBankAdminService.ListPeerBanks` and fetches each via `interbank-service.PeerEgressService.ProxyToPeer` — peer resolution + `X-Api-Key`/HMAC signing + the actual GET all happen inside interbank-service (the single outbound HTTP egress to permitted peers). The api-gateway's `GET /api/v3/otc/offers` handler is a thin pass-through over this RPC and owns no cache; query params (`security_type`, `ticker`, `kind`, `bank_code`, pagination) map 1-to-1 onto the gRPC request.

### Lifecycle flows

#### Acceptance (`accept`)

`AcceptNegotiation` (stock-service handler) →
1. Look up the negotiation as a REMOTE row in the unified `otc_negotiations` table (via `OTCNegotiationRepository.GetRemoteNegByRoutingAndNative`).
2. Resolve the seller's **nominated** account number (2.9.0): `SellerAccountResolver` (wired via `PeerOTCGRPCHandler.WithSellerAccountResolver`) reads the local parent listing (`RemoteParentRouting == ownRouting` → `RemoteParentNativeID` is the local `OTCOffer` id) and resolves its bound `InitiatorAccountID` to its 18-digit account number when the listing is `sell_initiated`, the account is active, and its currency matches the premium currency. Mirrors the local accept saga's `sellerAccountID = offer.InitiatorAccountID`. When no nomination is resolvable (free-form negotiation with no local parent, unbound account, wrong currency) it returns `""` → the seller-credit leg falls back to the participant id (the documented first-active resolution in `posting_executor.resolveAccountForPosting`).
3. Compose 4 postings (OPTION-as-asset form) — buyer DEBIT premium / seller CREDIT premium / seller DEBIT `OptionDescription` / buyer CREDIT `OptionDescription`. **Both premium money legs carry a pinned `ACCOUNT{num}` when the party nominated one** — the buyer's `BuyerAccountNumber` (set at bid time) on the DEBIT, and the seller's resolved nominated account (step 2) on the CREDIT — spec §2.6 (`TxAccount` may target a specific account). The two OPTION legs ALWAYS carry the participant id (it becomes the contract's `buyer_id`/`seller_id`, used for exercise + `/me/otc/contracts` listing). The `OptionDescription` encodes the option asset; its `negotiationId` field provides the cross-bank reference. On the DEBIT (seller-side) `RecordOptionContract` at COMMIT, the seller's bank persists the resolved nominated account number on the remote contract (`option_contracts.remote_seller_account_number`) so the later exercise strike credit honors it too.
4. Call `interbank-service.PeerTxService.InitiateOutboundTxWithPostings` with `tx_kind="otc-accept"` (2026-06-07 cutover — was transaction-service).
5. The SI-TX flow:
   - `posting_executor.Reserve` (NEW_TX) on each bank validates option-asset postings via `CheckSellerCanDeliver` for DEBIT direction → vote NO with `INSUFFICIENT_ASSET` if seller short.
   - On YES, `cacheAndReturn` persists `peer_idempotence_records.options_json` listing the option items.
   - On COMMIT_TX, `materialiseOptions` calls `PeerOTCService.RecordOptionContract` per option leg → writes a remote `option_contracts` row (routing_number != own) + (DEBIT side) calls `HoldingReservationService.ReserveForPeerOptionContract` to lock seller's shares. If the seller-side lock fails (reservation error or unparseable `seller_id`), `RecordOptionContract` **returns an error** rather than reporting success — leaving an `active` contract with no holding reservation behind it (silent over-promise) is not allowed. The COMMIT then does not ack and retries; both the contract row (idempotent on `crossbank_tx_id, posting_index`) and the reservation (idempotent on `peer_option_contract_id`) are replay-safe, so the lock heals once shares are available.
6. Negotiation status transitions to `accepted`.

> **Concurrency & ownership guards (2026-05-30, found by adversarial testing).**
> - **Accept and exercise are claimed atomically.** `AcceptNegotiation` does a compare-and-set `ongoing → accepted` on the negotiation, and `InitiateOptionExercise` does `active → exercising` on the contract, BEFORE dispatching the SI-TX; a concurrent second call loses the CAS and is rejected (409). Without this, two simultaneous accepts/exercises each charged the buyer (premium / strike) and reserved shares / minted contracts twice — the share legs are row-locked-idempotent but the money legs were not. On a synchronous dispatch failure the claim reverts (so the action stays retryable); the commit-side `recordOptionExercise`/`ExerciseBuyerCreditForPeerOption` accept the transient `exercising` state.
> - **Sender/strike account ownership is enforced gateway-side.** `/me/payments` (cross-bank branch) and the unified exercise route `POST /api/v3/otc/contracts/:id/exercise` (remote-dispatch branch) resolve the caller-supplied account and call `enforceOwnership` before dispatch — a client cannot debit another client's account via a cross-bank payment or an exercise strike. (Negotiation bidder_account_id was already checked.)
> - Business rejections from the dispatch (insufficient seller shares / insufficient buyer funds) preserve their gRPC code → the gateway returns 409, not 500.
> - **The receiver validates an OTC exercise's MONEY legs against its own stored contract (forged-money defense, found 2026-05-30 round 3).** The interbank `/interbank` endpoint is peer-authenticated by a shared API key only, so a buggy/malicious peer can post arbitrary amounts. Previously the share quantity was trusted-from-the-stored-contract (`ConsumeForPeerOptionContract` uses `contract.Quantity`) but the strike money was trusted-from-the-posting — decoupled, enabling three thefts: **(a) forged-low strike** (seller delivers full shares for ~0 money), **(b) buyer-overcharge** (a forged-high strike DEBIT sent to the buyer's bank), **(c) replay** (a second exercise of an already-`exercised` contract debits the buyer the strike again while COMMIT no-ops on delivery). Fix: a new internal gRPC `PeerOTCService.ValidatePeerOptionMoneyLeg(negotiation_routing, negotiation_id, direction, tx_shape, ticker, quantity, strike_price, money_amount, currency) → (ok, reason)` loads the stored `peer_option_contract` by `(negotiation, direction)` and for an exercise-shape TX requires: contract status ∈ {active, exercising} (closes replay), `quantity`/`ticker`/`strike_price` match the stored contract, and `money_amount == StrikePrice × Quantity`. `posting_executor.Reserve` calls it in a **pre-pass before any reservation** for EVERY option leg on this bank's routing (DEBIT = we hold the seller, paired with the money CREDIT; CREDIT = we hold the buyer, paired with the money DEBIT — `pairedMoney` pairs by leg direction on own routing and reports the money leg's actual currency, robust to the participant-id-vs-account-number asymmetry between the two money legs); any mismatch / validator error → `UNACCEPTABLE_ASSET` NO vote with no hold placed. Receiver-side only — NO SI-TX wire-protocol change. **Accept-shape legs** (OPTION-as-asset) are validated too: the contract doesn't exist on the receiver yet, so the validator loads the stored **negotiation** by `foreign_id` (the UUID — unique per bank, identical on both; looked up without `peer_bank_code` because the validator runs on both the coordinator [own-routing peer code] and the receiver [counterparty code]) and requires terms (ticker/quantity/strike) to match the offer + premium == `offer.Premium` when the money leg is in the premium currency. **Residual (low severity):** a cross-currency BUYER premium is FX-converted at the live rate (not recomputable at vote time) so it is only checked > 0 — the SELLER always receives `offer.Premium` in its own currency, so the underpayment-victim side is always exact.

#### Exercise (remote branch of `POST /api/v3/otc/contracts/:id/exercise`)

When the unified exercise route resolves a remote (`kind=remote`) contract, stock-service dispatches the cross-bank exercise. `PeerOTCGRPCHandler.InitiateOptionExercise` →
1. Validate this bank holds the buyer side (`direction=CREDIT`) and contract is `active`.
2. Compose 4 postings using the OPTION-pseudo-account form from the original contract terms:

   **Exercise wire encoding (OPTION-pseudo-account form):**
   | # | Account | Asset | Amount | Description |
   |---|---|---|---|---|
   | 1 | buyer `ACCOUNT` (buyer's money account number) | `MONAS` (strike currency) | −(strikePrice × quantity) | Buyer pays strike |
   | 2 | `OPTION` pseudo-account `{type:"OPTION", id: negotiationId}` | `MONAS` (strike currency) | +(strikePrice × quantity) | Strike credited to seller via pseudo-account. The seller's bank resolves the pseudo-account leg to the seller's **nominated** account (2.9.0): `LookupPeerOptionContract` returns the stored `remote_seller_account_number` and `posting_executor.reserveExercisePseudoLeg` credits that exact account (empty ⇒ first-active fallback). |
   | 3 | `OPTION` pseudo-account `{type:"OPTION", id: negotiationId}` | `STOCK {ticker}` | −quantity | Underlying leaves pseudo-account |
   | 4 | buyer `PERSON` (buyer participant id) | `STOCK {ticker}` | +quantity | Underlying delivered to buyer |

   An **OPTION pseudo-account** is a `TxAccount` of `type="OPTION"` whose `id` is the `negotiationId` (`ForeignBankId`). It is NOT an `OptionDescription` asset — the STOCK legs are what distinguish this from an accept TX. The receiver identifies an exercise by **transaction shape**: OPTION-as-account with STOCK legs ⇒ exercise; OPTION-as-asset (`OptionDescription`) ⇒ accept. No `intent` flag is present on the wire.

   **Receiver-side settlement rule:** a bank settles the OPTION pseudo-account legs of an exercise if and only if it holds the **seller-side** option contract for that `negotiationId` (ownership-by-contract, not routing-prefix). Vote `NO` reasons specific to option exercise: `OPTION_NEGOTIATION_NOT_FOUND` (no matching contract for the `negotiationId`), `OPTION_USED_OR_EXPIRED` (contract already exercised or `settlement_date` has passed), `OPTION_AMOUNT_INCORRECT` (strike money amount ≠ `strikePrice × quantity`).

3. Dispatch via `InitiateOutboundTxWithPostings` (`tx_kind="otc-exercise"`).
4. On COMMIT_TX, `RecordOptionContract`'s exercise branch:
   - DEBIT side: `ConsumeForPeerOptionContract` settles the reservation and decrements seller's holding. It is idempotent on a synthetic settlement txn id; a replay returns `AlreadySettled=true` so the handler **skips** the realised-`CapitalGain` write (which is not idempotent) and avoids double-counting P/L. Then `SetStatus(exercised)`.
   - CREDIT side: `ExerciseBuyerCreditForPeerOption` credits the buyer's holding **and** flips the contract to `status=exercised` in a single transaction, with the contract status read under a row lock as the idempotency guard. A replayed exercise (duplicate COMMIT_TX) finds `status=exercised` and is a no-op, so the buyer's shares are never double-credited. If the credit fails (or `buyer_id` is unparseable), it **returns an error and does not mark the contract exercised** — the buyer has paid the strike, so a silent failure would leave them paid-but-undelivered; the SI-TX exercise commit retries instead.

**Option legs carry participant ids, money legs carry account numbers (fixed 2026-05-30).** `AcceptNegotiation` composes the buyer DEBIT premium leg with the buyer's pinned **account number** (`OtcOffer.BuyerAccountNumber`, so the executor debits the exact account) but the buyer CREDIT **option** leg with the buyer **participant id** (`row.BuyerID` = `client-<n>`). That participant id becomes the minted contract's `buyer_id`, which (a) the exercise CREDIT branch (`ExerciseBuyerCreditForPeerOption`) parses to resolve the owner and credit the buyer's holding (a scaffolding row with `security_id=0/listing_id=0` when the buyer's bank doesn't list the security), and (b) `ListByLocalParticipant` matches for the `/me/otc/contracts` listing. (Previously the option leg also used the account number — an unparseable `buyer_id` that broke exercise credit AND hid the contract from the buyer's listing.) The exercise composition's option legs likewise carry participant ids. **Inline commit atomicity:** `InitiateOutboundTxWithPostings` marks the row `committed` ONLY when local commit + settle + option-materialise + `PostCommitTx` all succeed; any failure leaves it `pending` so `OutboundReplayCron` retries via `CommitOutboundLocal`, which re-materialises the sender-side option legs (`PostingExecutor.ExtractOwnOptionItems`) — so a sender-side contract that failed to materialise inline is recoverable. Verified live on two stacks: a full accept→exercise round is clean on both sides (buyer pays premium+strike and receives the holding, contract `exercised` + listed; seller credited, shares delivered, reservation released, no orphans).

#### Expiry (cron)

`OTCExpiryCron` runs daily at 02:00 UTC (and once on stock-service startup to catch up missed runs). For each remote `option_contracts` row (routing_number != own) with `status='active'` and `settlement_date < today`:
- DEBIT direction (seller's bank): `ReleaseForPeerOptionContract` releases the reservation; shares unlock.
- CREDIT direction (buyer's bank): no holding op.
- Both: row → `status=expired`. Seller keeps the premium (no money movement).

#### Safety-net negotiation reconciler (SP-1 Task 9, 2026-06-05)

`PeerOTCNegotiationReconciler` is a background goroutine (`service.NewPeerOTCNegotiationReconciler`) that runs every **2 minutes** in stock-service. It polls each active peer bank's `GET /api/v3/cross-bank-protocol/negotiations/{rid}/{id}` for every REMOTE `ongoing` row (from `OTCNegotiationRepository.ListRemoteNegOngoing`) whose authoritative bank is the PEER (identified by `peerRoutingForRow`: whichever of `RemoteBuyerRouting` / `RemoteSellerRouting` does not equal `ownRouting`). When the peer reports `isOngoing: false` (accepted, cancelled, expired, or any terminal state), and the local remote row is still `ongoing`, the reconciler flips it to `status=cancelled` via `OTCNegotiationRepository.UpdateRemoteNegStatus` (same path as the inbound DELETE webhook) and emits a best-effort `OTC_OFFER_CANCELLED` in-app notification to the local party if one can be resolved.

**False-cancel guard:** any transport error, non-2xx HTTP status, or JSON parse failure on the poll causes the row to be **skipped** for that tick. The reconciler never cancels on ambiguous data. Intra-bank rows (both `buyer_routing_number` and `seller_routing_number` equal `ownRouting`) are also skipped (no peer to query).

**Cronreg integration:** the reconciler is registered as `"peer-otc-neg-reconciler"` in the stock-service cron registry, so operators can pause, resume, or manually trigger it via `GET /api/v3/admin/crons/stock-service/peer-otc-neg-reconciler`.

**Implementation:** `stock-service/internal/service/peer_otc_reconciler.go`. `OTCNegotiationRepository.ListRemoteNegOngoing()` returns all REMOTE `ongoing` rows for the poll loop.

### Database tables

- **`peer_otc_negotiations`** — **RETIRED (SP-2a, 2026-06-05): folded into the unified `otc_negotiations` table as REMOTE rows** (`routing_number=<peer routing>`, `native_id=<peer foreign negotiation id>`; the `(routing_number, native_id)` UNIQUE is the natural key; local rows have `routing_number=OwnRouting()`). Remote-only columns added to `otc_negotiations`: `remote_offer_json`, `remote_buyer_routing`, `remote_buyer_id`, `remote_seller_routing`, `remote_seller_id`, `remote_parent_routing`, `remote_parent_native_id`. The standalone model + repository are deleted; all inbound webhooks, the reconciler, and the SP-1 read-merge use `OTCNegotiationRepository`'s remote methods. No data migration (fresh start).
- **`peer_option_contracts`** — **RETIRED (SP-2a, 2026-06-05): folded into the unified `option_contracts` table as REMOTE rows.** A remote contract is an `OptionContract` with `routing_number=<counterparty routing>` (the side this bank does NOT host: CREDIT→counterparty=seller's bank, DEBIT→counterparty=buyer's bank) and `native_id="<crossbank_tx_id>:<posting_index>"` (the retired natural key preserved inside `native_id`, so the `(routing_number, native_id)` UNIQUE keeps `UpsertRemoteContract` idempotent; local rows have `routing_number=OwnRouting()`). The autoincrement `OptionContract.id` is the stable local surrogate id surfaced to the frontend on SP-1 read routes and used as the `holding_reservations.peer_option_contract_id` reference. Remote-only nullable columns added to `OptionContract`: `remote_posting_index`, `remote_negotiation_routing` + `remote_negotiation_native_id` (the originating negotiation reference; indexed together; the exercise / money-leg validators look the contract up by these + `remote_direction`), `remote_direction` (`DEBIT`|`CREDIT`, size 8), `remote_buyer_id` + `remote_seller_id` (SI-TX participant ids `"client-<N>"`|`"bank"`, size 128), and `remote_seller_account_number` (size 34, nullable, 2.9.0 — the seller's nominated 18-digit account number stored on a DEBIT/seller-side row at accept-COMMIT; the exercise strike credit reads it back via `LookupPeerOptionContract`; NULL ⇒ first-active fallback). The buyer/seller routings are stored in the existing `buyer_bank_code`/`seller_bank_code` columns (as strings); the rest of the terms ride the shared columns: `quantity` (int amount as decimal; `IntPart()` round-trips it), `strike_price`, `ticker`, `strike_currency` (the option currency), `settlement_date` (parsed `time.Time`; re-formatted RFC3339-UTC on read — the instant is preserved for the `optionExpired` check), `crossbank_tx_id`, and `status` (the PEER vocabulary `active`|`exercising`|`exercised`|`expired` as-is on remote rows). A remote row satisfies the model's NOT-NULL / CHECK / `ValidateOwner` constraints with `buyer_owner_type=seller_owner_type="bank"` + nil owner ids, `offer_id=NULL`, `premium_paid=0`, `premium_currency=strike_currency`, `buyer_account_id=seller_account_id=0`, `saga_id=crossbank_tx_id`, `premium_paid_at=created_at`. `OptionContractRepository` gained remote-scoped methods (all scoped `routing_number != OwnRouting()`): `UpsertRemoteContract` (ON CONFLICT DO NOTHING on the natural key, loads back the surrogate id), `GetRemoteContractByNegotiationAndDirection`, `GetRemoteContractByID` (NotFound for a local id), `SetRemoteContractStatus` + `CompareAndSetRemoteContractStatus` (SkipHooks guarded status flips; the CAS is the exercise-claim concurrency control), `HasRemoteContractForNegotiation` (reconciler accepted-vs-cancelled discriminator), `ListRemoteContractsExpiring` (daily cron cross-bank pass), and `ListRemoteContractsByLocalParticipant` (SP-1 `/me/otc/contracts` remote merge). The routing-guarded local-only queries (`GetByID`/`GetByOfferID`/`ListExpiring`) exclude remote rows via their `routing_number == OwnRouting()` filter. The standalone `PeerOptionContract` model + `PeerOptionContractRepository` are deleted; `peer_otc_grpc_handler.go`, the expiry cron, the reconciler, the stale-reservation scanner, and the SP-1 contract reads all use `OptionContractRepository`'s remote methods. No data migration (fresh start). Cross-bank WIRE + SI-TX exercise behaviour unchanged.
- **`holding_reservations`** — extended with `peer_option_contract_id` (third optional FK alongside `order_id` and `otc_contract_id`). DB CHECK constraint `holding_reservation_owner_chk` enforces "exactly one of three" non-NULL.
- **`peer_idempotence_records`** — extended with `options_json` column (in addition to `debits_json`). Persists option items at NEW_TX vote-YES so COMMIT_TX can materialise them without depending on the original postings list.

### SI-TX wire types

Defined in `contract/sitx/otc_types.go`. Spec-conforming shapes (per cohort spec at <https://arsen.srht.site/si-tx-proto/>):

- `ForeignBankId` — `(routingNumber, id)` tuple.
- `OtcOffer` — `stock`, `settlementDate`, `pricePerUnit`, `premium`, `buyerId`, `sellerId`, `amount`, `lastModifiedBy`. (Internal storage is a flat-fielded variant; the gateway translates between the spec wire shape and internal gRPC.)
- `OtcNegotiation` — `OtcOffer & {isOngoing: boolean}`.
- `OptionDescription` — used as a posting `assetId` (JSON-encoded) for the **accept** TX (OPTION-as-asset form). Spec-conforming nested shape:
  ```
  OptionDescription = {
    negotiationId:   ForeignBankId,          // { routingNumber: number, id: string }
    stock:           { ticker: string },
    pricePerUnit:    { amount: number, currency: string },  // strike per share
    settlementDate:  string,                 // ISO 8601
    amount:          number                  // quantity (share count)
  }
  ```
  The old flat fields (`ticker`, `strikePrice`, `currency` at top level) and the non-spec `intent` extension field were removed from the wire as of 2026-06-02 (see `docs/superpowers/specs/2026-06-02-sitx-option-wire-conformance-design.md`). Exercise TXs do NOT use `OptionDescription` — they use the OPTION-pseudo-account form (see Exercise lifecycle above). The accept-vs-exercise distinction is derived entirely from transaction shape, never from a wire `intent` flag.
- `UserInformation` — response shape of `GET /user/{rid}/{id}`.
- `PublicStocksResponse` + `PublicStock` — response shape of `GET /public-stock`.

### Atomicity guarantees

Per Celina 5 §"Plaćanja" (*"u celosti, ili ne uopšte"*):
- NEW_TX-time pre-check: insufficient seller holdings → vote NO before any money moves.
- Money DEBIT legs are **reserve-then-settle** (not immediate-debit): the hold dips AvailableBalance at NEW_TX, settles Balance at COMMIT_TX, and is released on NO/ROLLBACK/timeout — both the sender's own leg (`peer-out:<idem>`) and receiver-side legs (per-posting `DebitsJSON` keys).
- Option contract materialisation happens at COMMIT_TX (never at NEW_TX), so a rolled-back TX leaves no contract row.
- **Exercise does not re-reserve seller shares.** The NEW_TX-time share hold (`ReserveSellerSharesForNewTx`) fires only for accept-shape TXs (OPTION-as-asset / `OptionDescription` postings); an exercise-shape TX (OPTION-pseudo-account with STOCK legs) skips the reserve (the shares were already held at accept and are consumed at COMMIT by `RecordOptionContract`) — otherwise the exercise would orphan a second hold that permanently locks the shares.
- Holding reservations use composite-unique indexes for idempotent retry.

### Out of scope

- ~~Bank-side OTC participation across banks~~ — **DELIVERED in SP-3 (2026-06-05).** An employee acting as the bank is now a first-class cross-bank OTC principal: bank-owned offers publish `employee-<ActingEmployeeID>` on the SI-TX wire (biddable by peers), and the bank can bid/counter/accept/reject/cancel/exercise cross-bank against BANK accounts/holdings (sentinel `1000000000`). The unified bid path (`POST /api/v3/otc/options/:id/bid` against a remote listing) now publishes `buyerId=employee-<N>` for the bank principal (no longer a 409); the wire id is stable per-resource via the `acting_employee_id` column; the bank sees its own remote chains in all read views; inbound `employee-<N>` party ids parse to bank ownership; the exercise strike account is gated (gateway `ResolveAndCheckAccountByNumber` + stock-service bank re-assert).
- Cross-bank currency conversion at exercise — buyer must hold the strike currency directly; cross-currency strikes would need exchange-service plumbing through the SI-TX path.
- HMAC outbound auth has been wired but not exercised end-to-end with another team's bank.
