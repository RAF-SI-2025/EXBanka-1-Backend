# EXBanka Backend API Documentation

## Overview

EXBanka is a Go-based banking microservices system. The API Gateway exposes a REST API on port 8080 and communicates with backend services via gRPC.

**Swagger UI:** Available at `http://localhost:8080/swagger/index.html` when the API Gateway is running.

---

## Architecture

```
Client
  │ HTTP/JSON
  ▼
API Gateway (:8080) ─── JWT Auth Middleware
  │         │
  │ gRPC    │ gRPC
  ▼         ▼
Auth Service   User Service
  (:50051)      (:50052)
  │    │         │    │
  │    │ Kafka   │    │ Redis
  │    ▼         │    ▼
  │  Notification│  Redis Cache
  │  Service     │
  │  (:50053)    │
  └──────────────┘
       PostgreSQL (auth_db port 5433, user_db port 5432)
```

**Communication:**
- Clients → API Gateway: HTTP/JSON (Gin)
- API Gateway → Services: gRPC (protobuf)
- Services → Notification: Kafka topic `notification.send-email`
- Notification → Services: Kafka topic `notification.email-sent`
- Caching: Redis (JWT tokens, employee lookups)

---

## Services

| Service | Port | Description |
|---------|------|-------------|
| API Gateway | :8080 (HTTP) | REST entry point, JWT validation |
| Auth Service | :50051 (gRPC) | JWT lifecycle, password workflows |
| User Service | :50052 (gRPC) | Employee CRUD, credential management |
| Notification Service | :50053 (gRPC) | Email delivery via SMTP + Kafka |

---

## Database Schema

### Employee (user_db)

| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| id | int64 | PK, autoincrement | |
| first_name | string | NOT NULL | |
| last_name | string | NOT NULL | |
| date_of_birth | timestamp | NOT NULL | |
| gender | string(10) | | |
| email | string | UNIQUE, NOT NULL | |
| phone | string | | |
| address | string | | |
| jmbg | string(13) | UNIQUE, NOT NULL | 13-digit national ID |
| username | string | UNIQUE, NOT NULL | |
| password_hash | string | NOT NULL | bcrypt hash |
| salt | string | NOT NULL | hex-encoded 16 bytes |
| position | string | | |
| department | string | | |
| active | bool | default: true | |
| role | string | NOT NULL, default: 'EmployeeBasic' | |
| activated | bool | default: false | true after first password set |
| created_at | timestamp | | |
| updated_at | timestamp | | |

### Tokens (auth_db)

| Table | Fields | Notes |
|-------|--------|-------|
| refresh_tokens | id, user_id, token, expires_at, revoked, created_at | Long-lived, stored revocably |
| activation_tokens | id, user_id, token, expires_at, used, created_at | 24h, one-time |
| password_reset_tokens | id, user_id, token, expires_at, used, created_at | 1h, one-time |

---

## Roles & Permissions

| Role | Permissions |
|------|-------------|
| EmployeeBasic | clients.read, accounts.create, accounts.read, cards.manage, credits.manage |
| EmployeeAgent | + securities.trade, securities.read |
| EmployeeSupervisor | + agents.manage, otc.manage, funds.manage |
| EmployeeAdmin | + employees.create, employees.update, employees.read, employees.permissions |

---

## Token Lifecycle

| Token | Lifetime | Storage | Notes |
|-------|----------|---------|-------|
| Access JWT | 15 min | Stateless (Redis cache) | Bears role + permissions |
| Refresh token | 168h (7 days) | auth_db | Revocable |
| Activation token | 24h | auth_db | Triggers activation email link |
| Password reset token | 1h | auth_db | Triggers password reset email link |

**Email Links:** Activation and password reset emails contain clickable links to the frontend. Configure `FRONTEND_BASE_URL` to set the base URL.

---

## API Endpoints

### Authentication (Public)

#### POST /api/auth/login
Authenticate and receive tokens.

**Request:**
```json
{
  "email": "user@example.com",
  "password": "Password12"
}
```

**Response 200:**
```json
{
  "access_token": "eyJ...",
  "refresh_token": "abc123..."
}
```

#### POST /api/auth/refresh
Exchange a refresh token for a new token pair.

**Request:**
```json
{ "refresh_token": "abc123..." }
```

**Response 200:**
```json
{ "access_token": "eyJ...", "refresh_token": "new123..." }
```

#### POST /api/auth/logout
Revoke a refresh token.

**Request:**
```json
{ "refresh_token": "abc123..." }
```

#### POST /api/auth/password/reset-request
Request a password reset email.

**Request:**
```json
{ "email": "user@example.com" }
```

**Response 200:** `{ "message": "if the email exists, a reset link has been sent" }`

#### POST /api/auth/password/reset
Reset password using token from email link.

**Request:**
```json
{
  "token": "abc123...",
  "new_password": "NewPass12",
  "confirm_password": "NewPass12"
}
```

#### POST /api/auth/activate
Activate account and set password using token from email link.

