# Stock Data Source Abstraction + v2 Options API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a pluggable stock-data `Source` interface with three implementations (external providers, deterministic generated, Market-Simulator HTTP), an admin switch endpoint that wipes-and-reseeds, a v2 router with two new option routes (create order + exercise) and a v1-fallback, the `securities.manage` permission, and the `API Versioning Compatibility Requirement` rule in `CLAUDE.md`. Plus a read-only audit of the buy→portfolio flow.

**Architecture:** A new `stock-service/internal/source/` package defines `Source` with `FetchExchanges/Stocks/Futures/Forex/Options` and `RefreshPrices`. `SecuritySyncService` holds a single `Source` behind a mutex. Three implementations: `external_source` wraps existing providers, `generated_source` produces deterministic local data with a random-walk refresh, `simulator_source` self-registers with Market-Simulator (`POST /api/banks/register`), persists the API key in `system_settings`, and runs a 3s price refresh goroutine while active. Admin-only `POST /api/v1/admin/stock-source` wipes stocks/listings/options/futures/forex/exchanges + orders/holdings/capital_gains/tax_collections/order_transactions, then reseeds from the new source. Options get full `listings` rows (`security_type='option'`) so v2 orders target them like any other security — `Option` gains an optional `listing_id` (additive, non-breaking for v1). v2 router is a new file; a single `NoRoute` handler rewrites unknown `/api/v2/*` paths to `/api/v1/*` via `HandleContext`.

**Tech Stack:** Go, GORM, PostgreSQL, Gin, gRPC/protobuf, Kafka, Redis, `shopspring/decimal`, `swaggo`, existing `test-app` integration harness.

---

## Conventions for this plan

- Every file path is absolute from the repo root `EXBanka-1-Backend/`.
- `<svc>/…` means "inside the given service directory".
- Commits use Conventional Commits (`feat:`, `refactor:`, `test:`, `docs:`, `chore:`).
- After every code-touching task: `make lint` on affected services. If lint touches files you didn't modify, do not fix them — leave them.
- After each phase: `make test` must still pass.
- **TDD discipline:** for business-logic tasks (source implementations, switch orchestration, handlers) write the failing test first, run it to confirm failure, implement, run to confirm pass. For pure scaffolding (proto regen, main.go wiring, docker-compose) a "verify compile + smoke test" step replaces the failing-test step.

---

## Phase 0 — Groundwork

### Task 0.1: Add API versioning rule to CLAUDE.md

**Files:**
- Modify: `CLAUDE.md` (insert new section immediately before `## Implementation Plans`)

- [ ] **Step 1: Open CLAUDE.md and find the "Implementation Plans" heading.**

Locate the exact line `## Implementation Plans` in `CLAUDE.md`. The new section goes *directly above* it.

- [ ] **Step 2: Insert the new section verbatim.**

Insert this block above `## Implementation Plans`:

```markdown
## API Versioning Compatibility Requirement

**Newer versions of the API must never break older versions unless the user has explicitly permitted it.** This is a hard requirement — not optional.

- When introducing `/api/v{N+1}/...` routes, existing `/api/v{N}/...` routes must remain functional with their exact current request and response shapes.
- Adding new *optional* fields to v{N} response bodies is allowed and does not count as a breaking change, provided existing clients that ignore unknown fields continue to work.
- Removing fields, renaming fields, changing field types, changing HTTP methods, changing status codes, changing authentication requirements, or tightening validation on existing v{N} routes are breaking changes and require explicit user authorization before merging.
- The v2 router in api-gateway transparently falls back to v1 for any route not explicitly redefined at v2, so v2 clients can reach any v1 endpoint using a v2 URL.
- When in doubt, ask the user before changing any existing route.

```

- [ ] **Step 3: Commit.**

```bash
git add CLAUDE.md
git commit -m "docs: add API versioning compatibility requirement to CLAUDE.md"
```

---

### Task 0.2: Portfolio audit (read-only, no code changes)

**Files:** none modified. Output is a findings block appended to this plan as a comment-only PR comment or pasted into the implementing agent's summary. Goal: confirm that `direction=buy` on a stock or futures order results in a `holdings` row visible to the user via `GET /api/v1/me/portfolio`.

- [ ] **Step 1: Read the order execution path.**

```bash
# Read these files end-to-end and note their behavior:
#   stock-service/internal/service/order_execution.go
#   stock-service/internal/service/order_service.go
#   stock-service/internal/repository/holding_repository.go
#   stock-service/internal/service/portfolio_service.go
#   api-gateway/internal/handler/portfolio_handler.go
```

- [ ] **Step 2: Trace the buy path.**

Follow `CreateOrder` through:
1. Which service method approves+executes the order.
2. Which repository method upserts the `holdings` row (look for `UpsertByUserAndListing` or similar; aggregation key is `(user_id, security_type, security_id, account_id)` per `model/holding.go:10`).
3. Whether the execution path distinguishes `buy` vs `sell` and whether futures follow the same path as stocks.

- [ ] **Step 3: Trace the portfolio read path.**

Follow `GET /api/v1/me/portfolio`:
1. `PortfolioHandler.ListMyPortfolio` → extracts `user_id` from JWT.
2. `portfolioClient.ListMyHoldings` gRPC call.
3. Stock-service handler → `PortfolioService.ListMyHoldings(user_id)` → `HoldingRepository.ListByUser(user_id)`.
4. Confirm the returned list includes whatever was written in Step 2.

- [ ] **Step 4: Write findings into the implementing agent's summary.**

Report three answers:
- **Does buying a stock create a holding row?** (yes / no + file:line citation)
- **Does buying a futures contract create a holding row?** (yes / no + file:line citation)
- **Does `GET /api/v1/me/portfolio` return newly-created holdings?** (yes / no + file:line citation)

If any answer is **no**, STOP and raise the bug to the user before writing any code. Do not fix it as part of this plan without explicit user authorization.

- [ ] **Step 5: No commit (read-only audit).**

---

### Task 0.3: Add `securities.manage` permission constant and seed

**Files:**
- Modify: `user-service/internal/service/role_service.go` (add to `EmployeeAdmin` seed list)
- Test: `user-service/internal/service/role_service_test.go`

- [ ] **Step 1: Read the existing role seed.**

```bash
# Identify where the EmployeeAdmin permission list is defined in role_service.go.
# Look for a slice literal with "employees.create" etc.
```

- [ ] **Step 2: Write a failing test asserting EmployeeAdmin seed contains securities.manage.**

Open `user-service/internal/service/role_service_test.go`. Find the existing test that asserts the `EmployeeAdmin` seed list (or add a new test if none). Add:

```go
func TestSeedRoles_EmployeeAdminHasSecuritiesManage(t *testing.T) {
    perms := permissionsForRole("EmployeeAdmin")
    require.Contains(t, perms, "securities.manage", "EmployeeAdmin must have securities.manage permission")
}
```

If `permissionsForRole` does not exist as a test helper, find the slice directly via the service constructor and assert against it instead. Adapt the test to whatever pattern already exists in the file — do not invent new infrastructure.

- [ ] **Step 3: Run the test to verify it fails.**

```bash
cd user-service && go test ./internal/service/ -run TestSeedRoles_EmployeeAdminHasSecuritiesManage -v
```

Expected: FAIL with "does not contain securities.manage".

- [ ] **Step 4: Add the permission to the EmployeeAdmin seed list.**

In `user-service/internal/service/role_service.go`, find the `EmployeeAdmin` permission slice literal. Append `"securities.manage"` to it. Keep alphabetical/grouped order if one exists.

- [ ] **Step 5: Run the test again to verify it passes.**

```bash
cd user-service && go test ./internal/service/ -run TestSeedRoles_EmployeeAdminHasSecuritiesManage -v
```

Expected: PASS.

- [ ] **Step 6: Run full user-service tests to check no regressions.**

```bash
cd user-service && go test ./... -count=1
```

- [ ] **Step 7: Lint.**

```bash
cd user-service && golangci-lint run ./...
```

- [ ] **Step 8: Commit.**

```bash
git add user-service/internal/service/role_service.go user-service/internal/service/role_service_test.go
git commit -m "feat(user-service): add securities.manage permission to EmployeeAdmin seed"
```

---

## Phase 1 — Option-listing unification (non-breaking v1 change)

### Task 1.1: Add nullable `ListingID` to `Option` model

**Files:**
- Modify: `stock-service/internal/model/option.go`

- [ ] **Step 1: Open `stock-service/internal/model/option.go` and add a nullable `ListingID`.**

Replace the current `Option` struct body with:

```go
type Option struct {
    ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
    Ticker            string          `gorm:"uniqueIndex;size:30;not null" json:"ticker"`
    Name              string          `gorm:"not null" json:"name"`
    StockID           uint64          `gorm:"index;not null" json:"stock_id"`
    Stock             Stock           `gorm:"foreignKey:StockID" json:"-"`
    OptionType        string          `gorm:"size:4;not null" json:"option_type"` // "call" or "put"
    StrikePrice       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"strike_price"`
    ImpliedVolatility decimal.Decimal `gorm:"type:numeric(10,6);not null;default:1" json:"implied_volatility"`
    Premium           decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"premium"`
    OpenInterest      int64           `gorm:"not null;default:0" json:"open_interest"`
    SettlementDate    time.Time       `gorm:"not null;index" json:"settlement_date"`
    ListingID         *uint64         `gorm:"index" json:"listing_id,omitempty"`
    Version           int64           `gorm:"not null;default:1" json:"-"`
    CreatedAt         time.Time       `json:"created_at"`
    UpdatedAt         time.Time       `json:"updated_at"`
}
```

- [ ] **Step 2: Verify the service compiles.**

```bash
cd stock-service && go build ./...
```

Expected: no compilation errors. GORM's `AutoMigrate` on startup will add the column automatically.

- [ ] **Step 3: Commit.**

```bash
git add stock-service/internal/model/option.go
git commit -m "feat(stock-service): add optional ListingID to Option model"
```

---

### Task 1.2: Add optional `listing_id` to `OptionItem` and `OptionDetail` protos

**Files:**
- Modify: `contract/proto/stock/stock.proto`

- [ ] **Step 1: Read the current proto messages around options.**

```bash
# Find `message OptionItem` and `message OptionDetail` in contract/proto/stock/stock.proto.
# Their current fields run 1-9 / 1-11; we will append a new optional field.
```

- [ ] **Step 2: Add `optional uint64 listing_id` to both messages.**

In `contract/proto/stock/stock.proto`, after the last field of `OptionItem`, append:

```protobuf
  optional uint64 listing_id = 10;
```

(Adjust the number to be one more than the current last field. Do the same for `OptionDetail` — pick the next unused field number.)

- [ ] **Step 3: Regenerate protobuf Go code.**

```bash
make proto
```

Expected: `contract/stockpb/stock.pb.go` is rewritten. No errors.

- [ ] **Step 4: Verify stock-service and api-gateway still compile.**

```bash
cd stock-service && go build ./... && cd ../api-gateway && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit.**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/stock.pb.go
git commit -m "feat(contract): add optional listing_id to OptionItem and OptionDetail"
```

---

### Task 1.3: Populate option listings on seed

**Files:**
- Modify: `stock-service/internal/service/security_sync.go` (the `generateAllOptions` function)
- Modify: `stock-service/internal/service/listing_service.go` (or wherever listing creation lives — check and choose the right place)
- Test: `stock-service/internal/service/security_sync_test.go`

- [ ] **Step 1: Read the current `generateAllOptions` implementation.**

```bash
# stock-service/internal/service/security_sync.go  — lines 580-610
```

Note: after `s.optionRepo.UpsertByTicker(&opt)`, we need to create a `listings` row for the option on the same exchange as its underlying stock.

- [ ] **Step 2: Write a failing test: when `generateAllOptions` runs, every option row has a non-null `ListingID`, and a matching `listings` row exists with `security_type='option'` pointing to the same `exchange_id` as the underlying stock's listing.**

Add to `stock-service/internal/service/security_sync_test.go` (create the file if it does not exist):

```go
func TestGenerateAllOptions_AttachesListingToEveryOption(t *testing.T) {
    db := setupTestDB(t) // existing helper; adapt to project conventions
    stockRepo := repository.NewStockRepository(db)
    optionRepo := repository.NewOptionRepository(db)
    listingRepo := repository.NewListingRepository(db)
    exchangeRepo := repository.NewExchangeRepository(db)

    // Seed one exchange, one stock, one listing for the stock.
    ex := &model.StockExchange{Acronym: "NYSE", MICCode: "XNYS", Name: "NYSE"}
    require.NoError(t, exchangeRepo.Create(ex))

    stock := &model.Stock{Ticker: "AAPL", Name: "Apple", Price: decimal.NewFromInt(180)}
    require.NoError(t, stockRepo.UpsertByTicker(stock))

    stockListing := &model.Listing{
        SecurityID:   stock.ID,
        SecurityType: "stock",
        ExchangeID:   ex.ID,
        Price:        stock.Price,
    }
    require.NoError(t, listingRepo.Create(stockListing))

    svc := service.NewSecuritySyncService( /* wire with real repos */ )
    svc.GenerateAllOptionsForTest() // exported wrapper for the test — add it in Step 4

    var opts []model.Option
    require.NoError(t, db.Find(&opts).Error)
    require.NotEmpty(t, opts, "expected options to be generated")

    for _, o := range opts {
        require.NotNil(t, o.ListingID, "option %s missing listing_id", o.Ticker)

        var l model.Listing
        require.NoError(t, db.First(&l, *o.ListingID).Error)
        require.Equal(t, "option", l.SecurityType)
        require.Equal(t, o.ID, l.SecurityID)
        require.Equal(t, ex.ID, l.ExchangeID, "option listing must be on the same exchange as underlying stock")
    }
}
```

If the project does not yet have `setupTestDB`, use the existing pattern from another `_test.go` file in `stock-service/internal/service/`. If `Listing` is missing a repository `Create` method, use `db.Create(&l)` directly.

- [ ] **Step 3: Run the test to verify it fails.**

```bash
cd stock-service && go test ./internal/service/ -run TestGenerateAllOptions_AttachesListingToEveryOption -v
```

Expected: FAIL (either compilation error because `GenerateAllOptionsForTest` does not exist, or the option rows have nil ListingID).

- [ ] **Step 4: Implement the listing-creation logic.**

In `stock-service/internal/service/security_sync.go`, modify `generateAllOptions`:

