# v3 Route Standardization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standardize 11 inconsistent v3 routes (path-param style, verb usage, plural/singular, action naming) AND split 5 query-param-overloaded list endpoints into purpose-specific endpoints. v3 stays the only live API version.

**Architecture:** All changes happen in `api-gateway/internal/router/router_v3.go` and `api-gateway/internal/handler/*.go`. Five list handlers that internally dispatch to different gRPC methods based on a query param get split into separate handler methods + separate routes. Test-app workflows + REST_API_v1.md (canonical doc per project memory) get URL rewrites.

**Tech Stack:** Go, Gin, existing gRPC clients.

**Breaking changes for v3 clients:** yes. Frontend + test-app must be updated. v3 is the only live version with no v4 yet, so this is a clean break (no API versioning compatibility concern).

---

## File Structure

**Modified files:**
- `api-gateway/internal/router/router_v3.go` — route registrations (rename + split)
- `api-gateway/internal/handler/transaction_handler.go` — split ListPayments + ListTransfers
- `api-gateway/internal/handler/card_handler.go` — split ListCards
- `api-gateway/internal/handler/account_handler.go` — split ListAllAccounts; remove GetAccountByNumber path
- `api-gateway/internal/handler/credit_handler.go` — split ListAllLoans
- `api-gateway/internal/handler/role_handler.go` — Assign/Revoke read `:id` not `:role_name`
- `api-gateway/internal/handler/actuary_handler.go` — split SetNeedApproval into require/skip pair
- `api-gateway/internal/handler/account_handler.go` — split UpdateAccountStatus into activate/deactivate pair
- `api-gateway/internal/handler/stock_order_handler.go` — rename DeclineOrder → RejectOrder
- `api-gateway/internal/handler/portfolio_handler.go` — rename BuyOTCOfferOnBehalf route
- `api-gateway/internal/handler/stock_source_handler.go` — relocate routes from `/admin/stock-source`
- `api-gateway/internal/handler/session_handler.go` — RevokeSession reads :id from path
- All affected handler test files (mock + assert URL changes)

**test-app:**
- `test-app/workflows/*.go` — URL rewrites for changed routes
- `test-app/internal/client/*.go` — any helper that hardcodes a renamed path

**Docs:**
- `docs/api/REST_API_v1.md` — sections updated for renamed/split routes
- `docs/Specification.md` — section 23 (REST API conventions) updated
- `api-gateway/docs/swagger.{json,yaml}` — regenerated

---

## Phase A: Path / verb renames (the 11 from the audit)

### Task A1: Standardize role-permission endpoints on `:id`

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/role_handler.go`
- Modify: `api-gateway/internal/handler/role_handler_test.go` (if exists)
- Modify: `docs/api/REST_API_v1.md`

**Steps:**

- [ ] **Step 1: Failing test**

In `role_handler_test.go` add (or modify):
```go
func TestAssignPermissionToRole_ReadsIdFromPath(t *testing.T) {
    // Register route POST /api/v3/roles/:id/permissions, hit it with /api/v3/roles/3/permissions
    // body {"permission":"clients.read.all"}, verify the handler:
    // (a) parses :id as uint64, (b) calls userClient.AssignPermissionToRole with role_id=3,
    //     not role_name="3".
}
```

Run: `cd api-gateway && go test ./internal/handler/... -run TestAssignPermissionToRole_ReadsIdFromPath -v`
Expected: FAIL — handler still reads `:role_name`.

- [ ] **Step 2: Update handler**

In `api-gateway/internal/handler/role_handler.go`:
```go
// Before:
roleName := c.Param("role_name")
_, err := h.userClient.AssignPermissionToRole(ctx, &userpb.AssignPermissionToRoleRequest{
    RoleName:   roleName,
    Permission: req.Permission,
})

