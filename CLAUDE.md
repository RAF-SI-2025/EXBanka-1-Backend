# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Specification Reference

**Before implementing any new feature, read `Specification.md` in the repo root.** It contains the complete system specification: every entity, API route, pattern, convention, Kafka topic, enum value, and business rule. Use it as the single source of truth instead of scanning the entire codebase. The "Adding a New Feature Checklist" section (Section 4) is the step-by-step guide for full-stack feature implementation.

**After implementing any feature or change, update `Specification.md` to reflect what was added or modified.** This includes: new API routes (Section 17), new or changed entities (Section 18), new Kafka topics or message types (Section 19), new enum values (Section 20), new business rules (Section 21), new gRPC service definitions (Section 11), new permissions (Section 6), and any changes to the gateway client wiring (Section 3). The spec must always match the current state of the codebase.

## Repository Layout

This is a Go workspace monorepo. Each service has its own self-contained directory at the repo root:

```
(repo root)
├── contract/               # Shared protobuf definitions and Kafka message types
├── api-gateway/             # HTTP REST entry point (Gin, port 8080)
├── auth-service/            # JWT token lifecycle and password workflows (gRPC, port 50051)
├── user-service/            # Employee CRUD and credential management (gRPC, port 50052)
├── notification-service/    # Email/push notification delivery (gRPC port 50053, Kafka consumer)
├── client-service/          # Bank client CRUD and credential management (gRPC, port 50054)
├── account-service/         # Bank accounts, currencies, companies (gRPC, port 50055)
├── card-service/            # Payment cards and authorized persons (gRPC, port 50056)
├── transaction-service/     # Payments, transfers, currency conversion via exchange-service (gRPC, port 50057)
├── credit-service/          # Loan requests, loans, installments (gRPC, port 50058)
├── exchange-service/        # Currency exchange rates and conversion (gRPC, port 50059)
├── verification-service/    # Mobile verification challenges (gRPC, port 50061)
├── docs/                    # API documentation and implementation plans
├── docker-compose.yml
├── Makefile
└── go.work
```

## Commands

All commands should be run from the repo root.

```bash
make proto        # Regenerate protobuf Go files (run after editing .proto files)
make build        # Build all four services into their respective bin/ directories
make tidy         # Run go mod tidy for all services
make lint         # Run golangci-lint on all services (requires golangci-lint installed)
make docker-up    # Start all infrastructure + services via Docker
make docker-down  # Stop containers
make docker-logs  # Stream logs
make clean        # Remove generated protobuf files and binaries
make test         # Run all tests across all services
```

**Individual service builds:**
```bash
cd api-gateway          && go build -o bin/api-gateway          ./cmd
cd user-service         && go build -o bin/user-service         ./cmd
cd auth-service         && go build -o bin/auth-service         ./cmd
cd notification-service && go build -o bin/notification-service ./cmd
```

## Environment

Each service reads its `.env` file by walking up the directory tree from its working directory until it finds one. Place a `.env` file at the repo root (or within the service directory) containing the required variables. Key variables:

| Variable | Default | Notes |
|---|---|---|
| `USER_DB_PORT` / `AUTH_DB_PORT` | 5432 / 5433 | Two separate PostgreSQL instances |
| `JWT_SECRET` | *(must set)* | 256-bit secret |
| `JWT_ACCESS_EXPIRY` / `JWT_REFRESH_EXPIRY` | 15m / 168h | |
| `AUTH_GRPC_ADDR` / `USER_GRPC_ADDR` | localhost:50051 / :50052 | |
| `NOTIFICATION_GRPC_ADDR` | :50053 | |
| `CLIENT_GRPC_ADDR` | localhost:50054 | client-service gRPC address; also required by credit-service and card-service |
| `ACCOUNT_GRPC_ADDR` | localhost:50055 | account-service gRPC address; also required by credit-service and card-service |
| `CARD_GRPC_ADDR` | localhost:50056 | card-service gRPC address |
| `TRANSACTION_GRPC_ADDR` | localhost:50057 | transaction-service gRPC address |
| `CREDIT_GRPC_ADDR` | localhost:50058 | credit-service gRPC address |
| `EXCHANGE_GRPC_ADDR` | localhost:50059 | exchange-service gRPC address; also required by transaction-service |
| `VERIFICATION_GRPC_ADDR` | localhost:50061 | verification-service gRPC address; also required by transaction-service |
| `GATEWAY_HTTP_ADDR` | :8080 | |
| `MOBILE_REFRESH_EXPIRY` | 2160h (90 days) | Mobile refresh token expiry (auth-service) |
| `MOBILE_ACTIVATION_EXPIRY` | 15m | Mobile activation code expiry (auth-service) |
| `VERIFICATION_CHALLENGE_EXPIRY` | 5m | Verification challenge expiry (verification-service) |
| `VERIFICATION_MAX_ATTEMPTS` | 3 | Max verification attempts (verification-service) |
| `KAFKA_BROKERS` | localhost:9092 | |
| `REDIS_ADDR` | localhost:6379 | Shared by auth + user services |
| `SMTP_HOST` / `SMTP_PORT` | smtp.gmail.com / 587 | |
| `SMTP_USER` / `SMTP_PASSWORD` | *(must set)* | Gmail app password |
| `SMTP_FROM` | *(must set)* | Sender email address |
| `FRONTEND_BASE_URL` | http://localhost:3000 | Base URL for links in emails |

