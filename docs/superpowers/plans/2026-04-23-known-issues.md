# Known-Issues Plan — Authorization + System-Type Filter

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix one concrete authorization bug discovered while investigating user-reported issues. A logged-in client can see/cancel orders and holdings that belong to employees with the *same numeric user_id*. Root cause: every `/me/*` query in stock-service filters by `user_id` alone, ignoring `system_type`. Employees (in `user_db.employees`) and clients (in `client_db.clients`) have independent auto-increment IDs that routinely collide.

**Also covers:** verifying the two user-reported issues that need no separate fix:
- **Taxes:** verified working end-to-end (capital gains on sell / OTC / option-exercise, monthly cron, 15% rate, currency conversion, REST endpoints, integration test). Nothing to do here.
- **Portfolio not showing bought positions + cannot sell:** confirmed to be the *same* root cause as bug #3 (execution context cancellation). Phase 1 (`2026-04-22-unblock-order-flow.md`) fixes it. Nothing extra to do here.

**Architecture:** Thread `system_type` from the JWT context through `api-gateway` → gRPC requests → stock-service service layer → repository `WHERE` clauses. Same treatment for every `Order` / `OrderTransaction` / `Holding` / `CapitalGain` query that's scoped to a single user. Proto requests that carry `user_id` today get a sibling `system_type` string field.

**Tech Stack:** Go 1.22, Gin, protobuf (required: regenerate `contract/stockpb/`), GORM.

**Do not commit when just writing this plan.** Commit steps are TDD-style during execution.

**Verified during investigation (facts that ground the fix):**
- Gateway auth middleware already sets `c.Set("system_type", resp.SystemType)` (`api-gateway/internal/middleware/auth.go:56`). So the gateway HAS `system_type`; it just doesn't forward it.
- `Order.SystemType` exists in `stock-service/internal/model/order.go:14` (already populated at placement).
- `Holding.SystemType` exists in `stock-service/internal/model/holding.go` (set at Upsert-time in `portfolio_service.go:75`).
- `CapitalGain.SystemType` is set on both `ProcessSellFill` and OTC paths (per tax investigation).
- `order_service.GetOrder`, `CancelOrder`, `ListMyOrders` all filter by `user_id` but not `system_type`. Confirmed at:
  - `stock-service/internal/service/order_service.go:244` (`CancelOrder` ownership check)
  - `stock-service/internal/service/order_service.go:278` (`GetOrder` ownership check)
  - `stock-service/internal/service/order_service.go:290` (`ListMyOrders`)
- `holding_repository.ListByUser` (lines 98-117) and `GetByUserAndSecurity` — also missing `system_type`.

---

## Context

The ID collision is real: the app seeds at least 1 admin employee; first client created gets client.id = 1; admin employee.id = 1. Employee #1 and client #1 both have `user_id = 1`. When client #1 calls `GET /api/v1/me/orders`, the repo returns *all orders with UserID=1* regardless of SystemType, so the client sees the admin's activity too.

This is a 4-layer fix:

1. **contract/proto/stock/stock.proto** — add `system_type` fields to every user-scoped request type.
2. **api-gateway handlers** — read `system_type` from context and pass it into each RPC call.
3. **stock-service service layer** — accept `systemType` in every user-scoped method.
4. **stock-service repository layer** — add `AND system_type = ?` to every user-scoped query.

---

## File Structure

