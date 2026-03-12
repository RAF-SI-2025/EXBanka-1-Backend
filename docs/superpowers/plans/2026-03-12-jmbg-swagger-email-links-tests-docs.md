# JMBG Field, Swagger, Email Links, Tests & Documentation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add JMBG (13-digit unique national ID) to Employee, add Swagger docs to API Gateway, switch email templates from raw tokens to clickable links, write comprehensive tests for all microservices, and produce backend documentation.

**Architecture:** Vertical changes through the existing layered stack (model → repository → service → handler → proto → API gateway) for JMBG. Swagger uses swaggo/gin-swagger annotations on the API gateway. Email link change flows through auth-service config → Kafka message data → notification-service templates. Tests use Go's standard `testing` package with `testify` for assertions and table-driven tests. Documentation is a standalone Markdown file.

**Tech Stack:** Go, Gin, GORM, gRPC/protobuf, swaggo/swag, gin-swagger, testify, sqlmock/go-sqlmock (for DB tests), httptest (for API gateway)

---

## File Structure

### New Files
```
user-service/internal/service/jmbg_validator.go          — JMBG validation logic
user-service/internal/service/jmbg_validator_test.go      — JMBG validation tests
user-service/internal/service/employee_service_test.go    — Employee service unit tests
user-service/internal/service/role_service_test.go        — Role service unit tests
user-service/internal/repository/employee_repository_test.go — Repository tests
user-service/internal/handler/grpc_handler_test.go        — gRPC handler tests
auth-service/internal/service/auth_service_test.go        — Auth service unit tests
auth-service/internal/service/jwt_service_test.go         — JWT service unit tests
auth-service/internal/repository/token_repository_test.go — Token repository tests
auth-service/internal/handler/grpc_handler_test.go        — Auth gRPC handler tests
notification-service/internal/sender/templates_test.go    — Email template tests
notification-service/internal/consumer/email_consumer_test.go — Consumer tests
api-gateway/internal/handler/auth_handler_test.go         — Auth HTTP handler tests
api-gateway/internal/handler/employee_handler_test.go     — Employee HTTP handler tests
api-gateway/internal/middleware/auth_test.go              — Auth middleware tests
api-gateway/docs/docs.go                                  — Generated swagger docs
api-gateway/docs/swagger.json                             — Generated swagger spec
api-gateway/docs/swagger.yaml                             — Generated swagger spec
docs/API.md                                               — Backend documentation
```

### Modified Files
```
contract/proto/user/user.proto                            — Add jmbg field to messages
contract/userpb/*.pb.go                                   — Regenerated from proto
user-service/internal/model/employee.go                   — Add JMBG field to struct
user-service/internal/service/employee_service.go         — JMBG validation on create/update
user-service/internal/handler/grpc_handler.go             — Map JMBG in proto conversion
user-service/internal/repository/employee_repository.go   — Add GetByJMBG method
api-gateway/internal/handler/employee_handler.go          — Add JMBG to request/response structs
api-gateway/internal/handler/auth_handler.go              — Add swagger annotations
api-gateway/internal/router/router.go                     — Mount swagger UI route
api-gateway/cmd/main.go                                   — Import swagger docs
api-gateway/go.mod                                        — Add swaggo dependencies
auth-service/internal/config/config.go                    — Add FrontendBaseURL field
auth-service/internal/service/auth_service.go             — Pass FrontendBaseURL in Kafka data
notification-service/internal/sender/templates.go         — Use links instead of raw tokens
.env.example                                              — Add FRONTEND_BASE_URL
CLAUDE.md                                                 — Updated with new info
```

---

## Chunk 1: JMBG Field Implementation

### Task 1: Add JMBG Validation Logic

**Files:**
- Create: `user-service/internal/service/jmbg_validator.go`
- Test: `user-service/internal/service/jmbg_validator_test.go`

- [ ] **Step 1: Write failing tests for JMBG validation**

```go
// user-service/internal/service/jmbg_validator_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateJMBG(t *testing.T) {
	tests := []struct {
		name    string
		jmbg    string
		wantErr bool
		errMsg  string
	}{
		{"valid 13 digits", "0101990710024", false, ""},
		{"empty string", "", true, "JMBG must be exactly 13 digits"},
		{"too short", "123456789012", true, "JMBG must be exactly 13 digits"},
		{"too long", "12345678901234", true, "JMBG must be exactly 13 digits"},
		{"contains letters", "012345678901a", true, "JMBG must contain only digits"},
		{"contains spaces", "0123456789 12", true, "JMBG must contain only digits"},
		{"contains special chars", "012345678901!", true, "JMBG must contain only digits"},
		{"all zeros", "0000000000000", false, ""},
		{"valid 13 digits alt", "1234567890123", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJMBG(tt.jmbg)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd user-service && go test ./internal/service/ -run TestValidateJMBG -v`
Expected: FAIL — `ValidateJMBG` undefined

- [ ] **Step 3: Implement JMBG validation**

```go
// user-service/internal/service/jmbg_validator.go
package service

import "errors"

func ValidateJMBG(jmbg string) error {
	if len(jmbg) != 13 {
		return errors.New("JMBG must be exactly 13 digits")
	}
	for _, c := range jmbg {
		if c < '0' || c > '9' {
			return errors.New("JMBG must contain only digits")
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd user-service && go test ./internal/service/ -run TestValidateJMBG -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add user-service/internal/service/jmbg_validator.go user-service/internal/service/jmbg_validator_test.go
git commit -m "feat(user-service): add JMBG validation logic with tests"
```

---

### Task 2: Add JMBG to Employee Model

**Files:**
- Modify: `user-service/internal/model/employee.go`

- [ ] **Step 1: Add JMBG field to Employee struct**

