//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// setupTransferForVerification creates a second account for the client and a pending
// transfer between the two accounts. Returns the transfer ID to use as source_id.
func setupTransferForVerification(t *testing.T, adminC *client.APIClient, clientID int, fromAccount string, clientC *client.APIClient) int {
	t.Helper()
	dstResp, err := adminC.POST("/api/accounts", map[string]interface{}{
		"owner_id":        clientID,
		"account_kind":    "current",
		"account_type":    "personal",
		"currency_code":   "RSD",
		"initial_balance": 0,
	})
	if err != nil {
		t.Fatalf("setupTransferForVerification: create dst account: %v", err)
	}
	helpers.RequireStatus(t, dstResp, 201)
	dstAccount := helpers.GetStringField(t, dstResp, "account_number")

	tfrResp, err := clientC.POST("/api/me/transfers", map[string]interface{}{
		"from_account_number": fromAccount,
		"to_account_number":   dstAccount,
		"amount":              100,
	})
	if err != nil {
		t.Fatalf("setupTransferForVerification: create transfer: %v", err)
	}
	helpers.RequireStatus(t, tfrResp, 201)
	return int(helpers.GetNumberField(t, tfrResp, "id"))
}

// --- Verification Endpoints ---

func TestVerification_CreateChallenge(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	resp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("create verification error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "challenge_id")
	helpers.RequireField(t, resp, "expires_at")
}

func TestVerification_InvalidMethodRejected(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	for _, method := range []string{"qr_scan", "number_match", "email"} {
		resp, err := clientC.POST("/api/verifications", map[string]interface{}{
			"source_service": "transfer",
			"source_id":      1,
			"method":         method,
		})
		if err != nil {
			t.Fatalf("error for method %s: %v", method, err)
		}
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400 for unavailable method %s, got %d", method, resp.StatusCode)
		}
	}
}

func TestVerification_GetChallengeStatus(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	resp, err := clientC.GET(fmt.Sprintf("/api/verifications/%d/status", challengeID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "status")
	helpers.RequireField(t, resp, "method")
}

func TestVerification_SubmitBypassCode(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	resp, err := clientC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": "111111",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireFieldEquals(t, resp, "success", true)
}

func TestVerification_SubmitWrongCode(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	resp, err := clientC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": "999999",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireFieldEquals(t, resp, "success", false)
}

func TestVerification_UnauthenticatedRejected(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestVerification_AckEndpointRequiresMobileAuth verifies that the ACK endpoint
// rejects non-mobile tokens with 403.
func TestVerification_AckEndpointRequiresMobileAuth(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	// Client tokens are not mobile tokens — should get 403
	resp, err := clientC.POST("/api/mobile/verifications/1/ack", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for non-mobile token on ACK endpoint, got %d", resp.StatusCode)
	}
}
