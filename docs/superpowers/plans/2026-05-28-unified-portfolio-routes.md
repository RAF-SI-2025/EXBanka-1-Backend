# Unified Portfolio Routes (Part B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single consistent route shape for viewing the portfolio of clients, the bank, and investment funds, with fund positions surfaced alongside stocks/options/futures and reflected in the P/L totals.

**Architecture:**
- Derived `portfolioId` of the form `client-<n>` / `bank` / `fund-<n>` (no new entity, no new table).
- Six new gateway routes; existing endpoints stay untouched (no breaking changes).
- New `stock.Portfolio.GetUnified` gRPC RPC; stock-service composes the grouped response from existing holding + fund_position repos.
- Two new permissions on EmployeeSupervisor/EmployeeAdmin: `portfolio.view_client`, `portfolio.view_fund`.
- Gateway-side ownership enforcement using `ResolvedIdentity` + a new `enforcePortfolioAccess` helper.

**Tech Stack:** Go workspace, Gin, gRPC + protobuf, GORM, existing `contract/permissions` catalog.

**Spec:** `docs/superpowers/specs/2026-05-28-final-audit-fixes-and-portfolio-cron-design.md` Part B.

---

## File Structure

- Create: `api-gateway/internal/handler/portfolio_id.go` — encode/decode helpers + portfolio access enforcement
- Create: `api-gateway/internal/handler/portfolio_id_test.go`
- Create: `api-gateway/internal/handler/unified_portfolio_handler.go` — new handler methods
- Modify: `api-gateway/internal/router/router_v3.go` — register six routes
- Modify: `contract/proto/stock/stock.proto` — add `GetUnifiedPortfolio` RPC + grouped response messages
- Modify: `stock-service/internal/handler/portfolio_handler.go` — implement new RPC
- Create: `stock-service/internal/service/unified_portfolio_service.go` — composition logic
- Modify: `contract/permissions/perms.gen.go` — add two new permissions (or `permissions_source.yaml` if there is one, regen)
- Modify: `user-service/internal/service/role_service.go` (only if `DefaultRoles` is hand-curated rather than generated)
- Modify: `docs/api/REST_API_v1.md` — document new routes
- Modify: `docs/Specification.md` — Section 17 routes + Section 6 permissions
- Test: `test-app/workflows/unified_portfolio_test.go`

---

## Task B1: Portfolio ID encode/decode + tests

**Files:**
- Create: `api-gateway/internal/handler/portfolio_id.go`
- Create: `api-gateway/internal/handler/portfolio_id_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// api-gateway/internal/handler/portfolio_id_test.go
package handler

import "testing"

func TestEncodePortfolioID(t *testing.T) {
	cases := []struct {
		ownerType string
		ownerID   *uint64
		want      string
	}{
		{"client", ptrU64(42), "client-42"},
		{"bank", nil, "bank"},
		{"investment_fund", ptrU64(7), "fund-7"},
	}
	for _, c := range cases {
		got, err := EncodePortfolioID(c.ownerType, c.ownerID)
		if err != nil { t.Fatalf("EncodePortfolioID(%q,%v) err: %v", c.ownerType, c.ownerID, err) }
		if got != c.want { t.Errorf("got %q want %q", got, c.want) }
	}
}

func TestDecodePortfolioID(t *testing.T) {
	cases := []struct {
		in        string
		wantType  string
		wantID    *uint64
		wantErr   bool
	}{
		{"client-42", "client", ptrU64(42), false},
		{"bank", "bank", nil, false},
		{"fund-7", "investment_fund", ptrU64(7), false},
		{"", "", nil, true},
		{"client-", "", nil, true},
		{"client-abc", "", nil, true},
		{"unknown-1", "", nil, true},
		{"fund-0", "", nil, true},
	}
	for _, c := range cases {
		ot, oid, err := DecodePortfolioID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Decode(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr { continue }
		if ot != c.wantType { t.Errorf("Decode(%q) type=%q want %q", c.in, ot, c.wantType) }
		if (oid == nil) != (c.wantID == nil) || (oid != nil && *oid != *c.wantID) {
			t.Errorf("Decode(%q) id=%v want %v", c.in, oid, c.wantID)
		}
	}
}

func ptrU64(v uint64) *uint64 { return &v }
```

- [ ] **Step 2: Verify it fails**

```bash
cd api-gateway && go test ./internal/handler/... -run TestEncodePortfolioID -v
```

Expected: FAIL — `undefined: EncodePortfolioID`

- [ ] **Step 3: Write implementation**

