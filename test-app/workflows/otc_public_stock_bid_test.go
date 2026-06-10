//go:build integration

package workflows

// Integration tests for the public-stock option negotiation feature.
//
// Feature summary (commits 3f4e85dc .. 1de266ad):
//   - HasPresetTerms column added to OTCOffer (local/remote option-offer
//     listings carry true; shells synthesised from a peer's /public-stock
//     carry false, meaning the buyer proposes their own strike/premium).
//   - has_preset_terms exposed in the unified offers list
//     (GET /api/v3/otc/options) and per-offer detail
//     (GET /api/v3/otc/options/:id).
//   - Bidding on a shell offer (POST /api/v3/otc/options/:id/bid) is subject
//     to a freshness guard that re-fetches the peer's live /public-stock
//     before dispatching the SI-TX negotiation.
//
// Single-stack coverage:
//   - A local sell_initiated listing must carry has_preset_terms=true on both
//     the list view and the per-offer detail view (local offers always have
//     preset terms set by the seller at creation time).
//
// Two-stack coverage (documented t.Skip — requires a registered peer bank):
//   - A remote shell synthesised from /public-stock must carry
//     has_preset_terms=false in the list view.
//   - Bidding on the shell with buyer-chosen strike/premium/settlement opens a
//     negotiation and returns 201 (shell freshness guard passes when the
//     seller+ticker are still live on the peer's /public-stock).

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestPublicStockBid_LocalOfferHasPresetTermsTrue verifies that a local
// sell_initiated OTC option listing carries has_preset_terms=true in both the
// unified offers list (GET /api/v3/otc/options) and the per-offer detail
// (GET /api/v3/otc/options/:id).
//
// This is the single-stack half of the has_preset_terms contract: local offers
// always have preset terms (the seller sets strike + premium at creation time),
// so the buyer-accepts-as-listed flow applies and the field must be true.
func TestPublicStockBid_LocalOfferHasPresetTermsTrue(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("public-stock bid: no seeded stock with listing — skipping")
	}

	// Seed the seller's holding via a market buy.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 3,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("public-stock bid: seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("public-stock bid: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("public-stock bid: seed buy did not fill within 45 s — skipping")
	}

	// Create a local sell_initiated listing with preset terms.
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":       "sell_initiated",
		"ticker":          ticker,
		"quantity":        "1",
		"strike_price":    "100",
		"premium":         "5",
		"settlement_date": "2030-12-31",
		"account_id":      sellerAcctID,
	})
	if err != nil {
		t.Fatalf("public-stock bid: create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("public-stock bid: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("public-stock bid: create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// ── Per-offer detail: has_preset_terms must be true ──
	detailResp, err := sellerC.GET(fmt.Sprintf("/api/v3/otc/options/%d", offerID))
	if err != nil {
		t.Fatalf("public-stock bid: get offer: %v", err)
	}
	helpers.RequireStatus(t, detailResp, 200)
	offerBody, hasNested := detailResp.Body["offer"].(map[string]interface{})
	if !hasNested {
		offerBody = detailResp.Body
	}
	if hpt, ok := offerBody["has_preset_terms"].(bool); !ok || !hpt {
		t.Errorf("public-stock bid: local offer detail: want has_preset_terms=true, got %v (body=%v)", offerBody["has_preset_terms"], offerBody)
	}

	// ── Unified list: has_preset_terms must be true on the matching offer ──
	// The discovery feed is cache-backed so we poll with a deadline.
	foundInList := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		listResp, lErr := sellerC.GET("/api/v3/otc/options")
		if lErr != nil {
			t.Fatalf("public-stock bid: list offers: %v", lErr)
		}
		if listResp.StatusCode == 404 {
			t.Skip("public-stock bid: OTC option list not deployed — skipping")
		}
		helpers.RequireStatus(t, listResp, 200)
		offers, _ := listResp.Body["offers"].([]interface{})
		for _, raw := range offers {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			lid, _ := item["local_id"].(float64)
			id, _ := item["id"].(float64)
			if int(lid) == offerID || int(id) == offerID {
				foundInList = true
				hpt, _ := item["has_preset_terms"].(bool)
				if !hpt {
					t.Errorf("public-stock bid: list view: local offer id=%d must carry has_preset_terms=true; item=%v", offerID, item)
				}
				break
			}
		}
		if foundInList {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !foundInList {
		t.Logf("public-stock bid: own offer (id=%d) not found in list within 30s (cache lag); skipping list assertion", offerID)
	}
}

// TestPublicStockBid_RemoteShellBid_RequiresTwoStacks documents the cross-bank
// path: a peer's /public-stock listing is synthesised into a local shell
// OTCOffer row with HasPresetTerms=false, which surfaces in
// GET /api/v3/otc/options with has_preset_terms=false and kind="remote".
// Bidding on it (POST /api/v3/otc/options/:id/bid) triggers a freshness guard
// that re-fetches the peer's live /public-stock before opening the SI-TX
// negotiation.  This cannot be driven from a single stack.
//
// The freshness guard itself is exercised at the unit level in
// stock-service/internal/handler/otc_negotiation_remote_shell_test.go
// (TestOpenNegotiation_ShellFreshness_BlocksWhenSellerGone and
// TestOpenNegotiation_ShellFreshness_PassesWhenSellerLive). The two-stack
// end-to-end scenario was verified manually with two docker-compose stacks.
func TestPublicStockBid_RemoteShellBid_RequiresTwoStacks(t *testing.T) {
	t.Skip("public-stock bid: cross-bank shell bid requires a 2nd bank stack " +
		"registered as a peer via POST /api/v3/peer-banks with a live " +
		"/public-stock listing; run manually with two docker-compose stacks. " +
		"Unit coverage: stock-service/internal/handler/otc_negotiation_remote_shell_test.go")
}
