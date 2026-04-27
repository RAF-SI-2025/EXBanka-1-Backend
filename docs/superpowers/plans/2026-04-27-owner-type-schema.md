# Owner Type Schema — Implementation Plan (Spec C)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the bank sentinel pattern. Replace `(user_id, system_type string)` with two enums: `principal_type ∈ {client, employee}` (in JWT, auth tables) and `owner_type ∈ {client, bank}` (on resource rows). Identity resolution moves from per-handler helpers (`mePortfolioIdentity`, `actingEmployeeID`, `meIdentity`) into one middleware applied per route group. Schema migrations expected; no data preservation (clean break).

**Architecture:** New middleware (`api-gateway/internal/middleware/identity.go`) computes a `ResolvedIdentity` once per request based on a per-route `IdentityRule`. Handlers consume the resolved struct — no identity computation in handlers. Stock-service models drop the `BankSentinelUserID` magic; `(user_id, system_type)` columns become `(owner_type, owner_id)` with CHECK constraints. JWT claims rename `system_type` → `principal_type`, `user_id` → `principal_id`.

**Tech Stack:** Go, Gin, gorm, jwt-go, protobuf.

**Spec reference:** `docs/superpowers/specs/2026-04-27-owner-type-schema-design.md`

**Order in pipeline:** Run AFTER Plan A (uses typed sentinels for the new validation errors). BEFORE Plan E (route consolidation depends on the identity middleware).

---

## File Structure

**New files:**
- `api-gateway/internal/middleware/identity.go` — `ResolvedIdentity` + `ResolveIdentity(rule, args...)`
- `api-gateway/internal/middleware/identity_test.go` — exhaustive table tests

**Modified files (high blast radius):**
- All 12 stock-service models with `(user_id, system_type)` columns
- All 12 corresponding repositories
- ~30 handler call sites in `api-gateway/internal/handler/` (stock, OTC, fund, tax handlers)
- `auth-service/internal/service/jwt_service.go` — Claims renamed
- `auth-service/internal/service/auth_service.go` — token gen passes new field names
- `api-gateway/internal/middleware/auth.go` — `setTokenContext` stores new keys
- All proto files in `contract/stockpb/`, `contract/authpb/`, `contract/notificationpb/` referencing `system_type`
- `contract/kafka/messages.go` — payload field renames

**Deleted code:**
- `api-gateway/internal/handler/validation.go:159` — `BankSentinelUserID` constant
- `api-gateway/internal/handler/validation.go:165` — `BankSystemType` constant
- `api-gateway/internal/handler/validation.go:140` — `meIdentity()`
- `api-gateway/internal/handler/validation.go:185` — `mePortfolioIdentity()`
- `api-gateway/internal/handler/validation.go:203` — `actingEmployeeID()`
- `system_type` column from 12 stock-service models
- All test fixtures hardcoding `BankSentinelUserID` (rewritten)

---

## Task 1: Define ResolvedIdentity + ResolveIdentity middleware

**Files:**
- Create: `api-gateway/internal/middleware/identity.go`
- Create: `api-gateway/internal/middleware/identity_test.go`

- [ ] **Step 1: Failing test — exhaustive table**

```go
// api-gateway/internal/middleware/identity_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"api-gateway/internal/middleware"
)

func TestResolveIdentity_OwnerIsPrincipal_Client(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", setPrincipal("client", 42), middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" || *id.OwnerID != 42 || id.ActingEmployeeID != nil {
				t.Errorf("got %+v", id)
			}
			c.Status(200)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != 200 { t.Errorf("status %d", w.Code) }
}

func TestResolveIdentity_OwnerIsBankIfEmployee_EmployeeBecomesBank(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", setPrincipal("employee", 7), middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "bank" || id.OwnerID != nil || id.ActingEmployeeID == nil || *id.ActingEmployeeID != 7 {
				t.Errorf("got %+v", id)
			}
			c.Status(200)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != 200 { t.Errorf("status %d", w.Code) }
}

func TestResolveIdentity_OwnerIsBankIfEmployee_ClientStaysClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", setPrincipal("client", 42), middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" || *id.OwnerID != 42 || id.ActingEmployeeID != nil {
				t.Errorf("got %+v", id)
			}
			c.Status(200)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != 200 { t.Errorf("status %d", w.Code) }
}

func TestResolveIdentity_OwnerFromURLParam_EmployeeOnBehalf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clients/:client_id/x",
		setPrincipal("employee", 7),
		middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" || *id.OwnerID != 99 || *id.ActingEmployeeID != 7 {
				t.Errorf("got %+v", id)
			}
			c.Status(200)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/clients/99/x", nil))
	if w.Code != 200 { t.Errorf("status %d", w.Code) }
}

func TestResolveIdentity_OwnerFromURLParam_NonNumericReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clients/:client_id/x",
		setPrincipal("client", 1),
		middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
		func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/clients/abc/x", nil))
	if w.Code != 400 { t.Errorf("expected 400, got %d", w.Code) }
}

// setPrincipal is a test helper that mimics what AuthMiddleware sets.
func setPrincipal(pType string, pID uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("principal_type", pType)
		c.Set("principal_id", pID)
		c.Next()
	}
}
```

