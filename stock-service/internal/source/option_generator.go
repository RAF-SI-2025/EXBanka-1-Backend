package source

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// GenerateOptionsForStock creates option contracts for a stock using the
// algorithmic approach (Approach 2 from spec):
// 1. Generate settlement dates: every 6 days for 30 days, then 6 more at 30-day intervals
// 2. Strike prices: 5 above and 5 below rounded stock price
// 3. For each date x strike: one CALL + one PUT
func GenerateOptionsForStock(stock *model.Stock) []model.Option {
	if stock.Price.IsZero() {
		return nil
	}

	now := time.Now()
	dates := generateSettlementDates(now)
	strikes := generateStrikePrices(stock.Price)

	var options []model.Option
	for _, date := range dates {
		for _, strike := range strikes {
			dateStr := date.Format("060102")
			strikeCents := strike.Mul(decimal.NewFromInt(100)).IntPart()

			// CALL option
			callTicker := fmt.Sprintf("%s%sC%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            callTicker,
				Name:              fmt.Sprintf("%s Call %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "call",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "call"),
				OpenInterest:      0,
				SettlementDate:    date,
			})

			// PUT option
			putTicker := fmt.Sprintf("%s%sP%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            putTicker,
				Name:              fmt.Sprintf("%s Put %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "put",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "put"),
				OpenInterest:      0,
				SettlementDate:    date,
			})
		}
	}
	return options
}

func generateSettlementDates(now time.Time) []time.Time {
	var dates []time.Time
	// Phase 1: every 6 days until 30 days out
	d := now.AddDate(0, 0, 6)
	for d.Sub(now).Hours()/24 <= 30 {
		dates = append(dates, d)
		d = d.AddDate(0, 0, 6)
	}
	// Phase 2: 6 more dates at 30-day intervals
	last := dates[len(dates)-1]
	for i := 0; i < 6; i++ {
		last = last.AddDate(0, 0, 30)
		dates = append(dates, last)
	}
	return dates
}

func generateStrikePrices(currentPrice decimal.Decimal) []decimal.Decimal {
	rounded := currentPrice.Round(0)
	var strikes []decimal.Decimal
	for i := -5; i <= 5; i++ {
		strikes = append(strikes, rounded.Add(decimal.NewFromInt(int64(i))))
	}
	return strikes
}

// estimatePremium is a simplified premium estimation.
// A proper Black-Scholes implementation could replace this.
func estimatePremium(stockPrice, strikePrice decimal.Decimal, settlementDate time.Time, optionType string) decimal.Decimal {
	daysToExpiry := decimal.NewFromFloat(time.Until(settlementDate).Hours() / 24)
	if daysToExpiry.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}

	// Intrinsic value
	var intrinsic decimal.Decimal
	if optionType == "call" {
		intrinsic = stockPrice.Sub(strikePrice)
	} else {
		intrinsic = strikePrice.Sub(stockPrice)
	}
	if intrinsic.IsNegative() {
		intrinsic = decimal.Zero
	}

	// Time value: rough approximation = stockPrice * 0.01 * sqrt(days/365)
	timeRatio := daysToExpiry.Div(decimal.NewFromInt(365))
	// Approximate sqrt via Babylonian method for one iteration
	sqrtApprox := timeRatio.Add(decimal.NewFromInt(1)).Div(decimal.NewFromInt(2))
	timeValue := stockPrice.Mul(decimal.NewFromFloat(0.01)).Mul(sqrtApprox)

	return intrinsic.Add(timeValue).Round(2)
}
