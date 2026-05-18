//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Mobile Verification Polling & Submission Tests ---
//
// These tests simulate a real mobile app: all interactions go through the API.
// The only Kafka usage is reading the mobile activation code (no other way to get it).
// Verification codes are extracted from display_data in the pending response,
// exactly as a real mobile app would do.

// extractCodeFromPendingItem extracts the verification code from a pending item's
// display_data field, just like a real mobile app would parse it.
func extractCodeFromPendingItem(t *testing.T, item map[string]interface{}) string {
	t.Helper()
	dd, ok := item["display_data"]
	if !ok {
		t.Fatal("pending item missing display_data")
	}

	// display_data may be a JSON string or already parsed map
	var displayMap map[string]interface{}
	switch v := dd.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &displayMap); err != nil {
			t.Fatalf("display_data is not valid JSON: %q", v)
		}
	case map[string]interface{}:
		displayMap = v
	default:
		t.Fatalf("display_data has unexpected type %T: %v", dd, dd)
	}

	code, ok := displayMap["code"].(string)
	if !ok || code == "" {
		t.Fatalf("display_data missing 'code' field: %v", displayMap)
	}
	return code
}

// TestMobileVerification_BrowserChallengeVisibleOnMobile simulates:
//  1. Admin creates client + account
//  2. Client activates mobile device (activation code from Kafka)
//  3. Client logs in from browser, creates a transfer
//  4. Browser creates verification challenge
//  5. Mobile app polls pending → sees the challenge with code in display_data
func TestMobileVerification_BrowserChallengeVisibleOnMobile(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, browserC, mobileC, _ := setupMobileDevice(t, adminC)

	// Browser: create transfer
	transferID := setupTransferForVerification(t, adminC, clientID, acctNum, browserC)

	// Browser: create verification challenge
	createResp, err := browserC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))
	t.Logf("Browser created challenge %d for transfer %d", challengeID, transferID)

	// Mobile app: poll pending verifications until challenge appears
	item := pollPendingUntilFound(t, mobileC, challengeID, 15*time.Second)

	// Mobile app: verify the item has expected fields
	if method, ok := item["method"].(string); !ok || method != "code_pull" {
		t.Fatalf("expected method code_pull, got %v", item["method"])
	}
	if _, ok := item["expires_at"]; !ok {
		t.Fatal("expected expires_at field in pending item")
	}

	// Mobile app: extract code from display_data (like a real app would)
	code := extractCodeFromPendingItem(t, item)
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %q", code)
	}

	t.Logf("PASS — mobile sees challenge %d with code %s in pending", challengeID, code)
}

// TestMobileVerification_VerifyFromMobile simulates the full end-to-end flow:
//  1. Browser creates transfer + challenge
//  2. Mobile polls pending, extracts code from display_data
//  3. Mobile submits the code via the submit endpoint
//  4. Browser checks status → verified
func TestMobileVerification_VerifyFromMobile(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, browserC, mobileC, _ := setupMobileDevice(t, adminC)

	// Browser: create transfer + challenge
	transferID := setupTransferForVerification(t, adminC, clientID, acctNum, browserC)
	createResp, err := browserC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Mobile: poll pending and extract code from display_data
	item := pollPendingUntilFound(t, mobileC, challengeID, 15*time.Second)
	code := extractCodeFromPendingItem(t, item)

	// Mobile: submit the verification code
	submitResp, err := mobileC.SignedPOST(
		fmt.Sprintf("/api/v3/mobile/verifications/%d/submit", challengeID),
		map[string]interface{}{"response": code},
	)
	if err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)
	helpers.RequireFieldEquals(t, submitResp, "success", true)

	// Browser: check status → should be verified
	statusResp, err := browserC.GET(fmt.Sprintf("/api/v3/verifications/%d/status", challengeID))
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	helpers.RequireStatus(t, statusResp, 200)
	helpers.RequireFieldEquals(t, statusResp, "status", "verified")

	t.Logf("PASS — challenge %d: mobile extracted code %s, submitted, browser sees 'verified'", challengeID, code)
}