**Modified:**
- `contract/proto/stock/stock.proto` — add `string system_type = N;` to: `ListMyOrdersRequest`, `GetOrderRequest`, `CancelOrderRequest`, `ListHoldingsRequest`, `GetHoldingRequest`, any `ListMyTax*Request`, and equivalents.
- `stock-service/internal/handler/order_handler.go` — pass `req.SystemType` into service calls.
- `stock-service/internal/handler/portfolio_handler.go` — same.
- `stock-service/internal/service/order_service.go` — accept + propagate `systemType`.
- `stock-service/internal/service/portfolio_service.go` — same.
- `stock-service/internal/repository/order_repository.go` — add `AND system_type = ?` to `ListByUser`, `GetByID` callers that take ownership.
- `stock-service/internal/repository/holding_repository.go` — same for `ListByUser`, `GetByUserAndSecurity`.
- `stock-service/internal/repository/capital_gain_repository.go` — same for user-scoped queries.
- `api-gateway/internal/handler/stock_order_handler.go` — read `system_type` from JWT ctx, set on RPC requests.
- `api-gateway/internal/handler/portfolio_handler.go` — same.
- `api-gateway/internal/handler/tax_handler.go` — same for `/me/tax`.

**Regenerated:** `contract/stockpb/*.pb.go`.

---

## Task 1: Proto additions

- [ ] **Step 1: Edit proto to add `system_type`**

In `contract/proto/stock/stock.proto`, add `string system_type = N;` to each of these messages (pick an unused tag number; use consistent value like 10 to stand out from existing fields):

```protobuf
message ListMyOrdersRequest {
    uint64 user_id = 1;
    string status = 2;
    string direction = 3;
    string order_type = 4;
    int32 page = 5;
    int32 page_size = 6;
    string system_type = 10;   // "client" or "employee"
}

message GetOrderRequest {
    uint64 id = 1;
    uint64 user_id = 2;
    string system_type = 10;
}

message CancelOrderRequest {
    uint64 id = 1;
    uint64 user_id = 2;
    string system_type = 10;
}
```

Do the same for `ListHoldingsRequest`, `GetHoldingRequest` (in portfolio service section of the proto), and any `ListMyTax*Request` types.

- [ ] **Step 2: Regenerate Go bindings**

Run: `make proto`

Expected: `contract/stockpb/*.pb.go` regenerate; compilation errors (good — means Go code now has to set the field).

- [ ] **Step 3: Commit**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(contract): add system_type to user-scoped stock RPC requests