```go
func (s *SecuritySyncService) generateAllOptions() {
    stocks, err := s.stockRepo.List()
    if err != nil {
        log.Printf("WARN: failed to list stocks for option generation: %v", err)
        return
    }

    totalGenerated := 0
    for _, stock := range stocks {
        // Resolve the stock's exchange via its listing row.
        stockListing, err := s.listingSvc.FindByStock(stock.ID) // add this method if missing
        if err != nil || stockListing == nil {
            log.Printf("WARN: no listing for stock %s; skipping option generation", stock.Ticker)
            continue
        }

        options := GenerateOptionsForStock(&stock)
        for _, opt := range options {
            opt := opt // capture
            if err := s.optionRepo.UpsertByTicker(&opt); err != nil {
                log.Printf("WARN: failed to upsert option %s: %v", opt.Ticker, err)
                continue
            }
            // Create or upsert the option listing.
            optListing := &model.Listing{
                SecurityID:   opt.ID,
                SecurityType: "option",
                ExchangeID:   stockListing.ExchangeID,
                Price:        opt.Premium,
                LastRefresh:  time.Now(),
            }
            if err := s.listingSvc.UpsertForOption(optListing); err != nil {
                log.Printf("WARN: failed to upsert option listing for %s: %v", opt.Ticker, err)
                continue
            }
            // Back-fill ListingID on the option row.
            if err := s.optionRepo.SetListingID(opt.ID, optListing.ID); err != nil {
                log.Printf("WARN: failed to set listing_id on option %s: %v", opt.Ticker, err)
                continue
            }
        }
        totalGenerated += len(options)
    }

    deleted, err := s.optionRepo.DeleteExpiredBefore(time.Now())
    if err != nil {
        log.Printf("WARN: failed to clean expired options: %v", err)
    }
    log.Printf("generated %d options for %d stocks, cleaned %d expired", totalGenerated, len(stocks), deleted)
}

// GenerateAllOptionsForTest is a test-only wrapper so unit tests can call the unexported function.
func (s *SecuritySyncService) GenerateAllOptionsForTest() {
    s.generateAllOptions()
}
```

Add the supporting methods:

- In `stock-service/internal/repository/option_repository.go`, add:

```go
func (r *OptionRepository) SetListingID(optionID, listingID uint64) error {
    return r.db.Model(&model.Option{}).Where("id = ?", optionID).Update("listing_id", listingID).Error
}
```

- In `stock-service/internal/service/listing_service.go`, add:

```go
func (s *ListingService) FindByStock(stockID uint64) (*model.Listing, error) {
    var l model.Listing
    err := s.db.Where("security_id = ? AND security_type = ?", stockID, "stock").First(&l).Error
    if err != nil {
        return nil, err
    }
    return &l, nil
}

func (s *ListingService) UpsertForOption(l *model.Listing) error {
    return s.db.Clauses(clause.OnConflict{
        Columns:   []clause.Column{{Name: "security_id"}, {Name: "security_type"}},
        DoUpdates: clause.AssignmentColumns([]string{"exchange_id", "price", "last_refresh", "updated_at"}),
    }).Create(l).Error
}
```

(If `ListingService` does not currently hold a `*gorm.DB`, thread it through via its constructor. Check existing fields first and follow the current pattern.)

Also add a compound unique index to `model/listing.go`:

```go
type Listing struct {
    ...
    SecurityID   uint64 `gorm:"not null;index;uniqueIndex:ux_listing_security"`
    SecurityType string `gorm:"size:10;not null;index;uniqueIndex:ux_listing_security"`
    ...
}
```

(Only if this index is missing. Check first.)

- [ ] **Step 5: Run the test to verify it passes.**

```bash
cd stock-service && go test ./internal/service/ -run TestGenerateAllOptions_AttachesListingToEveryOption -v
```

Expected: PASS.

- [ ] **Step 6: Run the full stock-service test suite to catch regressions.**

```bash
cd stock-service && go test ./... -count=1
```

- [ ] **Step 7: Lint.**

```bash
cd stock-service && golangci-lint run ./...
```

- [ ] **Step 8: Commit.**

```bash
git add stock-service/
git commit -m "feat(stock-service): attach listings to options on seed"
```

---

### Task 1.4: Populate `OptionItem` / `OptionDetail` protobuf `listing_id` in stock-service handler

**Files:**
- Modify: `stock-service/internal/handler/security_handler.go` — `toOptionItem` and `toOptionDetail` (lines 420-470)
- Test: `stock-service/internal/handler/security_handler_test.go`

- [ ] **Step 1: Write a failing test.**

Add to `stock-service/internal/handler/security_handler_test.go`:

```go
func TestToOptionItem_IncludesListingIDWhenSet(t *testing.T) {
    listingID := uint64(42)
    opt := &model.Option{
        ID:          7,
        Ticker:      "AAPL260116C00200000",
        Name:        "AAPL Call Jan 2026 $200",
        StockID:     1,
        OptionType:  "call",
        StrikePrice: decimal.NewFromInt(200),
        Premium:     decimal.NewFromFloat(5.75),
        ListingID:   &listingID,
    }
    item := toOptionItem(opt)
    require.NotNil(t, item.ListingId)
    require.Equal(t, uint64(42), *item.ListingId)
}

func TestToOptionItem_LeavesListingIDNilWhenUnset(t *testing.T) {
    opt := &model.Option{ID: 7, Ticker: "X", Name: "X", ListingID: nil, StrikePrice: decimal.Zero, Premium: decimal.Zero}
    item := toOptionItem(opt)
    require.Nil(t, item.ListingId)
}
```

- [ ] **Step 2: Run the test to verify it fails.**

```bash
cd stock-service && go test ./internal/handler/ -run TestToOptionItem -v
```

Expected: FAIL (field does not exist or is not populated).

- [ ] **Step 3: Update `toOptionItem` and `toOptionDetail`.**

In `stock-service/internal/handler/security_handler.go`, locate `toOptionItem` and modify:

```go
func toOptionItem(o *model.Option) *pb.OptionItem {
    if o == nil {
        return nil
    }
    item := &pb.OptionItem{
        Id:                o.ID,
        Ticker:            o.Ticker,
        Name:              o.Name,
        StockId:           o.StockID,
        OptionType:        o.OptionType,
        StrikePrice:       o.StrikePrice.String(),
        ImpliedVolatility: o.ImpliedVolatility.String(),
        Premium:           o.Premium.String(),
        OpenInterest:      o.OpenInterest,
        SettlementDate:    o.SettlementDate.Format(time.RFC3339),
    }
    if o.ListingID != nil {
        lid := *o.ListingID
        item.ListingId = &lid
    }
    return item
}
```

Adapt field names to whatever the generated proto uses (`ListingId` vs `Listing_Id` etc.). Do the same for `toOptionDetail`.

- [ ] **Step 4: Run the test to verify it passes.**

```bash
cd stock-service && go test ./internal/handler/ -run TestToOptionItem -v
```

Expected: PASS.

- [ ] **Step 5: Full stock-service test run + lint.**

```bash
cd stock-service && go test ./... -count=1 && golangci-lint run ./...
```

- [ ] **Step 6: Commit.**

```bash
git add stock-service/
git commit -m "feat(stock-service): surface listing_id on OptionItem and OptionDetail"
```

---

## Phase 2 — Source interface + external implementation (no behavior change)

### Task 2.1: Create `internal/source/types.go` with the `Source` interface and DTOs

**Files:**
- Create: `stock-service/internal/source/types.go`

- [ ] **Step 1: Create the file.**

Write to `stock-service/internal/source/types.go`:

```go
package source

import (
    "context"
    "time"

    "github.com/shopspring/decimal"

    "github.com/exbanka/stock-service/internal/model"
)

// Source is the abstraction over how stock-service acquires securities data.
// At any moment there is exactly one active Source. Switching replaces the
// reference under a mutex owned by the sync service.
type Source interface {
    // Name returns the canonical identifier used in system_settings and the
    // admin switch endpoint. One of: "external", "generated", "simulator".
    Name() string

    // FetchExchanges returns the full list of exchanges the source exposes.
    FetchExchanges(ctx context.Context) ([]model.StockExchange, error)

    // FetchStocks returns stocks with their initial listing data.
    FetchStocks(ctx context.Context) ([]StockWithListing, error)

    // FetchFutures returns futures contracts with their initial listing data.
    FetchFutures(ctx context.Context) ([]FuturesWithListing, error)

    // FetchForex returns forex pairs with their initial listing data.
    FetchForex(ctx context.Context) ([]ForexWithListing, error)

    // FetchOptions returns options for a single underlying stock.
    // The caller passes the already-persisted stock so implementations have
    // the ID and price available.
    FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error)

    // RefreshPrices updates price fields on already-seeded securities. This
    // is called by the sync service's background loop. Implementations that
    // do not need periodic refresh (e.g. external_source handles its own
    // cadence elsewhere) may return nil.
    RefreshPrices(ctx context.Context) error
}

// StockWithListing pairs a stock model with its initial listing attributes.
// Used so callers can persist a securities row and its listing row together.
type StockWithListing struct {
    Stock       model.Stock
    ExchangeID  uint64
    Price       decimal.Decimal
    High        decimal.Decimal
    Low         decimal.Decimal
    LastRefresh time.Time
}

// FuturesWithListing pairs a futures contract with its initial listing attributes.
type FuturesWithListing struct {
    Futures     model.FuturesContract
    ExchangeID  uint64
    Price       decimal.Decimal
    High        decimal.Decimal
    Low         decimal.Decimal
    LastRefresh time.Time
}

// ForexWithListing pairs a forex pair with its initial listing attributes.
type ForexWithListing struct {
    Forex       model.ForexPair
    ExchangeID  uint64
    Price       decimal.Decimal
    High        decimal.Decimal
    Low         decimal.Decimal
    LastRefresh time.Time
}
```

- [ ] **Step 2: Verify it compiles.**

```bash
cd stock-service && go build ./internal/source/...
```

- [ ] **Step 3: Commit.**

```bash
git add stock-service/internal/source/types.go
git commit -m "feat(stock-service): add Source interface and DTOs"
```

---

### Task 2.2: Implement `ExternalSource` wrapping existing providers

**Files:**
- Create: `stock-service/internal/source/external_source.go`
- Test: `stock-service/internal/source/external_source_test.go`

- [ ] **Step 1: Write a failing test that `NewExternalSource(...).Name() == "external"`.**

```go
package source_test

import (
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/exbanka/stock-service/internal/source"
)

func TestExternalSource_Name(t *testing.T) {
    s := source.NewExternalSource(nil, nil, nil, nil, "", nil)
    require.Equal(t, "external", s.Name())
}
```

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/source/ -run TestExternalSource_Name -v
```

Expected: FAIL ("undefined: source.NewExternalSource").

- [ ] **Step 3: Implement `ExternalSource`.**

Write to `stock-service/internal/source/external_source.go`:

```go
package source

import (
    "context"

    "github.com/exbanka/stock-service/internal/model"
    "github.com/exbanka/stock-service/internal/provider"
)

// ExternalSource wraps the existing external provider clients.
// It produces the same data as the pre-refactor code path.
type ExternalSource struct {
    alpaca   *provider.AlpacaClient
    finnhub  *provider.FinnhubClient
    eodhd    *provider.EODHDClient
    av       *provider.AlphaVantageClient
    csvPath  string
    futuresS *FuturesSeedLoader
}

// FuturesSeedLoader is a tiny adapter around the existing JSON seed file loader.
// (Wire the real implementation from security_sync.go.)
type FuturesSeedLoader struct {
    Path string
}

func NewExternalSource(
    alpaca *provider.AlpacaClient,
    finnhub *provider.FinnhubClient,
    eodhd *provider.EODHDClient,
    av *provider.AlphaVantageClient,
    csvPath string,
    futuresSeed *FuturesSeedLoader,
) *ExternalSource {
    return &ExternalSource{
        alpaca:   alpaca,
        finnhub:  finnhub,
        eodhd:    eodhd,
        av:       av,
        csvPath:  csvPath,
        futuresS: futuresSeed,
    }
}

func (s *ExternalSource) Name() string { return "external" }

func (s *ExternalSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
    // Move the CSV-loading logic currently inside SecuritySyncService.syncExchanges() here.
    return provider.LoadExchangesFromCSV(s.csvPath)
}

func (s *ExternalSource) FetchStocks(ctx context.Context) ([]StockWithListing, error) {
    // Move the body of SecuritySyncService.syncStocks here verbatim, returning
    // []StockWithListing instead of writing to repos. Keep the same provider-
    // preference order (Alpaca first, then Finnhub/EODHD/AV fallbacks).
    // See security_sync.go for the current implementation to port.
    return nil, errNotYetPorted
}

func (s *ExternalSource) FetchFutures(ctx context.Context) ([]FuturesWithListing, error) {
    // Load from the JSON seed file used by SecuritySyncService.seedFutures.
    return nil, errNotYetPorted
}

func (s *ExternalSource) FetchForex(ctx context.Context) ([]ForexWithListing, error) {
    // Move the body of SecuritySyncService.seedForexPairs here.
    return nil, errNotYetPorted
}

func (s *ExternalSource) FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error) {
    // The external providers do not provide options today; use the local
    // GenerateOptionsForStock helper from the service package. To avoid an
    // import cycle, duplicate the small helper or expose it via a shared sub-
    // package. See Step 4 below for the cycle fix.
    return nil, errNotYetPorted
}