// TestMobileVerification_MobileCreatedChallengeVisible simulates a flow where
// the mobile app itself initiates the transaction and challenge:
//  1. Mobile creates transfer via /api/me/transfers
//  2. Mobile creates verification challenge via /api/verifications
//  3. Mobile polls pending → sees its own challenge
//  4. Mobile extracts code from display_data and submits
func TestMobileVerification_MobileCreatedChallengeVisible(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, _, mobileC, _ := setupMobileDevice(t, adminC)

	// Admin: create destination account
	dstResp, err := adminC.POST("/api/v3/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("create dst account: %v", err)
	}
	helpers.RequireStatus(t, dstResp, 201)
	dstAccount := helpers.GetStringField(t, dstResp, "account_number")

	// Mobile: create transfer (mobile token works with AnyAuth endpoints)
	tfrResp, err := mobileC.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": acctNum,
		"to_account_number":   dstAccount,
		"amount":              50,
	})
	if err != nil {
		t.Fatalf("create transfer from mobile: %v", err)
	}
	helpers.RequireStatus(t, tfrResp, 201)
	transferID := int(helpers.GetNumberField(t, tfrResp, "id"))

	// Mobile: create verification challenge
	createResp, err := mobileC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("create challenge from mobile: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))
	t.Logf("Mobile created transfer %d + challenge %d", transferID, challengeID)

	// Mobile: poll pending and extract code
	item := pollPendingUntilFound(t, mobileC, challengeID, 15*time.Second)
	code := extractCodeFromPendingItem(t, item)

	// Mobile: submit verification
	submitResp, err := mobileC.SignedPOST(
		fmt.Sprintf("/api/v3/mobile/verifications/%d/submit", challengeID),
		map[string]interface{}{"response": code},
	)
	if err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)
	helpers.RequireFieldEquals(t, submitResp, "success", true)

	t.Logf("PASS — challenge %d created and verified entirely from mobile (code: %s)", challengeID, code)
}

// TestMobileVerification_MultipleChallengesVisible simulates:
//  1. Browser creates transfer + challenge (challenge A)
//  2. Mobile creates transfer + challenge (challenge B)
//  3. Mobile polls pending → sees BOTH challenges
//  4. Mobile extracts codes from display_data and verifies both
//  5. Browser confirms both are verified
func TestMobileVerification_MultipleChallengesVisible(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, browserC, mobileC, _ := setupMobileDevice(t, adminC)

	// Create two destination accounts
	dstAccounts := make([]string, 2)
	for i := range dstAccounts {
		dstResp, err := adminC.POST("/api/v3/accounts", map[string]interface{}{
			"owner_id":        clientID,
			"account_kind":    "current",
			"account_type":    "personal",
			"currency_code":   "RSD",
			"initial_balance": 0,
		})
		if err != nil {
			t.Fatalf("create dst account %d: %v", i, err)
		}
		helpers.RequireStatus(t, dstResp, 201)
		dstAccounts[i] = helpers.GetStringField(t, dstResp, "account_number")
	}

	// Browser: transfer 1 + challenge 1
	tfr1Resp, err := browserC.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": acctNum,
		"to_account_number":   dstAccounts[0],
		"amount":              10,
	})
	if err != nil {
		t.Fatalf("create transfer 1: %v", err)
	}
	helpers.RequireStatus(t, tfr1Resp, 201)
	transfer1ID := int(helpers.GetNumberField(t, tfr1Resp, "id"))

	ch1Resp, err := browserC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transfer1ID,
	})
	if err != nil {
		t.Fatalf("create challenge 1: %v", err)
	}
	helpers.RequireStatus(t, ch1Resp, 200)
	challenge1ID := int(helpers.GetNumberField(t, ch1Resp, "challenge_id"))

	// Mobile: transfer 2 + challenge 2
	tfr2Resp, err := mobileC.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": acctNum,
		"to_account_number":   dstAccounts[1],
		"amount":              10,
	})
	if err != nil {
		t.Fatalf("create transfer 2: %v", err)
	}
	helpers.RequireStatus(t, tfr2Resp, 201)
	transfer2ID := int(helpers.GetNumberField(t, tfr2Resp, "id"))

	ch2Resp, err := mobileC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transfer2ID,
	})
	if err != nil {
		t.Fatalf("create challenge 2: %v", err)
	}
	helpers.RequireStatus(t, ch2Resp, 200)
	challenge2ID := int(helpers.GetNumberField(t, ch2Resp, "challenge_id"))
	t.Logf("Created challenges %d (browser) and %d (mobile)", challenge1ID, challenge2ID)

	// Mobile: poll until both challenges appear, extract codes from display_data
	item1 := pollPendingUntilFound(t, mobileC, challenge1ID, 15*time.Second)
	code1 := extractCodeFromPendingItem(t, item1)

	item2 := pollPendingUntilFound(t, mobileC, challenge2ID, 15*time.Second)
	code2 := extractCodeFromPendingItem(t, item2)

	// Mobile: verify both using codes from display_data
	for _, tc := range []struct {
		challengeID int
		code        string
	}{
		{challenge1ID, code1},
		{challenge2ID, code2},
	} {
		submitResp, err := mobileC.SignedPOST(
			fmt.Sprintf("/api/v3/mobile/verifications/%d/submit", tc.challengeID),
			map[string]interface{}{"response": tc.code},
		)
		if err != nil {
			t.Fatalf("submit challenge %d: %v", tc.challengeID, err)
		}
		helpers.RequireStatus(t, submitResp, 200)
		helpers.RequireFieldEquals(t, submitResp, "success", true)
	}

	// Browser: confirm both are verified
	for _, cID := range []int{challenge1ID, challenge2ID} {
		statusResp, err := browserC.GET(fmt.Sprintf("/api/v3/verifications/%d/status", cID))
		if err != nil {
			t.Fatalf("get status for %d: %v", cID, err)
		}
		helpers.RequireStatus(t, statusResp, 200)
		helpers.RequireFieldEquals(t, statusResp, "status", "verified")
	}

	t.Logf("PASS — both challenges verified from mobile with real codes (%s, %s)", code1, code2)
}

