# OTC Client Access + Ticker-Based Offers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open the OTC trading surface to clients (gated by resource ownership instead of employee permissions), add explicit employee-on-behalf support, and let `POST /api/v3/otc/offers` be keyed by ticker.

**Architecture:** Clients reach the OTC routes through a new `RequirePermissionOrClient` middleware that lets client principals pass while still permission-gating employees. Every OTC route validates that caller-supplied accounts belong to the resolved owner via a shared gateway helper. Each party binds only their own account: the initiator at offer-creation, the acceptor at accept; the counterparty account is read from the persisted offer. The REST `ticker` field is resolved to a `stock_id` gateway-side via a new `GetStockByTicker` RPC.

**Tech Stack:** Go, Gin (api-gateway), gRPC/protobuf (`contract/`), GORM + PostgreSQL (stock-service), golangci-lint, the repo's `make` targets.

**Source spec:** `docs/superpowers/specs/2026-05-14-otc-client-access-design.md`

**Design deviation from the spec:** Spec Section 2 listed an `account_id` on `CounterOffer`. Deeper exploration showed counters never move money and `identifyOTCBuyerSellerOwners` already derives buyer/seller from `(offer.direction, initiator, acceptor)` — so only two accounts are ever needed (initiator's, bound at create; acceptor's, bound at accept). `CounterOffer` therefore takes **no** `account_id`; it only gains `on_behalf_of_client_id` for the permission/ownership gate. This is simpler and matches the actual saga code.

---

## File Structure

**Created:**
- None — all changes modify existing files.

**Modified — contracts:**
- `contract/permissions/catalog.yaml` — new `otc.trade.on_behalf` permission + role grants
- `contract/permissions/perms.gen.go` — regenerated
- `contract/proto/stock/stock.proto` — OTC request messages + `GetStockByTicker` RPC
- `contract/stockpb/*.pb.go` — regenerated

**Modified — stock-service:**
- `stock-service/internal/model/otc_offer.go` — `InitiatorAccountID` field
- `stock-service/internal/model/option_contract.go` — `BuyerAccountID` + `SellerAccountID` fields
- `stock-service/internal/service/security_service.go` — `GetByTicker` method
- `stock-service/internal/handler/security_handler.go` — `GetStockByTicker` gRPC handler + facade
- `stock-service/internal/service/otc_offer_service.go` — `CreateOfferInput.InitiatorAccountID`
- `stock-service/internal/service/otc_accept_saga.go` — single acceptor account, populate contract accounts
- `stock-service/internal/service/otc_exercise_saga.go` — read accounts from contract
- `stock-service/internal/handler/otc_options_handler.go` — handler signatures for the new proto shapes

**Modified — api-gateway:**
- `api-gateway/internal/middleware/auth.go` — `RequirePermissionOrClient` middleware
- `api-gateway/internal/handler/validation.go` — `resolveAndCheckAccount` ownership helper
- `api-gateway/internal/handler/otc_options_handler.go` — `CreateOffer`, `CounterOffer`, `AcceptOffer`, `ExerciseContract`, constructor
- `api-gateway/internal/handler/portfolio_handler.go` — `MakePublic`, `BuyOTCOffer` (ownership)
- `api-gateway/internal/router/handlers.go` — `NewOTCOptionsHandler` wiring
- `api-gateway/internal/router/router_v3.go` — OTC route middleware swap

**Modified — tests & docs:**
- `test-app/workflows/otc_options_test.go` — replace `TestOTCOptions_ClientCannotTrade`, add lifecycle/ownership/on-behalf/ticker tests
- `Specification.md`, `docs/api/REST_API_v3.md`, `api-gateway/docs/*` (swagger)

---

## Phase 1 — Permissions

### Task 1: Add the `otc.trade.on_behalf` permission

**Files:**
- Modify: `contract/permissions/catalog.yaml`
- Regenerate: `contract/permissions/perms.gen.go`

- [ ] **Step 1: Add the permission to the catalog list**

In `contract/permissions/catalog.yaml`, the OTC block is currently:

```yaml
  # OTC
  - otc.trade.accept
  - otc.trade.exercise
  - otc.trade.expire
  - otc.read.all
```

Change it to:

```yaml
  # OTC
  - otc.trade.accept
  - otc.trade.exercise
  - otc.trade.expire
  - otc.trade.on_behalf
  - otc.read.all
```

- [ ] **Step 2: Grant it to `EmployeeAgent`**

In the `EmployeeAgent` `grants:` block, currently:

```yaml
      - otc.trade.accept
      - otc.trade.exercise
```

Change to:

```yaml
      - otc.trade.accept
      - otc.trade.exercise
      - otc.trade.on_behalf
```

(`EmployeeSupervisor` and `EmployeeAdmin` inherit `EmployeeAgent`, so they get it transitively. No other role blocks change.)

- [ ] **Step 3: Regenerate the typed permissions**

Run: `make permissions`
Expected: `contract/permissions/perms.gen.go` is rewritten; `git diff` shows a new `OnBehalf Permission` field under `Otc.Trade` with value `"otc.trade.on_behalf"`.

- [ ] **Step 4: Verify it compiles**

Run: `cd contract && go build ./...`
Expected: builds with no errors.

- [ ] **Step 5: Commit**

```bash
git add contract/permissions/catalog.yaml contract/permissions/perms.gen.go
git commit -m "feat(perms): add otc.trade.on_behalf permission"
```

---

## Phase 2 — Proto & contracts

### Task 2: Update OTC proto messages and add `GetStockByTicker`

**Files:**
- Modify: `contract/proto/stock/stock.proto`
- Regenerate: `contract/stockpb/*.pb.go`

- [ ] **Step 1: Add `GetStockByTicker` to `SecurityGRPCService`**

In `contract/proto/stock/stock.proto`, the service block currently starts:

```proto
service SecurityGRPCService {
  // Stocks
  rpc ListStocks(ListStocksRequest) returns (ListStocksResponse);
  rpc GetStock(GetStockRequest) returns (StockDetail);
```

Change to:

```proto
service SecurityGRPCService {
  // Stocks
  rpc ListStocks(ListStocksRequest) returns (ListStocksResponse);
  rpc GetStock(GetStockRequest) returns (StockDetail);
  rpc GetStockByTicker(GetStockByTickerRequest) returns (StockDetail);
```

And next to `message GetStockRequest { uint64 id = 1; }` add:

```proto
message GetStockByTickerRequest {
  string ticker = 1;
}
```

- [ ] **Step 2: Add `account_id` + `on_behalf_of_client_id` to `CreateOTCOfferRequest`**

Currently:

```proto
message CreateOTCOfferRequest {
  int64  actor_user_id = 1;
  string actor_system_type = 2;
  string direction = 3;
  uint64 stock_id = 4;
  string quantity = 5;
  string strike_price = 6;
  string premium = 7;
  string settlement_date = 8;
  PartyRef counterparty = 9;
}
```

Change to:

```proto
message CreateOTCOfferRequest {
  int64  actor_user_id = 1;
  string actor_system_type = 2;
  string direction = 3;
  uint64 stock_id = 4;
  string quantity = 5;
  string strike_price = 6;
  string premium = 7;
  string settlement_date = 8;
  PartyRef counterparty = 9;
  // Initiator's account: pays the premium (buy_initiated) or receives it
  // (sell_initiated). Bound at creation; the gateway verifies ownership.
  uint64 account_id = 10;
  // Set when an employee acts on behalf of a client. 0 = acting as the bank.
  uint64 on_behalf_of_client_id = 11;
}
```

- [ ] **Step 3: Add `on_behalf_of_client_id` to `CounterOTCOfferRequest`**

Currently:

```proto
message CounterOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  string quantity = 4;
  string strike_price = 5;
  string premium = 6;
  string settlement_date = 7;
}
```

Change to:

```proto
message CounterOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  string quantity = 4;
  string strike_price = 5;
  string premium = 6;
  string settlement_date = 7;
  // Set when an employee acts on behalf of a client. 0 = acting as the bank.
  uint64 on_behalf_of_client_id = 8;
}
```

- [ ] **Step 4: Replace the two account IDs on `AcceptOTCOfferRequest` with one**

Currently:

```proto
message AcceptOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  // Buyer's account that pays the premium.
  uint64 buyer_account_id = 4;
  // Seller's account that receives the premium.
  uint64 seller_account_id = 5;
}
```

Change to:

```proto
message AcceptOTCOfferRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  // The acceptor's own account. Maps to buyer or seller via offer.direction;
  // the counterparty (initiator) account is read from the persisted offer.
  uint64 account_id = 4;
  // Set when an employee acts on behalf of a client. 0 = acting as the bank.
  uint64 on_behalf_of_client_id = 5;
}
```

- [ ] **Step 5: Drop the account IDs on `ExerciseContractRequest`, add on-behalf**

Currently:

```proto
message ExerciseContractRequest {
  uint64 contract_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  // Buyer's account that pays the strike (and gets the shares).
  uint64 buyer_account_id = 4;
  // Seller's account that receives the strike funds.
  uint64 seller_account_id = 5;
}
```

Change to:

```proto
message ExerciseContractRequest {
  uint64 contract_id = 1;
  int64  actor_user_id = 2;
  string actor_system_type = 3;
  // Set when an employee acts on behalf of a client. 0 = acting as the bank.
  uint64 on_behalf_of_client_id = 4;
}
```

- [ ] **Step 6: Regenerate**

Run: `make proto`
Expected: `contract/stockpb/stock.pb.go` and `contract/stockpb/stock_grpc.pb.go` are rewritten. `git diff` shows the new `GetStockByTicker` client/server methods, `GetStockByTickerRequest`, the new fields on `CreateOTCOfferRequest`/`CounterOTCOfferRequest`, the reshaped `AcceptOTCOfferRequest`/`ExerciseContractRequest`.

