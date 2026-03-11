# Banking Microservices Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Auth, User, and API-Gateway microservices for a banking system in Go.

**Architecture:** Monorepo with 3 independent services communicating via gRPC (sync) and Kafka (async events for notification service). API-Gateway is the only HTTP-facing service, translating REST↔gRPC and enforcing JWT auth. Each service has its own PostgreSQL database.

**Tech Stack:** Go, Gin, Gorm, gRPC/protoc, Kafka (segmentio/kafka-go), JWT (golang-jwt), bcrypt, godotenv, Docker

**Ports:** API-Gateway :8080 (HTTP), Auth-service :50051 (gRPC), User-service :50052 (gRPC)

**Kafka Topics:** `notification.send-email` (consumed by future notification service)

**Roles (hierarchical):**
- `EmployeeBasic` → view clients, basic banking ops
- `EmployeeAgent` → + trade securities (with limits)
- `EmployeeSupervisor` → + no limits, manage OTC/funds/agents
- `EmployeeAdmin` → + manage all employees

---

## File Structure

```
claude-plan/
├── go.work
├── contract/
│   ├── go.mod
│   ├── proto/
│   │   ├── auth/auth.proto
│   │   └── user/user.proto
│   ├── authpb/          (generated)
│   ├── userpb/          (generated)
│   └── kafka/
│       └── messages.go
├── auth-service/
│   ├── go.mod
│   ├── Dockerfile
│   └── cmd/main.go
│   └── internal/
│       ├── config/config.go
│       ├── model/token.go
│       ├── repository/token_repository.go
│       ├── service/auth_service.go
│       ├── service/jwt_service.go
│       ├── handler/grpc_handler.go
│       └── kafka/producer.go
├── user-service/
│   ├── go.mod
│   ├── Dockerfile
│   └── cmd/main.go
│   └── internal/
│       ├── config/config.go
│       ├── model/employee.go
│       ├── repository/employee_repository.go
│       ├── service/employee_service.go
│       ├── service/role_service.go
│       ├── handler/grpc_handler.go
│       └── kafka/producer.go
├── api-gateway/
│   ├── go.mod
│   ├── Dockerfile
│   └── cmd/main.go
│   └── internal/
│       ├── config/config.go
│       ├── middleware/auth.go
│       ├── handler/auth_handler.go
│       ├── handler/employee_handler.go
│       ├── grpc/auth_client.go
│       ├── grpc/user_client.go
│       └── router/router.go
├── docker-compose.yml
├── Makefile
└── .env.example
```

---

## Chunk 1: Project Setup & Contracts

### Task 1: Initialize monorepo and Go workspace

**Files:**
- Create: `go.work`
- Create: `contract/go.mod`
- Create: `auth-service/go.mod`
- Create: `user-service/go.mod`
- Create: `api-gateway/go.mod`

- [ ] **Step 1: Initialize git repo**

```bash
cd claude-plan
git init
```

- [ ] **Step 2: Create go.work file**

```go
// go.work
go 1.23

use (
    ./contract
    ./auth-service
    ./user-service
    ./api-gateway
)
```

- [ ] **Step 3: Create contract module**

```bash
mkdir -p contract/proto/auth contract/proto/user contract/authpb contract/userpb contract/kafka
cd contract && go mod init github.com/claude-plan/contract
```

- [ ] **Step 4: Create service modules**

```bash
cd auth-service && go mod init github.com/claude-plan/auth-service
cd user-service && go mod init github.com/claude-plan/user-service
cd api-gateway && go mod init github.com/claude-plan/api-gateway
```

- [ ] **Step 5: Create directory structure for all services**

```bash
mkdir -p auth-service/cmd auth-service/internal/{config,model,repository,service,handler,kafka}
mkdir -p user-service/cmd user-service/internal/{config,model,repository,service,handler,kafka}
mkdir -p api-gateway/cmd api-gateway/internal/{config,middleware,handler,grpc,router}
```

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "chore: initialize monorepo with go workspace and service scaffolding"
```

---

### Task 2: Define proto files

**Files:**
- Create: `contract/proto/user/user.proto`
- Create: `contract/proto/auth/auth.proto`

- [ ] **Step 7: Write user.proto**

```protobuf
// contract/proto/user/user.proto
syntax = "proto3";

package user;

option go_package = "github.com/claude-plan/contract/userpb;userpb";

service UserService {
  rpc CreateEmployee(CreateEmployeeRequest) returns (EmployeeResponse);
  rpc GetEmployee(GetEmployeeRequest) returns (EmployeeResponse);
  rpc ListEmployees(ListEmployeesRequest) returns (ListEmployeesResponse);
  rpc UpdateEmployee(UpdateEmployeeRequest) returns (EmployeeResponse);
  rpc ValidateCredentials(ValidateCredentialsRequest) returns (ValidateCredentialsResponse);
  rpc GetUserByEmail(GetUserByEmailRequest) returns (UserResponse);
}

message CreateEmployeeRequest {
  string first_name = 1;
  string last_name = 2;
  int64 date_of_birth = 3;
  string gender = 4;
  string email = 5;
  string phone = 6;
  string address = 7;
  string username = 8;
  string position = 9;
  string department = 10;
  string role = 11;
  bool active = 12;
}

message UpdateEmployeeRequest {
  int64 id = 1;
  optional string last_name = 2;
  optional string gender = 3;
  optional string phone = 4;
  optional string address = 5;
  optional string position = 6;
  optional string department = 7;
  optional string role = 8;
  optional bool active = 9;
}

message GetEmployeeRequest {
  int64 id = 1;
}

message GetUserByEmailRequest {
  string email = 1;
}

message ListEmployeesRequest {
  string email_filter = 1;
  string name_filter = 2;
  string position_filter = 3;
  int32 page = 4;
  int32 page_size = 5;
}

message ValidateCredentialsRequest {
  string email = 1;
  string password = 2;
}

message ValidateCredentialsResponse {
  bool valid = 1;
  int64 user_id = 2;
  string email = 3;
  string role = 4;
  repeated string permissions = 5;
}

message EmployeeResponse {
  int64 id = 1;
  string first_name = 2;
  string last_name = 3;
  int64 date_of_birth = 4;
  string gender = 5;
  string email = 6;
  string phone = 7;
  string address = 8;
  string username = 9;
  string position = 10;
  string department = 11;
  bool active = 12;
  string role = 13;
  repeated string permissions = 14;
}

message ListEmployeesResponse {
  repeated EmployeeResponse employees = 1;
  int32 total_count = 2;
}

message UserResponse {
  int64 id = 1;
  string email = 2;
  string role = 3;
  repeated string permissions = 4;
  string password_hash = 5;
  bool active = 6;
}
```

- [ ] **Step 8: Write auth.proto**

```protobuf
// contract/proto/auth/auth.proto
syntax = "proto3";

package auth;

option go_package = "github.com/claude-plan/contract/authpb;authpb";

service AuthService {
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc ValidateToken(ValidateTokenRequest) returns (ValidateTokenResponse);
  rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
  rpc Logout(LogoutRequest) returns (LogoutResponse);
  rpc RequestPasswordReset(PasswordResetRequest) returns (PasswordResetResponse);
  rpc ResetPassword(ResetPasswordRequest) returns (ResetPasswordResponse);
  rpc ActivateAccount(ActivateAccountRequest) returns (ActivateAccountResponse);
  rpc CreateActivationToken(CreateActivationTokenRequest) returns (CreateActivationTokenResponse);
}

message LoginRequest {
  string email = 1;
  string password = 2;
}

message LoginResponse {
  string access_token = 1;
  string refresh_token = 2;
}

message ValidateTokenRequest {
  string token = 1;
}

message ValidateTokenResponse {
  bool valid = 1;
  int64 user_id = 2;
  string email = 3;
  string role = 4;
  repeated string permissions = 5;
}

message RefreshTokenRequest {
  string refresh_token = 1;
}

message RefreshTokenResponse {
  string access_token = 1;
  string refresh_token = 2;
}

message PasswordResetRequest {
  string email = 1;
}

message PasswordResetResponse {
  bool success = 1;
}

message ResetPasswordRequest {
  string token = 1;
  string new_password = 2;
  string confirm_password = 3;
}

message ResetPasswordResponse {
  bool success = 1;
}

message ActivateAccountRequest {
  string token = 1;
  string password = 2;
  string confirm_password = 3;
}

message ActivateAccountResponse {
  bool success = 1;
}

message LogoutRequest {
  string refresh_token = 1;
}

message LogoutResponse {
  bool success = 1;
}

message CreateActivationTokenRequest {
  int64 user_id = 1;
  string email = 2;
  string first_name = 3;
}

