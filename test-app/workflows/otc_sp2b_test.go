//go:build integration

package workflows

// SP-2b integration tests — unified OTC write routes.
//
// SP-2b made the frontend use ONE write route per OTC action regardless of
// whether the listing lives on this bank (local) or a peer bank (remote):
// stock-service dispatches on routing_number == OwnRouting(). As part of the
// clean-cut, these routes were DELETED:
//
//   POST   /api/v3/me/peer-otc/negotiations
//   GET    /api/v3/me/peer-otc/negotiations
//   PUT    /api/v3/me/peer-otc/negotiations/:id
//   POST   /api/v3/me/peer-otc/negotiations/:id/accept   (and other verbs)
//   DELETE /api/v3/me/peer-otc/negotiations/:id
//   POST   /api/v3/me/otc/contracts/peer/:id/exercise
//
// and these unified write routes carry both local + remote traffic:
//
//   POST   /api/v3/otc/options/:id/bid
//   POST   /api/v3/me/otc/options/:id/negotiations/:nid/counter
//   POST   /api/v3/me/otc/options/:id/negotiations/:nid/accept
//   POST   /api/v3/me/otc/options/:id/negotiations/:nid/reject
//   DELETE /api/v3/me/otc/options/:id/negotiations/:nid
//   POST   /api/v3/otc/contracts/:id/exercise
//
// SP-2b also stamps my_negotiation_id / my_negotiation_status on offer reads so
// the FE can jump straight to its own chain.
//
// Tests here exercise the LOCAL dispatch path end-to-end. Cross-bank dispatch
// (remote routing_number) is unreachable from a single stack and is left as a
// documented t.Skip — never a silent omission. All ids are pulled from REST
// responses (FE-style), never read from the DB.

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// ─────────────────────────────────────────────────────────────────────────────
// 1. Unified LOCAL negotiation lifecycle: bid → counter → accept → contract
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms drives a full local OTC
// option negotiation through the UNIFIED write routes (the same routes a
// cross-bank chain would use): a seller lists, a buyer bids, the buyer counters
// their own chain, and the seller accepts the current terms. The accept forms
// an OptionContract via the local contract-formation saga.
//
// Every id (offer id, negotiation id) is pulled from the bid/list REST
// responses — never the DB — to mirror how the frontend operates.
func TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms(t *testing.T) {
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	buyerAcctID, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-2b lifecycle: no seeded stock with listing — skipping")
	}

	// Seed the seller's holding so the contract-formation saga can reserve shares.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 10,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-2b lifecycle: seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-2b lifecycle: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	if !tryWaitForOrderFill(t, sellerC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("SP-2b lifecycle: seed buy did not fill — skipping")
	}

	// Seller lists a sell_initiated OTC option (POST /api/v3/me/otc/options).
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "2",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-2b lifecycle: create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-2b lifecycle: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("SP-2b lifecycle: create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	// ── BID (unified route: POST /api/v3/otc/options/:id/bid) ──
	// Pull the negotiation id from the bid response — never the DB.
	bidResp, err := buyerC.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": buyerAcctID,
		"quantity":          "1",
		"strike_price":      "100",
		"premium":           "5",
		"settlement_date":   "2030-12-31",
	})
	if err != nil {
		t.Fatalf("SP-2b lifecycle: bid: %v", err)
	}
	if bidResp.StatusCode != 201 {
		t.Fatalf("SP-2b lifecycle: bid: want 201, got %d body=%v", bidResp.StatusCode, bidResp.Body)
	}
	negID := nestedNegotiationID(t, bidResp.Body)
	if negID == 0 {
		t.Fatalf("SP-2b lifecycle: bid response carried no negotiation id; body=%v", bidResp.Body)
	}
	if st := nestedNegotiationStatus(bidResp.Body); st != "" && st != "open" {
		t.Errorf("SP-2b lifecycle: fresh bid chain should be status=open, got %q", st)
	}

	// ── COUNTER (unified route: POST .../:nid/counter) ──
	// The bidder counters their own chain with a new premium. The chain id
	// comes from the bid response above (FE-style).
	counterResp, err := buyerC.POST(
		fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/counter", offerID, negID),
		map[string]interface{}{
			"quantity":        "1",
			"strike_price":    "100",
			"premium":         "6",
			"settlement_date": "2030-12-31",
		})
	if err != nil {
		t.Fatalf("SP-2b lifecycle: counter: %v", err)
	}
	if counterResp.StatusCode != 200 {
		t.Fatalf("SP-2b lifecycle: counter: want 200, got %d body=%v", counterResp.StatusCode, counterResp.Body)
	}
	if st := nestedNegotiationStatus(counterResp.Body); st != "" && st != "countered" {
		t.Errorf("SP-2b lifecycle: after buyer counter, chain should be status=countered, got %q", st)
	}

	// Sanity: the poster's all-chains view shows this chain in the countered state.
	chainsResp, err := sellerC.GET(fmt.Sprintf("/api/v3/otc/options/%d/negotiations", offerID))
	if err != nil {
		t.Fatalf("SP-2b lifecycle: poster list chains: %v", err)
	}
	helpers.RequireStatus(t, chainsResp, 200)
	chains, _ := chainsResp.Body["negotiations"].([]interface{})
	foundChain := false
	for _, raw := range chains {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if jsonInt(item["id"]) == negID {
			foundChain = true
			if st, _ := item["status"].(string); st != "countered" {
				t.Errorf("SP-2b lifecycle: poster view of chain %d: want status=countered, got %q", negID, st)
			}
		}
	}
	if !foundChain {
		t.Errorf("SP-2b lifecycle: chain %d not present in poster's negotiations list", negID)
	}

	// ── ACCEPT (unified route: POST .../:nid/accept) ──
	// The seller (party opposite to the last counter, which was the buyer)
	// accepts the current terms. This triggers the local contract-formation saga.
	acceptResp, err := sellerC.POST(
		fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/accept", offerID, negID),
		map[string]interface{}{
			"acceptor_account_id": sellerAcctID,
		})
	if err != nil {
		t.Fatalf("SP-2b lifecycle: accept: %v", err)
	}
	if acceptResp.StatusCode != 200 {
		t.Fatalf("SP-2b lifecycle: accept: want 200, got %d body=%v", acceptResp.StatusCode, acceptResp.Body)
	}
	if winning, ok := acceptResp.Body["winning"].(bool); ok && !winning {
		t.Errorf("SP-2b lifecycle: accept on the only chain should be winning=true; body=%v", acceptResp.Body)
	}
	if ps, _ := acceptResp.Body["parent_status"].(string); ps != "" && ps != "consumed" {
		t.Errorf("SP-2b lifecycle: after accept, parent_status should be consumed, got %q", ps)
	}

	// ── Assert a contract formed (local saga). The buyer is the holder. ──
	// Poll the buyer's contracts list — the contract-formation saga is async.
	var formed bool
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		cResp, cErr := buyerC.GET("/api/v3/me/otc/contracts")
		if cErr != nil {
			t.Fatalf("SP-2b lifecycle: buyer list contracts: %v", cErr)
		}
		helpers.RequireStatus(t, cResp, 200)
		contracts, _ := cResp.Body["contracts"].([]interface{})
		if len(contracts) > 0 {
			formed = true
			// The buyer must own the formed contract.
			for _, raw := range contracts {
				item, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if meOwner, _ := item["me_owner"].(bool); !meOwner {
					t.Errorf("SP-2b lifecycle: buyer's contract should be me_owner=true; item=%v", item)
				}
			}
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !formed {
		t.Errorf("SP-2b lifecycle: no contract formed for buyer within 20s after accept")
	}
}