- [ ] **Step 7: Confirm the contract module still builds**

Run: `cd contract && go build ./...`
Expected: builds clean.

- [ ] **Step 8: Commit**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(proto): OTC account binding + GetStockByTicker RPC"
```

> Note: stock-service and api-gateway will not compile until Phases 3–10 land. That is expected — the remaining tasks fix each call site. Run `make build` only at the end of Phase 10.

---

## Phase 3 — stock-service: ticker lookup

### Task 3: `SecurityService.GetByTicker`

**Files:**
- Modify: `stock-service/internal/service/security_service.go`
- Test: `stock-service/internal/service/security_service_test.go`

- [ ] **Step 1: Write the failing test**

The existing test file already has a `secSvcStockRepo` fake whose `GetByTicker(_ string) (*model.Stock, error)` is defined (line ~34). Add this test:

```go
func TestSecurityService_GetByTicker_Found(t *testing.T) {
	want := &model.Stock{ID: 7, Ticker: "AAPL"}
	svc := service.NewSecurityService(&secSvcStockRepo{stock: want}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, err := svc.GetByTicker("AAPL")
	if err != nil {
		t.Fatalf("GetByTicker: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("got id %d, want 7", got.ID)
	}
}
```

If the existing `secSvcStockRepo.GetByTicker` ignores its argument and returns a fixed stock, adjust the fake to return its `stock` field (check the current fake; if it is `func (m *secSvcStockRepo) GetByTicker(_ string) (*model.Stock, error) { return m.stock, m.err }` it already works).

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd stock-service && go test ./internal/service/ -run TestSecurityService_GetByTicker_Found`
Expected: FAIL — `svc.GetByTicker` undefined.

- [ ] **Step 3: Implement `GetByTicker`**

In `stock-service/internal/service/security_service.go`, next to the existing `GetStock` (line ~54):

```go
func (s *SecurityService) GetStock(id uint64) (*model.Stock, error) {
```

add:

```go
// GetByTicker resolves a stock by its globally-unique ticker. Used by the
// OTC option flow so callers can reference securities by ticker.
func (s *SecurityService) GetByTicker(ticker string) (*model.Stock, error) {
	return s.stockRepo.GetByTicker(ticker)
}
```

(The struct field is `stockRepo`; confirm the exact name by reading the `SecurityService` struct at the top of the file and the `NewSecurityService` constructor — match whatever the existing `GetStock` uses.)

- [ ] **Step 4: Run the test**

Run: `cd stock-service && go test ./internal/service/ -run TestSecurityService_GetByTicker_Found`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/security_service.go stock-service/internal/service/security_service_test.go
git commit -m "feat(stock-service): SecurityService.GetByTicker"
```

### Task 4: `SecurityHandler.GetStockByTicker` gRPC handler

**Files:**
- Modify: `stock-service/internal/handler/security_handler.go`
- Test: `stock-service/internal/handler/security_handler_rpc_test.go`

- [ ] **Step 1: Extend the `securitySvcFacade` interface**

In `security_handler.go`, the facade is:

```go
type securitySvcFacade interface {
	ListStocks(filter repository.StockFilter) ([]model.Stock, int64, error)
	GetStockWithOptions(id uint64) (*model.Stock, []model.Option, error)
	...
}
```

Add a method:

```go
type securitySvcFacade interface {
	ListStocks(filter repository.StockFilter) ([]model.Stock, int64, error)
	GetStockWithOptions(id uint64) (*model.Stock, []model.Option, error)
	GetByTicker(ticker string) (*model.Stock, error)
	...
}
```

- [ ] **Step 2: Write the failing handler test**

In `security_handler_rpc_test.go`, the `mockSecuritySvc` fake needs a `GetByTicker`. Add to the fake:

```go
func (m *mockSecuritySvc) GetByTicker(ticker string) (*model.Stock, error) {
	if m.byTickerErr != nil {
		return nil, m.byTickerErr
	}
	return m.byTickerStock, nil
}
```

and add the `byTickerStock *model.Stock` and `byTickerErr error` fields to the `mockSecuritySvc` struct. Then add:

```go
func TestSecurityHandler_GetStockByTicker_Success(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{byTickerStock: &model.Stock{ID: 9, Ticker: "MSFT"}}, nil, nil, nil)
	resp, err := h.GetStockByTicker(context.Background(), &pb.GetStockByTickerRequest{Ticker: "MSFT"})
	if err != nil {
		t.Fatalf("GetStockByTicker: %v", err)
	}
	if resp.Id != 9 {
		t.Errorf("got id %d, want 9", resp.Id)
	}
}

func TestSecurityHandler_GetStockByTicker_NotFound(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{byTickerErr: gorm.ErrRecordNotFound}, nil, nil, nil)
	_, err := h.GetStockByTicker(context.Background(), &pb.GetStockByTickerRequest{Ticker: "NOPE"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", status.Code(err))
	}
}
```

Add `"gorm.io/gorm"` to the test imports if not present. Confirm the exact signature of `newSecurityHandlerForTest` (it is referenced at `security_handler.go:62`) and match its argument list.

- [ ] **Step 3: Run to confirm failure**

Run: `cd stock-service && go test ./internal/handler/ -run TestSecurityHandler_GetStockByTicker`
Expected: FAIL — `h.GetStockByTicker` undefined.

- [ ] **Step 4: Implement the handler**

In `security_handler.go`, after `GetStock` (line ~155–162):

```go
func (h *SecurityHandler) GetStockByTicker(ctx context.Context, req *pb.GetStockByTickerRequest) (*pb.StockDetail, error) {
	if req.Ticker == "" {
		return nil, status.Error(codes.InvalidArgument, "ticker is required")
	}
	stock, err := h.secSvc.GetByTicker(req.Ticker)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "no stock with ticker %q", req.Ticker)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	lid := h.resolveListingID(stock.ID, "stock")
	return toStockDetail(stock, nil, lid), nil
}
```

Add `"errors"` and `"gorm.io/gorm"` to the file's imports. Confirm `toStockDetail`'s signature accepts a `nil` options slice (it is called as `toStockDetail(stock, options, lid)` in `GetStock`; `nil` for options is valid).

- [ ] **Step 5: Run the tests**

Run: `cd stock-service && go test ./internal/handler/ -run TestSecurityHandler_GetStockByTicker`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/handler/security_handler.go stock-service/internal/handler/security_handler_rpc_test.go
git commit -m "feat(stock-service): GetStockByTicker gRPC handler"
```

---

## Phase 4 — stock-service: models

### Task 5: `OTCOffer.InitiatorAccountID`

**Files:**
- Modify: `stock-service/internal/model/otc_offer.go`
- Test: `stock-service/internal/model/otc_coverage_test.go`

- [ ] **Step 1: Add the field**

In `stock-service/internal/model/otc_offer.go`, the `OTCOffer` struct currently has (near the end):

```go
	Public            bool      `gorm:"not null;default:true" json:"public"`
	Private           bool      `gorm:"not null;default:false" json:"private"`
	PrivateToBankCode *string   `gorm:"size:3" json:"private_to_bank_code,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Version           int64     `gorm:"not null;default:0" json:"-"`
```

Insert `InitiatorAccountID` right after `StockID`'s logical group — place it directly above `Public`:

```go
	// InitiatorAccountID is the initiator's account bound at offer creation:
	// it pays the premium on buy_initiated offers and receives it on
	// sell_initiated offers. The counterparty's account is bound at accept.
	InitiatorAccountID uint64    `gorm:"not null;default:0" json:"initiator_account_id"`
	Public            bool      `gorm:"not null;default:true" json:"public"`
```

- [ ] **Step 2: Write a test asserting the field round-trips**

In `otc_coverage_test.go` add:

```go
func TestOTCOffer_InitiatorAccountID_Persists(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.OTCOffer{})
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerTypeClient,
		Direction:          model.OTCDirectionSellInitiated,
		StockID:            1,
		Quantity:           decimal.NewFromInt(10),
		StrikePrice:        decimal.NewFromInt(5),
		Premium:            decimal.NewFromInt(1),
		SettlementDate:     time.Now().Add(48 * time.Hour),
		Status:             model.OTCOfferStatusPending,
		InitiatorAccountID: 4242,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got model.OTCOffer
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.InitiatorAccountID != 4242 {
		t.Errorf("got %d, want 4242", got.InitiatorAccountID)
	}
}
```

Match the imports/helpers already used by neighbouring tests in the file (`testutil`, `decimal`, `time`, `model.OwnerTypeClient` — confirm the exact `OwnerType` constant name in `owner.go`).

- [ ] **Step 3: Run it**

Run: `cd stock-service && go test ./internal/model/ -run TestOTCOffer_InitiatorAccountID_Persists`
Expected: PASS (AutoMigrate adds the column; the field is plain data).

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/model/otc_offer.go stock-service/internal/model/otc_coverage_test.go
git commit -m "feat(stock-service): OTCOffer.InitiatorAccountID"
```

### Task 6: `OptionContract.BuyerAccountID` + `SellerAccountID`

**Files:**
- Modify: `stock-service/internal/model/option_contract.go`
- Test: `stock-service/internal/model/otc_coverage_test.go`

- [ ] **Step 1: Add the fields**

In `option_contract.go`, the struct has:

```go
	SettlementDate  time.Time       `gorm:"type:date;not null;index:ix_oc_settle" json:"settlement_date"`
	Status          string          `gorm:"size:16;not null;index:ix_oc_buyer,priority:3;index:ix_oc_seller,priority:3" json:"status"`
	SagaID          string          `gorm:"size:64;not null" json:"saga_id"`
```

Insert the two account fields directly above `Status`:

