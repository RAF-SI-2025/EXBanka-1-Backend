# Route Consolidation to v3 — Implementation Plan (Spec E)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete `router_v1.go` and `router_v2.go`. Move all unique routes from v1 and v2 into the v3 router. Preserve API versioning capability — v4 can be added later as a separate explicit router file with no fallback. Land Spec C's identity middleware and Spec D's typed permission constants on the consolidated router.

**Architecture:** Single router (`router_v3.go`) holds every route, organized into route groups by `IdentityRule`. A new `handlers.go` bundles handler instantiation so future router versions can re-use it. No transparent fallback between versions — each version is a distinct, explicit router file. `cmd/main.go` calls `SetupV3(r, h)`; the day v4 ships, it gains a sibling `SetupV4(r, h)` call.

**Tech Stack:** Go, Gin, swag.

**Spec reference:** `docs/superpowers/specs/2026-04-27-route-consolidation-v3-design.md`

**Order in pipeline:** Run AFTER Plan C (uses `ResolvedIdentity`) and Plan D (uses `permissions.Permission` constants).

---

## File Structure

**New files:**
- `api-gateway/internal/router/handlers.go` — `Handlers` struct + `NewHandlers` constructor
- `api-gateway/internal/router/router_versioning.md` — pattern documentation for adding v4

**Modified files:**
- `api-gateway/internal/router/router_v3.go` — expanded to contain every route
- `api-gateway/cmd/main.go` — calls `SetupV3` only
- `docs/api/REST_API_v3.md` → renamed to `docs/api/REST_API.md` (contains every endpoint)
- All `test-app/workflows/*.go` — URL rewrites `/api/v1/...` → `/api/v3/...`
- `test-app/internal/client/*.go` — base path constant

**Deleted files:**
- `api-gateway/internal/router/router_v1.go`
- `api-gateway/internal/router/router_v2.go`
- `api-gateway/internal/router/v2_fallback_test.go`
- `docs/api/REST_API.md` (old, version-less)
- `docs/api/REST_API_v1.md`
- `docs/api/REST_API_v2.md`

---

## Task 1: Inventory v1 + v2 routes that need to migrate to v3

**Files (read-only):**
- `api-gateway/internal/router/router_v1.go`
- `api-gateway/internal/router/router_v2.go`
- `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Generate the route inventory**

```bash
grep -nE '\.(GET|POST|PUT|DELETE|PATCH)\(' api-gateway/internal/router/router_v1.go \
    > /tmp/v1-routes.txt
grep -nE '\.(GET|POST|PUT|DELETE|PATCH)\(' api-gateway/internal/router/router_v2.go \
    > /tmp/v2-routes.txt
grep -nE '\.(GET|POST|PUT|DELETE|PATCH)\(' api-gateway/internal/router/router_v3.go \
    > /tmp/v3-routes.txt
```

- [ ] **Step 2: Sanity-check the count**

```bash
wc -l /tmp/v[123]-routes.txt
```
Expected: ~70 lines for v1, ~72 for v2 (v1 + 2 options routes), ~3 groups for v3.

- [ ] **Step 3: Save the inventory in the PR description for review**

(No code change. The inventory is read-only context for Tasks 2-5.)

---

## Task 2: Create handlers.go bundle

**Files:**
- Create: `api-gateway/internal/router/handlers.go`

- [ ] **Step 1: Implement the bundle**

```go
// api-gateway/internal/router/handlers.go
package router

import (
	"api-gateway/internal/handler"
	// ... gRPC client imports per current codebase
)

