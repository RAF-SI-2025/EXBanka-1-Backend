package service

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// UnifiedPortfolioService composes a grouped portfolio view for any owner
// (client, bank, or investment_fund) by fanning out to the holding and fund-
// position repositories and enriching with current listing prices.
//
// Fund value estimation: Σ(fund_holding.quantity × current_listing_price).
// The fund's liquid RSD balance lives in account-service and is NOT fetched
// here to avoid cross-service coupling at the repository layer; callers that
// need the full liquidation value should call account-service separately.
// This is documented as a known simplification.
type UnifiedPortfolioService struct {
	holdingRepo     *repository.HoldingRepository
	fundPosRepo     *repository.ClientFundPositionRepository
	fundRepo        *repository.FundRepository
	fundHoldingRepo *repository.FundHoldingRepository
	listingRepo     *repository.ListingRepository
}

// NewUnifiedPortfolioService constructs the service with all required repos.
func NewUnifiedPortfolioService(
	holdingRepo *repository.HoldingRepository,
	fundPosRepo *repository.ClientFundPositionRepository,
	fundRepo *repository.FundRepository,
	fundHoldingRepo *repository.FundHoldingRepository,
	listingRepo *repository.ListingRepository,
) *UnifiedPortfolioService {
	return &UnifiedPortfolioService{
		holdingRepo:     holdingRepo,
		fundPosRepo:     fundPosRepo,
		fundRepo:        fundRepo,
		fundHoldingRepo: fundHoldingRepo,
		listingRepo:     listingRepo,
	}
}

// UnifiedPortfolio is the composed portfolio result returned by Get.
type UnifiedPortfolio struct {
	OwnerType      string
	OwnerID        *uint64
	OwnerName      string
	TotalValueRSD  decimal.Decimal
	TotalProfitRSD decimal.Decimal
	TotalProfitPct decimal.Decimal
	Securities     PortfolioGroup
	Funds          PortfolioGroup
}

// PortfolioGroup is one asset-class slice of a portfolio with aggregate totals.
type PortfolioGroup struct {
	TotalValueRSD  decimal.Decimal
	TotalProfitRSD decimal.Decimal
	TotalProfitPct decimal.Decimal
	Positions      []PortfolioPosition
}

// PortfolioPosition is one line in a portfolio — either a security holding or a
// fund position. Fields that don't apply to a given AssetType are zero/empty.
type PortfolioPosition struct {
	AssetType         string
	Symbol            string
	FundID            uint64
	FundName          string
	ContractID        uint64
	Quantity          int64
	AvgCostRSD        decimal.Decimal
	CurrentPriceRSD   decimal.Decimal
	CurrentValueRSD   decimal.Decimal
	PLRSD             decimal.Decimal
	PLPct             decimal.Decimal
	AmountInvestedRSD decimal.Decimal
	PctOfFund         decimal.Decimal
	StrikeRSD         decimal.Decimal
	PremiumPaidRSD    decimal.Decimal
	IntrinsicValueRSD decimal.Decimal
	SettlementDate    *time.Time
	LastUpdated       time.Time
}

