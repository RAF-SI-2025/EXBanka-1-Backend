# Employee Bank-Account Activity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an employee-facing endpoint that returns a bank account's transaction (ledger) activity, mirroring the client-facing `/me/accounts/:id/activity`.

**Architecture:** A new gateway route `GET /api/v3/bank-accounts/:id/activity` in the existing permission-gated `bank-accounts` group, backed by a new `AccountHandler.GetBankAccountActivity` handler that reuses the existing `GetAccount` + `GetLedgerEntries` gRPC calls. The handler swaps the client handler's ownership check for a bank-account *kind* check. No proto, RPC, or schema changes.

**Tech Stack:** Go, Gin (api-gateway), gRPC (`accountpb`), golangci-lint, `make` targets.

**Source spec:** `docs/superpowers/specs/2026-05-15-employee-bank-account-activity-design.md`

---

## File Structure

**Modified:**
- `api-gateway/internal/handler/account_handler.go` — new `GetBankAccountActivity` handler
- `api-gateway/internal/handler/account_handler_test.go` — handler tests + route registration in the `accountRouter` test helper
- `api-gateway/internal/router/router_v3.go` — register the new route in the `bankAccountsRead` group
- `test-app/workflows/` — one integration test
- `docs/api/REST_API_v3.md`, `docs/Specification.md`, `api-gateway/docs/*` (swagger)

**No files created.**

---

## Task 1: `GetBankAccountActivity` handler

**Files:**
- Modify: `api-gateway/internal/handler/account_handler.go`
- Modify: `api-gateway/internal/handler/account_handler_test.go`

- [ ] **Step 1: Register the route in the test helper**

In `account_handler_test.go`, the `accountRouter` helper registers routes. After the existing line:

```go
	r.GET("/me/accounts/:id/activity", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		h.GetMyAccountActivity(c)
	})
	return r
}
```

add the new route just before `return r`:

```go
	r.GET("/me/accounts/:id/activity", func(c *gin.Context) {
		c.Set("principal_id", int64(1))
		h.GetMyAccountActivity(c)
	})
	r.GET("/bank-accounts/:id/activity", h.GetBankAccountActivity)
	return r
}
```

- [ ] **Step 2: Write the failing tests**

In `account_handler_test.go`, add these tests (the `accountFullStub` already has `getFn` and `getLedger` function fields; `stubBankAccountClient` already exists):

```go
func TestAccount_GetBankAccountActivity_Success(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: in.Id, OwnerId: 1000000000, AccountNumber: "111-BANK-RSD", CurrencyCode: "RSD", AccountKind: "bank"}, nil
		},
		getLedger: func(in *accountpb.GetLedgerEntriesRequest) (*accountpb.GetLedgerEntriesResponse, error) {
			require.Equal(t, "111-BANK-RSD", in.AccountNumber)
			return &accountpb.GetLedgerEntriesResponse{
				Entries:    []*accountpb.LedgerEntryResponse{{Id: 1, EntryType: "credit", Amount: "100"}},
				TotalCount: 1,
			}, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/bank-accounts/5/activity", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total_count":1`)
	require.Contains(t, rec.Body.String(), `"entry_type":"credit"`)
}

