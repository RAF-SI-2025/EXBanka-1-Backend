package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

// PositionDTO is the rich shape returned by ListMyPositions /
// ListBankPositions: contribution + derived current value, profit and
// percentage of total fund value.
type PositionDTO struct {
	FundID          uint64
	FundName        string
	ManagerFullName string
	ContributionRSD decimal.Decimal
	PercentageFund  decimal.Decimal // 0..1; multiply by 100 for percent display
	CurrentValueRSD decimal.Decimal
	ProfitRSD       decimal.Decimal
	LastChangedAt   time.Time
}

// fundValueRSD returns fund cash + Σ holding (qty × current_price_rsd).
// Holdings priced in non-RSD listings are converted via exchange-service.
// Falls back to cash-only when the position-reads deps aren't fully wired.
func (s *FundService) fundValueRSD(ctx context.Context, fund *model.InvestmentFund) (decimal.Decimal, error) {
	cash := decimal.Zero
	if s.accounts != nil {
		acct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
		if err == nil {
			if d, perr := decimal.NewFromString(acct.Balance); perr == nil {
				cash = d
			}
		}
	}
	if s.holdings == nil || s.listingRepo == nil {
		return cash, nil
	}
	holdings, err := s.holdings.ListByFundFIFO(fund.ID)
	if err != nil {
		return cash, err
	}
	total := cash
	for _, h := range holdings {
		listing, err := s.listingRepo.GetBySecurityIDAndType(h.SecurityID, h.SecurityType)
		if err != nil {
			continue
		}
		px := listing.Price
		if listing.Exchange.Currency != "" && listing.Exchange.Currency != "RSD" && s.exchange != nil {
			conv, cerr := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: listing.Exchange.Currency,
				ToCurrency:   "RSD",
				Amount:       px.String(),
			})
			if cerr == nil {
				if d, perr := decimal.NewFromString(conv.ConvertedAmount); perr == nil {
					px = d
				}
			}
		}
		total = total.Add(decimal.NewFromInt(h.Quantity).Mul(px))
	}
	return total, nil
}

// ListMyPositionsDTO returns rich position rows for (userID, systemType).
// Falls back to plain rows when position-reads deps aren't wired.
func (s *FundService) ListMyPositionsDTO(ctx context.Context, userID uint64, systemType string) ([]PositionDTO, error) {
	if s.positions == nil {
		return nil, nil
	}
	rows, err := s.positions.ListByOwner(userID, systemType)
	if err != nil {
		return nil, err
	}
	out := make([]PositionDTO, 0, len(rows))
	for _, p := range rows {
		fund, err := s.repo.GetByID(p.FundID)
		if err != nil {
			continue
		}
		dto := PositionDTO{
			FundID:          p.FundID,
			FundName:        fund.Name,
			ContributionRSD: p.TotalContributedRSD,
			LastChangedAt:   p.UpdatedAt,
		}
		fundValue, fErr := s.fundValueRSD(ctx, fund)
		if fErr != nil || fundValue.IsZero() {
			// Filter zeroed-out positions when contribution is also zero.
			if p.TotalContributedRSD.IsZero() {
				continue
			}
			out = append(out, dto)
			continue
		}
		totalContrib, err := s.positions.SumTotalContributed(p.FundID)
		if err != nil || totalContrib.IsZero() {
			out = append(out, dto)
			continue
		}
		pct := p.TotalContributedRSD.Div(totalContrib)
		curVal := fundValue.Mul(pct)
		profit := curVal.Sub(p.TotalContributedRSD)
		if p.TotalContributedRSD.IsZero() && curVal.IsZero() {
			continue
		}
		dto.PercentageFund = pct
		dto.CurrentValueRSD = curVal
		dto.ProfitRSD = profit
		out = append(out, dto)
	}
	return out, nil
}

// ListBankPositionsDTO is ListMyPositionsDTO scoped to the bank sentinel.
func (s *FundService) ListBankPositionsDTO(ctx context.Context) ([]PositionDTO, error) {
	return s.ListMyPositionsDTO(ctx, bankSentinelUserID, "employee")
}
