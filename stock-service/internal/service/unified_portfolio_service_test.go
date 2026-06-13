package service

import "testing"

func TestUnifiedPortfolio_ClientWithStockAndFund(t *testing.T) {
	// Skeleton — flesh out with mock repos once mock infrastructure is in place.
	// The composition logic is covered end-to-end by the integration tests in
	// test-app/workflows/unified_portfolio_test.go (B9, skipped for now because
	// it requires Docker).
	//
	// What a full unit test would verify:
	//   - Securities.Positions[0].CurrentValueRSD == qty × current_price
	//   - Securities.Positions[0].PLRSD == (current_price - avg_cost) × qty
	//   - Funds.Positions[0].CurrentValueRSD == fund_value × pct_of_fund / 100
	//   - Funds.Positions[0].PLRSD == current_value - amount_invested
	//   - TotalValueRSD == securities_value + funds_value
	t.Skip("Skeleton — flesh out with mock repos")
}