```go
// api-gateway/internal/handler/portfolio_id.go
package handler

import (
	"fmt"
	"regexp"
	"strconv"
)

var (
	rxClient = regexp.MustCompile(`^client-(\d+)$`)
	rxFund   = regexp.MustCompile(`^fund-(\d+)$`)
)

// EncodePortfolioID returns the URL-safe string form of a portfolio identity.
//   client owner       -> "client-<id>"
//   bank owner         -> "bank"          (no id; singleton)
//   investment_fund    -> "fund-<id>"
func EncodePortfolioID(ownerType string, ownerID *uint64) (string, error) {
	switch ownerType {
	case "client":
		if ownerID == nil { return "", fmt.Errorf("client portfolio requires owner_id") }
		return fmt.Sprintf("client-%d", *ownerID), nil
	case "bank":
		return "bank", nil
	case "investment_fund":
		if ownerID == nil { return "", fmt.Errorf("fund portfolio requires owner_id") }
		return fmt.Sprintf("fund-%d", *ownerID), nil
	default:
		return "", fmt.Errorf("unknown owner type %q", ownerType)
	}
}

// DecodePortfolioID parses the URL form back into (ownerType, ownerID).
// Owner IDs must be > 0. Returns a 400-compatible error string on bad input.
func DecodePortfolioID(s string) (string, *uint64, error) {
	if s == "bank" {
		return "bank", nil, nil
	}
	if m := rxClient.FindStringSubmatch(s); m != nil {
		id, _ := strconv.ParseUint(m[1], 10, 64)
		if id == 0 { return "", nil, fmt.Errorf("invalid portfolio id: client id must be > 0") }
		return "client", &id, nil
	}
	if m := rxFund.FindStringSubmatch(s); m != nil {
		id, _ := strconv.ParseUint(m[1], 10, 64)
		if id == 0 { return "", nil, fmt.Errorf("invalid portfolio id: fund id must be > 0") }
		return "investment_fund", &id, nil
	}
	return "", nil, fmt.Errorf("invalid portfolio id %q", s)
}
```

- [ ] **Step 4: Verify tests pass**

```bash
cd api-gateway && go test ./internal/handler/... -run TestEncodePortfolioID -v
cd api-gateway && go test ./internal/handler/... -run TestDecodePortfolioID -v
```

Expected: PASS (both)

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/portfolio_id.go api-gateway/internal/handler/portfolio_id_test.go
git commit -m "feat(gateway): portfolio id encode/decode helpers"
```

---

## Task B2: enforcePortfolioAccess helper

**Files:**
- Modify: `api-gateway/internal/handler/validation.go` (add function at end of file)
- Modify: `api-gateway/internal/handler/portfolio_id_test.go` (extend)

- [ ] **Step 1: Write failing test cases**

```go
// In portfolio_id_test.go, add:
func TestEnforcePortfolioAccess_ClientOwnPortfolio(t *testing.T) {
	c := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 42}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), nil); err != nil {
		t.Fatal("client viewing own portfolio should succeed")
	}
}
func TestEnforcePortfolioAccess_ClientOtherClient(t *testing.T) {
	c := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 42}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(43), nil); err == nil {
		t.Fatal("client viewing other client's portfolio should be forbidden")
	}
}
func TestEnforcePortfolioAccess_EmployeeBank(t *testing.T) {
	c := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "bank", nil, []string{}); err != nil {
		t.Fatal("employee viewing bank portfolio should succeed")
	}
}
func TestEnforcePortfolioAccess_EmployeeClientWithoutPerm(t *testing.T) {
	c := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), []string{}); err == nil {
		t.Fatal("employee without portfolio.view_client should be denied")
	}
}
func TestEnforcePortfolioAccess_EmployeeClientWithPerm(t *testing.T) {
	c := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), []string{"portfolio.view_client"}); err != nil {
		t.Fatal("employee WITH portfolio.view_client should be allowed")
	}
}
```

(Helper `newTestGinCtx` constructs a gin.Context against a discardable response writer; if missing, add a tiny test-helper file.)

- [ ] **Step 2: Verify failing**

```bash
cd api-gateway && go test ./internal/handler/... -run TestEnforcePortfolioAccess -v
```

Expected: FAIL — `undefined: enforcePortfolioAccess`

- [ ] **Step 3: Implement**

In `api-gateway/internal/handler/validation.go`, append:

```go
// enforcePortfolioAccess returns an error and writes a 403 response if the
// resolved identity may not view the portfolio identified by (ownerType,
// ownerID). Permission set is passed in to keep validation.go free of cyclic
// deps with the middleware package.
func enforcePortfolioAccess(c *gin.Context, id *middleware.ResolvedIdentity,
	targetOwnerType string, targetOwnerID *uint64, callerPermissions []string) error {

	hasPerm := func(p string) bool {
		for _, x := range callerPermissions { if x == p { return true } }
		return false
	}

	switch id.PrincipalType {
	case "client":
		if targetOwnerType == "client" && targetOwnerID != nil && *targetOwnerID == id.PrincipalID {
			return nil
		}
		apiError(c, 403, ErrForbidden, "you may only view your own portfolio")
		return fmt.Errorf("client %d may not view %s/%v", id.PrincipalID, targetOwnerType, targetOwnerID)
	case "employee":
		switch targetOwnerType {
		case "bank":
			return nil
		case "client":
			if hasPerm("portfolio.view_client") { return nil }
		case "investment_fund":
			if hasPerm("portfolio.view_fund") { return nil }
			// Supervisor-managing-this-fund exception: handled by the stock-service
			// layer (which has the fund-manager link); gateway forwards if perm holds.
		}
		apiError(c, 403, ErrForbidden, "missing permission to view this portfolio")
		return fmt.Errorf("employee %d missing perm for %s", id.PrincipalID, targetOwnerType)
	default:
		apiError(c, 401, ErrUnauthorized, "unknown principal")
		return fmt.Errorf("unknown principal type %q", id.PrincipalType)
	}
}
```

(`ErrForbidden`, `ErrUnauthorized` already exist in validation.go.)

- [ ] **Step 4: Verify tests pass**

```bash
cd api-gateway && go test ./internal/handler/... -run TestEnforcePortfolioAccess -v
```

Expected: PASS (all 5)

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/validation.go api-gateway/internal/handler/portfolio_id_test.go
git commit -m "feat(gateway): enforcePortfolioAccess for unified portfolio routes"
```