**New service DB ports (if running separate DBs):**

| Service | Default DB Port |
|---|---|
| client-service | 5434 |
| account-service | 5435 |
| card-service | 5436 |
| transaction-service | 5437 |
| credit-service | 5438 |
| exchange-service | 5439 |
| verification-service | 5440 |
| notification-service | 5441 |

**Note:** `credit-service` and `card-service` both depend on `CLIENT_GRPC_ADDR` and `ACCOUNT_GRPC_ADDR` in addition to their own gRPC addresses. Ensure these variables are set in their docker-compose environment sections.

## Architecture

**Communication layers:**
- Clients → API Gateway: HTTP/JSON (Gin)
- API Gateway → Services: gRPC (protobuf, defined in `contract/proto/`)
- Services → Notification: Kafka topic `notification.send-email`
- Notification → Services: Kafka topic `notification.email-sent` (delivery confirmation)
- Verification-service → Notification: Kafka topic `verification.challenge-created`
- Verification-service → Transaction: Kafka topic `verification.challenge-verified`
- Notification → Gateway: Kafka topic `notification.mobile-push` (WebSocket delivery)
- Persistence: PostgreSQL via GORM (auto-migrated on startup)
- Caching: Redis (JWT validation in auth-service, employee lookups in user-service)
- Swagger UI: Available at /swagger/index.html on the API Gateway (port 8080)

**Each backend service follows the same layered structure:**
```
cmd/main.go          → wires dependencies, starts server
internal/
  config/            → loads env vars into config structs
  model/             → GORM-tagged domain structs (DB schema)
  repository/        → raw DB queries via GORM
  service/           → business logic, calls repository + sends Kafka events
  handler/           → gRPC handler, translates protobuf ↔ service calls
  cache/             → Redis cache wrapper (auth-service, user-service)
  kafka/producer.go  → publishes messages to Kafka
```

The API Gateway has no DB; instead it has `internal/grpc/` clients and `internal/middleware/auth.go` for JWT validation.

The Notification Service has a PostgreSQL database (`notification_db`, port 5441) for mobile inbox storage. It also has `internal/consumer/` for Kafka consumption, `internal/sender/` for SMTP, and `internal/push/` for future push notification providers.

**Database auto-migration:** Both DB-backed services call `db.AutoMigrate(...)` on startup — no separate migration tool is needed.

**Redis caching:** Both auth-service and user-service degrade gracefully if Redis is unavailable — they log a warning and operate without cache.

## Key Domain Concepts

**Roles & permissions** (stored in `user_db`, seeded from `user-service/internal/service/role_service.go`):
- Roles (`EmployeeBasic`, `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin`) are stored in the `roles` table with their associated `permissions` in a `role_permissions` join table.
- Employees can have multiple roles (`employee_roles`) and additional per-employee permissions (`employee_additional_permissions`).
- Default seed: `EmployeeBasic` → clients/accounts/cards/credits access; `EmployeeAgent` → adds securities trading; `EmployeeSupervisor` → adds agents/OTC/funds management + verification.skip + verification.manage; `EmployeeAdmin` → adds employees management + all permissions.
- `verification.skip` — allows skipping mobile verification for transactions (EmployeeSupervisor, EmployeeAdmin)
- `verification.manage` — allows managing verification settings per role (EmployeeSupervisor, EmployeeAdmin)