func (s *ExternalSource) RefreshPrices(ctx context.Context) error {
    // External refresh already runs on a slower cadence elsewhere (see
    // SecuritySyncService.RefreshPrices). Returning nil means "don't refresh
    // from the background loop; the existing refresh goroutine handles it".
    return nil
}
```

- [ ] **Step 4: Resolve the import cycle.**

`source` should not import `service` (where `GenerateOptionsForStock` currently lives). Move the three helpers (`GenerateOptionsForStock`, `generateSettlementDates`, `generateStrikePrices`, `estimatePremium`) from `stock-service/internal/service/security_service.go` into a new file `stock-service/internal/source/option_generator.go` in the `source` package, and re-export them. Update the one callsite in `security_sync.go` to import from `source`.

If there are other callers, grep first:

```bash
grep -rn "GenerateOptionsForStock\|generateSettlementDates\|generateStrikePrices\|estimatePremium" stock-service/
```

Fix every callsite.

- [ ] **Step 5: Add a placeholder `errNotYetPorted` during port.**

```go
var errNotYetPorted = errors.New("external source: method not yet ported from SecuritySyncService")
```

Delete this once Step 6 finishes porting all five `FetchX` methods.

- [ ] **Step 6: Port each `FetchX` method.**

For each of `FetchStocks`, `FetchFutures`, `FetchForex`, `FetchOptions`:
1. Open `security_sync.go` and copy the relevant loop verbatim.
2. Replace all `s.<repo>.UpsertX(&row)` calls with appending to a local slice of `StockWithListing` / `FuturesWithListing` / `ForexWithListing` / `[]model.Option`.
3. Return the slice.
4. Leave the cadence decisions (which provider to prefer, how to fall back) exactly as they were.

- [ ] **Step 7: Run the Name() test again.**

```bash
cd stock-service && go test ./internal/source/ -run TestExternalSource_Name -v
```

Expected: PASS.

- [ ] **Step 8: Write a smoke test for `FetchExchanges` using the real CSV file from `stock-service/data/exchanges.csv`.**

```go
func TestExternalSource_FetchExchanges_FromCSV(t *testing.T) {
    s := source.NewExternalSource(nil, nil, nil, nil, "../../data/exchanges.csv", nil)
    got, err := s.FetchExchanges(context.Background())
    require.NoError(t, err)
    require.NotEmpty(t, got, "csv should yield at least one exchange")
}
```

Run: `go test ./internal/source/ -run TestExternalSource_FetchExchanges_FromCSV -v`. Expected: PASS.

- [ ] **Step 9: Lint + commit.**

```bash
cd stock-service && golangci-lint run ./... && cd ..
git add stock-service/
git commit -m "feat(stock-service): add ExternalSource wrapping existing providers"
```

---

### Task 2.3: Refactor `SecuritySyncService` to hold a `Source`

**Files:**
- Modify: `stock-service/internal/service/security_sync.go`
- Modify: `stock-service/cmd/main.go`
- Test: `stock-service/internal/service/security_sync_test.go`

- [ ] **Step 1: Change `SecuritySyncService`'s dependencies.**

Replace the four provider-client fields (`avClient`, `eodhClient`, `alpacaClient`, `finnhubClient`) with a single `src source.Source` protected by a `sync.RWMutex`:

```go
type SecuritySyncService struct {
    stockRepo    StockRepo
    futuresRepo  FuturesRepo
    forexRepo    ForexPairRepo
    optionRepo   OptionRepo
    exchangeRepo *repository.ExchangeRepository
    settingRepo  SettingRepo
    listingSvc   *ListingService
    cache        *cache.RedisCache
    influxClient *influx.Client

    srcMu sync.RWMutex
    src   source.Source
}

func NewSecuritySyncService(
    stockRepo StockRepo, futuresRepo FuturesRepo, forexRepo ForexPairRepo,
    optionRepo OptionRepo, exchangeRepo *repository.ExchangeRepository,
    settingRepo SettingRepo, listingSvc *ListingService,
    redisCache *cache.RedisCache, influxClient *influx.Client,
    initialSource source.Source,
) *SecuritySyncService {
    return &SecuritySyncService{
        stockRepo:    stockRepo,
        futuresRepo:  futuresRepo,
        forexRepo:    forexRepo,
        optionRepo:   optionRepo,
        exchangeRepo: exchangeRepo,
        settingRepo:  settingRepo,
        listingSvc:   listingSvc,
        cache:        redisCache,
        influxClient: influxClient,
        src:          initialSource,
    }
}

func (s *SecuritySyncService) Source() source.Source {
    s.srcMu.RLock()
    defer s.srcMu.RUnlock()
    return s.src
}

func (s *SecuritySyncService) SetSource(newSrc source.Source) {
    s.srcMu.Lock()
    defer s.srcMu.Unlock()
    s.src = newSrc
}
```

- [ ] **Step 2: Rewrite `syncExchanges`, `syncStocks`, `seedForexPairs`, `seedFutures`, `generateAllOptions` to delegate to `s.Source()`.**

Each method becomes:

```go
func (s *SecuritySyncService) syncExchanges(ctx context.Context) {
    src := s.Source()
    exchanges, err := src.FetchExchanges(ctx)
    if err != nil {
        log.Printf("WARN: failed to fetch exchanges from %s: %v", src.Name(), err)
        return
    }
    for _, ex := range exchanges {
        ex := ex
        if err := s.exchangeRepo.Upsert(&ex); err != nil {
            log.Printf("WARN: failed to upsert exchange %s: %v", ex.Acronym, err)
        }
    }
}

// Apply the same shape to syncStocks, seedFutures, seedForexPairs, generateAllOptions.
```

For `generateAllOptions`, iterate stocks, call `src.FetchOptions(ctx, &stock)`, then run the listing-creation logic added in Task 1.3.

- [ ] **Step 3: Update `cmd/main.go` to construct and pass an `ExternalSource`.**

In `stock-service/cmd/main.go`, replace the four provider parameters to `NewSecuritySyncService` with a single `source.NewExternalSource(...)` call.

- [ ] **Step 4: Verify compile.**

```bash
cd stock-service && go build ./...
```

- [ ] **Step 5: Write / update a unit test confirming the service delegates to the active Source.**

```go
type fakeSource struct {
    name string
    exchangesCalled int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
    f.exchangesCalled++
    return []model.StockExchange{{Acronym: "FAKE", MICCode: "XFAK", Name: "Fake"}}, nil
}
// ... implement the rest returning empty slices ...

func TestSecuritySyncService_DelegatesToSource(t *testing.T) {
    db := setupTestDB(t)
    // ... wire repos ...
    fs := &fakeSource{name: "external"}
    svc := service.NewSecuritySyncService(/* ... */, fs)
    svc.SeedAll(context.Background(), "")
    require.Equal(t, 1, fs.exchangesCalled)
}
```

- [ ] **Step 6: Run the test to confirm PASS.**

```bash
cd stock-service && go test ./internal/service/ -run TestSecuritySyncService_DelegatesToSource -v
```

- [ ] **Step 7: Run full test suite + lint + commit.**

```bash
cd stock-service && go test ./... -count=1 && golangci-lint run ./...
git add stock-service/
git commit -m "refactor(stock-service): SecuritySyncService holds Source abstraction"
```

---

## Phase 3 — Generated source

### Task 3.1: Add the hard-coded generated data tables

**Files:**
- Create: `stock-service/internal/source/generated_data.go`

- [ ] **Step 1: Write the tables.**

```go
package source

import (
    "time"

    "github.com/shopspring/decimal"

    "github.com/exbanka/stock-service/internal/model"
)

// Exchanges — 20 real-sounding global exchanges with stable IDs.
var generatedExchanges = []model.StockExchange{
    {Acronym: "NYSE",     MICCode: "XNYS", Name: "New York Stock Exchange"},
    {Acronym: "NASDAQ",   MICCode: "XNAS", Name: "Nasdaq"},
    {Acronym: "LSE",      MICCode: "XLON", Name: "London Stock Exchange"},
    {Acronym: "TSE",      MICCode: "XTKS", Name: "Tokyo Stock Exchange"},
    {Acronym: "HKEX",     MICCode: "XHKG", Name: "Hong Kong Stock Exchange"},
    {Acronym: "SSE",      MICCode: "XSHG", Name: "Shanghai Stock Exchange"},
    {Acronym: "EURONEXT", MICCode: "XAMS", Name: "Euronext"},
    {Acronym: "TSX",      MICCode: "XTSE", Name: "Toronto Stock Exchange"},
    {Acronym: "BSE",      MICCode: "XBOM", Name: "Bombay Stock Exchange"},
    {Acronym: "ASX",      MICCode: "XASX", Name: "Australian Securities Exchange"},
    {Acronym: "JSE",      MICCode: "XJSE", Name: "Johannesburg Stock Exchange"},
    {Acronym: "BMV",      MICCode: "XMEX", Name: "Mexican Stock Exchange"},
    {Acronym: "BVMF",     MICCode: "BVMF", Name: "B3 Brazil"},
    {Acronym: "KRX",      MICCode: "XKRX", Name: "Korea Exchange"},
    {Acronym: "BME",      MICCode: "XMAD", Name: "Bolsas y Mercados Espanoles"},
    {Acronym: "SIX",      MICCode: "XSWX", Name: "SIX Swiss Exchange"},
    {Acronym: "OMX",      MICCode: "XSTO", Name: "Nasdaq Nordic"},
    {Acronym: "WSE",      MICCode: "XWAR", Name: "Warsaw Stock Exchange"},
    {Acronym: "BVC",      MICCode: "XBOG", Name: "Colombia Stock Exchange"},
    {Acronym: "MOEX",     MICCode: "MISX", Name: "Moscow Exchange"},
}

type stockSeed struct {
    Ticker string
    Name   string
    Price  float64
}

var generatedStocks = []stockSeed{
    {"AAPL", "Apple Inc.", 180.00},
    {"MSFT", "Microsoft Corporation", 420.00},
    {"GOOGL", "Alphabet Inc. Class A", 155.00},
    {"AMZN", "Amazon.com Inc.", 185.00},
    {"META", "Meta Platforms Inc.", 510.00},
    {"NVDA", "NVIDIA Corporation", 900.00},
    {"TSLA", "Tesla Inc.", 175.00},
    {"JPM", "JPMorgan Chase & Co.", 200.00},
    {"V", "Visa Inc.", 280.00},
    {"JNJ", "Johnson & Johnson", 155.00},
    {"WMT", "Walmart Inc.", 60.00},
    {"PG", "Procter & Gamble", 160.00},
    {"MA", "Mastercard Inc.", 470.00},
    {"HD", "The Home Depot Inc.", 380.00},
    {"DIS", "The Walt Disney Company", 115.00},
    {"BAC", "Bank of America Corp.", 38.00},
    {"XOM", "Exxon Mobil Corporation", 115.00},
    {"KO", "The Coca-Cola Company", 62.00},
    {"PEP", "PepsiCo Inc.", 175.00},
    {"CSCO", "Cisco Systems Inc.", 50.00},
}

type futuresSeed struct {
    Ticker       string
    Name         string
    ContractSize int64
    Price        float64
    DaysToExpiry int
}

var generatedFutures = []futuresSeed{
    {"CL",  "Crude Oil WTI",            1000,  85.00, 45},
    {"GC",  "Gold",                     100,   2380.00, 60},
    {"SI",  "Silver",                   5000,  28.00, 60},
    {"NG",  "Natural Gas Henry Hub",    10000, 2.40, 45},
    {"HG",  "Copper",                   25000, 4.35, 60},
    {"ZC",  "Corn",                     5000,  4.80, 90},
    {"ZS",  "Soybean",                  5000,  11.80, 90},
    {"ZW",  "Wheat",                    5000,  5.50, 90},
    {"CC",  "Cocoa",                    10,    9500.00, 75},
    {"KC",  "Coffee",                   37500, 2.10, 75},
    {"SB",  "Sugar #11",                112000, 0.23, 75},
    {"CT",  "Cotton #2",                50000, 0.82, 75},
    {"ES",  "E-mini S&P 500",           50,    5200.00, 30},
    {"NQ",  "E-mini Nasdaq-100",        20,    18200.00, 30},
    {"YM",  "E-mini Dow",               5,     39000.00, 30},
    {"RTY", "E-mini Russell 2000",      50,    2080.00, 30},
    {"ZB",  "30Y US Treasury Bond",     1000,  120.50, 60},
    {"ZN",  "10Y US T-Note",            1000,  110.75, 60},
    {"6E",  "Euro FX",                  125000, 1.085, 30},
    {"6J",  "Japanese Yen",             12500000, 0.0066, 30},
}

// supportedCurrencies is the fixed set of 8 currencies used by exchange-service.
var supportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

// forexSeedPrices maps "BASE/QUOTE" → realistic mid price.
// Only the 8×7 = 56 ordered pairs are used.
var forexSeedPrices = map[string]float64{
    "USD/EUR": 0.925, "EUR/USD": 1.080,
    "USD/GBP": 0.790, "GBP/USD": 1.265,
    "USD/JPY": 152.0, "JPY/USD": 0.00658,
    "USD/CHF": 0.905, "CHF/USD": 1.105,
    "USD/CAD": 1.360, "CAD/USD": 0.735,
    "USD/AUD": 1.530, "AUD/USD": 0.653,
    "USD/RSD": 108.0, "RSD/USD": 0.00925,
    "EUR/GBP": 0.855, "GBP/EUR": 1.170,
    "EUR/JPY": 164.0, "JPY/EUR": 0.00610,
    "EUR/CHF": 0.975, "CHF/EUR": 1.025,
    "EUR/CAD": 1.470, "CAD/EUR": 0.680,
    "EUR/AUD": 1.655, "AUD/EUR": 0.604,
    "EUR/RSD": 117.0, "RSD/EUR": 0.00855,
    "GBP/JPY": 192.0, "JPY/GBP": 0.00520,
    "GBP/CHF": 1.145, "CHF/GBP": 0.873,
    "GBP/CAD": 1.720, "CAD/GBP": 0.581,
    "GBP/AUD": 1.935, "AUD/GBP": 0.517,
    "GBP/RSD": 137.0, "RSD/GBP": 0.00730,
    "JPY/CHF": 0.00596, "CHF/JPY": 168.0,
    "JPY/CAD": 0.00895, "CAD/JPY": 111.7,
    "JPY/AUD": 0.01007, "AUD/JPY": 99.3,
    "JPY/RSD": 0.711, "RSD/JPY": 1.407,
    "CHF/CAD": 1.502, "CAD/CHF": 0.666,
    "CHF/AUD": 1.690, "AUD/CHF": 0.592,
    "CHF/RSD": 119.5, "RSD/CHF": 0.00837,
    "CAD/AUD": 1.125, "AUD/CAD": 0.889,
    "CAD/RSD": 79.4, "RSD/CAD": 0.01259,
    "AUD/RSD": 70.6, "RSD/AUD": 0.01417,
}

// dec is a small helper to make the tables above easier to read.
func dec(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// futuresSettlementDate computes a deterministic settlement date from a seed.
// Called with a fixed "now" during generation so tests are reproducible.
func futuresSettlementDate(now time.Time, daysOut int) time.Time {
    return now.AddDate(0, 0, daysOut)
}

var _ = model.ForexPair{} // keep import even if not yet referenced
```

- [ ] **Step 2: Compile check.**

```bash
cd stock-service && go build ./internal/source/
```

- [ ] **Step 3: Commit.**

```bash
git add stock-service/internal/source/generated_data.go
git commit -m "feat(stock-service): add generated source data tables"
```

---

### Task 3.2: Implement `GeneratedSource` with deterministic seed + random-walk refresh

**Files:**
- Create: `stock-service/internal/source/generated_source.go`
- Test: `stock-service/internal/source/generated_source_test.go`

- [ ] **Step 1: Write failing tests.**

```go
package source_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/exbanka/stock-service/internal/source"
)