**Request:**
```json
{
  "token": "abc123...",
  "password": "MyPass12",
  "confirm_password": "MyPass12"
}
```

---

### Employees (Protected — requires JWT Bearer token)

All employee endpoints require `Authorization: Bearer <access_token>` header.

#### GET /api/employees
List employees. Requires `employees.read` permission.

**Query params:** `page`, `page_size`, `email`, `name`, `position`

**Response 200:**
```json
{
  "employees": [{ "id": 1, "first_name": "John", "jmbg": "0101990710024", ... }],
  "total_count": 42
}
```

#### GET /api/employees/:id
Get employee by ID. Requires `employees.read` permission.

#### POST /api/employees
Create employee and send activation email. Requires `employees.create` permission.

**Request:**
```json
{
  "first_name": "John",
  "last_name": "Doe",
  "date_of_birth": 946684800,
  "gender": "M",
  "email": "john@example.com",
  "phone": "+381601234567",
  "address": "123 Main St",
  "jmbg": "0101990710024",
  "username": "johndoe",
  "position": "Teller",
  "department": "Retail",
  "role": "EmployeeBasic",
  "active": true
}
```

**Notes:**
- `jmbg` must be exactly 13 digits, unique
- `date_of_birth` is Unix timestamp (int64)
- Account is not activated until the employee sets their password via the activation link
- Activation email is sent automatically

#### PUT /api/employees/:id
Update employee. Requires `employees.update` permission. Cannot edit EmployeeAdmin employees.

**Request (all fields optional):**
```json
{
  "last_name": "Smith",
  "phone": "+381609876543",
  "jmbg": "0101990710025",
  "role": "EmployeeAgent",
  "active": true
}
```

---

## JMBG Field

JMBG (Jedinstveni Matični Broj Građana) is a unique 13-digit Serbian national identification number assigned to each employee.

- Must be exactly 13 digits (no letters or special characters)
- Must be unique across all employees
- Required when creating an employee
- Can be updated via PUT /api/employees/:id

---

## Kafka Topics

| Topic | Direction | Message Type |
|-------|-----------|-------------|
| `notification.send-email` | Services → Notification | `SendEmailMessage` |
| `notification.email-sent` | Notification → Services | `EmailSentMessage` |

**SendEmailMessage:**
```json
{
  "to": "user@example.com",
  "email_type": "ACTIVATION",
  "data": { "first_name": "John", "token": "abc...", "link": "http://frontend/activate?token=abc..." }
}
```

---

## Password Requirements

Passwords must:
- Be 8–32 characters
- Contain at least 2 digits
- Contain at least 1 uppercase letter
- Contain at least 1 lowercase letter

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAY_HTTP_ADDR` | `:8080` | API Gateway listen address |
| `AUTH_GRPC_ADDR` | `localhost:50051` | Auth service gRPC address |
| `USER_GRPC_ADDR` | `localhost:50052` | User service gRPC address |
| `NOTIFICATION_GRPC_ADDR` | `:50053` | Notification service gRPC address |
| `USER_DB_*` / `AUTH_DB_*` | — | PostgreSQL connection (must set) |
| `JWT_SECRET` | — | 256-bit JWT signing secret (must set) |
| `JWT_ACCESS_EXPIRY` | `15m` | Access token lifetime |
| `JWT_REFRESH_EXPIRY` | `168h` | Refresh token lifetime |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `KAFKA_BROKERS` | `localhost:9092` | Kafka broker addresses |
| `SMTP_HOST` / `SMTP_PORT` | `smtp.gmail.com` / `587` | SMTP server |
| `SMTP_USER` / `SMTP_PASSWORD` | — | SMTP credentials (must set) |
| `SMTP_FROM` | — | Sender email address (must set) |
| `FRONTEND_BASE_URL` | `http://localhost:3000` | Base URL for email links |

---

## Running Locally

```bash
# Start all infrastructure and services
make docker-up

# Stop services
make docker-down

# View logs
make docker-logs

# Build all services
make build

# Run all tests
make test

# Regenerate proto files (after editing .proto)
make proto
```

**Individual service builds:**
```bash
cd user-service && go build -o bin/user-service ./cmd
cd auth-service && go build -o bin/auth-service ./cmd
cd api-gateway && go build -o bin/api-gateway ./cmd
cd notification-service && go build -o bin/notification-service ./cmd
```

**Individual service tests:**
```bash
cd user-service && go test ./... -v
cd auth-service && go test ./... -v
cd notification-service && go test ./... -v
cd api-gateway && go test ./... -v
```

---

## Development

### Worktrees
Development uses git worktrees under `.worktrees/`. Create a worktree with:
```bash
git worktree add .worktrees/<branch-name> -b <branch-name>
```

### Proto Changes
After editing any `.proto` file:
```bash
make proto  # Regenerates Go files in contract/authpb/, contract/userpb/, contract/notificationpb/
```

### Database Migrations
No manual migrations needed. GORM auto-migrates on service startup.