**Employee limits** (stored in `user_db`, `employee_limits` table):
- Each employee has configurable limits: `MaxLoanApprovalAmount`, `MaxSingleTransaction`, `MaxDailyTransaction`, `MaxClientDailyLimit`, `MaxClientMonthlyLimit` (all `decimal.Decimal`).
- Limit templates (`limit_templates`) allow bulk-assigning limits by template name (e.g., `BasicTeller`).

**Client limits** (stored in `client_db`, `client_limits` table):
- Clients have `DailyLimit`, `MonthlyLimit`, `TransferLimit`.
- When an employee sets client limits, the values are constrained to not exceed the employee's own `MaxClientDailyLimit` and `MaxClientMonthlyLimit`.

**Virtual cards** (card-service):
- Cards can be physical or virtual (`is_virtual` field).
- Virtual cards have `usage_type`: `single_use` (expires after one use), `multi_use` (has `max_uses` counter), or `unlimited`.
- All cards support PIN management (bcrypt-hashed, 4 digits, locked after 3 failed attempts).
- Temporary block: `CardBlock` record with optional `ExpiresAt` — auto-unblocked by background goroutine every minute.

**Bank account management** (account-service):
- Bank-owned accounts are flagged with `is_bank_account = true` and `owner_id = 1_000_000_000` (sentinel).
- The bank must maintain at least one RSD account and one foreign currency account at all times (enforced on delete).
- Seeded at startup: 1 RSD + 1 EUR bank account.
- Used for collecting transaction fees and loan installment credits.

**Exchange rates** (exchange-service):
- Exchange rates are synced from the open.er-api.com API every 6 hours (configurable via `EXCHANGE_SYNC_INTERVAL_HOURS`).
- Rates are seeded with hardcoded defaults on first startup; external API sync failure is non-fatal (log warning, keep seed rates).
- Rates stored as both `CODE/RSD` and inverse `RSD/CODE` pairs with buy/sell spread applied.
- Transaction-service calls exchange-service via gRPC (`Convert` RPC) for cross-currency transfers; no commission applied at that layer.
- Exchange-service config: `EXCHANGE_COMMISSION_RATE` (default 0.005), `EXCHANGE_SPREAD` (default 0.003), `EXCHANGE_API_KEY` (optional, for paid tier).

**Transfer fees** (transaction-service):
- Configurable fee rules stored in `transfer_fees` table (type: `percentage` or `fixed`, with `min_amount` threshold and `max_fee` cap).
- Multiple matching rules are cumulative (stack).
- Fee lookup failure (DB error) REJECTS the transaction (not silently ignored).
- Default seeds: (1) 0.1% percentage fee for all transactions >= 1000 RSD; (2) 5% commission for all transactions >= 5000 RSD.
- Collected fees are credited to the bank's own RSD account after each transaction.

**Auth token system type** (auth-service):
- JWT claims include a `system_type` field: `employee` for employee logins, `client` for client logins.
- Middleware uses `system_type` to route to `AuthMiddleware` (employee) or `AnyAuthMiddleware` (client + employee).

**Token types** (auth-service):
- Access token: short-lived JWT (15 min), stateless validation. Claims include `user_id`, `roles []string`, `permissions []string`, and `system_type` (`employee` or `client`).
- Refresh token: long-lived (168h), stored in `auth_db` and revocable
- Activation token: 24h, triggers email with activation link via Kafka → notification-service
- Password reset token: 1h, triggers email with reset link via Kafka → notification-service

**Notification flow:** Services publish `SendEmailMessage` to Kafka → notification-service consumes, sends via SMTP, publishes `EmailSentMessage` delivery confirmation back to Kafka.

**Employee creation flow:** API Gateway → User service (create employee) → Auth service (create activation token) → Kafka → Notification service (send activation email).

**Client login flow:** API Gateway (`POST /api/auth/client-login`) → Auth service (`ClientLogin` RPC) → Client service (`ValidateCredentials` RPC) → Auth service generates JWT with `role="client"` and issues refresh token. The client JWT is validated by `AnyAuthMiddleware` in the API Gateway for `/api/me/*` routes.

**JMBG (Jedinstveni Matični Broj Građana):**
- Unique 13-digit national identification number required for all employees
- Validated on create and update (exactly 13 digits)
- Stored with unique index in user_db

## API Gateway Input Validation Requirement

**The API gateway must validate all input data before forwarding to gRPC services.** This is a hard requirement — not optional.

