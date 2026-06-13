//go:build integration

package workflows

// SP-3 integration tests — the bank as a first-class cross-bank OTC principal.
//
// SP-3 made an employee acting AS THE BANK a full cross-bank OTC participant:
//
//   - Bank-owned OTC option offers publish "employee-<ActingEmployeeID>" on the
//     SI-TX wire (never the legacy literal "bank"), so peer banks can bid on
//     them.
//   - The bank can bid / counter / accept / reject / cancel / exercise across
//     banks, settling against BANK accounts/holdings (owner sentinel
//     1000000000). The wire id is stable per-resource via the acting_employee_id
//     column.
//   - The bank sees its own remote chains in every read view
//     (ListMyNegotiations, the per-listing negotiations view, history, timeline,
//     and the my_negotiation_id stamp), matched by the "employee-<N>" prefix.
//   - The cross-bank exercise strike account is gated (gateway
//     ResolveAndCheckAccountByNumber + a stock-service bank re-assert).
//
// SP-3 is entirely stock-service-side: no route was added or removed and the
// gateway forwards identity unchanged. The LOCAL slice below — an employee
// (admin) acting as the bank creating a bank-owned OTC option offer through the
// real unified route — is runnable on a single stack and is exercised here. The
// cross-bank bank-principal flows (bid as bank / a peer bidding on our bank
// offer / bank counter+accept+exercise) require a second docker-compose stack
// registered as a peer and are left as documented t.Skip — never silent
// omissions; they are exercised in the Docker two-stack run.
//
// FE-style: every id is pulled from a REST response, never read from the DB.

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// ─────────────────────────────────────────────────────────────────────────────
// 1. LOCAL — an employee (admin) acting as the bank creates a bank-owned OTC
//    option offer; it is created bank-owned and shows up in the bank's listing
//    with me_owner=true.
// ─────────────────────────────────────────────────────────────────────────────