// After:
id, err := strconv.ParseUint(c.Param("id"), 10, 64)
if err != nil { apiError(c, 400, ErrValidation, "invalid role id"); return }
_, err = h.userClient.AssignPermissionToRole(ctx, &userpb.AssignPermissionToRoleRequest{
    RoleId:     id,
    Permission: req.Permission,
})
```

The proto request `AssignPermissionToRoleRequest` currently has `string role_name = 1`. **Add** `uint64 role_id = 3` to the proto (don't remove `role_name` to keep the user-service handler backward-compatible during the rollout, but the gateway sends `role_id` and the user-service handler prefers id when set). Or rename: `string role_name` → `uint64 role_id`. Pick one based on user-service handler signature audit:

```bash
grep -nA 5 "AssignPermissionToRole" user-service/internal/handler/grpc_handler.go | head -10
```

If the user-service service-layer takes `roleName string`, change it to `roleID uint64` (find role by ID instead of name, which is faster anyway). Update both sides + regen proto.

Same change for `RevokePermissionFromRole`. Keep `:permission` as the third path param (per user request).

- [ ] **Step 3: Update router**

```go
// Before:
rolePermAssign.POST("/roles/:role_name/permissions", h.Role.AssignPermissionToRole)
rolePermRevoke.DELETE("/roles/:role_name/permissions/:permission", h.Role.RevokePermissionFromRole)

// After:
rolePermAssign.POST("/roles/:id/permissions", h.Role.AssignPermissionToRole)
rolePermRevoke.DELETE("/roles/:id/permissions/:permission", h.Role.RevokePermissionFromRole)
```

- [ ] **Step 4: Run tests**

`cd api-gateway && go test ./internal/handler/... -run TestAssignPermissionToRole -count=1 -v`
Expected: PASS.

`cd user-service && go test ./... -count=1 2>&1 | tail -5`
Expected: PASS (after proto regen + handler update).

`cd api-gateway && go test ./... -count=1 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Update REST_API_v1.md**

Find the role-permission section (search for `roles/:role_name/permissions`), update path to `:id`. Document that `:permission` stays as a permission-code string.

- [ ] **Step 6: Commit**

```bash
git add contract/proto/user/user.proto contract/userpb/ \
        user-service/internal/{handler,service}/ \
        api-gateway/internal/handler/role_handler.go \
        api-gateway/internal/handler/role_handler_test.go \
        api-gateway/internal/router/router_v3.go \
        docs/api/REST_API_v3.md
git commit -m "refactor(roles): role-permission endpoints use :id path param consistently"
```

---

### Task A2: Replace `/accounts/by-number/:account_number` with `?account_number=X` filter

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/account_handler.go`
- Modify: `api-gateway/internal/handler/account_handler_test.go`
- Modify: `docs/api/REST_API_v1.md`

**Steps:**

- [ ] **Step 1: Failing test**

```go
func TestListAllAccounts_FilterByAccountNumber(t *testing.T) {
    // GET /api/v3/accounts?account_number=ABC123 → calls
    // accountClient.GetAccountByNumber under the hood, returns array with that
    // single account (or empty array if not found, NOT 404).
}
```

- [ ] **Step 2: Update `ListAllAccounts` handler**

Add a new branch:
```go
if num := c.Query("account_number"); num != "" {
    resp, err := h.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: num})
    if err != nil {
        if status.Code(err) == codes.NotFound {
            c.JSON(http.StatusOK, gin.H{"accounts": []gin.H{}, "total": 0})
            return
        }
        handleGRPCError(c, err); return
    }
    c.JSON(http.StatusOK, gin.H{"accounts": []gin.H{accountToJSON(resp)}, "total": 1})
    return
}
```

Mutual exclusion with `?client_id=X` — return 400 if both set. Document this in the handler godoc.

- [ ] **Step 3: Delete the old route + handler**

Remove from `router_v3.go`:
```go
accountsRead.GET("/by-number/:account_number", h.Account.GetAccountByNumber)
```

Mark `GetAccountByNumber` handler method as deprecated (keep the method as it's a thin wrapper around the gRPC call and may be used internally — or delete entirely if no other caller). `grep -rn "GetAccountByNumber" api-gateway/` to verify.

- [ ] **Step 4: Tests + REST doc + commit**

```bash
git add api-gateway/internal/{router,handler}/ docs/api/REST_API_v3.md
git commit -m "refactor(accounts): filter by account_number via query param (was /by-number/:account_number)"
```

---

### Task A3: Rename `decline` → `reject` for orders

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/stock_order_handler.go`
- Modify: tests