- All string enum fields must be validated against their allowed values using the `oneOf()` helper in `api-gateway/internal/handler/validation.go`. This helper also normalizes input to lowercase, making validation case-insensitive (e.g., `"FOREIGN"` becomes `"foreign"`).
- Numeric fields must be validated for range and sign: amounts must be positive (`positive()`), limits must be non-negative (`nonNegative()`), ranges must be checked with `inRange()`.
- Format-constrained fields (e.g., PIN must be 4 digits) must be validated with format-specific helpers (e.g., `validatePin()`).
- When adding a new endpoint or field, add validation in the gateway handler BEFORE the gRPC call. Return HTTP 400 with a clear error message on validation failure.
- Known enum values (keep in sync with service-layer validation):
  - `account_kind`: `current`, `foreign`
  - `account status`: `active`, `inactive`
  - `card_brand`: `visa`, `mastercard`, `dinacard`, `amex`
  - `owner_type`: `client`, `authorized_person`
  - `usage_type`: `single_use`, `multi_use`
  - `fee_type`: `percentage`, `fixed`
  - `loan_type`: `cash`, `housing`, `auto`, `refinancing`, `student`
  - `interest_type`: `fixed`, `variable`
  - `verification_method`: `code_pull`, `qr_scan`, `number_match`, `email`

## REST API Conventions

**Route structure:**
- `/api/me/*` — authenticated user's own resources. Ownership derived from JWT `user_id`, never from URL params. Protected by `AnyAuthMiddleware`.
- `/api/<resource>` — general/admin/employee routes. Protected by `AuthMiddleware` + `RequirePermission`.
- Collection filtering uses query params (`?client_id=X`, `?account_number=X`), not path segments (`/client/:id`).
- When multiple filter params are provided on the same endpoint, return 400 — only one filter at a time.

**HTTP verbs:**
- `POST` for all state-change actions (approve, reject, block, unblock, deactivate, execute, temporary-block).
- `PUT` only for idempotent full replacements (update employee, set permissions, set limits, update name).
- `GET` for reads, `DELETE` for deletes.

**Error handling:**
- All error responses use the `apiError()` helper in `validation.go` — never raw `gin.H{"error": "string"}`.
- Response format: `{"error": {"code": "...", "message": "...", "details": {...}}}` where `details` is optional.
- HTTP status code must always match the error semantics — a 403 body never arrives with a 500 status.
- gRPC error mapping (in `validation.go`):
  - `InvalidArgument` → 400 `validation_error`
  - `Unauthenticated` → 401 `unauthorized`
  - `PermissionDenied` → 403 `forbidden`
  - `NotFound` → 404 `not_found`
  - `AlreadyExists` → 409 `conflict`
  - `FailedPrecondition` → 409 `business_rule_violation`
  - `ResourceExhausted` → 429 `rate_limited`
  - Default → 500 `internal_error`
- Middleware uses `abortWithError()` (local to middleware package) with the same JSON shape.

**Middleware assignment:**
- `AnyAuthMiddleware` for `/api/me/*` routes (accepts both client and employee tokens).
- `AuthMiddleware` + `RequirePermission("...")` for employee/admin routes.
- Client routes use `AnyAuthMiddleware` which accepts both client and employee tokens.
- Public routes (auth, exchange rates) need no middleware.

**When adding a new endpoint:**
- If it's "user accesses their own resource" → add under `/api/me/*` with `AnyAuthMiddleware`.
- If it's an employee/admin operation or general data → add under `/api/<resource>` with `AuthMiddleware`.
- Use `apiError()` for all error responses, `handleGRPCError()` for gRPC errors.
- Update swagger annotations, `docs/api/REST_API_v1.md`, and `test-app` integration tests.
- For `/api/me` handlers: create a `ListMy<Resource>` wrapper that extracts `user_id` from JWT context.

## Swagger Documentation Requirement

**Every route added or changed in `api-gateway` must have up-to-date Swagger annotations.** This is a hard requirement — not optional.

- Every handler function exposed via the router must have a `// @Summary`, `// @Tags`, `// @Param`, `// @Success`, `// @Failure`, and `// @Router` godoc annotation.
- After adding or modifying any route or handler, regenerate the docs: `make swagger` (or `cd api-gateway && swag init -g cmd/main.go --output docs`).
- Swagger is **not dynamic** — the generated files in `api-gateway/docs/` must be committed alongside handler changes.
- `make build` runs `swag init` automatically before compiling.

## REST API Documentation Requirement

**Every route added, changed, or removed in `api-gateway` must be reflected in `docs/api/REST_API_v1.md`.** This is a hard requirement — not optional.