Add the JMBG field between `Address` and `Username` in the Employee struct:

```go
JMBG string `gorm:"uniqueIndex;size:13;not null" json:"jmbg"`
```

The full struct becomes:
```go
type Employee struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName    string    `gorm:"not null" json:"first_name"`
	LastName     string    `gorm:"not null" json:"last_name"`
	DateOfBirth  time.Time `gorm:"not null" json:"date_of_birth"`
	Gender       string    `gorm:"size:10" json:"gender"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	Phone        string    `json:"phone"`
	Address      string    `json:"address"`
	JMBG         string    `gorm:"uniqueIndex;size:13;not null" json:"jmbg"`
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

- [ ] **Step 2: Verify build**

Run: `cd user-service && go build ./...`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add user-service/internal/model/employee.go
git commit -m "feat(user-service): add JMBG field to Employee model"
```

---

### Task 3: Add JMBG to Proto Definition

**Files:**
- Modify: `contract/proto/user/user.proto`

- [ ] **Step 1: Add jmbg field to proto messages**

In `CreateEmployeeRequest`, add after `active` (field 12):
```protobuf
string jmbg = 13;
```

In `UpdateEmployeeRequest`, add after `active` (field 9):
```protobuf
optional string jmbg = 10;
```

In `EmployeeResponse`, add after `permissions` (field 14):
```protobuf
string jmbg = 15;
```

- [ ] **Step 2: Regenerate protobuf Go code**

Run: `make proto`
Expected: Generated files updated in `contract/userpb/`

- [ ] **Step 3: Verify build**

Run: `cd contract && go build ./...`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add contract/proto/user/user.proto contract/userpb/
git commit -m "feat(contract): add JMBG field to user proto messages"
```

---

### Task 4: Wire JMBG Through User Service Layers

**Files:**
- Modify: `user-service/internal/service/employee_service.go`
- Modify: `user-service/internal/handler/grpc_handler.go`
- Modify: `user-service/internal/repository/employee_repository.go`

- [ ] **Step 1: Add JMBG validation to CreateEmployee in employee_service.go**

In `user-service/internal/service/employee_service.go`, in the `CreateEmployee` method, add JMBG validation after the role validation check:

```go
func (s *EmployeeService) CreateEmployee(ctx context.Context, emp *model.Employee) error {
	if !ValidRole(emp.Role) {
		return errors.New("invalid role")
	}
	if err := ValidateJMBG(emp.JMBG); err != nil {
		return err
	}
	// ... rest unchanged
}
```

- [ ] **Step 2: Add JMBG to UpdateEmployee in employee_service.go**

In the `UpdateEmployee` method, add after the `active` handling:

```go
if v, ok := updates["jmbg"].(string); ok {
	if err := ValidateJMBG(v); err != nil {
		return nil, err
	}
	emp.JMBG = v
}
```

- [ ] **Step 3: Add GetByJMBG to employee_repository.go**

Add to `user-service/internal/repository/employee_repository.go`:

```go
func (r *EmployeeRepository) GetByJMBG(jmbg string) (*model.Employee, error) {
	var emp model.Employee
	err := r.db.Where("jmbg = ?", jmbg).First(&emp).Error
	return &emp, err
}
```

- [ ] **Step 4: Wire JMBG in gRPC handler**

In `user-service/internal/handler/grpc_handler.go`:

In `CreateEmployee`, add `JMBG: req.Jmbg` to the model construction:
```go
emp := &model.Employee{
	// ... existing fields ...
	JMBG:        req.Jmbg,
	// ...
}
```

In `UpdateEmployee`, add JMBG handling:
```go
if req.Jmbg != nil {
	updates["jmbg"] = *req.Jmbg
}
```

In `toEmployeeResponse`, add:
```go
Jmbg: emp.JMBG,
```

- [ ] **Step 5: Verify build**

Run: `cd user-service && go build ./...`
Expected: SUCCESS

- [ ] **Step 6: Commit**

```bash
git add user-service/internal/service/employee_service.go user-service/internal/handler/grpc_handler.go user-service/internal/repository/employee_repository.go
git commit -m "feat(user-service): wire JMBG through service, handler, and repository layers"
```

---

### Task 5: Add JMBG to API Gateway

**Files:**
- Modify: `api-gateway/internal/handler/employee_handler.go`

- [ ] **Step 1: Add JMBG to createEmployeeRequest struct**

```go
type createEmployeeRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth" binding:"required"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required,email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	JMBG        string `json:"jmbg" binding:"required"`
	Username    string `json:"username" binding:"required"`
	Position    string `json:"position"`
	Department  string `json:"department"`
	Role        string `json:"role" binding:"required"`
	Active      bool   `json:"active"`
}
```

- [ ] **Step 2: Pass JMBG in CreateEmployee handler**

In the `CreateEmployee` function, add `Jmbg: req.JMBG` to the gRPC request:
```go
resp, err := h.userClient.CreateEmployee(c.Request.Context(), &userpb.CreateEmployeeRequest{
	// ... existing fields ...
	Jmbg: req.JMBG,
})
```

- [ ] **Step 3: Add JMBG to updateEmployeeRequest struct**

```go
type updateEmployeeRequest struct {
	LastName   *string `json:"last_name"`
	Gender     *string `json:"gender"`
	Phone      *string `json:"phone"`
	Address    *string `json:"address"`
	JMBG       *string `json:"jmbg"`
	Position   *string `json:"position"`
	Department *string `json:"department"`
	Role       *string `json:"role"`
	Active     *bool   `json:"active"`
}
```

- [ ] **Step 4: Pass JMBG in UpdateEmployee handler**

Add after the Active handling:
```go
if req.JMBG != nil {
	pbReq.Jmbg = req.JMBG
}
```