```go
	SettlementDate  time.Time       `gorm:"type:date;not null;index:ix_oc_settle" json:"settlement_date"`
	// Accounts bound at accept time: buyer's pays the premium/strike, seller's
	// receives them. Read straight off the contract on exercise.
	BuyerAccountID  uint64          `gorm:"not null;default:0" json:"buyer_account_id"`
	SellerAccountID uint64          `gorm:"not null;default:0" json:"seller_account_id"`
	Status          string          `gorm:"size:16;not null;index:ix_oc_buyer,priority:3;index:ix_oc_seller,priority:3" json:"status"`
```

- [ ] **Step 2: Write a round-trip test**

In `otc_coverage_test.go`:

```go
func TestOptionContract_AccountIDs_Persist(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.OptionContract{})
	c := &model.OptionContract{
		OfferID:         1,
		BuyerOwnerType:  model.OwnerTypeClient,
		SellerOwnerType: model.OwnerTypeClient,
		StockID:         1,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5),
		PremiumPaid:     decimal.NewFromInt(1),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().Add(48 * time.Hour),
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-1",
		PremiumPaidAt:   time.Now(),
		BuyerAccountID:  111,
		SellerAccountID: 222,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got model.OptionContract
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.BuyerAccountID != 111 || got.SellerAccountID != 222 {
		t.Errorf("got buyer=%d seller=%d, want 111/222", got.BuyerAccountID, got.SellerAccountID)
	}
}
```

- [ ] **Step 3: Run it**

Run: `cd stock-service && go test ./internal/model/ -run TestOptionContract_AccountIDs_Persist`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/model/option_contract.go stock-service/internal/model/otc_coverage_test.go
git commit -m "feat(stock-service): OptionContract buyer/seller account IDs"
```

---

## Phase 5 — stock-service: OTC service layer

### Task 7: `Create` persists the initiator account

**Files:**
- Modify: `stock-service/internal/service/otc_offer_service.go`
- Test: `stock-service/internal/service/otc_offer_service_test.go` (or the existing OTC service test file — confirm the name)

- [ ] **Step 1: Add `InitiatorAccountID` to `CreateOfferInput`**

In `otc_offer_service.go`, `CreateOfferInput` is:

```go
type CreateOfferInput struct {
	ActorUserID            int64
	ActorSystemType        string
	Direction              string
	StockID                uint64
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	Premium                decimal.Decimal
	SettlementDate         time.Time
	CounterpartyUserID     *int64
	CounterpartySystemType *string
}
```

Add the field:

```go
type CreateOfferInput struct {
	ActorUserID            int64
	ActorSystemType        string
	Direction              string
	StockID                uint64
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	Premium                decimal.Decimal
	SettlementDate         time.Time
	CounterpartyUserID     *int64
	CounterpartySystemType *string
	InitiatorAccountID     uint64
}
```

- [ ] **Step 2: Write the failing test**

Find the existing OTC offer service test (grep `func TestOTCOfferService` under `stock-service/internal/service/`). Following the pattern of the existing `Create` success test, add one that asserts the account is stored:

```go
func TestOTCOfferService_Create_StoresInitiatorAccount(t *testing.T) {
	// Build the service exactly as the neighbouring Create tests do
	// (same fakes for offers repo, holdings, revisions).
	svc, offersRepo := newOTCOfferServiceForTest(t) // match the existing helper name
	in := service.CreateOfferInput{
		ActorUserID: 5, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1,
		Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(10),
		Premium: decimal.NewFromInt(1), SettlementDate: time.Now().Add(72 * time.Hour),
		InitiatorAccountID: 9001,
	}
	o, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if o.InitiatorAccountID != 9001 {
		t.Errorf("got %d, want 9001", o.InitiatorAccountID)
	}
	_ = offersRepo
}
```

If there is no shared `newOTCOfferServiceForTest` helper, copy the construction block verbatim from the nearest existing `Create` test in the same file.

- [ ] **Step 3: Run to confirm failure**

Run: `cd stock-service && go test ./internal/service/ -run TestOTCOfferService_Create_StoresInitiatorAccount`
Expected: FAIL — `o.InitiatorAccountID` is 0.

- [ ] **Step 4: Persist the field in `Create`**

In `Create`, the model is built as:

```go
	o := &model.OTCOffer{
		InitiatorOwnerType:          initOwnerType,
		InitiatorOwnerID:            initOwnerID,
		CounterpartyOwnerType:       cpOwnerType,
		CounterpartyOwnerID:         cpOwnerID,
		Direction:                   in.Direction,
		StockID:                     in.StockID,
		Quantity:                    in.Quantity,
		StrikePrice:                 in.StrikePrice,
		Premium:                     in.Premium,
		SettlementDate:              in.SettlementDate,
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: in.ActorSystemType,
		LastModifiedByPrincipalID:   uint64(in.ActorUserID),
	}
```

Add one line:

```go
	o := &model.OTCOffer{
		InitiatorOwnerType:          initOwnerType,
		InitiatorOwnerID:            initOwnerID,
		CounterpartyOwnerType:       cpOwnerType,
		CounterpartyOwnerID:         cpOwnerID,
		Direction:                   in.Direction,
		StockID:                     in.StockID,
		Quantity:                    in.Quantity,
		StrikePrice:                 in.StrikePrice,
		Premium:                     in.Premium,
		SettlementDate:              in.SettlementDate,
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: in.ActorSystemType,
		LastModifiedByPrincipalID:   uint64(in.ActorUserID),
		InitiatorAccountID:          in.InitiatorAccountID,
	}
```

- [ ] **Step 5: Run the test**

Run: `cd stock-service && go test ./internal/service/ -run TestOTCOfferService_Create_StoresInitiatorAccount`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/otc_offer_service.go stock-service/internal/service/otc_offer_service_test.go
git commit -m "feat(stock-service): persist initiator account on OTC offer create"
```

### Task 8: `Accept` binds the acceptor account, populates the contract

**Files:**
- Modify: `stock-service/internal/service/otc_accept_saga.go`
- Test: the OTC accept saga test file (grep `func TestOTCOfferService_Accept` / `func Test.*Accept` under `stock-service/internal/service/`)

- [ ] **Step 1: Reshape `AcceptInput`**

Currently:

```go
type AcceptInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	BuyerAccountID  uint64 // buyer's account that pays the premium
	SellerAccountID uint64 // seller's account that receives the premium
}
```

Change to:

```go
type AcceptInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	// AcceptorAccountID is the accepting party's own account. The other
	// side's account is read from offer.InitiatorAccountID. Which is buyer
	// vs seller is decided by offer.direction.
	AcceptorAccountID uint64
}
```

- [ ] **Step 2: Derive buyer/seller account IDs inside `Accept`**

In `Accept`, immediately after this block:

```go
	buyerOwnerType, buyerOwnerID, sellerOwnerType, sellerOwnerID := identifyOTCBuyerSellerOwners(o, in.ActorUserID, in.ActorSystemType)

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
```

replace the `buyerAcct`/`sellerAcct` fetch with account-ID derivation first:

```go
	buyerOwnerType, buyerOwnerID, sellerOwnerType, sellerOwnerID := identifyOTCBuyerSellerOwners(o, in.ActorUserID, in.ActorSystemType)

	// The initiator bound their account at create; the acceptor binds theirs
	// now. Which is buyer vs seller follows offer.direction: on a
	// sell_initiated offer the initiator is the seller and the acceptor the
	// buyer; on a buy_initiated offer it is the reverse.
	var buyerAccountID, sellerAccountID uint64
	if o.Direction == model.OTCDirectionSellInitiated {
		sellerAccountID = o.InitiatorAccountID
		buyerAccountID = in.AcceptorAccountID
	} else {
		buyerAccountID = o.InitiatorAccountID
		sellerAccountID = in.AcceptorAccountID
	}
	if buyerAccountID == 0 || sellerAccountID == 0 {
		return nil, errors.New("both buyer and seller accounts must be bound")
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: buyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: sellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
```

- [ ] **Step 3: Persist the account IDs on the contract and use the derived IDs in the saga**

The contract literal currently is:

```go
	contract := &model.OptionContract{
		OfferID:         o.ID,
		BuyerOwnerType:  buyerOwnerType,
		BuyerOwnerID:    buyerOwnerID,
		SellerOwnerType: sellerOwnerType,
		SellerOwnerID:   sellerOwnerID,
		StockID:         o.StockID, Quantity: o.Quantity, StrikePrice: o.StrikePrice,
		PremiumPaid: o.Premium, PremiumCurrency: premiumCcy, StrikeCurrency: premiumCcy,
		SettlementDate: o.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
	}
```

Add the two account fields:

```go
	contract := &model.OptionContract{
		OfferID:         o.ID,
		BuyerOwnerType:  buyerOwnerType,
		BuyerOwnerID:    buyerOwnerID,
		SellerOwnerType: sellerOwnerType,
		SellerOwnerID:   sellerOwnerID,
		StockID:         o.StockID, Quantity: o.Quantity, StrikePrice: o.StrikePrice,
		PremiumPaid: o.Premium, PremiumCurrency: premiumCcy, StrikeCurrency: premiumCcy,
		SettlementDate: o.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID:  buyerAccountID,
		SellerAccountID: sellerAccountID,
	}
```

Then in the `StepReservePremium` forward step, the call currently reads:

```go
				_, e := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium))
```

Change `in.BuyerAccountID` to `buyerAccountID`:

```go
				_, e := s.accounts.ReserveFunds(ctx, buyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium))
```

(`buyerAcct.AccountNumber` / `sellerAcct.AccountNumber` are still used in steps 3–4 and remain valid because `buyerAcct`/`sellerAcct` are now fetched from the derived IDs.)

- [ ] **Step 4: Update the existing accept-saga tests**