// TestSP3_BankOwnedOptionOffer_CreatedAndListedAsOwner drives the real unified
// route POST /api/v3/me/otc/options as an ADMIN. The /me/otc/options group is
// behind the bankIfEmp middleware (OwnerIsBankIfEmployee), so an employee caller
// resolves to owner_type="bank" and the listing is created BANK-owned. We assert:
//
//  1. The create response's offer carries the bank seller id (seller_id="bank"
//     on this LOCAL read view — the cross-bank wire id "employee-<N>" is only
//     composed on the SI-TX publish path, not surfaced here).
//  2. The same offer appears in the bank's own listing
//     (GET /api/v3/me/otc/options) stamped me_owner=true.
//
// All ids come from REST responses (FE-style); the holding the sell_initiated
// offer needs is seeded by the bank itself via a /me/orders buy (which also
// resolves to owner=bank under bankIfEmp).
func TestSP3_BankOwnedOptionOffer_CreatedAndListedAsOwner(t *testing.T) {
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-3 bank-owned offer: no seeded stock with listing — skipping")
	}

	bankAcctID := getBankRSDAccountID(t, adminC)

	// Seed a BANK holding of the ticker so the sell_initiated offer is backed.
	// The admin places this buy via /me/orders against the bank's RSD account;
	// bankIfEmp resolves owner=bank, so the resulting holding is bank-owned.
	orderResp, err := adminC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 10,
		"account_id": bankAcctID,
	})
	if err != nil {
		t.Fatalf("SP-3 bank-owned offer: seed bank buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-3 bank-owned offer: seed bank buy returned %d body=%v — skipping", orderResp.StatusCode, orderResp.Body)
	}
	if !tryWaitForOrderFill(t, adminC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("SP-3 bank-owned offer: seed bank buy did not fill — skipping")
	}

	// ── Create a bank-owned sell_initiated OTC option offer via the real route.
	createResp, err := adminC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "2",
		"account_id": bankAcctID,
	})
	if err != nil {
		t.Fatalf("SP-3 bank-owned offer: create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-3 bank-owned offer: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("SP-3 bank-owned offer: create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}

	offer, ok := createResp.Body["offer"].(map[string]interface{})
	if !ok {
		t.Fatalf("SP-3 bank-owned offer: create response missing 'offer' object; body=%v", createResp.Body)
	}
	offerID := jsonInt(offer["local_id"])
	if offerID == 0 {
		offerID = jsonInt(offer["id"])
	}
	if offerID == 0 {
		t.Fatalf("SP-3 bank-owned offer: create response carried no offer id; offer=%v", offer)
	}
	// The local read view stamps the bank seller id as "bank" (the cross-bank
	// wire identity "employee-<N>" is composed only on the SI-TX publish path).
	if sid, _ := offer["seller_id"].(string); sid != "bank" {
		t.Errorf("SP-3 bank-owned offer: created offer seller_id = %q, want %q (offer must be bank-owned)", sid, "bank")
	}

	// ── The offer appears in the BANK's own listing with me_owner=true. ──
	// The /me/otc/options listing is served from a periodically-refreshed
	// in-memory cache, so a just-created offer is not immediately present —
	// poll until it shows up (mirrors the polling in TestSP2b_OfferList_*).
	var foundItem map[string]interface{}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) && foundItem == nil {
		listResp, err := adminC.GET("/api/v3/me/otc/options")
		if err != nil {
			t.Fatalf("SP-3 bank-owned offer: bank list own options: %v", err)
		}
		helpers.RequireStatus(t, listResp, 200)
		offers, _ := listResp.Body["offers"].([]interface{})
		for _, raw := range offers {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if jsonInt(item["id"]) != offerID {
				continue
			}
			foundItem = item
			break
		}
		if foundItem == nil {
			time.Sleep(2 * time.Second)
		}
	}
	if foundItem == nil {
		t.Errorf("SP-3 bank-owned offer: created offer id=%d not present in the bank's own /me/otc/options listing within 45s", offerID)
		return
	}
	if meOwner, _ := foundItem["me_owner"].(bool); !meOwner {
		t.Errorf("SP-3 bank-owned offer: bank's own listing item should be me_owner=true; item=%v", foundItem)
	}
	if sid, _ := foundItem["seller_id"].(string); sid != "bank" {
		t.Errorf("SP-3 bank-owned offer: bank's own listing item seller_id = %q, want %q", sid, "bank")
	}
}