- [ ] **Step 5: Add JMBG to employeeToJSON helper**

```go
func employeeToJSON(emp *userpb.EmployeeResponse) gin.H {
	return gin.H{
		// ... existing fields ...
		"jmbg":          emp.Jmbg,
		// ...
	}
}
```

- [ ] **Step 6: Verify build**

Run: `cd api-gateway && go build ./...`
Expected: SUCCESS

- [ ] **Step 7: Commit**

```bash
git add api-gateway/internal/handler/employee_handler.go
git commit -m "feat(api-gateway): add JMBG field to employee create/update/response"
```

---

## Chunk 2: Swagger Integration on API Gateway

### Task 6: Add Swagger Dependencies

**Files:**
- Modify: `api-gateway/go.mod`

- [ ] **Step 1: Install swaggo dependencies**

```bash
cd api-gateway && go get github.com/swaggo/swag@latest && go get github.com/swaggo/gin-swagger@latest && go get github.com/swaggo/files@latest
```

- [ ] **Step 2: Install swag CLI tool**

```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

- [ ] **Step 3: Commit**

```bash
git add api-gateway/go.mod api-gateway/go.sum
git commit -m "chore(api-gateway): add swaggo swagger dependencies"
```

---

### Task 7: Add Swagger Annotations to Auth Handler

**Files:**
- Modify: `api-gateway/internal/handler/auth_handler.go`

- [ ] **Step 1: Add swagger annotations to Login**

Add above the `Login` method:
```go
// Login godoc
// @Summary      User login
// @Description  Authenticate with email and password, returns access and refresh tokens
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  loginRequest  true  "Login credentials"
// @Success      200  {object}  map[string]string  "access_token, refresh_token"
// @Failure      400  {object}  map[string]string  "error message"
// @Failure      401  {object}  map[string]string  "invalid credentials"
// @Router       /api/auth/login [post]
```

- [ ] **Step 2: Add swagger annotations to RefreshToken**

```go
// RefreshToken godoc
// @Summary      Refresh access token
// @Description  Exchange a valid refresh token for a new access/refresh token pair
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  refreshRequest  true  "Refresh token"
// @Success      200  {object}  map[string]string  "access_token, refresh_token"
// @Failure      400  {object}  map[string]string  "error message"
// @Failure      401  {object}  map[string]string  "invalid refresh token"
// @Router       /api/auth/refresh [post]
```

- [ ] **Step 3: Add swagger annotations to RequestPasswordReset**

```go
// RequestPasswordReset godoc
// @Summary      Request password reset
// @Description  Send a password reset link to the user's email
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  passwordResetRequest  true  "Email address"
// @Success      200  {object}  map[string]string  "confirmation message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/auth/password/reset-request [post]
```

- [ ] **Step 4: Add swagger annotations to ResetPassword**

```go
// ResetPassword godoc
// @Summary      Reset password
// @Description  Reset password using a token from the reset email link
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  resetPasswordRequest  true  "Reset token and new password"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/auth/password/reset [post]
```

- [ ] **Step 5: Add swagger annotations to ActivateAccount**

```go
// ActivateAccount godoc
// @Summary      Activate account
// @Description  Activate a new account using the token from the activation email link
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  activateRequest  true  "Activation token and password"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/auth/activate [post]
```

- [ ] **Step 6: Add swagger annotations to Logout**

```go
// Logout godoc
// @Summary      Logout
// @Description  Revoke the refresh token to end the session
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  logoutRequest  true  "Refresh token to revoke"
// @Success      200  {object}  map[string]string  "success message"
// @Router       /api/auth/logout [post]
```

**Note:** swaggo may have trouble resolving unexported types (`loginRequest`, etc.). If `swag init` fails or produces incomplete docs, export these structs (e.g., rename `loginRequest` → `LoginRequest`). Swaggo recent versions can parse unexported types when they're in the same package, so try first without exporting.

- [ ] **Step 7: Commit**

```bash
git add api-gateway/internal/handler/auth_handler.go
git commit -m "docs(api-gateway): add swagger annotations to auth handler"
```

---

### Task 8: Add Swagger Annotations to Employee Handler

**Files:**
- Modify: `api-gateway/internal/handler/employee_handler.go`

- [ ] **Step 1: Add swagger annotations to ListEmployees**

Add above the `ListEmployees` method:
```go
// ListEmployees godoc
// @Summary      List employees
// @Description  Get paginated list of employees with optional filters
// @Tags         employees
// @Produce      json
// @Param        page       query  int     false  "Page number"  default(1)
// @Param        page_size  query  int     false  "Page size"    default(20)
// @Param        email      query  string  false  "Filter by email (partial match)"
// @Param        name       query  string  false  "Filter by first/last name (partial match)"
// @Param        position   query  string  false  "Filter by position (partial match)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "employees array and total_count"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/employees [get]
```

- [ ] **Step 2: Add swagger annotations to GetEmployee**

```go
// GetEmployee godoc
// @Summary      Get employee by ID
// @Description  Retrieve a single employee's details
// @Tags         employees
// @Produce      json
// @Param        id  path  int  true  "Employee ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "employee details"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      404  {object}  map[string]string       "not found"
// @Router       /api/employees/{id} [get]
```

- [ ] **Step 3: Add swagger annotations to CreateEmployee**

```go
// CreateEmployee godoc
// @Summary      Create a new employee
// @Description  Create employee and send activation email. Requires employees.create permission.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        body  body  createEmployeeRequest  true  "Employee data"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/employees [post]
```

- [ ] **Step 4: Add swagger annotations to UpdateEmployee**

```go
// UpdateEmployee godoc
// @Summary      Update an employee
// @Description  Partially update employee fields. Cannot edit admin employees.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        id    path  int                    true  "Employee ID"
// @Param        body  body  updateEmployeeRequest  true  "Fields to update"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      403  {object}  map[string]string       "cannot edit admin"
// @Failure      404  {object}  map[string]string       "not found"
// @Router       /api/employees/{id} [put]
```

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/employee_handler.go
git commit -m "docs(api-gateway): add swagger annotations to employee handler"
```

