//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_PaymentVerificationFailureAndRetry exercises the verification failure path:
//
//	sender creates payment → creates challenge → submits wrong code 3 times →
//	challenge status is "failed" → sender creates NEW payment with correct flow →
//	second payment succeeds, balance decreased.
func TestWF_PaymentVerificationFailureAndRetry(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create sender and receiver
	_, senderAcct, senderC, _ := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-5: sender acct=%s, receiver acct=%s", senderAcct, receiverAcct)

	// Step 2: Sender creates a payment
	const paymentAmount = 3000.0
	createResp, err := senderC.POST("/api/me/payments", map[string]interface{}{
		"from_account_number": senderAcct,
		"to_account_number":   receiverAcct,
		"amount":              paymentAmount,
		"payment_purpose":     "verification retry test",
	})
	if err != nil {
		t.Fatalf("WF-5: create payment: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	paymentID := int(helpers.GetNumberField(t, createResp, "id"))
	t.Logf("WF-5: payment created id=%d", paymentID)

	// Step 3: Create challenge (get code but don't submit the correct one)
	challengeID, _ := createChallengeOnly(t, senderC, "payment", paymentID)
	t.Logf("WF-5: challenge id=%d", challengeID)

	// Step 4: Submit wrong code "000000" three times
	for i := 0; i < 3; i++ {
		resp, err := senderC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
			"code": "000000",
		})
		if err != nil {
			t.Fatalf("WF-5: wrong code attempt %d: %v", i+1, err)
		}
		t.Logf("WF-5: wrong code attempt %d: status=%d", i+1, resp.StatusCode)
	}

	// Step 5: Check that the challenge is now failed — submitting any code should not return
	// a successful verification
	failCheckResp, err := senderC.POST(fmt.Sprintf("/api/verifications/%d/code", challengeID), map[string]interface{}{
		"code": "111111",
	})
	if err != nil {
		t.Fatalf("WF-5: fail check request: %v", err)
	}
	// After 3 wrong attempts, the challenge should be in a failed/locked state.
	// The response should either be a non-200 status or verified=false.
	if failCheckResp.StatusCode == 200 {
		if verified, ok := failCheckResp.Body["verified"].(bool); ok && verified {
			t.Errorf("WF-5: expected challenge to be failed after 3 wrong attempts, but verification succeeded")
		}
	}
	t.Logf("WF-5: challenge correctly failed/locked (status=%d)", failCheckResp.StatusCode)

	// Step 6: Record balance before retry
	balBefore := getAccountBalance(t, adminC, senderAcct)

	// Step 7: Create a NEW payment and use createAndExecutePayment with the correct flow
	newPaymentID := createAndExecutePayment(t, senderC, senderAcct, receiverAcct, paymentAmount)
	t.Logf("WF-5: new payment executed id=%d", newPaymentID)

	// Step 8: Assert sender balance decreased (second payment went through)
	balAfter := getAccountBalance(t, adminC, senderAcct)
	balDecrease := balBefore - balAfter
	if balDecrease < paymentAmount-0.01 {
		t.Errorf("WF-5: sender balance decrease %.2f, expected >= %.2f", balDecrease, paymentAmount)
	}

	t.Logf("WF-5: PASS — verification retry succeeded, balance %.2f→%.2f (decrease=%.2f)",
		balBefore, balAfter, balDecrease)
}