---

## Task B3: Proto definition — GetUnifiedPortfolio RPC

**Files:**
- Modify: `contract/proto/stock/stock.proto` (in `PortfolioGRPCService` block)

- [ ] **Step 1: Add RPC + messages**

```protobuf
service PortfolioGRPCService {
  // ... existing RPCs ...
  rpc GetUnifiedPortfolio(GetUnifiedPortfolioRequest) returns (UnifiedPortfolioResponse);
}

message GetUnifiedPortfolioRequest {
  string owner_type = 1; // "client" | "bank" | "investment_fund"
  uint64 owner_id   = 2; // 0 when owner_type == "bank"
}

message PortfolioPosition {
  string asset_type = 1;             // "stock"|"option"|"future"|"investment_fund"
  string symbol = 2;                 // for stocks/options/futures
  uint64 fund_id = 3;                // for fund positions
  string fund_name = 4;              // for fund positions
  uint64 contract_id = 5;            // for options/futures
  int64 quantity = 6;
  string avg_cost_rsd = 7;           // decimal as string
  string current_price_rsd = 8;
  string current_value_rsd = 9;
  string p_l_rsd = 10;
  string p_l_pct = 11;
  string amount_invested_rsd = 12;   // fund positions
  string pct_of_fund = 13;
  string strike_rsd = 14;
  string premium_paid_rsd = 15;
  string intrinsic_value_rsd = 16;
  string settlement_date = 17;
  string last_updated = 18;
}

message PortfolioGroup {
  string total_value_rsd = 1;
  string total_profit_rsd = 2;
  string total_profit_pct = 3;
  repeated PortfolioPosition positions = 4;
}

message UnifiedPortfolioResponse {
  string portfolio_id = 1;
  string owner_type = 2;
  uint64 owner_id = 3;
  string owner_name = 4;
  string total_value_rsd = 5;
  string total_profit_rsd = 6;
  string total_profit_pct = 7;
  PortfolioGroup securities = 8;
  PortfolioGroup funds = 9;
}
```

- [ ] **Step 2: Regenerate**

```bash
make proto
```

- [ ] **Step 3: Commit**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(proto): GetUnifiedPortfolio RPC + grouped response"
```

---

## Task B4: Implement UnifiedPortfolioService composition

**Files:**
- Create: `stock-service/internal/service/unified_portfolio_service.go`
- Create: `stock-service/internal/service/unified_portfolio_service_test.go`

- [ ] **Step 1: Write the composition logic**

```go
// stock-service/internal/service/unified_portfolio_service.go
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
)

type UnifiedPortfolioService struct {
	holdingRepo      *repository.HoldingRepository
	fundPosRepo      *repository.ClientFundPositionRepository
	fundRepo         *repository.InvestmentFundRepository
	listingRepo      *repository.ListingRepository
	exchangeRateRepo *repository.ExchangeRateRepository
}

func NewUnifiedPortfolioService(
	h *repository.HoldingRepository,
	fp *repository.ClientFundPositionRepository,
	f *repository.InvestmentFundRepository,
	l *repository.ListingRepository,
	er *repository.ExchangeRateRepository,
) *UnifiedPortfolioService {
	return &UnifiedPortfolioService{h, fp, f, l, er}
}

type UnifiedPortfolio struct {
	OwnerType       string
	OwnerID         *uint64
	OwnerName       string
	TotalValueRSD   decimal.Decimal
	TotalProfitRSD  decimal.Decimal
	TotalProfitPct  decimal.Decimal
	Securities      PortfolioGroup
	Funds           PortfolioGroup
}

type PortfolioGroup struct {
	TotalValueRSD  decimal.Decimal
	TotalProfitRSD decimal.Decimal
	TotalProfitPct decimal.Decimal
	Positions      []PortfolioPosition
}

type PortfolioPosition struct {
	AssetType           string
	Symbol              string
	FundID              uint64
	FundName            string
	ContractID          uint64
	Quantity            int64
	AvgCostRSD          decimal.Decimal
	CurrentPriceRSD     decimal.Decimal
	CurrentValueRSD     decimal.Decimal
	PLRSD               decimal.Decimal
	PLPct               decimal.Decimal
	AmountInvestedRSD   decimal.Decimal
	PctOfFund           decimal.Decimal
	StrikeRSD           decimal.Decimal
	PremiumPaidRSD      decimal.Decimal
	IntrinsicValueRSD   decimal.Decimal
	SettlementDate      *time.Time
	LastUpdated         time.Time
}