Every test that constructs `service.AcceptInput{... BuyerAccountID: X, SellerAccountID: Y}` must change. For each:
- Set the offer fake's `InitiatorAccountID` to the side that matches `offer.Direction` (sell_initiated → initiator is seller, so `InitiatorAccountID = <old SellerAccountID>`; buy_initiated → `InitiatorAccountID = <old BuyerAccountID>`).
- Set `AcceptInput.AcceptorAccountID` to the other side.

Add one explicit regression test:

```go
func TestOTCOfferService_Accept_BindsAcceptorAccount_SellInitiated(t *testing.T) {
	// Construct the accept saga service with the same fakes the existing
	// Accept tests use. The offer is sell_initiated with InitiatorAccountID=700.
	svc, offerFake := newOTCAcceptServiceForTest(t) // match the existing helper
	offerFake.offer.Direction = model.OTCDirectionSellInitiated
	offerFake.offer.InitiatorAccountID = 700
	c, err := svc.Accept(context.Background(), service.AcceptInput{
		OfferID: offerFake.offer.ID, ActorUserID: 42, ActorSystemType: "client",
		AcceptorAccountID: 800,
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if c.SellerAccountID != 700 || c.BuyerAccountID != 800 {
		t.Errorf("got buyer=%d seller=%d, want 800/700", c.BuyerAccountID, c.SellerAccountID)
	}
}
```

If the existing tests build the service inline (no helper), copy that construction block verbatim and adapt.

- [ ] **Step 5: Run to confirm failure, then it will be fixed by Steps 1–3**

