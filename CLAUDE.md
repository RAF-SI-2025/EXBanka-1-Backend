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
├── docker-compose.yml
├── Makefile
└── go.work
```

Development happens in git worktrees under `.worktrees/`. Each worktree checks out a feature branch of the full repo.

## Commands

All commands should be run from the active worktree root (e.g., `.worktrees/api-gateway/`).

```bash
make proto        # Regenerate protobuf Go files (run after editing .proto files)
make build        # Build all four services into their respective bin/ directories
make tidy         # Run go mod tidy for all services
make docker-up    # Start all infrastructure + services via Docker
make docker-down  # Stop containers
make docker-logs  # Stream logs
make clean        # Remove generated protobuf files and binaries
```

**Individual service builds:**
```bash
cd api-gateway          && go build -o bin/api-gateway          ./cmd
cd user-service         && go build -o bin/user-service         ./cmd
cd auth-service         && go build -o bin/auth-service         ./cmd
cd notification-service && go build -o bin/notification-service ./cmd
```

## Environment

Copy `.env.example` to `.env` in the active worktree root. Key variables:

| Variable | Default | Notes |
|---|---|---|
| `USER_DB_PORT` / `AUTH_DB_PORT` | 5432 / 5433 | Two separate PostgreSQL instances |
| `JWT_SECRET` | *(must set)* | 256-bit secret |
| `JWT_ACCESS_EXPIRY` / `JWT_REFRESH_EXPIRY` | 15m / 168h | |
| `AUTH_GRPC_ADDR` / `USER_GRPC_ADDR` | localhost:50051 / :50052 | |
| `NOTIFICATION_GRPC_ADDR` | :50053 | |
| `GATEWAY_HTTP_ADDR` | :8080 | |
| `KAFKA_BROKERS` | localhost:9092 | |
| `REDIS_ADDR` | localhost:6379 | Shared by auth + user services |
| `SMTP_HOST` / `SMTP_PORT` | smtp.gmail.com / 587 | |
| `SMTP_USER` / `SMTP_PASSWORD` | *(must set)* | Gmail app password |
| `SMTP_FROM` | *(must set)* | Sender email address |

## Architecture

**Communication layers:**
- Clients → API Gateway: HTTP/JSON (Gin)
- API Gateway → Services: gRPC (protobuf, defined in `contract/proto/`)
- Services → Notification: Kafka topic `notification.send-email`
- Notification → Services: Kafka topic `notification.email-sent` (delivery confirmation)
- Persistence: PostgreSQL via GORM (auto-migrated on startup)
- Caching: Redis (JWT validation in auth-service, employee lookups in user-service)

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

**Roles & permissions** (defined in `user-service/internal/service/role_service.go`):
- `EmployeeBasic` → clients/accounts/cards/credits access
- `EmployeeAgent` → adds securities trading
- `EmployeeSupervisor` → adds agents/OTC/funds management
- `EmployeeAdmin` → adds employees management

**Token types** (auth-service):
- Access token: short-lived JWT (15 min), stateless validation (cached in Redis)
- Refresh token: long-lived (168h), stored in `auth_db` and revocable
- Activation token: 24h, triggers email via Kafka → notification-service
- Password reset token: 1h, triggers email via Kafka → notification-service

**Notification flow:** Services publish `SendEmailMessage` to Kafka → notification-service consumes, sends via SMTP, publishes `EmailSentMessage` delivery confirmation back to Kafka.

**Employee creation flow:** API Gateway → User service (create employee) → Auth service (create activation token) → Kafka → Notification service (send activation email).

## Proto Code Generation

Requires `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` plugins. Generated files go into `contract/authpb/`, `contract/userpb/`, and `contract/notificationpb/`. Run `make proto` after any `.proto` change.

## Password Validation

Both auth-service and user-service validate passwords imperatively (no regex). Rules: 8-32 chars, at least 2 digits, 1 uppercase, 1 lowercase letter.
