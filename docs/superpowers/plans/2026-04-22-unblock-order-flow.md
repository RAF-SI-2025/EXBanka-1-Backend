# Unblock Order Flow — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the three bugs in `docs/Bugs.txt` that keep the securities order pipeline from running end-to-end: #1 (ListingInfo.id not populated → clients send the wrong listing_id → 404), #2 (generated source never assigns Volume → simulated market behaviour wrong), #3 (execution goroutine context derived from request ctx → goroutine exits before first fill). No money-safety changes here — that is Phase 2 (`2026-04-22-bank-safe-settlement.md`).

**Architecture:** Pure in-service bug fixes scoped to stock-service. Wire listing IDs through the `toListingInfo` helper and its callers; add deterministic volume hashing to the generated source; introduce a `baseCtx` field on the order-execution engine so the fill goroutine outlives the gRPC request ctx. No cross-service RPCs, no schema changes, no proto changes.

**Tech Stack:** Go 1.22, GORM, gRPC, `decimal.Decimal` (shopspring), FNV-32a hashing, existing `contract/testutil/` test doubles.

**Design spec:** `docs/superpowers/specs/2026-04-22-securities-bank-safety-design.md` — §3 covers Phase 1 scope and §9 defines non-goals.

**Do not commit in this planning step.** The plan includes normal TDD commit steps for the engineer executing it; authorization to commit is granted when the engineer runs the plan, not by writing the plan.

---

## Context

Relevant files before changes:

- `stock-service/internal/handler/security_handler.go:298` — `toListingInfo()` signature missing listing ID parameter; all mapper functions propagate the security-table PK as `pb.ListingInfo.id` via the zero value.
- `stock-service/internal/source/generated_source.go:198` / `:223` / `:255` — `FetchStocks`/`FetchFutures`/`FetchForex` never set `Volume` on either the security model or the `*WithListing` wrapper.
- `stock-service/internal/service/order_execution.go:66-78` — `StartOrderExecution` derives the goroutine context from the caller's ctx, which is the gRPC request ctx in the common `CreateOrder`/`ApproveOrder` paths.
- `stock-service/internal/repository/listing_repository.go:34` — `GetBySecurityIDAndType(id, type)` **already exists** and is what we need to resolve listing IDs; we don't have to add a new repo method.
- `stock-service/internal/model/{stock,futures_contract,forex_pair,listing}.go` — `Volume int64 gorm:"not null;default:0"` on all four; no schema migration needed, just populate non-zero values.
- `stock-service/internal/service/security_sync.go:203` — `syncStocks` upserts from the wrapper's `sw.Stock` field. Volume must be set on `sw.Stock` (not only on the wrapper) for persistence to take effect.
- `stock-service/cmd/main.go:278-303` — main already constructs a long-lived `ctx, cancel := context.WithCancel(context.Background())`; the engine constructor needs to accept that ctx.

Test patterns in this service: unit tests live next to code (`*_test.go`), mocked repositories through interfaces defined in the service package (search for `type *Repo interface` patterns), integration tests in `test-app/workflows/`.

---

## File Structure

**Files to modify:**
- `stock-service/internal/handler/security_handler.go` — add `listingID uint64` to `toListingInfo`; resolve IDs in all `to*Item`/`to*Detail` callers
- `stock-service/internal/source/generated_source.go` — add `hashVolume` helper; populate `Volume` on all three security types and wrappers
- `stock-service/internal/source/types.go` — add `Volume int64` to `StockWithListing`/`FuturesWithListing`/`ForexWithListing`
- `stock-service/internal/service/order_execution.go` — add `baseCtx context.Context` field; accept it in `NewOrderExecutionEngine`; use it in `StartOrderExecution`
- `stock-service/cmd/main.go` — pass the existing long-lived ctx into `NewOrderExecutionEngine`
- `docs/Specification.md` — add Phase-1 banner noting the fill path is still best-effort until Phase 2 ships (per spec §8.5)

**Files to create:**
- `stock-service/internal/source/generated_volume_test.go` — new test for deterministic volume hashing
- `stock-service/internal/service/order_execution_baseccx_test.go` — regression test for bug #3
- `stock-service/internal/handler/security_handler_listing_id_test.go` — regression test for bug #1

**Files to update as test cascades:**
- `stock-service/internal/handler/security_handler_test.go` — existing tests may reference mapper helpers; update call sites
- `stock-service/internal/service/security_sync_test.go:270-290` — `stubSource` implements the Source interface; update if the interface changes (it does not, but the wrappers gain a new field)

---

## Task 1: Bug #1a — Extend toListingInfo with listingID param

