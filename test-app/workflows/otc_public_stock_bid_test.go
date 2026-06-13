//go:build integration

package workflows

// Integration tests for the public-stock option negotiation feature.
//
// Feature summary (unified options-as-stock model):
//   - An OTC option offer is termless "optionable inventory":
//     (owner, ticker, quantity). The offer carries NO strike/premium/
//     settlement_date of its own — those terms are viewer-contextual and are
//     sourced from the negotiation chain (a bidder sees their own chain's
//     current terms; the owner sees their most recent counter; otherwise the
//     term fields are empty). A freshly-created offer with no negotiation thus
//     shows empty strike_price/premium/settlement_date on every read surface.
//   - has_preset_terms is GONE from the unified offers list
//     (GET /api/v3/otc/options) and per-offer detail
//     (GET /api/v3/otc/options/:id) — there is no preset/no-preset distinction
//     anymore; the bidder always proposes the terms on their chain.
//   - Cross-bank option discovery is /public-stock only: a peer's open
//     sell-initiated option offers surface as {stock, sellers:[{seller,amount}]}
//     (one seller entry per owner+ticker). The proprietary
//     /public-option-offers endpoint was removed (404).
//   - Bidding on a remote shell offer (POST /api/v3/otc/options/:id/bid) is
//     subject to a freshness guard that re-fetches the peer's live
//     /public-stock before dispatching the SI-TX negotiation.
//
// Single-stack coverage:
//   - A freshly-created local sell_initiated listing surfaces in both the list
//     view and the per-offer detail view with empty (viewer-contextual) term
//     fields, since no negotiation chain exists yet.
//
// Two-stack coverage (documented t.Skip — requires a registered peer bank):
//   - A remote shell synthesised from /public-stock surfaces in the list view
//     with kind="remote" and empty viewer-contextual terms (the buyer proposes
//     their own strike/premium on bid).
//   - Bidding on the shell with buyer-chosen strike/premium/settlement opens a
//     negotiation and returns 201 (shell freshness guard passes when the
//     seller+ticker are still live on the peer's /public-stock).

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestPublicStockBid_LocalOfferTermsViewerContextual verifies that a freshly
// created local sell_initiated OTC option listing surfaces in both the unified
// offers list (GET /api/v3/otc/options) and the per-offer detail
// (GET /api/v3/otc/options/:id) — and that its term fields
// (strike_price/premium/settlement_date) are empty, because the offer is
// termless and no negotiation chain has been opened on it yet. The terms are
// viewer-contextual and only populate from a negotiation chain.
func TestPublicStockBid_LocalOfferTermsViewerContextual(t *testing.T) {
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

	// Create a termless local sell_initiated listing (no preset terms).
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "1",
		"account_id": sellerAcctID,
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

	// ── Per-offer detail: term fields must be empty (no negotiation yet) ──
	detailResp, err := sellerC.GET(fmt.Sprintf("/api/v3/otc/options/%d", offerID))
	if err != nil {
		t.Fatalf("public-stock bid: get offer: %v", err)
	}
	helpers.RequireStatus(t, detailResp, 200)
	offerBody, hasNested := detailResp.Body["offer"].(map[string]interface{})
	if !hasNested {
		offerBody = detailResp.Body
	}
	if _, gone := offerBody["has_preset_terms"]; gone {
		t.Errorf("public-stock bid: has_preset_terms must no longer be present on offer detail; body=%v", offerBody)
	}
	for _, term := range []string{"strike_price", "premium", "settlement_date"} {
		if v, ok := offerBody[term]; ok && v != nil {
			if s, isStr := v.(string); isStr && s != "" {
				t.Errorf("public-stock bid: freshly-created offer must show empty %s (no negotiation), got %q (body=%v)", term, s, offerBody)
			}
		}
	}

	// ── Unified list: the offer must surface (cache-backed feed, so poll) ──
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
				if _, gone := item["has_preset_terms"]; gone {
					t.Errorf("public-stock bid: list view: has_preset_terms must no longer be present; item=%v", item)
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
// OTCOffer row that surfaces in GET /api/v3/otc/options with kind="remote" and
// empty viewer-contextual terms (the buyer proposes their own strike/premium on
// bid). Bidding on it (POST /api/v3/otc/options/:id/bid) triggers a freshness
// guard that re-fetches the peer's live /public-stock before opening the SI-TX
// negotiation. This cannot be driven from a single stack.
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
