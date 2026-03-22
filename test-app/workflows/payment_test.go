package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF8: Payment Workflow ---

// Note: Payments are client-only write operations.
// These tests verify the employee-visible read paths and
// validate that unauthenticated/wrong-auth access is blocked.

func TestPayment_EmployeeCanReadPayments(t *testing.T) {
	c := loginAsAdmin(t)
	// Get payment by ID (may not exist but should return 404, not 401/403)
	resp, err := c.GET("/api/payments/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Expected: 404 (not found) not 401/403
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.Fatalf("expected read access for employee, got %d", resp.StatusCode)
	}
}

func TestPayment_UnauthenticatedCannotCreatePayment(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/payments", map[string]interface{}{
		"from_account_number": "123",
		"to_account_number":   "456",
		"amount":              "100.00",
		"payment_code":        "289",
		"purpose":             "test",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPayment_VerificationCodeRequired(t *testing.T) {
	// Verification code creation is client-only
	c := newClient()
	resp, err := c.POST("/api/verification", map[string]interface{}{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPayment_KafkaEventsOnPayment(t *testing.T) {
	// This test just verifies the Kafka listener can monitor payment topics.
	// Full payment flow requires an authenticated client with funded accounts.
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	// Allow listener to connect
	time.Sleep(2 * time.Second)

	// Check we can query payment topics (no events expected in fresh state)
	events := el.EventsByTopic("transaction.payment-completed")
	t.Logf("payment-completed events observed: %d", len(events))
}