**Files:**
- Modify: `stock-service/internal/handler/security_handler.go:298-312`
- Test: `stock-service/internal/handler/security_handler_listing_id_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `stock-service/internal/handler/security_handler_listing_id_test.go`:

```go
package handler

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestToListingInfo_PopulatesID(t *testing.T) {
	info := toListingInfo(
		42,                              // listingID — NEW leading parameter
		7,                               // exchangeID
		"NYSE",                          // exchangeAcronym
		decimal.NewFromInt(100),         // price
		decimal.NewFromInt(101),         // high
		decimal.NewFromInt(99),          // low
		decimal.NewFromInt(1),           // change
		1_234_567,                       // volume
		decimal.NewFromInt(50),          // initialMarginCost
		time.Unix(1_700_000_000, 0).UTC(), // lastRefresh
	)
	if info.Id != 42 {
		t.Fatalf("expected Id=42, got %d", info.Id)
	}
	if info.Volume != 1_234_567 {
		t.Fatalf("expected Volume=1234567, got %d", info.Volume)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd stock-service && go test ./internal/handler/ -run TestToListingInfo_PopulatesID -v`

Expected: FAIL (compile error: too many arguments, or `info.Id` is `0`).

- [ ] **Step 3: Modify toListingInfo to accept listingID**

Replace the function at `stock-service/internal/handler/security_handler.go:298-312`:

```go
func toListingInfo(
	listingID uint64,
	exchangeID uint64,
	exchangeAcronym string,
	price, high, low, change decimal.Decimal,
	volume int64,
	initialMarginCost decimal.Decimal,
	lastRefresh time.Time,
) *pb.ListingInfo {
	changePercent := service.StockChangePercent(price, change)
	return &pb.ListingInfo{
		Id:                listingID,
		ExchangeId:        exchangeID,
		ExchangeAcronym:   exchangeAcronym,
		Price:             price.StringFixed(4),
		High:              high.StringFixed(4),
		Low:               low.StringFixed(4),
		Change:            change.StringFixed(4),
		ChangePercent:     changePercent.StringFixed(2),
		Volume:            volume,
		InitialMarginCost: initialMarginCost.StringFixed(2),
		LastRefresh:       lastRefresh.Format(time.RFC3339),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd stock-service && go test ./internal/handler/ -run TestToListingInfo_PopulatesID -v`

Expected: FAIL — callers of `toListingInfo` no longer compile until Task 2 fixes them. That's OK; Task 2 is the follow-up.

- [ ] **Step 5: No commit yet**

We wait until Task 2 so the build is green.

---

## Task 2: Bug #1b — Pass listing IDs into every mapper callsite

**Files:**
- Modify: `stock-service/internal/handler/security_handler.go:314-418` (all `to*Item`/`to*Detail`)
- Modify: any handler method that calls these mappers (the handler methods are further up in the same file — they currently have `s *model.Stock` etc; we need to look up the listing ID and pass it in)
- Test: `stock-service/internal/handler/security_handler_listing_id_test.go` (extend)

- [ ] **Step 1: Find all mapper call sites**

Run: `cd stock-service && grep -n "toStockItem\|toStockDetail\|toFuturesItem\|toFuturesDetail\|toForexPairItem\|toForexPairDetail" internal/handler/security_handler.go`

Expected: each mapper is called from one or more handler methods (e.g., `ListStocks`, `GetStock`). Note each call site — you'll update them.

- [ ] **Step 2: Update mapper signatures to accept the listingID**

Replace each of the six mapper helpers in `stock-service/internal/handler/security_handler.go:314-418`. Full replacement:

```go
func toStockItem(s *model.Stock, listingID uint64) *pb.StockItem {
	return &pb.StockItem{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		Listing: toListingInfo(
			listingID,
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
	}
}

func toStockDetail(s *model.Stock, options []model.Option, listingID uint64) *pb.StockDetail {
	optItems := make([]*pb.OptionItem, len(options))
	for i, o := range options {
		optItems[i] = toOptionItem(&o)
	}
	return &pb.StockDetail{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		MarketCap:         s.MarketCap().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
		Options: optItems,
	}
}

func toFuturesItem(f *model.FuturesContract, listingID uint64) *pb.FuturesItem {
	return &pb.FuturesItem{
		Id:             f.ID,
		Ticker:         f.Ticker,
		Name:           f.Name,
		ContractSize:   f.ContractSize,
		ContractUnit:   f.ContractUnit,
		SettlementDate: f.SettlementDate.Format("2006-01-02"),
		Listing: toListingInfo(
			listingID,
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toFuturesDetail(f *model.FuturesContract, listingID uint64) *pb.FuturesDetail {
	return &pb.FuturesDetail{
		Id:                f.ID,
		Ticker:            f.Ticker,
		Name:              f.Name,
		ContractSize:      f.ContractSize,
		ContractUnit:      f.ContractUnit,
		SettlementDate:    f.SettlementDate.Format("2006-01-02"),
		MaintenanceMargin: f.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toForexPairItem(fp *model.ForexPair, listingID uint64) *pb.ForexPairItem {
	return &pb.ForexPairItem{
		Id:            fp.ID,
		Ticker:        fp.Ticker,
		Name:          fp.Name,
		BaseCurrency:  fp.BaseCurrency,
		QuoteCurrency: fp.QuoteCurrency,
		ExchangeRate:  fp.ExchangeRate.StringFixed(8),
		Liquidity:     fp.Liquidity,
		ContractSize:  fp.ContractSizeValue(),
		Listing: toListingInfo(
			listingID,
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}

func toForexPairDetail(fp *model.ForexPair, listingID uint64) *pb.ForexPairDetail {
	return &pb.ForexPairDetail{
		Id:                fp.ID,
		Ticker:            fp.Ticker,
		Name:              fp.Name,
		BaseCurrency:      fp.BaseCurrency,
		QuoteCurrency:     fp.QuoteCurrency,
		ExchangeRate:      fp.ExchangeRate.StringFixed(8),
		Liquidity:         fp.Liquidity,
		ContractSize:      fp.ContractSizeValue(),
		MaintenanceMargin: fp.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}
```

- [ ] **Step 3: Resolve listingID at every handler call site**

For each handler method that builds a `pb.StockItem`/`StockDetail`/etc., look up the listing ID first and pass it into the mapper. The repo already has the method we need: `ListingRepository.GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error)` at `stock-service/internal/repository/listing_repository.go:34`.

Pattern for a single-item handler (example — apply the same shape to each of the six `Get*` handlers):

```go
// Inside GetStock(ctx, req) — where s is *model.Stock:
listing, err := h.listingRepo.GetBySecurityIDAndType(s.ID, "stock")
if err != nil {
	log.Printf("WARN: listing not found for stock %d: %v", s.ID, err)
	// Return the stock with listing id=0 rather than failing the whole request.
	// Clients will get 404 when trying to order — same as today.
	return &pb.GetStockResponse{Data: toStockDetail(s, options, 0)}, nil
}
return &pb.GetStockResponse{Data: toStockDetail(s, options, listing.ID)}, nil
```

For list handlers, batch the lookups. Add this helper to the same file, right above the mapper block:

```go
// resolveListingIDs returns a map from security ID to listing ID for the given type.
// securityIDs is the list of IDs just fetched from e.g. stockRepo.List.
func (h *SecurityHandler) resolveListingIDs(securityIDs []uint64, securityType string) map[uint64]uint64 {
	out := make(map[uint64]uint64, len(securityIDs))
	if len(securityIDs) == 0 {
		return out
	}
	// Uses the existing repository; one SQL per call via a WHERE security_id IN (...) query.
	listings, err := h.listingRepo.ListBySecurityIDsAndType(securityIDs, securityType)
	if err != nil {
		log.Printf("WARN: batch listing lookup failed for %s: %v", securityType, err)
		return out
	}
	for _, l := range listings {
		out[l.SecurityID] = l.ID
	}
	return out
}
```

Note `ListBySecurityIDsAndType` doesn't exist yet — add it to the repository (next step).

- [ ] **Step 4: Add the batch listing-lookup repository method**

Append to `stock-service/internal/repository/listing_repository.go` (after `GetBySecurityIDAndType` at line 41):

```go
// ListBySecurityIDsAndType returns all listings for the given security IDs of the given type.
// Used for batch resolution in list handlers.
func (r *ListingRepository) ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error) {
	if len(securityIDs) == 0 {
		return nil, nil
	}
	var listings []model.Listing
	if err := r.db.Where("security_type = ? AND security_id IN ?", securityType, securityIDs).
		Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}
```

Update the `ListingRepo` interface in `stock-service/internal/handler/security_handler.go` (or wherever the mock-able interface lives — search for `type ListingRepo interface` in the handler package). Add:

```go
ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error)
```

- [ ] **Step 5: Update all list handlers to batch-resolve and pass listing IDs**

Pattern for `ListStocks` (apply the analogous pattern to `ListFutures`, `ListForexPairs`, and the search handler if any):

```go
func (h *SecurityHandler) ListStocks(ctx context.Context, req *pb.ListStocksRequest) (*pb.ListStocksResponse, error) {
	// ... existing filter parsing ...
	stocks, total, err := h.stockRepo.List(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ids := make([]uint64, len(stocks))
	for i, s := range stocks {
		ids[i] = s.ID
	}
	listingMap := h.resolveListingIDs(ids, "stock")

	items := make([]*pb.StockItem, len(stocks))
	for i, s := range stocks {
		lid := listingMap[s.ID] // zero if unresolved — log already emitted
		stock := s
		items[i] = toStockItem(&stock, lid)
	}
	return &pb.ListStocksResponse{Data: items, Total: total}, nil
}
```

The same shape applies for `ListFutures` (security_type `"futures"`), `ListForexPairs` (security_type `"forex"`).

Single-item handlers (`GetStock`, `GetFutures`, `GetForexPair`, and the two detail variants) use `GetBySecurityIDAndType` directly — see Step 3.

- [ ] **Step 6: Extend the failing test with list and detail coverage**

Append to `stock-service/internal/handler/security_handler_listing_id_test.go`:

```go
func TestToStockItem_PopulatesListingID(t *testing.T) {
	s := &model.Stock{ID: 10, Ticker: "AAPL", Name: "Apple"}
	s.Exchange.Acronym = "NASDAQ"
	item := toStockItem(s, 99)
	if item.Listing.Id != 99 {
		t.Fatalf("expected Listing.Id=99, got %d", item.Listing.Id)
	}
	if item.Id != 10 {
		t.Fatalf("expected Id (security)=10, got %d", item.Id)
	}
}

func TestToForexPairItem_PopulatesListingID(t *testing.T) {
	fp := &model.ForexPair{ID: 5, Ticker: "EUR/USD", BaseCurrency: "EUR", QuoteCurrency: "USD"}
	item := toForexPairItem(fp, 77)
	if item.Listing.Id != 77 {
		t.Fatalf("expected Listing.Id=77, got %d", item.Listing.Id)
	}
}

func TestToFuturesItem_PopulatesListingID(t *testing.T) {
	fc := &model.FuturesContract{ID: 7, Ticker: "CL"}
	item := toFuturesItem(fc, 33)
	if item.Listing.Id != 33 {
		t.Fatalf("expected Listing.Id=33, got %d", item.Listing.Id)
	}
}
```

- [ ] **Step 7: Build and run unit tests**

Run: `cd stock-service && go build ./... && go test ./internal/handler/... -v`

Expected: all tests pass, no compile errors.

- [ ] **Step 8: Run the full service test suite**

Run: `cd stock-service && go test ./...`

Expected: PASS. If existing tests break, it's because they reference the old mapper signatures — update them to pass `0` as the listing ID (that's the pre-existing behaviour).

- [ ] **Step 9: Run lint**

Run: `cd stock-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 10: Commit**

```bash
git add stock-service/internal/handler/security_handler.go \
        stock-service/internal/handler/security_handler_listing_id_test.go \
        stock-service/internal/handler/security_handler_test.go \
        stock-service/internal/repository/listing_repository.go
git commit -m "fix(stock-service): populate ListingInfo.id in security responses

Bug #1 from docs/Bugs.txt: toListingInfo never set pb.ListingInfo.id, so
clients had no way to discover the listings table PK and instead sent the
security PK as listing_id, causing OrderService.CreateOrder to miss and
return 404.

Threads listingID through toListingInfo and all six toStockItem/
toStockDetail/toFuturesItem/toFuturesDetail/toForexPairItem/
toForexPairDetail mappers. Adds ListingRepo.ListBySecurityIDsAndType for
batched list handlers; single-item handlers use the existing
GetBySecurityIDAndType."
```

---

## Task 3: Bug #2 — Populate Volume in generated source

**Files:**
- Modify: `stock-service/internal/source/generated_source.go:173-297`
- Modify: `stock-service/internal/source/types.go:43-72` (add `Volume int64` to all three wrappers)
- Modify: `stock-service/internal/service/security_sync.go` (propagate Volume from wrapper to listing — minor)
- Test: `stock-service/internal/source/generated_volume_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `stock-service/internal/source/generated_volume_test.go`:

```go
package source

import (
	"context"
	"testing"
)

func TestGeneratedSource_Volume_Populated_AllTypes(t *testing.T) {
	g := NewGeneratedSource()
	ctx := context.Background()

	stocks, err := g.FetchStocks(ctx)
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(stocks) == 0 {
		t.Fatal("expected at least one stock")
	}
	for _, s := range stocks {
		if s.Stock.Volume <= 0 {
			t.Errorf("stock %s: Stock.Volume = %d, want > 0", s.Stock.Ticker, s.Stock.Volume)
		}
		if s.Volume <= 0 {
			t.Errorf("stock %s: wrapper.Volume = %d, want > 0", s.Stock.Ticker, s.Volume)
		}
		if s.Stock.Volume < 100_000 || s.Stock.Volume > 50_000_000 {
			t.Errorf("stock %s: Volume %d outside range 100k-50M", s.Stock.Ticker, s.Stock.Volume)
		}
	}

	futures, err := g.FetchFutures(ctx)
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	for _, f := range futures {
		if f.Futures.Volume <= 0 {
			t.Errorf("futures %s: Volume = %d, want > 0", f.Futures.Ticker, f.Futures.Volume)
		}
		if f.Futures.Volume < 1_000 || f.Futures.Volume > 500_000 {
			t.Errorf("futures %s: Volume %d outside range 1k-500k", f.Futures.Ticker, f.Futures.Volume)
		}
	}

	forex, err := g.FetchForex(ctx)
	if err != nil {
		t.Fatalf("FetchForex: %v", err)
	}
	for _, fp := range forex {
		if fp.Forex.Volume <= 0 {
			t.Errorf("forex %s: Volume = %d, want > 0", fp.Forex.Ticker, fp.Forex.Volume)
		}
		if fp.Forex.Volume < 100_000_000 || fp.Forex.Volume > 10_000_000_000 {
			t.Errorf("forex %s: Volume %d outside range 100M-10B", fp.Forex.Ticker, fp.Forex.Volume)
		}
	}
}

func TestGeneratedSource_Volume_Deterministic(t *testing.T) {
	g1 := NewGeneratedSource()
	g2 := NewGeneratedSource()
	ctx := context.Background()

	s1, _ := g1.FetchStocks(ctx)
	s2, _ := g2.FetchStocks(ctx)
	if len(s1) != len(s2) {
		t.Fatalf("different lengths: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i].Stock.Volume != s2[i].Stock.Volume {
			t.Errorf("stock %s: volume differs between instances: %d vs %d",
				s1[i].Stock.Ticker, s1[i].Stock.Volume, s2[i].Stock.Volume)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd stock-service && go test ./internal/source/ -run TestGeneratedSource_Volume -v`

Expected: FAIL — Volume is 0 everywhere; wrapper type has no `Volume` field yet.

- [ ] **Step 3: Add Volume field to wrapper structs**

Replace `stock-service/internal/source/types.go:43-72`:

```go
// StockWithListing pairs a stock model with its initial listing attributes
// so the caller can persist a securities row and its listing row together.
type StockWithListing struct {
	Stock       model.Stock
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}

// FuturesWithListing pairs a futures contract with its initial listing attributes.
type FuturesWithListing struct {
	Futures     model.FuturesContract
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}

// ForexWithListing pairs a forex pair with its initial listing attributes.
type ForexWithListing struct {
	Forex       model.ForexPair
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}
```

- [ ] **Step 4: Add hashVolume helper**

Append to `stock-service/internal/source/generated_source.go` right above the `// --- Mapping helpers ---` or final section (near the existing `exchangeForTicker` at line 78):

```go
// hashVolume returns a deterministic int64 in [minVol, maxVol] derived from the
// given seed string. Same seed → same value across process restarts, so the
// generated source produces reproducible Volume fields for stocks/futures/forex.
func hashVolume(seed string, minVol, maxVol int64) int64 {
	if maxVol <= minVol {
		return minVol
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	rangeSize := uint64(maxVol - minVol + 1)
	return minVol + int64(h.Sum64()%rangeSize)
}
```

- [ ] **Step 5: Populate Volume on Stocks**

Replace the body of `FetchStocks` at `stock-service/internal/source/generated_source.go:198-220`:

```go
// FetchStocks returns the 20 generated stocks with current prices and deterministic volumes.
func (g *GeneratedSource) FetchStocks(_ context.Context) ([]StockWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]StockWithListing, 0, len(generatedStocks))
	for _, s := range generatedStocks {
		price := g.stockPx[s.Ticker]
		exchangeID := g.resolveExchangeID(s.Ticker)
		volume := hashVolume("stock:"+s.Ticker, 100_000, 50_000_000)
		out = append(out, StockWithListing{
			Stock: model.Stock{
				Ticker:     s.Ticker,
				Name:       s.Name,
				Price:      price,
				Volume:     volume,
				ExchangeID: exchangeID,
			},
			ExchangeID:  exchangeID,
			Price:       price,
			High:        price,
			Low:         price,
			Volume:      volume,
			LastRefresh: g.now,
		})
	}
	return out, nil
}
```

- [ ] **Step 6: Populate Volume on Futures**

Replace the body of `FetchFutures` at `stock-service/internal/source/generated_source.go:223-252`:

```go
// FetchFutures returns the 20 generated futures contracts with current prices and deterministic volumes.
func (g *GeneratedSource) FetchFutures(_ context.Context) ([]FuturesWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]FuturesWithListing, 0, len(generatedFutures))
	for _, f := range generatedFutures {
		price := g.futuresPx[f.Ticker]
		unit, ok := futuresContractUnit[f.Ticker]
		if !ok {
			unit = "contract"
		}
		exchangeID := g.resolveExchangeID(f.Ticker)
		volume := hashVolume("futures:"+f.Ticker, 1_000, 500_000)
		out = append(out, FuturesWithListing{
			Futures: model.FuturesContract{
				Ticker:         f.Ticker,
				Name:           f.Name,
				ContractSize:   f.ContractSize,
				ContractUnit:   unit,
				Price:          price,
				Volume:         volume,
				SettlementDate: futuresSettlementDate(g.now, f.DaysToExpiry),
				ExchangeID:     exchangeID,
			},
			ExchangeID:  exchangeID,
			Price:       price,
			High:        price,
			Low:         price,
			Volume:      volume,
			LastRefresh: g.now,
		})
	}
	return out, nil
}
```

- [ ] **Step 7: Populate Volume on Forex**

Replace the body of `FetchForex` at `stock-service/internal/source/generated_source.go:255-297`:

```go
// FetchForex returns 56 forex pairs (8 currencies × 7 counterparties) with current rates and deterministic volumes.
func (g *GeneratedSource) FetchForex(_ context.Context) ([]ForexWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ForexWithListing, 0, 56)
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
			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}
			exchangeID := g.resolveExchangeID(pair)
			volume := hashVolume("forex:"+pair, 100_000_000, 10_000_000_000)
			out = append(out, ForexWithListing{
				Forex: model.ForexPair{
					Ticker:        pair,
					Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
					BaseCurrency:  base,
					QuoteCurrency: quote,
					ExchangeRate:  price,
					Liquidity:     liquidity,
					Volume:        volume,
					ExchangeID:    exchangeID,
					LastRefresh:   g.now,
				},
				ExchangeID:  exchangeID,
				Price:       price,
				High:        price,
				Low:         price,
				Volume:      volume,
				LastRefresh: g.now,
			})
		}
	}
	return out, nil
}
```

- [ ] **Step 8: Propagate Volume into listing upserts**

Check `stock-service/internal/service/listing_service.go` (or wherever listings are built from `StockWithListing`) for calls that construct `model.Listing`. Inside each branch that writes a listing from a wrapper, set `listing.Volume = wrapper.Volume`. If the existing code reads from `wrapper.Stock.Volume` (or similar) that already picks up our change, no further edit needed.

Concrete search + verify:

Run: `cd stock-service && grep -n "Volume" internal/service/listing_service.go internal/service/security_sync.go`

Inspect every line with `Volume`. Ensure that when a listing row is persisted from a `StockWithListing`/`FuturesWithListing`/`ForexWithListing`, the listing's `Volume` field is assigned from the wrapper's `Volume` (or equivalently from the inner security model's Volume — either works since they now match).

- [ ] **Step 9: Run the new volume tests**

Run: `cd stock-service && go test ./internal/source/ -run TestGeneratedSource_Volume -v`

Expected: both tests PASS.

- [ ] **Step 10: Run the whole source + service test suite**

Run: `cd stock-service && go test ./internal/source/... ./internal/service/...`

Expected: PASS. If the `stubSource` in `security_sync_test.go:270-290` breaks because the Source interface now exposes the wrapper with a Volume field, update the stub to set a sensible value (e.g., `1_000_000`).

- [ ] **Step 11: Run lint**

Run: `cd stock-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 12: Commit**

```bash
git add stock-service/internal/source/generated_source.go \
        stock-service/internal/source/types.go \
        stock-service/internal/source/generated_volume_test.go \
        stock-service/internal/service/listing_service.go \
        stock-service/internal/service/security_sync.go \
        stock-service/internal/service/security_sync_test.go
git commit -m "fix(stock-service): populate deterministic Volume in generated source

Bug #2 from docs/Bugs.txt: generated_source.go never set Volume on any
security type or wrapper. Result: every stock/futures/forex row had
volume=0 in the DB, sort_by=volume was useless, and calculateWaitTime
fell back to volume=1 which collapsed partial-fill wait times.

Adds hashVolume(seed, min, max) helper using FNV-64a. Ranges:
stocks 100k-50M, futures 1k-500k, forex 100M-10B. Deterministic across
restarts and process instances."
```

---

## Task 4: Bug #3 — Execution engine baseCtx

**Files:**
- Modify: `stock-service/internal/service/order_execution.go:20-78`
- Modify: `stock-service/cmd/main.go` (engine construction site)
- Test: `stock-service/internal/service/order_execution_basectx_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `stock-service/internal/service/order_execution_basectx_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeOrderRepo implements just enough of OrderRepo for the baseCtx test.
type fakeOrderRepo struct {
	order *model.Order
}

func (r *fakeOrderRepo) GetByID(id uint64) (*model.Order, error) {
	if r.order == nil || r.order.ID != id {
		return nil, errors.New("not found")
	}
	return r.order, nil
}
func (r *fakeOrderRepo) Update(o *model.Order) error {
	r.order = o
	return nil
}
func (r *fakeOrderRepo) ListActiveApproved() ([]*model.Order, error) { return nil, nil }

type fakeTxRepo struct{ created chan uint64 }

func (r *fakeTxRepo) Create(txn *model.OrderTransaction) error {
	select {
	case r.created <- txn.OrderID:
	default:
	}
	return nil
}

type fakeListingRepo struct{ l *model.Listing }

func (r *fakeListingRepo) GetByID(id uint64) (*model.Listing, error) { return r.l, nil }

type fakeSettingRepo struct{}

func (r *fakeSettingRepo) GetCommissionRate() decimal.Decimal { return decimal.NewFromFloat(0.0025) }

type fakeFillHandler struct{ filled chan struct{} }

func (h *fakeFillHandler) ProcessBuyFill(o *model.Order, t *model.OrderTransaction) error {
	close(h.filled)
	return nil
}
func (h *fakeFillHandler) ProcessSellFill(o *model.Order, t *model.OrderTransaction) error {
	close(h.filled)
	return nil
}

type fakePublisher struct{}

func (fakePublisher) PublishOrderFilled(_ context.Context, _ interface{}) error { return nil }

// TestStartOrderExecution_UsesBaseCtx_NotRequestCtx is the regression test for
// bug #3. Before the fix: the goroutine was started with a context derived from
// the caller's ctx, which is the gRPC request ctx in production. When the RPC
// returned, the goroutine's ctx was cancelled and the fill loop exited
// immediately without recording any OrderTransaction or calling the fill
// handler. After the fix: the goroutine uses the engine's baseCtx, so a
// cancelled request ctx has no effect on execution.
func TestStartOrderExecution_UsesBaseCtx_NotRequestCtx(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	orderRepo := &fakeOrderRepo{order: &model.Order{
		ID:                42,
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		RemainingPortions: 1,
		Quantity:          1,
		AllOrNone:         true,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         7,
	}}
	listingRepo := &fakeListingRepo{l: &model.Listing{
		ID:     7,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeTxRepo{created: make(chan uint64, 1)}
	fillHandler := &fakeFillHandler{filled: make(chan struct{})}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeSettingRepo{},
		fakePublisher{}, fillHandler,
	)

	// Pre-cancel the caller ctx to simulate the gRPC request returning.
	callerCtx, callerCancel := context.WithCancel(context.Background())
	callerCancel()

	engine.StartOrderExecution(callerCtx, 42)

	select {
	case <-fillHandler.filled:
		// Good: fill ran even though caller ctx was cancelled.
	case <-time.After(3 * time.Second):
		t.Fatalf("order did not fill within 3s — goroutine likely exited because it used caller ctx")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd stock-service && go test ./internal/service/ -run TestStartOrderExecution_UsesBaseCtx -v`

Expected: FAIL on compile (`NewOrderExecutionEngine` doesn't take a `ctx` as first arg yet) — or if the signature already lines up somehow, the test times out waiting for the fill.

- [ ] **Step 3: Modify the engine to hold baseCtx**

Replace `stock-service/internal/service/order_execution.go:20-78`:

```go
type OrderExecutionEngine struct {
	baseCtx     context.Context
	orderRepo   OrderRepo
	txRepo      OrderTransactionRepo
	listingRepo ListingRepo
	settingRepo SettingRepo
	producer    OrderFilledPublisher
	fillHandler FillHandler
	mu          sync.Mutex
	activeJobs  map[uint64]context.CancelFunc
}

// NewOrderExecutionEngine constructs the engine. baseCtx MUST be a long-lived
// context (typically the one created in cmd/main.go from context.Background()).
// It is used as the parent of every order-execution goroutine, decoupling fill
// execution from the lifetime of the gRPC request that triggered it. This fix
// is described in docs/Bugs.txt bug #3.
func NewOrderExecutionEngine(
	baseCtx context.Context,
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	producer OrderFilledPublisher,
	fillHandler FillHandler,
) *OrderExecutionEngine {
	return &OrderExecutionEngine{
		baseCtx:     baseCtx,
		orderRepo:   orderRepo,
		txRepo:      txRepo,
		listingRepo: listingRepo,
		settingRepo: settingRepo,
		producer:    producer,
		fillHandler: fillHandler,
		activeJobs:  make(map[uint64]context.CancelFunc),
	}
}

// Start begins processing all active approved orders.
// Should be called once at startup and whenever an order is approved.
// The ctx passed in is ignored for goroutine lifetime; the engine uses its
// own baseCtx. The ctx parameter is kept for signature stability.
func (e *OrderExecutionEngine) Start(_ context.Context) {
	orders, err := e.orderRepo.ListActiveApproved()
	if err != nil {
		log.Printf("WARN: order engine: failed to list active orders: %v", err)
		return
	}

	for _, order := range orders {
		e.StartOrderExecution(e.baseCtx, order.ID)
	}
	log.Printf("order engine: started execution for %d active orders", len(orders))
}

// StartOrderExecution launches a background goroutine for a single order.
// The ctx parameter is ignored for the goroutine's lifetime; the goroutine
// always uses a context derived from the engine's baseCtx so it is not
// cancelled when the calling gRPC request ends. The ctx parameter is kept
// for callsite ergonomics.
func (e *OrderExecutionEngine) StartOrderExecution(_ context.Context, orderID uint64) {
	e.mu.Lock()
	if _, exists := e.activeJobs[orderID]; exists {
		e.mu.Unlock()
		return
	}

	orderCtx, cancel := context.WithCancel(e.baseCtx)
	e.activeJobs[orderID] = cancel
	e.mu.Unlock()

	go e.executeOrder(orderCtx, orderID)
}
```

(The rest of `order_execution.go` from `StopOrderExecution` down — including `executeOrder`, `calculateWaitTime`, etc. — is unchanged.)

- [ ] **Step 4: Update the engine construction in main.go**

Edit `stock-service/cmd/main.go`. Find the existing `NewOrderExecutionEngine` call (search for it). Update it to pass the long-lived ctx as the first argument:

```go
// Replace e.g.
// execEngine := service.NewOrderExecutionEngine(orderRepo, txRepo, listingRepo, settingRepo, producer, fillHandler)
// with
execEngine := service.NewOrderExecutionEngine(
	ctx,          // the long-lived main-level ctx from `context.WithCancel(context.Background())`
	orderRepo, txRepo, listingRepo, settingRepo, producer, fillHandler,
)
```

Keep the existing `execEngine.Start(ctx)` call unchanged — Start's ctx arg is now ignored internally but the signature is stable.

- [ ] **Step 5: Run the bug-3 regression test**

Run: `cd stock-service && go test ./internal/service/ -run TestStartOrderExecution_UsesBaseCtx -v`

Expected: PASS within 3 seconds.

- [ ] **Step 6: Run the whole service test suite**

Run: `cd stock-service && go test ./...`

Expected: PASS. Any existing test that calls `NewOrderExecutionEngine` needs to pass a base ctx (typically `context.Background()` is fine for a test). Update each test's engine construction accordingly.

- [ ] **Step 7: Run lint**

Run: `cd stock-service && golangci-lint run ./...`

Expected: no new warnings.

- [ ] **Step 8: Build the service**

Run: `cd stock-service && go build ./...`

Expected: clean build.

- [ ] **Step 9: Commit**

```bash
git add stock-service/internal/service/order_execution.go \
        stock-service/internal/service/order_execution_basectx_test.go \
        stock-service/cmd/main.go
git commit -m "fix(stock-service): decouple order execution from request ctx

Bug #3 from docs/Bugs.txt: StartOrderExecution derived its goroutine ctx
from the caller's ctx, which is the gRPC request ctx in CreateOrder and
ApproveOrder. The request ctx is cancelled when the RPC returns, which
cancelled the goroutine's ctx and caused executeOrder to exit on its
first select without recording any OrderTransaction or calling the fill
handler. Orders would get stuck at status=approved until a service
restart, at which point Start(ctx) would resume them using main's
long-lived ctx — explaining why the bug looked intermittent.

Adds baseCtx field to OrderExecutionEngine; wires main.go's long-lived
ctx in through NewOrderExecutionEngine; StartOrderExecution derives
orderCtx from baseCtx. The ctx parameter on StartOrderExecution and
Start is kept for call-site stability but is now ignored internally."
```

---

## Task 5: Integration test — order lifecycle end-to-end

**Files:**
- Modify: `test-app/workflows/stock_helpers_test.go` (verify listing_id extraction works)
- Modify: `test-app/workflows/wf_stock_buy_sell_test.go` (expect order fills within a reasonable timeout)

- [ ] **Step 1: Verify the integration test for list-then-order still resolves listing_id from response**

Read `test-app/workflows/stock_helpers_test.go` around line 143–174 (function `getFirstStockListingID`). Confirm it extracts `listing.id` from the response — after Phase 1 this value is now real (not 0). No code change needed; this is a read-only verification step.

Run: `cd test-app && go test ./workflows/ -run TestGetFirstStockListingID -v` (if such a test exists, otherwise skip).

- [ ] **Step 2: Update the buy/sell workflow test to assert the order actually fills**

Find the existing end-to-end test in `test-app/workflows/wf_stock_buy_sell_test.go`. Find the step after order placement. Before Phase 1, an order placed via `POST /api/v1/me/orders` would stay `approved` forever because the execution goroutine exited immediately. After Phase 1 fix #3 is in place, the order should move to `is_done=true` within a short window (the `calculateWaitTime` cap is 60s, and with `AllOrNone=false` the engine issues partial fills).

Add (or adjust existing) assertion immediately after order creation:

```go
// Wait for at least one fill to materialise. With the bug-#3 fix the
// execution goroutine survives the request ctx, so we should see a
// transaction within the calculateWaitTime cap (60s) plus processing
// slack. If this times out, bug #3 has regressed.
orderID := createResp.Order.Id
deadline := time.Now().Add(120 * time.Second)
for time.Now().Before(deadline) {
	getResp := getOrderByID(t, gw, clientToken, orderID)
	if getResp.IsDone || len(getResp.Transactions) > 0 {
		return // success
	}
	time.Sleep(2 * time.Second)
}
t.Fatalf("order %d did not fill within 120s — bug #3 regression", orderID)
```

If the test helpers `getOrderByID` and the response type don't exist exactly as shown, use whatever the existing helpers-package returns. The key assertion is: **after Phase 1, an order that would previously stick at `approved` forever now completes (or at least produces a transaction row) within ~2 minutes**.

- [ ] **Step 3: Run the full integration suite**

Run: `cd test-app && go test ./workflows/ -run TestStockBuySell -v -timeout 5m`

Expected: PASS. If account balance changes are asserted in this test, they may still be wrong (Phase 2 fixes that); relax those assertions to not block on amount correctness.

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/wf_stock_buy_sell_test.go
git commit -m "test(workflows): assert order fills within 120s after bug-#3 fix

With the baseCtx fix in place the execution goroutine survives the gRPC
request, so orders placed via /api/v1/me/orders should reach is_done
within calculateWaitTime plus slack. This test regresses to failing if
bug #3 is reintroduced."
```

---

## Task 6: Update Specification.md with the Phase-1 bank-safety banner

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Add the warning banner**

Open `docs/Specification.md`. Find the section dealing with Securities / Order Settlement (search for "order" / "settlement" / "stock-service"). At the top of that section, insert:

```markdown
> ⚠ **Phase-1 note (2026-04-22):** The securities order pipeline runs end-to-end
> as of Phase 1 (`docs/superpowers/plans/2026-04-22-unblock-order-flow.md`),
> but the fill path is **best-effort, not bank-safe**. Funds are not reserved at
> placement; cross-currency debits use raw listing-currency amounts; holding
> mutations can diverge from account state on partial failure; Kafka events
> publish before the fill saga commits. These are all fixed in Phase 2
> (`docs/superpowers/plans/2026-04-22-bank-safe-settlement.md`). Do not rely on
> the securities pipeline for real settlement until Phase 2 ships.
```

- [ ] **Step 2: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): flag securities fill path as pre-bank-safe until Phase 2"
```

---

## Task 7: Final verification

- [ ] **Step 1: Full unit test sweep**

Run: `make test`

Expected: PASS across all services that were touched.

- [ ] **Step 2: Full lint sweep**

Run: `make lint`

Expected: zero new warnings in `stock-service`.

- [ ] **Step 3: Docker smoke test (optional but recommended)**

Run: `make docker-up`, then in another terminal run one of the integration tests against the running gateway:

```bash
cd test-app && go test ./workflows/ -run TestStockBuySell -v -timeout 5m
```

Expected: order flows through placement → fill → response; account ledger shows (still incorrect amount — Phase 2 fixes) but transactions exist.

Bring the stack back down:

```bash
make docker-down
```

- [ ] **Step 4: Sanity-check the bugs doc**

Open `docs/Bugs.txt`. Strike out / annotate bugs #1, #2, #3 as "fixed in Phase 1". Do NOT remove; they remain a reference for future triage. Commit the annotation as its own change:

```bash
git add docs/Bugs.txt
git commit -m "docs(bugs): mark bugs #1, #2, #3 fixed in Phase 1"
```

Phase 1 is now complete. Proceed to Phase 2 (`docs/superpowers/plans/2026-04-22-bank-safe-settlement.md`).