- [ ] **Step 2: Run — FAIL (middleware missing)**

Run: `cd api-gateway && go test ./internal/middleware/... -run TestResolveIdentity -v`

- [ ] **Step 3: Implement**

```go
// api-gateway/internal/middleware/identity.go
package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ResolvedIdentity is the per-request, fully-resolved actor and owner.
// Handlers consume this struct via c.MustGet("identity") — they do not
// compute identity themselves.
type ResolvedIdentity struct {
	PrincipalType    string  // "client" | "employee"
	PrincipalID      uint64
	OwnerType        string  // "client" | "bank"
	OwnerID          *uint64 // nil iff OwnerType == "bank"
	ActingEmployeeID *uint64 // = &PrincipalID iff PrincipalType == "employee", else nil
}

type IdentityRule int

const (
	// OwnerIsPrincipal: owner == principal. Used for /me/profile, /me/cards, etc.
	OwnerIsPrincipal IdentityRule = iota
	// OwnerIsBankIfEmployee: principal=employee → owner=bank; else owner=principal.
	// Used for /me/orders, /me/portfolios — the trading routes the bank also "owns."
	OwnerIsBankIfEmployee
	// OwnerFromURLParam: owner=client, owner_id read from a path parameter.
	// Used for /clients/:client_id/* admin-acts-on-client routes.
	OwnerFromURLParam
)

func ResolveIdentity(rule IdentityRule, args ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principalType := c.GetString("principal_type")
		principalID := c.GetUint64("principal_id")
		if principalType == "" || principalID == 0 {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "principal not set")
			return
		}

		id := &ResolvedIdentity{PrincipalType: principalType, PrincipalID: principalID}
		if principalType == "employee" {
			emp := principalID
			id.ActingEmployeeID = &emp
		}

		switch rule {
		case OwnerIsPrincipal:
			id.OwnerType = principalType
			pid := principalID
			id.OwnerID = &pid

		case OwnerIsBankIfEmployee:
			if principalType == "employee" {
				id.OwnerType = "bank"
				id.OwnerID = nil
			} else {
				id.OwnerType = "client"
				pid := principalID
				id.OwnerID = &pid
			}

		case OwnerFromURLParam:
			paramName := "client_id"
			if len(args) > 0 { paramName = args[0] }
			raw := c.Param(paramName)
			cid, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || cid == 0 {
				abortWithError(c, http.StatusBadRequest, "invalid_client_id",
					"client_id must be a positive integer")
				return
			}
			id.OwnerType = "client"
			id.OwnerID = &cid

		default:
			abortWithError(c, http.StatusInternalServerError, "internal_error",
				"unknown IdentityRule")
			return
		}

		c.Set("identity", id)
		c.Next()
	}
}

// abortWithError emits the {"error": {"code", "message"}} shape and aborts.
// Reuses validation.go's apiError pattern; if a local helper exists, use it.
func abortWithError(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"code": code, "message": msg},
	})
}
```

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/middleware/identity.go \
        api-gateway/internal/middleware/identity_test.go
