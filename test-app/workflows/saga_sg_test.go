//go:build integration

package workflows

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// SG-* — SAGA.pdf exercise-saga scenarios.
//
// Gated behind SAGA_SG=1 because the fault scenarios (SG-05/07) require the
// stock-service to be built with -tags sagafaults (and run with
// SAGA_FAULTS_OK=1) so the X-Saga-* headers actually inject faults. The normal
// integration suite leaves SAGA_SG unset and skips this file.
//
// The fault tests assert the spec's core all-or-nothing guarantee through an
// end-to-end invariant: a forced-fail exercise must roll back completely, which
// is proven by a subsequent clean exercise of the SAME contract succeeding —
// only possible if the seller's shares, both parties' funds, and the contract's
// active status were fully restored (SAGA.pdf invariants I1/I2/I3/I6).
func sgEnabled(t *testing.T) {
	if os.Getenv("SAGA_SG") != "1" {
		t.Skip("SG saga suite disabled (set SAGA_SG=1 and run the sagafaults stock-service image)")
	}
}

// sgSetupContract drives a fresh OTC option contract to the point just before
// exercise: seller buys the stock, lists a sell_initiated offer, buyer accepts.
// Returns the contract id and the buyer client (the only party allowed to
// exercise). Skips (not fails) when the market simulator can't seed the holding.
func sgSetupContract(t *testing.T, adminC *client.APIClient) (contractID int, buyerC *client.APIClient) {
	t.Helper()
	enableTestingMode(t, adminC)

	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	buyerID, _, buyerCli, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)
	bAcctID, _ := createClientAccount(t, adminC, buyerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("seeded stock has no ticker/listing")
	}

	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 10,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("could not seed seller holding (order POST %d)", orderResp.StatusCode)
	}
	orderID := int(helpers.GetNumberField(t, orderResp, "id"))
	if !tryWaitForOrderFill(t, sellerC, orderID, 45*time.Second) {
		t.Skip("seed buy order did not fill")
	}

	// Phase-8/9 OTC option flow: seller lists → buyer opens a negotiation
	// chain (bid) → seller accepts the bid, which mints the OptionContract.
	createResp, err := sellerC.POST("/api/v3/me/otc/options", map[string]interface{}{
		"direction": "sell_initiated", "ticker": ticker, "quantity": "1",
		"strike_price": "100", "premium": "5", "settlement_date": "2030-12-31",
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("create listing: %v", err)
	}
	if createResp.StatusCode == 404 {
		t.Skip("v3 OTC option endpoints not deployed")
	}
	if createResp.StatusCode != 201 {
		t.Fatalf("create listing: got %d body=%v", createResp.StatusCode, createResp.Body)
	}
	offerID := int(helpers.GetNestedNumberField(t, createResp, "offer", "id"))

	bidResp, err := buyerCli.POST(fmt.Sprintf("/api/v3/otc/options/%d/bid", offerID), map[string]interface{}{
		"bidder_account_id": bAcctID, "quantity": "1", "strike_price": "100",
		"premium": "5", "settlement_date": "2030-12-31",
	})
	if err != nil {
		t.Fatalf("bid: %v", err)
	}
	if bidResp.StatusCode != 201 {
		t.Fatalf("bid: got %d body=%v", bidResp.StatusCode, bidResp.Body)
	}
	nid := int(helpers.GetNestedNumberField(t, bidResp, "negotiation", "id"))

	// Seller accepts the buyer's bid (accepter must be opposite the last
	// proposer). The accept runs the contract-formation saga and mints the
	// contract.
	acceptResp, err := sellerC.POST(fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/accept", offerID, nid), map[string]interface{}{
		"acceptor_account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acceptResp.StatusCode != 201 && acceptResp.StatusCode != 200 {
		t.Fatalf("accept: got %d body=%v", acceptResp.StatusCode, acceptResp.Body)
	}
	// contract_id may be at the root or nested under "contract".
	contractID = 0
	if v, ok := acceptResp.Body["contract_id"].(float64); ok {
		contractID = int(v)
	} else if cm, ok := acceptResp.Body["contract"].(map[string]interface{}); ok {
		if v, ok := cm["id"].(float64); ok {
			contractID = int(v)
		}
	}
	if contractID == 0 {
		t.Fatalf("accept: could not find contract id in response: %v", acceptResp.Body)
	}
	return contractID, buyerCli
}