Run: `cd stock-service && go test ./internal/service/ -run Accept`
Expected: after Steps 1–4 all accept-saga tests PASS. (Run the whole `-run Accept` set since the input reshape touches every accept test.)

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/otc_accept_saga.go stock-service/internal/service/
git commit -m "feat(stock-service): bind acceptor account, persist contract accounts on OTC accept"
```

### Task 9: `ExerciseContract` reads accounts from the contract

**Files:**
- Modify: `stock-service/internal/service/otc_exercise_saga.go`
- Test: the OTC exercise saga test file (grep `Exercise` under `stock-service/internal/service/`)

- [ ] **Step 1: Reshape `ExerciseInput`**

Currently:

```go
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
	BuyerAccountID  uint64 // buyer's account that pays the strike (and gets shares)
	SellerAccountID uint64 // seller's account that receives the strike funds
}
```

Change to:

```go
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
}
```

- [ ] **Step 2: Read the account IDs off the contract inside `ExerciseContract`**

After the buyer-only guard:

```go
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
	if c.BuyerOwnerType != actorOwnerType || !ownerIDEqual(c.BuyerOwnerID, actorOwnerID) {
		return nil, errors.New("only the contract buyer can exercise")
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
```

change the two fetches to use the contract's stored IDs:

```go
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
	if c.BuyerOwnerType != actorOwnerType || !ownerIDEqual(c.BuyerOwnerID, actorOwnerID) {
		return nil, errors.New("only the contract buyer can exercise")
	}

	if c.BuyerAccountID == 0 || c.SellerAccountID == 0 {
		return nil, errors.New("contract has no bound accounts")
	}
	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: c.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: c.SellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
```

Then read the rest of `otc_exercise_saga.go` (lines ~90 onward, not quoted here) and replace every remaining `in.BuyerAccountID` with `c.BuyerAccountID` and `in.SellerAccountID` with `c.SellerAccountID` — for example the `ReserveFunds` call in the `reserve_strike` step. Grep the file for `in.BuyerAccountID` and `in.SellerAccountID` to find them all; there should be no occurrences left when done.

- [ ] **Step 3: Update the existing exercise tests**

Every test constructing `service.ExerciseInput{... BuyerAccountID: X, SellerAccountID: Y}` changes: drop those two fields from the input, and instead set `BuyerAccountID`/`SellerAccountID` on the **contract fake** the test feeds to `s.contracts.GetByID`. Add a regression test:

```go
func TestOTCOfferService_Exercise_UsesContractAccounts(t *testing.T) {
	svc, contractFake := newOTCExerciseServiceForTest(t) // match the existing helper
	contractFake.contract.BuyerAccountID = 111
	contractFake.contract.SellerAccountID = 222
	_, err := svc.ExerciseContract(context.Background(), service.ExerciseInput{
		ContractID: contractFake.contract.ID, ActorUserID: 42, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("ExerciseContract: %v", err)
	}
	// The account-service fake records which IDs GetAccount was called with;
	// assert it saw 111 and 222 (match the fake's recording field).
}
```

Adapt the assertion to whatever the existing account-service fake exposes (a recorded-IDs slice or per-ID map). If the fake does not record calls, add a minimal `gotAccountIDs []uint64` slice to it.

- [ ] **Step 4: Run the exercise tests**

Run: `cd stock-service && go test ./internal/service/ -run Exercise`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/otc_exercise_saga.go stock-service/internal/service/
git commit -m "feat(stock-service): exercise reads accounts from the contract"
```

---

## Phase 6 — stock-service: OTC gRPC handler

### Task 10: Update `otc_options_handler.go` to the new proto shapes

**Files:**
- Modify: `stock-service/internal/handler/otc_options_handler.go`
- Test: `stock-service/internal/handler/otc_options_handler_test.go` (confirm exact name; grep `OTCOptionsHandler` under `stock-service/internal/handler/`)

- [ ] **Step 1: Update `CreateOffer`**

The handler currently builds:

```go
	input := service.CreateOfferInput{
		ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		Direction: in.Direction, StockID: in.StockId,
		Quantity: qty, StrikePrice: strike, Premium: prem,
		SettlementDate: settle,
	}
```

Add the account field:

```go
	input := service.CreateOfferInput{
		ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		Direction: in.Direction, StockID: in.StockId,
		Quantity: qty, StrikePrice: strike, Premium: prem,
		SettlementDate:     settle,
		InitiatorAccountID: in.AccountId,
	}
```

Also add a guard near the other `InvalidArgument` checks at the top of `CreateOffer`:

```go
	if in.AccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
```

(`in.OnBehalfOfClientId` is consumed by the gateway for the ownership/permission gate; stock-service does not need it for `Create`, so it is intentionally ignored here.)

- [ ] **Step 2: Update `AcceptOffer`**

Currently:

```go
func (h *OTCOptionsHandler) AcceptOffer(ctx context.Context, in *stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error) {
	if in.BuyerAccountId == 0 || in.SellerAccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_account_id and seller_account_id are required")
	}
	c, err := h.svc.Accept(ctx, service.AcceptInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		BuyerAccountID: in.BuyerAccountId, SellerAccountID: in.SellerAccountId,
	})
```

Change to:

```go
func (h *OTCOptionsHandler) AcceptOffer(ctx context.Context, in *stockpb.AcceptOTCOfferRequest) (*stockpb.AcceptOfferResponse, error) {
	if in.AccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	c, err := h.svc.Accept(ctx, service.AcceptInput{
		OfferID: in.OfferId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		AcceptorAccountID: in.AccountId,
	})
```

- [ ] **Step 3: Update `ExerciseContract`**

Currently:

```go
func (h *OTCOptionsHandler) ExerciseContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
	if in.BuyerAccountId == 0 || in.SellerAccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_account_id and seller_account_id are required")
	}
	c, err := h.svc.ExerciseContract(ctx, service.ExerciseInput{
		ContractID: in.ContractId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		BuyerAccountID: in.BuyerAccountId, SellerAccountID: in.SellerAccountId,
	})
```

Change to:

```go
func (h *OTCOptionsHandler) ExerciseContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
	c, err := h.svc.ExerciseContract(ctx, service.ExerciseInput{
		ContractID: in.ContractId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
	})
```

- [ ] **Step 4: Update the handler tests**

In `otc_options_handler_test.go`, every request literal that set `BuyerAccountId`/`SellerAccountId` on `AcceptOTCOfferRequest` or `ExerciseContractRequest`, and every `CreateOTCOfferRequest` literal, changes:
- `CreateOTCOfferRequest`: add `AccountId: <nonzero>`.
- `AcceptOTCOfferRequest`: replace `BuyerAccountId`/`SellerAccountId` with `AccountId: <nonzero>`.
- `ExerciseContractRequest`: remove `BuyerAccountId`/`SellerAccountId`.
Update the stub service's `AcceptInput`/`ExerciseInput`/`CreateOfferInput` assertions to match the new field names.

- [ ] **Step 5: Build and test stock-service**

Run: `cd stock-service && go build ./... && go test ./internal/handler/ ./internal/service/ ./internal/model/`
Expected: builds clean; all tests PASS.

- [ ] **Step 6: Lint and commit**

Run: `cd stock-service && golangci-lint run ./...`
Expected: no new warnings.

```bash
git add stock-service/internal/handler/otc_options_handler.go stock-service/internal/handler/otc_options_handler_test.go
git commit -m "feat(stock-service): OTC handler matches new account-binding proto"
```

---

## Phase 7 — api-gateway: middleware & helpers

### Task 11: `RequirePermissionOrClient` middleware

**Files:**
- Modify: `api-gateway/internal/middleware/auth.go`
- Test: `api-gateway/internal/middleware/auth_test.go` (confirm name; grep `RequireAllPermissions` under `api-gateway/internal/middleware/`)

- [ ] **Step 1: Write the failing test**

Add to the middleware test file (mirror how the existing `RequireAllPermissions`/`RequireAnyPermission` tests build a gin context — they set `permissions` and `principal_type` via `c.Set`):

```go
func TestRequirePermissionOrClient_ClientPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("principal_type", "client")
		c.Set("permissions", []string{})
		c.Next()
	})
	r.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Otc.Trade.Accept))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	require.Equal(t, 200, rec.Code)
}

func TestRequirePermissionOrClient_EmployeeWithoutPermBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("principal_type", "employee")
		c.Set("permissions", []string{})
		c.Next()
	})
	r.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Otc.Trade.Accept))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	require.Equal(t, 403, rec.Code)
}

func TestRequirePermissionOrClient_EmployeeWithPermPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("principal_type", "employee")
		c.Set("permissions", []string{string(perms.Otc.Trade.Accept), string(perms.Securities.Trade.Any)})
		c.Next()
	})
	r.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Otc.Trade.Accept, perms.Securities.Trade.Any))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	require.Equal(t, 200, rec.Code)
}
```

Match the import block to the existing middleware test file (`gin`, `httptest`, `testing`, `require`, `middleware`, `perms`).

- [ ] **Step 2: Run to confirm failure**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRequirePermissionOrClient`
Expected: FAIL — `middleware.RequirePermissionOrClient` / `middleware.PermAll` undefined.

- [ ] **Step 3: Implement the middleware**

In `api-gateway/internal/middleware/auth.go`, after `RequireAllPermissions` (ends at line ~203), add:

```go
// PermMode selects how RequirePermissionOrClient evaluates the permission
// list for employee principals.
type PermMode int

const (
	// PermAll requires the employee to hold every listed permission.
	PermAll PermMode = iota
	// PermAny requires the employee to hold at least one listed permission.
	PermAny
)

// RequirePermissionOrClient gates a route that both clients and employees may
// use. Client principals always pass — their access is constrained by
// resource-ownership checks in the handler, not by permissions. Employee
// principals are still permission-gated: PermAll requires all listed
// permissions, PermAny requires at least one.
func RequirePermissionOrClient(mode PermMode, ps ...perms.Permission) gin.HandlerFunc {
	wants := make([]string, len(ps))
	for i, p := range ps {
		wants[i] = string(p)
	}
	return func(c *gin.Context) {
		if c.GetString("principal_type") == "client" {
			c.Next()
			return
		}
		raw, ok := c.Get("permissions")
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "no permissions")
			return
		}
		have, ok := raw.([]string)
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid permissions format")
			return
		}
		set := make(map[string]bool, len(have))
		for _, p := range have {
			set[p] = true
		}
		switch mode {
		case PermAny:
			for _, w := range wants {
				if set[w] {
					c.Next()
					return
				}
			}
			abortWithError(c, http.StatusForbidden, "forbidden", "insufficient permissions")
		default: // PermAll
			for _, w := range wants {
				if !set[w] {
					abortWithError(c, http.StatusForbidden, "forbidden", "missing permission "+w)
					return
				}
			}
			c.Next()
		}
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRequirePermissionOrClient`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/middleware/auth.go api-gateway/internal/middleware/auth_test.go
git commit -m "feat(api-gateway): RequirePermissionOrClient middleware"
```

### Task 12: `resolveAndCheckAccount` ownership helper

**Files:**
- Modify: `api-gateway/internal/handler/validation.go`
- Test: `api-gateway/internal/handler/validation_test.go` (confirm name; grep `enforceOwnership` under `api-gateway/internal/handler/`)

- [ ] **Step 1: Write the failing test**

The helper takes the resolved identity, an account ID, an on-behalf client ID, and the account-service client; it fetches the account and verifies ownership. Use a stub `accountpb.AccountServiceClient`. Add to the handler test file:

```go
type stubAccountClient struct {
	accountpb.AccountServiceClient
	acct *accountpb.AccountResponse
	err  error
}

func (s *stubAccountClient) GetAccount(ctx context.Context, in *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.acct, nil
}

func TestResolveAndCheckAccount_ClientOwns(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 5, OwnerType: "client", OwnerID: u64ptr(5)}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 5, AccountKind: "current"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 0)
	require.NoError(t, err)
}

func TestResolveAndCheckAccount_ClientDoesNotOwn(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 5, OwnerType: "client", OwnerID: u64ptr(5)}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 999, AccountKind: "current"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 0)
	require.Error(t, err)
	require.Equal(t, 403, rec.Code)
}

func TestResolveAndCheckAccount_EmployeeBankAccount(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 7, OwnerType: "bank"}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 1000000000, AccountKind: "bank"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 0)
	require.NoError(t, err)
}

func TestResolveAndCheckAccount_EmployeeNonBankAccountRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 7, OwnerType: "bank"}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 5, AccountKind: "current"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 0)
	require.Error(t, err)
	require.Equal(t, 403, rec.Code)
}

func TestResolveAndCheckAccount_EmployeeOnBehalf(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 7, OwnerType: "bank"}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 333, AccountKind: "current"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 333)
	require.NoError(t, err)
}

func TestResolveAndCheckAccount_EmployeeOnBehalfWrongClient(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 7, OwnerType: "bank"}
	ac := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 9, OwnerId: 444, AccountKind: "current"}}
	err := handler.ResolveAndCheckAccount(c, ac, id, 9, 333)
	require.Error(t, err)
	require.Equal(t, 403, rec.Code)
}
```

Add a tiny `func u64ptr(v uint64) *uint64 { return &v }` helper to the test file if one is not already present, and ensure imports include `accountpb`, `grpc`, `middleware`, `handler`, `gin`, `httptest`, `require`, `context`.

- [ ] **Step 2: Run to confirm failure**

Run: `cd api-gateway && go test ./internal/handler/ -run TestResolveAndCheckAccount`
Expected: FAIL — `handler.ResolveAndCheckAccount` undefined.

- [ ] **Step 3: Implement the helper**

In `api-gateway/internal/handler/validation.go`, after `enforceOwnership` (ends ~line 126), add. Note this is exported (capitalized) so handler tests in package `handler_test` can call it:

```go
// bankSentinelOwnerID is the owner_id account-service stamps on bank-owned
// accounts (mirrors account-service's is_bank_account sentinel).
const bankSentinelOwnerID uint64 = 1_000_000_000

// ResolveAndCheckAccount fetches the account and verifies it belongs to the
// party the caller is acting as, per the Resource Ownership Verification
// Requirement in CLAUDE.md:
//   - client principal       → account.owner_id == principal_id, not a bank account
//   - employee, no on-behalf → account is a bank account (account_kind == "bank")
//   - employee + on-behalf   → account.owner_id == onBehalfClientID
// On any mismatch it writes a 403 response and returns a non-nil error; the
// caller MUST return immediately. A gRPC failure fetching the account is
// surfaced via handleGRPCError and also returns non-nil.
func ResolveAndCheckAccount(c *gin.Context, accountClient accountpb.AccountServiceClient, id *middleware.ResolvedIdentity, accountID, onBehalfClientID uint64) error {
	if accountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "account_id is required")
		return fmt.Errorf("account_id is zero")
	}
	acct, err := accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		handleGRPCError(c, err)
		return fmt.Errorf("get account %d: %w", accountID, err)
	}
	isBank := acct.AccountKind == "bank" || acct.OwnerId == bankSentinelOwnerID

	switch id.PrincipalType {
	case "client":
		if isBank || acct.OwnerId != id.PrincipalID {
			apiError(c, http.StatusForbidden, ErrForbidden, "account does not belong to you")
			return fmt.Errorf("client %d does not own account %d", id.PrincipalID, accountID)
		}
	case "employee":
		if onBehalfClientID != 0 {
			if isBank || acct.OwnerId != onBehalfClientID {
				apiError(c, http.StatusForbidden, ErrForbidden, "account does not belong to that client")
				return fmt.Errorf("account %d not owned by on-behalf client %d", accountID, onBehalfClientID)
			}
		} else {
			if !isBank {
				apiError(c, http.StatusForbidden, ErrForbidden, "employees may only use bank accounts unless acting on behalf of a client")
				return fmt.Errorf("account %d is not a bank account", accountID)
			}
		}
	default:
		apiError(c, http.StatusForbidden, ErrForbidden, "unknown principal type")
		return fmt.Errorf("unknown principal type %q", id.PrincipalType)
	}
	return nil
}
```

Confirm `validation.go` already imports `net/http`, `fmt`, `accountpb`, and `middleware`. It imports `gin` already; add `accountpb "github.com/exbanka/contract/accountpb"` and `"github.com/exbanka/api-gateway/internal/middleware"` if missing.

- [ ] **Step 4: Run the tests**

Run: `cd api-gateway && go test ./internal/handler/ -run TestResolveAndCheckAccount`
Expected: all six PASS.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/validation.go api-gateway/internal/handler/validation_test.go
git commit -m "feat(api-gateway): ResolveAndCheckAccount ownership helper"
```

---

## Phase 8 — api-gateway: OTC handlers

### Task 13: Wire `securityClient` + `accountClient` into `OTCOptionsHandler`

**Files:**
- Modify: `api-gateway/internal/handler/otc_options_handler.go`
- Modify: `api-gateway/internal/router/handlers.go`

- [ ] **Step 1: Extend the handler struct and constructor**

In `otc_options_handler.go`, currently:

```go
type OTCOptionsHandler struct {
	client  stockpb.OTCOptionsServiceClient
	peerOTC stockpb.PeerOTCServiceClient
}

func NewOTCOptionsHandler(client stockpb.OTCOptionsServiceClient, peerOTC stockpb.PeerOTCServiceClient) *OTCOptionsHandler {
	return &OTCOptionsHandler{client: client, peerOTC: peerOTC}
}
```

Change to:

```go
type OTCOptionsHandler struct {
	client   stockpb.OTCOptionsServiceClient
	peerOTC  stockpb.PeerOTCServiceClient
	security stockpb.SecurityGRPCServiceClient
	accounts accountpb.AccountServiceClient
}

func NewOTCOptionsHandler(
	client stockpb.OTCOptionsServiceClient,
	peerOTC stockpb.PeerOTCServiceClient,
	security stockpb.SecurityGRPCServiceClient,
	accounts accountpb.AccountServiceClient,
) *OTCOptionsHandler {
	return &OTCOptionsHandler{client: client, peerOTC: peerOTC, security: security, accounts: accounts}
}
```

Add `accountpb "github.com/exbanka/contract/accountpb"` to the imports.

- [ ] **Step 2: Update the wiring**

In `api-gateway/internal/router/handlers.go`, line ~169:

```go
		OTCOptions:       handler.NewOTCOptionsHandler(d.OTCOptionsClient, d.PeerOTCClient),
```

Change to:

```go
		OTCOptions:       handler.NewOTCOptionsHandler(d.OTCOptionsClient, d.PeerOTCClient, d.SecurityClient, d.AccountClient),
```

(`Deps.SecurityClient` and `Deps.AccountClient` already exist — confirmed in `handlers.go`.)

- [ ] **Step 3: Build check (will still fail on handler bodies — that is fine)**

Run: `cd api-gateway && go build ./internal/router/ 2>&1 | head`
Expected: the `handlers.go` line compiles; remaining errors are only in `otc_options_handler.go` bodies, fixed in Tasks 14–17. Do not commit yet — commit at the end of Task 17 so the gateway package always builds at a commit boundary.

### Task 14: `CreateOffer` — ticker resolution, account ownership, on-behalf

**Files:**
- Modify: `api-gateway/internal/handler/otc_options_handler.go`
- Test: `api-gateway/internal/handler/otc_options_handler_test.go`

- [ ] **Step 1: Replace the request struct and handler**

Currently `createOTCOfferRequest` and `CreateOffer` are lines 26–81. Replace the struct:

```go
type createOTCOfferRequest struct {
	Direction              string  `json:"direction"`
	Ticker                 string  `json:"ticker"`
	Quantity               string  `json:"quantity"`
	StrikePrice            string  `json:"strike_price"`
	Premium                string  `json:"premium"`
	SettlementDate         string  `json:"settlement_date"`
	AccountID              uint64  `json:"account_id"`
	CounterpartyUserID     *int64  `json:"counterparty_user_id,omitempty"`
	CounterpartySystemType *string `json:"counterparty_system_type,omitempty"`
	OnBehalfOfClientID     uint64  `json:"on_behalf_of_client_id,omitempty"`
}
```

Replace the handler body (keep/adjust the swagger annotation above it — change `@Param body` description and add nothing else):

```go
// CreateOffer godoc
// @Summary      Create an OTC option offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createOTCOfferRequest true "offer details (ticker-keyed; account_id is the initiator's account)"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/otc/offers [post]
func (h *OTCOptionsHandler) CreateOffer(c *gin.Context) {
	var req createOTCOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := oneOf("direction", req.Direction, "sell_initiated", "buy_initiated"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.Ticker == "" || req.Quantity == "" || req.StrikePrice == "" || req.SettlementDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "ticker, quantity, strike_price and settlement_date are required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.AccountID, req.OnBehalfOfClientID); err != nil {
		return
	}
	stock, err := h.security.GetStockByTicker(c.Request.Context(), &stockpb.GetStockByTickerRequest{Ticker: req.Ticker})
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "unknown ticker: "+req.Ticker)
		return
	}
	in := &stockpb.CreateOTCOfferRequest{
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		Direction:          req.Direction,
		StockId:            stock.Id,
		Quantity:           req.Quantity,
		StrikePrice:        req.StrikePrice,
		Premium:            req.Premium,
		SettlementDate:     req.SettlementDate,
		AccountId:          req.AccountID,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	}
	if req.CounterpartyUserID != nil && *req.CounterpartyUserID != 0 {
		stype := "client"
		if req.CounterpartySystemType != nil {
			stype = *req.CounterpartySystemType
		}
		in.Counterparty = &stockpb.PartyRef{UserId: *req.CounterpartyUserID, SystemType: stype}
	}
	resp, err := h.client.CreateOffer(c.Request.Context(), in)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"offer": resp})
}
```

Note: for an employee acting as the bank, `identity.OwnerID` is nil → `ownerToLegacyUserID` returns 0 and `ActorSystemType` is `"bank"`, which is the existing contract. The on-behalf path keeps `OwnerType`/`OwnerID` as bank in the proto but carries `OnBehalfOfClientId` — this matches how `BuyOTCOfferOnBehalf` already works (stock-service resolves the owner from `OnBehalfOfClientId`). If the OTC options service does not yet honour `OnBehalfOfClientId` for owner resolution, that is out of scope for this gateway task; the gateway's job per the spec is the ownership gate, which `ResolveAndCheckAccount` enforces.

- [ ] **Step 2: Update the `CreateOffer` handler tests**

In `otc_options_handler_test.go`, the existing `TestOTCOpt_CreateOffer_Success` posts `{"direction":...,"stock_id":11,...}`. The router helper `otcOptionsRouter` must now also inject an `identity` into the context and provide stub security + account clients. Update the stub setup:
- Extend `stubOTCOptionsClient` is unchanged; add a `stubSecurityClient` with a `getByTickerFn` and a `stubAccountClient` (reuse the one from Task 12 if in the same package).
- The `otcOptionsRouter` test helper must register a middleware that sets `principal_type`, `principal_id`, and `identity` (a `*middleware.ResolvedIdentity`). Add that to the helper.
- Change the request body from `"stock_id":11` to `"ticker":"AAPL","account_id":50`.
- The `stubSecurityClient.getByTickerFn` returns `&stockpb.StockDetail{Id: 11}`.
- The `stubAccountClient` returns an account with `OwnerId` matching the test client's principal id and `AccountKind:"current"`.

Add a failing-path test:

```go
func TestOTCOpt_CreateOffer_UnknownTicker(t *testing.T) {
	sec := &stubSecurityClient{getByTickerFn: func(*stockpb.GetStockByTickerRequest) (*stockpb.StockDetail, error) {
		return nil, status.Error(codes.NotFound, "no stock")
	}}
	acct := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 50, OwnerId: 42, AccountKind: "current"}}
	r := otcOptionsRouterFull(handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}, sec, acct), 42, "client")
	body := `{"direction":"sell_initiated","ticker":"NOPE","quantity":"1","strike_price":"5","premium":"1","settlement_date":"2026-12-31","account_id":50}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers", strings.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

`otcOptionsRouterFull(handler, principalID, principalType)` is a new variant of the existing `otcOptionsRouter` helper that also injects identity context — add it to the test file's helper section.

- [ ] **Step 3: Run the CreateOffer tests**

Run: `cd api-gateway && go test ./internal/handler/ -run TestOTCOpt_CreateOffer`
Expected: PASS (after Tasks 15–17 the package fully builds; if it does not build yet because Counter/Accept/Exercise still reference old fields, finish Tasks 15–17 then re-run).

### Task 15: `CounterOffer` — on-behalf support

**Files:**
- Modify: `api-gateway/internal/handler/otc_options_handler.go`
- Test: `api-gateway/internal/handler/otc_options_handler_test.go`

- [ ] **Step 1: Add `on_behalf_of_client_id` to the request struct and pass it through**

`counterOTCOfferRequest` becomes:

```go
type counterOTCOfferRequest struct {
	Quantity           string `json:"quantity"`
	StrikePrice        string `json:"strike_price"`
	Premium            string `json:"premium"`
	SettlementDate     string `json:"settlement_date"`
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
}
```

In `CounterOffer`, the gRPC request literal becomes:

```go
	resp, err := h.client.CounterOffer(c.Request.Context(), &stockpb.CounterOTCOfferRequest{
		OfferId:            id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		Quantity:           req.Quantity, StrikePrice: req.StrikePrice, Premium: req.Premium,
		SettlementDate:     req.SettlementDate,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	})
