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
| api-gateway | auth, user, client, account, card, transaction, credit, exchange, verification, notification, stock (StockExchange / Security / Order / Portfolio / OTC / Tax / SourceAdmin / **InvestmentFund** (Celina 4) / **OTCOptions** (Spec 2)) |
| stock-service | account-service (debit/credit/reservations/bank-account), exchange-service (FX), user-service (employee names + actuary limits), client-service (client name resolution), **transaction-service (Spec 3 InterBankService for cross-bank Phase 3 + ReverseInterBankTransfer)** |
| auth-service | user-service (employee lookup), client-service (client login) |
| user-service | auth-service (activation tokens) |
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
| `clientLimitClient` | ClientLimitService | `CLIENT_GRPC_ADDR` (shared) |
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
| `/ws/mobile` | WebSocket auth (JWT + X-Device-ID) | Mobile device |

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
| otc | `otc.trade.accept` |
| funds | `funds.read.all`, `funds.manage.catalog` |
| orders | `orders.place.on_behalf_client`, `orders.place.on_behalf_bank`, `orders.read.all`, `orders.cancel.all` |
| verification | `verification.skip`, `verification.manage` |

### Role Definitions

| Role | Inherits Permissions |
|---|---|
| EmployeeBasic | clients.*, accounts.*, cards.*, payments.read, credits.read |
| EmployeeAgent | EmployeeBasic + securities.*, otc.trade, orders.place-on-behalf, orders.place.on-behalf-client, orders.place.for-bank |
| EmployeeSupervisor | EmployeeAgent + agents.manage, otc.manage, funds.manage, funds.bank-position-read, verification.skip, verification.manage |
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
| `account/account.proto` | `AccountService`, `BankAccountService` | 14 + 6 |
| `card/card.proto` | `CardService`, `VirtualCardService`, `CardRequestService` | 9 + 5 + 6 |
| `transaction/transaction.proto` | `TransactionService`, `FeeService`, `InterBankService` (Spec 3 + Spec 4 `ReverseInterBankTransfer`) | 13 + 5 + 5 |
| `credit/credit.proto` | `CreditService` | 16 |
| `exchange/exchange.proto` | `ExchangeService` | 4 |
| `notification/notification.proto` | `NotificationService` | 2 |
| `stock/stock.proto` | `SecurityGRPCService`, `OrderGRPCService`, `PortfolioGRPCService`, `OTCGRPCService`, `SourceAdminService`, **`InvestmentFundService`** (Celina 4, 9 RPCs), **`OTCOptionsService`** (Spec 2, 9 RPCs), **`CrossBankOTCService`** (Spec 4, 12 RPCs) | (see below) |

**account-service BankAccountService additions:**

`BankAccountService` — two new RPCs for loan disbursement saga:
- `DebitBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse)` — atomically debits the bank sentinel account for a given currency, with idempotency keyed on `reference + direction`.
- `CreditBankAccount(BankAccountOpRequest) returns (BankAccountOpResponse)` — atomically credits the bank sentinel account for a given currency, with idempotency keyed on `reference + direction`.

**account-service AccountService reservation RPCs (Phase 2 securities settlement):**

Four new RPCs on `AccountService` back the securities-order reservation system. All run inside `SELECT FOR UPDATE` transactions on the target account.

- `ReserveFunds(account_id, order_id, amount, currency_code) returns (reservation_id, reserved_balance, available_balance)` — creates an `AccountReservation`, increments `Account.ReservedBalance`, decrements `Account.AvailableBalance`. Idempotent on `order_id`: a retry with the same `order_id` returns the existing reservation. Returns `FailedPrecondition` on currency mismatch or insufficient available balance.
- `ReleaseReservation(order_id) returns (released_amount, reserved_balance)` — transitions the reservation to `released`, rolls `ReservedBalance` back, restores `AvailableBalance`. No-op (with empty response) if the reservation is missing, already released, or already settled.
- `PartialSettleReservation(order_id, order_transaction_id, amount, memo) returns (settled_amount, remaining_reserved, balance_after, ledger_entry_id)` — settles part (or all) of a reservation against a specific fill. Writes a `LedgerEntry` so the fill appears in transaction history, debits `Balance`, decrements `ReservedBalance`, and when the reservation is fully consumed transitions status to `settled`. Idempotent on `order_transaction_id` via a unique index on `AccountReservationSettlement.order_transaction_id`.
- `GetReservation(order_id) returns (exists, status, amount, settled_total, settled_transaction_ids)` — read-only; used by stock-service saga recovery to determine which fill saga steps already committed on account-service.