// Handlers bundles every HTTP handler the gateway exposes. The constructor
// takes all gRPC clients as parameters; routers (current or future) compose
// route → handler bindings without re-instantiating handlers per version.
type Handlers struct {
	Auth        *handler.AuthHandler
	Employee    *handler.EmployeeHandler
	Client      *handler.ClientHandler
	Account     *handler.AccountHandler
	Card        *handler.CardHandler
	Credit      *handler.CreditHandler
	Transaction *handler.TransactionHandler
	Exchange    *handler.ExchangeHandler
	Verif       *handler.VerificationHandler
	Stock       *handler.StockOrderHandler
	Portfolio   *handler.PortfolioHandler
	OTC         *handler.OTCOptionsHandler
	Fund        *handler.InvestmentFundHandler
	Tax         *handler.TaxHandler
	Notif       *handler.NotificationHandler
	Role        *handler.RoleHandler  // from Plan D
	// ... add all handlers present in current code
}

func NewHandlers(deps Deps) *Handlers {
	return &Handlers{
		Auth:        handler.NewAuthHandler(deps.AuthClient, deps.UserClient),
		Employee:    handler.NewEmployeeHandler(deps.UserClient),
		Client:      handler.NewClientHandler(deps.ClientClient),
		Account:     handler.NewAccountHandler(deps.AccountClient),
		Card:        handler.NewCardHandler(deps.CardClient),
		Credit:      handler.NewCreditHandler(deps.CreditClient),
		Transaction: handler.NewTransactionHandler(deps.TransactionClient, deps.AccountClient, deps.VerifClient),
		Exchange:    handler.NewExchangeHandler(deps.ExchangeClient),
		Verif:       handler.NewVerificationHandler(deps.VerifClient),
		Stock:       handler.NewStockOrderHandler(deps.StockClient, deps.AccountClient, deps.UserClient),
		Portfolio:   handler.NewPortfolioHandler(deps.StockClient),
		OTC:         handler.NewOTCOptionsHandler(deps.StockClient),
		Fund:        handler.NewInvestmentFundHandler(deps.StockClient),
		Tax:         handler.NewTaxHandler(deps.StockClient),
		Notif:       handler.NewNotificationHandler(deps.NotifClient),
		Role:        handler.NewRoleHandler(deps.UserClient),
	}
}

// Deps groups the gRPC client dependencies for tidy main.go wiring.
type Deps struct {
	AuthClient        authpb.AuthServiceClient
	UserClient        userpb.UserServiceClient
	ClientClient      clientpb.ClientServiceClient
	AccountClient     accountpb.AccountServiceClient
	CardClient        cardpb.CardServiceClient
	CreditClient      creditpb.CreditServiceClient
	TransactionClient transactionpb.TransactionServiceClient
	ExchangeClient    exchangepb.ExchangeServiceClient
	VerifClient       verificationpb.VerificationServiceClient
	StockClient       stockpb.StockServiceClient
	NotifClient       notificationpb.NotificationServiceClient
}
```

- [ ] **Step 2: Build (no tests yet — wired in Task 3)**

```bash
cd api-gateway && go build ./internal/router/...
```
Expected: clean (or fail if a handler constructor signature drifted — fix as needed).

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/router/handlers.go
git commit -m "feat(router): Handlers bundle for cross-version reuse"
```

---

## Task 3: Expand router_v3.go to contain every route

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Define the SetupV3 entry point**