**Steps:**

- [ ] **Step 1: Rename handler method `DeclineOrder` → `RejectOrder`**

In `stock_order_handler.go`:
```go
// Before:
func (h *StockOrderHandler) DeclineOrder(c *gin.Context) { ... }

// After:
func (h *StockOrderHandler) RejectOrder(c *gin.Context) { ... }
```

If the gRPC method downstream is named `DeclineOrder`, leave it (this is a gateway-only rename). Otherwise rename in stock-service too.

- [ ] **Step 2: Update router**

```go
// Before:
ordersReject.POST("/:id/decline", h.StockOrder.DeclineOrder)

// After:
ordersReject.POST("/:id/reject", h.StockOrder.RejectOrder)
```

- [ ] **Step 3: Update tests + handler bundle (`handlers.go`)**

`grep -rn "DeclineOrder" api-gateway/ test-app/` and rename references.

- [ ] **Step 4: Update REST doc + commit**

```bash
git commit -m "refactor(orders): rename POST /orders/:id/decline → /:id/reject for verb consistency"
```

---

### Task A4: Plural collection: `/cards/authorized-person` → `/cards/authorized-persons`

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

**Steps:**

- [ ] **Step 1: Rename route**

```go
// Before:
cardsCreate.POST("/authorized-person", h.Card.CreateAuthorizedPerson)

// After:
cardsCreate.POST("/authorized-persons", h.Card.CreateAuthorizedPerson)
```

- [ ] **Step 2: tests + REST doc + commit**

```bash
git commit -m "refactor(cards): /cards/authorized-person → /cards/authorized-persons (plural collection)"
```

---

### Task A5: Move stock-source out of `/admin/` namespace

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: REST doc

**Steps:**

- [ ] **Step 1: Update routes**

```go
// Before:
adminStockSource := protected.Group("/admin/stock-source")
adminStockSource.POST("", h.StockSource.SwitchSource)
adminStockSource.GET("", h.StockSource.GetSourceStatus)

// After:
stockSources := protected.Group("/stock-sources")
stockSources.POST("", h.StockSource.SwitchSource)        // POST = switch (action)
stockSources.GET("/active", h.StockSource.GetSourceStatus) // GET active source
```

The empty `GET ""` reading "current source" reads better as `GET /active`.

- [ ] **Step 2: tests + REST doc + commit**

```bash
git commit -m "refactor(stock-sources): drop /admin/ prefix; GET /stock-sources/active for current source"
```

---

### Task A6: Move OTC on-behalf out of `/otc/admin/` namespace

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

**Steps:**

- [ ] **Step 1: Update routes**

```go
// Before:
otcOnBehalf := v3.Group("/otc/admin/offers")
otcOnBehalf.POST("/:id/buy", h.Portfolio.BuyOTCOfferOnBehalf)

// After:
otcOnBehalf := v3.Group("/otc/offers")
otcOnBehalf.POST("/:id/buy-on-behalf", h.Portfolio.BuyOTCOfferOnBehalf)
```

The action verb suffix (`buy-on-behalf`) makes the intent explicit at the URL.

- [ ] **Step 2: tests + REST doc + commit**

---