// Get returns the unified portfolio for the given owner. ownerID may be nil
// when ownerType == "bank".
func (s *UnifiedPortfolioService) Get(_ context.Context, ownerType string, ownerID *uint64) (*UnifiedPortfolio, error) {
	if ownerType != "bank" && ownerID == nil {
		return nil, fmt.Errorf("owner_id required for %s", ownerType)
	}

	ot := model.OwnerType(ownerType)

	// ── Securities ───────────────────────────────────────────────────────────
	holdings, _, err := s.holdingRepo.ListByOwner(ot, ownerID, repository.HoldingFilter{PageSize: 10000})
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}

	priceCache, err := s.fetchListingPrices(holdings)
	if err != nil {
		return nil, fmt.Errorf("fetch listing prices: %w", err)
	}

	out := &UnifiedPortfolio{OwnerType: ownerType, OwnerID: ownerID}

	for _, h := range holdings {
		pos := composeHoldingPosition(h, priceCache)
		out.Securities.Positions = append(out.Securities.Positions, pos)
		out.Securities.TotalValueRSD = out.Securities.TotalValueRSD.Add(pos.CurrentValueRSD)
		out.Securities.TotalProfitRSD = out.Securities.TotalProfitRSD.Add(pos.PLRSD)
	}

	// ── Fund positions ────────────────────────────────────────────────────────
	fundPositions, err := s.fundPosRepo.ListByOwner(ot, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list fund positions: %w", err)
	}

	for _, fp := range fundPositions {
		pos, fErr := s.composeFundPosition(fp)
		if fErr != nil {
			return nil, fErr
		}
		out.Funds.Positions = append(out.Funds.Positions, pos)
		out.Funds.TotalValueRSD = out.Funds.TotalValueRSD.Add(pos.CurrentValueRSD)
		out.Funds.TotalProfitRSD = out.Funds.TotalProfitRSD.Add(pos.PLRSD)
	}

	// ── Percent-gain calculations ─────────────────────────────────────────────
	invested := totalInvested(out.Securities, out.Funds)
	if !invested.IsZero() {
		out.Securities.TotalProfitPct = pctCalc(out.Securities.TotalProfitRSD, invested)
		out.Funds.TotalProfitPct = pctCalc(out.Funds.TotalProfitRSD, invested)
	}

	// ── Grand totals ──────────────────────────────────────────────────────────
	out.TotalValueRSD = out.Securities.TotalValueRSD.Add(out.Funds.TotalValueRSD)
	out.TotalProfitRSD = out.Securities.TotalProfitRSD.Add(out.Funds.TotalProfitRSD)
	if !invested.IsZero() {
		out.TotalProfitPct = pctCalc(out.TotalProfitRSD, invested)
	}

	return out, nil
}

// fetchListingPrices resolves a map of listingID → current_price for all
// unique listing IDs appearing in the given holdings.
func (s *UnifiedPortfolioService) fetchListingPrices(holdings []model.Holding) (map[uint64]decimal.Decimal, error) {
	// Deduplicate
	seen := make(map[uint64]struct{}, len(holdings))
	ids := make([]uint64, 0, len(holdings))
	for _, h := range holdings {
		if _, ok := seen[h.ListingID]; !ok {
			seen[h.ListingID] = struct{}{}
			ids = append(ids, h.ListingID)
		}
	}
	listings, err := s.listingRepo.ListByIDs(ids)
	if err != nil {
		return nil, err
	}
	m := make(map[uint64]decimal.Decimal, len(listings))
	for _, l := range listings {
		m[l.ID] = l.Price
	}
	return m, nil
}

// fetchFundHoldingPrices resolves a map of (securityType, securityID) → price
// for the given fund holdings. Falls back to AveragePriceRSD when the listing
// is not found (e.g. options, forex).
func (s *UnifiedPortfolioService) fetchFundHoldingPrices(holdings []model.FundHolding) (map[uint64]decimal.Decimal, error) {
	// Group by security type to batch the queries.
	byType := make(map[string][]uint64)
	for _, h := range holdings {
		byType[h.SecurityType] = append(byType[h.SecurityType], h.SecurityID)
	}

	priceBySecID := make(map[uint64]decimal.Decimal, len(holdings))
	for secType, ids := range byType {
		listings, err := s.listingRepo.ListBySecurityIDsAndType(ids, secType)
		if err != nil {
			return nil, err
		}
		for _, l := range listings {
			priceBySecID[l.SecurityID] = l.Price
		}
	}
	return priceBySecID, nil
}

// computeFundValue estimates the fund's total NAV as Σ(holding.qty × listing_price).
// The fund's liquid RSD balance is excluded (see note on UnifiedPortfolioService).
func (s *UnifiedPortfolioService) computeFundValue(fundID uint64) (decimal.Decimal, error) {
	holdings, err := s.fundHoldingRepo.ListByFundFIFO(fundID)
	if err != nil {
		return decimal.Zero, fmt.Errorf("list fund holdings for fund %d: %w", fundID, err)
	}
	if len(holdings) == 0 {
		return decimal.Zero, nil
	}

	prices, err := s.fetchFundHoldingPrices(holdings)
	if err != nil {
		return decimal.Zero, fmt.Errorf("fetch fund holding prices: %w", err)
	}

	total := decimal.Zero
	for _, h := range holdings {
		price, ok := prices[h.SecurityID]
		if !ok {
			// Fallback to average cost when listing not found
			price = h.AveragePriceRSD
		}
		total = total.Add(price.Mul(decimal.NewFromInt(h.Quantity)))
	}
	return total, nil
}

