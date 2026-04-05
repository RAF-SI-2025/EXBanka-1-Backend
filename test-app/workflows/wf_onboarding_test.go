//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_FullClientOnboardingToFirstTransaction exercises the complete new-client
// onboarding flow through to their first payment:
//
//	admin creates sender + receiver → sender adds payment recipient →
//	sender pays 5000 RSD → verify balances (receiver gained ~5000, sender lost >=5000,
//	bank gained fee) → admin can view the payment.
func TestWF_FullClientOnboardingToFirstTransaction(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create sender and receiver clients via helper
	senderID, senderAcct, senderC, senderEmail := setupActivatedClient(t, adminC)
	_, receiverAcct, _, _ := setupActivatedClient(t, adminC)

	t.Logf("WF-1: sender id=%d acct=%s, receiver acct=%s", senderID, senderAcct, receiverAcct)

	// Step 2: Record balances before payment
	senderBalBefore := getAccountBalance(t, adminC, senderAcct)
	receiverBalBefore := getAccountBalance(t, adminC, receiverAcct)
	_, bankBalBefore := getBankRSDAccount(t, adminC)

	// Step 3: Sender adds receiver as a payment recipient
	recipientResp, err := senderC.POST("/api/me/payment-recipients", map[string]interface{}{
		"client_id":      senderID,
		"account_number": receiverAcct,
		"recipient_name": "Test Receiver",
	})
	if err != nil {
		t.Fatalf("WF-1: create payment recipient: %v", err)
	}
	helpers.RequireStatus(t, recipientResp, 201)
	t.Logf("WF-1: payment recipient created")

	// Step 4: Sender creates and executes payment of 5000 RSD
	const paymentAmount = 5000.0
	paymentID := createAndExecutePayment(t, senderC, receiverAcct, paymentAmount, senderEmail)
	t.Logf("WF-1: payment executed id=%d", paymentID)

	// Step 5: Assert balances changed correctly
	senderBalAfter := getAccountBalance(t, adminC, senderAcct)
	receiverBalAfter := getAccountBalance(t, adminC, receiverAcct)
	_, bankBalAfter := getBankRSDAccount(t, adminC)

	// Receiver should have gained ~5000 RSD
	receiverGain := receiverBalAfter - receiverBalBefore
	if receiverGain < paymentAmount-0.01 || receiverGain > paymentAmount+0.01 {
		t.Errorf("WF-1: receiver balance increase %.2f, expected %.2f", receiverGain, paymentAmount)
	}

	// Sender should have lost at least 5000 RSD (5000 + fee)
	senderLoss := senderBalBefore - senderBalAfter
	if senderLoss < paymentAmount-0.01 {
		t.Errorf("WF-1: sender balance decrease %.2f, expected >= %.2f", senderLoss, paymentAmount)
	}

	// Bank should have gained the fee (sender loss minus 5000)
	fee := senderLoss - paymentAmount
	bankGain := bankBalAfter - bankBalBefore
	if fee > 0.01 {
		if bankGain < fee-0.01 || bankGain > fee+0.01 {
			t.Errorf("WF-1: bank fee gain %.2f, expected %.2f", bankGain, fee)
		}
		t.Logf("WF-1: fee charged = %.2f", fee)
	}

	// Step 6: Verify admin can view the payment
	adminPaymentResp, err := adminC.GET(fmt.Sprintf("/api/payments/%d", paymentID))
	if err != nil {
		t.Fatalf("WF-1: admin get payment: %v", err)
	}
	helpers.RequireStatus(t, adminPaymentResp, 200)
	helpers.RequireField(t, adminPaymentResp, "id")

	t.Logf("WF-1: PASS — sender %.2f→%.2f, receiver %.2f→%.2f, bank fee=%.2f",
		senderBalBefore, senderBalAfter, receiverBalBefore, receiverBalAfter, fee)
}