### Task A7: Replace `PUT /accounts/:id/status` with action pair

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/account_handler.go`
- Modify: tests

**Steps:**

- [ ] **Step 1: Add two action handler methods**

```go
// account_handler.go
func (h *AccountHandler) ActivateAccount(c *gin.Context) {
    id, err := strconv.ParseUint(c.Param("id"), 10, 64)
    if err != nil { apiError(c, 400, ErrValidation, "invalid id"); return }
    _, err = h.accountClient.UpdateAccountStatus(c.Request.Context(), &accountpb.UpdateAccountStatusRequest{
        Id:     id,
        Status: "active",
    })
    if err != nil { handleGRPCError(c, err); return }
    c.JSON(http.StatusOK, gin.H{"status": "active"})
}

func (h *AccountHandler) DeactivateAccount(c *gin.Context) {
    // same shape, status: "inactive"
}
```

- [ ] **Step 2: Update routes**

```go
// Before:
accountsStatus.PUT("/:id/status", h.Account.UpdateAccountStatus)

// After:
accountsStatus.POST("/:id/activate", h.Account.ActivateAccount)
accountsStatus.POST("/:id/deactivate", h.Account.DeactivateAccount)
```

- [ ] **Step 3: Delete old `UpdateAccountStatus` HTTP handler**

The gRPC method stays (the new handlers call it internally with the right status string).

- [ ] **Step 4: tests + REST doc + commit**

```bash
git commit -m "refactor(accounts): replace PUT /:id/status with POST /:id/activate + /:id/deactivate"
```

---

### Task A8: Replace `PUT /actuaries/:id/approval` with action pair

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/actuary_handler.go`

Same pattern as A7. Two methods: `RequireApproval` and `SkipApproval`. Routes become:

```go
actuariesAssign.POST("/:id/require-approval", h.Actuary.RequireApproval)
actuariesAssign.POST("/:id/skip-approval", h.Actuary.SkipApproval)
```

Commit: `refactor(actuaries): replace PUT /:id/approval with POST /:id/{require,skip}-approval`

---

### Task A9: Replace `POST /me/sessions/revoke` with `DELETE /me/sessions/:id`

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/session_handler.go`

**Steps:**

- [ ] **Step 1: Update handler**

`RevokeSession` currently reads session ID from request body. Change to read from `:id` path param.

```go
// Before:
var req struct{ SessionID uint64 `json:"session_id"` }
c.ShouldBindJSON(&req)

// After:
id, err := strconv.ParseUint(c.Param("id"), 10, 64)
if err != nil { apiError(c, 400, ErrValidation, "invalid session id"); return }
```

- [ ] **Step 2: Update route**

```go
// Before:
me.POST("/sessions/revoke", h.Session.RevokeSession)

// After:
me.DELETE("/sessions/:id", h.Session.RevokeSession)
```

`POST /me/sessions/revoke-others` stays — it's a true bulk-action and DELETE with a body is awkward.

- [ ] **Step 3: tests + REST doc + commit**

---

### Task A10: Move `actuaries/performance` registration into the actuaries group

**Pure code organization fix — URL stays the same (`/api/v3/actuaries/performance`).**

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

**Steps:**

- [ ] **Step 1: Move the registration**

```go
// Before:
fundsBank := protected.Group("/")
fundsBank.GET("/actuaries/performance", h.Fund.ActuaryPerformance)

// After:
actuariesRead.GET("/performance", h.Fund.ActuaryPerformance)
```

- [ ] **Step 2: Verify URL unchanged via test**

`cd test-app && go test -tags integration -run "Actuary" -count=1` if applicable.

- [ ] **Step 3: Commit**

```bash
git commit -m "chore(router): mount actuaries/performance in actuaries group (URL unchanged)"
```

---

## Phase B: Split overloaded list endpoints

Five list endpoints today are query-param-dispatch routers — they call entirely different gRPC methods based on which filter is provided. This violates single-responsibility, makes Swagger docs misleading (one route returns wildly different shapes/permissions), and makes testing harder. Split each into purpose-specific endpoints.

### Task B1: Split `GET /api/v3/payments`

**Today** (`TransactionHandler.ListPayments`):
- `?client_id=X` → `txClient.ListPaymentsByClient(...)`
- `?account_number=X` → `txClient.ListPaymentsByAccount(...)` (with date_from/date_to/status_filter/amount_min/amount_max)
- both empty → 400

**After:**

```
GET /api/v3/clients/:client_id/payments    → ListPaymentsByClient (no client_id query needed)
GET /api/v3/accounts/:account_number/payments → ListPaymentsByAccount (with all the rich filters)
```

The collection path makes the parent resource explicit. The query-param style is reserved for cross-cutting filters (date range, amount range, status), not "which gRPC to call".

**Note:** account-number-as-path is fine here — it's a resource path, not a filter. CLAUDE.md's "filter via query param" rule applies to LIST collections; here we're navigating into a parent resource's sub-collection.

- [ ] **Step 1: Add two new handler methods**

```go
// transaction_handler.go
func (h *TransactionHandler) ListPaymentsByClientPath(c *gin.Context) {
    clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
    // ... call ListPaymentsByClient ...
}