func (s *UnifiedPortfolioService) composeFundPosition(fp model.ClientFundPosition) (PortfolioPosition, error) {
	fund, err := s.fundRepo.GetByID(fp.FundID)
	if err != nil {
		return PortfolioPosition{}, fmt.Errorf("get fund %d: %w", fp.FundID, err)
	}

	// Compute pct_of_fund = this owner's contribution / sum(all contributions to fund)
	allPositions, err := s.fundPosRepo.ListByFund(fp.FundID)
	if err != nil {
		return PortfolioPosition{}, fmt.Errorf("list all positions for fund %d: %w", fp.FundID, err)
	}
	var totalContributed decimal.Decimal
	for _, p := range allPositions {
		totalContributed = totalContributed.Add(p.TotalContributedRSD)
	}

	pctOfFund := decimal.Zero
	if !totalContributed.IsZero() {
		pctOfFund = fp.TotalContributedRSD.Div(totalContributed).Mul(decimal.NewFromInt(100))
	}

	fundValue, err := s.computeFundValue(fp.FundID)
	if err != nil {
		return PortfolioPosition{}, err
	}

	// Position's current value = fund's total value × pct_of_fund / 100
	currentValue := decimal.Zero
	if !fundValue.IsZero() {
		currentValue = fundValue.Mul(pctOfFund).Div(decimal.NewFromInt(100))
	}

	pl := currentValue.Sub(fp.TotalContributedRSD)

	return PortfolioPosition{
		AssetType:         "investment_fund",
		FundID:            fp.FundID,
		FundName:          fund.Name,
		AmountInvestedRSD: fp.TotalContributedRSD,
		CurrentValueRSD:   currentValue,
		PctOfFund:         pctOfFund,
		PLRSD:             pl,
		PLPct:             pctCalc(pl, fp.TotalContributedRSD),
		LastUpdated:       fp.UpdatedAt,
	}, nil
}

// composeHoldingPosition converts a Holding row + price cache into a position.
func composeHoldingPosition(h model.Holding, prices map[uint64]decimal.Decimal) PortfolioPosition {
	currentPrice := prices[h.ListingID] // zero when listing not found
	qty := decimal.NewFromInt(h.Quantity)
	currentValue := currentPrice.Mul(qty)
	totalCost := h.AveragePrice.Mul(qty)
	pl := currentValue.Sub(totalCost)

	return PortfolioPosition{
		AssetType:       mapSecurityType(h.SecurityType),
		Symbol:          h.Ticker,
		Quantity:        h.Quantity,
		AvgCostRSD:      h.AveragePrice,
		CurrentPriceRSD: currentPrice,
		CurrentValueRSD: currentValue,
		PLRSD:           pl,
		PLPct:           pctCalc(pl, totalCost),
		LastUpdated:     h.UpdatedAt,
	}
}

func mapSecurityType(s string) string {
	switch s {
	case "stock":
		return "stock"
	case "option":
		return "option"
	case "futures", "future":
		return "future"
	default:
		return s
	}
}

func pctCalc(numerator, denominator decimal.Decimal) decimal.Decimal {
	if denominator.IsZero() {
		return decimal.Zero
	}
	return numerator.Div(denominator).Mul(decimal.NewFromInt(100))
}

func totalInvested(s PortfolioGroup, f PortfolioGroup) decimal.Decimal {
	sum := decimal.Zero
	for _, p := range s.Positions {
		sum = sum.Add(p.AvgCostRSD.Mul(decimal.NewFromInt(p.Quantity)))
	}
	for _, p := range f.Positions {
		sum = sum.Add(p.AmountInvestedRSD)
	}
	return sum
}