func TestGeneratedSource_Name(t *testing.T) {
    s := source.NewGeneratedSource()
    require.Equal(t, "generated", s.Name())
}

func TestGeneratedSource_FetchExchanges_Returns20(t *testing.T) {
    s := source.NewGeneratedSource()
    ex, err := s.FetchExchanges(context.Background())
    require.NoError(t, err)
    require.Len(t, ex, 20)
}

func TestGeneratedSource_FetchStocks_Returns20WithNonZeroPrice(t *testing.T) {
    s := source.NewGeneratedSource()
    stocks, err := s.FetchStocks(context.Background())
    require.NoError(t, err)
    require.Len(t, stocks, 20)
    for _, sw := range stocks {
        require.False(t, sw.Price.IsZero(), "stock %s has zero price", sw.Stock.Ticker)
        require.NotZero(t, sw.ExchangeID, "stock %s has zero exchange id", sw.Stock.Ticker)
    }
}

func TestGeneratedSource_FetchFutures_Returns20(t *testing.T) {
    s := source.NewGeneratedSource()
    futures, err := s.FetchFutures(context.Background())
    require.NoError(t, err)
    require.Len(t, futures, 20)
}

func TestGeneratedSource_FetchForex_Returns56(t *testing.T) {
    s := source.NewGeneratedSource()
    fx, err := s.FetchForex(context.Background())
    require.NoError(t, err)
    require.Len(t, fx, 56)
}

func TestGeneratedSource_IsDeterministic(t *testing.T) {
    a := source.NewGeneratedSource()
    b := source.NewGeneratedSource()
    as, _ := a.FetchStocks(context.Background())
    bs, _ := b.FetchStocks(context.Background())
    require.Equal(t, len(as), len(bs))
    for i := range as {
        require.Equal(t, as[i].Stock.Ticker, bs[i].Stock.Ticker)
        require.True(t, as[i].Price.Equal(bs[i].Price), "price drift on %s", as[i].Stock.Ticker)
    }
}

func TestGeneratedSource_RefreshPrices_MovesButStaysInRange(t *testing.T) {
    s := source.NewGeneratedSource()
    stocks, _ := s.FetchStocks(context.Background())
    before := stocks[0].Price
    require.NoError(t, s.RefreshPrices(context.Background()))
    stocksAfter, _ := s.FetchStocks(context.Background())
    after := stocksAfter[0].Price
    // Within ±1% in a single refresh.
    diff := after.Sub(before).Abs()
    limit := before.Mul(source.DecFromFloat(0.01))
    require.True(t, diff.LessThanOrEqual(limit),
        "price moved too far: before=%s after=%s", before.String(), after.String())
}
```

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/source/ -run TestGeneratedSource -v
```

Expected: FAIL ("undefined: source.NewGeneratedSource").

- [ ] **Step 3: Implement `GeneratedSource`.**

Write to `stock-service/internal/source/generated_source.go`:

```go
package source

import (
    "context"
    "hash/fnv"
    "math/rand"
    "sync"
    "time"

    "github.com/shopspring/decimal"

    "github.com/exbanka/stock-service/internal/model"
)

const (
    generatedSeed          = int64(0x1EB0081A) // arbitrary stable seed; not cryptographic
    generatedRandomWalkPct = 0.005              // ±0.5% per refresh tick
)

// DecFromFloat is exported so tests can build expected decimals.
func DecFromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

type GeneratedSource struct {
    mu        sync.RWMutex
    now       time.Time        // frozen on construction for determinism
    rng       *rand.Rand
    stockPx   map[string]decimal.Decimal // ticker → current price
    futuresPx map[string]decimal.Decimal
    forexPx   map[string]decimal.Decimal
}

func NewGeneratedSource() *GeneratedSource {
    src := rand.NewSource(generatedSeed)
    g := &GeneratedSource{
        now:       time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC),
        rng:       rand.New(src),
        stockPx:   make(map[string]decimal.Decimal, len(generatedStocks)),
        futuresPx: make(map[string]decimal.Decimal, len(generatedFutures)),
        forexPx:   make(map[string]decimal.Decimal, len(forexSeedPrices)),
    }
    for _, s := range generatedStocks {
        g.stockPx[s.Ticker] = dec(s.Price)
    }
    for _, f := range generatedFutures {
        g.futuresPx[f.Ticker] = dec(f.Price)
    }
    for pair, p := range forexSeedPrices {
        g.forexPx[pair] = dec(p)
    }
    return g
}

func (g *GeneratedSource) Name() string { return "generated" }

// exchangeForTicker picks an exchange index 1..20 deterministically from the ticker.
func exchangeForTicker(ticker string) uint64 {
    h := fnv.New32a()
    _, _ = h.Write([]byte(ticker))
    return uint64(h.Sum32()%20) + 1
}

func (g *GeneratedSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
    out := make([]model.StockExchange, len(generatedExchanges))
    copy(out, generatedExchanges)
    return out, nil
}

func (g *GeneratedSource) FetchStocks(ctx context.Context) ([]StockWithListing, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    out := make([]StockWithListing, 0, len(generatedStocks))
    for _, s := range generatedStocks {
        price := g.stockPx[s.Ticker]
        out = append(out, StockWithListing{
            Stock: model.Stock{
                Ticker: s.Ticker,
                Name:   s.Name,
                Price:  price,
            },
            ExchangeID:  exchangeForTicker(s.Ticker),
            Price:       price,
            High:        price,
            Low:         price,
            LastRefresh: g.now,
        })
    }
    return out, nil
}

func (g *GeneratedSource) FetchFutures(ctx context.Context) ([]FuturesWithListing, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    out := make([]FuturesWithListing, 0, len(generatedFutures))
    for _, f := range generatedFutures {
        price := g.futuresPx[f.Ticker]
        out = append(out, FuturesWithListing{
            Futures: model.FuturesContract{
                Ticker:         f.Ticker,
                Name:           f.Name,
                ContractSize:   f.ContractSize,
                Price:          price,
                SettlementDate: futuresSettlementDate(g.now, f.DaysToExpiry),
            },
            ExchangeID:  exchangeForTicker(f.Ticker),
            Price:       price,
            High:        price,
            Low:         price,
            LastRefresh: g.now,
        })
    }
    return out, nil
}

func (g *GeneratedSource) FetchForex(ctx context.Context) ([]ForexWithListing, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    out := make([]ForexWithListing, 0, 56)
    // Iterate in a deterministic order by walking supported currencies.
    for _, base := range supportedCurrencies {
        for _, quote := range supportedCurrencies {
            if base == quote {
                continue
            }
            pair := base + "/" + quote
            price, ok := g.forexPx[pair]
            if !ok {
                continue
            }
            out = append(out, ForexWithListing{
                Forex: model.ForexPair{
                    Ticker:        pair,
                    BaseCurrency:  base,
                    QuoteCurrency: quote,
                    Price:         price,
                },
                ExchangeID:  exchangeForTicker(pair),
                Price:       price,
                High:        price,
                Low:         price,
                LastRefresh: g.now,
            })
        }
    }
    return out, nil
}

func (g *GeneratedSource) FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error) {
    // The shared option generator already takes a stock and returns 20 options
    // (10 calls + 10 puts across a strike grid). It lives in this package now.
    return GenerateOptionsForStock(stock), nil
}

func (g *GeneratedSource) RefreshPrices(ctx context.Context) error {
    g.mu.Lock()
    defer g.mu.Unlock()
    walk := func(p decimal.Decimal) decimal.Decimal {
        // Uniform in [-pct, +pct]
        delta := (g.rng.Float64()*2 - 1) * generatedRandomWalkPct
        return p.Mul(decimal.NewFromFloat(1 + delta))
    }
    for k, v := range g.stockPx {
        g.stockPx[k] = walk(v)
    }
    for k, v := range g.futuresPx {
        g.futuresPx[k] = walk(v)
    }
    for k, v := range g.forexPx {
        g.forexPx[k] = walk(v)
    }
    return nil
}
```

- [ ] **Step 4: Run the tests to verify pass.**

```bash
cd stock-service && go test ./internal/source/ -run TestGeneratedSource -v
```

Expected: all six tests PASS.

- [ ] **Step 5: Lint + commit.**

```bash
cd stock-service && golangci-lint run ./internal/source/
git add stock-service/internal/source/
git commit -m "feat(stock-service): add GeneratedSource with deterministic seed"
```

---

## Phase 4 — Simulator source

### Task 4.1: Create HTTP client + self-registration

**Files:**
- Create: `stock-service/internal/source/simulator_client.go`
- Test: `stock-service/internal/source/simulator_client_test.go`

- [ ] **Step 1: Write failing tests for self-registration.**

```go
package source_test

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/exbanka/stock-service/internal/source"
)

type fakeSettingStore struct {
    data map[string]string
}

func (f *fakeSettingStore) Get(key string) (string, error) { return f.data[key], nil }
func (f *fakeSettingStore) Set(key, value string) error     { f.data[key] = value; return nil }

func TestSimulatorClient_RegistersWhenKeyMissing(t *testing.T) {
    registered := false
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.URL.Path {
        case "/api/banks/register":
            require.Equal(t, "POST", r.Method)
            registered = true
            _, _ = w.Write([]byte(`{"data":{"id":1,"name":"ExBanka","api_key":"ms_testkey"}}`))
        default:
            http.Error(w, "unexpected path", 404)
        }
    }))
    defer server.Close()

    store := &fakeSettingStore{data: map[string]string{}}
    c := source.NewSimulatorClient(server.URL, "ExBanka", store)
    require.NoError(t, c.EnsureRegistered())
    require.True(t, registered)
    require.Equal(t, "ms_testkey", store.data["market_simulator_api_key"])
}

func TestSimulatorClient_ReusesStoredKey(t *testing.T) {
    validated := false
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/banks/me" {
            validated = true
            require.Equal(t, "ms_existing", r.Header.Get("X-API-Key"))
            _ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 1, "name": "ExBanka"}})
            return
        }
        http.Error(w, "unexpected", 500)
    }))
    defer server.Close()

    store := &fakeSettingStore{data: map[string]string{"market_simulator_api_key": "ms_existing"}}
    c := source.NewSimulatorClient(server.URL, "ExBanka", store)
    require.NoError(t, c.EnsureRegistered())
    require.True(t, validated)
}

func TestSimulatorClient_ReRegistersOnInvalidKey(t *testing.T) {
    registered := false
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.URL.Path {
        case "/api/banks/me":
            http.Error(w, "unauthorized", 401)
        case "/api/banks/register":
            registered = true
            _, _ = w.Write([]byte(`{"data":{"id":1,"name":"ExBanka","api_key":"ms_new"}}`))
        }
    }))
    defer server.Close()

    store := &fakeSettingStore{data: map[string]string{"market_simulator_api_key": "ms_stale"}}
    c := source.NewSimulatorClient(server.URL, "ExBanka", store)
    require.NoError(t, c.EnsureRegistered())
    require.True(t, registered)
    require.Equal(t, "ms_new", store.data["market_simulator_api_key"])
}
```

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/source/ -run TestSimulatorClient_ -v
```

Expected: FAIL ("undefined: source.NewSimulatorClient").

- [ ] **Step 3: Implement the client.**

Write to `stock-service/internal/source/simulator_client.go`:

```go
package source

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

const settingKeyAPIKey = "market_simulator_api_key"

// SettingStore is a tiny interface over system_settings so tests can mock it.
type SettingStore interface {
    Get(key string) (string, error)
    Set(key, value string) error
}

// SimulatorClient handles auth + HTTP against Market-Simulator.
type SimulatorClient struct {
    baseURL  string
    bankName string
    store    SettingStore
    http     *http.Client
    apiKey   string
}

func NewSimulatorClient(baseURL, bankName string, store SettingStore) *SimulatorClient {
    return &SimulatorClient{
        baseURL:  baseURL,
        bankName: bankName,
        store:    store,
        http:     &http.Client{Timeout: 10 * time.Second},
    }
}

// EnsureRegistered either validates the stored API key or registers a new bank.
func (c *SimulatorClient) EnsureRegistered() error {
    existing, _ := c.store.Get(settingKeyAPIKey)
    if existing != "" {
        c.apiKey = existing
        if err := c.validate(); err == nil {
            return nil
        }
    }
    return c.register()
}

func (c *SimulatorClient) validate() error {
    req, err := http.NewRequest("GET", c.baseURL+"/api/banks/me", nil)
    if err != nil {
        return err
    }
    req.Header.Set("X-API-Key", c.apiKey)
    resp, err := c.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("validate: status %d", resp.StatusCode)
    }
    return nil
}

func (c *SimulatorClient) register() error {
    body, _ := json.Marshal(map[string]string{"name": c.bankName})
    req, err := http.NewRequest("POST", c.baseURL+"/api/banks/register", bytes.NewReader(body))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    b, _ := io.ReadAll(resp.Body)
    if resp.StatusCode >= 400 {
        return fmt.Errorf("register: status %d body %s", resp.StatusCode, string(b))
    }
    var parsed struct {
        Data struct {
            APIKey string `json:"api_key"`
        } `json:"data"`
    }
    if err := json.Unmarshal(b, &parsed); err != nil {
        return fmt.Errorf("register: decode: %w", err)
    }
    if parsed.Data.APIKey == "" {
        return fmt.Errorf("register: empty api_key in response")
    }
    c.apiKey = parsed.Data.APIKey
    return c.store.Set(settingKeyAPIKey, c.apiKey)
}

// Do performs an authenticated HTTP request against Market-Simulator.
// It retries once on 401 by re-registering.
func (c *SimulatorClient) Do(req *http.Request) (*http.Response, error) {
    req.Header.Set("X-API-Key", c.apiKey)
    resp, err := c.http.Do(req)
    if err != nil {
        return nil, err
    }
    if resp.StatusCode == 401 {
        resp.Body.Close()
        if err := c.register(); err != nil {
            return nil, err
        }
        req.Header.Set("X-API-Key", c.apiKey)
        return c.http.Do(req)
    }
    return resp, nil
}