- Add a new section or subsection for every new endpoint, following the existing format (authentication, path/query parameters, request body, example request, and all response codes).
- Update existing sections when request/response shapes, authentication requirements, or behavior changes.
- Remove sections for any deleted routes.
- `docs/api/REST_API_v1.md` must be committed alongside handler and router changes.

## Testing Requirement

**Every feature or change must include tests.** This is a hard requirement — not optional.

- **Unit tests** (service + handler layers) must be added or updated for every service change. Use mocked repositories and gRPC clients. Test both success and error paths.
- **Integration tests** (`test-app/workflows/`) must be added or updated when a feature touches API endpoints or cross-service flows. Each new endpoint needs at least one integration test.
- **Implementation plans must include a testing section** — every plan must specify which unit tests and integration tests to add or update. Plans without testing steps are incomplete.
- Use shared helpers from `contract/testutil/` (unit tests) and `test-app/workflows/helpers_test.go` (integration tests) to avoid duplication. Never inline Kafka scanning, verification flows, or client setup — use the shared helpers.
- Tests must validate **spec behavior** — not just HTTP status codes. Check response bodies, side effects (balance changes, Kafka events), and business rules from the spec.
- Tests must pass before committing. Run `make test` for unit tests and the integration suite for workflow tests.
- See `docs/superpowers/specs/2026-04-04-comprehensive-testing-design.md` for the full testing design and patterns.

## Linting Requirement

**After every code change, run `make lint` on the affected services.** This is a hard requirement — not optional.

