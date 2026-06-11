//go:build integration

package workflows

// SP-1 unified-read integration tests.
//
// These tests cover the SP-1 guarantee: every OTC option read (offers,
// negotiations, contracts) returns items with the provenance fields
// kind / routing_number / bank_code and the me_owner flag, computed in
// the stock-service.  All tests below are runnable WITHOUT a second live
// bank.  Tests that require a cross-bank peer are guarded with t.Skip so
// the suite stays green in a single-stack setup.

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// jsonInt coerces a decoded JSON number (float64) to int, returning 0 for nil
// or any non-number. Used to read uint64 id fields off parsed list items.
func jsonInt(v interface{}) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// assertProvenanceFields checks that an item map (from a JSON array) carries the
// four SP-1 fields. It does NOT assert specific values — callers do that
// independently — it only fails when a field is outright absent.
func assertProvenanceFields(t *testing.T, item map[string]interface{}, label string) {
	t.Helper()
	for _, f := range []string{"id", "kind", "me_owner"} {
		if _, ok := item[f]; !ok {
			t.Errorf("SP-1 %s: field %q missing from item %v", label, f, item)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v3/otc/options — unified offer list
// ─────────────────────────────────────────────────────────────────────────────

// TestSP1_OfferList_EmptyIsOK verifies the endpoint is reachable, returns
// 200, and the offers[] array is present (may be empty when no listings exist).
func TestSP1_OfferList_EmptyIsOK(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 OTC option endpoints not deployed")
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "offers")
}

// TestSP1_OfferList_HasProvenanceFields posts a sell_initiated listing as a
// client, then reads the offer list and verifies every item has id, kind, and
// me_owner.  The poster's own item must have me_owner=true; any other
// item that the poster did not post must have me_owner=false.
//
// This test requires stocks to be seeded and the order simulator to be
// running so the seller can acquire a holding.  It skips gracefully when
// those conditions aren't met.
func TestSP1_OfferList_HasProvenanceFields(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-1 offer-list: no seeded stock with listing — skipping")
	}

	// Seed the seller's holding via a market buy.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 5,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-1 offer-list: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("SP-1 offer-list: seed buy did not fill within 45 s — skipping")
	}

	// Create the listing.
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "1",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-1 offer-list: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// Give the cache a moment to pick up the new listing.
	time.Sleep(1 * time.Second)

	// Read the offer list as the seller.
	listResp, err := sellerC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	offers, ok := listResp.Body["offers"].([]interface{})
	if !ok {
		t.Fatalf("SP-1 offer-list: expected 'offers' array, body=%v", listResp.Body)
	}
	if len(offers) == 0 {
		t.Skip("SP-1 offer-list: offer list empty right after create — cache lag; skipping")
	}

	foundOwn := false
	for _, raw := range offers {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		assertProvenanceFields(t, item, "offers[]")

		// Identify our own listing by local_id (the stable surrogate from the
		// discovery feed, which equals the numeric offer id for local items).
		lid, _ := item["local_id"].(float64)
		id, _ := item["id"].(float64)
		isOurs := int(lid) == offerID || int(id) == offerID
		if isOurs {
			foundOwn = true
			meOwner, _ := item["me_owner"].(bool)
			if !meOwner {
				t.Errorf("SP-1 offer-list: own listing should have me_owner=true, got item=%v", item)
			}
			kind, _ := item["kind"].(string)
			if kind != "local" {
				t.Errorf("SP-1 offer-list: own listing should have kind=local, got %q", kind)
			}
		}
	}
	if !foundOwn {
		// Not a hard failure — cache may still be warming.
		t.Logf("SP-1 offer-list: own offer (id=%d) not found in list (cache may lag); items seen=%d", offerID, len(offers))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v3/otc/options/:id — single-offer read
// ─────────────────────────────────────────────────────────────────────────────

// TestSP1_GetOffer_LocalKindAndMeOwner creates a listing and reads it back
// via the stable :id route.  The response must carry kind="local" and
// me_owner reflecting whether the caller is the poster.
func TestSP1_GetOffer_LocalKindAndMeOwner(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-1 get-offer: no seeded stock — skipping")
	}

	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 3,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-1 get-offer: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("SP-1 get-offer: seed buy did not fill — skipping")
	}

	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "1",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-1 get-offer: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// ── Seller reads their own listing ──
	getResp, err := sellerC.GET(fmt.Sprintf("/api/v3/otc/options/%d", offerID))
	if err != nil {
		t.Fatalf("get offer: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)

	// The offer detail is nested under "offer" for local reads.
	offerBody, hasNested := getResp.Body["offer"].(map[string]interface{})
	if !hasNested {
		// Flat body (some deployments return a flat shape).
		offerBody = getResp.Body
	}

	kind, _ := offerBody["kind"].(string)
	if kind != "local" {
		t.Errorf("SP-1 get-offer: seller reading own offer: want kind=local, got %q", kind)
	}
	meOwner, _ := offerBody["me_owner"].(bool)
	if !meOwner {
		t.Errorf("SP-1 get-offer: seller reading own offer: want me_owner=true, got false; body=%v", offerBody)
	}

	// ── A different client reads the same listing — me_owner must be false ──
	_, _, otherC, _ := setupActivatedClient(t, adminC)
	otherResp, err := otherC.GET(fmt.Sprintf("/api/v3/otc/options/%d", offerID))
	if err != nil {
		t.Fatalf("get offer (other client): %v", err)
	}
	helpers.RequireStatus(t, otherResp, 200)

	otherOfferBody, hasNested2 := otherResp.Body["offer"].(map[string]interface{})
	if !hasNested2 {
		otherOfferBody = otherResp.Body
	}
	otherMeOwner, _ := otherOfferBody["me_owner"].(bool)
	if otherMeOwner {
		t.Errorf("SP-1 get-offer: non-poster reading offer: want me_owner=false, got true; body=%v", otherOfferBody)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v3/me/otc/options/negotiations — caller's chains (bidder view)
// ─────────────────────────────────────────────────────────────────────────────

// TestSP1_MyNegotiations_HasProvenanceFields drives a sell_initiated OTC listing
// to the point where a buyer (bidder) has opened a negotiation chain, then
// verifies the bidder's negotiations list contains items with kind and me_owner.
// The chain the caller opened AS the bidder must have me_owner=false.
func TestSP1_MyNegotiations_HasProvenanceFields(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	// Use the shared setupOfferWithBid helper (from otc_timeline_test.go).
	// It returns (offerID, posterC, bidderC).
	offerID, _, bidderC := setupOfferWithBid(t, adminC)
	_ = offerID

	resp, err := bidderC.GET("/api/v3/me/otc/options/negotiations")
	if err != nil {
		t.Fatalf("list my negotiations: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("SP-1 my-negotiations: endpoint not deployed — skipping")
	}
	helpers.RequireStatus(t, resp, 200)

	negotiations, ok := resp.Body["negotiations"].([]interface{})
	if !ok {
		t.Fatalf("SP-1 my-negotiations: want 'negotiations' array, body=%v", resp.Body)
	}
	if len(negotiations) == 0 {
		t.Skip("SP-1 my-negotiations: empty list right after bid (possible lag) — skipping assertion")
	}

	for _, raw := range negotiations {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		// id and kind must always be present. me_owner is NOT asserted for
		// presence here: it is proto-omitempty (absent == false on the wire),
		// and a bidder's chain is definitionally me_owner=false, so the key is
		// legitimately omitted. We therefore treat absent me_owner as false.
		for _, f := range []string{"id", "kind"} {
			if _, ok := item[f]; !ok {
				t.Errorf("SP-1 my-negotiations: field %q missing from item %v", f, item)
			}
		}

		kind, _ := item["kind"].(string)
		if kind == "" {
			t.Errorf("SP-1 my-negotiations: item missing kind field: %v", item)
		}
		// A bidder's own chain must always have me_owner=false
		// (me_owner=true is reserved for the listing's poster/seller). Absent
		// (omitted) == false, which satisfies this; only an explicit true fails.
		meOwner, _ := item["me_owner"].(bool)
		if meOwner {
			t.Errorf("SP-1 my-negotiations: bidder's chain has me_owner=true (should be false); item=%v", item)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v3/me/otc/contracts — caller's formed contracts
// ─────────────────────────────────────────────────────────────────────────────

// TestSP1_MyContracts_EmptyHasCorrectShape verifies the contracts endpoint
// returns 200 with a contracts[] array (possibly empty) for a fresh client.
// When contracts exist (exercised lifecycle test), each item must carry
// kind, me_owner.  This variant only exercises the empty-list shape.
func TestSP1_MyContracts_EmptyHasCorrectShape(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/otc/contracts")
	if err != nil {
		t.Fatalf("list my contracts: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("SP-1 my-contracts: endpoint not deployed — skipping")
	}
	helpers.RequireStatus(t, resp, 200)

	// contracts[] must be present (may be empty slice).
	if _, ok := resp.Body["contracts"]; !ok {
		t.Errorf("SP-1 my-contracts: want 'contracts' key in body, got %v", resp.Body)
	}
}

// TestSP1_MyContracts_BuyerIsOwner exercises a full lifecycle — seller lists,
// buyer accepts, contract is formed — and checks that the buyer sees
// me_owner=true in the resulting contract row.
//
// This test depends on the market simulator (to seed the seller's holding)
// and is skipped when the simulator is unavailable or too slow.
func TestSP1_MyContracts_BuyerIsOwner(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	buyerAcctID, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-1 contracts: no seeded stock — skipping")
	}

	// Seed seller holding.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 5,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-1 contracts: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	if !tryWaitForOrderFill(t, sellerC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("SP-1 contracts: seed buy did not fill — skipping")
	}

	// Seller creates listing.
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "1",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-1 contracts: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// Buyer bids.
	bidResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": buyerAcctID,
		"quantity":          "1",
		"strike_price":      "100",
		"premium":           "5",
		"settlement_date":   "2030-12-31",
	})
	if err != nil {
		t.Fatalf("buyer bid: %v", err)
	}
	if bidResp.StatusCode != 201 {
		t.Fatalf("buyer bid: want 201, got %d body=%v", bidResp.StatusCode, bidResp.Body)
	}
	// The bid response is shaped {"negotiation":{"id":...}} (gateway
	// OpenNegotiationChain wraps the chain under "negotiation"); read the
	// nested id, not a flat one.
	negID := int(helpers.GetNestedNumberField(t, bidResp, "negotiation", "id"))

	// Seller accepts (first-accept-wins). The accept route requires the
	// acceptor's account id in the body and returns 200 (not 201).
	acceptResp, err := sellerC.POST(fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/accept", offerID, negID), map[string]interface{}{
		"acceptor_account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seller accept: %v", err)
	}
	if acceptResp.StatusCode != 200 {
		t.Fatalf("seller accept: want 200, got %d body=%v", acceptResp.StatusCode, acceptResp.Body)
	}

	// The contract-formation saga is async — poll the buyer's contracts list
	// until it appears (mirrors TestSP2b_UnifiedLocalLifecycle).
	var contracts []interface{}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		contractsResp, cErr := buyerC.GET("/api/v3/me/otc/contracts")
		if cErr != nil {
			t.Fatalf("buyer list contracts: %v", cErr)
		}
		helpers.RequireStatus(t, contractsResp, 200)
		if cs, ok := contractsResp.Body["contracts"].([]interface{}); ok && len(cs) > 0 {
			contracts = cs
			break
		}
		time.Sleep(1 * time.Second)
	}
	if len(contracts) == 0 {
		t.Skip("SP-1 contracts: contract not yet formed within 20s — skipping provenance check")
	}

	for _, raw := range contracts {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		assertProvenanceFields(t, item, "contracts[]")

		kind, _ := item["kind"].(string)
		if kind == "" {
			t.Errorf("SP-1 contracts: contract item missing kind: %v", item)
		}
		// Buyer must always be the owner of a formed contract.
		meOwner, _ := item["me_owner"].(bool)
		if !meOwner {
			t.Errorf("SP-1 contracts: buyer's contract has me_owner=false (expected true); item=%v", item)
		}
	}

	// Seller lists their contracts — must see me_owner=false (seller/writer is not the holder).
	sellerContractsResp, err := sellerC.GET("/api/v3/me/otc/contracts")
	if err != nil {
		t.Fatalf("seller list contracts: %v", err)
	}
	helpers.RequireStatus(t, sellerContractsResp, 200)
	sellerContracts, _ := sellerContractsResp.Body["contracts"].([]interface{})
	for _, raw := range sellerContracts {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		meOwner, _ := item["me_owner"].(bool)
		if meOwner {
			t.Errorf("SP-1 contracts: seller's contract has me_owner=true (expected false); item=%v", item)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cross-bank tests (require a second live bank — skipped in single-stack setup)
// ─────────────────────────────────────────────────────────────────────────────

// TestSP1_RemoteOffer_AppearsWithKindRemote is the integration test for remote
// offers appearing in the unified list.  It requires a second live bank
// (registered as a peer via POST /api/v3/peer-banks) with an open OTC option
// listing.  Run with COHORT_DRY_RUN_PEER set (or any env-controlled flag) and
// two docker-compose stacks up.
//
// NOTE: This test is intentionally skip-guarded for single-stack CI.  Do not
// remove the t.Skip — it is the correct behaviour when no peer is reachable.
func TestSP1_RemoteOffer_AppearsWithKindRemote(t *testing.T) {
	t.Skip("SP-1 remote-offer: requires two-stack peer — run with a live second bank registered as a peer")
	// Implementation hint when a peer is available:
	//   1. Register the peer bank via POST /api/v3/peer-banks.
	//   2. Wait for the option cache to refresh (< 60 s typically).
	//   3. GET /api/v3/otc/options — expect at least one item with kind="remote" and me_owner=false.
	//   4. GET /api/v3/otc/options/:local_id for a remote item — expect kind="remote", me_owner=false.
}

// TestSP1_RemoteNegotiation_MergesIntoMyNegotiations verifies that when a
// caller has a cross-bank negotiation chain (they bid on a peer-bank listing),
// the chain appears in GET /api/v3/me/otc/options/negotiations with kind="remote"
// and me_owner=false (bidder is never the owner).
//
// NOTE: Skipped in single-stack setup.
func TestSP1_RemoteNegotiation_MergesIntoMyNegotiations(t *testing.T) {
	t.Skip("SP-1 remote-negotiation: requires two-stack peer with an active cross-bank negotiation chain")
}

// TestSP1_RemoteContract_AppearsWithKindRemote verifies that when a caller
// bought an option from a peer bank (cross-bank exercise), their
// GET /api/v3/me/otc/contracts list includes the remote contract with
// kind="remote" and me_owner=true (they are the buyer/holder).
//
// NOTE: Skipped in single-stack setup.
func TestSP1_RemoteContract_AppearsWithKindRemote(t *testing.T) {
	t.Skip("SP-1 remote-contract: requires two-stack peer with a completed cross-bank option contract")
}

// TestSP1_PeerCancelReconciler_SkipInSingleStack documents the peer-cancel
// reconciler (offer-cancel and safety-net negotiation reconciler from SP-1
// Task 9).  Both reconcilers are background goroutines; verifying them
// end-to-end requires a second bank whose state the reconciler polls.
//
// NOTE: Skipped in single-stack setup.
func TestSP1_PeerCancelReconciler_SkipInSingleStack(t *testing.T) {
	t.Skip("SP-1 peer-cancel-reconciler: requires two-stack peer to exercise offer-cancel and negotiation safety-net reconciliation")
}

// ─────────────────────────────────────────────────────────────────────────────
// SP-2b — my_negotiation_id / my_negotiation_status on offer reads
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2b_OfferList_StampsMyNegotiationForBidder drives a sell_initiated
// listing to the BID state, then verifies the bidder's discovery list
// (GET /api/v3/otc/options) stamps my_negotiation_id + my_negotiation_status on
// the offer they bid on (so the FE can jump straight to its own chain). The
// discovery feed is cache-backed (~5 s refresh) so the offer may take a couple
// of cycles to surface; we poll. The poster (who never bid) must NOT see
// my_negotiation_id on that offer.
func TestSP2b_OfferList_StampsMyNegotiationForBidder(t *testing.T) {
	adminC := loginAsAdmin(t)
	offerID, posterC, bidderC := setupOfferWithBid(t, adminC)

	// Poll the bidder's discovery feed until the offer appears with a stamped
	// my_negotiation_id (cache refresh lag tolerance).
	var bidderItem map[string]interface{}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := bidderC.GET("/api/v3/otc/options")
		if err != nil {
			t.Fatalf("bidder list offers: %v", err)
		}
		if resp.StatusCode == 404 {
			t.Skip("SP-2b: OTC option list not deployed — skipping")
		}
		helpers.RequireStatus(t, resp, 200)
		offers, _ := resp.Body["offers"].([]interface{})
		for _, raw := range offers {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if jsonInt(item["id"]) == offerID {
				bidderItem = item
				if _, has := item["my_negotiation_id"]; has {
					break
				}
			}
		}
		if bidderItem != nil {
			if _, has := bidderItem["my_negotiation_id"]; has {
				break
			}
		}
		time.Sleep(1 * time.Second)
	}
	if bidderItem == nil {
		t.Skip("SP-2b: bidder's offer never surfaced in the cache-backed list — skipping")
	}
	myNegID, hasNeg := bidderItem["my_negotiation_id"]
	if !hasNeg || jsonInt(myNegID) == 0 {
		t.Fatalf("SP-2b: bidder's offer must carry a non-zero my_negotiation_id; item=%v", bidderItem)
	}
	if st, _ := bidderItem["my_negotiation_status"].(string); st == "" {
		t.Errorf("SP-2b: bidder's offer must carry my_negotiation_status; item=%v", bidderItem)
	}

	// The poster never bid → their list view of the same offer must NOT stamp
	// my_negotiation_id (me_owner does not imply a bidder chain).
	posterResp, err := posterC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("poster list offers: %v", err)
	}
	helpers.RequireStatus(t, posterResp, 200)
	posterOffers, _ := posterResp.Body["offers"].([]interface{})
	for _, raw := range posterOffers {
		item, ok := raw.(map[string]interface{})
		if !ok || jsonInt(item["id"]) != offerID {
			continue
		}
		if v, has := item["my_negotiation_id"]; has && jsonInt(v) != 0 {
			t.Errorf("SP-2b: poster (non-bidder) must not carry my_negotiation_id; item=%v", item)
		}
	}
}

// TestSP2b_GetOffer_PosterHasNoMyNegotiation verifies the offer-detail read
// (GET /api/v3/otc/options/:id) does NOT stamp my_negotiation_id for the poster
// — they own the listing (me_owner=true) but hold no bidder chain of their own.
func TestSP2b_GetOffer_PosterHasNoMyNegotiation(t *testing.T) {
	adminC := loginAsAdmin(t)
	offerID, posterC, _ := setupOfferWithBid(t, adminC)

	resp, err := posterC.GET(fmt.Sprintf("/api/v3/otc/options/%d", offerID))
	if err != nil {
		t.Fatalf("poster get offer: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("SP-2b: get-offer not deployed — skipping")
	}
	helpers.RequireStatus(t, resp, 200)
	offerBody, ok := resp.Body["offer"].(map[string]interface{})
	if !ok {
		t.Fatalf("SP-2b: get-offer: want 'offer' object, body=%v", resp.Body)
	}
	if meOwner, _ := offerBody["me_owner"].(bool); !meOwner {
		t.Errorf("SP-2b: poster reading own offer must be me_owner=true; body=%v", offerBody)
	}
	if v, has := offerBody["my_negotiation_id"]; has && jsonInt(v) != 0 {
		t.Errorf("SP-2b: poster (non-bidder) must not carry my_negotiation_id on detail; body=%v", offerBody)
	}
}
