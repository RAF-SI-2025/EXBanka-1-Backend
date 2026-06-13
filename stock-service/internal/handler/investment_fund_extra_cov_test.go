package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func TestInvestmentFundHandler_ListFunds_MetricSort(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	ctx := context.Background()
	_, _ = h.CreateFund(ctx, &stockpb.CreateFundRequest{Name: "A"})
	_, _ = h.CreateFund(ctx, &stockpb.CreateFundRequest{Name: "B"})

	// A metric sort_by routes through ListSortedByMetric. With no snapshots the
	// funds report metrics_available=false but the branch + fillFundMetrics run.
	resp, err := h.ListFunds(ctx, &stockpb.ListFundsRequest{
		Page: 1, PageSize: 10, SortBy: "annualized_return", SortOrder: "desc",
	})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetTotal() != 2 {
		t.Fatalf("want 2 funds, got %d", resp.GetTotal())
	}
	for _, f := range resp.GetFunds() {
		if f.GetMetricsAvailable() {
			t.Fatalf("expected metrics unavailable, got available for fund %d", f.GetId())
		}
		if f.GetAnnualizedReturnPct() != "0" {
			t.Fatalf("want 0 annualized return, got %q", f.GetAnnualizedReturnPct())
		}
	}
}

func TestInvestmentFundHandler_GetFund_WithHoldingsEnriched(t *testing.T) {
	h, db := newInvestmentFundHandlerFixture(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&model.FundHolding{}, &model.Listing{}, &model.Stock{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	created, err := h.CreateFund(ctx, &stockpb.CreateFundRequest{Name: "Detailed"})
	testutil.RequireNoGRPCError(t, err)

	// Seed a fund holding + its listing + stock so the holdings-loop body and
	// the listing/stock enrichment branches all execute.
	if err := db.Create(&model.FundHolding{
		FundID: created.GetId(), SecurityType: "stock", SecurityID: 100,
		Quantity: 4, AveragePriceRSD: decimal.NewFromInt(50),
	}).Error; err != nil {
		t.Fatalf("seed fund holding: %v", err)
	}
	if err := db.Create(&model.Stock{ID: 100, Ticker: "AAPL"}).Error; err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	if err := db.Create(&model.Listing{ID: 1, SecurityID: 100, SecurityType: "stock", Price: decimal.NewFromInt(75)}).Error; err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	h2 := h.WithFundDetailDeps(
		repository.NewFundHoldingRepository(db),
		repository.NewListingRepository(db),
		repository.NewStockRepository(db),
	)
	resp, err := h2.GetFund(ctx, &stockpb.GetFundRequest{FundId: created.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if len(resp.GetHoldings()) != 1 {
		t.Fatalf("want 1 holding, got %d", len(resp.GetHoldings()))
	}
	item := resp.GetHoldings()[0]
	if item.GetTicker() != "AAPL" {
		t.Fatalf("want enriched ticker AAPL, got %q", item.GetTicker())
	}
	if item.GetCurrentPriceRsd() != "75" {
		t.Fatalf("want current price 75, got %q", item.GetCurrentPriceRsd())
	}
	if item.GetCurrentValueRsd() != "300" {
		t.Fatalf("want current value 300, got %q", item.GetCurrentValueRsd())
	}
}