message CreateActivationTokenResponse {
  bool success = 1;
}
```

- [ ] **Step 9: Commit**

```bash
git add contract/proto/ && git commit -m "feat: add proto definitions for auth and user services"
```

---

### Task 3: Generate protobuf Go code

**Files:**
- Create: `contract/authpb/*.go` (generated)
- Create: `contract/userpb/*.go` (generated)
- Create: `Makefile`

- [ ] **Step 10: Create Makefile with proto generation target**

```makefile
# Makefile
.PHONY: proto clean

proto:
	protoc -I contract/proto \
		--go_out=contract --go_opt=paths=source_relative \
		--go-grpc_out=contract --go-grpc_opt=paths=source_relative \
		auth/auth.proto user/user.proto
	@# With go_package "...contract/authpb;authpb" and paths=source_relative,
	@# files are generated at contract/auth/ and contract/user/.
	@# Move them to authpb/ and userpb/ for cleaner imports.
	mkdir -p contract/authpb contract/userpb
	mv contract/auth/*.pb.go contract/authpb/ 2>/dev/null || true
	mv contract/user/*.pb.go contract/userpb/ 2>/dev/null || true
	rmdir contract/auth contract/user 2>/dev/null || true

clean:
	rm -f contract/authpb/*.go contract/userpb/*.go
```

**Note:** If the generated files don't land where expected, check `protoc` output. An alternative approach is to place proto files directly inside `contract/authpb/` and `contract/userpb/` and adjust the `-I` flag accordingly. Verify after running `make proto` that `contract/authpb/auth.pb.go` and `contract/userpb/user.pb.go` exist.

- [ ] **Step 11: Run proto generation**

```bash
make proto
```

Expected: `contract/authpb/auth.pb.go`, `contract/authpb/auth_grpc.pb.go`, `contract/userpb/user.pb.go`, `contract/userpb/user_grpc.pb.go` are created.

- [ ] **Step 12: Verify contract module compiles**

```bash
cd contract && go mod tidy && go build ./...
```

Expected: No errors.

- [ ] **Step 13: Commit**

```bash
git add -A && git commit -m "feat: generate protobuf Go code for auth and user services"
```

---

### Task 4: Define Kafka message types

**Files:**
- Create: `contract/kafka/messages.go`

- [ ] **Step 14: Write shared Kafka message types**

```go
// contract/kafka/messages.go
package kafka

const (
    TopicSendEmail = "notification.send-email"
)

type EmailType string

const (
    EmailTypeActivation    EmailType = "ACTIVATION"
    EmailTypePasswordReset EmailType = "PASSWORD_RESET"
    EmailTypeConfirmation  EmailType = "CONFIRMATION"
)

type SendEmailMessage struct {
    To        string    `json:"to"`
    EmailType EmailType `json:"email_type"`
    Data      map[string]string `json:"data"`
}
```

`Data` carries template variables: e.g. `{"token": "abc123", "first_name": "Petar", "link": "https://..."}`.

- [ ] **Step 15: Verify contract module still compiles**

```bash
cd contract && go build ./...
```

- [ ] **Step 16: Commit**

```bash
git add contract/kafka/ && git commit -m "feat: add shared Kafka message types for notification events"
```

---

### Task 5: Create .env.example and config pattern

**Files:**
- Create: `.env.example`

- [ ] **Step 17: Write .env.example**

```env
# .env.example

# User Service DB
USER_DB_HOST=localhost
USER_DB_PORT=5432
USER_DB_USER=postgres
USER_DB_PASSWORD=postgres
USER_DB_NAME=user_db

# Auth Service DB
AUTH_DB_HOST=localhost
AUTH_DB_PORT=5433
AUTH_DB_USER=postgres
AUTH_DB_PASSWORD=postgres
AUTH_DB_NAME=auth_db

# JWT
JWT_SECRET=your-256-bit-secret-change-in-production
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h

# gRPC
AUTH_GRPC_ADDR=localhost:50051
USER_GRPC_ADDR=localhost:50052

# API Gateway
GATEWAY_HTTP_ADDR=:8080

# Kafka
KAFKA_BROKERS=localhost:9092
```

- [ ] **Step 18: Create .gitignore**

```gitignore
# .gitignore
.env
*.exe
*.test
vendor/
```

- [ ] **Step 19: Commit**

```bash
git add .env.example .gitignore && git commit -m "chore: add env example and gitignore"
```

---

## Chunk 2: User Service

### Task 6: User service config

**Files:**
- Create: `user-service/internal/config/config.go`

- [ ] **Step 20: Write config loader**

```go
// user-service/internal/config/config.go
package config

import (
    "os"

    "github.com/joho/godotenv"
)

type Config struct {
    DBHost     string
    DBPort     string
    DBUser     string
    DBPassword string
    DBName     string
    GRPCAddr   string
    KafkaBrokers string
}

func Load() *Config {
    godotenv.Load()
    return &Config{
        DBHost:       getEnv("USER_DB_HOST", "localhost"),
        DBPort:       getEnv("USER_DB_PORT", "5432"),
        DBUser:       getEnv("USER_DB_USER", "postgres"),
        DBPassword:   getEnv("USER_DB_PASSWORD", "postgres"),
        DBName:       getEnv("USER_DB_NAME", "user_db"),
        GRPCAddr:     getEnv("USER_GRPC_ADDR", ":50052"),
        KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
    }
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

func (c *Config) DSN() string {
    return "host=" + c.DBHost +
        " port=" + c.DBPort +
        " user=" + c.DBUser +
        " password=" + c.DBPassword +
        " dbname=" + c.DBName +
        " sslmode=disable"
}
```

- [ ] **Step 21: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add config loader"
```

---

### Task 7: Employee model and role system

**Files:**
- Create: `user-service/internal/model/employee.go`
- Create: `user-service/internal/service/role_service.go`

- [ ] **Step 22: Write employee model**

```go
// user-service/internal/model/employee.go
package model

import "time"

type Employee struct {
    ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
    FirstName    string    `gorm:"not null" json:"first_name"`
    LastName     string    `gorm:"not null" json:"last_name"`
    DateOfBirth  time.Time `gorm:"not null" json:"date_of_birth"`
    Gender       string    `gorm:"size:10" json:"gender"`
    Email        string    `gorm:"uniqueIndex;not null" json:"email"`
    Phone        string    `json:"phone"`
    Address      string    `json:"address"`
    Username     string    `gorm:"uniqueIndex;not null" json:"username"`
    PasswordHash string    `gorm:"not null" json:"-"`
    Salt         string    `gorm:"not null" json:"-"`
    Position     string    `json:"position"`
    Department   string    `json:"department"`
    Active       bool      `gorm:"default:true" json:"active"`
    Role         string    `gorm:"not null;default:'EmployeeBasic'" json:"role"`
    Activated    bool      `gorm:"default:false" json:"-"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}
```

- [ ] **Step 23: Write role service with permission mappings**

```go
// user-service/internal/service/role_service.go
package service

var rolePermissions = map[string][]string{
    "EmployeeBasic": {
        "clients.read",
        "accounts.create",
        "accounts.read",
        "cards.manage",
        "credits.manage",
    },
    "EmployeeAgent": {
        "clients.read",
        "accounts.create",
        "accounts.read",
        "cards.manage",
        "credits.manage",
        "securities.trade",
        "securities.read",
    },
    "EmployeeSupervisor": {
        "clients.read",
        "accounts.create",
        "accounts.read",
        "cards.manage",
        "credits.manage",
        "securities.trade",
        "securities.read",
        "agents.manage",
        "otc.manage",
        "funds.manage",
    },
    "EmployeeAdmin": {
        "clients.read",
        "accounts.create",
        "accounts.read",
        "cards.manage",
        "credits.manage",
        "securities.trade",
        "securities.read",
        "agents.manage",
        "otc.manage",
        "funds.manage",
        "employees.create",
        "employees.update",
        "employees.read",
        "employees.permissions",
    },
}

func ValidRole(role string) bool {
    _, ok := rolePermissions[role]
    return ok
}

func GetPermissions(role string) []string {
    perms, ok := rolePermissions[role]
    if !ok {
        return nil
    }
    result := make([]string, len(perms))
    copy(result, perms)
    return result
}
```

- [ ] **Step 24: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add employee model and role permission mappings"
```

---

### Task 8: Employee repository

**Files:**
- Create: `user-service/internal/repository/employee_repository.go`

- [ ] **Step 25: Write employee repository**

```go
// user-service/internal/repository/employee_repository.go
package repository

import (
    "github.com/claude-plan/user-service/internal/model"
    "gorm.io/gorm"
)

type EmployeeRepository struct {
    db *gorm.DB
}

func NewEmployeeRepository(db *gorm.DB) *EmployeeRepository {
    return &EmployeeRepository{db: db}
}

func (r *EmployeeRepository) Create(emp *model.Employee) error {
    return r.db.Create(emp).Error
}

func (r *EmployeeRepository) GetByID(id int64) (*model.Employee, error) {
    var emp model.Employee
    err := r.db.First(&emp, id).Error
    return &emp, err
}

func (r *EmployeeRepository) GetByEmail(email string) (*model.Employee, error) {
    var emp model.Employee
    err := r.db.Where("email = ?", email).First(&emp).Error
    return &emp, err
}

func (r *EmployeeRepository) Update(emp *model.Employee) error {
    return r.db.Save(emp).Error
}

func (r *EmployeeRepository) List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
    var employees []model.Employee
    var total int64
    q := r.db.Model(&model.Employee{})

    if emailFilter != "" {
        q = q.Where("email ILIKE ?", "%"+emailFilter+"%")
    }
    if nameFilter != "" {
        q = q.Where("first_name ILIKE ? OR last_name ILIKE ?", "%"+nameFilter+"%", "%"+nameFilter+"%")
    }
    if positionFilter != "" {
        q = q.Where("position ILIKE ?", "%"+positionFilter+"%")
    }

    q.Count(&total)

    if page < 1 { page = 1 }
    if pageSize < 1 { pageSize = 20 }
    offset := (page - 1) * pageSize

    err := q.Offset(offset).Limit(pageSize).Find(&employees).Error
    return employees, total, err
}
```

- [ ] **Step 26: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add employee repository with CRUD and filtering"
```

---

### Task 9: Kafka producer for user service

**Files:**
- Create: `user-service/internal/kafka/producer.go`

- [ ] **Step 27: Write Kafka producer**

```go
// user-service/internal/kafka/producer.go
package kafka

import (
    "context"
    "encoding/json"

    kafkago "github.com/segmentio/kafka-go"
    kafkamsg "github.com/claude-plan/contract/kafka"
)

type Producer struct {
    writer *kafkago.Writer
}

func NewProducer(brokers string) *Producer {
    return &Producer{
        writer: &kafkago.Writer{
            Addr:     kafkago.TCP(brokers),
            Balancer: &kafkago.LeastBytes{},
        },
    }
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
    data, err := json.Marshal(msg)
    if err != nil {
        return err
    }
    return p.writer.WriteMessages(ctx, kafkago.Message{
        Topic: kafkamsg.TopicSendEmail,
        Value: data,
    })
}

func (p *Producer) Close() error {
    return p.writer.Close()
}
```

- [ ] **Step 28: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add Kafka producer for notification events"
```

---

### Task 10: Employee service (business logic)

**Files:**
- Create: `user-service/internal/service/employee_service.go`

- [ ] **Step 29: Write employee service**

```go
// user-service/internal/service/employee_service.go
package service

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "regexp"

    "golang.org/x/crypto/bcrypt"

    kafkamsg "github.com/claude-plan/contract/kafka"
    "github.com/claude-plan/user-service/internal/model"
    "github.com/claude-plan/user-service/internal/repository"
    kafkaprod "github.com/claude-plan/user-service/internal/kafka"
)

var passwordRegex = regexp.MustCompile(`^(?=(?:.*[0-9]){2,})(?=.*[a-z])(?=.*[A-Z]).{8,32}$`)

type EmployeeService struct {
    repo     *repository.EmployeeRepository
    producer *kafkaprod.Producer
}

func NewEmployeeService(repo *repository.EmployeeRepository, producer *kafkaprod.Producer) *EmployeeService {
    return &EmployeeService{repo: repo, producer: producer}
}

func (s *EmployeeService) CreateEmployee(ctx context.Context, emp *model.Employee) error {
    if !ValidRole(emp.Role) {
        return errors.New("invalid role")
    }

    salt := generateSalt()
    emp.Salt = salt
    emp.PasswordHash = "" // no password until activation
    emp.Activated = false

    if err := s.repo.Create(emp); err != nil {
        return fmt.Errorf("create employee: %w", err)
    }

    // NOTE: Activation token creation is handled by the API Gateway.
    // After creating the employee, the gateway calls auth-service's
    // CreateActivationToken RPC, which stores the token and publishes
    // the Kafka email event. This keeps token storage in auth-service's DB.
    return nil
}

func (s *EmployeeService) GetEmployee(id int64) (*model.Employee, error) {
    return s.repo.GetByID(id)
}

func (s *EmployeeService) GetByEmail(email string) (*model.Employee, error) {
    return s.repo.GetByEmail(email)
}

func (s *EmployeeService) ListEmployees(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
    return s.repo.List(emailFilter, nameFilter, positionFilter, page, pageSize)
}

func (s *EmployeeService) UpdateEmployee(id int64, updates map[string]interface{}) (*model.Employee, error) {
    emp, err := s.repo.GetByID(id)
    if err != nil {
        return nil, err
    }

    if role, ok := updates["role"].(string); ok {
        if !ValidRole(role) {
            return nil, errors.New("invalid role")
        }
        emp.Role = role
    }
    if v, ok := updates["last_name"].(string); ok { emp.LastName = v }
    if v, ok := updates["gender"].(string); ok { emp.Gender = v }
    if v, ok := updates["phone"].(string); ok { emp.Phone = v }
    if v, ok := updates["address"].(string); ok { emp.Address = v }
    if v, ok := updates["position"].(string); ok { emp.Position = v }
    if v, ok := updates["department"].(string); ok { emp.Department = v }
    if v, ok := updates["active"].(*bool); ok { emp.Active = *v }
    if v, ok := updates["active"].(bool); ok { emp.Active = v }

    if err := s.repo.Update(emp); err != nil {
        return nil, err
    }
    return emp, nil
}

func (s *EmployeeService) ValidateCredentials(email, password string) (*model.Employee, bool) {
    emp, err := s.repo.GetByEmail(email)
    if err != nil || !emp.Active || !emp.Activated {
        return nil, false
    }
    if err := bcrypt.CompareHashAndPassword([]byte(emp.PasswordHash), []byte(password)); err != nil {
        return nil, false
    }
    return emp, true
}

func ValidatePassword(password string) error {
    if !passwordRegex.MatchString(password) {
        return errors.New("password must be 8-32 chars with at least 2 digits, 1 uppercase and 1 lowercase letter")
    }
    return nil
}

func HashPassword(password string) (string, error) {
    bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    return string(bytes), err
}

func generateSalt() string {
    b := make([]byte, 16)
    rand.Read(b)
    return hex.EncodeToString(b)
}

func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return hex.EncodeToString(b)
}
```

- [ ] **Step 30: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add employee service with business logic and password validation"
```

---

### Task 11: User service gRPC handler

**Files:**
- Create: `user-service/internal/handler/grpc_handler.go`

- [ ] **Step 31: Write gRPC handler**

```go
// user-service/internal/handler/grpc_handler.go
package handler

import (
    "context"
    "time"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    pb "github.com/claude-plan/contract/userpb"
    "github.com/claude-plan/user-service/internal/model"
    "github.com/claude-plan/user-service/internal/service"
)

type UserGRPCHandler struct {
    pb.UnimplementedUserServiceServer
    empService *service.EmployeeService
}

func NewUserGRPCHandler(empService *service.EmployeeService) *UserGRPCHandler {
    return &UserGRPCHandler{empService: empService}
}

func (h *UserGRPCHandler) CreateEmployee(ctx context.Context, req *pb.CreateEmployeeRequest) (*pb.EmployeeResponse, error) {
    dob := time.Unix(req.DateOfBirth, 0)
    emp := &model.Employee{
        FirstName:   req.FirstName,
        LastName:    req.LastName,
        DateOfBirth: dob,
        Gender:      req.Gender,
        Email:       req.Email,
        Phone:       req.Phone,
        Address:     req.Address,
        Username:    req.Username,
        Position:    req.Position,
        Department:  req.Department,
        Role:        req.Role,
        Active:      req.Active,
    }

    if err := h.empService.CreateEmployee(ctx, emp); err != nil {
        return nil, status.Errorf(codes.Internal, "failed to create employee: %v", err)
    }
    return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) GetEmployee(ctx context.Context, req *pb.GetEmployeeRequest) (*pb.EmployeeResponse, error) {
    emp, err := h.empService.GetEmployee(req.Id)
    if err != nil {
        return nil, status.Errorf(codes.NotFound, "employee not found")
    }
    return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) ListEmployees(ctx context.Context, req *pb.ListEmployeesRequest) (*pb.ListEmployeesResponse, error) {
    employees, total, err := h.empService.ListEmployees(
        req.EmailFilter, req.NameFilter, req.PositionFilter,
        int(req.Page), int(req.PageSize),
    )
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to list employees: %v", err)
    }

    resp := &pb.ListEmployeesResponse{TotalCount: int32(total)}
    for _, emp := range employees {
        resp.Employees = append(resp.Employees, toEmployeeResponse(&emp))
    }
    return resp, nil
}

func (h *UserGRPCHandler) UpdateEmployee(ctx context.Context, req *pb.UpdateEmployeeRequest) (*pb.EmployeeResponse, error) {
    updates := make(map[string]interface{})
    if req.LastName != nil { updates["last_name"] = *req.LastName }
    if req.Gender != nil { updates["gender"] = *req.Gender }
    if req.Phone != nil { updates["phone"] = *req.Phone }
    if req.Address != nil { updates["address"] = *req.Address }
    if req.Position != nil { updates["position"] = *req.Position }
    if req.Department != nil { updates["department"] = *req.Department }
    if req.Role != nil { updates["role"] = *req.Role }
    if req.Active != nil { updates["active"] = *req.Active }

    emp, err := h.empService.UpdateEmployee(req.Id, updates)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to update: %v", err)
    }
    return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) ValidateCredentials(ctx context.Context, req *pb.ValidateCredentialsRequest) (*pb.ValidateCredentialsResponse, error) {
    emp, valid := h.empService.ValidateCredentials(req.Email, req.Password)
    if !valid {
        return &pb.ValidateCredentialsResponse{Valid: false}, nil
    }
    return &pb.ValidateCredentialsResponse{
        Valid:       true,
        UserId:      emp.ID,
        Email:       emp.Email,
        Role:        emp.Role,
        Permissions: service.GetPermissions(emp.Role),
    }, nil
}

func (h *UserGRPCHandler) GetUserByEmail(ctx context.Context, req *pb.GetUserByEmailRequest) (*pb.UserResponse, error) {
    emp, err := h.empService.GetByEmail(req.Email)
    if err != nil {
        return nil, status.Errorf(codes.NotFound, "user not found")
    }
    return &pb.UserResponse{
        Id:           emp.ID,
        Email:        emp.Email,
        Role:         emp.Role,
        Permissions:  service.GetPermissions(emp.Role),
        PasswordHash: emp.PasswordHash,
        Active:       emp.Active,
    }, nil
}

func toEmployeeResponse(emp *model.Employee) *pb.EmployeeResponse {
    return &pb.EmployeeResponse{
        Id:          emp.ID,
        FirstName:   emp.FirstName,
        LastName:    emp.LastName,
        DateOfBirth: emp.DateOfBirth.Unix(),
        Gender:      emp.Gender,
        Email:       emp.Email,
        Phone:       emp.Phone,
        Address:     emp.Address,
        Username:    emp.Username,
        Position:    emp.Position,
        Department:  emp.Department,
        Active:      emp.Active,
        Role:        emp.Role,
        Permissions: service.GetPermissions(emp.Role),
    }
}
```

- [ ] **Step 32: Commit**

```bash
git add user-service/ && git commit -m "feat(user): add gRPC handler for user service"
```

---

### Task 12: User service main entry point

**Files:**
- Create: `user-service/cmd/main.go`

- [ ] **Step 33: Write main.go**

```go
// user-service/cmd/main.go
package main

import (
    "fmt"
    "log"
    "net"

    "google.golang.org/grpc"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"

    pb "github.com/claude-plan/contract/userpb"
    "github.com/claude-plan/user-service/internal/config"
    "github.com/claude-plan/user-service/internal/handler"
    kafkaprod "github.com/claude-plan/user-service/internal/kafka"
    "github.com/claude-plan/user-service/internal/model"
    "github.com/claude-plan/user-service/internal/repository"
    "github.com/claude-plan/user-service/internal/service"
)

func main() {
    cfg := config.Load()

    db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
    if err != nil {
        log.Fatalf("failed to connect to database: %v", err)
    }
    if err := db.AutoMigrate(&model.Employee{}); err != nil {
        log.Fatalf("failed to migrate: %v", err)
    }

    producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
    defer producer.Close()

    repo := repository.NewEmployeeRepository(db)
    empService := service.NewEmployeeService(repo, producer)
    grpcHandler := handler.NewUserGRPCHandler(empService)

    lis, err := net.Listen("tcp", cfg.GRPCAddr)
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }

    s := grpc.NewServer()
    pb.RegisterUserServiceServer(s, grpcHandler)

    fmt.Printf("User service listening on %s\n", cfg.GRPCAddr)
    if err := s.Serve(lis); err != nil {
        log.Fatalf("failed to serve: %v", err)
    }
}
```

- [ ] **Step 34: Run go mod tidy for user-service**

```bash
cd user-service && go mod tidy
```

Expected: Dependencies resolved.

- [ ] **Step 35: Verify user-service compiles**

```bash
cd user-service && go build ./...
```

Expected: No errors.

- [ ] **Step 36: Commit**

```bash
git add -A && git commit -m "feat(user): add main entry point and wire dependencies"
```

---

## Chunk 3: Auth Service

### Task 13: Auth service config

**Files:**
- Create: `auth-service/internal/config/config.go`

- [ ] **Step 37: Write config loader**

```go
// auth-service/internal/config/config.go
package config

import (
    "os"
    "time"

    "github.com/joho/godotenv"
)

type Config struct {
    DBHost         string
    DBPort         string
    DBUser         string
    DBPassword     string
    DBName         string
    GRPCAddr       string
    UserGRPCAddr   string
    KafkaBrokers   string
    JWTSecret      string
    AccessExpiry   time.Duration
    RefreshExpiry  time.Duration
}

func Load() *Config {
    godotenv.Load()

    accessExp, _ := time.ParseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"))
    refreshExp, _ := time.ParseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"))

    return &Config{
        DBHost:       getEnv("AUTH_DB_HOST", "localhost"),
        DBPort:       getEnv("AUTH_DB_PORT", "5433"),
        DBUser:       getEnv("AUTH_DB_USER", "postgres"),
        DBPassword:   getEnv("AUTH_DB_PASSWORD", "postgres"),
        DBName:       getEnv("AUTH_DB_NAME", "auth_db"),
        GRPCAddr:     getEnv("AUTH_GRPC_ADDR", ":50051"),
        UserGRPCAddr: getEnv("USER_GRPC_ADDR", "localhost:50052"),
        KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
        JWTSecret:    getEnv("JWT_SECRET", "change-me"),
        AccessExpiry: accessExp,
        RefreshExpiry: refreshExp,
    }
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

func (c *Config) DSN() string {
    return "host=" + c.DBHost +
        " port=" + c.DBPort +
        " user=" + c.DBUser +
        " password=" + c.DBPassword +
        " dbname=" + c.DBName +
        " sslmode=disable"
}
```

- [ ] **Step 38: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add config loader"
```

---

### Task 14: Auth token models

**Files:**
- Create: `auth-service/internal/model/token.go`

- [ ] **Step 39: Write token models**

```go
// auth-service/internal/model/token.go
package model

import "time"

type RefreshToken struct {
    ID        int64     `gorm:"primaryKey;autoIncrement"`
    UserID    int64     `gorm:"not null;index"`
    Token     string    `gorm:"uniqueIndex;not null"`
    ExpiresAt time.Time `gorm:"not null"`
    Revoked   bool      `gorm:"default:false"`
    CreatedAt time.Time
}

type ActivationToken struct {
    ID        int64     `gorm:"primaryKey;autoIncrement"`
    UserID    int64     `gorm:"not null;index"`
    Token     string    `gorm:"uniqueIndex;not null"`
    ExpiresAt time.Time `gorm:"not null"`
    Used      bool      `gorm:"default:false"`
    CreatedAt time.Time
}

type PasswordResetToken struct {
    ID        int64     `gorm:"primaryKey;autoIncrement"`
    UserID    int64     `gorm:"not null;index"`
    Token     string    `gorm:"uniqueIndex;not null"`
    ExpiresAt time.Time `gorm:"not null"`
    Used      bool      `gorm:"default:false"`
    CreatedAt time.Time
}
```

- [ ] **Step 40: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add token models"
```

---

### Task 15: Token repository

**Files:**
- Create: `auth-service/internal/repository/token_repository.go`

- [ ] **Step 41: Write token repository**

```go
// auth-service/internal/repository/token_repository.go
package repository

import (
    "github.com/claude-plan/auth-service/internal/model"
    "gorm.io/gorm"
)

type TokenRepository struct {
    db *gorm.DB
}

func NewTokenRepository(db *gorm.DB) *TokenRepository {
    return &TokenRepository{db: db}
}

// Refresh tokens
func (r *TokenRepository) CreateRefreshToken(t *model.RefreshToken) error {
    return r.db.Create(t).Error
}

func (r *TokenRepository) GetRefreshToken(token string) (*model.RefreshToken, error) {
    var t model.RefreshToken
    err := r.db.Where("token = ? AND revoked = false", token).First(&t).Error
    return &t, err
}

func (r *TokenRepository) RevokeRefreshToken(token string) error {
    return r.db.Model(&model.RefreshToken{}).Where("token = ?", token).Update("revoked", true).Error
}

func (r *TokenRepository) RevokeAllForUser(userID int64) error {
    return r.db.Model(&model.RefreshToken{}).Where("user_id = ?", userID).Update("revoked", true).Error
}

// Activation tokens
func (r *TokenRepository) CreateActivationToken(t *model.ActivationToken) error {
    return r.db.Create(t).Error
}

func (r *TokenRepository) GetActivationToken(token string) (*model.ActivationToken, error) {
    var t model.ActivationToken
    err := r.db.Where("token = ? AND used = false", token).First(&t).Error
    return &t, err
}

func (r *TokenRepository) MarkActivationUsed(token string) error {
    return r.db.Model(&model.ActivationToken{}).Where("token = ?", token).Update("used", true).Error
}

// Password reset tokens
func (r *TokenRepository) CreatePasswordResetToken(t *model.PasswordResetToken) error {
    return r.db.Create(t).Error
}

func (r *TokenRepository) GetPasswordResetToken(token string) (*model.PasswordResetToken, error) {
    var t model.PasswordResetToken
    err := r.db.Where("token = ? AND used = false", token).First(&t).Error
    return &t, err
}

func (r *TokenRepository) MarkPasswordResetUsed(token string) error {
    return r.db.Model(&model.PasswordResetToken{}).Where("token = ?", token).Update("used", true).Error
}
```

- [ ] **Step 42: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add token repository"
```

---

### Task 16: JWT service

**Files:**
- Create: `auth-service/internal/service/jwt_service.go`

- [ ] **Step 43: Write JWT service**

```go
// auth-service/internal/service/jwt_service.go
package service

import (
    "errors"
    "time"

    "github.com/golang-jwt/jwt/v5"
)

type Claims struct {
    UserID      int64    `json:"user_id"`
    Email       string   `json:"email"`
    Role        string   `json:"role"`
    Permissions []string `json:"permissions"`
    jwt.RegisteredClaims
}

type JWTService struct {
    secret       []byte
    accessExpiry time.Duration
}

func NewJWTService(secret string, accessExpiry time.Duration) *JWTService {
    return &JWTService{
        secret:       []byte(secret),
        accessExpiry: accessExpiry,
    }
}

func (s *JWTService) GenerateAccessToken(userID int64, email, role string, permissions []string) (string, error) {
    claims := &Claims{
        UserID:      userID,
        Email:       email,
        Role:        role,
        Permissions: permissions,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
            IssuedAt:  jwt.NewNumericDate(time.Now()),
        },
    }
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString(s.secret)
}

func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
    token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
        if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, errors.New("unexpected signing method")
        }
        return s.secret, nil
    })
    if err != nil {
        return nil, err
    }
    claims, ok := token.Claims.(*Claims)
    if !ok || !token.Valid {
        return nil, errors.New("invalid token")
    }
    return claims, nil
}
```

- [ ] **Step 44: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add JWT service for token generation and validation"
```

---

### Task 17: Auth service (business logic)

**Files:**
- Create: `auth-service/internal/service/auth_service.go`
- Create: `auth-service/internal/kafka/producer.go`

- [ ] **Step 45: Write Kafka producer (same pattern as user-service)**

```go
// auth-service/internal/kafka/producer.go
package kafka

import (
    "context"
    "encoding/json"

    kafkago "github.com/segmentio/kafka-go"
    kafkamsg "github.com/claude-plan/contract/kafka"
)

type Producer struct {
    writer *kafkago.Writer
}

func NewProducer(brokers string) *Producer {
    return &Producer{
        writer: &kafkago.Writer{
            Addr:     kafkago.TCP(brokers),
            Balancer: &kafkago.LeastBytes{},
        },
    }
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
    data, err := json.Marshal(msg)
    if err != nil {
        return err
    }
    return p.writer.WriteMessages(ctx, kafkago.Message{
        Topic: kafkamsg.TopicSendEmail,
        Value: data,
    })
}

func (p *Producer) Close() error {
    return p.writer.Close()
}
```

- [ ] **Step 46: Write auth service**

```go
// auth-service/internal/service/auth_service.go
package service

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "time"

    "golang.org/x/crypto/bcrypt"

    kafkamsg "github.com/claude-plan/contract/kafka"
    userpb "github.com/claude-plan/contract/userpb"
    "github.com/claude-plan/auth-service/internal/model"
    "github.com/claude-plan/auth-service/internal/repository"
    kafkaprod "github.com/claude-plan/auth-service/internal/kafka"
)

type AuthService struct {
    tokenRepo    *repository.TokenRepository
    jwtService   *JWTService
    userClient   userpb.UserServiceClient
    producer     *kafkaprod.Producer
    refreshExp   time.Duration
}

func NewAuthService(
    tokenRepo *repository.TokenRepository,
    jwtService *JWTService,
    userClient userpb.UserServiceClient,
    producer *kafkaprod.Producer,
    refreshExp time.Duration,
) *AuthService {
    return &AuthService{
        tokenRepo:  tokenRepo,
        jwtService: jwtService,
        userClient: userClient,
        producer:   producer,
        refreshExp: refreshExp,
    }
}

func (s *AuthService) Login(ctx context.Context, email, password string) (string, string, error) {
    resp, err := s.userClient.ValidateCredentials(ctx, &userpb.ValidateCredentialsRequest{
        Email:    email,
        Password: password,
    })
    if err != nil || !resp.Valid {
        return "", "", errors.New("invalid credentials")
    }

    accessToken, err := s.jwtService.GenerateAccessToken(resp.UserId, resp.Email, resp.Role, resp.Permissions)
    if err != nil {
        return "", "", err
    }

    refreshToken := generateToken()
    if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
        UserID:    resp.UserId,
        Token:     refreshToken,
        ExpiresAt: time.Now().Add(s.refreshExp),
    }); err != nil {
        return "", "", err
    }

    return accessToken, refreshToken, nil
}

func (s *AuthService) ValidateToken(tokenString string) (*Claims, error) {
    return s.jwtService.ValidateToken(tokenString)
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (string, string, error) {
    rt, err := s.tokenRepo.GetRefreshToken(refreshTokenStr)
    if err != nil {
        return "", "", errors.New("invalid refresh token")
    }
    if time.Now().After(rt.ExpiresAt) {
        return "", "", errors.New("refresh token expired")
    }

    // Revoke old refresh token (rotation)
    _ = s.tokenRepo.RevokeRefreshToken(refreshTokenStr)

    // Fetch fresh user data to get current role/permissions
    userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: rt.UserID})
    if err != nil {
        return "", "", errors.New("user not found")
    }

    accessToken, err := s.jwtService.GenerateAccessToken(
        userResp.Id, userResp.Email, userResp.Role, userResp.Permissions,
    )
    if err != nil {
        return "", "", err
    }

    newRefreshToken := generateToken()
    if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
        UserID:    rt.UserID,
        Token:     newRefreshToken,
        ExpiresAt: time.Now().Add(s.refreshExp),
    }); err != nil {
        return "", "", err
    }

    return accessToken, newRefreshToken, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
    return s.tokenRepo.RevokeRefreshToken(refreshTokenStr)
}

func (s *AuthService) CreateActivationToken(ctx context.Context, userID int64, email, firstName string) error {
    token := generateToken()
    if err := s.tokenRepo.CreateActivationToken(&model.ActivationToken{
        UserID:    userID,
        Token:     token,
        ExpiresAt: time.Now().Add(24 * time.Hour),
    }); err != nil {
        return err
    }

    return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
        To:        email,
        EmailType: kafkamsg.EmailTypeActivation,
        Data: map[string]string{
            "token":      token,
            "first_name": firstName,
        },
    })
}

func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) error {
    user, err := s.userClient.GetUserByEmail(ctx, &userpb.GetUserByEmailRequest{Email: email})
    if err != nil {
        return nil // Don't reveal if email exists
    }

    token := generateToken()
    if err := s.tokenRepo.CreatePasswordResetToken(&model.PasswordResetToken{
        UserID:    user.Id,
        Token:     token,
        ExpiresAt: time.Now().Add(1 * time.Hour),
    }); err != nil {
        return err
    }

    return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
        To:        email,
        EmailType: kafkamsg.EmailTypePasswordReset,
        Data:      map[string]string{"token": token},
    })
}

func (s *AuthService) ResetPassword(ctx context.Context, tokenStr, newPassword, confirmPassword string) error {
    if newPassword != confirmPassword {
        return errors.New("passwords do not match")
    }
    if err := validatePassword(newPassword); err != nil {
        return err
    }

    prt, err := s.tokenRepo.GetPasswordResetToken(tokenStr)
    if err != nil {
        return errors.New("invalid or expired token")
    }
    if time.Now().After(prt.ExpiresAt) {
        return errors.New("token expired")
    }

    hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
    if err != nil {
        return err
    }

    _, err = s.userClient.SetPassword(ctx, &userpb.SetPasswordRequest{
        UserId:       prt.UserID,
        PasswordHash: string(hash),
    })
    if err != nil {
        return fmt.Errorf("failed to set password: %w", err)
    }

    _ = s.tokenRepo.MarkPasswordResetUsed(tokenStr)
    _ = s.tokenRepo.RevokeAllForUser(prt.UserID)

    return nil
}

func (s *AuthService) ActivateAccount(ctx context.Context, tokenStr, password, confirmPassword string) error {
    if password != confirmPassword {
        return errors.New("passwords do not match")
    }
    if err := validatePassword(password); err != nil {
        return err
    }

    at, err := s.tokenRepo.GetActivationToken(tokenStr)
    if err != nil {
        return errors.New("invalid or expired activation token")
    }
    if time.Now().After(at.ExpiresAt) {
        return errors.New("token expired")
    }

    hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return err
    }

    _, err = s.userClient.SetPassword(ctx, &userpb.SetPasswordRequest{
        UserId:       at.UserID,
        PasswordHash: string(hash),
    })
    if err != nil {
        return fmt.Errorf("failed to set password: %w", err)
    }

    _ = s.tokenRepo.MarkActivationUsed(tokenStr)

    // Send confirmation email
    user, _ := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: at.UserID})
    if user != nil {
        _ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
            To:        user.Email,
            EmailType: kafkamsg.EmailTypeConfirmation,
            Data:      map[string]string{"first_name": user.FirstName},
        })
    }

    return nil
}

func validatePassword(password string) error {
    if len(password) < 8 || len(password) > 32 {
        return errors.New("password must be 8-32 characters")
    }
    digits := 0
    hasUpper := false
    hasLower := false
    for _, c := range password {
        switch {
        case c >= '0' && c <= '9':
            digits++
        case c >= 'A' && c <= 'Z':
            hasUpper = true
        case c >= 'a' && c <= 'z':
            hasLower = true
        }
    }
    if digits < 2 || !hasUpper || !hasLower {
        return errors.New("password must have at least 2 digits, 1 uppercase and 1 lowercase letter")
    }
    return nil
}

func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return hex.EncodeToString(b)
}
```

**Important note:** The proto needs a `SetPassword` RPC added to `UserService` to allow auth-service to update passwords. Add to `user.proto`:

```protobuf
rpc SetPassword(SetPasswordRequest) returns (SetPasswordResponse);

message SetPasswordRequest {
  int64 user_id = 1;
  string password_hash = 2;
}

message SetPasswordResponse {
  bool success = 1;
}
```

And implement in user-service handler + repository. This is addressed in Task 18.

- [ ] **Step 47: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add auth service with login, token refresh, password reset, activation"
```

---

### Task 18: Add SetPassword RPC to user service

**Files:**
- Modify: `contract/proto/user/user.proto`
- Modify: `user-service/internal/handler/grpc_handler.go`
- Modify: `user-service/internal/repository/employee_repository.go`

- [ ] **Step 48: Add SetPassword to user.proto**

Add to the `UserService` service block:
```protobuf
rpc SetPassword(SetPasswordRequest) returns (SetPasswordResponse);
```

Add messages:
```protobuf
message SetPasswordRequest {
  int64 user_id = 1;
  string password_hash = 2;
}

message SetPasswordResponse {
  bool success = 1;
}
```

- [ ] **Step 49: Regenerate proto code**

```bash
make proto
```

- [ ] **Step 50: Add SetPassword to employee repository**

Add to `employee_repository.go`:
```go
func (r *EmployeeRepository) SetPassword(userID int64, passwordHash string) error {
    return r.db.Model(&model.Employee{}).Where("id = ?", userID).
        Updates(map[string]interface{}{"password_hash": passwordHash, "activated": true}).Error
}
```

- [ ] **Step 51: Add SetPassword to gRPC handler**

Add to `grpc_handler.go`:
```go
func (h *UserGRPCHandler) SetPassword(ctx context.Context, req *pb.SetPasswordRequest) (*pb.SetPasswordResponse, error) {
    if err := h.empService.SetPassword(req.UserId, req.PasswordHash); err != nil {
        return nil, status.Errorf(codes.Internal, "failed to set password: %v", err)
    }
    return &pb.SetPasswordResponse{Success: true}, nil
}
```

Add `SetPassword` to `EmployeeService`:
```go
func (s *EmployeeService) SetPassword(userID int64, hash string) error {
    return s.repo.SetPassword(userID, hash)
}
```

- [ ] **Step 52: Verify both services compile**

```bash
cd user-service && go mod tidy && go build ./...
cd auth-service && go mod tidy && go build ./...
```

- [ ] **Step 53: Commit**

```bash
git add -A && git commit -m "feat: add SetPassword RPC for account activation and password reset"
```

---

### Task 19: Auth service gRPC handler

**Files:**
- Create: `auth-service/internal/handler/grpc_handler.go`

- [ ] **Step 54: Write auth gRPC handler**

```go
// auth-service/internal/handler/grpc_handler.go
package handler

import (
    "context"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    pb "github.com/claude-plan/contract/authpb"
    "github.com/claude-plan/auth-service/internal/service"
)

type AuthGRPCHandler struct {
    pb.UnimplementedAuthServiceServer
    authService *service.AuthService
}

func NewAuthGRPCHandler(authService *service.AuthService) *AuthGRPCHandler {
    return &AuthGRPCHandler{authService: authService}
}

func (h *AuthGRPCHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
    access, refresh, err := h.authService.Login(ctx, req.Email, req.Password)
    if err != nil {
        return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
    }
    return &pb.LoginResponse{
        AccessToken:  access,
        RefreshToken: refresh,
    }, nil
}

func (h *AuthGRPCHandler) ValidateToken(ctx context.Context, req *pb.ValidateTokenRequest) (*pb.ValidateTokenResponse, error) {
    claims, err := h.authService.ValidateToken(req.Token)
    if err != nil {
        return &pb.ValidateTokenResponse{Valid: false}, nil
    }
    return &pb.ValidateTokenResponse{
        Valid:       true,
        UserId:      claims.UserID,
        Email:       claims.Email,
        Role:        claims.Role,
        Permissions: claims.Permissions,
    }, nil
}

func (h *AuthGRPCHandler) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
    access, refresh, err := h.authService.RefreshToken(ctx, req.RefreshToken)
    if err != nil {
        return nil, status.Errorf(codes.Unauthenticated, "invalid refresh token")
    }
    return &pb.RefreshTokenResponse{
        AccessToken:  access,
        RefreshToken: refresh,
    }, nil
}

func (h *AuthGRPCHandler) RequestPasswordReset(ctx context.Context, req *pb.PasswordResetRequest) (*pb.PasswordResetResponse, error) {
    err := h.authService.RequestPasswordReset(ctx, req.Email)
    // Always return success to not leak email existence
    if err != nil {
        return &pb.PasswordResetResponse{Success: true}, nil
    }
    return &pb.PasswordResetResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordResponse, error) {
    if err := h.authService.ResetPassword(ctx, req.Token, req.NewPassword, req.ConfirmPassword); err != nil {
        return nil, status.Errorf(codes.InvalidArgument, "%v", err)
    }
    return &pb.ResetPasswordResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) ActivateAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
    if err := h.authService.ActivateAccount(ctx, req.Token, req.Password, req.ConfirmPassword); err != nil {
        return nil, status.Errorf(codes.InvalidArgument, "%v", err)
    }
    return &pb.ActivateAccountResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
    if err := h.authService.Logout(ctx, req.RefreshToken); err != nil {
        return nil, status.Errorf(codes.Internal, "%v", err)
    }
    return &pb.LogoutResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) CreateActivationToken(ctx context.Context, req *pb.CreateActivationTokenRequest) (*pb.CreateActivationTokenResponse, error) {
    if err := h.authService.CreateActivationToken(ctx, req.UserId, req.Email, req.FirstName); err != nil {
        return nil, status.Errorf(codes.Internal, "%v", err)
    }
    return &pb.CreateActivationTokenResponse{Success: true}, nil
}
```

- [ ] **Step 55: Commit**

```bash
git add auth-service/ && git commit -m "feat(auth): add gRPC handler"
```

---

### Task 20: Auth service main entry point

**Files:**
- Create: `auth-service/cmd/main.go`

- [ ] **Step 56: Write main.go**

```go
// auth-service/cmd/main.go
package main

import (
    "fmt"
    "log"
    "net"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"

    authpb "github.com/claude-plan/contract/authpb"
    userpb "github.com/claude-plan/contract/userpb"
    "github.com/claude-plan/auth-service/internal/config"
    "github.com/claude-plan/auth-service/internal/handler"
    kafkaprod "github.com/claude-plan/auth-service/internal/kafka"
    "github.com/claude-plan/auth-service/internal/model"
    "github.com/claude-plan/auth-service/internal/repository"
    "github.com/claude-plan/auth-service/internal/service"
)

func main() {
    cfg := config.Load()

    // Database
    db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
    if err != nil {
        log.Fatalf("failed to connect to database: %v", err)
    }
    if err := db.AutoMigrate(
        &model.RefreshToken{},
        &model.ActivationToken{},
        &model.PasswordResetToken{},
    ); err != nil {
        log.Fatalf("failed to migrate: %v", err)
    }

    // gRPC client to user-service
    userConn, err := grpc.NewClient(cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("failed to connect to user service: %v", err)
    }
    defer userConn.Close()
    userClient := userpb.NewUserServiceClient(userConn)

    // Kafka
    producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
    defer producer.Close()

    // Services
    tokenRepo := repository.NewTokenRepository(db)
    jwtService := service.NewJWTService(cfg.JWTSecret, cfg.AccessExpiry)
    authService := service.NewAuthService(tokenRepo, jwtService, userClient, producer, cfg.RefreshExpiry)
    grpcHandler := handler.NewAuthGRPCHandler(authService)

    // gRPC server
    lis, err := net.Listen("tcp", cfg.GRPCAddr)
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }

    s := grpc.NewServer()
    authpb.RegisterAuthServiceServer(s, grpcHandler)

    fmt.Printf("Auth service listening on %s\n", cfg.GRPCAddr)
    if err := s.Serve(lis); err != nil {
        log.Fatalf("failed to serve: %v", err)
    }
}
```

- [ ] **Step 57: Verify auth-service compiles**

```bash
cd auth-service && go mod tidy && go build ./...
```

- [ ] **Step 58: Commit**

```bash
git add -A && git commit -m "feat(auth): add main entry point and wire dependencies"
```

---

## Chunk 4: API Gateway

### Task 21: Gateway config

**Files:**
- Create: `api-gateway/internal/config/config.go`

- [ ] **Step 59: Write config loader**

```go
// api-gateway/internal/config/config.go
package config

import (
    "os"

    "github.com/joho/godotenv"
)

type Config struct {
    HTTPAddr     string
    AuthGRPCAddr string
    UserGRPCAddr string
}

func Load() *Config {
    godotenv.Load()
    return &Config{
        HTTPAddr:     getEnv("GATEWAY_HTTP_ADDR", ":8080"),
        AuthGRPCAddr: getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
        UserGRPCAddr: getEnv("USER_GRPC_ADDR", "localhost:50052"),
    }
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

- [ ] **Step 60: Commit**

```bash
git add api-gateway/ && git commit -m "feat(gateway): add config loader"
```

---

### Task 22: gRPC clients

**Files:**
- Create: `api-gateway/internal/grpc/auth_client.go`
- Create: `api-gateway/internal/grpc/user_client.go`

- [ ] **Step 61: Write auth gRPC client**

```go
// api-gateway/internal/grpc/auth_client.go
package grpc

import (
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    authpb "github.com/claude-plan/contract/authpb"
)

func NewAuthClient(addr string) (authpb.AuthServiceClient, *grpc.ClientConn, error) {
    conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        return nil, nil, err
    }
    return authpb.NewAuthServiceClient(conn), conn, nil
}
```

- [ ] **Step 62: Write user gRPC client**

```go
// api-gateway/internal/grpc/user_client.go
package grpc

import (
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    userpb "github.com/claude-plan/contract/userpb"
)

func NewUserClient(addr string) (userpb.UserServiceClient, *grpc.ClientConn, error) {
    conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        return nil, nil, err
    }
    return userpb.NewUserServiceClient(conn), conn, nil
}
```

- [ ] **Step 63: Commit**

```bash
git add api-gateway/ && git commit -m "feat(gateway): add gRPC client wrappers for auth and user services"
```

---

### Task 23: Auth middleware

**Files:**
- Create: `api-gateway/internal/middleware/auth.go`

- [ ] **Step 64: Write JWT auth middleware for Gin**

```go
// api-gateway/internal/middleware/auth.go
package middleware

import (
    "net/http"
    "strings"

    "github.com/gin-gonic/gin"

    authpb "github.com/claude-plan/contract/authpb"
)

func AuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
    return func(c *gin.Context) {
        header := c.GetHeader("Authorization")
        if header == "" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
            return
        }

        parts := strings.SplitN(header, " ", 2)
        if len(parts) != 2 || parts[0] != "Bearer" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
            return
        }

        resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{
            Token: parts[1],
        })
        if err != nil || !resp.Valid {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
            return
        }

        c.Set("user_id", resp.UserId)
        c.Set("email", resp.Email)
        c.Set("role", resp.Role)
        c.Set("permissions", resp.Permissions)
        c.Next()
    }
}

func RequirePermission(permission string) gin.HandlerFunc {
    return func(c *gin.Context) {
        perms, exists := c.Get("permissions")
        if !exists {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no permissions"})
            return
        }
        for _, p := range perms.([]string) {
            if p == permission {
                c.Next()
                return
            }
        }
        c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
    }
}
```

- [ ] **Step 65: Commit**

```bash
git add api-gateway/ && git commit -m "feat(gateway): add JWT auth middleware with permission checking"
```

---

### Task 24: Auth HTTP handlers

**Files:**
- Create: `api-gateway/internal/handler/auth_handler.go`

- [ ] **Step 66: Write auth HTTP handler**

```go
// api-gateway/internal/handler/auth_handler.go
package handler

import (
    "net/http"

    "github.com/gin-gonic/gin"

    authpb "github.com/claude-plan/contract/authpb"
)

type AuthHandler struct {
    authClient authpb.AuthServiceClient
}

func NewAuthHandler(authClient authpb.AuthServiceClient) *AuthHandler {
    return &AuthHandler{authClient: authClient}
}

type loginRequest struct {
    Email    string `json:"email" binding:"required,email"`
    Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
    var req loginRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    resp, err := h.authClient.Login(c.Request.Context(), &authpb.LoginRequest{
        Email:    req.Email,
        Password: req.Password,
    })
    if err != nil {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
        return
    }
    c.JSON(http.StatusOK, gin.H{
        "access_token":  resp.AccessToken,
        "refresh_token": resp.RefreshToken,
    })
}

type refreshRequest struct {
    RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthHandler) RefreshToken(c *gin.Context) {
    var req refreshRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    resp, err := h.authClient.RefreshToken(c.Request.Context(), &authpb.RefreshTokenRequest{
        RefreshToken: req.RefreshToken,
    })
    if err != nil {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
        return
    }
    c.JSON(http.StatusOK, gin.H{
        "access_token":  resp.AccessToken,
        "refresh_token": resp.RefreshToken,
    })
}

type passwordResetRequest struct {
    Email string `json:"email" binding:"required,email"`
}

func (h *AuthHandler) RequestPasswordReset(c *gin.Context) {
    var req passwordResetRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    h.authClient.RequestPasswordReset(c.Request.Context(), &authpb.PasswordResetRequest{
        Email: req.Email,
    })
    // Always 200 to not leak email existence
    c.JSON(http.StatusOK, gin.H{"message": "if the email exists, a reset link has been sent"})
}

type resetPasswordRequest struct {
    Token           string `json:"token" binding:"required"`
    NewPassword     string `json:"new_password" binding:"required"`
    ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
    var req resetPasswordRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    _, err := h.authClient.ResetPassword(c.Request.Context(), &authpb.ResetPasswordRequest{
        Token:           req.Token,
        NewPassword:     req.NewPassword,
        ConfirmPassword: req.ConfirmPassword,
    })
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}

type activateRequest struct {
    Token           string `json:"token" binding:"required"`
    Password        string `json:"password" binding:"required"`
    ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func (h *AuthHandler) ActivateAccount(c *gin.Context) {
    var req activateRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    _, err := h.authClient.ActivateAccount(c.Request.Context(), &authpb.ActivateAccountRequest{
        Token:           req.Token,
        Password:        req.Password,
        ConfirmPassword: req.ConfirmPassword,
    })
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"message": "account activated successfully"})
}

type logoutRequest struct {
    RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthHandler) Logout(c *gin.Context) {
    var req logoutRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    h.authClient.Logout(c.Request.Context(), &authpb.LogoutRequest{
        RefreshToken: req.RefreshToken,
    })
    c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}
```

- [ ] **Step 67: Commit**

```bash
git add api-gateway/ && git commit -m "feat(gateway): add auth HTTP handlers"
```

---

### Task 25: Employee HTTP handlers

**Files:**
- Create: `api-gateway/internal/handler/employee_handler.go`

- [ ] **Step 68: Write employee HTTP handler**

```go
// api-gateway/internal/handler/employee_handler.go
package handler

import (
    "net/http"
    "strconv"

    "github.com/gin-gonic/gin"

    authpb "github.com/claude-plan/contract/authpb"
    userpb "github.com/claude-plan/contract/userpb"
)

type EmployeeHandler struct {
    userClient userpb.UserServiceClient
    authClient authpb.AuthServiceClient
}

func NewEmployeeHandler(userClient userpb.UserServiceClient, authClient authpb.AuthServiceClient) *EmployeeHandler {
    return &EmployeeHandler{userClient: userClient, authClient: authClient}
}

func (h *EmployeeHandler) ListEmployees(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

    resp, err := h.userClient.ListEmployees(c.Request.Context(), &userpb.ListEmployeesRequest{
        EmailFilter:    c.Query("email"),
        NameFilter:     c.Query("name"),
        PositionFilter: c.Query("position"),
        Page:           int32(page),
        PageSize:       int32(pageSize),
    })
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list employees"})
        return
    }

    employees := make([]gin.H, 0, len(resp.Employees))
    for _, emp := range resp.Employees {
        employees = append(employees, employeeToJSON(emp))
    }
    c.JSON(http.StatusOK, gin.H{
        "employees":   employees,
        "total_count": resp.TotalCount,
    })
}

func (h *EmployeeHandler) GetEmployee(c *gin.Context) {
    id, err := strconv.ParseInt(c.Param("id"), 10, 64)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
        return
    }

    resp, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "employee not found"})
        return
    }
    c.JSON(http.StatusOK, employeeToJSON(resp))
}

type createEmployeeRequest struct {
    FirstName   string `json:"first_name" binding:"required"`
    LastName    string `json:"last_name" binding:"required"`
    DateOfBirth int64  `json:"date_of_birth" binding:"required"`
    Gender      string `json:"gender"`
    Email       string `json:"email" binding:"required,email"`
    Phone       string `json:"phone"`
    Address     string `json:"address"`
    Username    string `json:"username" binding:"required"`
    Position    string `json:"position"`
    Department  string `json:"department"`
    Role        string `json:"role" binding:"required"`
    Active      bool   `json:"active"`
}

func (h *EmployeeHandler) CreateEmployee(c *gin.Context) {
    var req createEmployeeRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    resp, err := h.userClient.CreateEmployee(c.Request.Context(), &userpb.CreateEmployeeRequest{
        FirstName:   req.FirstName,
        LastName:    req.LastName,
        DateOfBirth: req.DateOfBirth,
        Gender:      req.Gender,
        Email:       req.Email,
        Phone:       req.Phone,
        Address:     req.Address,
        Username:    req.Username,
        Position:    req.Position,
        Department:  req.Department,
        Role:        req.Role,
        Active:      req.Active,
    })
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    // Orchestrate: tell auth-service to create activation token and send email
    _, _ = h.authClient.CreateActivationToken(c.Request.Context(), &authpb.CreateActivationTokenRequest{
        UserId:    resp.Id,
        Email:     resp.Email,
        FirstName: resp.FirstName,
    })

    c.JSON(http.StatusCreated, employeeToJSON(resp))
}

type updateEmployeeRequest struct {
    LastName   *string `json:"last_name"`
    Gender     *string `json:"gender"`
    Phone      *string `json:"phone"`
    Address    *string `json:"address"`
    Position   *string `json:"position"`
    Department *string `json:"department"`
    Role       *string `json:"role"`
    Active     *bool   `json:"active"`
}

func (h *EmployeeHandler) UpdateEmployee(c *gin.Context) {
    id, err := strconv.ParseInt(c.Param("id"), 10, 64)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
        return
    }

    // Spec: admins can only edit non-admin employees
    target, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "employee not found"})
        return
    }
    if target.Role == "EmployeeAdmin" {
        c.JSON(http.StatusForbidden, gin.H{"error": "cannot edit admin employees"})
        return
    }

    var req updateEmployeeRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    pbReq := &userpb.UpdateEmployeeRequest{Id: id}
    if req.LastName != nil { pbReq.LastName = req.LastName }
    if req.Gender != nil { pbReq.Gender = req.Gender }
    if req.Phone != nil { pbReq.Phone = req.Phone }
    if req.Address != nil { pbReq.Address = req.Address }
    if req.Position != nil { pbReq.Position = req.Position }
    if req.Department != nil { pbReq.Department = req.Department }
    if req.Role != nil { pbReq.Role = req.Role }
    if req.Active != nil { pbReq.Active = req.Active }

    resp, err := h.userClient.UpdateEmployee(c.Request.Context(), pbReq)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, employeeToJSON(resp))
}

func employeeToJSON(emp *userpb.EmployeeResponse) gin.H {
    return gin.H{
        "id":            emp.Id,
        "first_name":    emp.FirstName,
        "last_name":     emp.LastName,
        "date_of_birth": emp.DateOfBirth,
        "gender":        emp.Gender,
        "email":         emp.Email,
        "phone":         emp.Phone,
        "address":       emp.Address,
        "username":      emp.Username,
        "position":      emp.Position,
        "department":    emp.Department,
        "active":        emp.Active,
        "role":          emp.Role,
        "permissions":   emp.Permissions,
    }
}
```

- [ ] **Step 69: Commit**

```bash
git add api-gateway/ && git commit -m "feat(gateway): add employee HTTP handlers with CRUD"
```

---

### Task 26: Router and gateway main

**Files:**
- Create: `api-gateway/internal/router/router.go`
- Create: `api-gateway/cmd/main.go`

- [ ] **Step 70: Write router**

```go
// api-gateway/internal/router/router.go
package router

import (
    "github.com/gin-gonic/gin"

    authpb "github.com/claude-plan/contract/authpb"
    userpb "github.com/claude-plan/contract/userpb"
    "github.com/claude-plan/api-gateway/internal/handler"
    "github.com/claude-plan/api-gateway/internal/middleware"
)

func Setup(authClient authpb.AuthServiceClient, userClient userpb.UserServiceClient) *gin.Engine {
    r := gin.Default()

    authHandler := handler.NewAuthHandler(authClient)
    empHandler := handler.NewEmployeeHandler(userClient, authClient)

    api := r.Group("/api")
    {
        // Public auth routes (no token required)
        auth := api.Group("/auth")
        {
            auth.POST("/login", authHandler.Login)
            auth.POST("/refresh", authHandler.RefreshToken)
            auth.POST("/logout", authHandler.Logout)
            auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
            auth.POST("/password/reset", authHandler.ResetPassword)
            auth.POST("/activate", authHandler.ActivateAccount)
        }

        // Protected routes
        protected := api.Group("/")
        protected.Use(middleware.AuthMiddleware(authClient))
        {
            employees := protected.Group("/employees")
            employees.Use(middleware.RequirePermission("employees.read"))
            {
                employees.GET("", empHandler.ListEmployees)
                employees.GET("/:id", empHandler.GetEmployee)
            }

            adminEmployees := protected.Group("/employees")
            adminEmployees.Use(middleware.RequirePermission("employees.create"))
            {
                adminEmployees.POST("", empHandler.CreateEmployee)
            }

            updateEmployees := protected.Group("/employees")
            updateEmployees.Use(middleware.RequirePermission("employees.update"))
            {
                updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
            }
        }
    }

    return r
}
```

- [ ] **Step 71: Write main.go**

```go
// api-gateway/cmd/main.go
package main

import (
    "fmt"
    "log"

    "github.com/claude-plan/api-gateway/internal/config"
    grpcclients "github.com/claude-plan/api-gateway/internal/grpc"
    "github.com/claude-plan/api-gateway/internal/router"
)

func main() {
    cfg := config.Load()

    authClient, authConn, err := grpcclients.NewAuthClient(cfg.AuthGRPCAddr)
    if err != nil {
        log.Fatalf("failed to connect to auth service: %v", err)
    }
    defer authConn.Close()

    userClient, userConn, err := grpcclients.NewUserClient(cfg.UserGRPCAddr)
    if err != nil {
        log.Fatalf("failed to connect to user service: %v", err)
    }
    defer userConn.Close()

    r := router.Setup(authClient, userClient)

    fmt.Printf("API Gateway listening on %s\n", cfg.HTTPAddr)
    if err := r.Run(cfg.HTTPAddr); err != nil {
        log.Fatalf("failed to start server: %v", err)
    }
}
```

- [ ] **Step 72: Run go mod tidy and verify compilation**

```bash
cd api-gateway && go mod tidy && go build ./...
```

- [ ] **Step 73: Commit**

```bash
git add -A && git commit -m "feat(gateway): add router, main entry point, wire all dependencies"
```

---

## Chunk 5: Docker & Integration

### Task 27: Dockerfiles

**Files:**
- Create: `auth-service/Dockerfile`
- Create: `user-service/Dockerfile`
- Create: `api-gateway/Dockerfile`

- [ ] **Step 74: Write auth-service Dockerfile**

```dockerfile
# auth-service/Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY auth-service/ auth-service/
# Disable workspace mode; use replace directive to resolve local contract module
ENV GOWORK=off
RUN cd auth-service && \
    go mod edit -replace github.com/claude-plan/contract=../contract && \
    go mod download && go build -o /auth-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /auth-service /auth-service
CMD ["/auth-service"]
```

- [ ] **Step 75: Write user-service Dockerfile**

```dockerfile
# user-service/Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY user-service/ user-service/
ENV GOWORK=off
RUN cd user-service && \
    go mod edit -replace github.com/claude-plan/contract=../contract && \
    go mod download && go build -o /user-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /user-service /user-service
CMD ["/user-service"]
```

- [ ] **Step 76: Write api-gateway Dockerfile**

```dockerfile
# api-gateway/Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY api-gateway/ api-gateway/
ENV GOWORK=off
RUN cd api-gateway && \
    go mod edit -replace github.com/claude-plan/contract=../contract && \
    go mod download && go build -o /api-gateway ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /api-gateway /api-gateway
CMD ["/api-gateway"]
```

- [ ] **Step 77: Commit**

```bash
git add */Dockerfile && git commit -m "chore: add Dockerfiles for all services"
```

---

### Task 28: Docker Compose

**Files:**
- Create: `docker-compose.yml`

- [ ] **Step 78: Write docker-compose.yml**

```yaml
# docker-compose.yml
services:
  user-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: user_db
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5432:5432"
    volumes:
      - user_db_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  auth-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: auth_db
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5433:5432"
    volumes:
      - auth_db_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  kafka:
    image: confluentinc/cp-kafka:7.6.0
    environment:
      KAFKA_NODE_ID: 1
      KAFKA_LISTENER_SECURITY_PROTOCOL_MAP: CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT,HOST:PLAINTEXT
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092,HOST://localhost:9094
      KAFKA_LISTENERS: PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093,HOST://0.0.0.0:9094
      KAFKA_CONTROLLER_LISTENER_NAMES: CONTROLLER
      KAFKA_CONTROLLER_QUORUM_VOTERS: 1@kafka:9093
      KAFKA_PROCESS_ROLES: broker,controller
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
      CLUSTER_ID: "MkU3OEVBNTcwNTJENDM2Qk"
    ports:
      - "9092:9092"
      - "9094:9094"
    healthcheck:
      test: ["CMD-SHELL", "kafka-topics --bootstrap-server localhost:9092 --list || exit 1"]
      interval: 10s
      timeout: 10s
      retries: 5
      start_period: 30s

  user-service:
    build:
      context: .
      dockerfile: user-service/Dockerfile
    environment:
      USER_DB_HOST: user-db
      USER_DB_PORT: "5432"
      USER_DB_USER: postgres
      USER_DB_PASSWORD: postgres
      USER_DB_NAME: user_db
      USER_GRPC_ADDR: ":50052"
      KAFKA_BROKERS: kafka:9092
    ports:
      - "50052:50052"
    depends_on:
      user-db:
        condition: service_healthy
      kafka:
        condition: service_healthy

  auth-service:
    build:
      context: .
      dockerfile: auth-service/Dockerfile
    environment:
      AUTH_DB_HOST: auth-db
      AUTH_DB_PORT: "5432"
      AUTH_DB_USER: postgres
      AUTH_DB_PASSWORD: postgres
      AUTH_DB_NAME: auth_db
      AUTH_GRPC_ADDR: ":50051"
      USER_GRPC_ADDR: "user-service:50052"
      KAFKA_BROKERS: kafka:9092
      JWT_SECRET: docker-test-secret-change-me
      JWT_ACCESS_EXPIRY: 15m
      JWT_REFRESH_EXPIRY: 168h
    ports:
      - "50051:50051"
    depends_on:
      auth-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      user-service:
        condition: service_started

  api-gateway:
    build:
      context: .
      dockerfile: api-gateway/Dockerfile
    environment:
      GATEWAY_HTTP_ADDR: ":8080"
      AUTH_GRPC_ADDR: "auth-service:50051"
      USER_GRPC_ADDR: "user-service:50052"
    ports:
      - "8080:8080"
    depends_on:
      auth-service:
        condition: service_started
      user-service:
        condition: service_started

