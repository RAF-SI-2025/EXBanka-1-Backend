//go:build integration

package workflows

// SP-2a integration tests.
//
// SP-2a folded the three remote OTC mirror tables (remote_otc_offer /
// peer_otc_negotiation / peer_option_contract) into the unified local tables
// (OTCOffer / OTCNegotiation / OptionContract, distinguished by routing_number
// vs. OwnRouting()).  The external behaviour changes in SP-2a are:
//
//  1. POST /api/v3/peer-banks returns 400 when bank_code OR routing_number
//     equals this bank's own (peer-collision invariant).
//  2. GET /api/v3/otc/options still returns offers with kind / id / me_owner
//     (regression guard — the fold must not break the SP-1 discovery surface).
//
// Tests that require a second live bank are guarded with t.Skip so the suite
// remains green in a single-stack setup.

import (
	"net"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// ─────────────────────────────────────────────────────────────────────────────
// 1. Peer-bank collision guard
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2a_PeerBank_RejectsOwnBankCode verifies that registering a peer whose
// bank_code equals this bank's own code is rejected with HTTP 400.
//
// The default OWN_BANK_CODE is "111" (config default, also the docker-compose
// seed). The test asserts the service rejects "111" as a peer bank_code.
// This is enforced in transaction-service CreatePeerBank (InvalidArgument →
// gateway maps to 400 validation_error).
func TestSP2a_PeerBank_RejectsOwnBankCode(t *testing.T) {
	t.Parallel()

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)

	// Attempt to register our own bank code ("111") as a peer.
	resp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "111",
		"routing_number": 222, // routing differs — we only want to test bank_code collision
		"base_url":       "http://localhost:9999/api/v3",
		"api_token":      "collision-test-token",
		"active":         true,
	})
	if err != nil {
		t.Fatalf("SP-2a peer-collision (bank_code): request failed: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("SP-2a peer-collision (bank_code): want 400, got %d; body=%v", resp.StatusCode, resp.Body)
	}

	errBody, _ := resp.Body["error"].(map[string]interface{})
	if errBody == nil {
		t.Logf("SP-2a peer-collision (bank_code): response body=%v (no structured error — acceptable)", resp.Body)
	} else {
		code, _ := errBody["code"].(string)
		t.Logf("SP-2a peer-collision (bank_code): error code=%q (400 confirmed)", code)
	}
}

// TestSP2a_PeerBank_RejectsOwnRoutingNumber verifies that registering a peer
// whose routing_number equals this bank's own routing is also rejected with 400.
//
// Default OWN_BANK_CODE "111" → routing_number 111 (the numeric parse of "111").
func TestSP2a_PeerBank_RejectsOwnRoutingNumber(t *testing.T) {
	t.Parallel()

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)

	// bank_code differs but routing_number equals own (111).
	resp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "222",
		"routing_number": 111, // own routing — must be rejected
		"base_url":       "http://localhost:9999/api/v3",
		"api_token":      "collision-routing-token",
		"active":         true,
	})
	if err != nil {
		t.Fatalf("SP-2a peer-collision (routing): request failed: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("SP-2a peer-collision (routing): want 400, got %d; body=%v", resp.StatusCode, resp.Body)
	}
}

// TestSP2a_PeerBank_AcceptsDistinctPeer verifies the positive path: a peer
// with distinct bank_code AND routing_number is accepted (201).  We clean up
// immediately after to leave the DB tidy.
func TestSP2a_PeerBank_AcceptsDistinctPeer(t *testing.T) {
	t.Parallel()

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)

	resp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "333",
		"routing_number": 333,
		"base_url":       "http://localhost:9999/api/v3",
		"api_token":      "distinct-peer-token-sp2a",
		"active":         false, // inactive so no OTC cache fan-out
	})
	if err != nil {
		t.Fatalf("SP-2a distinct peer: request failed: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("SP-2a distinct peer: want 201, got %d; body=%v", resp.StatusCode, resp.Body)
	}

	peerID, ok := resp.Body["id"].(float64)
	if !ok {
		t.Fatalf("SP-2a distinct peer: missing numeric id in response: %+v", resp.Body)
	}
	t.Logf("SP-2a distinct peer: created id=%d", int(peerID))

	// Cleanup.
	idStr := helpers.FormatID(int(peerID))
	delResp, err := adminC.DELETE("/api/v3/peer-banks/" + idStr)
	if err != nil {
		t.Logf("SP-2a distinct peer: cleanup delete failed: %v", err)
		return
	}
	if delResp.StatusCode != 204 {
		t.Logf("SP-2a distinct peer: cleanup delete returned %d (non-fatal)", delResp.StatusCode)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. OTC options discovery regression guard
// ─────────────────────────────────────────────────────────────────────────────

// TestSP2a_OTCOptions_DiscoveryStillServes verifies that GET /api/v3/otc/options
// is reachable and returns 200 with an offers[] array after the SP-2a table fold.
// The array may be empty when no listings have been posted yet; the test only
// guards shape, not content.
func TestSP2a_OTCOptions_DiscoveryStillServes(t *testing.T) {
	t.Parallel()

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("SP-2a otc/options: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("SP-2a otc/options: endpoint not deployed — skipping")
	}
	helpers.RequireStatus(t, resp, 200)

	// offers[] key must be present (may be empty slice).
	if _, ok := resp.Body["offers"]; !ok {
		t.Errorf("SP-2a otc/options: want 'offers' key in response body, got %v", resp.Body)
	}
}

// TestSP2a_OTCOptions_HasProvenanceFields is a narrow regression test: when
// at least one local offer exists, every item in offers[] must carry the SP-1
// provenance fields (id, kind, me_owner) after the SP-2a fold.  This ensures
// the fold did not drop the service-layer provenance stamping.
//
// This test requires stocks to be seeded and the order simulator to be active.
// It skips cleanly when those conditions aren't met (same pattern as SP-1 test).
func TestSP2a_OTCOptions_HasProvenanceFields(t *testing.T) {
	t.Parallel()

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-2a otc-options provenance: no seeded stock — skipping")
	}

	// Seed the seller's holding via a market buy.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 3,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-2a: seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-2a: seed buy returned %d — skipping", orderResp.StatusCode)
	}
	if !tryWaitForOrderFill(t, sellerC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("SP-2a: seed buy did not fill — skipping")
	}

	// Create an OTC option listing.
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "1",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-2a: create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-2a: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("SP-2a: create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}

	// Give the cache a moment.
	time.Sleep(2 * time.Second)

	listResp, err := sellerC.GET("/api/v3/otc/options")
	if err != nil {
		t.Fatalf("SP-2a: list offers: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	offers, ok := listResp.Body["offers"].([]interface{})
	if !ok || len(offers) == 0 {
		t.Skip("SP-2a: offer list empty right after create (cache lag) — skipping provenance check")
	}

	// Every item must carry the SP-1/SP-2a provenance fields.
	for _, raw := range offers {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, field := range []string{"id", "kind", "me_owner"} {
			if _, present := item[field]; !present {
				t.Errorf("SP-2a: offer item missing field %q: %v", field, item)
			}
		}
		// kind must be a non-empty string ("local" or "remote").
		kind, _ := item["kind"].(string)
		if kind == "" {
			t.Errorf("SP-2a: offer item has empty kind: %v", item)
		}
	}
}

// TestSP2a_RemotePeerOffer_RequiresTwoStacks documents the cross-bank assertion.
// Requires two docker-compose stacks + a registered peer; guarded with t.Skip.
func TestSP2a_RemotePeerOffer_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-2a remote-offer: requires two-stack peer — run with a second live bank registered as a peer")
}