// TestSP3_BankCanBidOnLocalOption_OwnerTypeBank is a secondary LOCAL check that
// the bank principal is accepted on the unified bid route (the bid handler's
// ResolveAndCheckAccount gate requires a BANK account for the bank principal —
// SP-3 forces a bank account for the bank case rather than rejecting). A client
// posts a sell_initiated listing; the bank (admin acting as bank) opens a bid
// chain on it via POST /api/v3/otc/options/:id/bid with the bank's RSD account.
//
// This exercises the gateway-side gate that SP-3 documents (bank principal MAY
// bid; a non-bank account would 403). Cross-bank dispatch is a separate,
// two-stack-only concern (see the skips below).
func TestSP3_BankCanBidOnLocalOption_OwnerTypeBank(t *testing.T) {
	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("SP-3 bank-bid-local: no seeded stock with listing — skipping")
	}

	// Seller seeds their holding then lists a sell_initiated OTC option.
	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 10,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-3 bank-bid-local: seller seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("SP-3 bank-bid-local: seller seed buy returned %d — skipping", orderResp.StatusCode)
	}
	if !tryWaitForOrderFill(t, sellerC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("SP-3 bank-bid-local: seller seed buy did not fill — skipping")
	}

	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction":  "sell_initiated",
		"ticker":     ticker,
		"quantity":   "2",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("SP-3 bank-bid-local: create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("SP-3 bank-bid-local: OTC option endpoints not deployed — skipping")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("SP-3 bank-bid-local: create listing: want 201, got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))
	if offerID == 0 {
		t.Fatalf("SP-3 bank-bid-local: create response carried no offer id; body=%v", createResp.Body)
	}

	// ── The bank (admin acting as bank) bids with the bank's RSD account. ──
	// The bid handler resolves owner=bank and the ResolveAndCheckAccount gate
	// requires a BANK account; the seeded bank RSD account satisfies it. SP-3
	// accepts the bank principal (it no longer 409s as "not yet supported").
	bankAcctID := getBankRSDAccountID(t, adminC)
	bidResp, err := adminC.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": bankAcctID,
		"quantity":          "1",
		"strike_price":      "100",
		"premium":           "5",
		"settlement_date":   "2030-12-31",
	})
	if err != nil {
		t.Fatalf("SP-3 bank-bid-local: bank bid: %v", err)
	}
	// The local bid succeeds (201). A 412 means the seeded holding/listing was
	// not in a biddable state (race / no fill); skip rather than fail since the
	// SP-3 contract under test is "the bank principal is accepted, not 409".
	switch bidResp.StatusCode {
	case 201:
		negID := nestedNegotiationID(t, bidResp.Body)
		if negID == 0 {
			t.Errorf("SP-3 bank-bid-local: bank bid response carried no negotiation id; body=%v", bidResp.Body)
		}
	case 412:
		t.Skipf("SP-3 bank-bid-local: listing not biddable (412) — skipping; body=%v", bidResp.Body)
	case 409:
		t.Fatalf("SP-3 bank-bid-local: bank bid returned 409 — SP-3 should NOT reject the bank principal; body=%v", bidResp.Body)
	default:
		t.Fatalf("SP-3 bank-bid-local: bank bid: want 201 (bank accepted), got %d body=%v", bidResp.StatusCode, bidResp.Body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Cross-bank bank-principal flows — documented skips (require a 2nd live stack)
// ─────────────────────────────────────────────────────────────────────────────

// TestSP3_BankBidsCrossBank_RequiresTwoStacks documents that the bank (an
// employee acting as the bank) can bid on a REMOTE peer listing through the SAME
// unified route POST /api/v3/otc/options/:id/bid. The bid publishes
// buyerId="employee-<ActingEmployeeID>" to the seller's bank and settles against
// a BANK account. Exercising it needs a second docker-compose stack registered
// as a peer; unreachable from a single stack.
func TestSP3_BankBidsCrossBank_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-3 bank-bid cross-bank: requires a 2nd stack — the bank's bid on a remote listing publishes buyerId=employee-<N> and settles a bank account; run with a second live bank registered as a peer")
}

// TestSP3_PeerBidsOnOurBankOffer_RequiresTwoStacks documents that a peer bank
// can bid on OUR bank-owned LOCAL listing (which publishes
// sellerId="employee-<ActingEmployeeID>" cross-bank). Those peer bids surface to
// the bank caller in GET /api/v3/otc/options/:id/negotiations as kind="remote",
// me_owner=true (SP-3 Task 5b). Requires a 2nd stack to originate the peer bid.
func TestSP3_PeerBidsOnOurBankOffer_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-3 peer-bids-on-our-bank-offer: requires a 2nd stack — a peer must bid cross-bank on our employee-<N> bank offer for the remote-chain merge to surface")
}

// TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks documents that
// the bank can counter / accept / reject / cancel a cross-bank chain (reusing
// the row's stable employee-<N> wire id) and exercise a cross-bank-formed
// contract through the unified /me/otc/options/:id/negotiations/* and
// /otc/contracts/:id/exercise routes — the exercise strike account being gated
// to a BANK account by ResolveAndCheckAccountByNumber. Requires a 2nd stack with
// an active cross-bank bank-principal chain.
func TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks(t *testing.T) {
	t.Skip("SP-3 bank counter/accept/exercise cross-bank: requires a 2nd stack with an active cross-bank chain where this bank is a party (employee-<N>)")
}