---

### Task 9: Generate Swagger Docs and Mount UI

**Files:**
- Modify: `api-gateway/cmd/main.go`
- Modify: `api-gateway/internal/router/router.go`
- Create: `api-gateway/docs/` (generated)

- [ ] **Step 1: Add main.go swagger general API info annotation**

In `api-gateway/cmd/main.go`, add these annotations directly above `func main()`:
```go
// @title           EXBanka API
// @version         1.0
// @description     EXBanka Banking Microservices API Gateway
// @host            localhost:8080
// @BasePath        /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Enter "Bearer <token>"
```

**Important:** These MUST be placed above `func main()`, NOT before `package main`.

- [ ] **Step 2: Generate swagger docs**

Run: `cd api-gateway && swag init -g cmd/main.go -o docs`
Expected: Creates `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml`

- [ ] **Step 3: Mount swagger UI route in router.go**

Add imports to `api-gateway/internal/router/router.go`:
```go
import (
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	// ... existing imports ...
)
```

Add before `return r`:
```go
r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
```

- [ ] **Step 4: Import docs package in main.go**

Add blank import to `api-gateway/cmd/main.go`:
```go
import (
	_ "github.com/exbanka/api-gateway/docs"
	// ... existing imports ...
)
```

- [ ] **Step 5: Verify build**

Run: `cd api-gateway && go build ./...`
Expected: SUCCESS

- [ ] **Step 6: Commit**

```bash
git add api-gateway/cmd/main.go api-gateway/internal/router/router.go api-gateway/docs/
git commit -m "feat(api-gateway): integrate swagger UI at /swagger/"
```

---

## Chunk 3: Email Links Instead of Raw Tokens

### Task 10: Add FrontendBaseURL to Auth Service Config

**Files:**
- Modify: `auth-service/internal/config/config.go`
- Modify: `.env.example`

- [ ] **Step 1: Add FrontendBaseURL to auth-service config**

In `auth-service/internal/config/config.go`, add to the `Config` struct:
```go
FrontendBaseURL string
```

In the `Load()` function, add:
```go
FrontendBaseURL: getEnv("FRONTEND_BASE_URL", "http://localhost:3000"),
```

- [ ] **Step 2: Add to .env.example**

Add:
```
FRONTEND_BASE_URL=http://localhost:3000
```

- [ ] **Step 4: Commit**

```bash
git add auth-service/internal/config/config.go .env.example
git commit -m "feat: add FRONTEND_BASE_URL config to auth-service"
```

---

### Task 11: Pass FrontendBaseURL Through Auth Service

**Files:**
- Modify: `auth-service/internal/service/auth_service.go`
- Modify: `auth-service/cmd/main.go`

- [ ] **Step 1: Add frontendBaseURL to AuthService struct**

In `auth-service/internal/service/auth_service.go`, add `frontendBaseURL string` to the `AuthService` struct and `NewAuthService` constructor:

```go
type AuthService struct {
	tokenRepo       *repository.TokenRepository
	jwtService      *JWTService
	userClient      userpb.UserServiceClient
	producer        *kafkaprod.Producer
	cache           *cache.RedisCache
	refreshExp      time.Duration
	frontendBaseURL string
}

func NewAuthService(
	tokenRepo *repository.TokenRepository,
	jwtService *JWTService,
	userClient userpb.UserServiceClient,
	producer *kafkaprod.Producer,
	cache *cache.RedisCache,
	refreshExp time.Duration,
	frontendBaseURL string,
) *AuthService {
	return &AuthService{
		tokenRepo:       tokenRepo,
		jwtService:      jwtService,
		userClient:      userClient,
		producer:        producer,
		cache:           cache,
		refreshExp:      refreshExp,
		frontendBaseURL: frontendBaseURL,
	}
}
```

- [ ] **Step 2: Include link URL in Kafka activation email data**

In the `CreateActivationToken` method, change the Kafka message data to include a link:

```go
return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
	To:        email,
	EmailType: kafkamsg.EmailTypeActivation,
	Data: map[string]string{
		"token":      token,
		"first_name": firstName,
		"link":       s.frontendBaseURL + "/activate?token=" + token,
	},
})
```

- [ ] **Step 3: Include link URL in Kafka password reset email data**

In the `RequestPasswordReset` method, change:

```go
return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
	To:        email,
	EmailType: kafkamsg.EmailTypePasswordReset,
	Data: map[string]string{
		"token": token,
		"link":  s.frontendBaseURL + "/reset-password?token=" + token,
	},
})
```

- [ ] **Step 4: Update auth-service cmd/main.go to pass FrontendBaseURL**

**IMPORTANT:** Steps 1 and 4 MUST be done together before any build attempt. Modifying the constructor signature without updating the call site will break the build.

In `auth-service/cmd/main.go`, update the `NewAuthService` call to pass `cfg.FrontendBaseURL` as the last argument.

- [ ] **Step 5: Verify build**

Run: `cd auth-service && go build ./...`
Expected: SUCCESS

- [ ] **Step 6: Commit**

```bash
git add auth-service/internal/service/auth_service.go auth-service/cmd/main.go
git commit -m "feat(auth-service): include frontend links in activation and password reset emails"
```

---

### Task 12: Update Email Templates to Use Links

**Files:**
- Modify: `notification-service/internal/sender/templates.go`