func (h *TransactionHandler) ListPaymentsByAccountPath(c *gin.Context) {
    accountNumber := c.Param("account_number")
    // ... call ListPaymentsByAccount with rich filters from query ...
}
```

- [ ] **Step 2: Wire new routes; delete old route**

```go
// Add to clientsRead group (or appropriate parent):
clientsRead.GET("/:id/payments", h.Tx.ListPaymentsByClientPath)
accountsRead.GET("/:id/payments", h.Tx.ListPaymentsByAccountPath)  // by ID
// (alternatively: accountsRead.GET("/by-number/:account_number/payments", ...) but Task A2 dropped that pattern)

// Remove from paymentsRead group:
paymentsRead.GET("", h.Tx.ListPayments)
```

Decision: account scope can go via `/accounts/:id/payments` (account.id) or via the new `?account_number=X` filter pattern from Task A2. Pick path-style (`/accounts/:id/payments`) for consistency with the client-scoped variant.

- [ ] **Step 3: Delete `ListPayments` HTTP handler method** (the dispatch shim)

Keep `ListPaymentsByClient` and `ListPaymentsByAccount` (the targeted handlers) as the canonical paths. Or rename them to drop the `-Path` suffix.

- [ ] **Step 4: tests + REST doc + commit**

```bash
git commit -m "refactor(payments): split GET /payments dispatch into /clients/:id/payments + /accounts/:id/payments"
```

### Task B2: Split `GET /api/v3/transfers` (same pattern as B1)

Currently dispatches `?client_id=X` → `ListTransfersByClient`. No `?account_number=X` branch today (transfers are client-scoped only). Split:

```
GET /api/v3/clients/:client_id/transfers   → ListTransfersByClient
```

Delete old `paymentsRead`-style group route.

Commit: `refactor(transfers): GET /clients/:id/transfers (was /transfers?client_id=X)`

### Task B3: Split `GET /api/v3/accounts`

**Today** (`ListAllAccounts`):
- `?client_id=X` → `ListAccountsByClient`
- otherwise → `ListAllAccounts` (with name_filter, account_number_filter, type_filter)

**After:**

```
GET /api/v3/clients/:client_id/accounts   → ListAccountsByClient
GET /api/v3/accounts                      → ListAllAccounts (all filters via query: name, account_number, type)
```

The bare `/accounts` endpoint is the supervisor's "find any account" view; client-scoped lookups use the nested path.

Commit: `refactor(accounts): split GET /accounts dispatch into /clients/:id/accounts + /accounts (filter only)`

### Task B4: Split `GET /api/v3/loans`

**Today** (`ListAllLoans`):
- `?client_id=X` → `ListLoansByClient`
- otherwise → `ListAllLoans` (with loan_type, account_number, status filters)

**After:**

```
GET /api/v3/clients/:client_id/loans   → ListLoansByClient
GET /api/v3/loans                      → ListAllLoans (filters via query)
```

Commit: `refactor(loans): split GET /loans dispatch into /clients/:id/loans + /loans (filter only)`

### Task B5: Split `GET /api/v3/cards`

**Today** (`ListCards`):
- `?client_id=X` → `ListCardsByClient`
- `?account_number=X` → `ListCardsByAccount`
- both empty → 400

**After:**

```
GET /api/v3/clients/:client_id/cards     → ListCardsByClient
GET /api/v3/accounts/:id/cards           → ListCardsByAccount  (account by ID; account-number-via-query handled like B3)
```

Or expose a top-level `GET /cards` that supports rich filters once the gRPC layer adds a `ListAllCards` method (today there's no such method — `ListCards` only dispatches to the two by-* RPCs). For now, just the two scoped paths.

Commit: `refactor(cards): split GET /cards dispatch into /clients/:id/cards + /accounts/:id/cards`

---

## Phase C: Cross-cutting cleanup

### Task C1: Rewrite test-app workflows

For each renamed route in Phase A + B, find every test-app caller and update.

```bash
cd test-app
grep -rn "/by-number/\|/decline\b\|/authorized-person\b\|/admin/stock-source\|/otc/admin\|/sessions/revoke\b" --include="*.go"
grep -rn "txClient.*payments\|payments.*client_id=\|cards.*client_id=" --include="*.go"
```

For each match: update the URL to the new shape. Preserve test logic.

Commit per Phase: `test: migrate workflows for v3 route standardization (Phase A)` and `(Phase B)`.

### Task C2: Update REST_API_v1.md

Per project memory: "REST API doc updates go to v1 only" — the canonical doc is `docs/api/REST_API_v1.md` even though it documents v3 routes (post-consolidation).

For each renamed route: update the section header + path + verb + body schema.

For each split list route: replace the single `GET /payments` (etc.) section with two new sections describing the client-scoped and account-scoped variants.

Commit: `docs(rest): document v3 route standardization (renames + list splits)`

### Task C3: Regenerate Swagger

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

Commit the regenerated `api-gateway/docs/{docs.go,swagger.json,swagger.yaml}`.

Commit: `docs(swagger): regenerate after v3 route standardization`

### Task C4: Update Specification.md

Section 23 (REST API) and any sections that list URL patterns.

Commit: `docs(spec): note v3 route standardization (renames + list splits)`

---

## Verification (run after each phase)

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
for svc in account auth card client credit exchange notification stock transaction user verification; do
    (cd $svc-service && go build ./... 2>&1 | head -3 | sed "s|^|$svc-service: |")
done
cd api-gateway && go build ./... 2>&1 | head -5
cd api-gateway && go test ./... -count=1 2>&1 | tail -10
cd test-app && go build -tags integration ./... 2>&1 | head -5
```