func (c *SimulatorClient) URL(path string) string { return c.baseURL + path }
```

- [ ] **Step 4: Run the tests.**

```bash
cd stock-service && go test ./internal/source/ -run TestSimulatorClient_ -v
```

Expected: PASS.

- [ ] **Step 5: Lint + commit.**

```bash
cd stock-service && golangci-lint run ./internal/source/
git add stock-service/internal/source/
git commit -m "feat(stock-service): add Market-Simulator HTTP client with self-registration"
```

---

### Task 4.2: Implement `SimulatorSource`

**Files:**
- Create: `stock-service/internal/source/simulator_source.go`
- Test: `stock-service/internal/source/simulator_source_test.go`

- [ ] **Step 1: Write failing tests covering `FetchExchanges`, `FetchStocks` with exchange-picking, `FetchOptions`, and `RefreshPrices` goroutine start/stop.**

```go
func TestSimulatorSource_FetchStocks_PicksLowestExchangeID(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.URL.Path {
        case "/api/banks/me":
            _, _ = w.Write([]byte(`{"data":{"id":1}}`))
        case "/api/market/stocks":
            _, _ = w.Write([]byte(`{"data":[{"id":1,"ticker":"AAPL","name":"Apple"}],"pagination":{"page":1,"per_page":20,"total":1}}`))
        case "/api/market/stocks/AAPL/listings":
            // AAPL is on exchanges 5 and 2; pick 2.
            _, _ = w.Write([]byte(`{"data":[{"id":10,"exchange_id":5,"price":"180.00"},{"id":11,"exchange_id":2,"price":"179.95"}]}`))
        default:
            http.Error(w, "unexpected path "+r.URL.Path, 404)
        }
    }))
    defer server.Close()

    store := &fakeSettingStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
    client := source.NewSimulatorClient(server.URL, "ExBanka", store)
    require.NoError(t, client.EnsureRegistered())

    s := source.NewSimulatorSource(client)
    stocks, err := s.FetchStocks(context.Background())
    require.NoError(t, err)
    require.Len(t, stocks, 1)
    require.Equal(t, uint64(2), stocks[0].ExchangeID)
}
```

Add similar tests for `FetchExchanges`, `FetchFutures`, `FetchForex`, `FetchOptions`.

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/source/ -run TestSimulatorSource -v
```

Expected: FAIL.

- [ ] **Step 3: Implement `SimulatorSource`.**

Write to `stock-service/internal/source/simulator_source.go`:

```go
package source

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "sort"
    "time"

    "github.com/shopspring/decimal"

    "github.com/exbanka/stock-service/internal/model"
)

const (
    simulatorBaseURL         = "http://market-simulator:8080"
    simulatorBankName        = "ExBanka"
    simulatorRefreshInterval = 3 * time.Second
)

// SimulatorSource implements Source by talking to Market-Simulator.
type SimulatorSource struct {
    client *SimulatorClient
}

func NewSimulatorSource(client *SimulatorClient) *SimulatorSource {
    return &SimulatorSource{client: client}
}

func (s *SimulatorSource) Name() string { return "simulator" }

// --- response shapes ---

type msExchange struct {
    ID      uint64 `json:"id"`
    Acronym string `json:"acronym"`
    MICCode string `json:"mic_code"`
    Name    string `json:"name"`
}

type msStock struct {
    ID     uint64 `json:"id"`
    Ticker string `json:"ticker"`
    Name   string `json:"name"`
}

type msListing struct {
    ID         uint64 `json:"id"`
    ExchangeID uint64 `json:"exchange_id"`
    Price      string `json:"price"`
}

type msPaginatedStocks struct {
    Data []msStock `json:"data"`
}

type msPaginatedListings struct {
    Data []msListing `json:"data"`
}

// --- helpers ---

func (s *SimulatorSource) getJSON(ctx context.Context, path string, out interface{}) error {
    req, err := http.NewRequestWithContext(ctx, "GET", s.client.URL(path), nil)
    if err != nil {
        return err
    }
    resp, err := s.client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        return fmt.Errorf("simulator: GET %s status %d", path, resp.StatusCode)
    }
    return json.NewDecoder(resp.Body).Decode(out)
}

func parseDec(s string) decimal.Decimal {
    d, _ := decimal.NewFromString(s)
    return d
}

// --- interface methods ---

func (s *SimulatorSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
    var parsed struct {
        Data []msExchange `json:"data"`
    }
    if err := s.getJSON(ctx, "/api/market/exchanges?per_page=200", &parsed); err != nil {
        return nil, err
    }
    out := make([]model.StockExchange, 0, len(parsed.Data))
    for _, e := range parsed.Data {
        out = append(out, model.StockExchange{Acronym: e.Acronym, MICCode: e.MICCode, Name: e.Name})
    }
    return out, nil
}

func (s *SimulatorSource) FetchStocks(ctx context.Context) ([]StockWithListing, error) {
    var stocks msPaginatedStocks
    if err := s.getJSON(ctx, "/api/market/stocks?per_page=200", &stocks); err != nil {
        return nil, err
    }
    out := make([]StockWithListing, 0, len(stocks.Data))
    for _, st := range stocks.Data {
        var listings msPaginatedListings
        if err := s.getJSON(ctx, "/api/market/stocks/"+st.Ticker+"/listings", &listings); err != nil {
            continue
        }
        if len(listings.Data) == 0 {
            continue
        }
        // Pick the listing with the lowest exchange ID.
        sort.Slice(listings.Data, func(i, j int) bool {
            return listings.Data[i].ExchangeID < listings.Data[j].ExchangeID
        })
        chosen := listings.Data[0]
        price := parseDec(chosen.Price)
        out = append(out, StockWithListing{
            Stock:       model.Stock{Ticker: st.Ticker, Name: st.Name, Price: price},
            ExchangeID:  chosen.ExchangeID,
            Price:       price,
            High:        price,
            Low:         price,
            LastRefresh: time.Now(),
        })
    }
    return out, nil
}

// FetchFutures, FetchForex, FetchOptions follow the same pattern: call the
// corresponding /api/market/<kind> endpoint, decode, build the DTO.
// Implement them the same way.

func (s *SimulatorSource) FetchFutures(ctx context.Context) ([]FuturesWithListing, error) {
    // Mirror FetchStocks: GET /api/market/futures?per_page=200, then listings.
    return nil, fmt.Errorf("simulator: FetchFutures not yet implemented")
}

func (s *SimulatorSource) FetchForex(ctx context.Context) ([]ForexWithListing, error) {
    return nil, fmt.Errorf("simulator: FetchForex not yet implemented")
}

func (s *SimulatorSource) FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error) {
    // Query by the simulator's own stock_id — we need to look up that ID each
    // call, or cache the mapping after FetchStocks. For simplicity, call
    // /api/market/options?stock_id=<simulator_stock_id>.
    return nil, fmt.Errorf("simulator: FetchOptions not yet implemented")
}

func (s *SimulatorSource) RefreshPrices(ctx context.Context) error {
    // No-op at the interface level. The sync service starts a dedicated
    // 3s goroutine when the simulator source is active; that goroutine
    // calls FetchStocks/Futures/Forex and writes back to the DB directly.
    return nil
}
```

- [ ] **Step 4: Port the three remaining fetchers.**

Finish `FetchFutures`, `FetchForex`, and `FetchOptions` following the pattern above. For `FetchOptions`, after `FetchStocks` succeeds the `SimulatorSource` should cache the ticker→simulator_stock_id mapping so subsequent `FetchOptions` calls can resolve it. Minimal sketch:

```go
type SimulatorSource struct {
    client      *SimulatorClient
    stockIDByTicker map[string]uint64
}
```

Populate the cache inside `FetchStocks`. In `FetchOptions`, look up `stock.Ticker` and call `/api/market/options?stock_id=<id>&per_page=200`.

- [ ] **Step 5: Run tests.**

```bash
cd stock-service && go test ./internal/source/ -run TestSimulatorSource -v
```

Expected: PASS.

- [ ] **Step 6: Lint + commit.**

```bash
cd stock-service && golangci-lint run ./internal/source/
git add stock-service/internal/source/
git commit -m "feat(stock-service): add SimulatorSource"
```

---

### Task 4.3: Wire `SystemSettingRepository` to implement `SettingStore`

**Files:**
- Modify: `stock-service/internal/repository/system_setting_repository.go`
- Test: reuse the existing repository tests if any; add a light adapter test otherwise.

- [ ] **Step 1: Check the current repo.**

```bash
# Read stock-service/internal/repository/system_setting_repository.go and
# confirm Get/Set method names. If they match `SettingStore` (Get/Set), no
# changes needed — the repo IS a SettingStore.
```

- [ ] **Step 2: If the method names differ, add a tiny adapter type in the same file.**

```go
type SettingStoreAdapter struct {
    R *SystemSettingRepository
}

func (a *SettingStoreAdapter) Get(key string) (string, error) { return a.R.Get(key) }
func (a *SettingStoreAdapter) Set(key, value string) error    { return a.R.Set(key, value) }
```

- [ ] **Step 3: Compile + commit.**

```bash
cd stock-service && go build ./...
git add stock-service/internal/repository/system_setting_repository.go
git commit -m "feat(stock-service): SystemSettingRepository satisfies SettingStore"
```

---

## Phase 5 — Source switching (orchestration + admin endpoint)

### Task 5.1: Add `SwitchSource` and `GetSourceStatus` RPCs to the contract

**Files:**
- Modify: `contract/proto/stock/stock.proto`

- [ ] **Step 1: Add the new service or extend an existing one.**

Append to `contract/proto/stock/stock.proto`:

```protobuf
service SourceAdminService {
  rpc SwitchSource(SwitchSourceRequest) returns (SwitchSourceResponse);
  rpc GetSourceStatus(GetSourceStatusRequest) returns (SourceStatus);
}

message SwitchSourceRequest {
  string source = 1; // "external" | "generated" | "simulator"
}

message SwitchSourceResponse {
  SourceStatus status = 1;
}

message GetSourceStatusRequest {}

message SourceStatus {
  string source         = 1; // currently active source
  string status         = 2; // "idle" | "reseeding" | "failed"
  string started_at     = 3; // RFC3339
  string last_error     = 4; // populated when status=failed
}
```

- [ ] **Step 2: Regenerate.**

```bash
make proto
```

- [ ] **Step 3: Verify compile.**

```bash
cd stock-service && go build ./... && cd ../api-gateway && go build ./...
```

- [ ] **Step 4: Commit.**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(contract): add SourceAdminService RPCs"
```

---

### Task 5.2: Wipe repository helper

**Files:**
- Create: `stock-service/internal/repository/wipe_repository.go`
- Test: `stock-service/internal/repository/wipe_repository_test.go`

- [ ] **Step 1: Write the failing test.**

```go
func TestWipeAll_DeletesAllStockAndTradingState(t *testing.T) {
    db := setupTestDB(t)
    // Seed one row in every wiped table.
    db.Create(&model.StockExchange{Acronym: "X", MICCode: "X1", Name: "X"})
    db.Create(&model.Stock{Ticker: "T", Name: "T", Price: decimal.NewFromInt(1)})
    db.Create(&model.Listing{SecurityID: 1, SecurityType: "stock", ExchangeID: 1})
    db.Create(&model.Option{Ticker: "O", Name: "O", StockID: 1, StrikePrice: decimal.Zero, Premium: decimal.Zero, SettlementDate: time.Now()})
    db.Create(&model.FuturesContract{Ticker: "F", Name: "F", Price: decimal.Zero, SettlementDate: time.Now()})
    db.Create(&model.ForexPair{Ticker: "U/E", BaseCurrency: "USD", QuoteCurrency: "EUR", Price: decimal.NewFromFloat(1.08)})
    db.Create(&model.Order{UserID: 1, ListingID: 1, Direction: "buy", OrderType: "market", Quantity: 1})
    db.Create(&model.Holding{UserID: 1, SystemType: "client", UserFirstName: "A", UserLastName: "B", SecurityType: "stock", SecurityID: 1, ListingID: 1, Ticker: "T", Name: "T", Quantity: 1, AveragePrice: decimal.NewFromInt(1), AccountID: 1})
    // (Add minimal rows for OrderTransaction, CapitalGain, TaxCollection too.)

    wipe := repository.NewWipeRepository(db)
    require.NoError(t, wipe.WipeAll())

    tables := []string{"stock_exchanges", "stocks", "listings", "options", "futures_contracts", "forex_pairs", "orders", "holdings", "order_transactions", "capital_gains", "tax_collections"}
    for _, tbl := range tables {
        var n int64
        require.NoError(t, db.Table(tbl).Count(&n).Error)
        require.Equal(t, int64(0), n, "table %s should be empty after wipe", tbl)
    }
}
```

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/repository/ -run TestWipeAll -v
```

Expected: FAIL (function does not exist).

- [ ] **Step 3: Implement.**

Write to `stock-service/internal/repository/wipe_repository.go`:

```go
package repository

import "gorm.io/gorm"

type WipeRepository struct {
    db *gorm.DB
}

func NewWipeRepository(db *gorm.DB) *WipeRepository {
    return &WipeRepository{db: db}
}

// WipeAll deletes every row from securities and user-trading tables owned
// by stock-service. Order matters because of FKs — children first.
// Called only by the admin source-switch flow.
func (r *WipeRepository) WipeAll() error {
    return r.db.Transaction(func(tx *gorm.DB) error {
        // User trading state (children first).
        tables := []string{
            "tax_collections",
            "capital_gains",
            "order_transactions",
            "orders",
            "holdings",
            // Securities (options reference stocks via StockID; listings are
            // referenced by orders/holdings; exchanges are parents of listings).
            "options",
            "listings",
            "forex_pairs",
            "futures_contracts",
            "stocks",
            "stock_exchanges",
        }
        for _, t := range tables {
            if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
                return err
            }
        }
        return nil
    })
}
```

- [ ] **Step 4: Run test + lint + commit.**

```bash
cd stock-service && go test ./internal/repository/ -run TestWipeAll -v
cd stock-service && golangci-lint run ./internal/repository/
git add stock-service/internal/repository/wipe_repository.go stock-service/internal/repository/wipe_repository_test.go
git commit -m "feat(stock-service): add WipeRepository for source switches"
```

---

### Task 5.3: Switch orchestration in `SecuritySyncService`

**Files:**
- Modify: `stock-service/internal/service/security_sync.go`
- Test: `stock-service/internal/service/security_sync_test.go`

- [ ] **Step 1: Add state fields.**

```go
type SecuritySyncService struct {
    // ... existing fields ...
    wipe    *repository.WipeRepository

    switchMu   sync.Mutex
    statusMu   sync.RWMutex
    status     string // "idle" | "reseeding" | "failed"
    lastErr    string
    startedAt  time.Time
    refreshCtx context.CancelFunc // non-nil while the simulator refresh loop is running
}
```

Thread `wipe` through the constructor and from `cmd/main.go`.

- [ ] **Step 2: Write the switch method.**