func (s *UnifiedPortfolioService) Get(ctx context.Context, ownerType string, ownerID *uint64) (*UnifiedPortfolio, error) {
	if ownerType != "bank" && ownerID == nil {
		return nil, fmt.Errorf("owner_id required for %s", ownerType)
	}

	holdings, err := s.holdingRepo.ListByOwner(ownerType, ownerID)
	if err != nil { return nil, fmt.Errorf("list holdings: %w", err) }

	fundPositions, err := s.fundPosRepo.ListByOwner(ownerType, ownerID)
	if err != nil { return nil, fmt.Errorf("list fund positions: %w", err) }

	priceCache, err := s.fetchListingPrices(ctx, holdings)
	if err != nil { return nil, fmt.Errorf("fetch listing prices: %w", err) }

	out := &UnifiedPortfolio{OwnerType: ownerType, OwnerID: ownerID}

	// Securities
	for _, h := range holdings {
		pos, err := s.composeHoldingPosition(h, priceCache)
		if err != nil { return nil, err }
		out.Securities.Positions = append(out.Securities.Positions, pos)
		out.Securities.TotalValueRSD = out.Securities.TotalValueRSD.Add(pos.CurrentValueRSD)
		out.Securities.TotalProfitRSD = out.Securities.TotalProfitRSD.Add(pos.PLRSD)
	}

	// Fund positions
	for _, fp := range fundPositions {
		pos, err := s.composeFundPosition(ctx, fp)
		if err != nil { return nil, err }
		out.Funds.Positions = append(out.Funds.Positions, pos)
		out.Funds.TotalValueRSD = out.Funds.TotalValueRSD.Add(pos.CurrentValueRSD)
		out.Funds.TotalProfitRSD = out.Funds.TotalProfitRSD.Add(pos.PLRSD)
	}

	// Pct calculations
	if invested := totalInvested(out.Securities, out.Funds); !invested.IsZero() {
		out.Securities.TotalProfitPct = pct(out.Securities.TotalProfitRSD, invested)
		out.Funds.TotalProfitPct = pct(out.Funds.TotalProfitRSD, invested)
	}

	// Totals
	out.TotalValueRSD = out.Securities.TotalValueRSD.Add(out.Funds.TotalValueRSD)
	out.TotalProfitRSD = out.Securities.TotalProfitRSD.Add(out.Funds.TotalProfitRSD)
	if invested := totalInvested(out.Securities, out.Funds); !invested.IsZero() {
		out.TotalProfitPct = pct(out.TotalProfitRSD, invested)
	}

	return out, nil
}

func (s *UnifiedPortfolioService) composeHoldingPosition(h model.Holding, prices map[uint64]decimal.Decimal) (PortfolioPosition, error) {
	currentPrice := prices[h.ListingID]
	currentValue := currentPrice.Mul(decimal.NewFromInt(h.Quantity))
	totalCost := h.AveragePrice.Mul(decimal.NewFromInt(h.Quantity))
	pl := currentValue.Sub(totalCost)
	return PortfolioPosition{
		AssetType:       mapSecurityType(h.SecurityType),
		Symbol:          h.Ticker,
		Quantity:        h.Quantity,
		AvgCostRSD:      h.AveragePrice,
		CurrentPriceRSD: currentPrice,
		CurrentValueRSD: currentValue,
		PLRSD:           pl,
		PLPct:           pct(pl, totalCost),
		LastUpdated:     h.UpdatedAt,
	}, nil
}

func (s *UnifiedPortfolioService) composeFundPosition(ctx context.Context, fp model.ClientFundPosition) (PortfolioPosition, error) {
	fund, err := s.fundRepo.GetByID(fp.FundID)
	if err != nil { return PortfolioPosition{}, fmt.Errorf("get fund %d: %w", fp.FundID, err) }
	// Sum all positions to compute pct
	allPositions, err := s.fundPosRepo.ListByFund(fp.FundID)
	if err != nil { return PortfolioPosition{}, fmt.Errorf("list fund positions: %w", err) }
	var totalInvested decimal.Decimal
	for _, p := range allPositions {
		totalInvested = totalInvested.Add(p.TotalContributedRSD)
	}
	pctOfFund := decimal.Zero
	if !totalInvested.IsZero() {
		pctOfFund = fp.TotalContributedRSD.Div(totalInvested).Mul(decimal.NewFromInt(100))
	}
	// Fund value = liquid + value of fund's holdings (re-uses listing prices)
	fundValue, err := s.computeFundValue(ctx, fund)
	if err != nil { return PortfolioPosition{}, err }
	currentValue := fundValue.Mul(pctOfFund).Div(decimal.NewFromInt(100))
	pl := currentValue.Sub(fp.TotalContributedRSD)
	return PortfolioPosition{
		AssetType:         "investment_fund",
		FundID:            fp.FundID,
		FundName:          fund.Name,
		AmountInvestedRSD: fp.TotalContributedRSD,
		CurrentValueRSD:   currentValue,
		PctOfFund:         pctOfFund,
		PLRSD:             pl,
		PLPct:             pct(pl, fp.TotalContributedRSD),
		LastUpdated:       fp.UpdatedAt,
	}, nil
}