volumes:
  user_db_data:
  auth_db_data:
```

- [ ] **Step 79: Commit**

```bash
git add docker-compose.yml && git commit -m "chore: add docker-compose with all services, databases, and Kafka"
```

---

### Task 29: Makefile with all targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 80: Update Makefile with full targets**

```makefile
# Makefile
.PHONY: proto clean build run docker-up docker-down

proto:
	protoc -I contract/proto \
		--go_out=contract --go_opt=paths=source_relative \
		--go-grpc_out=contract --go-grpc_opt=paths=source_relative \
		proto/auth/auth.proto proto/user/user.proto

build:
	cd user-service && go build -o bin/user-service ./cmd
	cd auth-service && go build -o bin/auth-service ./cmd
	cd api-gateway && go build -o bin/api-gateway ./cmd

tidy:
	cd contract && go mod tidy
	cd user-service && go mod tidy
	cd auth-service && go mod tidy
	cd api-gateway && go mod tidy

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

clean:
	rm -f contract/authpb/*.go contract/userpb/*.go
	rm -f user-service/bin/* auth-service/bin/* api-gateway/bin/*
```

- [ ] **Step 81: Commit**

```bash
git add Makefile && git commit -m "chore: add Makefile with proto, build, docker, and tidy targets"
```

---

### Task 30: Smoke test with Docker

- [ ] **Step 82: Build and start all containers**

```bash
make docker-up
```

Expected: All 5 containers start (2 postgres, kafka, 3 services).

- [ ] **Step 83: Test health - create employee via API**

```bash
# Login won't work yet (no users), but verify gateway responds
curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@test.com","password":"Test1234"}' | jq .
```

Expected: `{"error": "invalid credentials"}` (401) — confirms the full chain works.

- [ ] **Step 84: Verify docker logs show no crashes**

```bash
docker compose logs --tail=20
```

Expected: All services show "listening on ..." messages.

- [ ] **Step 85: Tear down**

```bash
make docker-down
```

- [ ] **Step 86: Final commit**

```bash
git add -A && git commit -m "chore: verify full stack builds and runs in Docker"
```

---

## API Reference

| Method | Path | Auth | Permission | Description |
|--------|------|------|------------|-------------|
| POST | `/api/auth/login` | No | - | Login with email+password |
| POST | `/api/auth/refresh` | No | - | Refresh access token |
| POST | `/api/auth/logout` | No | - | Revoke refresh token |
| POST | `/api/auth/password/reset-request` | No | - | Request password reset email |
| POST | `/api/auth/password/reset` | No | - | Reset password with token |
| POST | `/api/auth/activate` | No | - | Activate account with token |
| GET | `/api/employees` | Yes | `employees.read` | List employees (filterable) |
| GET | `/api/employees/:id` | Yes | `employees.read` | Get employee by ID |
| POST | `/api/employees` | Yes | `employees.create` | Create employee + send activation email (admin only) |
| PUT | `/api/employees/:id` | Yes | `employees.update` | Update non-admin employee (admin only) |