```go
// SwitchSource atomically switches the active data source. It is
// idempotent-unsafe: concurrent calls return an error on the second caller.
func (s *SecuritySyncService) SwitchSource(ctx context.Context, newSource source.Source) error {
    if !s.switchMu.TryLock() {
        return fmt.Errorf("another switch is in progress")
    }
    defer s.switchMu.Unlock()

    s.setStatus("reseeding", "")
    s.startedAt = time.Now()

    // Stop the current simulator refresh loop if any.
    if s.refreshCtx != nil {
        s.refreshCtx()
        s.refreshCtx = nil
    }

    // Persist the new source choice immediately so restarts pick it up.
    if err := s.settingRepo.Set("active_stock_source", newSource.Name()); err != nil {
        s.setStatus("failed", err.Error())
        return err
    }

    // Wipe.
    if err := s.wipe.WipeAll(); err != nil {
        s.setStatus("failed", "wipe: "+err.Error())
        return err
    }

    // Swap the source pointer.
    s.SetSource(newSource)

    // Reseed synchronously for `generated`; async for others.
    if newSource.Name() == "generated" {
        if err := s.reseedAll(ctx); err != nil {
            s.setStatus("failed", err.Error())
            return err
        }
        s.setStatus("idle", "")
    } else {
        go func() {
            bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
            defer cancel()
            if err := s.reseedAll(bgCtx); err != nil {
                s.setStatus("failed", err.Error())
                return
            }
            s.setStatus("idle", "")
            if newSource.Name() == "simulator" {
                s.startSimulatorRefreshLoop()
            }
        }()
    }

    return nil
}

// reseedAll runs every FetchX on the current source and writes to the DB.
func (s *SecuritySyncService) reseedAll(ctx context.Context) error {
    s.syncExchanges(ctx)
    s.syncStocks(ctx)
    s.seedFutures("")
    s.seedForexPairs()
    s.generateAllOptions()
    if s.listingSvc != nil {
        s.listingSvc.SyncListingsFromSecurities()
    }
    return nil
}

func (s *SecuritySyncService) setStatus(status, errMsg string) {
    s.statusMu.Lock()
    defer s.statusMu.Unlock()
    s.status = status
    s.lastErr = errMsg
}

func (s *SecuritySyncService) GetStatus() (status, lastErr string, startedAt time.Time, sourceName string) {
    s.statusMu.RLock()
    defer s.statusMu.RUnlock()
    return s.status, s.lastErr, s.startedAt, s.Source().Name()
}

func (s *SecuritySyncService) startSimulatorRefreshLoop() {
    ctx, cancel := context.WithCancel(context.Background())
    s.refreshCtx = cancel
    ticker := time.NewTicker(3 * time.Second)
    go func() {
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                s.refreshSimulatorPrices(ctx)
            }
        }
    }()
}

// refreshSimulatorPrices re-fetches and updates only the denormalized price
// columns on listings/stocks/options. Metadata rows are untouched.
func (s *SecuritySyncService) refreshSimulatorPrices(ctx context.Context) {
    src := s.Source()

    stocks, err := src.FetchStocks(ctx)
    if err != nil {
        log.Printf("WARN: refresh stocks: %v", err)
    } else {
        for _, sw := range stocks {
            if err := s.listingSvc.UpdatePriceByTicker("stock", sw.Stock.Ticker,
                sw.Price, sw.High, sw.Low); err != nil {
                log.Printf("WARN: update stock listing %s: %v", sw.Stock.Ticker, err)
            }
            if err := s.stockRepo.UpdatePriceByTicker(sw.Stock.Ticker, sw.Price); err != nil {
                log.Printf("WARN: update stock %s: %v", sw.Stock.Ticker, err)
            }
        }
    }

    futures, err := src.FetchFutures(ctx)
    if err != nil {
        log.Printf("WARN: refresh futures: %v", err)
    } else {
        for _, fw := range futures {
            if err := s.listingSvc.UpdatePriceByTicker("futures", fw.Futures.Ticker,
                fw.Price, fw.High, fw.Low); err != nil {
                log.Printf("WARN: update futures listing %s: %v", fw.Futures.Ticker, err)
            }
            if err := s.futuresRepo.UpdatePriceByTicker(fw.Futures.Ticker, fw.Price); err != nil {
                log.Printf("WARN: update futures %s: %v", fw.Futures.Ticker, err)
            }
        }
    }

    forex, err := src.FetchForex(ctx)
    if err != nil {
        log.Printf("WARN: refresh forex: %v", err)
    } else {
        for _, fx := range forex {
            if err := s.listingSvc.UpdatePriceByTicker("forex", fx.Forex.Ticker,
                fx.Price, fx.High, fx.Low); err != nil {
                log.Printf("WARN: update forex listing %s: %v", fx.Forex.Ticker, err)
            }
            if err := s.forexRepo.UpdatePriceByTicker(fx.Forex.Ticker, fx.Price); err != nil {
                log.Printf("WARN: update forex %s: %v", fx.Forex.Ticker, err)
            }
        }
    }
}
```

Add the missing repository methods referenced above. Each is a simple `UPDATE ... SET price = ? WHERE ticker = ?`:

```go
// stock-service/internal/repository/listing_repository.go
func (r *ListingRepository) UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error {
    // Join with the security table to find the listing by ticker.
    // Use raw SQL because the join target varies by security_type.
    var table string
    switch securityType {
    case "stock":
        table = "stocks"
    case "futures":
        table = "futures_contracts"
    case "forex":
        table = "forex_pairs"
    default:
        return fmt.Errorf("unsupported security_type %q", securityType)
    }
    return r.db.Exec(
        "UPDATE listings l SET price = ?, high = ?, low = ?, last_refresh = NOW() "+
            "FROM "+table+" s WHERE l.security_id = s.id AND l.security_type = ? AND s.ticker = ?",
        price, high, low, securityType, ticker,
    ).Error
}

// stock-service/internal/repository/stock_repository.go
func (r *StockRepository) UpdatePriceByTicker(ticker string, price decimal.Decimal) error {
    return r.db.Model(&model.Stock{}).Where("ticker = ?", ticker).Update("price", price).Error
}

// Add symmetric UpdatePriceByTicker on FuturesRepository and ForexPairRepository.
```

- [ ] **Step 3: Write a failing test for idle→reseeding→idle status transitions with a stub source.**

```go
func TestSwitchSource_StatusTransitions(t *testing.T) {
    db := setupTestDB(t)
    svc := newSyncServiceForTest(t, db)
    stub := &fakeSource{name: "generated"}
    require.NoError(t, svc.SwitchSource(context.Background(), stub))
    status, _, _, srcName := svc.GetStatus()
    require.Equal(t, "idle", status)
    require.Equal(t, "generated", srcName)
}
```

- [ ] **Step 4: Run tests.**

```bash
cd stock-service && go test ./internal/service/ -run TestSwitchSource -v
```

Expected: PASS.

- [ ] **Step 5: Run full stock-service tests + lint + commit.**

```bash
cd stock-service && go test ./... -count=1 && golangci-lint run ./...
git add stock-service/
git commit -m "feat(stock-service): add SwitchSource orchestration"
```

---

### Task 5.4: gRPC handler for `SourceAdminService`

**Files:**
- Create: `stock-service/internal/handler/source_admin_handler.go`
- Modify: `stock-service/cmd/main.go` (register the new gRPC service)
- Test: `stock-service/internal/handler/source_admin_handler_test.go`

- [ ] **Step 1: Write the failing test.**

```go
func TestSourceAdminHandler_SwitchSource_Valid(t *testing.T) {
    svc := &fakeSyncService{}
    h := handler.NewSourceAdminHandler(svc, sourceFactory)
    resp, err := h.SwitchSource(context.Background(), &pb.SwitchSourceRequest{Source: "generated"})
    require.NoError(t, err)
    require.Equal(t, "generated", resp.Status.Source)
}

func TestSourceAdminHandler_SwitchSource_RejectsUnknown(t *testing.T) {
    svc := &fakeSyncService{}
    h := handler.NewSourceAdminHandler(svc, sourceFactory)
    _, err := h.SwitchSource(context.Background(), &pb.SwitchSourceRequest{Source: "garbage"})
    require.Error(t, err)
    st, ok := status.FromError(err)
    require.True(t, ok)
    require.Equal(t, codes.InvalidArgument, st.Code())
}
```

- [ ] **Step 2: Run to confirm failure.**

```bash
cd stock-service && go test ./internal/handler/ -run TestSourceAdminHandler -v
```

- [ ] **Step 3: Implement.**

```go
package handler

import (
    "context"

    pb "github.com/exbanka/contract/stockpb"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    "github.com/exbanka/stock-service/internal/service"
    "github.com/exbanka/stock-service/internal/source"
)

// SourceFactory builds a Source from its name.
type SourceFactory func(name string) (source.Source, error)

type SourceAdminHandler struct {
    pb.UnimplementedSourceAdminServiceServer
    svc     service.SwitchableSyncService // a minimal interface we add in service/ for testability
    factory SourceFactory
}

func NewSourceAdminHandler(svc service.SwitchableSyncService, factory SourceFactory) *SourceAdminHandler {
    return &SourceAdminHandler{svc: svc, factory: factory}
}

func (h *SourceAdminHandler) SwitchSource(ctx context.Context, req *pb.SwitchSourceRequest) (*pb.SwitchSourceResponse, error) {
    if req.Source != "external" && req.Source != "generated" && req.Source != "simulator" {
        return nil, status.Errorf(codes.InvalidArgument, "unknown source %q", req.Source)
    }
    newSrc, err := h.factory(req.Source)
    if err != nil {
        return nil, status.Errorf(codes.FailedPrecondition, "build source: %v", err)
    }
    if err := h.svc.SwitchSource(ctx, newSrc); err != nil {
        return nil, status.Errorf(codes.Internal, "switch: %v", err)
    }
    return &pb.SwitchSourceResponse{Status: h.currentStatus()}, nil
}

func (h *SourceAdminHandler) GetSourceStatus(ctx context.Context, _ *pb.GetSourceStatusRequest) (*pb.SourceStatus, error) {
    return h.currentStatus(), nil
}

func (h *SourceAdminHandler) currentStatus() *pb.SourceStatus {
    statusStr, lastErr, startedAt, name := h.svc.GetStatus()
    return &pb.SourceStatus{
        Source:    name,
        Status:    statusStr,
        StartedAt: startedAt.UTC().Format("2006-01-02T15:04:05Z"),
        LastError: lastErr,
    }
}
```

Add `SwitchableSyncService` interface to `stock-service/internal/service/interfaces.go`:

```go
type SwitchableSyncService interface {
    SwitchSource(ctx context.Context, newSource source.Source) error
    GetStatus() (status, lastErr string, startedAt time.Time, sourceName string)
}
```

- [ ] **Step 4: Register in `cmd/main.go`.**

```go
sourceAdminHandler := handler.NewSourceAdminHandler(syncSvc, func(name string) (source.Source, error) {
    switch name {
    case "external":
        return externalSrc, nil
    case "generated":
        return source.NewGeneratedSource(), nil
    case "simulator":
        client := source.NewSimulatorClient(/* const URL */, /* const name */, settingRepo)
        if err := client.EnsureRegistered(); err != nil {
            return nil, err
        }
        return source.NewSimulatorSource(client), nil
    }
    return nil, fmt.Errorf("unknown source %q", name)
})
pb.RegisterSourceAdminServiceServer(grpcServer, sourceAdminHandler)
```

- [ ] **Step 5: Run tests + lint + commit.**

```bash
cd stock-service && go test ./... -count=1 && golangci-lint run ./...
git add stock-service/
git commit -m "feat(stock-service): add SourceAdminService gRPC handler"
```

---

### Task 5.5: Gateway routes `POST /api/v1/admin/stock-source` and `GET /api/v1/admin/stock-source`

**Files:**
- Create: `api-gateway/internal/handler/stock_source_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`
- Modify: `api-gateway/cmd/main.go` (dial new gRPC client)
- Modify: `api-gateway/internal/grpc/clients.go` (add the client constructor)
- Test: `api-gateway/internal/handler/stock_source_handler_test.go`

- [ ] **Step 1: Add the gRPC client constructor.**

In `api-gateway/internal/grpc/clients.go` (or wherever other clients are built), add:

```go
func NewSourceAdminClient(addr string) (stockpb.SourceAdminServiceClient, *grpc.ClientConn, error) {
    conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        return nil, nil, err
    }
    return stockpb.NewSourceAdminServiceClient(conn), conn, nil
}
```

- [ ] **Step 2: Create the handler.**

```go
package handler

import (
    "net/http"

    "github.com/gin-gonic/gin"

    stockpb "github.com/exbanka/contract/stockpb"
)

type StockSourceHandler struct {
    client stockpb.SourceAdminServiceClient
}

func NewStockSourceHandler(c stockpb.SourceAdminServiceClient) *StockSourceHandler {
    return &StockSourceHandler{client: c}
}

type switchSourceRequest struct {
    Source string `json:"source"`
}

// SwitchSource godoc
// @Summary      Switch the active stock data source
// @Description  Wipes all stock-service + trading state and reseeds from the new source. DESTRUCTIVE.
// @Tags         Admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body switchSourceRequest true "Target source"
// @Success      202 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      503 {object} map[string]interface{}
// @Router       /api/v1/admin/stock-source [post]
func (h *StockSourceHandler) SwitchSource(c *gin.Context) {
    var req switchSourceRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
        return
    }
    if _, err := oneOf("source", req.Source, "external", "generated", "simulator"); err != nil {
        apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
        return
    }
    resp, err := h.client.SwitchSource(c.Request.Context(), &stockpb.SwitchSourceRequest{Source: req.Source})
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    c.JSON(http.StatusAccepted, gin.H{
        "source":     resp.Status.Source,
        "status":     resp.Status.Status,
        "started_at": resp.Status.StartedAt,
        "last_error": resp.Status.LastError,
    })
}

// GetSourceStatus godoc
// @Summary      Get the current stock data source and its status
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/admin/stock-source [get]
func (h *StockSourceHandler) GetSourceStatus(c *gin.Context) {
    resp, err := h.client.GetSourceStatus(c.Request.Context(), &stockpb.GetSourceStatusRequest{})
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    c.JSON(http.StatusOK, gin.H{
        "source":     resp.Source,
        "status":     resp.Status,
        "started_at": resp.StartedAt,
        "last_error": resp.LastError,
    })
}
```

- [ ] **Step 3: Register routes in `router_v1.go`.**

Find the `v1 := r.Group("/api/v1")` block. Add (anywhere after middleware setup):

```go
adminStock := v1.Group("/admin/stock-source")
adminStock.Use(middleware.AuthMiddleware(authClient))
adminStock.Use(middleware.RequirePermission("securities.manage"))
{
    adminStock.POST("", stockSourceHandler.SwitchSource)
    adminStock.GET("", stockSourceHandler.GetSourceStatus)
}
```