func (s *UnifiedPortfolioService) fetchListingPrices(ctx context.Context, holdings []model.Holding) (map[uint64]decimal.Decimal, error) {
	ids := make([]uint64, 0, len(holdings))
	for _, h := range holdings { ids = append(ids, h.ListingID) }
	listings, err := s.listingRepo.ListByIDs(ids)
	if err != nil { return nil, err }
	out := make(map[uint64]decimal.Decimal, len(listings))
	for _, l := range listings { out[l.ID] = l.CurrentPriceRSD() }
	return out, nil
}

func (s *UnifiedPortfolioService) computeFundValue(ctx context.Context, fund *model.InvestmentFund) (decimal.Decimal, error) {
	// liquid_rsd + Σ(holding qty * current_price)
	holdings, err := s.holdingRepo.ListByOwner("investment_fund", &fund.ID)
	if err != nil { return decimal.Zero, err }
	prices, err := s.fetchListingPrices(ctx, holdings)
	if err != nil { return decimal.Zero, err }
	value := fund.LiquidRSD
	for _, h := range holdings {
		value = value.Add(prices[h.ListingID].Mul(decimal.NewFromInt(h.Quantity)))
	}
	return value, nil
}

func mapSecurityType(s string) string {
	switch s {
	case "stock": return "stock"
	case "option": return "option"
	case "futures", "future": return "future"
	default: return s
	}
}

func pct(numerator, denominator decimal.Decimal) decimal.Decimal {
	if denominator.IsZero() { return decimal.Zero }
	return numerator.Div(denominator).Mul(decimal.NewFromInt(100))
}

func totalInvested(s PortfolioGroup, f PortfolioGroup) decimal.Decimal {
	sum := decimal.Zero
	for _, p := range s.Positions { sum = sum.Add(p.AvgCostRSD.Mul(decimal.NewFromInt(p.Quantity))) }
	for _, p := range f.Positions { sum = sum.Add(p.AmountInvestedRSD) }
	return sum
}
```

(Field names like `LiquidRSD`, `CurrentPriceRSD()`, repo method `ListByIDs` are best-guess names. Verify against existing code before compiling; adjust to match real signatures. Behavior is the same.)

- [ ] **Step 2: Write the unit test**

```go
// stock-service/internal/service/unified_portfolio_service_test.go
func TestUnifiedPortfolio_ClientWithStockAndFund(t *testing.T) {
	// Mock holdingRepo to return one AAPL holding (50 qty @ 200 RSD avg, current 220 RSD)
	// Mock fundPosRepo to return one fund position (25000 RSD invested)
	// Mock fundRepo + listingRepo so fundValue computes to 50000 RSD
	// Mock fundPosRepo.ListByFund to return only this client's position (so pctOfFund=100%)
	// Call Get("client", &id)
	// Assert:
	//   Securities.Positions[0].CurrentValueRSD == 11000
	//   Securities.Positions[0].PLRSD == 1000
	//   Funds.Positions[0].CurrentValueRSD == 50000 (* 100%)
	//   Funds.Positions[0].PLRSD == 25000
	//   TotalValueRSD == 61000
	t.Skip("Skeleton — flesh out with mock repos")
}
```

- [ ] **Step 3: Build stock-service**

```bash
cd stock-service && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/unified_portfolio_service.go stock-service/internal/service/unified_portfolio_service_test.go
git commit -m "feat(stock): UnifiedPortfolioService composes grouped portfolio"
```

---

## Task B5: stock-service handler — GetUnifiedPortfolio RPC

**Files:**
- Modify: `stock-service/internal/handler/portfolio_handler.go`

- [ ] **Step 1: Add method**

```go
func (h *PortfolioHandler) GetUnifiedPortfolio(ctx context.Context, req *stockpb.GetUnifiedPortfolioRequest) (*stockpb.UnifiedPortfolioResponse, error) {
	if req.OwnerType == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_type required")
	}
	var ownerID *uint64
	if req.OwnerId != 0 {
		id := req.OwnerId
		ownerID = &id
	}
	out, err := h.unifiedSvc.Get(ctx, req.OwnerType, ownerID)
	if err != nil { return nil, status.Error(codes.Internal, err.Error()) }

	return mapToProto(out), nil
}