**stock-service gRPC additions:**

`PortfolioGRPCService` — portfolio operations including option exercise:
- `ExerciseOptionByOptionID(ExerciseOptionByOptionIDRequest) returns (ExerciseResult)` — exercises an option by option ID instead of holding ID. Fields: `option_id uint64` (required), `user_id uint64` (required), `holding_id uint64` (optional; 0 means auto-resolve to the user's most recent unexpired holding for that option).

`OrderGRPCService` — `CreateOrder` and `BuyOTCOffer` RPCs accept two new optional fields: `acting_employee_id` (uint64, employee placing the trade on behalf of a client; 0 means the caller is the client) and `on_behalf_of_client_id` (uint64, the client being traded for; 0 means the caller is trading for themselves). The gateway sets these fields when an employee uses the `POST /api/v3/orders` or `POST /api/v3/otc/offers/:id/buy-on-behalf` endpoints.

`SourceAdminService` — destructive data-source management:
- `SwitchSource(SwitchSourceRequest) returns (SwitchSourceResponse)` — switches the active stock data source. Request field: `source string` (one of `external`, `generated`, `simulator`). Response wraps a `SourceStatus` message.
- `GetSourceStatus(GetSourceStatusRequest) returns (SourceStatus)` — returns the current source name and switch status. `SourceStatus` fields: `source string`, `status string` (`idle` | `reseeding` | `failed`), `started_at string` (RFC3339), `last_error string`.

**Key pattern:** When a proto file has multiple services (e.g., `CardService` + `VirtualCardService` + `CardRequestService`), they all run in the same microservice process on the same port but are registered as separate gRPC services. The API Gateway creates separate client instances that share the same connection address.

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
| POST | `/api/v3/otc/offers/:id/buy-on-behalf` | otc.trade.accept or orders.place-on-behalf | otcHandler.BuyOTCOfferOnBehalf | Employee buys OTC offer on behalf of a named client; gateway verifies account belongs to client (mismatch → 403) |
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

| Method | Path | Handler | Description |
|---|---|---|---|
| GET | `/ws/mobile` | wsHandler.HandleConnect | Mobile WebSocket connection |

### Swagger

| Method | Path | Description |
|---|---|---|
| GET | `/swagger/*any` | Swagger UI |

---

## 18. Complete Entity Reference

> **New feature entities:** Investment-fund entities are catalogued in [§24](#24-investment-funds-celina-4). Intra-bank OTC option entities (`OTCOffer`, `OTCOfferRevision`, `OptionContract`, `OTCOfferReadReceipt`) are in [§26](#26-intra-bank-otc-options-celina-4--spec-2). Cross-bank OTC additions (`InterBankSagaLog`; `OTCOffer.Public/Private`; `OptionContract.CrossbankTxID/CrossbankExerciseTxID`; `HoldingReservation.OTCContractID`) are in [§27](#27-cross-bank-otc-options-celina-5--spec-4--foundation). The `Order` model gained a `FundID *uint64` column for on-behalf-of-fund order placement.

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
ID(uint64), AccountID(uint64,indexed), OrderID(uint64,unique), Amount(numeric18,4),
CurrencyCode(3), Status(active|released|settled,indexed), CreatedAt, UpdatedAt, Version(int64)
```
`OrderID` is the idempotency key — retrying `ReserveFunds` with the same `order_id` is a safe no-op. `Amount` is immutable after insert; only `Status` transitions.

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
AveragePrice(numeric 18,8), PublicQuantity(int64), ReservedQuantity(int64,default:0),
AccountID(uint64), Version(int64), CreatedAt, UpdatedAt
```
`OwnerType`+`OwnerID` replaces the pre-Task-4 (UserID, SystemType) pair (plan 2026-04-27-owner-type-schema.md). Bank-owned holdings have `OwnerType="bank"` with `OwnerID IS NULL`; client-owned holdings have `OwnerType="client"` with a non-null `OwnerID`. The `BeforeSave` hook calls `model.ValidateOwner` to enforce the invariant. Unique index `idx_holding_per_owner_security` keys on `(owner_type, COALESCE(owner_id, 0), security_type, security_id)` so each (owner, security) pair rolls up to a single row.

`ReservedQuantity` is the running total of units locked by active sell-side `HoldingReservation` rows. `AvailableQuantity = Quantity - ReservedQuantity`. Sell orders are rejected at placement if `AvailableQuantity` is insufficient; filled sells decrement both `Quantity` and `ReservedQuantity` atomically.

**HoldingReservation** — Quantity-based mirror of `AccountReservation`. Locks shares on a holding for the duration of a sell order. Immutable except for `Status`/`Version`.
```
ID(uint64), HoldingID(uint64,indexed), OrderID(uint64,unique), Quantity(int64),
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

**SystemSetting** — Global key-value configuration (key = primary key)
```
Key(string, PK, size:64), Value(string)
```
`system_settings.active_stock_source` — persists the currently active stock data source
(`external`, `generated`, or `simulator`) across service restarts.

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
| `user.client-limits-updated` | user-service | (consumers) | ClientLimitsUpdatedMessage |
| `user.role-permissions-changed` | user-service | auth-service | RolePermissionsChangedMessage |
| `client.created` | client-service | notification-service | ClientCreatedMessage |
| `client.updated` | client-service | (consumers) | (generic) |
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
| `notification.mobile-push` | notification-service | api-gateway | MobilePushMessage |
| `notification.general` | account/card/credit/auth/transaction-service | notification-service | GeneralNotificationMessage |
| `stock.order-created` | stock-service | (consumers) | OrderCreatedMessage |
| `stock.order-approved` | stock-service | (consumers) | OrderApprovedMessage |
| `stock.order-declined` | stock-service | (consumers) | OrderDeclinedMessage |
| `stock.order-filled` | stock-service | (consumers) | OrderFilledMessage (payload below) |
| `stock.order-cancelled` | stock-service | (consumers) | OrderCancelledMessage |

### General Notification Types

Published to `notification.general` by various services. notification-service consumes and stores as persistent user notifications (no email, no expiry).

| Type | Source | Trigger |
|---|---|---|
| `account_created` | account-service | Account created |
| `card_issued` | card-service | Card created |
| `card_blocked` | card-service | Card blocked |
| `money_sent` | transaction-service | Payment/transfer executed (sender side) |
| `money_received` | transaction-service | Payment/transfer executed (receiver side) |
| `loan_approved` | credit-service | Loan request approved |
| `loan_rejected` | credit-service | Loan request rejected |
| `password_changed` | auth-service | Password reset completed |

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

### 21.1 gRPC Error Sentinels

Service errors carry typed sentinels defined in `<service>/internal/service/errors.go`. Each sentinel embeds a gRPC code via `contract/shared/svcerr.SentinelError`, so wrapping with `fmt.Errorf("Op: %w", sentinel)` automatically resolves to the correct wire status code via `status.FromError`.

Handlers do NOT map errors — they return wrapped service errors directly. The `contract/shared/grpcmw.UnaryLoggingInterceptor` records the full wrap chain (with the original underlying error) for any non-OK response, before the wire status is sent.

The api-gateway maps gRPC status codes to HTTP via `api-gateway/internal/handler/validation.go:grpcToHTTPError`. Distinct sentinels surface as distinct HTTP error codes, so a client can distinguish "wrong password" (401 unauthorized) from "account locked" (403 forbidden) from "account pending" (409 business_rule_violation), etc.

Email-not-found and bcrypt-mismatch deliberately collapse to the same `ErrInvalidCredentials` sentinel for security (prevents email enumeration). All other failure modes are distinct.

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
| POST | `/api/v3/otc/offers` | AnyAuthMiddleware + RequireAllPermissions(`securities.trade`,`otc.trade`) | OTCOptionsHandler.CreateOffer | Create OTC option offer (Spec 2) |
| POST | `/api/v3/otc/offers/:id/counter` | AnyAuthMiddleware + RequireAllPermissions(`securities.trade`,`otc.trade`) | OTCOptionsHandler.CounterOffer | Counter offer terms |
| POST | `/api/v3/otc/offers/:id/accept` | AnyAuthMiddleware + RequireAllPermissions(`securities.trade`,`otc.trade`) | OTCOptionsHandler.AcceptOffer | Accept offer (premium-payment saga; cross-bank dispatches via Spec 4) |
| POST | `/api/v3/otc/offers/:id/reject` | AnyAuthMiddleware + RequireAllPermissions(`securities.trade`,`otc.trade`) | OTCOptionsHandler.RejectOffer | Reject offer |
| POST | `/api/v3/otc/contracts/:id/exercise` | AnyAuthMiddleware + RequireAllPermissions(`securities.trade`,`otc.trade`) | OTCOptionsHandler.ExerciseContract | Exercise option (cross-bank dispatches via Spec 4) |
| GET | `/api/v3/otc/offers/:id` | AnyAuthMiddleware | OTCOptionsHandler.GetOffer | Offer detail with revisions |
| GET | `/api/v3/otc/contracts/:id` | AnyAuthMiddleware | OTCOptionsHandler.GetContract | Contract detail |
| GET | `/api/v3/me/otc/offers` | AnyAuthMiddleware | OTCOptionsHandler.ListMyOffers | Caller's OTC offers |
| GET | `/api/v3/me/otc/contracts` | AnyAuthMiddleware | OTCOptionsHandler.ListMyContracts | Caller's OTC contracts |

## 24. Investment Funds (Celina 4)

### Entities (stock-service)

| Entity | Table | Purpose |
|---|---|---|
| `InvestmentFund` | `investment_funds` | Supervisor-managed pool. One bank-owned RSD account, manager_employee_id, minimum contribution. Optimistic locking via Version. |
| `ClientFundPosition` | `client_fund_positions` | One row per (fund, owner). Owner identified by (`OwnerType`, `OwnerID`) — `bank` with `OwnerID IS NULL` for the bank's own stake, `client` with non-null `OwnerID` for clients. (Renamed from the pre-Task-4 `(UserID=1_000_000_000, SystemType="employee")` sentinel pattern by plan 2026-04-27-owner-type-schema.md.) TotalContributedRSD accumulates contributions and decrements on redeem. |
| `FundContribution` | `fund_contributions` | Append-mostly history of every invest/redeem event. Owner identified by (`OwnerType`, `OwnerID`); status pending → completed/failed under the saga that produced it. SagaID is a UUID string referencing saga_logs. |
| `FundHolding` | `fund_holdings` | Fund-side analogue of Holding. Increments on on-behalf-of-fund order fills, decrements on liquidation. FIFO order-by created_at for liquidation. |
| `Order.FundID` | `orders.fund_id` | New optional column. Non-nil when the order was placed on behalf of a fund — `OwnerType="bank"`/`OwnerID IS NULL` and fills credit `fund_holdings` instead of `holdings`. |

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

## 25. Inter-Bank Cross-Bank Communication (Celina 5) — Pending SI-TX Implementation

The previous in-house 2PC inter-bank transfer protocol (HMAC headers, `Prepare` / `Ready` / `NotReady` / `Commit` / `Committed` / `CheckStatus` action enum, three split routes under `/internal/inter-bank/*`, status state machine, reconciler cron) was removed in the Phase 1 demolition of the SI-TX refactor.

The replacement implementation conforms to the SI-TX cohort wire protocol referenced by Celina 5 (`https://arsen.srht.site/si-tx-proto/`) and is being built in subsequent phases:

- **Phase 2 (foundation):** `Message<Type>` envelope, hybrid `X-Api-Key` / HMAC peer auth middleware, `peer_banks` registry table, `peer_idempotence_records` replay cache. Skeleton `POST /api/v3/interbank` (501). Admin CRUD on peer_banks via `/api/v3/peer-banks` routes.
- **Phase 3 (TX execution):** `NEW_TX` / `COMMIT_TX` / `ROLLBACK_TX` posting executor, vote builder with the 8 SI-TX `NoVote` reasons, sender-side `outbound_peer_txs` + replay cron. Restores inter-bank transfer functionality.
- **Phase 4 (OTC negotiations):** `GET /api/v3/public-stock`, the 5 `/api/v3/negotiations/{rid}/{id}` routes, `GET /api/v3/user/{rid}/{id}`. Restores cross-bank OTC functionality.

Design doc: `docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md`. Phase 1 plan: `docs/superpowers/plans/2026-04-29-celina5-sitx-phase1-demolition.md`.

During this transition `POST /api/v3/me/transfers` works for intra-bank receivers (own 3-digit prefix). Foreign-prefix receivers receive `501 not_implemented` from `PeerDisabledHandler`.

## 26. Intra-bank OTC Options (Celina 4 / Spec 2)

### Entities (stock-service)

| Entity | Table | Purpose |
|---|---|---|
| `OTCOffer` | `otc_offers` | One negotiation thread between two parties on a stock-option contract. Carries direction, stock_id, qty, strike, premium, settlement_date, status. Initiator + counterparty identified by (`InitiatorOwnerType`, `InitiatorOwnerID`) / (`CounterpartyOwnerType`, `CounterpartyOwnerID`); `LastModifiedByPrincipalType`/`LastModifiedByPrincipalID` records the actor (principal) of the latest revision. (Renamed from the pre-Task-4 `(user_id, system_type)` triples by plan 2026-04-27-owner-type-schema.md.) Optimistic-locked. |
| `OTCOfferRevision` | `otc_offer_revisions` | Append-only history of every CREATE/COUNTER/ACCEPT/REJECT action on an offer. Carries `ModifiedByPrincipalType`/`ModifiedByPrincipalID` (the principal who issued the revision, not the resource owner). (offer_id, revision_number) is unique. |
| `OptionContract` | `option_contracts` | The premium-paid executed option produced by the accept saga. Buyer + seller identified by (`BuyerOwnerType`, `BuyerOwnerID`) / (`SellerOwnerType`, `SellerOwnerID`); status ∈ {ACTIVE, EXERCISED, EXPIRED, FAILED}. |
| `OTCOfferReadReceipt` | `otc_offer_read_receipts` | Composite-PK row tracking the most recent updated_at the owner has seen for an offer. PK is (`OwnerType`, `OwnerID`, `OfferID`); bank readers materialise as `OwnerID=0` because Postgres disallows NULL in primary keys. Drives the `unread` flag. |
| `HoldingReservation.OTCContractID` | `holding_reservations.otc_contract_id` | New nullable column. Either OrderID or OTCContractID is set; CHECK constraint enforces the XOR. |

### Permissions

- `otc.trade` (new) — required for create/counter/accept/reject/exercise. Granted to `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin` (which already have `securities.trade`).

### gRPC service: `OTCOptionsService`

Defined in `contract/proto/stock/stock.proto`. RPCs: CreateOffer, ListMyOffers, GetOffer, CounterOffer, AcceptOffer, RejectOffer, ListMyContracts, GetContract, ExerciseContract.

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

## 27. Cross-Bank OTC Options (Celina 5) — Pending SI-TX Implementation

The previous in-house cross-bank OTC saga (12-RPC `CrossBankOTCService`, `CrossbankAcceptSaga` / `CrossbankExerciseSaga` / `CrossbankExpireSaga`, `InterBankSagaLog` durable audit table, `CrossbankCheckStatusCron` / `CrossbankOrphanReservationCron` / `CrossbankExpiryCron`) was removed in the Phase 1 demolition of the SI-TX refactor.

The SI-TX-conformant replacement is built in Phase 4 of the refactor (see §25). It exposes peer-facing OTC endpoints at:

- `GET /api/v3/public-stock` — peer OTC discovery
- `POST /api/v3/negotiations` — initiate cross-bank offer
- `PUT /api/v3/negotiations/{rid}/{id}` — counter-offer
- `GET /api/v3/negotiations/{rid}/{id}` — read
- `DELETE /api/v3/negotiations/{rid}/{id}` — cancel
- `GET /api/v3/negotiations/{rid}/{id}/accept` — accept (triggers SI-TX TX formation via `POST /api/v3/interbank` `NEW_TX`)
- `GET /api/v3/user/{rid}/{id}` — user info

Design doc: `docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md`.

During this transition cross-bank OTC is unavailable; intra-bank OTC (Celina 4 / §26) continues to work.
