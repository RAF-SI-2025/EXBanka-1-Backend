# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
├── transaction-service/     # Payments, transfers, exchange rates (gRPC, port 50057)
├── credit-service/          # Loan requests, loans, installments (gRPC, port 50058)
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
| `GATEWAY_HTTP_ADDR` | :8080 | |
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

**Note:** `credit-service` and `card-service` both depend on `CLIENT_GRPC_ADDR` and `ACCOUNT_GRPC_ADDR` in addition to their own gRPC addresses. Ensure these variables are set in their docker-compose environment sections.

## Architecture

**Communication layers:**
- Clients → API Gateway: HTTP/JSON (Gin)
- API Gateway → Services: gRPC (protobuf, defined in `contract/proto/`)
- Services → Notification: Kafka topic `notification.send-email`
- Notification → Services: Kafka topic `notification.email-sent` (delivery confirmation)
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

The Notification Service has no DB; it has `internal/consumer/` for Kafka consumption, `internal/sender/` for SMTP, and `internal/push/` for future push notification providers.

**Database auto-migration:** Both DB-backed services call `db.AutoMigrate(...)` on startup — no separate migration tool is needed.

**Redis caching:** Both auth-service and user-service degrade gracefully if Redis is unavailable — they log a warning and operate without cache.

## Key Domain Concepts

**Roles & permissions** (stored in `user_db`, seeded from `user-service/internal/service/role_service.go`):
- Roles (`EmployeeBasic`, `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin`) are stored in the `roles` table with their associated `permissions` in a `role_permissions` join table.
- Employees can have multiple roles (`employee_roles`) and additional per-employee permissions (`employee_additional_permissions`).
- Default seed: `EmployeeBasic` → clients/accounts/cards/credits access; `EmployeeAgent` → adds securities trading; `EmployeeSupervisor` → adds agents/OTC/funds management; `EmployeeAdmin` → adds employees management.

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

**NBS exchange rates** (transaction-service):
- Exchange rates are synced from the National Bank of Serbia XML API every 6 hours.
- Rates are seeded with hardcoded defaults on first startup; NBS sync failure on startup is non-fatal (log warning, keep seed rates).
- Rates stored as both `CODE/RSD` and inverse `RSD/CODE` pairs.

**Transfer fees** (transaction-service):
- Configurable fee rules stored in `transfer_fees` table (type: `percentage` or `fixed`, with `min_amount` threshold and `max_fee` cap).
- Multiple matching rules are cumulative (stack).
- Fee lookup failure (DB error) REJECTS the transaction (not silently ignored).
- Default seed: 0.1% percentage fee for all transactions >= 1000 RSD.
- Collected fees are credited to the bank's own RSD account after each transaction.

**Auth token system type** (auth-service):
- JWT claims include a `system_type` field: `employee` for employee logins, `client` for client logins.
- Middleware uses `system_type` to route to `AuthMiddleware` (employee) or `ClientAuthMiddleware` (client).

**Token types** (auth-service):
- Access token: short-lived JWT (15 min), stateless validation. Claims include `user_id`, `roles []string`, `permissions []string`, and `system_type` (`employee` or `client`).
- Refresh token: long-lived (168h), stored in `auth_db` and revocable
- Activation token: 24h, triggers email with activation link via Kafka → notification-service
- Password reset token: 1h, triggers email with reset link via Kafka → notification-service

**Notification flow:** Services publish `SendEmailMessage` to Kafka → notification-service consumes, sends via SMTP, publishes `EmailSentMessage` delivery confirmation back to Kafka.

**Employee creation flow:** API Gateway → User service (create employee) → Auth service (create activation token) → Kafka → Notification service (send activation email).

**Client login flow:** API Gateway (`POST /api/auth/client-login`) → Auth service (`ClientLogin` RPC) → Client service (`ValidateCredentials` RPC) → Auth service generates JWT with `role="client"` and issues refresh token. The client JWT is then validated by `ClientAuthMiddleware` in the API Gateway for client-protected routes.

**JMBG (Jedinstveni Matični Broj Građana):**
- Unique 13-digit national identification number required for all employees
- Validated on create and update (exactly 13 digits)
- Stored with unique index in user_db

## Swagger Documentation Requirement

**Every route added or changed in `api-gateway` must have up-to-date Swagger annotations.** This is a hard requirement — not optional.

- Every handler function exposed via the router must have a `// @Summary`, `// @Tags`, `// @Param`, `// @Success`, `// @Failure`, and `// @Router` godoc annotation.
- After adding or modifying any route or handler, regenerate the docs: `make swagger` (or `cd api-gateway && swag init -g cmd/main.go --output docs`).
- Swagger is **not dynamic** — the generated files in `api-gateway/docs/` must be committed alongside handler changes.
- `make build` runs `swag init` automatically before compiling.

## REST API Documentation Requirement

**Every route added, changed, or removed in `api-gateway` must be reflected in `docs/api/REST_API.md`.** This is a hard requirement — not optional.

- Add a new section or subsection for every new endpoint, following the existing format (authentication, path/query parameters, request body, example request, and all response codes).
- Update existing sections when request/response shapes, authentication requirements, or behavior changes.
- Remove sections for any deleted routes.
- `docs/api/REST_API.md` must be committed alongside handler and router changes.

## Kafka Event Publishing Requirement

**All services must publish Kafka events for every significant action they perform.** This is a hard requirement — not optional.

- Every create, update, delete, and state-change operation in a service's business logic layer must result in a Kafka event being published.
- Events must be published from the `service/` layer (not handler or repository).
- Use the existing `kafka/producer.go` pattern within each service.
- Event topic naming convention: `<service>.<action>` (e.g., `user.employee-created`, `auth.token-activated`, `notification.email-sent`).
- Define event payload structs in `contract/` so other services can consume them.

## Implementation Plans

Before starting any feature or bug fix, read the existing implementation plans in `docs/superpowers/plans/`. These plans contain context, decisions, and scope that must inform your work. Plans are Markdown files named by date and feature (e.g., `2026-03-12-feature-name.md`).

## Proto Code Generation

Requires `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` plugins. Generated files go into `contract/authpb/`, `contract/userpb/`, and `contract/notificationpb/`. Run `make proto` after any `.proto` change.

## Password Validation

Both auth-service and user-service validate passwords imperatively (no regex). Rules: 8-32 chars, at least 2 digits, 1 uppercase, 1 lowercase letter.