```

No account check here — counters bind no account (see the design deviation note at the top of this plan).

- [ ] **Step 2: Update the `CounterOffer` test**

The existing counter test's stub assertion stays valid; just confirm the request still binds. If a test asserts on request fields, add `OnBehalfOfClientId` where relevant. No new behaviour test needed for counter beyond confirming it still returns 200.

### Task 16: `AcceptOffer` — single account, ownership, on-behalf

**Files:**
- Modify: `api-gateway/internal/handler/otc_options_handler.go`
- Test: `api-gateway/internal/handler/otc_options_handler_test.go`

- [ ] **Step 1: Replace the request struct and handler**

`acceptOTCOfferRequest` becomes:

```go
type acceptOTCOfferRequest struct {
	AccountID          uint64 `json:"account_id"`
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
}
```

`AcceptOffer` body becomes (keep the swagger block, adjust the `@Param body` text to "acceptor's account id"):

```go
func (h *OTCOptionsHandler) AcceptOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req acceptOTCOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.AccountID, req.OnBehalfOfClientID); err != nil {
		return
	}
	resp, err := h.client.AcceptOffer(c.Request.Context(), &stockpb.AcceptOTCOfferRequest{
		OfferId:            id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		AccountId:          req.AccountID,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
```

- [ ] **Step 2: Update the `AcceptOffer` tests**

Change every accept test body from `{"buyer_account_id":..,"seller_account_id":..}` to `{"account_id":..}`, run through `otcOptionsRouterFull`, and provide a `stubAccountClient` whose account `OwnerId` matches the test principal. Add an ownership-failure test:

```go
func TestOTCOpt_AcceptOffer_AccountNotOwned(t *testing.T) {
	acct := &stubAccountClient{acct: &accountpb.AccountResponse{Id: 50, OwnerId: 999, AccountKind: "current"}}
	r := otcOptionsRouterFull(handler.NewOTCOptionsHandler(&stubOTCOptionsClient{}, &stubPeerOTCExerciseClient{}, &stubSecurityClient{}, acct), 42, "client")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/otc/offers/1/accept", strings.NewReader(`{"account_id":50}`)))
	require.Equal(t, http.StatusForbidden, rec.Code)
}
```

### Task 17: `ExerciseContract` — drop account IDs, on-behalf

**Files:**
- Modify: `api-gateway/internal/handler/otc_options_handler.go`
- Test: `api-gateway/internal/handler/otc_options_handler_test.go`

- [ ] **Step 1: Replace the request struct and handler**

`exerciseRequest` becomes:

```go
type exerciseRequest struct {
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
}
```

`ExerciseContract` body becomes (adjust the swagger `@Param body` text to "optional on-behalf client id"; accounts now come from the contract):

```go
func (h *OTCOptionsHandler) ExerciseContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req exerciseRequest
	// Body is optional — only on_behalf_of_client_id may be present.
	_ = c.ShouldBindJSON(&req)
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ExerciseContract(c.Request.Context(), &stockpb.ExerciseContractRequest{
		ContractId:         id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
```

No `ResolveAndCheckAccount` call — exercise binds no caller-supplied account; the contract's stored accounts are used and stock-service already enforces buyer-only via the owner identity.

- [ ] **Step 2: Update the `ExerciseContract` tests**

Drop `buyer_account_id`/`seller_account_id` from every exercise test body (use `{}` or no body), route through `otcOptionsRouterFull`. The existing success test should still expect 201.

- [ ] **Step 3: Build, test, lint the api-gateway handler package**

Run: `cd api-gateway && go build ./... && go test ./internal/handler/ ./internal/middleware/`
Expected: builds clean; all handler + middleware tests PASS.

Run: `cd api-gateway && golangci-lint run ./internal/handler/ ./internal/middleware/`
Expected: no new warnings.

- [ ] **Step 4: Commit Tasks 13–17 together**

```bash
git add api-gateway/internal/handler/otc_options_handler.go api-gateway/internal/handler/otc_options_handler_test.go api-gateway/internal/router/handlers.go
git commit -m "feat(api-gateway): OTC option handlers — ticker, ownership checks, on-behalf"
```

### Task 18: `MakePublic` + `BuyOTCOffer` holding/account ownership

**Files:**
- Modify: `api-gateway/internal/handler/portfolio_handler.go`
- Modify: `stock-service/internal/service/portfolio_service.go` (or wherever `MakePublic` lives — grep `func.*MakePublic` under `stock-service/internal/service/`)
- Test: `api-gateway/internal/handler/portfolio_handler_test.go`, the stock-service portfolio service test

- [ ] **Step 1: Confirm/add the holding-owner check in stock-service `MakePublic`**

Grep `func (s *PortfolioService) MakePublic` (or similar) in stock-service. Read it. It receives `(holdingID, ownerType, ownerID, quantity)`. Verify it loads the holding and rejects when `holding.OwnerType != ownerType || holding.OwnerID != ownerID`. If that check is **missing**, add it at the top of the method:

```go
	holding, err := s.holdings.GetByID(holdingID)
	if err != nil {
		return nil, err
	}
	if holding.OwnerType != ownerType || !ownerIDEqual(holding.OwnerID, ownerID) {
		return nil, errors.New("holding does not belong to caller")
	}
```

Match the actual repository accessor name (`s.holdings.GetByID` or equivalent — read the struct). Write a stock-service unit test `TestPortfolioService_MakePublic_RejectsForeignHolding` feeding a holding owned by a different `(ownerType, ownerID)` and asserting an error. If the check already exists, just add the test to lock it in.

- [ ] **Step 2: `BuyOTCOffer` — confirm ownership still enforced**

`BuyOTCOffer` already calls `enforceOwnership(c, acctResp.OwnerId)` after `GetAccount`. With the route moving to `AnyAuthMiddleware` (Task 19), `enforceOwnership` still does the right thing for clients (employees bypass it, but employees on this route act as the bank via `OwnerIsBankIfEmployee` and the existing `buy-on-behalf` route covers the on-behalf path). **No code change** to `BuyOTCOffer` is required — but add a regression test confirming a client buying with another client's account gets 404:

```go
func TestBuyOTCOffer_ForeignAccountRejected(t *testing.T) {
	// Build PortfolioHandler with a stub accountClient returning OwnerId=999;
	// route with identity principal_type=client, principal_id=42.
	// Expect 404 (enforceOwnership leaks nothing).
}
```

Fill in the construction using the existing `portfolio_handler_test.go` patterns.

- [ ] **Step 3: Build, test, lint**

Run: `cd api-gateway && go test ./internal/handler/ -run 'MakePublic|BuyOTCOffer'`
Run: `cd stock-service && go test ./internal/service/ -run MakePublic`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/handler/portfolio_handler.go api-gateway/internal/handler/portfolio_handler_test.go stock-service/internal/service/
git commit -m "feat: enforce holding/account ownership on OTC make-public and buy"
```

---

## Phase 9 — api-gateway: router

### Task 19: Swap the OTC route middleware

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Update the public-market buy group**

Currently (lines ~226–231):

```go
	otcTrade := v3.Group("/otc/offers")
	otcTrade.Use(middleware.AuthMiddleware(h.Auth.Client()))
	otcTrade.Use(middleware.RequireAnyPermission(perms.Otc.Trade.Accept, perms.Securities.Trade.Any))
	otcTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcTrade.POST("/:id/buy", h.Portfolio.BuyOTCOffer)
	}
```

Change to:

```go
	otcTrade := v3.Group("/otc/offers")
	otcTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcTrade.Use(middleware.RequirePermissionOrClient(middleware.PermAny, perms.Otc.Trade.Accept, perms.Securities.Trade.Any))
	otcTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcTrade.POST("/:id/buy", h.Portfolio.BuyOTCOffer)
	}
```

- [ ] **Step 2: Update the OTC options trading group**

Currently (lines ~241–252):

```go
	otcOptionsTrade := v3.Group("/otc")
	otcOptionsTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcOptionsTrade.Use(middleware.RequireAllPermissions(perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
	otcOptionsTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcOptionsTrade.POST("/offers", h.OTCOptions.CreateOffer)
		otcOptionsTrade.POST("/offers/:id/counter", h.OTCOptions.CounterOffer)
		otcOptionsTrade.POST("/offers/:id/accept", h.OTCOptions.AcceptOffer)
		otcOptionsTrade.POST("/offers/:id/reject", h.OTCOptions.RejectOffer)
		otcOptionsTrade.POST("/contracts/:id/exercise", h.OTCOptions.ExerciseContract)
	}
```

Change only the permission middleware line:

```go
	otcOptionsTrade := v3.Group("/otc")
	otcOptionsTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcOptionsTrade.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
	otcOptionsTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcOptionsTrade.POST("/offers", h.OTCOptions.CreateOffer)
		otcOptionsTrade.POST("/offers/:id/counter", h.OTCOptions.CounterOffer)
		otcOptionsTrade.POST("/offers/:id/accept", h.OTCOptions.AcceptOffer)
		otcOptionsTrade.POST("/offers/:id/reject", h.OTCOptions.RejectOffer)
		otcOptionsTrade.POST("/contracts/:id/exercise", h.OTCOptions.ExerciseContract)
	}
```

- [ ] **Step 3: Migrate `/buy-on-behalf` to the new permission**

Currently (lines ~720–726):

```go
	otcOnBehalf := protected.Group("/otc/offers")
	otcOnBehalf.Use(middleware.RequireAnyPermission(
		perms.Otc.Trade.Accept, perms.Orders.Place.OnBehalfClient))
	otcOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcOnBehalf.POST("/:id/buy-on-behalf", h.Portfolio.BuyOTCOfferOnBehalf)
	}
```

Change the permission line to use the namespaced permission:

```go
	otcOnBehalf := protected.Group("/otc/offers")
	otcOnBehalf.Use(middleware.RequireAnyPermission(
		perms.Otc.Trade.Accept, perms.Otc.Trade.OnBehalf))
	otcOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcOnBehalf.POST("/:id/buy-on-behalf", h.Portfolio.BuyOTCOfferOnBehalf)
	}
```

- [ ] **Step 4: Build the whole gateway**

Run: `cd api-gateway && go build ./...`
Expected: builds clean.

- [ ] **Step 5: Full gateway + stock-service build and unit tests**

Run: `make build`
Expected: all services compile (the `swag init` step runs as part of `make build`).

Run: `cd api-gateway && go test ./... && cd ../stock-service && go test ./...`
Expected: all unit tests PASS.

- [ ] **Step 6: Lint**

Run: `cd api-gateway && golangci-lint run ./... && cd ../stock-service && golangci-lint run ./...`
Expected: no new warnings.

- [ ] **Step 7: Commit**

```bash
git add api-gateway/internal/router/router_v3.go
git commit -m "feat(api-gateway): open OTC routes to clients via RequirePermissionOrClient"
```

---

## Phase 10 — Integration tests

> These run against the docker-compose stack. Start it once: `make docker-up`, wait for healthy, then run the integration suite per the repo's usual `make test-integration` (or the documented command). Each task below adds tests to `test-app/workflows/otc_options_test.go`.

### Task 20: Client OTC option lifecycle + replace the obsolete test

**Files:**
- Modify: `test-app/workflows/otc_options_test.go`

- [ ] **Step 1: Delete `TestOTCOptions_ClientCannotTrade`**

Remove the whole `TestOTCOptions_ClientCannotTrade` function (lines ~72–96) — its premise is now false.

- [ ] **Step 2: Add the client lifecycle test**

```go
// TestOTCOptions_ClientLifecycle drives a full OTC option negotiation with
// two clients: client A (seller) creates a sell_initiated offer by ticker,
// client B (buyer) accepts it, then B exercises the resulting contract.
func TestOTCOptions_ClientLifecycle(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerC, _ := setupActivatedClient(t, adminC)

	// Seller needs a stock holding + an account; buyer needs an account.
	sellerAcct := createClientAccount(t, adminC, sellerID, "RSD", 100000)
	buyerAcct := createClientAccount(t, adminC, buyerID, "RSD", 100000)
	ticker := seedStockHolding(t, adminC, sellerID, 100) // helper: ensures a stock + holding

	// Seller creates a sell_initiated offer keyed by ticker.
	createResp, err := sellerC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction":       "sell_initiated",
		"ticker":          ticker,
		"quantity":        "10",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
		"account_id":      sellerAcct,
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// Buyer accepts with their own account.
	acceptResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/offers/%d/accept", offerID), map[string]interface{}{
		"account_id": buyerAcct,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	helpers.RequireStatus(t, acceptResp, 201)
	contractID := int(helpers.GetNumberField(t, acceptResp, "contract_id"))

	// Buyer exercises the contract (no account body — accounts come from the contract).
	exResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/contracts/%d/exercise", contractID), map[string]interface{}{})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	helpers.RequireStatus(t, exResp, 201)
}
```

If `createClientAccount`, `seedStockHolding`, `GetNestedNumberField` do not already exist in `helpers_test.go`, add them there (small wrappers around the admin `POST /api/v3/accounts`, the stock-seeding endpoint, and JSON traversal — follow the existing helper style). Reuse existing helpers wherever they already cover this.

- [ ] **Step 3: Run it**

Run the integration suite filtered to `TestOTCOptions_ClientLifecycle`.
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/otc_options_test.go test-app/workflows/helpers_test.go
git commit -m "test(otc): client OTC option lifecycle integration test"
```

### Task 21: Ownership-violation + on-behalf integration tests

**Files:**
- Modify: `test-app/workflows/otc_options_test.go`

- [ ] **Step 1: Add the ownership + on-behalf tests**

```go
// TestOTCOptions_ClientCannotUseForeignAccount: a client creating an offer
// with an account owned by a different client gets 403.
func TestOTCOptions_ClientCannotUseForeignAccount(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	attackerID, _, attackerC, _ := setupActivatedClient(t, adminC)
	victimID, _, _, _ := setupActivatedClient(t, adminC)
	_ = attackerID
	victimAcct := createClientAccount(t, adminC, victimID, "RSD", 100000)
	ticker := seedStockHolding(t, adminC, victimID, 100)

	resp, err := attackerC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker,
		"quantity": "1", "strike_price": "100", "premium": "5",
		"settlement_date": "2030-12-31", "account_id": victimAcct,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

// TestOTCOptions_EmployeeOnBehalf: an employee with otc.trade.on_behalf
// creates an offer for a client using that client's account.
func TestOTCOptions_EmployeeOnBehalf(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, _, _ := setupActivatedClient(t, adminC)
	clientAcct := createClientAccount(t, adminC, clientID, "RSD", 100000)
	ticker := seedStockHolding(t, adminC, clientID, 100)

	// adminC is EmployeeAdmin → has otc.trade.on_behalf transitively.
	resp, err := adminC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker,
		"quantity": "1", "strike_price": "100", "premium": "5",
		"settlement_date": "2030-12-31",
		"account_id":             clientAcct,
		"on_behalf_of_client_id": clientID,
	})
	if err != nil {
		t.Fatalf("create on-behalf: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

// TestOTCOptions_EmployeeBankAccountOnly: an employee NOT acting on behalf
// must use a bank account; a client account is rejected.
func TestOTCOptions_EmployeeBankAccountOnly(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, _, _ := setupActivatedClient(t, adminC)
	clientAcct := createClientAccount(t, adminC, clientID, "RSD", 100000)
	ticker := seedStockHolding(t, adminC, clientID, 100)

	resp, err := adminC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker,
		"quantity": "1", "strike_price": "100", "premium": "5",
		"settlement_date": "2030-12-31",
		"account_id": clientAcct, // client account, no on_behalf → 403
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}
```

- [ ] **Step 2: Run them**

Run the integration suite filtered to `TestOTCOptions_ClientCannotUseForeignAccount|TestOTCOptions_EmployeeOnBehalf|TestOTCOptions_EmployeeBankAccountOnly`.
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/otc_options_test.go
git commit -m "test(otc): ownership-violation and on-behalf integration tests"
```

### Task 22: Ticker-based create test

**Files:**
- Modify: `test-app/workflows/otc_options_test.go`

- [ ] **Step 1: Add the unknown-ticker test**

```go
// TestOTCOptions_UnknownTickerRejected: an unknown ticker → 400.
func TestOTCOptions_UnknownTickerRejected(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)
	clientAcct := createClientAccount(t, adminC, clientID, "RSD", 100000)

	resp, err := clientC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction": "sell_initiated", "ticker": "ZZ_NOT_A_TICKER",
		"quantity": "1", "strike_price": "100", "premium": "5",
		"settlement_date": "2030-12-31", "account_id": clientAcct,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}
```

(The valid-ticker path is already exercised by `TestOTCOptions_ClientLifecycle`.)

- [ ] **Step 2: Run it**

Run the integration suite filtered to `TestOTCOptions_UnknownTickerRejected`.
Expected: PASS.

- [ ] **Step 3: Run the full OTC integration file**

Run the integration suite filtered to `TestOTCOptions`.
Expected: every `TestOTCOptions_*` PASSES.

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/otc_options_test.go
git commit -m "test(otc): ticker-based create integration test"
```

---

## Phase 11 — Documentation

### Task 23: Update Specification.md, REST_API_v3.md, swagger

**Files:**
- Modify: `Specification.md`
- Modify: `docs/api/REST_API_v3.md`
- Regenerate: `api-gateway/docs/*`

- [ ] **Step 1: `Specification.md`**

- §6 (permissions): add `otc.trade.on_behalf` — "allows an employee to act on behalf of a client on OTC routes"; note it is granted to `EmployeeAgent` and above.
- §17 (API routes): for `POST /api/v3/otc/offers`, `/counter`, `/accept`, `/contracts/:id/exercise`, `/otc/offers/:id/buy` — document that clients may now call them; document the new request fields (`ticker`, `account_id`, `on_behalf_of_client_id`) and that `AcceptOffer` takes a single `account_id`, `ExerciseContract` takes no account.
- §18 (entities): `OTCOffer` gains `initiator_account_id`; `OptionContract` gains `buyer_account_id` + `seller_account_id`.
- §21 (business rules): add the ownership rule — each party binds only their own account; the counterparty account is read from the persisted offer; employees must use bank accounts unless acting on behalf of a client.

- [ ] **Step 2: `docs/api/REST_API_v3.md`**

In the OTC section (around lines 5497–5841), update each affected endpoint's request body, examples, and response codes:
- `POST /api/v3/otc/offers`: `ticker` replaces `stock_id`; add `account_id` (required) and `on_behalf_of_client_id` (optional); add `400 unknown ticker` and `403 account not owned`.
- `POST /api/v3/otc/offers/:id/counter`: add optional `on_behalf_of_client_id`.
- `POST /api/v3/otc/offers/:id/accept`: body is now `{ "account_id": N, "on_behalf_of_client_id": N? }` — remove `buyer_account_id`/`seller_account_id`; add `403`.
- `POST /api/v3/otc/contracts/:id/exercise`: body is now optional `{ "on_behalf_of_client_id": N? }` — remove the account IDs.
- Note on each: clients may call these; employees are still permission-gated.

- [ ] **Step 3: Regenerate swagger**

Run: `make swagger`
Expected: `api-gateway/docs/` regenerates; `git diff` reflects the updated OTC handler annotations from Tasks 14–17.

- [ ] **Step 4: Commit**

```bash
git add Specification.md docs/api/REST_API_v3.md api-gateway/docs/
git commit -m "docs(otc): client OTC access, ticker-keyed offers, ownership rules"
```

---

## Final verification

- [ ] **Step 1: Full build**

Run: `make build`
Expected: all services compile, swagger generates.

- [ ] **Step 2: Full unit test sweep**

Run: `make test`
Expected: all unit tests across all services PASS.

- [ ] **Step 3: Lint**

Run: `make lint`
Expected: no new warnings in any service touched by this plan.

- [ ] **Step 4: Integration suite**

Run the full integration suite (`make test-integration` or the documented equivalent) against `make docker-up`.
Expected: green, including all `TestOTCOptions_*`.

---

## Self-Review Notes

- **Spec coverage:** Section 1 (routes & gating) → Tasks 11, 19. Section 2 (ownership + account binding) → Tasks 5–10, 12, 14–18. Section 3 (ticker swap) → Tasks 2–4, 14. Section 4 (testing) → Tasks 3–22 (unit) + 20–22 (integration). Docs → Task 23. CLAUDE.md ownership requirement was already added during brainstorming.
- **Deviation logged:** `CounterOffer` carries no `account_id` (see the note under the plan header) — Task 15 reflects this; the spec's Section 2 table should be read with that correction. Update the spec's Section 2 table when convenient, or leave the deviation note as the record.
- **Type consistency:** `RequirePermissionOrClient(mode, ...)` + `PermAll`/`PermAny` (Task 11) are used verbatim in Task 19. `ResolveAndCheckAccount(c, accountClient, identity, accountID, onBehalfClientID)` (Task 12) is called with that exact signature in Tasks 14, 16. `AcceptInput.AcceptorAccountID`, `CreateOfferInput.InitiatorAccountID`, `ExerciseInput` (no account fields) defined in Tasks 7–9 match the stock-service handler updates in Task 10. Proto field names `AccountId`, `OnBehalfOfClientId`, `GetStockByTickerRequest.Ticker` (Task 2) match every gateway/handler call site.
- **Commit boundaries:** Phases 1–7 each end at a compiling state. Phase 8 intentionally defers the gateway commit to the end of Task 17 (handler bodies and constructor must land together); Task 13's wiring change alone would not compile.
