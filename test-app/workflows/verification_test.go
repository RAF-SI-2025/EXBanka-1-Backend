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

func TestVerification_CreateChallengeWithEmailMethod(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	resp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
		"method":         "email",
	})
	if err != nil {
		t.Fatalf("create verification error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "challenge_id")
}

func TestVerification_InvalidMethodRejected(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      1,
		"method":         "qr_scan", // not available
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for unavailable method qr_scan, got %d", resp.StatusCode)
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

// TestVerification_RealEmailCode tests the full email verification flow without bypass:
// create challenge with method=email → verification-service sends real code to notification.send-email →
// scan Kafka for the code → submit it → verify success=true → execute transfer.
func TestVerification_RealEmailCode(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, clientEmail := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
		"method":         "email",
	})
	if err != nil {
		t.Fatalf("create verification error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	realCode := scanKafkaForVerificationCode(t, clientEmail)
	t.Logf("received real verification code from Kafka for %s", clientEmail)

	submitResp, err := clientC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": realCode,
	})
	if err != nil {
		t.Fatalf("submit code error: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)
	helpers.RequireFieldEquals(t, submitResp, "success", true)

	execResp, err := clientC.POST(fmt.Sprintf("/api/me/transfers/%d/execute", transferID), map[string]interface{}{
		"verification_code": realCode,
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("execute transfer error: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)
	t.Logf("transfer executed successfully with real email verification code")
}

// TestVerification_CodePullFallbackToEmail tests that code_pull without a device_id
// (browser session) falls back to email delivery — the same Kafka scan flow.
func TestVerification_CodePullFallbackToEmail(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, accountNumber, clientC, clientEmail := setupActivatedClient(t, adminC)
	transferID := setupTransferForVerification(t, adminC, clientID, accountNumber, clientC)

	// code_pull with no device_id — browser session, should fall back to email delivery
	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "transfer",
		"source_id":      transferID,
		// method defaults to "code_pull", no device_id → email fallback
	})
	if err != nil {
		t.Fatalf("create verification error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	realCode := scanKafkaForVerificationCode(t, clientEmail)
	t.Logf("code_pull fallback: received code from Kafka for %s", clientEmail)

	submitResp, err := clientC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": realCode,
	})
	if err != nil {
		t.Fatalf("submit code error: %v", err)
	}
	helpers.RequireStatus(t, submitResp, 200)
	helpers.RequireFieldEquals(t, submitResp, "success", true)
	t.Logf("code_pull email fallback verified successfully")
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