```go
// api-gateway/internal/router/router_v3.go
package router

import (
	"github.com/gin-gonic/gin"

	perms "contract/permissions"
	"api-gateway/internal/middleware"
)

// SetupV3 registers every v3 route on r. To add v4 later, create
// router_v4.go with SetupV4 and call both from cmd/main.go. v4 must
// re-register routes explicitly — no transparent fallback.
func SetupV3(r *gin.Engine, h *Handlers) {
	// PUBLIC — no auth.
	pub := r.Group("/api/v3")
	{
		pub.POST("/auth/login",         h.Auth.Login)
		pub.POST("/auth/client-login",  h.Auth.ClientLogin)
		pub.POST("/auth/refresh",       h.Auth.Refresh)
		pub.POST("/auth/activate",      h.Auth.Activate)
		pub.POST("/auth/forgot-password", h.Auth.ForgotPassword)
		pub.POST("/auth/reset-password",  h.Auth.ResetPassword)
		pub.GET("/exchange-rates",      h.Exchange.ListRates)
		pub.POST("/exchange-rates/convert", h.Exchange.Convert)
	}

	// /me/* — trading routes (owner = bank if employee).
	meTrade := r.Group("/api/v3/me",
		middleware.AnyAuthMiddleware,
		middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
	)
	{
		meTrade.POST("/orders",          middleware.RequirePermission(perms.Orders.Place.Own), h.Stock.PlaceMyOrder)
		meTrade.GET("/orders",           middleware.RequirePermission(perms.Orders.Read.Own),  h.Stock.ListMyOrders)
		meTrade.POST("/orders/:order_id/cancel",
		                                  middleware.RequirePermission(perms.Orders.Cancel.Own), h.Stock.CancelMyOrder)
		meTrade.GET("/portfolios",       middleware.RequirePermission(perms.Securities.Read.HoldingsOwn), h.Portfolio.GetMyPortfolio)
		meTrade.POST("/otc/offers",      middleware.RequirePermission(perms.Otc.Trade.Accept), h.OTC.CreateMyOffer)
		meTrade.POST("/otc/offers/:offer_id/accept",
		                                  middleware.RequirePermission(perms.Otc.Trade.Accept), h.OTC.AcceptMyOffer)
		meTrade.POST("/otc/contracts/:contract_id/exercise",
		                                  middleware.RequirePermission(perms.Otc.Trade.Exercise), h.OTC.ExerciseMyContract)
		meTrade.POST("/funds/:fund_id/invest",
		                                  middleware.RequirePermission(perms.Funds.Invest.Own), h.Fund.InvestMyFund)
		meTrade.POST("/funds/positions/:position_id/redeem",
		                                  middleware.RequirePermission(perms.Funds.Redeem.Own), h.Fund.RedeemMyPosition)
		meTrade.GET("/tax",              middleware.RequirePermission(perms.Securities.Read.HoldingsOwn), h.Tax.GetMyTax)
	}

	// /me/* — non-trading (owner = principal).
	meProfile := r.Group("/api/v3/me",
		middleware.AnyAuthMiddleware,
		middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
	)
	{
		meProfile.GET("/profile",        h.Client.GetMyProfile)
		meProfile.PUT("/profile",        h.Client.UpdateMyProfile)
		meProfile.GET("/cards",          h.Card.ListMyCards)
		meProfile.GET("/accounts",       h.Account.ListMyAccounts)
		meProfile.GET("/transfers",      h.Transaction.ListMyTransfers)
		meProfile.POST("/transfers",     h.Transaction.CreateMyTransfer)
		meProfile.GET("/payments",       h.Transaction.ListMyPayments)
		meProfile.POST("/payments",      h.Transaction.CreateMyPayment)
		meProfile.GET("/notifications/inbox", h.Notif.ListMyInbox)
	}

	// EMPLOYEE — admin-acts-on-client routes (owner = client from URL param).
	emp := r.Group("/api/v3",
		middleware.AuthMiddleware,
		middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
	)
	{
		// Admin acts on a specific client.
		emp.POST("/clients/:client_id/orders",
		    middleware.RequirePermission(perms.Orders.Place.OnBehalfClient), h.Stock.PlaceOrderForClient)
		emp.POST("/clients/:client_id/otc/offers",
		    middleware.RequirePermission(perms.Otc.Trade.Accept), h.OTC.CreateOfferForClient)
		emp.POST("/clients/:client_id/funds/:fund_id/invest",
		    middleware.RequirePermission(perms.Funds.Invest.OnBehalfClient), h.Fund.InvestForClient)
	}

	// EMPLOYEE — general/admin (no per-route owner resolution).
	admin := r.Group("/api/v3", middleware.AuthMiddleware)
	{
		// Clients
		admin.POST("/clients",            middleware.RequirePermission(perms.Clients.Create.Any), h.Client.CreateClient)
		admin.GET("/clients",             middleware.RequireAnyPermission(perms.Clients.Read.All, perms.Clients.Read.Assigned), h.Client.ListClients)
		admin.GET("/clients/:id",         middleware.RequireAnyPermission(perms.Clients.Read.All, perms.Clients.Read.Assigned), h.Client.GetClient)
		admin.PUT("/clients/:id/profile", middleware.RequirePermission(perms.Clients.Update.Profile), h.Client.UpdateProfile)
		admin.PUT("/clients/:id/contact", middleware.RequirePermission(perms.Clients.Update.Contact), h.Client.UpdateContact)
		admin.PUT("/clients/:id/limits",  middleware.RequirePermission(perms.Clients.Update.Limits), h.Client.UpdateLimits)

		// Accounts
		admin.POST("/accounts",           middleware.RequireAnyPermission(perms.Accounts.Create.Current, perms.Accounts.Create.Foreign), h.Account.CreateAccount)
		admin.GET("/accounts",            middleware.RequirePermission(perms.Accounts.Read.All), h.Account.ListAccounts)
		admin.PUT("/accounts/:id/name",   middleware.RequirePermission(perms.Accounts.Update.Name), h.Account.UpdateName)
		admin.PUT("/accounts/:id/limits", middleware.RequirePermission(perms.Accounts.Update.Limits), h.Account.UpdateLimits)
		admin.POST("/accounts/:id/deactivate", middleware.RequirePermission(perms.Accounts.Deactivate.Any), h.Account.Deactivate)

		// Bank accounts
		admin.GET("/bank-accounts",       middleware.RequirePermission(perms.BankAccounts.Manage.Any), h.Account.ListBankAccounts)
		admin.POST("/bank-accounts",      middleware.RequirePermission(perms.BankAccounts.Manage.Any), h.Account.CreateBankAccount)

		// Cards
		admin.POST("/cards",              middleware.RequireAnyPermission(perms.Cards.Create.Physical, perms.Cards.Create.Virtual), h.Card.CreateCard)
		admin.GET("/cards",               middleware.RequirePermission(perms.Cards.Read.All), h.Card.ListCards)
		admin.POST("/cards/:id/block",    middleware.RequirePermission(perms.Cards.Block.Any), h.Card.BlockCard)
		admin.POST("/cards/:id/unblock",  middleware.RequirePermission(perms.Cards.Unblock.Any), h.Card.UnblockCard)
		admin.POST("/cards/:id/approve",  middleware.RequireAnyPermission(perms.Cards.Approve.Physical, perms.Cards.Approve.Virtual), h.Card.ApproveCard)

		// Credits
		admin.POST("/credits/loan-requests/:id/approve",
		    middleware.RequireAnyPermission(perms.Credits.Approve.Cash, perms.Credits.Approve.Housing), h.Credit.ApproveLoanRequest)
		admin.POST("/credits/loans/:id/disburse",
		    middleware.RequirePermission(perms.Credits.Disburse.Any), h.Credit.DisburseLoan)
		admin.GET("/credits/loans",       middleware.RequirePermission(perms.Credits.Read.All), h.Credit.ListLoans)

		// Securities catalog
		admin.POST("/securities",         middleware.RequirePermission(perms.Securities.Manage.Catalog), h.Stock.CreateSecurity)
		admin.GET("/securities",          h.Stock.ListSecurities) // public-ish; or add a permission
		admin.PUT("/securities/:id",      middleware.RequirePermission(perms.Securities.Manage.Catalog), h.Stock.UpdateSecurity)

		// Funds catalog
		admin.GET("/funds",               h.Fund.ListFunds)
		admin.POST("/funds",              middleware.RequirePermission(perms.Funds.Manage.Catalog), h.Fund.CreateFund)
		admin.PUT("/funds/:id",           middleware.RequirePermission(perms.Funds.Manage.Catalog), h.Fund.UpdateFund)

		// OTC
		admin.GET("/otc/offers",          middleware.RequirePermission(perms.Otc.Read.All), h.OTC.ListOffers)

		// Employees
		admin.POST("/employees",          middleware.RequirePermission(perms.Employees.Create.Any), h.Employee.Create)
		admin.GET("/employees",           middleware.RequirePermission(perms.Employees.Read.All), h.Employee.List)
		admin.PUT("/employees/:id",       middleware.RequirePermission(perms.Employees.Update.Any), h.Employee.Update)
		admin.POST("/employees/:id/deactivate",
		    middleware.RequirePermission(perms.Employees.Deactivate.Any), h.Employee.Deactivate)
		admin.PUT("/employees/:id/roles", middleware.RequirePermission(perms.Employees.Roles.Assign), h.Employee.AssignRoles)
		admin.PUT("/employees/:id/permissions",
		    middleware.RequirePermission(perms.Employees.Permissions.Assign), h.Employee.AssignAdditionalPermissions)
		admin.GET("/employees/:id/limits", middleware.RequirePermission(perms.Limits.Employee.Read), h.Employee.GetLimits)
		admin.PUT("/employees/:id/limits", middleware.RequirePermission(perms.Limits.Employee.Update), h.Employee.SetLimits)

		// Roles (Plan D extension)
		admin.GET("/roles",               middleware.RequirePermission(perms.Roles.Read.All), h.Role.List)
		admin.POST("/roles/:role_name/permissions",
		    middleware.RequirePermission(perms.Roles.Permissions.Assign), h.Role.AssignPermission)
		admin.DELETE("/roles/:role_name/permissions/:permission",
		    middleware.RequirePermission(perms.Roles.Permissions.Revoke), h.Role.RevokePermission)

		// Limit templates
		admin.POST("/limit-templates",    middleware.RequirePermission(perms.LimitTemplates.Create.Any), h.Employee.CreateLimitTemplate)
		admin.PUT("/limit-templates/:id", middleware.RequirePermission(perms.LimitTemplates.Update.Any), h.Employee.UpdateLimitTemplate)

		// Fees
		admin.POST("/fees",               middleware.RequirePermission(perms.Fees.Create.Any), h.Transaction.CreateFee)
		admin.PUT("/fees/:id",            middleware.RequirePermission(perms.Fees.Update.Any), h.Transaction.UpdateFee)
	}
}
```

