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
| api-gateway | auth, user, client, account, card, transaction, credit, exchange, verification, notification |
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
  "user_id": 123,
  "email": "user@example.com",
  "roles": ["EmployeeBasic"],
  "permissions": ["clients.read", "accounts.read"],
  "system_type": "employee",
  "device_type": "",
  "device_id": "",
  "jti": "uuid",
  "iat": 1234567890,
  "exp": 1234567890
}
```

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

### Permission Codes

| Category | Permissions |
|---|---|
| clients | `clients.create`, `clients.read`, `clients.update` |
| accounts | `accounts.create`, `accounts.read`, `accounts.update` |
| cards | `cards.create`, `cards.read`, `cards.update`, `cards.approve` |
| payments | `payments.read` |
| credits | `credits.read`, `credits.approve` |
| securities | `securities.trade`, `securities.read` |
| employees | `employees.create`, `employees.update`, `employees.read`, `employees.permissions` |
| limits | `limits.manage` |
| admin | `bank-accounts.manage`, `fees.manage`, `interest-rates.manage` |
| agent/otc | `agents.manage`, `otc.manage`, `funds.manage` |
| verification | `verification.skip`, `verification.manage` |

### Role Definitions

| Role | Inherits Permissions |
|---|---|
| EmployeeBasic | clients.*, accounts.*, cards.*, payments.read, credits.read |
| EmployeeAgent | EmployeeBasic + securities.* |
| EmployeeSupervisor | EmployeeAgent + agents.manage, otc.manage, funds.manage, verification.skip, verification.manage |
| EmployeeAdmin | All permissions |

### Context Values Set by Middleware

After middleware runs, these are available via `c.GetXxx()`:

```go
c.GetInt64("user_id")      // Principal ID (employee or client)
c.GetString("email")       // Email
c.GetString("role")        // Primary role name
c.GetString("system_type") // "employee" or "client"
// "roles" and "permissions" are set as string slices
```

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

### Existing gRPC Service Definitions (15 services across 9 proto files)

An agent extending an existing service needs to know which gRPC services already exist:

| Proto File | gRPC Services | RPC Count |
|---|---|---|
| `auth/auth.proto` | `AuthService` | 11 |
| `user/user.proto` | `UserService`, `EmployeeLimitService` | 11 + 5 |
| `client/client.proto` | `ClientService`, `ClientLimitService` | 5 + 2 |
| `account/account.proto` | `AccountService`, `BankAccountService` | 14 + 4 |
| `card/card.proto` | `CardService`, `VirtualCardService`, `CardRequestService` | 9 + 5 + 6 |
| `transaction/transaction.proto` | `TransactionService`, `FeeService` | 13 + 5 |
| `credit/credit.proto` | `CreditService` | 16 |
| `exchange/exchange.proto` | `ExchangeService` | 4 |
| `notification/notification.proto` | `NotificationService` | 2 |

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
| GET | `/api/v1/me/notifications` | - | notifHandler.ListNotifications | List general notifications (v1 only) |
| GET | `/api/v1/me/notifications/unread-count` | - | notifHandler.GetUnreadCount | Get unread notification count (v1 only) |
| POST | `/api/v1/me/notifications/:id/read` | - | notifHandler.MarkRead | Mark notification as read (v1 only) |
| POST | `/api/v1/me/notifications/read-all` | - | notifHandler.MarkAllRead | Mark all as read (v1 only) |

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
| GET | `/api/accounts/by-number/:account_number` | accounts.read | accountHandler.GetAccountByNumber | Get by number |
| POST | `/api/accounts` | accounts.create | accountHandler.CreateAccount | Create account |
| PUT | `/api/accounts/:id/name` | accounts.update | accountHandler.UpdateAccountName | Update name |
| PUT | `/api/accounts/:id/limits` | accounts.update | accountHandler.UpdateAccountLimits | Update limits |
| PUT | `/api/accounts/:id/status` | accounts.update | accountHandler.UpdateAccountStatus | Update status |
| POST | `/api/companies` | accounts.create | accountHandler.CreateCompany | Create company |
| GET | `/api/bank-accounts` | bank-accounts.manage | accountHandler.ListBankAccounts | List bank accounts |
| POST | `/api/bank-accounts` | bank-accounts.manage | accountHandler.CreateBankAccount | Create bank account |
| DELETE | `/api/bank-accounts/:id` | bank-accounts.manage | accountHandler.DeleteBankAccount | Delete bank account |
| GET | `/api/cards` | cards.read | cardHandler.ListCards | List cards (filter) |
| GET | `/api/cards/:id` | cards.read | cardHandler.GetCard | Get card |
| POST | `/api/cards` | cards.create | cardHandler.CreateCard | Create card |
| POST | `/api/cards/authorized-person` | cards.create | cardHandler.CreateAuthorizedPerson | Add auth person |
| POST | `/api/cards/:id/block` | cards.update | cardHandler.BlockCard | Block card |
| POST | `/api/cards/:id/unblock` | cards.update | cardHandler.UnblockCard | Unblock card |
| POST | `/api/cards/:id/deactivate` | cards.update | cardHandler.DeactivateCard | Deactivate card |
| GET | `/api/cards/requests` | cards.approve | cardHandler.ListCardRequests | List card requests |
| GET | `/api/cards/requests/:id` | cards.approve | cardHandler.GetCardRequest | Get card request |
| POST | `/api/cards/requests/:id/approve` | cards.approve | cardHandler.ApproveCardRequest | Approve request |
| POST | `/api/cards/requests/:id/reject` | cards.approve | cardHandler.RejectCardRequest | Reject request |
| GET | `/api/payments` | payments.read | txHandler.ListPayments | List payments |
| GET | `/api/payments/:id` | payments.read | txHandler.GetPayment | Get payment |
| GET | `/api/transfers` | payments.read | txHandler.ListTransfers | List transfers |
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
Balance(numeric18,4), AvailableBalance(numeric18,4), EmployeeID(uint64),
ExpiresAt, CurrencyCode(3,indexed), Status(active|inactive,indexed),
AccountKind(current|foreign), AccountType(standard|premium|student|youth|pension),
AccountCategory, MaintenanceFee(numeric18,4), DailyLimit(numeric18,4,default:1000000),
MonthlyLimit(numeric18,4,default:10000000), DailySpending(numeric18,4),
MonthlySpending(numeric18,4), CompanyID(nullable), IsBankAccount(bool,indexed),
Version(int64), CreatedAt, UpdatedAt, DeletedAt(soft delete)
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

---

## 19. Complete Kafka Topic Reference

### All Topics

| Topic | Producer | Consumer | Message Type |
|---|---|---|---|
| `notification.send-email` | All services | notification-service | SendEmailMessage |
| `notification.email-sent` | notification-service | (logging) | EmailSentMessage |
| `notification.send-push` | (future) | notification-service | (future) |
| `notification.push-sent` | notification-service | (logging) | (future) |
| `auth.account-status-changed` | auth-service | (consumers) | AuthAccountStatusChangedMessage |
| `auth.dead-letter` | auth-service | (monitoring) | (failed events) |
| `user.employee-created` | user-service | notification-service | EmployeeCreatedMessage |
| `user.employee-updated` | user-service | (consumers) | (generic) |
| `user.employee-limits-updated` | user-service | (consumers) | EmployeeLimitsUpdatedMessage |
| `user.limit-template-created` | user-service | (consumers) | LimitTemplateMessage |
| `user.limit-template-updated` | user-service | (consumers) | LimitTemplateMessage |
| `user.limit-template-deleted` | user-service | (consumers) | LimitTemplateMessage |
| `user.client-limits-updated` | user-service | (consumers) | ClientLimitsUpdatedMessage |
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

When adding a new message type: define the struct in `contract/kafka/messages.go`, add a topic constant string, and follow the existing naming pattern (`{Entity}{Action}Message`).

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
| `loan_status` | `approved`, `defaulted` |
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
| `verification_method` | `code_pull` (default), `email` — active; `qr_scan`, `number_match` — planned but not yet active |
| `verification_status` | `pending`, `verified`, `expired`, `failed` |
| `mobile_device_status` | `pending`, `active`, `deactivated` |
| `mobile_inbox_status` | `pending`, `delivered`, `expired` |
| `device_type` (JWT) | `mobile` |

---

## 21. Sentinel Values & Business Rules

### Sentinel Values

| Value | Meaning |
|---|---|
| `owner_id = 1_000_000_000` | Bank-owned account |
| `owner_id = 2_000_000_000` | State-owned entity |

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

**Auth:**
- 5 failed login attempts → 30-min lockout
- Password: 8-32 chars, 2+ digits, 1 uppercase, 1 lowercase
- JMBG: exactly 13 digits

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

The API gateway supports versioned routes alongside the original unversioned routes:

| Prefix | Description |
|---|---|
| `/api/` | Original unversioned routes (frozen, backward-compatible) |
| `/api/v1/` | Version 1 routes (mirrors `/api/` plus new endpoints) |
| `/api/latest/` | Alias that rewrites to the highest version (`/api/v1/`) |

**Implementation files:**
- `api-gateway/internal/router/router.go` — frozen, unversioned `/api/` routes
- `api-gateway/internal/router/router_v1.go` — `/api/v1/` routes (mirrors router.go + new endpoints)
- `api-gateway/internal/router/router_latest.go` — `/api/latest/*` rewrite alias

### v1-only endpoints

These endpoints exist only under `/api/v1/` and are not available on the unversioned `/api/`:

| Method | Path | Status | Plan |
|---|---|---|---|
| GET | /api/v1/accounts/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/employees/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/clients/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/cards/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/loans/:id/changelog | 501 placeholder | Plan 2 |