// TestMobileVerification_AckRemovesChallengeFromPending simulates:
//  1. Browser creates transfer + challenge
//  2. Mobile polls pending, verifies the challenge
//  3. Mobile ACKs the inbox item
//  4. Mobile polls again → item is gone
func TestMobileVerification_AckRemovesChallengeFromPending(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, browserC, mobileC, _ := setupMobileDevice(t, adminC)

	// Browser: create transfer + challenge
	transferID := setupTransferForVerification(t, adminC, clientID, acctNum, browserC)
	createResp, err := browserC.POST("/api/v3/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Mobile: poll and extract code
	item := pollPendingUntilFound(t, mobileC, challengeID, 15*time.Second)
	code := extractCodeFromPendingItem(t, item)
	inboxID := int(item["id"].(float64))

	// Mobile: submit verification
	submitResp, err := mobileC.SignedPOST(
		fmt.Sprintf("/api/v3/mobile/verifications/%d/submit", challengeID),
		map[string]interface{}{"response": code},
	)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)

	// Mobile: ACK the inbox item
	ackResp, err := mobileC.SignedPOST(
		fmt.Sprintf("/api/v3/mobile/verifications/%d/ack", inboxID),
		nil,
	)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	helpers.RequireStatus(t, ackResp, 200)
	t.Logf("ACK'd inbox item %d for challenge %d", inboxID, challengeID)

	// Mobile: poll again — challenge should be gone
	finalResp, err := mobileC.SignedGET("/api/v3/mobile/verifications/pending")
	if err != nil {
		t.Fatalf("final poll: %v", err)
	}
	helpers.RequireStatus(t, finalResp, 200)

	finalItems, _ := finalResp.Body["items"].([]interface{})
	for _, fi := range finalItems {
		m, ok := fi.(map[string]interface{})
		if !ok {
			continue
		}
		if cid, ok := m["challenge_id"].(float64); ok && int(cid) == challengeID {
			t.Fatalf("challenge %d still in pending after ACK", challengeID)
		}
	}

	t.Logf("PASS — challenge %d gone from pending after verify + ACK", challengeID)
}
