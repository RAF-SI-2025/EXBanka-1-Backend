//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF13: Transfer Fee Management ---

func TestFees_ListFees(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/fees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestFees_UnauthenticatedCannotManageFees(t *testing.T) {
	c := newClient()
	resp, err := c.GET("/api/fees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestFees_CreatePercentageFeeForAllTransactions(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             fmt.Sprintf("PctAll_%d", helpers.DateOfBirthUnix()),
		"fee_type":         "percentage",
		"fee_value":        "0.5",
		"transaction_type": "all",
		"min_amount":       "500.00",
		"max_fee":          "10000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating percentage fee (all), got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestFees_CreateFixedFeeForPayments(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             fmt.Sprintf("FixedPayment_%d", helpers.DateOfBirthUnix()),
		"fee_type":         "fixed",
		"fee_value":        "100.00",
		"transaction_type": "payment",
		"min_amount":       "0.00",
		"max_fee":          "100.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating fixed fee (payment), got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestFees_CreatePercentageFeeForTransfers(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             fmt.Sprintf("PctTransfer_%d", helpers.DateOfBirthUnix()),
		"fee_type":         "percentage",
		"fee_value":        "0.1",
		"transaction_type": "transfer",
		"min_amount":       "1000.00",
		"max_fee":          "5000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("expected success creating percentage fee (transfer), got %d: %s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestFees_CreateWithMissingName(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		// "name" intentionally omitted — binding:"required"
		"fee_type":         "percentage",
		"fee_value":        "0.5",
		"transaction_type": "all",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing name, got %d", resp.StatusCode)
	}
}

func TestFees_CreateWithMissingFeeValue(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":     "MissingValue",
		"fee_type": "percentage",
		// "fee_value" intentionally omitted — binding:"required"
		"transaction_type": "all",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing fee_value, got %d", resp.StatusCode)
	}
}

func TestFees_CreateWithMissingTransactionType(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":      "MissingTxType",
		"fee_type":  "percentage",
		"fee_value": "0.5",
		// "transaction_type" intentionally omitted — binding:"required"
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing transaction_type, got %d", resp.StatusCode)
	}
}

func TestFees_CreateWithInvalidFeeType(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             "InvalidType",
		"fee_type":         "invalid",
		"fee_value":        "100",
		"transaction_type": "all",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		t.Fatal("expected failure with invalid fee_type")
	}
}

func TestFees_CreateWithInvalidTransactionType(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             "InvalidTxType",
		"fee_type":         "fixed",
		"fee_value":        "50",
		"transaction_type": "wire", // invalid
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		t.Fatal("expected failure with invalid transaction_type")
	}
}

func TestFees_UpdateFee(t *testing.T) {
	c := loginAsAdmin(t)

	// Create a fee to update
	createResp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             fmt.Sprintf("UpdateMe_%d", helpers.DateOfBirthUnix()),
		"fee_type":         "percentage",
		"fee_value":        "0.3",
		"transaction_type": "all",
		"min_amount":       "100.00",
		"max_fee":          "5000.00",
	})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping update: create returned %d", createResp.StatusCode)
	}
	feeID := int(helpers.GetNumberField(t, createResp, "id"))

	// Update it
	resp, err := c.PUT(fmt.Sprintf("/api/fees/%d", feeID), map[string]interface{}{
		"name":             "UpdatedFee",
		"fee_type":         "percentage",
		"fee_value":        "0.5",
		"transaction_type": "all",
		"min_amount":       "200.00",
		"max_fee":          "8000.00",
	})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestFees_DeleteFee(t *testing.T) {
	c := loginAsAdmin(t)

	// Create a fee to delete
	createResp, err := c.POST("/api/fees", map[string]interface{}{
		"name":             fmt.Sprintf("DeleteMe_%d", helpers.DateOfBirthUnix()),
		"fee_type":         "fixed",
		"fee_value":        "25.00",
		"transaction_type": "payment",
		"min_amount":       "0.00",
		"max_fee":          "25.00",
	})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if createResp.StatusCode >= 400 {
		t.Skipf("skipping delete: create returned %d", createResp.StatusCode)
	}
	feeID := int(helpers.GetNumberField(t, createResp, "id"))

	// Delete it
	resp, err := c.DELETE(fmt.Sprintf("/api/fees/%d", feeID))
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}