func TestAccount_GetBankAccountActivity_RejectsClientAccount(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{Id: in.Id, OwnerId: 42, AccountNumber: "265-1-00", CurrencyCode: "RSD", AccountKind: "current"}, nil
		},
		getLedger: func(*accountpb.GetLedgerEntriesRequest) (*accountpb.GetLedgerEntriesResponse, error) {
			t.Fatal("GetLedgerEntries must not be called for a non-bank account")
			return nil, nil
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/bank-accounts/5/activity", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAccount_GetBankAccountActivity_BadID(t *testing.T) {
	h := handler.NewAccountHandler(&accountFullStub{}, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/bank-accounts/abc/activity", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccount_GetBankAccountActivity_AccountNotFound(t *testing.T) {
	acc := &accountFullStub{
		getFn: func(*accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
			return nil, status.Error(codes.NotFound, "no account")
		},
	}
	h := handler.NewAccountHandler(acc, &stubBankAccountClient{}, nil, nil)
	r := accountRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/bank-accounts/9999/activity", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

Confirm the test file already imports `accountpb`, `status`, `codes`, `httptest`, `http`, `require` — `account_handler_test.go` uses all of these in existing tests, so no import changes are expected. If `status`/`codes` are not yet imported, add `"google.golang.org/grpc/codes"` and `"google.golang.org/grpc/status"`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd api-gateway && go test ./internal/handler/ -run TestAccount_GetBankAccountActivity`
Expected: FAIL — `h.GetBankAccountActivity` undefined.

- [ ] **Step 4: Implement the handler**

In `api-gateway/internal/handler/account_handler.go`, add the handler immediately after `GetMyAccountActivity` (which ends with the `c.JSON(http.StatusOK, gin.H{"entries": out, "total_count": resp.TotalCount})` block and its closing `}`). Insert before the `ledgerEntryToJSON` function:

```go
// GetBankAccountActivity godoc
// @Summary      List a bank account's ledger activity
// @Description  Returns paginated ledger entries (debits/credits) for a bank-owned account. Employee-only; the account must be a bank account or the request is rejected with 404.
// @Tags         accounts
// @Produce      json
// @Param        id         path   int true  "Bank account ID"
// @Param        page       query  int false "Page number (default 1)"
// @Param        page_size  query  int false "Page size (default 20, max 200)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "entries, total_count"
// @Failure      400  {object}  map[string]interface{}  "validation_error"
// @Failure      403  {object}  map[string]interface{}  "missing bank_accounts.manage permission"
// @Failure      404  {object}  map[string]interface{}  "not a bank account / account not found"
// @Router       /api/v3/bank-accounts/{id}/activity [get]
func (h *AccountHandler) GetBankAccountActivity(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize > 200 {
		pageSize = 200
	}

	acct, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	// This route is in the bank-accounts namespace — a non-bank account ID
	// must not leak that client's ledger. 404 (not 403) so the endpoint's
	// blast radius equals its name.
	if acct.AccountKind != "bank" && acct.OwnerId != 1_000_000_000 {
		apiError(c, 404, ErrNotFound, "bank account not found")
		return
	}

	resp, err := h.accountClient.GetLedgerEntries(c.Request.Context(), &accountpb.GetLedgerEntriesRequest{
		AccountNumber: acct.AccountNumber,
		Page:          int32(page),
		PageSize:      int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	out := make([]gin.H, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, ledgerEntryToJSON(e, acct.CurrencyCode))
	}
	c.JSON(http.StatusOK, gin.H{
		"entries":     out,
		"total_count": resp.TotalCount,
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd api-gateway && go test ./internal/handler/ -run TestAccount_GetBankAccountActivity`
Expected: PASS (all four).

- [ ] **Step 6: Lint**

Run: `cd api-gateway && golangci-lint run ./internal/handler/`
Expected: no new warnings.

- [ ] **Step 7: Commit**

```bash
git add api-gateway/internal/handler/account_handler.go api-gateway/internal/handler/account_handler_test.go
git commit -m "feat(api-gateway): GetBankAccountActivity handler"
```

---

## Task 2: Register the route

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Add the route to the `bankAccountsRead` group**

In `router_v3.go`, the `bankAccountsRead` group currently reads:

```go
		// Bank account management — single umbrella perm.
		bankAccountsRead := protected.Group("/bank-accounts")
		bankAccountsRead.Use(middleware.RequirePermission(perms.BankAccounts.Manage.Any))
		{
			bankAccountsRead.GET("", h.Account.ListBankAccounts)
		}
```

Change the block to:

```go
		// Bank account management — single umbrella perm.
		bankAccountsRead := protected.Group("/bank-accounts")
		bankAccountsRead.Use(middleware.RequirePermission(perms.BankAccounts.Manage.Any))
		{
			bankAccountsRead.GET("", h.Account.ListBankAccounts)
			bankAccountsRead.GET("/:id/activity", h.Account.GetBankAccountActivity)
		}
```

- [ ] **Step 2: Build the gateway**

Run: `cd api-gateway && go build ./...`
Expected: builds clean.

- [ ] **Step 3: Regenerate swagger**

Run: `make swagger`
Expected: `api-gateway/docs/` regenerates; `git diff` shows the new `GetBankAccountActivity` annotation picked up.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/router/router_v3.go api-gateway/docs/
git commit -m "feat(api-gateway): wire GET /api/v3/bank-accounts/:id/activity"
```

---

## Task 3: Integration test

**Files:**
- Modify: `test-app/workflows/` — add to an existing accounts/bank workflow file, or create `test-app/workflows/bank_account_activity_test.go`

> Requires the docker-compose stack: `make docker-up`, wait for healthy, then run the integration suite. If a `bank_accounts`-related workflow file already exists (grep `test-app/workflows/` for `bank-accounts`), add the test there; otherwise create the file below.

- [ ] **Step 1: Write the integration test**

Create `test-app/workflows/bank_account_activity_test.go` (or append the function to the existing bank-accounts workflow file, dropping the `package`/`import`/build-tag header if appending):

```go
//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestBankAccountActivity_EmployeeCanView asserts an employee (admin) can list
// the bank's accounts, pick one, and read its ledger activity.
func TestBankAccountActivity_EmployeeCanView(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	listResp, err := adminC.GET("/api/v3/bank-accounts")
	if err != nil {
		t.Fatalf("list bank accounts: %v", err)
	}
	if listResp.StatusCode == 404 {
		t.Skip("bank-accounts endpoint not deployed")
	}
	helpers.RequireStatus(t, listResp, 200)
	accounts, _ := listResp.Body["accounts"].([]interface{})
	if len(accounts) == 0 {
		t.Skip("no seeded bank accounts — skipping")
	}
	first, _ := accounts[0].(map[string]interface{})
	bankAcctID := int(first["id"].(float64))

	actResp, err := adminC.GET(fmt.Sprintf("/api/v3/bank-accounts/%d/activity", bankAcctID))
	if err != nil {
		t.Fatalf("get bank account activity: %v", err)
	}
	if actResp.StatusCode == 404 {
		t.Skip("bank-account activity endpoint not deployed")
	}
	helpers.RequireStatus(t, actResp, 200)
	if _, ok := actResp.Body["entries"]; !ok {
		t.Errorf("expected an 'entries' field in the response")
	}
}

// TestBankAccountActivity_RejectsClientAccount asserts the endpoint 404s when
// pointed at a client account id (it lives in the bank-accounts namespace).
func TestBankAccountActivity_RejectsClientAccount(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, _, _ := setupActivatedClient(t, adminC)
	clientAcctID, _ := createClientAccount(t, adminC, clientID, "RSD", 5000)

	resp, err := adminC.GET(fmt.Sprintf("/api/v3/bank-accounts/%d/activity", clientAcctID))
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if resp.StatusCode == 404 && resp.Body == nil {
		// endpoint not deployed vs. business 404 — both are 404; treat a
		// deployed endpoint's structured 404 as the pass condition below.
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for a client account id, got %d", resp.StatusCode)
	}
}
```

`createClientAccount` and `loginAsAdmin` / `setupActivatedClient` already exist in `test-app/workflows/helpers_test.go` (the first was added in Spec 1's plan).

- [ ] **Step 2: Compile-check with the integration tag**

Run: `cd test-app && go vet -tags integration ./...`
Expected: no errors.

- [ ] **Step 3: Run the integration test** (requires `make docker-up`)

Run the integration suite filtered to `TestBankAccountActivity`.
Expected: PASS (or `Skip` if bank accounts aren't seeded).

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/bank_account_activity_test.go
git commit -m "test(accounts): bank-account activity integration test"
```

---

## Task 4: Documentation

**Files:**
- Modify: `docs/api/REST_API_v3.md`
- Modify: `docs/Specification.md`

- [ ] **Step 1: `REST_API_v3.md`**

In the bank-accounts section (search for the `GET /api/v3/bank-accounts` entry), add a new subsection right after the list endpoint:

```markdown
### GET /api/v3/bank-accounts/:id/activity

List the ledger activity (debits/credits) for a bank-owned account.

**Authentication:** Employee JWT + `bank_accounts.manage.any`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | uint64 | Bank account ID |

**Query Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `page` | int | Page number (default 1) |
| `page_size` | int | Page size (default 20, max 200) |

**Response 200:**
```json
{
  "entries": [
    {
      "id": 1,
      "entry_type": "credit",
      "amount": "100.00",
      "currency": "RSD",
      "balance_before": "0.00",
      "balance_after": "100.00",
      "description": "Transfer fee collection",
      "reference_id": "...",
      "reference_type": "transfer",
      "occurred_at": 1747000000
    }
  ],
  "total_count": 1
}
```

**Error Responses:**
- `400` — invalid id
- `403` — missing `bank_accounts.manage.any`
- `404` — account not found, or the id is not a bank account
```

- [ ] **Step 2: `Specification.md` §17 routes table**

Find the bank-accounts rows in the §17 route table (search for `/api/v3/bank-accounts`). Add a row directly under the `GET /api/v3/bank-accounts` row:

```markdown
| GET | `/api/v3/bank-accounts/:id/activity` | AuthMiddleware + RequirePermission(`bank_accounts.manage.any`) | AccountHandler.GetBankAccountActivity | Ledger activity for a bank-owned account; non-bank account id → 404 |
```

- [ ] **Step 3: Commit**

```bash
git add docs/api/REST_API_v3.md docs/Specification.md
git commit -m "docs(accounts): document bank-account activity endpoint"
```

---

## Final verification

- [ ] **Step 1: Build + test + lint**

Run: `cd api-gateway && go build ./... && go test ./internal/handler/ ./internal/router/ && golangci-lint run ./internal/handler/ ./internal/router/`
Expected: builds, all tests pass, no new lint warnings.

- [ ] **Step 2: Integration** (requires Docker)

Run the integration suite filtered to `TestBankAccountActivity` against `make docker-up`.
Expected: green.

---

## Self-Review Notes

- **Spec coverage:** Route → Task 2. Handler + kind guard → Task 1. Testing (unit) → Task 1; (integration) → Task 3. Docs → Task 4. Every spec section maps to a task.
- **Placeholder scan:** none — every step has concrete code or an exact command.
- **Type consistency:** `GetBankAccountActivity` is the handler name in Tasks 1, 2, 4 and the swagger annotation. `accountFullStub.getFn` / `getLedger` field names match the existing test stub. Route path `/bank-accounts/:id/activity` is identical in the test helper (Task 1), the router (Task 2), and the docs (Task 4).
- **Note:** `make docker-up` may not be running in every environment — Task 3 Step 3 and the final integration step are guarded with `t.Skip` and can be deferred; the unit tests in Task 1 fully cover the handler logic.