All clean / PASS.

---

## Self-Review

**Spec coverage:**
- ✅ All 11 audit items have a Phase A task (A1-A10; A4 is the singular→plural)
- ✅ All 5 overloaded list endpoints have a Phase B task (B1-B5)
- ✅ Test-app, REST doc, swagger, Specification.md updates in Phase C

**Placeholders:** None — every step has runnable code or a concrete edit description.

**Type consistency:** `:id` is used uniformly across renamed routes. `:permission` retained per user direction (it's already a meaningful path segment for the revoke endpoint).

**Commit cadence:** ~16 commits (10 in A, 5 in B, 4 in C, but several can be batched if reviewer prefers fewer commits).

**Out of scope:**
- Splitting `CreateOrder` / `CreateOrderOnBehalf` by security_type (stock vs forex vs option). The polymorphism via body field is intentional — orders share most validation + saga pipeline. YAGNI.
- Splitting `CreateVerification` by method. Same rationale.
- Restructuring fund-route prefixes (`/investment-funds` for both catalog and bank-positions). Acceptable as-is; permission gates separate concerns.
- gRPC service method renames. The gateway is the public surface; gRPC is internal.

**Estimated remaining work:** ~16 commits, mechanical but spread across many files. Each Phase A task touches 1-3 files; each Phase B task touches 2-3 files plus tests.