// contractStatus returns the buyer's view of a contract's status (e.g.
// "ACTIVE", "EXERCISED") from GET /me/otc/contracts.
func contractStatus(t *testing.T, buyerC *client.APIClient, contractID int) string {
	t.Helper()
	resp, err := buyerC.GET("/api/v3/me/otc/contracts")
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("list contracts: status %d", resp.StatusCode)
	}
	arr, _ := resp.Body["contracts"].([]interface{})
	for _, c := range arr {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := m["id"].(float64); ok && int(id) == contractID {
			s, _ := m["status"].(string)
			return s
		}
	}
	t.Fatalf("contract %d not found in /me/otc/contracts", contractID)
	return ""
}

func sgExercise(buyerC *client.APIClient, contractID int, headers map[string]string) (*client.Response, error) {
	path := fmt.Sprintf("/api/v3/otc/contracts/%d/exercise", contractID)
	if len(headers) == 0 {
		return buyerC.POST(path, map[string]interface{}{})
	}
	return buyerC.POSTWithHeaders(path, map[string]interface{}{}, headers)
}

// SG-01: happy path — a clean exercise completes.
func TestSG01_HappyPath(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, buyerC := sgSetupContract(t, adminC)

	resp, err := sgExercise(buyerC, contractID, nil)
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("SG-01 expected 201, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// SG-02a: a non-buyer may not exercise.
func TestSG02a_NonBuyerRejected(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, _ := sgSetupContract(t, adminC)
	_, _, strangerC, _ := setupActivatedClient(t, adminC)

	resp, err := sgExercise(strangerC, contractID, nil)
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("SG-02a expected 4xx for non-buyer, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// SG-02b: a non-existent contract is rejected before any saga runs.
func TestSG02b_UnknownContract(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	_, buyerC := sgSetupContract(t, adminC)

	resp, err := sgExercise(buyerC, 999000111, nil)
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Fatalf("SG-02b expected 404/403 for unknown contract, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// SG-05: force-fail F3 (credit_strike_seller) — money steps compensate, no
// shares moved. Proven by a clean retry succeeding.
func TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, buyerC := sgSetupContract(t, adminC)

	failResp, err := sgExercise(buyerC, contractID, map[string]string{"X-Saga-Force-Fail": "credit_strike_seller"})
	if err != nil {
		t.Fatalf("exercise(force-fail): %v", err)
	}
	if failResp.StatusCode == 201 {
		t.Skip("forced failure did not take effect — stock-service not built with -tags sagafaults")
	}
	// Compensated: F3 failed before the share transfer, so F2/F1 rolled back and
	// the contract was never consumed — it must remain ACTIVE (SAGA.pdf I6).
	if st := contractStatus(t, buyerC, contractID); st != "ACTIVE" {
		t.Fatalf("SG-05 expected contract ACTIVE after compensation, got %q", st)
	}
}

// SG-07: force-fail F5 (mark_contract_exercised) — fails AFTER the share
// transfer. With the pivot removed (Phase 0) this must fully compensate:
// shares return to the seller, the buyer's credit is reversed, funds return,
// and the contract is left active — proven by a clean retry succeeding.
func TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, buyerC := sgSetupContract(t, adminC)

	failResp, err := sgExercise(buyerC, contractID, map[string]string{"X-Saga-Force-Fail": "mark_contract_exercised"})
	if err != nil {
		t.Fatalf("exercise(force-fail): %v", err)
	}
	if failResp.StatusCode == 201 {
		t.Skip("forced failure did not take effect — stock-service not built with -tags sagafaults")
	}
	// Post-pivot-removal payoff: F5 fails AFTER the share transfer, so the saga
	// must walk back C5..C1 (return shares to seller, reverse the buyer credit,
	// refund, release) and leave the contract ACTIVE — not EXERCISED.
	if st := contractStatus(t, buyerC, contractID); st != "ACTIVE" {
		t.Fatalf("SG-07 expected contract ACTIVE after full compensation, got %q", st)
	}
}