git commit -m "feat(middleware): ResolvedIdentity + per-route ResolveIdentity rule"
```

---

## Task 2: Rename JWT claim fields (auth-service)

**Files:**
- Modify: `auth-service/internal/service/jwt_service.go:18-31`
- Modify: `auth-service/internal/service/auth_service.go` — every call to `GenerateAccessToken`
- Modify: `auth-service/internal/handler/grpc_handler.go` — token-validation responses
- Modify: `contract/proto/auth.proto` — token claim mirroring messages
- Modify: `contract/authpb/*.pb.go` (regenerated)

- [ ] **Step 1: Update Claims struct**

```go
// auth-service/internal/service/jwt_service.go
type Claims struct {
	PrincipalType string   `json:"principal_type"`  // was: SystemType
	PrincipalID   uint64   `json:"principal_id"`    // was: UserID
	Permissions   []string `json:"permissions"`
	Roles         []string `json:"roles"`
	jwt.RegisteredClaims
}
```

- [ ] **Step 2: Update token generation calls**

In `auth_service.go`:

```go
// Where today: jwtSvc.GenerateAccessToken(userID, "employee", roles, perms)
// Becomes:    jwtSvc.GenerateAccessToken(principalID, "employee", roles, perms)
// (parameter names unchanged; only call-site clarity if needed)
```

- [ ] **Step 3: Update proto Token message**

In `contract/proto/auth.proto`, find the `ValidateTokenResponse` (and any analogous messages):

```proto
message ValidateTokenResponse {
  bool valid = 1;
  string principal_type = 2;  // was: system_type
  uint64 principal_id = 3;    // was: user_id
  repeated string permissions = 4;
  repeated string roles = 5;
}
```

Run `make proto`.

- [ ] **Step 4: Update auth-service handler that builds the response**

```go
// auth-service/internal/handler/grpc_handler.go
return &pb.ValidateTokenResponse{
    Valid:         true,
    PrincipalType: claims.PrincipalType,
    PrincipalId:   claims.PrincipalID,
    Permissions:   claims.Permissions,
    Roles:         claims.Roles,
}, nil
```

- [ ] **Step 5: Add a JWT round-trip test**

```go
func TestJWT_RoundTripPreservesPrincipalType(t *testing.T) {
	svc := newJWTService(t)
	tok, _ := svc.GenerateAccessToken(42, "employee", []string{"EmployeeAgent"}, []string{"orders.place.own"})
	claims, err := svc.ValidateToken(tok)
	if err != nil { t.Fatal(err) }
	if claims.PrincipalType != "employee" || claims.PrincipalID != 42 {
		t.Errorf("got %+v", claims)
	}
}
```

- [ ] **Step 6: Run auth-service tests**

Run: `cd auth-service && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add auth-service/ contract/proto/auth.proto contract/authpb/
git commit -m "refactor(auth): JWT claims renamed system_type→principal_type, user_id→principal_id"
```

---

## Task 3: Update api-gateway auth middleware to read new field names

**Files:**
- Modify: `api-gateway/internal/middleware/auth.go:51-63`

- [ ] **Step 1: Update setTokenContext**

```go
// api-gateway/internal/middleware/auth.go
func setTokenContext(c *gin.Context, resp *authpb.ValidateTokenResponse) {
	c.Set("principal_type", resp.PrincipalType)  // was: system_type
	c.Set("principal_id", resp.PrincipalId)      // was: user_id
	c.Set("permissions", resp.Permissions)
	c.Set("roles", resp.Roles)
}
```

- [ ] **Step 2: Find every reader of the old keys and update**

```bash
grep -rn 'GetString("system_type")\|GetUint64("user_id")\|c\.Get("user_id")\|c\.Get("system_type")' \
    --include="*.go" api-gateway/
```

For each match: rename key. (Most matches are about to be deleted in Task 6 anyway — they're inside `meIdentity`/`mePortfolioIdentity`/`actingEmployeeID`. But fix any that survive.)

- [ ] **Step 3: Run gateway tests**

Run: `cd api-gateway && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/middleware/auth.go
git commit -m "refactor(gateway): auth middleware sets principal_type/principal_id keys"
```

---

## Task 4: Schema — replace (user_id, system_type) with (owner_type, owner_id) on stock-service models

**Files (12 models):**
- `stock-service/internal/model/order.go`
- `stock-service/internal/model/holding.go`
- `stock-service/internal/model/capital_gain.go`
- `stock-service/internal/model/tax_collection.go`
- `stock-service/internal/model/client_fund_position.go`
- `stock-service/internal/model/otc_offer.go`
- `stock-service/internal/model/otc_offer_revision.go`
- `stock-service/internal/model/option_contract.go`
- `stock-service/internal/model/fund_contribution.go`
- `stock-service/internal/model/otc_offer_read_receipt.go`
- (any other model in `stock-service/internal/model/` carrying `system_type`)

- [ ] **Step 1: Define shared owner-type constants**

Create `stock-service/internal/model/owner.go`:

```go
package model

import "errors"

type OwnerType string

const (
	OwnerClient OwnerType = "client"
	OwnerBank   OwnerType = "bank"
)

func (o OwnerType) Valid() bool {
	return o == OwnerClient || o == OwnerBank
}

// Validate enforces the rule that bank owners have nil owner_id and client
// owners have non-nil owner_id. Used in BeforeSave hooks below.
func ValidateOwner(t OwnerType, id *uint64) error {
	if !t.Valid() { return errors.New("invalid owner_type") }
	if t == OwnerBank && id != nil { return errors.New("bank owner must have nil owner_id") }
	if t == OwnerClient && id == nil { return errors.New("client owner must have non-nil owner_id") }
	return nil
}
```

- [ ] **Step 2: Update each model — `order.go` lighthouse**

```go
// stock-service/internal/model/order.go (excerpt)
type Order struct {
	ID               uint64    `gorm:"primaryKey"`
	OwnerType        OwnerType `gorm:"size:8;not null;index:idx_owner;check:owner_type IN ('client','bank')"`
	OwnerID          *uint64   `gorm:"index:idx_owner"`
	ActingEmployeeID *uint64   `gorm:"index"`
	// ... existing fields (drop UserID, SystemType) ...
	Version int64 `gorm:"not null;default:0"`
	// CreatedAt, UpdatedAt
}

func (o *Order) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(o.OwnerType, o.OwnerID)
}

// Existing BeforeUpdate hook for optimistic locking stays.
```

- [ ] **Step 3: Repeat for the other 11 models**

Same pattern: drop `UserID uint64`, `SystemType string`. Add `OwnerType`, `OwnerID *uint64`, `ActingEmployeeID *uint64`. Add `BeforeSave` hook calling `ValidateOwner`.

For OTC offer (composite parties):
```go
type OTCOffer struct {
	// ...
	InitiatorOwnerType    OwnerType `gorm:"size:8;not null;check:initiator_owner_type IN ('client','bank')"`
	InitiatorOwnerID      *uint64
	CounterpartyOwnerType *OwnerType `gorm:"size:8;check:counterparty_owner_type IN ('client','bank')"`
	CounterpartyOwnerID   *uint64
	// "last modified by" stays as a PRINCIPAL — it's an audit field, not ownership.
	LastModifiedByPrincipalType string `gorm:"size:8;not null"`
	LastModifiedByPrincipalID   uint64 `gorm:"not null"`
	ActingEmployeeID            *uint64
	// ...
}
```

- [ ] **Step 4: Update indexes**

Anywhere the old code had `gorm:"uniqueIndex:idx_user_system_type_..."` — rename to `idx_owner_...` and reference `(owner_type, owner_id)` instead of `(user_id, system_type)`.

Example for Holding:
```go
type Holding struct {
	OwnerType    OwnerType `gorm:"size:8;not null;uniqueIndex:idx_holding_owner"`
	OwnerID      *uint64   `gorm:"uniqueIndex:idx_holding_owner"`
	SecurityType string    `gorm:"uniqueIndex:idx_holding_owner"`
	SecurityID   uint64    `gorm:"uniqueIndex:idx_holding_owner"`
	// ...
}
```

- [ ] **Step 5: Build and run unit tests**

```bash
cd stock-service && go build ./...
```
Expected: a long list of compile errors at every repository and service-layer call site referencing `UserID` / `SystemType`. Tasks 5 and 6 fix these.

- [ ] **Step 6: Commit (broken intermediate)**

```bash
git add stock-service/internal/model/
git commit -m "refactor(stock-service): models use owner_type/owner_id (intermediate — repositories follow)"
```

---

## Task 5: Update stock-service repositories

**Files:** All 12 repositories in `stock-service/internal/repository/` corresponding to the models in Task 4.

- [ ] **Step 1: Replace UserID/SystemType filters with OwnerType/OwnerID**

For each repository's queries, find:
```go
db.Where("user_id = ? AND system_type = ?", uid, st).Find(&out)
```
Replace with:
```go
db.Where("owner_type = ? AND owner_id IS NOT DISTINCT FROM ?", ownerType, ownerID).Find(&out)
```

(`IS NOT DISTINCT FROM` handles NULL equality for the bank case where `owner_id IS NULL`. PostgreSQL-specific.)

- [ ] **Step 2: Update method signatures**

```go
// Before:
func (r *OrderRepository) ListByOwner(uid uint64, systemType string) ([]Order, error)

// After:
func (r *OrderRepository) ListByOwner(ownerType OwnerType, ownerID *uint64) ([]Order, error)
```

- [ ] **Step 3: Build to confirm**

```bash
cd stock-service && go build ./internal/repository/...
```
Expected: compile clean. Service-layer callers next.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/repository/
git commit -m "refactor(stock-service): repositories filter by (owner_type, owner_id)"
```

---

## Task 6: Update stock-service service layer + delete bank sentinel

**Files:**
- Modify: `stock-service/internal/service/*.go` — every call into the repositories
- Modify: any service that constructed model instances with `UserID = BankSentinelUserID`

- [ ] **Step 1: Find all call sites**

```bash
grep -rn "UserID:.*\|SystemType:" stock-service/internal/service/ | head -50
```

- [ ] **Step 2: Replace each construction**

```go
// Before:
order := &model.Order{
    UserID:     in.OwnerUserID,
    SystemType: in.OwnerSystemType,
    // ...
}

// After:
order := &model.Order{
    OwnerType:        model.OwnerType(in.OwnerType),
    OwnerID:          in.OwnerID,    // already *uint64 from the new request shape
    ActingEmployeeID: in.ActingEmployeeID,
    // ...
}
```

- [ ] **Step 3: Update request shapes (proto → Go)**

In `contract/proto/stock.proto` and OTC/fund protos, every request that today carries `(user_id, system_type)` becomes:

```proto
import "google/protobuf/wrappers.proto";

enum OwnerType {
  OWNER_TYPE_UNSPECIFIED = 0;
  OWNER_TYPE_CLIENT      = 1;
  OWNER_TYPE_BANK        = 2;
}

message PlaceOrderRequest {
  OwnerType owner_type = 1;
  google.protobuf.UInt64Value owner_id = 2;  // null when owner_type=BANK
  google.protobuf.UInt64Value acting_employee_id = 3;
  // ... business fields
}
```

Run `make proto`.

- [ ] **Step 4: Build**

```bash
cd stock-service && go build ./...
```
Expected: clean.

- [ ] **Step 5: Run unit tests (will fail in tests that hardcode UserID — fix those too)**

```bash
cd stock-service && go test ./...
```

For each failing test, update the constructor to use the new shape. If a test uses `BankSentinelUserID`, replace with `OwnerType: model.OwnerBank, OwnerID: nil`.

- [ ] **Step 6: Commit**

```bash
git add contract/proto/ contract/stockpb/ stock-service/
git commit -m "refactor(stock-service): service layer + protos use OwnerType/OwnerID"
```

---

## Task 7: Delete bank sentinel + identity helpers

**Files:**
- Modify: `api-gateway/internal/handler/validation.go` — delete lines 159 (`BankSentinelUserID`), 165 (`BankSystemType`), 140 (`meIdentity`), 185 (`mePortfolioIdentity`), 203 (`actingEmployeeID`)

- [ ] **Step 1: Find all callers of the helpers**

```bash
grep -rn "mePortfolioIdentity\|meIdentity\|actingEmployeeID\|BankSentinelUserID\|BankSystemType" \
    --include="*.go" api-gateway/
```
Expected: ~30 hits in `api-gateway/internal/handler/`.

- [ ] **Step 2: For each call site, replace with ResolvedIdentity**

```go
// Before (in stock_order_handler.go):
ownerID, systemType := mePortfolioIdentity(c)
empID := actingEmployeeID(c)
req := &stockpb.PlaceOrderRequest{
    UserId:           ownerID,
    SystemType:       systemType,
    ActingEmployeeId: empID,
    // ...
}

// After:
id := c.MustGet("identity").(*middleware.ResolvedIdentity)
req := &stockpb.PlaceOrderRequest{
    OwnerType:        toProtoOwnerType(id.OwnerType),
    OwnerId:          ownerIDToProto(id.OwnerID),
    ActingEmployeeId: empToProto(id.ActingEmployeeID),
    // ...
}
```

Add tiny conversion helpers in `api-gateway/internal/handler/validation.go`:
```go
func toProtoOwnerType(s string) stockpb.OwnerType {
	switch s {
	case "client": return stockpb.OwnerType_OWNER_TYPE_CLIENT
	case "bank":   return stockpb.OwnerType_OWNER_TYPE_BANK
	}
	return stockpb.OwnerType_OWNER_TYPE_UNSPECIFIED
}

func ownerIDToProto(p *uint64) *wrapperspb.UInt64Value {
	if p == nil { return nil }
	return wrapperspb.UInt64(*p)
}

func empToProto(p *uint64) *wrapperspb.UInt64Value {
	if p == nil { return nil }
	return wrapperspb.UInt64(*p)
}
```

- [ ] **Step 3: Delete the helpers**

In `api-gateway/internal/handler/validation.go`, remove:
- `const BankSentinelUserID uint64 = 1_000_000_000`
- `const BankSystemType = "bank"`
- `func meIdentity(...) (...)`
- `func mePortfolioIdentity(...) (...)`
- `func actingEmployeeID(...) uint64`

- [ ] **Step 4: Confirm zero references**

```bash
grep -rn "mePortfolioIdentity\|meIdentity\|actingEmployeeID\|BankSentinelUserID\|BankSystemType" \
    --include="*.go" api-gateway/
```
Expected: NO matches.

- [ ] **Step 5: Build and test**

```bash
cd api-gateway && go build ./... && go test ./...
```
Expected: PASS (some handler tests may still fail because they used the old helpers — fix per failing test).

- [ ] **Step 6: Commit**

```bash
git add api-gateway/internal/handler/
git commit -m "refactor(gateway): delete bank-sentinel helpers; handlers use ResolvedIdentity"
```

---

## Task 8: Wire ResolveIdentity middleware into routes

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go` (or current router file)

- [ ] **Step 1: Group routes by identity rule**

```go
// router_v3.go (excerpt)
me := r.Group("/api/v3/me",
    middleware.AnyAuthMiddleware,
    middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
)
{
    me.POST("/orders",       middleware.RequirePermission("orders.place.own"), h.Stock.PlaceMyOrder)
    me.GET("/orders",        middleware.RequirePermission("orders.read.own"),  h.Stock.ListMyOrders)
    me.GET("/portfolios",    middleware.RequirePermission("securities.read.holdings.own"), h.Portfolio.GetMyPortfolio)
    me.POST("/otc/offers",   middleware.RequirePermission("otc.trade.accept"), h.OTC.CreateMyOffer)
    me.POST("/funds/:fund_id/invest", middleware.RequirePermission("funds.invest.own"), h.Fund.InvestMyFund)
    // ... all /me/* trading routes
}

meProfile := r.Group("/api/v3/me",
    middleware.AnyAuthMiddleware,
    middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
)
{
    meProfile.GET("/profile",  h.Client.GetMyProfile)
    meProfile.GET("/cards",    h.Card.ListMyCards)
    meProfile.GET("/accounts", h.Account.ListMyAccounts)
    // ... non-trading /me/* routes
}

employee := r.Group("/api/v3",
    middleware.AuthMiddleware,
    middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
)
{
    employee.POST("/clients/:client_id/orders", middleware.RequirePermission("orders.place.on_behalf_client"), h.Stock.PlaceOrderForClient)
    employee.POST("/clients/:client_id/otc/offers", middleware.RequirePermission("otc.trade.accept"), h.OTC.CreateOfferForClient)
    // ... admin acts on a specific client
}
```

(Permission constants stay as strings here pending Plan D — once D lands, they become `perms.Orders.Place.Own`, etc.)

- [ ] **Step 2: Run gateway tests**

```bash
cd api-gateway && go test ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/router/
git commit -m "wire(router): ResolveIdentity middleware per route group"
```

---

## Task 9: Rename Kafka payload fields

**Files:**
- Modify: `contract/kafka/messages.go:592, 639, 653, 692`

- [ ] **Step 1: Rename fields in each message struct**

```go
// contract/kafka/messages.go (excerpts)

type AuthSessionCreatedMessage struct {
    SessionID     string `json:"session_id"`
    PrincipalType string `json:"principal_type"`  // was: SystemType
    PrincipalID   uint64 `json:"principal_id"`    // was: UserID
    // ...
}

type StockFundInvestedMessage struct {
    OwnerType       string  `json:"owner_type"`        // was: SystemType
    OwnerID         *uint64 `json:"owner_id"`          // was: UserID (now nullable)
    ActingEmployeeID *uint64 `json:"acting_employee_id"`
    // ...
}

type StockFundRedeemedMessage struct {
    OwnerType        string  `json:"owner_type"`
    OwnerID          *uint64 `json:"owner_id"`
    ActingEmployeeID *uint64 `json:"acting_employee_id"`
    // ...
}

type OTCParty struct {
    OwnerType string  `json:"owner_type"`  // was: SystemType
    OwnerID   *uint64 `json:"owner_id"`    // was: UserID (now nullable)
}
```

- [ ] **Step 2: Update producers in stock-service**

```bash
grep -rn 'SystemType:\|UserID:' stock-service/internal/service/ | grep -E 'Message|Kafka'
```

For each match, update the struct literal to use the new field names.

- [ ] **Step 3: Update consumers (notification-service, anyone consuming AuthSessionCreated)**

```bash
grep -rn 'msg\.SystemType\|msg\.UserID' --include="*.go" notification-service/
```

- [ ] **Step 4: Run tests**

```bash
make test
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go stock-service/ notification-service/
git commit -m "refactor(kafka): rename system_type→owner_type, add nullable owner_id"
```

---

## Task 10: Integration tests — actuary-limit regression + on-behalf-of-client

**Files:**
- Create: `test-app/workflows/wf_actuary_limit_test.go`
- Create or modify: `test-app/workflows/wf_owner_type_isolation_test.go` (replaces `wf_systemtype_isolation_test.go`)

- [ ] **Step 1: Actuary-limit regression test**

```go
//go:build integration
// +build integration

package workflows_test

import (
	"net/http"
	"testing"
)

// Re-creates the Phase 3 regression: an employee with a low actuary limit
// places a large /me/order (which resolves to bank ownership). The actuary
// gate must fire on acting_employee_id.
func TestActuaryLimit_EmployeeMeOrder_GateFires(t *testing.T) {
	t.Parallel()
	emp := setupEmployeeWithLowActuaryLimit(t, /* limit */ 100)

	resp := emp.POST("/api/v3/me/orders", map[string]interface{}{
		"security_id":   1,
		"side":          "buy",
		"quantity":      1000,
		"price":         50, // 50_000 total — well above limit
		"order_type":    "market",
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body: %s)", resp.Code, resp.Body)
	}
	if !contains(resp.Body, "actuary limit") {
		t.Errorf("expected 'actuary limit' in body: %s", resp.Body)
	}
}
```

- [ ] **Step 2: On-behalf-of-client test**

```go
func TestOnBehalfClient_EmployeePlacesOrderForClient(t *testing.T) {
	t.Parallel()
	emp := setupActivatedEmployee(t, /* role */ "EmployeeAgent")
	cli := setupActivatedClient(t)

	startBalance := getAccountBalance(t, cli.AccountID)

	resp := emp.POST("/api/v3/clients/"+cli.ClientID+"/orders", map[string]interface{}{
		"security_id": 1,
		"side":        "buy",
		"quantity":    10,
		"price":       5,
	})
	if resp.Code != http.StatusOK { t.Fatalf("got %d: %s", resp.Code, resp.Body) }

	// Client account debited; client owns the resulting holding.
	endBalance := getAccountBalance(t, cli.AccountID)
	if startBalance - endBalance != 50 {
		t.Errorf("client balance delta wrong: %v", startBalance-endBalance)
	}

	// Holding row exists with owner_type=client, owner_id=cli.ClientID, acting_employee_id=emp.EmployeeID.
	holding := getHolding(t, cli.ClientID, /* security */ 1)
	if holding.OwnerType != "client" || holding.OwnerID == nil || *holding.OwnerID != cli.ClientIDAsUint64 {
		t.Errorf("holding ownership wrong: %+v", holding)
	}
	if holding.ActingEmployeeID == nil || *holding.ActingEmployeeID != emp.EmployeeIDAsUint64 {
		t.Errorf("holding acting_employee wrong: %+v", holding)
	}
}
```

- [ ] **Step 3: Owner-type isolation test (replaces system_type test)**

```go
func TestOwnerType_ClientCannotSeeBankOrders(t *testing.T) {
	t.Parallel()
	emp := setupActivatedEmployee(t, "EmployeeSupervisor")
	cli := setupActivatedClient(t)

	// Employee places a /me/order — owner=bank.
	emp.POST("/api/v3/me/orders", map[string]interface{}{ /* ... */ })

	// Client lists THEIR orders — should see zero.
	resp := cli.GET("/api/v3/me/orders")
	if !isEmptyArray(resp.Body) {
		t.Errorf("client saw bank orders: %s", resp.Body)
	}
}
```

- [ ] **Step 4: Run integration tests**

```bash
make test-integration TESTS='ActuaryLimit_|OnBehalfClient_|OwnerType_'
```
Expected: PASS.

- [ ] **Step 5: Delete the old `wf_systemtype_isolation_test.go`**

```bash
rm test-app/workflows/wf_systemtype_isolation_test.go
```

- [ ] **Step 6: Commit**

```bash
git add test-app/workflows/wf_actuary_limit_test.go \
        test-app/workflows/wf_owner_type_isolation_test.go