Add `stockSourceHandler := handler.NewStockSourceHandler(sourceAdminClient)` next to the other handler constructions. Add `sourceAdminClient stockpb.SourceAdminServiceClient` to `SetupV1Routes`'s parameter list.

- [ ] **Step 4: Wire in `cmd/main.go`.**

Dial and pass the new client into `SetupV1Routes`.

- [ ] **Step 5: Write handler tests covering happy path + 400 on bad source.**

Mock the gRPC client with a local stub. See `api-gateway/internal/handler/*_test.go` for existing patterns.

- [ ] **Step 6: Run tests, `make swagger`, lint, commit.**

```bash
cd api-gateway && go test ./... -count=1
make swagger
cd api-gateway && golangci-lint run ./...
git add api-gateway/
git commit -m "feat(api-gateway): add admin stock-source switch + status routes"
```

---

### Task 5.6: Startup restoration of the active source

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Read `active_stock_source` at boot.**

Before constructing `SecuritySyncService`, add:

```go
active, _ := settingRepo.Get("active_stock_source")
var initialSource source.Source
switch active {
case "generated":
    initialSource = source.NewGeneratedSource()
case "simulator":
    client := source.NewSimulatorClient(simulatorBaseURL, simulatorBankName, settingRepo)
    if err := client.EnsureRegistered(); err != nil {
        log.Printf("WARN: simulator registration failed at boot: %v; falling back to external", err)
        initialSource = source.NewExternalSource(/* ... */)
    } else {
        initialSource = source.NewSimulatorSource(client)
    }
default:
    initialSource = source.NewExternalSource(/* ... */)
}
```

After `SecuritySyncService` is constructed, if `initialSource.Name() == "simulator"`, start the 3s refresh loop.

- [ ] **Step 2: Compile + commit.**

```bash
cd stock-service && go build ./...
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): restore active data source at boot"
```

---

### Task 5.7: REST_API_v1.md update for admin route

**Files:**
- Modify: `docs/api/REST_API_v1.md`

- [ ] **Step 1: Append a section documenting both routes.**

Add a new `## Admin — Stock Data Source` section with request/response examples for `POST /api/v1/admin/stock-source` and `GET /api/v1/admin/stock-source`. Match the format of existing sections.

- [ ] **Step 2: Commit.**

```bash
git add docs/api/REST_API_v1.md
git commit -m "docs: document admin stock-source routes in REST_API_v1"
```

---

### Task 5.8: Integration test — source switch + verification

**Files:**
- Create: `test-app/workflows/stock_source_switch_test.go`

- [ ] **Step 1: Write the test.**

```go
func TestStockSource_SwitchToGenerated(t *testing.T) {
    ctx := t.Context()
    admin := loginAsAdmin(t, ctx)

    // Switch to generated.
    resp := doJSON(t, ctx, "POST", "/api/v1/admin/stock-source",
        map[string]string{"source": "generated"}, admin.Token)
    require.Equal(t, 202, resp.StatusCode)

    waitForSourceStatus(t, ctx, admin.Token, "idle", 10*time.Second)

    // Verify stocks are the 20 generated tickers.
    stocks := doJSON(t, ctx, "GET", "/api/v1/securities/stocks?page=1&page_size=50", nil, admin.Token)
    require.Equal(t, 200, stocks.StatusCode)
    body := decodeStocksList(t, stocks)
    require.Len(t, body.Stocks, 20)
    require.Contains(t, tickersOf(body.Stocks), "AAPL")

    // Verify options exist for at least one stock.
    opts := doJSON(t, ctx, "GET", "/api/v1/securities/options?stock_id="+strconv.Itoa(body.Stocks[0].ID), nil, admin.Token)
    require.Equal(t, 200, opts.StatusCode)
    optsBody := decodeOptionsList(t, opts)
    require.NotEmpty(t, optsBody.Options)
}
```

Use existing helpers from `test-app/workflows/helpers_test.go`. If any needed helper (e.g., `waitForSourceStatus`) does not exist, add it in the same file using the existing HTTP-polling patterns.

- [ ] **Step 2: Run the test.**

```bash
cd test-app && go test ./workflows/ -run TestStockSource_SwitchToGenerated -v
```

Expected: PASS.

- [ ] **Step 3: Commit.**

```bash
git add test-app/workflows/
git commit -m "test: integration test for stock source switch to generated"
```

---

## Phase 6 — v2 router + option order route

### Task 6.1: Create `router_v2.go` with fallback middleware

**Files:**
- Create: `api-gateway/internal/router/router_v2.go`
- Modify: `api-gateway/cmd/main.go`

- [ ] **Step 1: Create the file.**

```go
package router

import (
    "net/http"
    "strings"

    "github.com/gin-gonic/gin"

    "github.com/exbanka/api-gateway/internal/handler"
    "github.com/exbanka/api-gateway/internal/middleware"
    authpb "github.com/exbanka/contract/authpb"
    stockpb "github.com/exbanka/contract/stockpb"
)

// SetupV2Routes registers the v2-only route additions on the engine and
// installs a NoRoute handler that rewrites unknown /api/v2/* paths to v1.
// Call AFTER SetupV1Routes.
func SetupV2Routes(
    r *gin.Engine,
    authClient authpb.AuthServiceClient,
    securityClient stockpb.SecurityGRPCServiceClient,
    orderClient stockpb.OrderGRPCServiceClient,
    portfolioClient stockpb.PortfolioGRPCServiceClient,
) {
    optionsV2 := handler.NewOptionsV2Handler(securityClient, orderClient, portfolioClient)

    v2 := r.Group("/api/v2")
    {
        opts := v2.Group("/options")
        opts.Use(middleware.AnyAuthMiddleware(authClient))
        opts.Use(middleware.RequirePermission("securities.trade"))
        {
            opts.POST("/:option_id/orders", optionsV2.CreateOrder)
            opts.POST("/:option_id/exercise", optionsV2.Exercise)
        }
    }

    // Fallback: any /api/v2/* not matched above → rewrite to /api/v1/* and re-dispatch.
    r.NoRoute(func(c *gin.Context) {
        if strings.HasPrefix(c.Request.URL.Path, "/api/v2/") {
            c.Request.URL.Path = "/api/v1/" + strings.TrimPrefix(c.Request.URL.Path, "/api/v2/")
            r.HandleContext(c)
            return
        }
        c.JSON(http.StatusNotFound, gin.H{
            "error": gin.H{"code": "not_found", "message": "route not found"},
        })
    })
}
```

- [ ] **Step 2: Call `SetupV2Routes` from `cmd/main.go` after `SetupV1Routes`.**

- [ ] **Step 3: Compile check.**

```bash
cd api-gateway && go build ./...
```

Expected: compilation fails because `handler.NewOptionsV2Handler` does not exist yet — that's fine, we create it in Task 6.2.

- [ ] **Step 4: No commit yet — finish Task 6.2 first, then commit both together.**

---

### Task 6.2: `OptionsV2Handler` with `CreateOrder` and `Exercise`

**Files:**
- Create: `api-gateway/internal/handler/options_v2_handler.go`
- Test: `api-gateway/internal/handler/options_v2_handler_test.go`

- [ ] **Step 1: Write failing tests.**

```go
func TestOptionsV2_CreateOrder_Valid(t *testing.T) {
    secClient := &stubSecurityClient{
        getOption: func(req *stockpb.GetOptionRequest) *stockpb.OptionDetail {
            lid := uint64(77)
            return &stockpb.OptionDetail{Id: req.Id, Ticker: "AAPL260116C00200000", ListingId: &lid}
        },
    }
    ordClient := &stubOrderClient{
        createOrder: func(req *stockpb.CreateOrderRequest) *stockpb.Order {
            require.Equal(t, uint64(77), req.ListingId)
            require.Equal(t, "buy", req.Direction)
            return &stockpb.Order{Id: 999, ListingId: 77}
        },
    }
    h := handler.NewOptionsV2Handler(secClient, ordClient, nil)
    router := gin.New()
    router.POST("/api/v2/options/:option_id/orders", h.CreateOrder)
    body := `{"direction":"buy","order_type":"market","quantity":1,"account_id":42}`
    req := httptest.NewRequest("POST", "/api/v2/options/5/orders", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    require.Equal(t, 201, rec.Code)
}

func TestOptionsV2_CreateOrder_RejectsWithoutListing(t *testing.T) {
    secClient := &stubSecurityClient{
        getOption: func(req *stockpb.GetOptionRequest) *stockpb.OptionDetail {
            return &stockpb.OptionDetail{Id: req.Id, Ticker: "X"}
        },
    }
    h := handler.NewOptionsV2Handler(secClient, &stubOrderClient{}, nil)
    router := gin.New()
    router.POST("/api/v2/options/:option_id/orders", h.CreateOrder)
    body := `{"direction":"buy","order_type":"market","quantity":1,"account_id":42}`
    req := httptest.NewRequest("POST", "/api/v2/options/5/orders", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    require.Equal(t, 409, rec.Code)
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement.**

```go
package handler

import (
    "net/http"
    "strconv"

    "github.com/gin-gonic/gin"

    stockpb "github.com/exbanka/contract/stockpb"
)

type OptionsV2Handler struct {
    secClient  stockpb.SecurityGRPCServiceClient
    ordClient  stockpb.OrderGRPCServiceClient
    portClient stockpb.PortfolioGRPCServiceClient
}

func NewOptionsV2Handler(
    sec stockpb.SecurityGRPCServiceClient,
    ord stockpb.OrderGRPCServiceClient,
    port stockpb.PortfolioGRPCServiceClient,
) *OptionsV2Handler {
    return &OptionsV2Handler{secClient: sec, ordClient: ord, portClient: port}
}

type createOptionOrderRequest struct {
    Direction  string `json:"direction"`
    OrderType  string `json:"order_type"`
    Quantity   int64  `json:"quantity"`
    LimitValue string `json:"limit_value,omitempty"`
    StopValue  string `json:"stop_value,omitempty"`
    AllOrNone  bool   `json:"all_or_none"`
    Margin     bool   `json:"margin"`
    AccountID  uint64 `json:"account_id"`
    HoldingID  uint64 `json:"holding_id,omitempty"`
}

// CreateOrder godoc
// @Summary      Create an order for an option contract (v2)
// @Tags         Options V2
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        option_id path  int true "Option ID"
// @Param        body      body createOptionOrderRequest true "Order"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v2/options/{option_id}/orders [post]
func (h *OptionsV2Handler) CreateOrder(c *gin.Context) {
    optionID, err := strconv.ParseUint(c.Param("option_id"), 10, 64)
    if err != nil || optionID == 0 {
        apiError(c, http.StatusBadRequest, ErrValidation, "invalid option_id")
        return
    }

    var req createOptionOrderRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
        return
    }
    if _, err := oneOf("direction", req.Direction, "buy", "sell"); err != nil {
        apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
        return
    }
    if _, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit"); err != nil {
        apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
        return
    }
    if req.Quantity <= 0 {
        apiError(c, http.StatusBadRequest, ErrValidation, "quantity must be positive")
        return
    }
    if req.AccountID == 0 {
        apiError(c, http.StatusBadRequest, ErrValidation, "account_id is required")
        return
    }
    if req.OrderType == "limit" || req.OrderType == "stop_limit" {
        if req.LimitValue == "" {
            apiError(c, http.StatusBadRequest, ErrValidation, "limit_value required for limit/stop_limit")
            return
        }
    }
    if req.OrderType == "stop" || req.OrderType == "stop_limit" {
        if req.StopValue == "" {
            apiError(c, http.StatusBadRequest, ErrValidation, "stop_value required for stop/stop_limit")
            return
        }
    }

    opt, err := h.secClient.GetOption(c.Request.Context(), &stockpb.GetOptionRequest{Id: optionID})
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    if opt.ListingId == nil || *opt.ListingId == 0 {
        apiError(c, http.StatusConflict, "business_rule_violation", "option not tradeable")
        return
    }

    userID := userIDFromCtx(c)
    systemType := systemTypeFromCtx(c)

    createReq := &stockpb.CreateOrderRequest{
        UserId:     userID,
        SystemType: systemType,
        ListingId:  *opt.ListingId,
        HoldingId:  req.HoldingID,
        Direction:  req.Direction,
        OrderType:  req.OrderType,
        Quantity:   req.Quantity,
        AllOrNone:  req.AllOrNone,
        Margin:     req.Margin,
        AccountId:  req.AccountID,
    }
    if req.LimitValue != "" {
        createReq.LimitValue = &req.LimitValue
    }
    if req.StopValue != "" {
        createReq.StopValue = &req.StopValue
    }

    order, err := h.ordClient.CreateOrder(c.Request.Context(), createReq)
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    c.JSON(http.StatusCreated, order)
}