- [ ] **Step 1: Update activation email template**

Change the `EmailTypeActivation` case:

```go
case kafkamsg.EmailTypeActivation:
	subject = "Activate Your EXBanka Account"
	name := data["first_name"]
	link := data["link"]
	body = fmt.Sprintf(`<h2>Welcome, %s!</h2>
<p>Your account has been created. Click the link below to activate your account and set your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Activate Account</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 24 hours.</p>`, name, link, link)
```

- [ ] **Step 2: Update password reset email template**

Change the `EmailTypePasswordReset` case:

```go
case kafkamsg.EmailTypePasswordReset:
	link := data["link"]
	subject = "Password Reset Request"
	body = fmt.Sprintf(`<h2>Password Reset</h2>
<p>Click the link below to reset your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 1 hour. If you did not request this, ignore this email.</p>`, link, link)
```

- [ ] **Step 3: Verify build**

Run: `cd notification-service && go build ./...`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add notification-service/internal/sender/templates.go
git commit -m "feat(notification-service): use clickable links instead of raw tokens in email templates"
```

---

## Chunk 4: Tests — User Service

> **Dependency:** Chunks 4-7 (all test chunks) depend on Chunks 1-3 being completed first, since tests validate the modified code (JMBG fields, email link templates, etc.).

### Task 13: Role Service Tests

**Files:**
- Create: `user-service/internal/service/role_service_test.go`

- [ ] **Step 1: Write role service tests**

```go
// user-service/internal/service/role_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidRole(t *testing.T) {
	tests := []struct {
		role  string
		valid bool
	}{
		{"EmployeeBasic", true},
		{"EmployeeAgent", true},
		{"EmployeeSupervisor", true},
		{"EmployeeAdmin", true},
		{"InvalidRole", false},
		{"", false},
		{"employeebasic", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			assert.Equal(t, tt.valid, ValidRole(tt.role))
		})
	}
}

