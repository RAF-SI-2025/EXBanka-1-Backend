//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Verification Endpoints ---

func TestVerification_CreateChallenge(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	// Create a payment first so we have a source_id
	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   accountNumber,
		"amount":              "100.00",
		"recipient_name":      "Test Recipient",
		"payment_code":        "289",
		"reference_number":    "12345678",
		"payment_purpose":     "Verification test",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	if payResp.StatusCode >= 400 {
		t.Skipf("skipping: create payment returned %d: %s", payResp.StatusCode, string(payResp.RawBody))
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	// Create verification challenge (default method = code_pull)
	resp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
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
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   accountNumber,
		"amount":              "50.00",
		"recipient_name":      "Test Recipient",
		"payment_code":        "289",
		"reference_number":    "12345679",
		"payment_purpose":     "Email verification test",
	})
	if err != nil {
		t.Fatalf("create payment error: %v", err)
	}
	if payResp.StatusCode >= 400 {
		t.Skipf("skipping: create payment returned %d", payResp.StatusCode)
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	resp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
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
		"source_service": "payment",
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
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   accountNumber,
		"amount":              "75.00",
		"recipient_name":      "Status Test",
		"payment_code":        "289",
		"reference_number":    "12345680",
		"payment_purpose":     "Status test",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if payResp.StatusCode >= 400 {
		t.Skipf("skipping: create payment returned %d", payResp.StatusCode)
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Poll status
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
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   accountNumber,
		"amount":              "60.00",
		"recipient_name":      "Bypass Code Test",
		"payment_code":        "289",
		"reference_number":    "12345681",
		"payment_purpose":     "Bypass code test",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if payResp.StatusCode >= 400 {
		t.Skipf("skipping: create payment returned %d", payResp.StatusCode)
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Submit bypass code 111111
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
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	payResp, err := clientC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   accountNumber,
		"amount":              "55.00",
		"recipient_name":      "Wrong Code Test",
		"payment_code":        "289",
		"reference_number":    "12345682",
		"payment_purpose":     "Wrong code test",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if payResp.StatusCode >= 400 {
		t.Skipf("skipping: create payment returned %d", payResp.StatusCode)
	}
	paymentID := int(helpers.GetNumberField(t, payResp, "id"))

	createResp, err := clientC.POST("/api/verifications", map[string]interface{}{
		"source_service": "payment",
		"source_id":      paymentID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))

	// Submit wrong code
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
		"source_service": "payment",
		"source_id":      1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