- Run `make lint` (or `cd <service> && golangci-lint run ./...` for individual services) after modifying any Go code.
- All new or modified code must pass lint with zero new warnings. Do not introduce new `errcheck`, `unused`, `staticcheck`, or other lint violations.
- Fix any lint errors in code you touched before committing. You are not required to fix pre-existing lint issues in code you did not modify.
- Requires `golangci-lint` installed (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`).

## Kafka Event Publishing Requirement

**All services must publish Kafka events for every significant action they perform.** This is a hard requirement — not optional.

- Every create, update, delete, and state-change operation in a service's business logic layer must result in a Kafka event being published.
- Events must be published from the `service/` layer (not handler or repository).
- Use the existing `kafka/producer.go` pattern within each service.
- Event topic naming convention: `<service>.<action>` (e.g., `user.employee-created`, `auth.token-activated`, `notification.email-sent`).
- Define event payload structs in `contract/` so other services can consume them.

## Kafka Topic Pre-Creation Requirement

**Every service that uses Kafka must pre-create its topics on startup using `EnsureTopics`.** This is a hard requirement — not optional.

- Without pre-creation, Kafka consumer groups that start before topics exist get assigned 0 partitions and never receive messages (partition assignment race condition).
- Every service has a `internal/kafka/topics.go` file containing the `EnsureTopics(broker string, topics ...string)` function. This function connects to the Kafka controller and creates topics idempotently (no error if they already exist).
- In `cmd/main.go`, call `kafkaprod.EnsureTopics(cfg.KafkaBrokers, ...)` immediately after creating the producer, listing **all topics the service produces to AND consumes from**.
- When adding a new Kafka topic (new event type in `contract/kafka/messages.go`), add it to the `EnsureTopics` call in every service that produces or consumes that topic.
- The function retries Kafka connection up to 10 times (2s apart) to handle startup ordering in Docker Compose.

## Concurrency & Transaction Safety Requirement

**All code in this banking system must be concurrency-safe.** This is a hard requirement — not optional. Every service handles real money and must be bank-grade safe.

### Optimistic Locking (Version Field)

- Every model with a `Version int64` field **MUST** have a `BeforeUpdate` GORM hook that enforces version matching:
  ```go
  func (m *MyModel) BeforeUpdate(tx *gorm.DB) error {
      tx.Statement.Where("version = ?", m.Version)
      m.Version++
      return nil
  }
  ```
- **NEVER** use `db.Model(&MyModel{}).Updates(map...)` on a versioned model — this creates a zero-value struct with `Version=0`, and the hook adds `WHERE version = 0` which matches nothing. Always load the struct first, modify it, then call `db.Save(&struct)`.
- For bulk updates that intentionally skip version checks (e.g., spending resets, overdue marking), use `db.Session(&gorm.Session{SkipHooks: true})`.
- After every `db.Save()` on a versioned model, check `result.RowsAffected == 0` for optimistic lock conflict and return `shared.ErrOptimisticLock`.
- Models currently with Version fields: `Account`, `Company`, `Card`, `Loan`, `LoanRequest`, `Installment`, `Payment`, `Transfer`, `ExchangeRate`, `Client`, `ClientLimit`, `Employee`, `EmployeeLimit`.

### Transaction Requirements

- **Every multi-step DB write** (create + update, debit + credit, status check + status change) **MUST** be wrapped in `db.Transaction(func(tx *gorm.DB) error { ... })`. No exceptions.
- **Every read-modify-write pattern** (read value → check condition → update) **MUST** use `SELECT FOR UPDATE` inside a transaction:
  ```go
  db.Transaction(func(tx *gorm.DB) error {
      tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entity, id)
      // ... modify entity ...
      return tx.Save(&entity).Error
  })
  ```
- **Upsert operations** (check existence → create or update) **MUST** use PostgreSQL `ON CONFLICT` via GORM's `clause.OnConflict{}` — never SELECT-then-INSERT.

### Cross-Service Operations (Saga Pattern)

- When a business operation spans multiple gRPC calls to different services (e.g., transfer: debit account A → credit account B), use the **saga log pattern** (`transaction-service/internal/model/saga_log.go`).
- Each saga step is recorded as `pending` before execution, marked `completed` on success, or triggers compensation steps marked `compensating`.
- Failed compensations remain in `compensating` status for background recovery.
- **Kafka events MUST be published AFTER the DB transaction commits**, not inside the transaction.

### Spending Limits

- Spending limits (daily/monthly) are enforced **atomically** inside `account-service`'s `UpdateBalance` method, within a `SELECT FOR UPDATE` transaction. This is the **authoritative** check.
- Transaction-service and payment-service may perform advisory pre-checks via gRPC reads, but these are NOT authoritative — they are early-exit optimizations only.

### Background Goroutines

- All background goroutines (cron jobs, tickers) **MUST** accept `context.Context` and honor cancellation via `ctx.Done()`.
- Tickers **MUST** be stopped with `defer ticker.Stop()`.
- Use `select { case <-time.After(...): ... case <-ctx.Done(): return }` instead of bare `time.Sleep()`.

### Key Patterns Reference

- **Exemplary implementation:** `exchange-service/internal/repository/exchange_rate_repository.go` `Upsert()` — uses `db.Transaction` + `clause.Locking{Strength: "UPDATE"}` + version increment.
- **Exemplary implementation:** `account-service/internal/repository/ledger_repository.go` `DebitWithLock`/`CreditWithLock` — uses FOR UPDATE + balance check + ledger entry + spending update (for non-bank accounts) in single TX.

## Docker Compose Requirement

**When adding or modifying a service, `docker-compose.yml` must be updated to match.** This is a hard requirement — not optional.

- If a service's `config.go` adds a new environment variable (e.g., a gRPC address to another service, a DB setting, or any external dependency), add that variable to the service's `environment:` block in `docker-compose.yml`.
- gRPC addresses must use Docker service names, not `localhost` (e.g., `client-service:50054`, not `localhost:50054`).
- If a service depends on another service at runtime (DB, Kafka, Redis, or another gRPC service), add a `depends_on:` entry for it.
- When adding a new service, add its DB, its service definition, the volume, and wire it into the `api-gateway` environment and `depends_on`.
- `docker-compose.yml` must be committed alongside service changes.
- **`docker-compose-remote.yml` must be kept in sync with `docker-compose.yml`.** When adding/changing environment variables, ports, volumes, or services in `docker-compose.yml`, apply the same changes to `docker-compose-remote.yml` (which uses pre-built images from ghcr.io instead of building from source). The only difference between the two files is the `build:` vs `image:` directives — environment, ports, volumes, depends_on, and healthchecks must match.

## Implementation Plans

Before starting any feature or bug fix, read the existing implementation plans in `docs/superpowers/plans/`. These plans contain context, decisions, and scope that must inform your work. Plans are Markdown files named by date and feature (e.g., `2026-03-12-feature-name.md`).

## Proto Code Generation

Requires `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` plugins. Generated files go into `contract/authpb/`, `contract/userpb/`, and `contract/notificationpb/`. Run `make proto` after any `.proto` change.

## Password Validation

Both auth-service and user-service validate passwords imperatively (no regex). Rules: 8-32 chars, at least 2 digits, 1 uppercase, 1 lowercase letter.
