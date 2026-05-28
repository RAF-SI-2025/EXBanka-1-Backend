package service

// fund_statistics.go — E1 (Plan E, 2026-05-28)
//
// FundStatistics is the computed snapshot returned by Statistics().
// All monetary fields are in RSD.

import (
	"context"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// FundStatistics carries the enriched fields for GET /investment-funds/:id.
type FundStatistics struct {
	InvestorCount         int64
	TotalContributedRSD   decimal.Decimal
	LiquidRSDBal          decimal.Decimal
	TotalHoldingsValueRSD decimal.Decimal
	TotalValueRSD         decimal.Decimal
	// TotalDividendsPaidRSD is always zero until E4 implements the
	// fund_dividend_payments table. The field is included here so callers
	// can rely on it regardless of when E4 lands.
	TotalDividendsPaidRSD decimal.Decimal // TODO E4: sum from fund_dividend_payments
	ProfitRSD             decimal.Decimal
	ProfitPct             decimal.Decimal
}

// FundHoldingSnap is a single holding in the enriched fund detail response.
type FundHoldingSnap struct {
	SecurityType    string
	Ticker          string
	Quantity        int64
	AveragePriceRSD decimal.Decimal
	CurrentPriceRSD decimal.Decimal
	CurrentValueRSD decimal.Decimal
}

// Statistics computes the enriched fund snapshot for the detail response.
// Safe to call when optional deps (accounts, holdings, positions, listings)
// are not wired — each missing dep degrades gracefully to zero.
func (s *FundService) Statistics(ctx context.Context, fund *model.InvestmentFund) (FundStatistics, error) {
	var stat FundStatistics

	// investor_count + total_contributed_rsd from client_fund_positions.
	if s.positions != nil {
		rows, err := s.positions.ListByFund(fund.ID)
		if err == nil {
			for _, p := range rows {
				if p.TotalContributedRSD.IsPositive() {
					stat.InvestorCount++
				}
				stat.TotalContributedRSD = stat.TotalContributedRSD.Add(p.TotalContributedRSD)
			}
		}
	}

	// liquid_rsd_balance = fund's RSD account balance.
	if s.accounts != nil {
		acct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
		if err == nil {
			if d, perr := decimal.NewFromString(acct.Balance); perr == nil {
				stat.LiquidRSDBal = d
			}
		}
	}

	// total_holdings_value_rsd = Σ (quantity × current_price_rsd) across all
	// fund holdings with non-zero quantity.
	if s.holdings != nil && s.listingRepo != nil {
		holdings, err := s.holdings.ListByFundFIFO(fund.ID)
		if err == nil {
			for _, h := range holdings {
				listing, lerr := s.listingRepo.GetBySecurityIDAndType(h.SecurityID, h.SecurityType)
				if lerr != nil {
					continue
				}
				px := listing.Price
				stat.TotalHoldingsValueRSD = stat.TotalHoldingsValueRSD.Add(
					decimal.NewFromInt(h.Quantity).Mul(px),
				)
			}
		}
	}

	stat.TotalValueRSD = stat.LiquidRSDBal.Add(stat.TotalHoldingsValueRSD)

	// total_dividends_paid_rsd — always zero until E4.
	stat.TotalDividendsPaidRSD = decimal.Zero

	stat.ProfitRSD = stat.TotalValueRSD.Sub(stat.TotalContributedRSD)
	if stat.TotalContributedRSD.IsPositive() {
		stat.ProfitPct = stat.ProfitRSD.Div(stat.TotalContributedRSD).Mul(decimal.NewFromInt(100))
	}

	return stat, nil
}

// FundHoldingsSnapshot returns the enriched holdings list for the detail
// response. For each holding it resolves the ticker from stocks repository
// and current price from the listing.
func (s *FundService) FundHoldingsSnapshot(ctx context.Context, fund *model.InvestmentFund) ([]FundHoldingSnap, error) {
	if s.holdings == nil {
		return nil, nil
	}
	holdings, err := s.holdings.ListByFundFIFO(fund.ID)
	if err != nil {
		return nil, err
	}
	out := make([]FundHoldingSnap, 0, len(holdings))
	for _, h := range holdings {
		snap := FundHoldingSnap{
			SecurityType:    h.SecurityType,
			Quantity:        h.Quantity,
			AveragePriceRSD: h.AveragePriceRSD,
		}
		if s.listingRepo != nil {
			listing, lerr := s.listingRepo.GetBySecurityIDAndType(h.SecurityID, h.SecurityType)
			if lerr == nil && listing != nil {
				snap.CurrentPriceRSD = listing.Price
				snap.CurrentValueRSD = decimal.NewFromInt(h.Quantity).Mul(listing.Price)
			}
		}
		out = append(out, snap)
	}
	return out, nil
}