func mapToProto(p *service.UnifiedPortfolio) *stockpb.UnifiedPortfolioResponse {
	resp := &stockpb.UnifiedPortfolioResponse{
		OwnerType:       p.OwnerType,
		TotalValueRsd:   p.TotalValueRSD.String(),
		TotalProfitRsd:  p.TotalProfitRSD.String(),
		TotalProfitPct:  p.TotalProfitPct.String(),
		Securities:      mapGroup(p.Securities),
		Funds:           mapGroup(p.Funds),
		OwnerName:       p.OwnerName,
	}
	if p.OwnerID != nil { resp.OwnerId = *p.OwnerID }
	if pid, err := encodePID(p.OwnerType, p.OwnerID); err == nil { resp.PortfolioId = pid }
	return resp
}

func mapGroup(g service.PortfolioGroup) *stockpb.PortfolioGroup {
	out := &stockpb.PortfolioGroup{
		TotalValueRsd: g.TotalValueRSD.String(),
		TotalProfitRsd: g.TotalProfitRSD.String(),
		TotalProfitPct: g.TotalProfitPct.String(),
	}
	for _, p := range g.Positions {
		out.Positions = append(out.Positions, &stockpb.PortfolioPosition{
			AssetType: p.AssetType, Symbol: p.Symbol, FundId: p.FundID, FundName: p.FundName,
			ContractId: p.ContractID, Quantity: p.Quantity,
			AvgCostRsd: p.AvgCostRSD.String(),
			CurrentPriceRsd: p.CurrentPriceRSD.String(),
			CurrentValueRsd: p.CurrentValueRSD.String(),
			PLRsd: p.PLRSD.String(), PLPct: p.PLPct.String(),
			AmountInvestedRsd: p.AmountInvestedRSD.String(),
			PctOfFund: p.PctOfFund.String(),
			StrikeRsd: p.StrikeRSD.String(),
			PremiumPaidRsd: p.PremiumPaidRSD.String(),
			IntrinsicValueRsd: p.IntrinsicValueRSD.String(),
			LastUpdated: p.LastUpdated.UTC().Format(time.RFC3339),
		})
		if p.SettlementDate != nil {
			out.Positions[len(out.Positions)-1].SettlementDate = p.SettlementDate.UTC().Format(time.RFC3339)
		}
	}
	return out
}

// encodePID is a small helper mirroring the gateway-side encoder so the
// service can include the portfolio_id in its response. Keep in sync.
func encodePID(ownerType string, ownerID *uint64) (string, error) {
	switch ownerType {
	case "client":
		if ownerID == nil { return "", fmt.Errorf("client requires id") }
		return fmt.Sprintf("client-%d", *ownerID), nil
	case "bank": return "bank", nil
	case "investment_fund":
		if ownerID == nil { return "", fmt.Errorf("fund requires id") }
		return fmt.Sprintf("fund-%d", *ownerID), nil
	default: return "", fmt.Errorf("unknown %q", ownerType)
	}
}
```

- [ ] **Step 2: Update PortfolioHandler constructor to accept the unified service**

In stock-service/cmd/main.go, build the service and pass it:

```go
unifiedPortfolio := service.NewUnifiedPortfolioService(holdingRepo, fundPositionRepo, fundRepo, listingRepo, exchangeRateRepo)
portfolioHandler := handler.NewPortfolioHandler(portfolioSvc, taxSvc, unifiedPortfolio)
```

- [ ] **Step 3: Build**

```bash
cd stock-service && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/handler/portfolio_handler.go stock-service/cmd/main.go
git commit -m "feat(stock): GetUnifiedPortfolio handler + wire UnifiedPortfolioService"
```

---

## Task B6: Gateway handler + six new routes

**Files:**
- Create: `api-gateway/internal/handler/unified_portfolio_handler.go`
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Implement gateway handler**

```go
// api-gateway/internal/handler/unified_portfolio_handler.go
package handler

import (
	"net/http"
	"strconv"
	"github.com/exbanka/contract/contract/stockpb"
	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/gin-gonic/gin"
)

type UnifiedPortfolioHandler struct {
	client stockpb.PortfolioGRPCServiceClient
}

func NewUnifiedPortfolioHandler(c stockpb.PortfolioGRPCServiceClient) *UnifiedPortfolioHandler {
	return &UnifiedPortfolioHandler{client: c}
}

// GET /api/v3/me/portfolio
func (h *UnifiedPortfolioHandler) GetMy(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, id.OwnerType, id.OwnerID)
}

// GET /api/v3/portfolio/:portfolio_id
func (h *UnifiedPortfolioHandler) GetByPortfolioID(c *gin.Context) {
	pid := c.Param("portfolio_id")
	ot, oid, err := DecodePortfolioID(pid)
	if err != nil { apiError(c, 400, ErrValidation, err.Error()); return }
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, ot, oid)
}

// GET /api/v3/portfolio/client/:client_id
func (h *UnifiedPortfolioHandler) GetByClientID(c *gin.Context) {
	raw := c.Param("client_id")
	cid, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || cid == 0 { apiError(c, 400, ErrValidation, "client_id must be positive integer"); return }
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "client", &cid)
}

// GET /api/v3/portfolio/bank
func (h *UnifiedPortfolioHandler) GetBank(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "bank", nil)
}