// Exercise is implemented in Task 7.4.
func (h *OptionsV2Handler) Exercise(c *gin.Context) {
    c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"code": "not_implemented", "message": "see phase 7"}})
}
```

- [ ] **Step 4: Run tests, make swagger, lint, commit both files.**

```bash
cd api-gateway && go test ./internal/handler/ -run TestOptionsV2 -v
make swagger
cd api-gateway && golangci-lint run ./...
git add api-gateway/
git commit -m "feat(api-gateway): add v2 router with option create-order route"
```

---

### Task 6.3: Integration test — v2 options order

**Files:**
- Create: `test-app/workflows/options_order_v2_test.go`

- [ ] **Step 1: Write the test.**

```go
func TestOptionsV2_CreateOrder_EndToEnd(t *testing.T) {
    ctx := t.Context()
    admin := loginAsAdmin(t, ctx)
    client := loginAsTestClient(t, ctx)

    // Ensure we're on the generated source so an option exists with a listing.
    switchTo(t, ctx, admin.Token, "generated")

    // Find an option to buy.
    stocks := getJSON(t, ctx, "/api/v1/securities/stocks?page=1&page_size=1", admin.Token)
    stockID := stocks.Stocks[0].ID
    opts := getJSON(t, ctx, "/api/v1/securities/options?stock_id="+strconv.Itoa(stockID), admin.Token)
    require.NotEmpty(t, opts.Options)
    optID := opts.Options[0].ID

    // Place the v2 order.
    accountID := getRSDAccountID(t, ctx, client.Token)
    body := map[string]any{
        "direction":  "buy",
        "order_type": "market",
        "quantity":   1,
        "margin":     true,
        "account_id": accountID,
    }
    resp := doJSON(t, ctx, "POST", "/api/v2/options/"+strconv.Itoa(optID)+"/orders", body, client.Token)
    require.Equal(t, 201, resp.StatusCode)

    // Employee approves.
    approveLatestOrder(t, ctx, admin.Token)

    // Holding appears in portfolio.
    portfolio := getJSON(t, ctx, "/api/v1/me/portfolio", client.Token)
    require.True(t, portfolioContainsOption(portfolio, optID), "expected option holding in portfolio")
}
```

Use existing helpers. Add any new helpers (e.g., `switchTo`, `portfolioContainsOption`) in the same file.

- [ ] **Step 2: Run + commit.**

```bash
cd test-app && go test ./workflows/ -run TestOptionsV2_CreateOrder_EndToEnd -v
git add test-app/workflows/
git commit -m "test: integration test for v2 option order creation"
```

---

### Task 6.4: Integration test — v2 fallback to v1

**Files:**
- Create: `test-app/workflows/v2_fallback_test.go`

- [ ] **Step 1: Write the test.**

```go
func TestV2_FallsBackToV1(t *testing.T) {
    ctx := t.Context()
    admin := loginAsAdmin(t, ctx)

    v1Resp := doJSON(t, ctx, "GET", "/api/v1/securities/stocks?page=1&page_size=3", nil, admin.Token)
    v2Resp := doJSON(t, ctx, "GET", "/api/v2/securities/stocks?page=1&page_size=3", nil, admin.Token)

    require.Equal(t, v1Resp.StatusCode, v2Resp.StatusCode)
    require.JSONEq(t, readBody(t, v1Resp), readBody(t, v2Resp))
}
```

- [ ] **Step 2: Run + commit.**

```bash
cd test-app && go test ./workflows/ -run TestV2_FallsBackToV1 -v
git add test-app/workflows/
git commit -m "test: v2 router falls back to v1 for unknown paths"
```

---

## Phase 7 — v2 option exercise + new RPC

### Task 7.1: Add `ExerciseOptionByOptionID` RPC

**Files:**
- Modify: `contract/proto/stock/stock.proto`

- [ ] **Step 1: Add the RPC and message.**

Append to the `PortfolioGRPCService` service (or wherever `ExerciseOption` currently lives):

```protobuf
rpc ExerciseOptionByOptionID(ExerciseOptionByOptionIDRequest) returns (ExerciseResult);

message ExerciseOptionByOptionIDRequest {
  uint64 option_id  = 1;
  uint64 user_id    = 2;
  uint64 holding_id = 3; // optional; 0 means auto-resolve
}
```

- [ ] **Step 2: Regen + compile.**

```bash
make proto
cd stock-service && go build ./... && cd ../api-gateway && go build ./...
```

- [ ] **Step 3: Commit.**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(contract): add ExerciseOptionByOptionID RPC"
```

---

### Task 7.2: Stock-service implementation

**Files:**
- Modify: `stock-service/internal/handler/portfolio_handler.go`
- Modify: `stock-service/internal/service/portfolio_service.go`
- Test: `stock-service/internal/service/portfolio_service_test.go`

- [ ] **Step 1: Write the failing test.**

```go
func TestExerciseOptionByOptionID_AutoResolvesHolding(t *testing.T) {
    db := setupTestDB(t)
    seedOptionHolding(t, db, /* user_id= */ 42, /* option_id= */ 7)

    svc := service.NewPortfolioService(/* real deps */)
    result, err := svc.ExerciseOptionByOptionID(context.Background(), 7, 42, 0)
    require.NoError(t, err)
    require.NotNil(t, result)
}

func TestExerciseOptionByOptionID_NotFound(t *testing.T) {
    db := setupTestDB(t)
    svc := service.NewPortfolioService(/* real deps */)
    _, err := svc.ExerciseOptionByOptionID(context.Background(), 7, 42, 0)
    require.ErrorIs(t, err, shared.ErrNotFound)
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement the service method.**

```go
func (s *PortfolioService) ExerciseOptionByOptionID(ctx context.Context, optionID, userID, holdingID uint64) (*model.ExerciseResult, error) {
    if holdingID > 0 {
        // Delegate to existing ExerciseOption path.
        return s.exerciseOption(ctx, holdingID, userID)
    }
    // Auto-resolve: find the user's oldest long holding on this option.
    var h model.Holding
    err := s.db.
        Where("user_id = ? AND security_type = ? AND security_id = ? AND quantity > 0", userID, "option", optionID).
        Order("created_at ASC").
        First(&h).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        return nil, shared.ErrNotFound
    }
    if err != nil {
        return nil, err
    }
    return s.exerciseOption(ctx, h.ID, userID)
}
```

- [ ] **Step 4: Implement the gRPC handler.**

```go
func (h *PortfolioHandler) ExerciseOptionByOptionID(ctx context.Context, req *pb.ExerciseOptionByOptionIDRequest) (*pb.ExerciseResult, error) {
    result, err := h.svc.ExerciseOptionByOptionID(ctx, req.OptionId, req.UserId, req.HoldingId)
    if err != nil {
        return nil, mapServiceErr(err)
    }
    return toExerciseResultPB(result), nil
}
```

- [ ] **Step 5: Run tests + lint + commit.**

```bash
cd stock-service && go test ./... -count=1 && golangci-lint run ./...
git add stock-service/
git commit -m "feat(stock-service): implement ExerciseOptionByOptionID with auto-resolve"
```

---

### Task 7.3: Wire v2 exercise route in gateway

**Files:**
- Modify: `api-gateway/internal/handler/options_v2_handler.go` (replace the stub from Task 6.2)
- Test: `api-gateway/internal/handler/options_v2_handler_test.go`

- [ ] **Step 1: Write the failing test.**

```go
func TestOptionsV2_Exercise_HappyPath(t *testing.T) {
    portClient := &stubPortfolioClient{
        exerciseByOptionID: func(req *stockpb.ExerciseOptionByOptionIDRequest) *stockpb.ExerciseResult {
            require.Equal(t, uint64(5), req.OptionId)
            return &stockpb.ExerciseResult{Id: 1, OptionTicker: "X", ExercisedQuantity: 1, SharesAffected: 100, Profit: "42.00"}
        },
    }
    h := handler.NewOptionsV2Handler(&stubSecurityClient{}, &stubOrderClient{}, portClient)
    router := gin.New()
    router.POST("/api/v2/options/:option_id/exercise", h.Exercise)
    body := `{"holding_id":0}`
    req := httptest.NewRequest("POST", "/api/v2/options/5/exercise", strings.NewReader(body))
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    require.Equal(t, 200, rec.Code)
}
```

- [ ] **Step 2: Implement.**

Replace the `Exercise` stub:

```go
type exerciseOptionRequest struct {
    HoldingID uint64 `json:"holding_id"`
}

// Exercise godoc
// @Summary      Exercise an option by option ID (v2)
// @Tags         Options V2
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        option_id path int true "Option ID"
// @Param        body body exerciseOptionRequest false "Holding ID (optional)"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v2/options/{option_id}/exercise [post]
func (h *OptionsV2Handler) Exercise(c *gin.Context) {
    optionID, err := strconv.ParseUint(c.Param("option_id"), 10, 64)
    if err != nil || optionID == 0 {
        apiError(c, http.StatusBadRequest, ErrValidation, "invalid option_id")
        return
    }
    var req exerciseOptionRequest
    _ = c.ShouldBindJSON(&req) // body optional
    userID := userIDFromCtx(c)

    result, err := h.portClient.ExerciseOptionByOptionID(c.Request.Context(), &stockpb.ExerciseOptionByOptionIDRequest{
        OptionId:  optionID,
        UserId:    userID,
        HoldingId: req.HoldingID,
    })
    if err != nil {
        handleGRPCError(c, err)
        return
    }
    c.JSON(http.StatusOK, result)
}
```

- [ ] **Step 3: Run tests, make swagger, lint, commit.**

```bash
cd api-gateway && go test ./... -count=1
make swagger
cd api-gateway && golangci-lint run ./...
git add api-gateway/
git commit -m "feat(api-gateway): implement v2 option exercise route"
```

---

### Task 7.4: Integration test — v2 exercise

**Files:**
- Create: `test-app/workflows/options_exercise_v2_test.go`

- [ ] **Step 1: Write the test.**

```go
func TestOptionsV2_Exercise_EndToEnd(t *testing.T) {
    ctx := t.Context()
    admin := loginAsAdmin(t, ctx)
    client := loginAsTestClient(t, ctx)

    switchTo(t, ctx, admin.Token, "generated")

    // Buy an option first (reuse helper from Task 6.3).
    optID := buyOneOption(t, ctx, client.Token, admin.Token)

    // Exercise it.
    body := map[string]any{}
    resp := doJSON(t, ctx, "POST", "/api/v2/options/"+strconv.Itoa(optID)+"/exercise", body, client.Token)
    require.Equal(t, 200, resp.StatusCode)
}
```

- [ ] **Step 2: Run + commit.**

```bash
cd test-app && go test ./workflows/ -run TestOptionsV2_Exercise_EndToEnd -v
git add test-app/workflows/
git commit -m "test: integration test for v2 option exercise"
```

---

## Phase 8 — Documentation and finalization

### Task 8.1: Create `docs/api/REST_API_v2.md`

**Files:**
- Create: `docs/api/REST_API_v2.md`

- [ ] **Step 1: Write the file.**

```markdown
# REST API v2

> **Note:** Any path not listed here transparently falls back to `/api/v1`.
> See `REST_API_v1.md` for the full v1 surface.

Base URL: `/api/v2`

## Options

### Create an option order

`POST /api/v2/options/:option_id/orders`

**Auth:** `AnyAuthMiddleware` + permission `securities.trade`.

**Path params:**
- `option_id` (uint) — the option's ID from `GET /api/v1/securities/options?stock_id=...`.

**Body:**
```json
{
  "direction": "buy | sell",
  "order_type": "market | limit | stop | stop_limit",
  "quantity": 1,
  "limit_value": "5.75",
  "stop_value": "6.00",
  "all_or_none": false,
  "margin": true,
  "account_id": 42,
  "holding_id": 0
}
```

(rest of section documents 201, 400, 409 responses)

### Exercise an option

`POST /api/v2/options/:option_id/exercise`

(document shape)
```

- [ ] **Step 2: Commit.**

```bash
git add docs/api/REST_API_v2.md
git commit -m "docs: add REST_API_v2 for new option routes"
```

---

### Task 8.2: Update `Specification.md`

**Files:**
- Modify: `Specification.md`

- [ ] **Step 1: Update Sections per CLAUDE.md Section 1 instructions.**

For each section below, locate the relevant spec section and add a paragraph:
- **Section 6 (permissions)** — new `securities.manage` permission, assigned to `EmployeeAdmin`.
- **Section 11 (gRPC services)** — new `SourceAdminService` and new RPC `ExerciseOptionByOptionID`.
- **Section 17 (API routes)** — new routes `/api/v1/admin/stock-source` (GET/POST) and `/api/v2/options/:id/orders`, `/api/v2/options/:id/exercise`; note the v2 fallback-to-v1 behavior.
- **Section 18 (entities)** — `Option` gains optional `ListingID`; `Listing.SecurityType` includes `"option"`.
- **Section 21 (business rules)** — source switch wipes stock-service + trading state; v1 must remain backward-compatible (cross-reference new CLAUDE.md section).

- [ ] **Step 2: Commit.**

```bash
git add Specification.md
git commit -m "docs: update Specification.md for source abstraction + v2 options"
```

---

### Task 8.3: Regenerate swagger + final verification

- [ ] **Step 1: Regenerate swagger and commit if the file changed.**

```bash
make swagger
git status
# If api-gateway/docs/swagger.{json,yaml,go} changed:
git add api-gateway/docs/
git commit -m "chore(api-gateway): regenerate swagger"
```

- [ ] **Step 2: Full lint pass.**

```bash
make lint
```

Fix any lint errors in files we touched. Do not fix pre-existing errors in files we did not modify.

- [ ] **Step 3: Full test suite.**

```bash
make test
```

Expected: all tests pass.

- [ ] **Step 4: Integration suite.**

```bash
cd test-app && go test ./workflows/ -count=1
```

Expected: all tests pass.

- [ ] **Step 5: Final smoke run locally.**

```bash
make docker-up
# Wait ~10s for services to come up.
curl -s -i -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@admin.com","password":"AdminAdmin2026!."}'
# Copy the access_token.
TOKEN=...
curl -s -i -X POST http://localhost:8080/api/v1/admin/stock-source \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source":"generated"}'
# Expected: 202 Accepted.
curl -s -i "http://localhost:8080/api/v1/admin/stock-source" \
  -H "Authorization: Bearer $TOKEN"
# Expected: 200 with source=generated, status=idle.
curl -s -i "http://localhost:8080/api/v1/securities/stocks?page=1&page_size=3" \
  -H "Authorization: Bearer $TOKEN"
# Expected: 20 stocks including AAPL.
curl -s -i "http://localhost:8080/api/v2/securities/stocks?page=1&page_size=3" \
  -H "Authorization: Bearer $TOKEN"
# Expected: identical payload to v1 (fallback works).
```

- [ ] **Step 6: No commit for the smoke run.**

---

## Done-criteria checklist

- [ ] CLAUDE.md has the API versioning section.
- [ ] `securities.manage` permission is seeded on `EmployeeAdmin`.
- [ ] `Option` model has optional `ListingID`; `Listing.SecurityType` accepts `"option"`.
- [ ] `OptionItem` and `OptionDetail` protos have optional `listing_id` and stock-service populates it.
- [ ] `internal/source/` package exists with `types.go`, `external_source.go`, `generated_source.go`, `generated_data.go`, `simulator_client.go`, `simulator_source.go`.
- [ ] `SecuritySyncService` uses `source.Source` exclusively.
- [ ] `SourceAdminService` gRPC + `POST/GET /api/v1/admin/stock-source` routes work.
- [ ] Switching to `generated` reseeds synchronously; switching to `simulator` starts the 3s refresh goroutine.
- [ ] `router_v2.go` exists with `SetupV2Routes`. `NoRoute` rewrites unknown v2 paths to v1.
- [ ] `POST /api/v2/options/:option_id/orders` and `POST /api/v2/options/:option_id/exercise` work.
- [ ] `ExerciseOptionByOptionID` RPC added and implemented with auto-resolve when `holding_id=0`.
- [ ] Portfolio audit findings documented in the executing agent's summary.
- [ ] `REST_API_v2.md` exists. `REST_API_v1.md` updated for admin routes only. `Specification.md` updated.
- [ ] `make lint`, `make test`, and `test-app/workflows/` all pass.
- [ ] Smoke run against Docker Compose confirms the admin switch and v2 fallback.