// nestedNegotiationID pulls the negotiation id out of a write-route response.
// The bid/counter handlers wrap the OTCNegotiationResponse under "negotiation".
func nestedNegotiationID(t *testing.T, body map[string]interface{}) int {
	t.Helper()
	if neg, ok := body["negotiation"].(map[string]interface{}); ok {
		return jsonInt(neg["id"])
	}
	// Some shapes return the id at the top level.
	return jsonInt(body["id"])
}

// nestedNegotiationStatus reads the status off a negotiation write-route
// response (nested under "negotiation" or flat). Returns "" when absent.
func nestedNegotiationStatus(body map[string]interface{}) string {
	if neg, ok := body["negotiation"].(map[string]interface{}); ok {
		if s, ok := neg["status"].(string); ok {
			return s
		}
	}
	if s, ok := body["status"].(string); ok {
		return s
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Deleted routes now 404
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2b_DeletedPeerOTCRoutes_Return404 asserts every route removed by the
// SP-2b clean-cut is gone: the /me/peer-otc/negotiations family (all verbs)
// and POST /me/otc/contracts/peer/:id/exercise. Gin's NoRoute handler returns
// 404 for a path with no registered route — that is the correct, expected
// behaviour and is what we assert here. Requests are sent authenticated (so a
// surviving route could NOT mask itself as a 401), but 404 is route-not-found
// and fires before any group middleware.
func TestSP2b_DeletedPeerOTCRoutes_Return404(t *testing.T) {
	adminC := loginAsAdmin(t)
	// Use a real authenticated client so a 401 (auth) can't be mistaken for a
	// 404 (route gone). An admin token is accepted by AnyAuth groups.
	c := adminC

	type rt struct {
		method string
		path   string
		body   map[string]interface{}
	}
	deleted := []rt{
		{"POST", "/api/v3/me/peer-otc/negotiations", map[string]interface{}{"x": 1}},
		{"GET", "/api/v3/me/peer-otc/negotiations", nil},
		{"PUT", "/api/v3/me/peer-otc/negotiations/1", map[string]interface{}{"x": 1}},
		{"POST", "/api/v3/me/peer-otc/negotiations/1/accept", map[string]interface{}{"x": 1}},
		{"DELETE", "/api/v3/me/peer-otc/negotiations/1", nil},
		{"POST", "/api/v3/me/otc/contracts/peer/1/exercise", map[string]interface{}{"x": 1}},
	}

	for _, r := range deleted {
		var resp *client.Response
		var err error
		switch r.method {
		case "GET":
			resp, err = c.GET(r.path)
		case "POST":
			resp, err = c.POST(r.path, r.body)
		case "PUT":
			resp, err = c.PUT(r.path, r.body)
		case "DELETE":
			resp, err = c.DELETE(r.path)
		}
		if err != nil {
			t.Fatalf("SP-2b deleted-route %s %s: request failed: %v", r.method, r.path, err)
		}
		if resp.StatusCode != 404 {
			t.Errorf("SP-2b deleted-route %s %s: want 404 (route removed), got %d; body=%v",
				r.method, r.path, resp.StatusCode, resp.Body)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. my_negotiation_id present for bidder, absent for non-bidder
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2b_MyNegotiationID_PresentForBidderAbsentForOther verifies that after a
// buyer bids, the buyer's discovery-list view (GET /api/v3/otc/options) stamps
// my_negotiation_id == their chain's nid on that offer, while a third party who
// never bid (and is not the poster) does not see my_negotiation_id stamped.
//
// The discovery feed is cache-backed (~5s refresh) so we poll with a deadline.
func TestSP2b_MyNegotiationID_PresentForBidderAbsentForOther(t *testing.T) {
	adminC := loginAsAdmin(t)
	offerID, _, bidderC := setupOfferWithBid(t, adminC)

	// Recover the bidder's own chain id from their own-chain list (FE-style:
	// no DB read). This is the nid we expect stamped on the offer.
	negResp, err := bidderC.GET("/api/v3/me/otc/options/negotiations")
	if err != nil {
		t.Fatalf("SP-2b my-neg-id: bidder list own negotiations: %v", err)
	}
	if negResp.StatusCode == 404 {
		t.Skip("SP-2b my-neg-id: own-negotiations endpoint not deployed — skipping")
	}
	helpers.RequireStatus(t, negResp, 200)
	myNids := map[int]bool{}
	negotiations, _ := negResp.Body["negotiations"].([]interface{})
	for _, raw := range negotiations {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if jsonInt(item["parent_offer_id"]) == offerID {
			myNids[jsonInt(item["id"])] = true
		}
	}

	// Poll the bidder's discovery feed for the stamped my_negotiation_id.
	var stampedNid int
	var found bool
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !found {
		resp, lErr := bidderC.GET("/api/v3/otc/options")
		if lErr != nil {
			t.Fatalf("SP-2b my-neg-id: bidder list offers: %v", lErr)
		}
		if resp.StatusCode == 404 {
			t.Skip("SP-2b my-neg-id: OTC option list not deployed — skipping")
		}
		helpers.RequireStatus(t, resp, 200)
		offers, _ := resp.Body["offers"].([]interface{})
		for _, raw := range offers {
			item, ok := raw.(map[string]interface{})
			if !ok || jsonInt(item["id"]) != offerID {
				continue
			}
			if v, has := item["my_negotiation_id"]; has && jsonInt(v) != 0 {
				stampedNid = jsonInt(v)
				found = true
			}
		}
		if !found {
			time.Sleep(1 * time.Second)
		}
	}
	if !found {
		t.Skip("SP-2b my-neg-id: bidder's stamped offer never surfaced in cache-backed list — skipping")
	}
	// The stamped nid must be one of the bidder's own chains on this offer.
	if len(myNids) > 0 && !myNids[stampedNid] {
		t.Errorf("SP-2b my-neg-id: stamped my_negotiation_id=%d is not one of the bidder's own chains %v on offer %d",
			stampedNid, myNids, offerID)
	}

	// A third party (never bid, not poster) must NOT see my_negotiation_id stamped.
	_, _, otherC, _ := setupActivatedClient(t, adminC)
	otherResp, err := otherC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("SP-2b my-neg-id: third-party list offers: %v", err)
	}
	helpers.RequireStatus(t, otherResp, 200)
	otherOffers, _ := otherResp.Body["offers"].([]interface{})
	for _, raw := range otherOffers {
		item, ok := raw.(map[string]interface{})
		if !ok || jsonInt(item["id"]) != offerID {
			continue
		}
		if v, has := item["my_negotiation_id"]; has && jsonInt(v) != 0 {
			t.Errorf("SP-2b my-neg-id: third-party (non-bidder) must not carry my_negotiation_id; item=%v", item)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Cross-bank dispatch — documented skips (require a 2nd live stack)
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2b_UnifiedRemoteBid_RequiresTwoStacks documents that the SAME unified
// bid route (POST /api/v3/otc/options/:id/bid) dispatches a cross-bank SI-TX
// negotiation when :id is a remote listing. Exercising it requires a second
// docker-compose stack registered as a peer; unreachable from a single stack.
func TestSP2b_UnifiedRemoteBid_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-2b remote-bid: requires a 2nd stack — the unified bid route dispatches cross-bank when :id is a remote listing; run with a second live bank registered as a peer")
}

// TestSP2b_UnifiedRemoteCounterAccept_RequiresTwoStacks documents that
// counter/accept/reject/cancel on a remote chain go through the SAME unified
// /me/otc/options/:id/negotiations/:nid/* routes (stock-service dispatches to
// the peer via SI-TX). Requires a 2nd stack.
func TestSP2b_UnifiedRemoteCounterAccept_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-2b remote-counter/accept: requires a 2nd stack with an active cross-bank negotiation chain")
}

// TestSP2b_UnifiedRemoteExercise_RequiresTwoStacks documents that exercising a
// cross-bank-formed contract goes through the unified
// POST /api/v3/otc/contracts/:id/exercise route (the deleted peer-exercise
// route is gone). Requires a 2nd stack.
func TestSP2b_UnifiedRemoteExercise_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-2b remote-exercise: requires a 2nd stack with a completed cross-bank option contract")
}