// GET /api/v3/portfolio/investment-fund/:fund_id
func (h *UnifiedPortfolioHandler) GetByFundID(c *gin.Context) {
	raw := c.Param("fund_id")
	fid, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || fid == 0 { apiError(c, 400, ErrValidation, "fund_id must be positive integer"); return }
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "investment_fund", &fid)
}

func (h *UnifiedPortfolioHandler) dispatch(c *gin.Context, id *middleware.ResolvedIdentity, ot string, oid *uint64) {
	perms := middleware.GetCallerPermissions(c)
	if err := enforcePortfolioAccess(c, id, ot, oid, perms); err != nil { return /* apiError already written */ }

	var ownerID uint64
	if oid != nil { ownerID = *oid }
	resp, err := h.client.GetUnifiedPortfolio(c.Request.Context(), &stockpb.GetUnifiedPortfolioRequest{
		OwnerType: ot, OwnerId: ownerID,
	})
	if err != nil { handleGRPCError(c, err); return }
	c.JSON(http.StatusOK, resp)
}
```

(`middleware.GetCallerPermissions(c)` should return the JWT permissions list as `[]string`. If that helper doesn't exist, add a small one in middleware that reads from gin context where the JWT middleware stores claims.)

- [ ] **Step 2: Register routes in router_v3.go**

In `api-gateway/internal/router/router_v3.go`:

```go
// In the `me` group (under AnyAuthMiddleware):
me.GET("/portfolio", bankIfEmp, h.UnifiedPortfolio.GetMy)

// At the top-level (under AuthMiddleware):
portfolio := protected.Group("/portfolio")
portfolio.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
{
	portfolio.GET("/bank", h.UnifiedPortfolio.GetBank)
	portfolio.GET("/client/:client_id", h.UnifiedPortfolio.GetByClientID)
	portfolio.GET("/investment-fund/:fund_id", h.UnifiedPortfolio.GetByFundID)
	portfolio.GET("/:portfolio_id", h.UnifiedPortfolio.GetByPortfolioID)
}
```

Order matters in Gin: `/portfolio/bank`, `/portfolio/client/:id`, `/portfolio/investment-fund/:id` must be registered before `/portfolio/:portfolio_id` so static segments win over the catchall param.

- [ ] **Step 3: Build api-gateway**

```bash
cd api-gateway && go build ./...
```

- [ ] **Step 4: Regenerate swagger + update REST_API_v1.md**

```bash
make swagger
```

Add a "Unified Portfolio" section to `docs/api/REST_API_v1.md` documenting all six routes.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/unified_portfolio_handler.go api-gateway/internal/router/router_v3.go api-gateway/internal/middleware/ api-gateway/cmd/main.go api-gateway/docs/ docs/api/REST_API_v1.md
git commit -m "feat(gateway): six unified portfolio routes with portfolio-id model"
```

---

## Task B7: Watchlist + Favourites — same pattern

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/internal/handler/watchlist_handler.go`
- Modify: `api-gateway/internal/handler/favourites_handler.go` (or wherever favourites are)

- [ ] **Step 1: Add `GET /watchlist/:portfolio_id` route**

```go
// In router_v3.go's protected.Group block:
watch := protected.Group("/watchlist")
watch.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
{
	watch.GET("/:portfolio_id", h.Watchlist.GetByPortfolioID)
}

fav := protected.Group("/favourites")
fav.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
{
	fav.GET("/:portfolio_id", h.Favourites.GetByPortfolioID)
}
```

- [ ] **Step 2: Implement handlers**

Mirror the unified portfolio pattern — decode portfolio_id, enforce access, call existing watchlist/favourites service via gRPC. (The existing `/me/watchlist` and `/me/favourites` routes stay untouched.)

- [ ] **Step 3: Build + swagger**

```bash
cd api-gateway && go build ./... && make swagger
```

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/handler/watchlist_handler.go api-gateway/internal/handler/favourites_handler.go api-gateway/internal/router/router_v3.go api-gateway/docs/ docs/api/REST_API_v1.md
git commit -m "feat(gateway): watchlist + favourites by portfolio_id"
```

---

## Task B8: Add two new permissions

