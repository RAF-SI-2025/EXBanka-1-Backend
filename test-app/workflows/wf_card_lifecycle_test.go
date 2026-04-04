//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_CardFullLifecycle exercises the complete card lifecycle:
//
//	client + account created → client requests card → admin approves →
//	client sets PIN → client verifies PIN → client temporary-blocks →
//	admin unblocks → admin deactivates → unblock after deactivate fails →
//	client requests new card → succeeds.
func TestWF_CardFullLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create client with funded account
	_, accountNum, clientC, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-3: account=%s", accountNum)

	// Step 2: Client requests a card
	cardReqResp, err := clientC.POST("/api/me/cards/requests", map[string]interface{}{
		"account_number": accountNum,
		"card_brand":     "visa",
	})
	if err != nil {
		t.Fatalf("WF-3: create card request: %v", err)
	}
	helpers.RequireStatus(t, cardReqResp, 201)
	cardReqID := int(helpers.GetNumberField(t, cardReqResp, "id"))
	t.Logf("WF-3: card request id=%d", cardReqID)

	// Step 3: Admin approves the card request
	approveResp, err := adminC.POST(fmt.Sprintf("/api/cards/requests/%d/approve", cardReqID), nil)
	if err != nil {
		t.Fatalf("WF-3: approve card request: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)
	t.Logf("WF-3: card request approved")

	// Step 4: List client's cards to get the card ID
	cardsResp, err := adminC.GET(fmt.Sprintf("/api/cards?account_number=%s", accountNum))
	if err != nil {
		t.Fatalf("WF-3: list cards: %v", err)
	}
	helpers.RequireStatus(t, cardsResp, 200)

	var cardID int
	if cards, ok := cardsResp.Body["cards"]; ok {
		raw, _ := json.Marshal(cards)
		var arr []interface{}
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			if cm, ok := arr[0].(map[string]interface{}); ok {
				if id, ok := cm["id"].(float64); ok {
					cardID = int(id)
				}
			}
		}
	}
	if cardID == 0 {
		t.Fatal("WF-3: no card found after approval")
	}
	t.Logf("WF-3: card id=%d", cardID)

	// Step 5: Client sets PIN
	setPinResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/pin", cardID), map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("WF-3: set PIN: %v", err)
	}
	helpers.RequireStatus(t, setPinResp, 200)

	// Client verifies PIN
	verifyPinResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/verify-pin", cardID), map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("WF-3: verify PIN: %v", err)
	}
	helpers.RequireStatus(t, verifyPinResp, 200)
	t.Logf("WF-3: PIN set and verified")

	// Step 6: Client applies temporary block (client can only do temporary-block, not permanent block)
	blockResp, err := clientC.POST(fmt.Sprintf("/api/me/cards/%d/temporary-block", cardID), map[string]interface{}{
		"duration_hours": 1,
	})
	if err != nil {
		t.Fatalf("WF-3: temporary block: %v", err)
	}
	helpers.RequireStatus(t, blockResp, 200)
	t.Logf("WF-3: card temporarily blocked")

	// Step 7: Admin unblocks the card
	unblockResp, err := adminC.POST(fmt.Sprintf("/api/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("WF-3: admin unblock: %v", err)
	}
	helpers.RequireStatus(t, unblockResp, 200)
	t.Logf("WF-3: card unblocked by admin")

	// Step 8: Admin deactivates the card
	deactivateResp, err := adminC.POST(fmt.Sprintf("/api/cards/%d/deactivate", cardID), nil)
	if err != nil {
		t.Fatalf("WF-3: deactivate: %v", err)
	}
	helpers.RequireStatus(t, deactivateResp, 200)
	t.Logf("WF-3: card deactivated")

	// Step 9: Try to unblock after deactivation — should fail (non-200)
	unblockAfterDeactivate, err := adminC.POST(fmt.Sprintf("/api/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("WF-3: unblock after deactivate request: %v", err)
	}
	if unblockAfterDeactivate.StatusCode == 200 {
		t.Fatal("WF-3: expected unblock to fail after deactivation, got 200")
	}
	t.Logf("WF-3: unblock after deactivate correctly rejected (status=%d)", unblockAfterDeactivate.StatusCode)

	// Step 10: Client requests a new card — should succeed
	newCardReqResp, err := clientC.POST("/api/me/cards/requests", map[string]interface{}{
		"account_number": accountNum,
		"card_brand":     "mastercard",
	})
	if err != nil {
		t.Fatalf("WF-3: new card request: %v", err)
	}
	helpers.RequireStatus(t, newCardReqResp, 201)
	newCardReqID := int(helpers.GetNumberField(t, newCardReqResp, "id"))
	t.Logf("WF-3: PASS — full card lifecycle completed, new card request id=%d", newCardReqID)
}