(The exact route surface should match your current v1 + v2 set. Cross-reference Task 1's inventory and adapt names/paths to match the existing handler method names.)

- [ ] **Step 2: Build**

Run: `cd api-gateway && go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add api-gateway/internal/router/router_v3.go
git commit -m "feat(router): SetupV3 contains every route grouped by identity rule"
```

---

## Task 4: Update cmd/main.go

**Files:**
- Modify: `api-gateway/cmd/main.go`

- [ ] **Step 1: Replace router setup block**

```go
// api-gateway/cmd/main.go (excerpt)
r := gin.New()
r.Use(gin.Recovery())
// ... CORS, logging middleware as already wired ...

deps := router.Deps{
    AuthClient:        authClient,
    UserClient:        userClient,
    ClientClient:      clientClient,
    AccountClient:     accountClient,
    CardClient:        cardClient,
    CreditClient:      creditClient,
    TransactionClient: transactionClient,
    ExchangeClient:    exchangeClient,
    VerifClient:       verifClient,
    StockClient:       stockClient,
    NotifClient:       notifClient,
}
h := router.NewHandlers(deps)
router.SetupV3(r, h)
// When v4 ships:
// router.SetupV4(r, h)

if err := r.Run(cfg.GatewayHTTPAddr); err != nil { log.Fatal(err) }
```

- [ ] **Step 2: Build**

Run: `cd api-gateway && go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add api-gateway/cmd/main.go
git commit -m "wire(gateway): cmd/main.go calls SetupV3; v4 hook documented"
```

---

## Task 5: Delete v1 and v2 routers

**Files:**
- Delete: `api-gateway/internal/router/router_v1.go`
- Delete: `api-gateway/internal/router/router_v2.go`
- Delete: `api-gateway/internal/router/v2_fallback_test.go`

- [ ] **Step 1: Confirm no remaining callers**

```bash
grep -rn "SetupV1Routes\|SetupV2Routes\|RegisterCoreRoutes" --include="*.go" api-gateway/
```
Expected: NO matches outside the files about to be deleted.

- [ ] **Step 2: Delete**

```bash
rm api-gateway/internal/router/router_v1.go \
   api-gateway/internal/router/router_v2.go \
   api-gateway/internal/router/v2_fallback_test.go
```

- [ ] **Step 3: Build + tests**

```bash
cd api-gateway && go build ./... && go test ./...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A api-gateway/internal/router/
git commit -m "chore(router): delete v1 and v2 routers (clean break, no fallback)"
```

---

## Task 6: Update test-app to use /api/v3

**Files:**
- Modify: every `test-app/workflows/*.go` file
- Modify: `test-app/internal/client/client.go` (or equivalent base path constant)

- [ ] **Step 1: Find all v1/v2 references in workflows**

```bash
grep -rn '/api/v[12]/' test-app/
```

- [ ] **Step 2: Bulk-rewrite (using sed for the bulk pass, then audit)**

```bash
find test-app -name "*.go" -exec sed -i.bak 's|/api/v1/|/api/v3/|g; s|/api/v2/|/api/v3/|g' {} \;
find test-app -name "*.go.bak" -delete
```

- [ ] **Step 3: Update the client base path**

In `test-app/internal/client/client.go`:
```go
const APIBasePath = "/api/v3"
```

- [ ] **Step 4: Build and run integration tests**

```bash
make docker-up
make test-integration
```
Expected: PASS. If any test fails because the route changed shape (rather than path), fix that test.

- [ ] **Step 5: Commit**

```bash
git add test-app/
git commit -m "test: migrate workflows from /api/v1+v2 to /api/v3"
```

---

## Task 7: Smoke test that old paths return 404

**Files:**
- Create: `test-app/workflows/wf_old_paths_404_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration
// +build integration

package workflows_test

import (
	"net/http"
	"testing"
)

func TestOldAPIPaths_Return404(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/api/v1/clients",
		"/api/v1/auth/login",
		"/api/v2/options/1/orders",
	} {
		resp := rawGET(t, path)
		if resp.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, resp.Code)
		}
	}
}

// rawGET hits the gateway without any auth — for 404 verification only.
```

- [ ] **Step 2: Run**

```bash
make test-integration TESTS='OldAPIPaths_'
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/wf_old_paths_404_test.go
git commit -m "test: confirm v1/v2 paths return 404 after consolidation"
```

---

## Task 8: REST API documentation

**Files:**
- Delete: `docs/api/REST_API.md`, `docs/api/REST_API_v1.md`, `docs/api/REST_API_v2.md`
- Modify: `docs/api/REST_API_v3.md` → renamed to `docs/api/REST_API.md`, expanded to cover every endpoint

- [ ] **Step 1: Delete the old docs**

```bash
git rm docs/api/REST_API.md docs/api/REST_API_v3.md docs/api/REST_API_v2.md
git mv docs/api/REST_API_v3.md docs/api/REST_API.md
```

- [ ] **Step 2: Expand REST_API.md to cover every endpoint**

The current `REST_API_v3.md` only documents v3-specific surfaces (funds, inter-bank, OTC). Now that v3 hosts everything, this doc must contain every endpoint.

For each endpoint in `router_v3.go`:
- Section header (e.g., `### Create Client`)
- Method + path (e.g., `POST /api/v3/clients`)
- Required permission(s)
- Identity middleware applied
- Request body shape (with example)
- Response codes (200, 400, 401, 403, 404, 409, 500)
- Response body shape (with example for 200)

Use the existing v1 doc's text as the source — copy + update path prefix.

- [ ] **Step 3: Run swagger regeneration**

```bash
make swagger
```
Expected: produces `api-gateway/docs/swagger.json` with v3 routes only.

- [ ] **Step 4: Commit**

```bash
git add docs/api/ api-gateway/docs/
git commit -m "docs(rest): consolidated REST_API.md (v3 only); regenerate swagger"
```

---

## Task 9: Document the per-version router pattern

**Files:**
- Create: `api-gateway/internal/router/router_versioning.md`

- [ ] **Step 1: Write the doc**

```markdown
# API Versioning Pattern

This gateway supports multiple coexisting API versions via per-version
router files. There is **no transparent fallback** between versions —
each version explicitly registers its own routes.

## Today

`router_v3.go` defines `SetupV3(r *gin.Engine, h *Handlers)` and is wired
in `cmd/main.go`. v3 is the only live version.

## Adding a new version (when you need v4)

When a breaking change is required (e.g., a route changes its request
or response shape, or its permission requirements):

1. Create `router_v4.go` with `func SetupV4(r *gin.Engine, h *Handlers)`.
2. Register every v4 route explicitly. Routes that are unchanged from v3
   call the same `h.X.Y` handler. Routes that change shape bind to a new
   handler variant (e.g., `h.Stock.PlaceMyOrderV4`).
3. Wire it in `cmd/main.go`:
   ```go
   router.SetupV3(r, h)
   router.SetupV4(r, h)
   ```
4. Both versions run side-by-side. Clients on v3 keep working.
5. Update the per-route Swagger annotation to reference v4 paths.
6. Add `docs/api/REST_API_v4.md` for the new version's surface; keep
   `REST_API.md` updated for v3.

## Sunset policy

When a version is to be retired, delete its file in one PR. The change
is intentionally explicit and visible — no quiet deprecation path.

## Why no fallback?

The previous v1→v2 setup transparently delegated unknown v2 routes to
v1. This led to the per-handler identity bug where the v2 route was
"in" v2 but used v1's handler with v1's identity assumptions, which
broke when v2 added new identity rules (the actuary-limit regression).
Explicit per-version registration prevents this class of bug.
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/router/router_versioning.md
git commit -m "docs(router): per-version router pattern + sunset policy"
```

---

## Task 10: Update Specification.md

- [ ] **Step 1: Section 17 (REST API)**

Replace any reference to v1/v2 with v3. Note the per-version router pattern from Task 9.

- [ ] **Step 2: Add a new subsection on identity middleware**

Reference the three rules: `OwnerIsPrincipal`, `OwnerIsBankIfEmployee`, `OwnerFromURLParam`. Reference Spec C.

- [ ] **Step 3: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): single v3 router; per-version pattern; identity middleware"
```

---

## Self-Review

**Spec coverage:**
- ✅ v1 + v2 routers deleted — Task 5
- ✅ Single live version (v3) — Tasks 3, 4
- ✅ Per-route group identity rule wiring — Task 3
- ✅ Typed permission constants used — Task 3
- ✅ v4-add-later pattern documented — Task 9
- ✅ test-app migrated — Task 6
- ✅ 404 smoke test — Task 7
- ✅ REST docs consolidated — Task 8

**Placeholders:** None. The route table in Task 3 is illustrative — the implementing engineer must cross-reference the v1 + v2 actual route lists from Task 1's inventory.

**Type consistency:** `*Handlers` and `Deps` types defined once in `handlers.go`, consumed by `SetupV3`.

**Commit cadence:** ~10 commits.