Part of the authorization fix. Employees and clients share the user_id
namespace; every /me/* query must filter by (user_id, system_type) to
prevent cross-system data leaks."
```

---

## Task 2: Repository filters

**Files:**
- Modify: `stock-service/internal/repository/order_repository.go`
- Modify: `stock-service/internal/repository/holding_repository.go`
- Modify: `stock-service/internal/repository/capital_gain_repository.go`
- Test: new `order_repository_systemtype_test.go`, `holding_repository_systemtype_test.go`

- [ ] **Step 1: Write failing repo test for orders**

Create `stock-service/internal/repository/order_repository_systemtype_test.go`:

```go
package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOrderRepo_ListByUser_FiltersOnSystemType(t *testing.T) {
	db := newTestDB(t)
	repo := NewOrderRepository(db)

	// Two orders with same user_id=5 but different system_types.
	clientOrder := &model.Order{
		UserID: 5, SystemType: "client",
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 1,
		PricePerUnit: decimal.NewFromInt(100), Status: "approved",
		RemainingPortions: 1, AccountID: 10,
	}
	employeeOrder := &model.Order{
		UserID: 5, SystemType: "employee",
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 2,
		PricePerUnit: decimal.NewFromInt(100), Status: "approved",
		RemainingPortions: 2, AccountID: 20,
	}
	if err := repo.Create(clientOrder); err != nil { t.Fatal(err) }
	if err := repo.Create(employeeOrder); err != nil { t.Fatal(err) }

	got, total, err := repo.ListByUser(5, "client", OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("expected 1 row for client, got %d", total)
	}
	if len(got) != 1 || got[0].ID != clientOrder.ID {
		t.Errorf("wrong order returned: %+v", got)
	}
}

func TestOrderRepo_GetByIDWithOwner_ChecksSystemType(t *testing.T) {
	db := newTestDB(t)
	repo := NewOrderRepository(db)

	employeeOrder := &model.Order{
		UserID: 5, SystemType: "employee",
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 2,
		PricePerUnit: decimal.NewFromInt(100), Status: "approved",
		RemainingPortions: 2, AccountID: 20,
	}
	_ = repo.Create(employeeOrder)

	// Client (same UserID) must NOT see the employee's order.
	_, err := repo.GetByIDWithOwner(employeeOrder.ID, 5, "client")
	if err == nil {
		t.Fatal("expected not-found error for cross-system access, got nil")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd stock-service && go test ./internal/repository/ -run "TestOrderRepo_.*SystemType" -v`

Expected: FAIL — methods don't exist with the new signatures yet.

- [ ] **Step 3: Update repo**

Edit `stock-service/internal/repository/order_repository.go`:

- Change `ListByUser(userID uint64, filter OrderFilter)` to `ListByUser(userID uint64, systemType string, filter OrderFilter)` and add `.Where("system_type = ?", systemType)` to the query.
- Add new method `GetByIDWithOwner(id, userID uint64, systemType string) (*model.Order, error)` that loads the row and returns `gorm.ErrRecordNotFound` if `order.UserID != userID || order.SystemType != systemType`. (Keep existing `GetByID` intact for internal engine use.)

Exact diff for `ListByUser`:

```go
func (r *OrderRepository) ListByUser(userID uint64, systemType string, filter OrderFilter) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	q := r.db.Model(&model.Order{}).
		Where("user_id = ? AND system_type = ?", userID, systemType)
	q = applyOrderFilters(q, filter)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = applyPagination(q, filter.Page, filter.PageSize)
	if err := q.Order("created_at DESC").Preload("Listing").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

// GetByIDWithOwner loads an order and verifies (user_id, system_type) ownership.
// Use for /me/* endpoints. Returns gorm.ErrRecordNotFound on mismatch so the
// caller can map to a 404 (never a 403 — we don't reveal whether the order
// exists for a different owner).
func (r *OrderRepository) GetByIDWithOwner(id, userID uint64, systemType string) (*model.Order, error) {
	var order model.Order
	err := r.db.Where("id = ? AND user_id = ? AND system_type = ?", id, userID, systemType).
		First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}
```

- [ ] **Step 4: Run repo tests**

Run: `cd stock-service && go test ./internal/repository/ -run "TestOrderRepo_.*SystemType" -v`

Expected: PASS.

- [ ] **Step 5: Do the same for holdings**

Write an analogous failing test in `holding_repository_systemtype_test.go` covering `ListByUser` (needs `systemType`) and `GetByUserAndSecurity` (needs `systemType`). Update the repo. Run tests.

- [ ] **Step 6: Same for capital_gain repository**

For `ListUsersWithGains` and any per-user query, add `system_type` as a filter parameter. This is important for correctness of tax collection (employee-tax and client-tax are bookkept separately against the same UserID).

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/repository/
git commit -m "fix(stock-service): filter by (user_id, system_type) in user-scoped repo queries

Employees and clients share the user_id integer namespace; without
system_type in the WHERE, /me/* endpoints can return rows belonging to a
different account type with a colliding ID. Adds system_type parameter to
ListByUser/GetByIDWithOwner/GetByUserAndSecurity across order, holding,
and capital_gain repositories."
```

---

## Task 3: Service-layer plumbing

**Files:**
- Modify: `stock-service/internal/service/order_service.go`
- Modify: `stock-service/internal/service/portfolio_service.go`

- [ ] **Step 1: Update `ListMyOrders`, `GetOrder`, `CancelOrder` signatures**

Each gains a `systemType` parameter and forwards it to the repo. Example:

```go
func (s *OrderService) ListMyOrders(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListByUser(userID, systemType, filter)
}

func (s *OrderService) GetOrder(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error) {
	order, err := s.orderRepo.GetByIDWithOwner(orderID, userID, systemType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("order not found")
		}
		return nil, nil, err
	}
	txns, err := s.txRepo.ListByOrderID(orderID)
	if err != nil {
		return nil, nil, err
	}
	return order, txns, nil
}

func (s *OrderService) CancelOrder(orderID, userID uint64, systemType string) (*model.Order, error) {
	order, err := s.orderRepo.GetByIDWithOwner(orderID, userID, systemType)
	// ... same pattern as GetOrder
}
```

- [ ] **Step 2: Portfolio service**

Update `ListHoldings`, `GetHolding`, and any ownership check.

- [ ] **Step 3: Run service tests**

Run: `cd stock-service && go test ./internal/service/...`

Expected: PASS; existing tests may need `systemType` arguments added — that's a mechanical fix.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/
git commit -m "fix(stock-service): thread system_type through user-scoped service methods"
```

---

## Task 4: gRPC handler wiring

**Files:**
- Modify: `stock-service/internal/handler/order_handler.go`
- Modify: `stock-service/internal/handler/portfolio_handler.go`

- [ ] **Step 1: Pass `req.SystemType` into each service call**

```go
func (h *OrderHandler) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.OrderDetail, error) {
	order, txns, err := h.orderSvc.GetOrder(req.Id, req.UserId, req.SystemType)
	// ...
}
func (h *OrderHandler) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.CancelOrder(req.Id, req.UserId, req.SystemType)
	// ...
}
func (h *OrderHandler) ListMyOrders(ctx context.Context, req *pb.ListMyOrdersRequest) (*pb.ListOrdersResponse, error) {
	orders, total, err := h.orderSvc.ListMyOrders(req.UserId, req.SystemType, /* filter */)
	// ...
}
```

- [ ] **Step 2: Same for portfolio handler**

- [ ] **Step 3: Validate incoming SystemType is non-empty**

At the top of each handler that uses it, return `InvalidArgument` if `req.SystemType == ""` so a malformed or forged client can't silently bypass the filter:

```go
if req.SystemType == "" {
	return nil, status.Error(codes.InvalidArgument, "system_type required")
}
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/handler/
git commit -m "fix(stock-service): forward system_type from RPC request to service layer"
```

---

## Task 5: API gateway — pass system_type from JWT context

**Files:**
- Modify: `api-gateway/internal/handler/stock_order_handler.go`
- Modify: `api-gateway/internal/handler/portfolio_handler.go`
- Modify: `api-gateway/internal/handler/tax_handler.go` (if `/me/tax` uses an RPC with SystemType now)

- [ ] **Step 1: Read `system_type` from context + pass into RPC**

Every `/me/*` gateway handler that calls a stock-service RPC gains:

```go
userID := c.GetInt64("user_id")
systemType, _ := c.Get("system_type")
st, _ := systemType.(string)
if st == "" {
	apiError(c, 401, "unauthorized", "missing system_type")
	return
}

resp, err := h.client.GetOrder(c.Request.Context(), &stockpb.GetOrderRequest{
	Id: id, UserId: uint64(userID), SystemType: st,
})
```

Apply to `GetMyOrder`, `CancelOrder`, `ListMyOrders`, `ListHoldings`, `GetHolding`, `ListMyTaxRecords`.

- [ ] **Step 2: Build + run gateway tests**

Run: `cd api-gateway && go build ./... && go test ./...`

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/handler/
git commit -m "fix(api-gateway): forward system_type from JWT context to stock RPCs

Closes the cross-system-type data-leak where a client with user_id=N
could see/cancel/mutate rows belonging to an employee with the same
user_id. Gateway already has system_type in context; this just threads
it through every /me/* RPC call."
```

---

## Task 6: Integration test — regression coverage

**Files:**
- Create: `test-app/workflows/wf_systemtype_isolation_test.go`

- [ ] **Step 1: Write the test**

```go
package workflows

import "testing"

func TestWF_OrderAccessIsolatedBySystemType(t *testing.T) {
	// This test exercises the cross-system-type data-leak fix.
	// Setup:
	//   1. Seed an employee with numeric id == 1.
	//   2. Seed a client also with numeric id == 1 (separate DB, separate table).
	//   3. Employee places an order → O_emp (visible only to employee).
	//   4. Client logs in and lists /me/orders → must NOT see O_emp.
	//   5. Client GET /me/orders/{O_emp.ID} → must 404 (not 403 — we don't leak existence).
	//   6. Client POST /me/orders/{O_emp.ID}/cancel → must 404.

	env := setupEnv(t) // existing fixture
	defer env.Teardown()

	empToken := env.LoginEmployee(t, 1)
	cliToken := env.LoginClient(t, 1)

	empOrder := env.CreateOrderAsEmployee(t, empToken, /* payload with stock listing */)
	if empOrder.UserID != 1 || empOrder.SystemType != "employee" {
		t.Fatalf("employee order bad shape: %+v", empOrder)
	}

	list := env.GetMyOrders(t, cliToken)
	for _, o := range list.Orders {
		if o.ID == empOrder.ID {
			t.Fatalf("DATA LEAK: client saw employee order %d", empOrder.ID)
		}
	}

	status := env.GetMyOrderByIDStatus(t, cliToken, empOrder.ID)
	if status != 404 {
		t.Fatalf("expected 404 for cross-system GET, got %d", status)
	}

	status = env.CancelMyOrderStatus(t, cliToken, empOrder.ID)
	if status != 404 {
		t.Fatalf("expected 404 for cross-system cancel, got %d", status)
	}
}
```

Adapt helper method names to existing workflow fixture conventions.

- [ ] **Step 2: Run**

Run: `cd test-app && go test ./workflows/ -run TestWF_OrderAccessIsolatedBySystemType -v`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/wf_systemtype_isolation_test.go
git commit -m "test(workflows): regression test for cross-system-type order isolation"
```