git rm test-app/workflows/wf_systemtype_isolation_test.go
git commit -m "test(owner-type): actuary regression + on-behalf-of-client + ownership isolation"
```

---

## Task 11: Drop legacy columns from DB on next deploy

The `db.AutoMigrate` mechanism does NOT drop unused columns by default. For the clean break, we want them gone.

- [ ] **Step 1: Add a one-shot startup migration in stock-service**

```go
// stock-service/cmd/main.go (add before AutoMigrate)
dropLegacyColumns(db, "orders", []string{"user_id", "system_type"})
dropLegacyColumns(db, "holdings", []string{"user_id", "system_type"})
// ... for each table
```

```go
func dropLegacyColumns(db *gorm.DB, table string, cols []string) {
    for _, col := range cols {
        if db.Migrator().HasColumn(table, col) {
            if err := db.Migrator().DropColumn(table, col); err != nil {
                log.Printf("WARN: drop %s.%s: %v", table, col, err)
            }
        }
    }
}
```

- [ ] **Step 2: Verify after deploy**

```sql
\d orders;
\d holdings;
-- expected: no user_id, no system_type columns
```

- [ ] **Step 3: After successful deploy + verification, remove the dropLegacyColumns calls**

(One-shot — keep them for one deploy cycle, then delete.)

- [ ] **Step 4: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "chore(stock-service): drop legacy user_id/system_type columns on startup"
```

