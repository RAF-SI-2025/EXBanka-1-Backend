//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Stocks ---

func TestSecurities_ListStocks(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/stocks")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "stocks")
	helpers.RequireField(t, resp, "total_count")
}

func TestSecurities_ListStocks_Unauthenticated(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/securities/stocks")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestSecurities_ListStocks_SearchByTicker(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/stocks?search=AAPL")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestSecurities_ListStocks_SortByPrice(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/stocks?sort_by=price&sort_order=desc")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestSecurities_ListStocks_InvalidSortBy(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/stocks?sort_by=invalid")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestSecurities_GetStock(t *testing.T) {
	adminC := loginAsAdmin(t)
	stockID, _ := getFirstStockListingID(t, adminC)

	resp, err := adminC.GET("/api/securities/stocks/" + helpers.FormatID(int(stockID)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "ticker")
	helpers.RequireField(t, resp, "listing")
}

func TestSecurities_GetStockHistory(t *testing.T) {
	adminC := loginAsAdmin(t)
	stockID, _ := getFirstStockListingID(t, adminC)

	resp, err := adminC.GET("/api/securities/stocks/" + helpers.FormatID(int(stockID)) + "/history?period=month")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "history")
}

func TestSecurities_GetStockHistory_InvalidPeriod(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/stocks/1/history?period=invalid")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

// --- Futures ---

func TestSecurities_ListFutures(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/futures")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "futures")
	helpers.RequireField(t, resp, "total_count")
}

func TestSecurities_ListFutures_SettlementDateFilter(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/futures?settlement_date_from=2026-01-01&settlement_date_to=2026-12-31")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// --- Forex ---

func TestSecurities_ListForexPairs(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/forex")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "forex_pairs")
}

func TestSecurities_ListForexPairs_LiquidityFilter(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/forex?liquidity=high")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestSecurities_ListForexPairs_InvalidLiquidity(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/forex?liquidity=invalid")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

// --- Options ---

func TestSecurities_ListOptions_RequiresStockID(t *testing.T) {
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/securities/options")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestSecurities_ListOptions_WithStockID(t *testing.T) {
	adminC := loginAsAdmin(t)
	stockID, _ := getFirstStockListingID(t, adminC)

	resp, err := adminC.GET("/api/securities/options?stock_id=" + helpers.FormatID(int(stockID)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "options")
}

func TestSecurities_ListOptions_FilterByType(t *testing.T) {
	adminC := loginAsAdmin(t)
	stockID, _ := getFirstStockListingID(t, adminC)

	resp, err := adminC.GET("/api/securities/options?stock_id=" + helpers.FormatID(int(stockID)) + "&option_type=call")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// --- Client access ---

func TestSecurities_ClientCanViewStocksAndFutures(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/securities/stocks")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	resp, err = clientC.GET("/api/securities/futures")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}