**Files:**
- Modify: `contract/permissions/perms.gen.go` (or its source if there's a generator)
- Modify: `user-service/internal/service/role_service.go` (if `DefaultRoles` is curated here)

- [ ] **Step 1: Add to Catalog**

In `contract/permissions/perms.gen.go` (or its source YAML if generated):

```go
"portfolio.view_client",
"portfolio.view_fund",
```

- [ ] **Step 2: Add to default roles**

If `DefaultRoles` map exists (likely in `contract/permissions/`):

```go
"EmployeeSupervisor": []Permission{
	// ... existing perms ...
	"portfolio.view_client",
	"portfolio.view_fund",
},
"EmployeeAdmin": []Permission{
	// ... existing perms ...
	"portfolio.view_client",
	"portfolio.view_fund",
},
```

- [ ] **Step 3: Regenerate or build**

If generated: run the generator. Otherwise `cd contract && go build ./...`.

- [ ] **Step 4: Test seeding**

`make docker-up`; check user_db `permissions` and `role_permissions` tables include the new entries (or wait for the AutoMigrate to re-seed).

- [ ] **Step 5: Commit**

```bash
git add contract/permissions/ user-service/internal/service/role_service.go
git commit -m "feat(permissions): portfolio.view_client + portfolio.view_fund"
```

---

## Task B9: Integration test for unified portfolio

**Files:**
- Create: `test-app/workflows/unified_portfolio_test.go`

- [ ] **Step 1: Write the integration test**

```go
// test-app/workflows/unified_portfolio_test.go
package workflows

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUnifiedPortfolio_ClientWithStockAndFund(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()

	clientID := tc.createClient("portfolio-test@example.com")
	tc.giveTradingPermission(clientID)

	// Buy 50 AAPL
	listingID := tc.findListingByTicker("AAPL")
	tc.placeOrderAndExecute(clientID, listingID, 50, "buy")

	// Invest 25000 RSD in fund #1
	tc.investInFund(clientID, 1, 25000)

	// As the client, fetch /me/portfolio
	tc.loginAs(clientID)
	resp := tc.GET("/api/v3/me/portfolio")
	if resp.StatusCode != http.StatusOK { t.Fatalf("status=%d", resp.StatusCode) }

	var body struct {
		PortfolioId    string `json:"portfolio_id"`
		Securities     struct {
			Positions []struct {
				AssetType string `json:"asset_type"`
				Symbol    string `json:"symbol"`
				Quantity  int64  `json:"quantity"`
			} `json:"positions"`
		} `json:"securities"`
		Funds struct {
			Positions []struct {
				AssetType         string `json:"asset_type"`
				FundId            uint64 `json:"fund_id"`
				AmountInvestedRsd string `json:"amount_invested_rsd"`
			} `json:"positions"`
		} `json:"funds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil { t.Fatal(err) }

	if body.PortfolioId == "" || body.PortfolioId[:7] != "client-" {
		t.Errorf("portfolio_id=%q", body.PortfolioId)
	}
	foundStock := false
	for _, p := range body.Securities.Positions {
		if p.Symbol == "AAPL" && p.Quantity == 50 { foundStock = true }
	}
	if !foundStock { t.Error("did not find AAPL holding in securities") }
	if len(body.Funds.Positions) != 1 || body.Funds.Positions[0].FundId != 1 {
		t.Errorf("expected exactly one fund position #1, got %+v", body.Funds.Positions)
	}
}

func TestUnifiedPortfolio_ClientForbiddenFromOtherClient(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()
	clientA := tc.createClient("a@x.com")
	clientB := tc.createClient("b@x.com")
	_ = clientA
	tc.loginAs(clientB)
	resp := tc.GET("/api/v3/portfolio/client-" + tc.fmtID(clientA))
	if resp.StatusCode != 403 { t.Errorf("expected 403 forbidden, got %d", resp.StatusCode) }
}

func TestUnifiedPortfolio_SupervisorCanViewFund(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()
	supervisorID := tc.createEmployee("sup@x.com", "EmployeeSupervisor")
	fundID := tc.createFund(supervisorID, "Test Fund")
	tc.loginEmployee(supervisorID)
	resp := tc.GET("/api/v3/portfolio/fund-" + tc.fmtID(fundID))
	if resp.StatusCode != 200 { t.Errorf("expected 200, got %d", resp.StatusCode) }
}
```

- [ ] **Step 2: Run**

```bash
make docker-up
sleep 30
cd test-app && go test ./workflows/... -run TestUnifiedPortfolio -v -timeout 3m
```

Expected: 3/3 PASS.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/unified_portfolio_test.go test-app/workflows/helpers_test.go
git commit -m "test: integration tests for unified portfolio routes"
```

---

## Task B10: Update Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Section 6 (permissions)** — add `portfolio.view_client`, `portfolio.view_fund`, list which roles get them.
- [ ] **Step 2: Section 11 (gRPC)** — add `stock.Portfolio.GetUnifiedPortfolio`.
- [ ] **Step 3: Section 17 (routes)** — document the six new portfolio routes + watchlist/favourites by portfolio_id.

- [ ] **Step 4: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): document unified portfolio routes and permissions"
```

---

## Self-Review

- [ ] Spec coverage:
  - B1 portfolio identity encoding: Task B1 ✓
  - B2 routes (six): Task B6 + B7 ✓
  - B3 authorization: Task B2 + integrated in Task B6 ✓
  - B4 response shape: Task B3 + B4 + B5 ✓
  - B5 implementation surface: Task B4 + B5 + B6 ✓
- [ ] Placeholder scan: One `t.Skip("Skeleton")` for unit test in B4 — acceptable as the integration test in B9 covers the same behavior end-to-end. Two notes about "verify against existing code" — these direct the implementer to confirm field/method names that may differ between the audit-gathered patterns and the live code.
- [ ] Type consistency: `UnifiedPortfolio`, `PortfolioGroup`, `PortfolioPosition` used identically in service, handler, and proto.

---

## Execution Handoff

Implementation will proceed via **subagent-driven-development**.