---

## Task 7: Documentation

- [ ] **Step 1: Update `docs/Bugs.txt`**

Append a fifth entry:

```
5. Client can see/cancel employee's orders (and vice versa) — same user_id collision
------------------------------------------------------------------------------------
Severity: HIGH — data privacy / authorization bug.

Root cause:
  Employees live in user_db.employees; clients live in client_db.clients.
  Both tables have their own auto-increment PKs, so employee.id=5 and
  client.id=5 routinely coexist. stock-service's Order/Holding/CapitalGain
  rows all carry `user_id` AND `system_type` columns, but /me/* queries
  in order_repository.ListByUser, GetByID ownership check (order_service:278),
  CancelOrder ownership check (order_service:244), holding_repository.ListByUser,
  and holding_repository.GetByUserAndSecurity filter by user_id alone.
  Result: a client with user_id=5 calling GET /api/v1/me/orders sees every
  order in the system placed by any employee with user_id=5.

Fix:
  - Add system_type string field to every user-scoped RPC request in
    contract/proto/stock/stock.proto.
  - Gateway reads system_type from JWT context (auth middleware already
    populates it) and forwards into each RPC.
  - Service-layer methods accept systemType and forward to repo.
  - Repositories add AND system_type = ? to every user-scoped WHERE.
  - Cross-system GETs return 404, not 403 (don't leak row existence).

Status: FIXED 2026-04-23.
```

- [ ] **Step 2: Commit**

```bash
git add docs/Bugs.txt
git commit -m "docs(bugs): record cross-system-type authorization bug + fix"
```

---

## Non-goals

- Renaming `ListAll` on the order repo to be clearer about its "admin only" intent (the agent's defense-in-depth suggestion). The `/api/v1/orders` admin endpoint is gated by `RequirePermission("orders.approve")` in the gateway — that IS the enforcement boundary. Adding a fake guard inside the repo would be security theater.
- Changing any other aspect of the portfolio or tax code. Both verified to work as documented.
- Merging these fixes into the Phase 2 saga plan. System_type filtering is orthogonal to reservations/saga/forex — deliberately kept as its own small plan so it can ship independently.