---

## Task 12: Update Specification.md

- [ ] **Step 1: Section 6 (permissions) — note ResolvedIdentity middleware**

Add reference: identity is resolved by middleware per route group; handlers consume `*ResolvedIdentity` from context.

- [ ] **Step 2: Section 18 (entities) — update stock-service models**

Update every entity listing that previously documented `(user_id, system_type)` to `(owner_type, owner_id)` + `acting_employee_id`.

- [ ] **Step 3: Section 19 (Kafka) — update payload schemas**

Reflect the field renames.

- [ ] **Step 4: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): owner_type schema, ResolvedIdentity, payload renames"
```

---

## Self-Review

**Spec coverage:**
- ✅ Two enums (principal_type / owner_type) — Tasks 2, 4
- ✅ acting_employee_id on rows — Task 4
- ✅ ResolvedIdentity middleware — Task 1
- ✅ Three identity rules wired per route group — Task 8
- ✅ Helpers deleted — Task 7
- ✅ JWT claims renamed — Task 2, 3
- ✅ Proto fields renamed — Task 6
- ✅ Kafka payloads renamed — Task 9
- ✅ Schema dropped — Task 11
- ✅ Functional behavior preserved (employee for client, employee for bank) — Task 10 tests both

**Placeholders:** None.

**Type consistency:**
- `model.OwnerType` (Go enum) ↔ `stockpb.OwnerType_OWNER_TYPE_CLIENT/BANK` (proto enum) — translated via `toProtoOwnerType()`.
- `*uint64` for `OwnerID` in Go ↔ `*wrapperspb.UInt64Value` in proto — translated via `ownerIDToProto()`.
- `acting_employee_id` is `*uint64` everywhere (column, request, response).

**Commit cadence:** ~12 commits. Several are intermediate (Task 4 leaves the build broken; Task 5 + 6 fix it). Acceptable because the chain is single-developer or single-subagent.