func TestGetPermissions(t *testing.T) {
	tests := []struct {
		role        string
		expectPerms []string
	}{
		{"EmployeeBasic", []string{"clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage"}},
		{"EmployeeAdmin", []string{"clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage", "securities.trade", "securities.read", "agents.manage", "otc.manage", "funds.manage", "employees.create", "employees.update", "employees.read", "employees.permissions"}},
		{"InvalidRole", nil},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			perms := GetPermissions(tt.role)
			if tt.expectPerms == nil {
				assert.Nil(t, perms)
			} else {
				assert.Equal(t, tt.expectPerms, perms)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests**

Run: `cd user-service && go test ./internal/service/ -run "TestValidRole|TestGetPermissions" -v`
Expected: PASS

Note: You may need to adjust the expected permissions based on the actual role_service.go implementation. Read `user-service/internal/service/role_service.go` to verify the exact permissions for each role before writing the test.

- [ ] **Step 3: Commit**

```bash
git add user-service/internal/service/role_service_test.go
git commit -m "test(user-service): add role service tests"
```

---

### Task 14: Password Validation Tests (User Service)

**Files:**
- Create: `user-service/internal/service/employee_service_test.go`

- [ ] **Step 1: Write password validation tests**

```go
// user-service/internal/service/employee_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid password", "Abcdef12", false},
		{"valid complex", "MyP@ssw0rd99", false},
		{"too short", "Ab1234", true},
		{"too long", "Abcdefghijklmnopqrstuvwxyz1234567", true},
		{"no uppercase", "abcdef12", true},
		{"no lowercase", "ABCDEF12", true},
		{"only one digit", "Abcdefg1", true},
		{"no digits", "Abcdefgh", true},
		{"empty", "", true},
		{"exactly 8 chars valid", "Abcdef12", false},
		{"exactly 32 chars valid", "Abcdefghijklmnopqrstuvwxyz123456", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "TestPass12", hash)

	// Different calls produce different hashes (bcrypt salt)
	hash2, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, hash2)
}
```

- [ ] **Step 2: Run tests**

Run: `cd user-service && go test ./internal/service/ -run "TestValidatePassword|TestHashPassword" -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add user-service/internal/service/employee_service_test.go
git commit -m "test(user-service): add password validation and hashing tests"
```

---

### Task 15: User Service Integration Tests with Interfaces

To properly test the user service, we need interfaces for the repository and cache so we can mock them.

**Files:**
- Create: `user-service/internal/service/interfaces.go`
- Modify: `user-service/internal/service/employee_service.go` (change struct fields to interfaces)
- Modify: `user-service/internal/service/employee_service_test.go` (add integration tests)

- [ ] **Step 1: Create interfaces file**

```go
// user-service/internal/service/interfaces.go
package service

import "github.com/exbanka/user-service/internal/model"

type EmployeeRepo interface {
	Create(emp *model.Employee) error
	GetByID(id int64) (*model.Employee, error)
	GetByEmail(email string) (*model.Employee, error)
	GetByJMBG(jmbg string) (*model.Employee, error)
	Update(emp *model.Employee) error
	SetPassword(userID int64, passwordHash string) error
	List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error)
}
```

- [ ] **Step 2: Update EmployeeService to use interface**

In `user-service/internal/service/employee_service.go`, change:
```go
type EmployeeService struct {
	repo     EmployeeRepo
	producer *kafkaprod.Producer
	cache    *cache.RedisCache
}

func NewEmployeeService(repo EmployeeRepo, producer *kafkaprod.Producer, cache *cache.RedisCache) *EmployeeService {
```

- [ ] **Step 3: Write service tests with mock repo**

Add to `user-service/internal/service/employee_service_test.go`:

```go
import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/exbanka/user-service/internal/model"
)

// mockRepo implements EmployeeRepo for testing
type mockRepo struct {
	employees map[int64]*model.Employee
	nextID    int64
	createErr error
}

func newMockRepo() *mockRepo {
	return &mockRepo{employees: make(map[int64]*model.Employee), nextID: 1}
}

func (m *mockRepo) Create(emp *model.Employee) error {
	if m.createErr != nil {
		return m.createErr
	}
	emp.ID = m.nextID
	m.nextID++
	m.employees[emp.ID] = emp
	return nil
}

func (m *mockRepo) GetByID(id int64) (*model.Employee, error) {
	emp, ok := m.employees[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return emp, nil
}

func (m *mockRepo) GetByEmail(email string) (*model.Employee, error) {
	for _, emp := range m.employees {
		if emp.Email == email {
			return emp, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockRepo) GetByJMBG(jmbg string) (*model.Employee, error) {
	for _, emp := range m.employees {
		if emp.JMBG == jmbg {
			return emp, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockRepo) Update(emp *model.Employee) error {
	m.employees[emp.ID] = emp
	return nil
}

func (m *mockRepo) SetPassword(userID int64, hash string) error {
	emp, ok := m.employees[userID]
	if !ok {
		return errors.New("not found")
	}
	emp.PasswordHash = hash
	emp.Activated = true
	return nil
}

func (m *mockRepo) List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	var result []model.Employee
	for _, emp := range m.employees {
		result = append(result, *emp)
	}
	return result, int64(len(result)), nil
}

func TestCreateEmployee_Valid(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		FirstName: "John",
		LastName:  "Doe",
		Email:     "john@example.com",
		Username:  "johndoe",
		Role:      "EmployeeBasic",
		JMBG:      "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), emp.ID)
	assert.False(t, emp.Activated)
	assert.Empty(t, emp.PasswordHash)
	assert.NotEmpty(t, emp.Salt)
}

func TestCreateEmployee_InvalidRole(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		Role: "InvalidRole",
		JMBG: "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestCreateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		Role: "EmployeeBasic",
		JMBG: "123",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestGetEmployee(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, FirstName: "Jane", Email: "jane@example.com"}
	svc := NewEmployeeService(repo, nil, nil)

	emp, err := svc.GetEmployee(1)
	assert.NoError(t, err)
	assert.Equal(t, "Jane", emp.FirstName)
}

func TestGetEmployee_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.GetEmployee(999)
	assert.Error(t, err)
}

func TestUpdateEmployee_InvalidRole(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Role: "EmployeeBasic"}
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.UpdateEmployee(1, map[string]interface{}{"role": "BadRole"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestUpdateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Role: "EmployeeBasic", JMBG: "0101990710024"}
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.UpdateEmployee(1, map[string]interface{}{"jmbg": "bad"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestValidateCredentials_Valid(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       true,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	emp, valid := svc.ValidateCredentials("test@test.com", "ValidPass12")
	assert.True(t, valid)
	assert.NotNil(t, emp)
}

func TestValidateCredentials_WrongPassword(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       true,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	_, valid := svc.ValidateCredentials("test@test.com", "WrongPass12")
	assert.False(t, valid)
}

func TestValidateCredentials_InactiveUser(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       false,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	_, valid := svc.ValidateCredentials("test@test.com", "ValidPass12")
	assert.False(t, valid)
}
```

- [ ] **Step 4: Add testify dependency**

Run: `cd user-service && go get github.com/stretchr/testify`

- [ ] **Step 5: Run all user service tests**

Run: `cd user-service && go test ./internal/service/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add user-service/internal/service/
git commit -m "test(user-service): add comprehensive employee service tests with mock repo"
```

---

## Chunk 5: Tests — Auth Service

### Task 16: JWT Service Tests

**Files:**
- Create: `auth-service/internal/service/jwt_service_test.go`

- [ ] **Step 1: Write JWT service tests**

```go
// auth-service/internal/service/jwt_service_test.go
package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := svc.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", []string{"clients.read"})
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), claims.UserID)
	assert.Equal(t, "user@test.com", claims.Email)
	assert.Equal(t, "EmployeeBasic", claims.Role)
	assert.Equal(t, []string{"clients.read"}, claims.Permissions)
}

func TestValidateToken_Invalid(t *testing.T) {
	svc := NewJWTService("test-secret", 15*time.Minute)

	_, err := svc.ValidateToken("invalid.token.string")
	assert.Error(t, err)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	svc1 := NewJWTService("secret-one", 15*time.Minute)
	svc2 := NewJWTService("secret-two", 15*time.Minute)

	token, _ := svc1.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", nil)
	_, err := svc2.ValidateToken(token)
	assert.Error(t, err)
}

func TestValidateToken_Expired(t *testing.T) {
	svc := NewJWTService("test-secret", -1*time.Second)

	token, err := svc.GenerateAccessToken(1, "user@test.com", "EmployeeBasic", nil)
	assert.NoError(t, err)

	_, err = svc.ValidateToken(token)
	assert.Error(t, err)
}
```

- [ ] **Step 2: Add testify dependency**

Run: `cd auth-service && go get github.com/stretchr/testify`

- [ ] **Step 3: Run tests**

Run: `cd auth-service && go test ./internal/service/ -run "TestGenerate|TestValidateToken" -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add auth-service/internal/service/jwt_service_test.go auth-service/go.mod auth-service/go.sum
git commit -m "test(auth-service): add JWT service tests"
```

---

### Task 17: Auth Service Password Validation Tests

**Files:**
- Create: `auth-service/internal/service/auth_service_test.go`

- [ ] **Step 1: Write password validation tests for auth-service**

```go
// auth-service/internal/service/auth_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePassword_AuthService(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid", "Abcdef12", false},
		{"too short", "Ab12", true},
		{"too long", "Abcdefghijklmnopqrstuvwxyz1234567", true},
		{"no uppercase", "abcdef12", true},
		{"no lowercase", "ABCDEF12", true},
		{"one digit only", "Abcdefg1", true},
		{"no digits", "Abcdefgh", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	assert.NoError(t, err)
	assert.Len(t, token1, 64) // 32 bytes hex encoded

	token2, err := generateToken()
	assert.NoError(t, err)
	assert.NotEqual(t, token1, token2) // unique each time
}
```

- [ ] **Step 2: Run tests**

Run: `cd auth-service && go test ./internal/service/ -run "TestValidatePassword_AuthService|TestGenerateToken" -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add auth-service/internal/service/auth_service_test.go
git commit -m "test(auth-service): add password validation and token generation tests"
```

---

## Chunk 6: Tests — Notification Service

### Task 18: Email Template Tests

**Files:**
- Create: `notification-service/internal/sender/templates_test.go`

- [ ] **Step 1: Write email template tests**

```go
// notification-service/internal/sender/templates_test.go
package sender

import (
	"testing"

	"github.com/stretchr/testify/assert"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestBuildEmail_Activation(t *testing.T) {
	data := map[string]string{
		"first_name": "John",
		"token":      "abc123",
		"link":       "http://localhost:3000/activate?token=abc123",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeActivation, data)

	assert.Equal(t, "Activate Your EXBanka Account", subject)
	assert.Contains(t, body, "John")
	assert.Contains(t, body, "http://localhost:3000/activate?token=abc123")
	assert.Contains(t, body, "Activate Account")
	assert.Contains(t, body, "24 hours")
}

func TestBuildEmail_PasswordReset(t *testing.T) {
	data := map[string]string{
		"token": "reset123",
		"link":  "http://localhost:3000/reset-password?token=reset123",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypePasswordReset, data)

	assert.Equal(t, "Password Reset Request", subject)
	assert.Contains(t, body, "http://localhost:3000/reset-password?token=reset123")
	assert.Contains(t, body, "Reset Password")
	assert.Contains(t, body, "1 hour")
}

func TestBuildEmail_Confirmation(t *testing.T) {
	data := map[string]string{"first_name": "Jane"}
	subject, body := BuildEmail(kafkamsg.EmailTypeConfirmation, data)

	assert.Equal(t, "Account Activated Successfully", subject)
	assert.Contains(t, body, "Jane")
	assert.Contains(t, body, "activated")
}

func TestBuildEmail_UnknownType(t *testing.T) {
	subject, body := BuildEmail("UNKNOWN", nil)

	assert.Equal(t, "EXBanka Notification", subject)
	assert.Contains(t, body, "notification")
}
```

- [ ] **Step 2: Add testify dependency**

Run: `cd notification-service && go get github.com/stretchr/testify`

- [ ] **Step 3: Run tests**

Run: `cd notification-service && go test ./internal/sender/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add notification-service/internal/sender/templates_test.go notification-service/go.mod notification-service/go.sum
git commit -m "test(notification-service): add email template tests"
```

---

## Chunk 7: Tests — API Gateway

### Task 19: API Gateway Auth Handler Tests

**Files:**
- Create: `api-gateway/internal/handler/auth_handler_test.go`

These tests use httptest with the Gin engine and a mock gRPC client.

- [ ] **Step 1: Write auth handler tests**

```go
// api-gateway/internal/handler/auth_handler_test.go
package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestLogin_MissingFields(t *testing.T) {
	r := setupTestRouter()
	// We can't easily mock gRPC client here, so test validation only
	// A real mock would require generating a mock of AuthServiceClient

	body := map[string]string{}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/auth/login", func(c *gin.Context) {
		var loginReq loginRequest
		if err := c.ShouldBindJSON(&loginReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogin_InvalidEmail(t *testing.T) {
	r := setupTestRouter()

	body := map[string]string{"email": "not-an-email", "password": "pass"}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/auth/login", func(c *gin.Context) {
		var loginReq loginRequest
		if err := c.ShouldBindJSON(&loginReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRefreshToken_MissingField(t *testing.T) {
	r := setupTestRouter()

	body := map[string]string{}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/refresh", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/auth/refresh", func(c *gin.Context) {
		var refreshReq refreshRequest
		if err := c.ShouldBindJSON(&refreshReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestActivateAccount_MissingFields(t *testing.T) {
	r := setupTestRouter()

	body := map[string]string{"token": "abc"}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/activate", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/auth/activate", func(c *gin.Context) {
		var activateReq activateRequest
		if err := c.ShouldBindJSON(&activateReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
```

- [ ] **Step 2: Add testify dependency**

Run: `cd api-gateway && go get github.com/stretchr/testify`

- [ ] **Step 3: Run tests**

Run: `cd api-gateway && go test ./internal/handler/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/handler/auth_handler_test.go api-gateway/go.mod api-gateway/go.sum
git commit -m "test(api-gateway): add auth handler validation tests"
```

---

### Task 20: API Gateway Employee Handler Tests

**Files:**
- Create: `api-gateway/internal/handler/employee_handler_test.go`

- [ ] **Step 1: Write employee handler validation tests**

```go
// api-gateway/internal/handler/employee_handler_test.go
package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	userpb "github.com/exbanka/contract/userpb"
)

func TestCreateEmployee_MissingRequired(t *testing.T) {
	r := setupTestRouter()

	// Missing required fields
	body := map[string]interface{}{
		"first_name": "John",
		// missing last_name, email, username, role, date_of_birth, jmbg
	}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/employees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/employees", func(c *gin.Context) {
		var createReq createEmployeeRequest
		if err := c.ShouldBindJSON(&createReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEmployee_InvalidEmail(t *testing.T) {
	r := setupTestRouter()

	body := map[string]interface{}{
		"first_name":    "John",
		"last_name":     "Doe",
		"date_of_birth": 946684800,
		"email":         "not-valid",
		"username":      "johndoe",
		"role":          "EmployeeBasic",
		"jmbg":          "0101990710024",
	}
	jsonBody, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/employees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	r.POST("/api/employees", func(c *gin.Context) {
		var createReq createEmployeeRequest
		if err := c.ShouldBindJSON(&createReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetEmployee_InvalidID(t *testing.T) {
	r := setupTestRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/employees/abc", nil)

	r.GET("/api/employees/:id", func(c *gin.Context) {
		_, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
	})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEmployeeToJSON(t *testing.T) {
	resp := &userpb.EmployeeResponse{
		Id:          1,
		FirstName:   "John",
		LastName:    "Doe",
		DateOfBirth: 946684800,
		Gender:      "M",
		Email:       "john@test.com",
		Phone:       "123456",
		Address:     "123 St",
		Username:    "johndoe",
		Position:    "Teller",
		Department:  "Retail",
		Active:      true,
		Role:        "EmployeeBasic",
		Permissions: []string{"clients.read"},
		Jmbg:        "0101990710024",
	}

	result := employeeToJSON(resp)

	assert.Equal(t, int64(1), result["id"])
	assert.Equal(t, "John", result["first_name"])
	assert.Equal(t, "0101990710024", result["jmbg"])
	assert.Equal(t, "EmployeeBasic", result["role"])
}
```

- [ ] **Step 2: Run tests**

Run: `cd api-gateway && go test ./internal/handler/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/handler/employee_handler_test.go
git commit -m "test(api-gateway): add employee handler validation tests"
```

---

### Task 21: Auth Middleware Tests

**Files:**
- Create: `api-gateway/internal/middleware/auth_test.go`

- [ ] **Step 1: Write middleware tests**

```go
// api-gateway/internal/middleware/auth_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequirePermission_NoPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.Use(func(c *gin.Context) {
		// Simulate authenticated user with no permissions
		c.Set("permissions", []string{})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequirePermission_HasPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"employees.read", "clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequirePermission_MissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.create"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run tests**

Run: `cd api-gateway && go test ./internal/middleware/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/middleware/auth_test.go
git commit -m "test(api-gateway): add auth middleware permission tests"
```

---

## Chunk 8: Documentation

### Task 22: Write Backend API Documentation

**Files:**
- Create: `docs/API.md`

- [ ] **Step 1: Write API documentation**

Create `docs/API.md` with:
- Project overview
- Architecture diagram (text-based)
- Service descriptions
- All API endpoints (method, path, auth required, request/response)
- Environment variables table
- Database schema overview (Employee model fields including JMBG)
- Kafka topics and message formats
- Token types and lifetimes
- Role hierarchy and permissions matrix
- How to run locally (make commands, Docker)
- How to run tests (`go test ./...` per service)
- Swagger UI location: `http://localhost:8080/swagger/index.html`

Note: Use information from CLAUDE.md and the codebase as source material. Keep it accurate and up-to-date with all changes made in this plan (JMBG field, email links, swagger).

- [ ] **Step 2: Commit**

```bash
git add docs/API.md
git commit -m "docs: add comprehensive backend API documentation"
```

---

### Task 23: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md with new information**

Add/update the following sections:

1. In **Repository Layout**, add `docs/` folder entry:
   ```
   ├── docs/                    # API documentation and implementation plans
   ```

2. In **Commands**, add:
   ```bash
   make test          # Run all tests across all services
   ```
   (Note: This `make test` target should also be added to the Makefile — see Task 24)

3. In **Environment**, add row:
   ```
   | `FRONTEND_BASE_URL` | http://localhost:3000 | Base URL for links in emails |
   ```

4. In **Key Domain Concepts**, add JMBG section:
   ```
   **JMBG (Jedinstveni Matični Broj Građana):**
   - Unique 13-digit national identification number
   - Required for all employees, validated on create and update
   - Stored with unique index in user_db
   ```

5. In **Architecture** section, add:
   ```
   - Swagger UI: Available at /swagger/index.html on the API Gateway
   ```

6. Update **Token types** to note emails now contain clickable links:
   ```
   - Activation token: 24h, triggers email with activation link via Kafka → notification-service
   - Password reset token: 1h, triggers email with reset link via Kafka → notification-service
   ```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md with JMBG, swagger, email links, and test info"
```

---

### Task 24: Add `make test` Target to Makefile

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add test target**

Add to the Makefile:
```makefile
test:
	cd user-service && go test ./... -v
	cd auth-service && go test ./... -v
	cd notification-service && go test ./... -v
	cd api-gateway && go test ./... -v
```

- [ ] **Step 2: Run make test to verify**

Run: `make test`
Expected: All tests pass across all services

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: add make test target to run all service tests"
```

---

## Summary of Changes

| Area | What Changes |
|------|-------------|
| **JMBG** | New field on Employee model, proto, all layers through API gateway. Validated as exactly 13 digits. Unique index in DB. |
| **Swagger** | gin-swagger + swaggo annotations on all API gateway handlers. UI at `/swagger/`. |
| **Email Links** | `FRONTEND_BASE_URL` config → auth-service passes `link` in Kafka data → notification-service templates render clickable buttons + fallback URL text. |
| **Tests** | 8 test files across all 4 services covering: JMBG validation, password validation, role logic, JWT lifecycle, email templates, HTTP validation, middleware permissions, service logic with mock repos. |
| **Docs** | `docs/API.md` comprehensive backend docs + updated `CLAUDE.md`. |
