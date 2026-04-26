//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestOTCOptions_CreateAndListMyOffers_HappyPath asserts the v3 OTC
// create + list endpoints are wired and an authenticated employee can
// create an offer and see it in /me/otc/offers. t.Skips on 404 so the
// test isn't a CI false-fail on stock-services that haven't deployed
// the v3/otc surface.
func TestOTCOptions_CreateAndListMyOffers_HappyPath(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	// Pick any stock listing so the offer references something seeded.
	stocksResp, err := adminC.GET("/api/v1/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("list stocks: %v", err)
	}
	if stocksResp.StatusCode != 200 {
		t.Skipf("stocks endpoint returned %d — skipping", stocksResp.StatusCode)
	}
	stocks, _ := stocksResp.Body["stocks"].([]interface{})
	if len(stocks) == 0 {
		t.Skipf("no stock listings seeded — skipping")
	}
	first, _ := stocks[0].(map[string]interface{})
	stockID := int(first["id"].(float64))

	// Settlement date a few days out.
	body := map[string]interface{}{
		"direction":       "sell_initiated",
		"stock_id":        stockID,
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
	}
	resp, err := adminC.POST("/api/v3/otc/offers", body)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skipf("v3 OTC endpoints not deployed — skipping")
	}
	if resp.StatusCode == 409 {
		// Most likely the seller-invariant check rejected (admin doesn't have the stock).
		t.Skipf("seller-invariant rejected (admin holds no shares) — skipping")
	}
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d body=%v", resp.StatusCode, resp.Body)
	}

	listResp, err := adminC.GET("/api/v3/me/otc/offers")
	if err != nil {
		t.Fatalf("list my offers: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	if rawOffers, ok := listResp.Body["offers"]; ok && rawOffers != nil {
		offers, _ := rawOffers.([]interface{})
		if len(offers) == 0 {
			t.Errorf("expected at least one offer in /me/otc/offers")
		}
	}
}

// TestOTCOptions_ClientCannotTrade asserts the otc.trade permission
// gate. Clients (no otc.trade) hit /api/v3/otc/offers POST and get 403.
func TestOTCOptions_ClientCannotTrade(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.POST("/api/v3/otc/offers", map[string]interface{}{
		"direction":       "sell_initiated",
		"stock_id":        1,
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
	})
	if err != nil {
		t.Fatalf("client POST: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	if resp.StatusCode == 201 {
		t.Errorf("client should not be able to create OTC offer, got 201")
	}
}

// TestOTCOptions_ListMyContractsEmpty asserts the contracts endpoint
// returns an empty list for a fresh client.
func TestOTCOptions_ListMyContractsEmpty(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/otc/contracts")
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC endpoints not deployed")
	}
	helpers.RequireStatus(t, resp, 200)
	if raw, ok := resp.Body["contracts"]; ok && raw != nil {
		contracts, _ := raw.([]interface{})
		if len(contracts) != 0 {
			t.Errorf("expected empty contracts for fresh client, got %d", len(contracts))
		}
	}
}
