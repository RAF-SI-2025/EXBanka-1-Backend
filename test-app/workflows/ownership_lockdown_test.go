//go:build integration

package workflows

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestOwnership_BobCannotReadAliceLoan asserts that client Bob calling
// GET /api/me/loans/:id with Alice's loan id returns 404 (not 200, not 403).
func TestOwnership_BobCannotReadAliceLoan(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, aliceAcct, aliceC, _ := setupActivatedClient(t, adminC)
	_, _, bobC, _ := setupActivatedClient(t, adminC)

	// Alice creates a loan request and admin approves it.
	aliceReqID := submitLoanRequest(t, aliceC, aliceAcct, 5000, 12)
	approveResp, err := adminC.POST(fmt.Sprintf("/api/v1/loan-requests/%d/approve", aliceReqID), nil)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	helpers.RequireStatus(t, approveResp, 200)
	aliceLoanID := int(helpers.GetNumberField(t, approveResp, "id"))

	// Bob tries to read Alice's loan → 404.
	resp, err := bobC.GET("/api/v1/me/loans/" + strconv.Itoa(aliceLoanID))
	if err != nil {
		t.Fatalf("bob GET: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 ownership mismatch, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// TestOwnership_BobCannotReadAlicePayment — same for /api/me/payments/:id
func TestOwnership_BobCannotReadAlicePayment(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, aliceAcct, aliceC, _ := setupActivatedClient(t, adminC)
	_, _, bobC, _ := setupActivatedClient(t, adminC)

	// Alice creates a payment (intentionally to a likely-nonexistent account — just need the record).
	payResp, err := aliceC.POST("/api/v1/me/payments", map[string]interface{}{
		"from_account_number": aliceAcct,
		"to_account_number":   "999000000000000001",
		"amount":              100,
		"payment_purpose":     "ownership lockdown test",
	})
	if err != nil {
		t.Fatalf("alice create payment: %v", err)
	}
	if payResp.StatusCode != 201 {
		t.Skipf("alice could not create payment (%d body=%v) — skipping ownership test", payResp.StatusCode, payResp.Body)
	}
	alicePayID := int(helpers.GetNumberField(t, payResp, "id"))

	// Bob tries to read it → 404.
	resp, err := bobC.GET("/api/v1/me/payments/" + strconv.Itoa(alicePayID))
	if err != nil {
		t.Fatalf("bob GET: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// TestOwnership_BobCannotSetPinOnAliceCard asserts that Bob cannot set a PIN
// on a card that belongs to Alice.
func TestOwnership_BobCannotSetPinOnAliceCard(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, aliceCardID, _, _ := setupClientWithCard(t, adminC, "visa")
	_, _, bobC, _ := setupActivatedClient(t, adminC)

	resp, err := bobC.POST("/api/v1/me/cards/"+strconv.Itoa(aliceCardID)+"/pin", map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("bob set pin: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 ownership mismatch, got %d", resp.StatusCode)
	}
}

// TestOwnership_BobCannotBlockAliceCard asserts that Bob cannot temporarily
// block a card that belongs to Alice.
func TestOwnership_BobCannotBlockAliceCard(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, aliceCardID, _, _ := setupClientWithCard(t, adminC, "visa")
	_, _, bobC, _ := setupActivatedClient(t, adminC)

	resp, err := bobC.POST("/api/v1/me/cards/"+strconv.Itoa(aliceCardID)+"/temporary-block", map[string]interface{}{
		"duration_hours": 1,
		"reason":         "ownership lockdown test",
	})
	if err != nil {
		t.Fatalf("bob block: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// TestOwnership_BodyClientIdIgnoredOnLoanRequest asserts that the gateway
// forces client_id from the JWT and ignores any client_id supplied in the body.
func TestOwnership_BodyClientIdIgnoredOnLoanRequest(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	aliceID, _, _, _ := setupActivatedClient(t, adminC)
	_, bobAcct, bobC, _ := setupActivatedClient(t, adminC)

	// Bob sends client_id=alice in body — should be ignored, loan belongs to Bob.
	resp, err := bobC.POST("/api/v1/me/loan-requests", map[string]interface{}{
		"client_id":        aliceID, // must be ignored by gateway
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           1000.00,
		"currency_code":    "RSD",
		"repayment_period": 12,
		"account_number":   bobAcct,
	})
	if err != nil {
		t.Fatalf("bob create: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d body=%v", resp.StatusCode, resp.Body)
	}
	resultClientID := int(helpers.GetNumberField(t, resp, "client_id"))
	if resultClientID == aliceID {
		t.Errorf("gateway forwarded body client_id=%d (alice) — should be Bob's ID", aliceID)
	}
}
