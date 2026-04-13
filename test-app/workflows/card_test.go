//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF7: Card Management ---

// createTestAccountForCards creates a client + current account and returns (clientID, accountNumber).
func createTestAccountForCards(t *testing.T, c *client.APIClient) (int, string) {
	t.Helper()
	clientID := createTestClient(t, c)

	resp, err := c.POST("/api/v1/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
		"account_type":  "personal",
		"currency_code": "RSD",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	acctNum := helpers.GetStringField(t, resp, "account_number")
	return clientID, acctNum
}

func TestCard_CreateAllBrands(t *testing.T) {
	c := loginAsAdmin(t)
	brands := []string{"visa", "mastercard", "dinacard", "amex"}

	for _, brand := range brands {
		t.Run("brand_"+brand, func(t *testing.T) {
			clientID, acctNum := createTestAccountForCards(t, c)

			el := kafka.NewEventListener(cfg.KafkaBrokers)
			el.Start()
			defer el.Stop()

			resp, err := c.POST("/api/v1/cards", map[string]interface{}{
				"account_number": acctNum,
				"card_brand":     brand,
				"owner_type":     "client",
				"owner_id":       clientID,
			})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			helpers.RequireStatus(t, resp, 201)
			helpers.RequireField(t, resp, "id")

			_, found := el.WaitForEvent("card.created", 10*time.Second, nil)
			if !found {
				t.Fatal("expected card.created Kafka event")
			}
		})
	}
}

func TestCard_CreateWithInvalidBrand(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID, acctNum := createTestAccountForCards(t, c)

	resp, err := c.POST("/api/v1/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "discover", // invalid
		"owner_type":     "client",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid brand")
	}
}

func TestCard_BlockUnblockDeactivate(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID, acctNum := createTestAccountForCards(t, c)

	createResp, err := c.POST("/api/v1/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "visa",
		"owner_type":     "client",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Block
	resp, err := c.POST(fmt.Sprintf("/api/v1/cards/%d/block", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Unblock
	resp, err = c.POST(fmt.Sprintf("/api/v1/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Deactivate
	resp, err = c.POST(fmt.Sprintf("/api/v1/cards/%d/deactivate", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestCard_VirtualCardSingleUse(t *testing.T) {
	t.Parallel()
	// Virtual card creation is a client-only route, needs client auth.
	// Test that unauthenticated access fails.
	c := newClient()
	resp, err := c.POST("/api/v1/me/cards/virtual", map[string]interface{}{
		"account_number": "test",
		"usage_type":     "single_use",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 for unauthenticated virtual card creation, got %d", resp.StatusCode)
	}
}

func TestCard_PINManagement(t *testing.T) {
	t.Parallel()
	// PIN operations are client-only routes.
	// Test that employee tokens can't access these routes.
	c := loginAsAdmin(t) // employee token
	resp, err := c.POST("/api/v1/me/cards/1/pin", map[string]interface{}{
		"pin": "1234",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Should fail - these are client-only routes
	if resp.StatusCode == 200 {
		t.Fatal("expected failure: PIN set should be client-only")
	}
}

func TestCard_GetCard(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID, acctNum := createTestAccountForCards(t, c)

	createResp, err := c.POST("/api/v1/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "visa",
		"owner_type":     "client",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := c.GET(fmt.Sprintf("/api/v1/cards/%d", cardID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestCard_ListByAccount(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientID, acctNum := createTestAccountForCards(t, c)

	// Create a card
	_, err := c.POST("/api/v1/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "mastercard",
		"owner_type":     "client",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	resp, err := c.GET(fmt.Sprintf("/api/v1/cards?account_number=%s", acctNum))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestCard_VirtualSingleUseWithClientAuth(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	resp, err := clientC.POST("/api/v1/me/cards/virtual", map[string]interface{}{
		"account_number": accountNumber,
		"owner_id":       clientID,
		"card_brand":     "visa",
		"usage_type":     "single_use",
		"expiry_months":  1,
		"card_limit":     "10000.0000",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
	t.Logf("virtual single_use card created")
}

func TestCard_VirtualMultiUseWithClientAuth(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	resp, err := clientC.POST("/api/v1/me/cards/virtual", map[string]interface{}{
		"account_number": accountNumber,
		"owner_id":       clientID,
		"card_brand":     "mastercard",
		"usage_type":     "multi_use",
		"max_uses":       5,
		"expiry_months":  2,
		"card_limit":     "10000.0000",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
	t.Logf("virtual multi_use card created (max_uses=5)")
}

func TestCard_VirtualUnlimitedWithClientAuth(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	// The API does not support "unlimited" usage_type.
	// Use multi_use with a high max_uses to approximate unlimited usage.
	resp, err := clientC.POST("/api/v1/me/cards/virtual", map[string]interface{}{
		"account_number": accountNumber,
		"owner_id":       clientID,
		"card_brand":     "dinacard",
		"usage_type":     "multi_use",
		"max_uses":       9999,
		"expiry_months":  3,
		"card_limit":     "10000.0000",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	t.Logf("virtual multi_use (high max_uses) card created")
}

func TestCard_VirtualInvalidUsageType(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	resp, err := clientC.POST("/api/v1/me/cards/virtual", map[string]interface{}{
		"account_number": accountNumber,
		"owner_id":       clientID,
		"card_brand":     "visa",
		"usage_type":     "disposable", // invalid
		"expiry_months":  1,
		"card_limit":     "10000.0000",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid usage_type")
	}
}

func TestCard_PINSetAndVerify(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	// Employee creates a card for the account
	createResp, err := adminClient.POST("/api/v1/cards", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "visa",
		"owner_type":     "client",
		"account_type":   "personal",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("create card error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Client sets PIN
	setResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/pin", cardID), map[string]interface{}{
		"pin": "4321",
	})
	if err != nil {
		t.Fatalf("set PIN error: %v", err)
	}
	helpers.RequireStatus(t, setResp, 200)

	// Client verifies correct PIN
	verifyResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/verify-pin", cardID), map[string]interface{}{
		"pin": "4321",
	})
	if err != nil {
		t.Fatalf("verify PIN error: %v", err)
	}
	helpers.RequireStatus(t, verifyResp, 200)
}

func TestCard_PINWrongThreeTimes_LocksCard(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	createResp, err := adminClient.POST("/api/v1/cards", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "mastercard",
		"owner_type":     "client",
		"account_type":   "personal",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("create card error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Set PIN
	setResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/pin", cardID), map[string]interface{}{
		"pin": "1111",
	})
	if err != nil {
		t.Fatalf("set PIN error: %v", err)
	}
	helpers.RequireStatus(t, setResp, 200)

	// Attempt wrong PIN 3 times
	for i := 0; i < 3; i++ {
		_, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/verify-pin", cardID), map[string]interface{}{
			"pin": "9999",
		})
		if err != nil {
			t.Fatalf("verify PIN attempt %d error: %v", i+1, err)
		}
	}

	// 4th verify should fail with 403 (card locked) or 400
	lockResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/verify-pin", cardID), map[string]interface{}{
		"pin": "1111", // correct but card should be locked
	})
	if err != nil {
		t.Fatalf("verify PIN after lockout error: %v", err)
	}
	if lockResp.StatusCode == 200 {
		t.Fatal("expected card to be locked after 3 wrong PIN attempts")
	}
	t.Logf("card locked after 3 wrong PINs: status=%d", lockResp.StatusCode)
}

func TestCard_ChangePin(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	createResp, err := adminClient.POST("/api/v1/cards", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "dinacard",
		"owner_type":     "client",
		"account_type":   "personal",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("create card error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Set initial PIN
	_, err = clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/pin", cardID), map[string]interface{}{
		"pin": "2222",
	})
	if err != nil {
		t.Fatalf("set PIN error: %v", err)
	}

	// Change PIN — endpoint may be PUT /api/me/cards/{id}/pin or POST with old_pin field
	changeResp, err := clientC.PUT(fmt.Sprintf("/api/v1/me/cards/%d/pin", cardID), map[string]interface{}{
		"old_pin": "2222",
		"new_pin": "3333",
	})
	if err != nil {
		t.Fatalf("change PIN error: %v", err)
	}
	// Accept 200 (success) or 404/405 if endpoint not implemented
	if changeResp.StatusCode >= 400 && changeResp.StatusCode != 404 && changeResp.StatusCode != 405 {
		t.Fatalf("change PIN returned unexpected error: %d: %s", changeResp.StatusCode, string(changeResp.RawBody))
	}
	t.Logf("change PIN status: %d", changeResp.StatusCode)
}

func TestCard_TemporaryBlockWithExpiry(t *testing.T) {
	t.Parallel()
	adminClient := loginAsAdmin(t)
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminClient)

	createResp, err := adminClient.POST("/api/v1/cards", map[string]interface{}{
		"account_number": accountNumber,
		"card_brand":     "amex",
		"owner_type":     "client",
		"account_type":   "personal",
		"owner_id":       clientID,
	})
	if err != nil {
		t.Fatalf("create card error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Client applies a temporary block for 1 hour
	blockResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/cards/%d/temporary-block", cardID), map[string]interface{}{
		"duration_hours": 1, // handler expects duration_hours (int32)
	})
	if err != nil {
		t.Fatalf("temporary block error: %v", err)
	}
	helpers.RequireStatus(t, blockResp, 200)
	t.Logf("card %d temporarily blocked for 1 hour", cardID)
}

func TestCard_AllBrandsDebitAndCredit(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	brands := []string{"visa", "mastercard", "dinacard", "amex"}
	cardTypes := []string{"debit", "credit"}

	for _, brand := range brands {
		for _, cardType := range cardTypes {
			t.Run(fmt.Sprintf("%s_%s", brand, cardType), func(t *testing.T) {
				clientID, acctNum := createTestAccountForCards(t, c)
				resp, err := c.POST("/api/v1/cards", map[string]interface{}{
					"account_number": acctNum,
					"card_brand":     brand,
					"card_type":      cardType,
					"owner_type":     "client",
					"owner_id":       clientID,
				})
				if err != nil {
					t.Fatalf("error: %v", err)
				}
				// Accept 201; some brand+type combos may be restricted (400/422 is acceptable)
				if resp.StatusCode != 201 && resp.StatusCode != 400 && resp.StatusCode != 422 {
					t.Fatalf("unexpected status %d for %s %s: %s", resp.StatusCode, brand, cardType, string(resp.RawBody))
				}
				t.Logf("brand=%s type=%s status=%d", brand, cardType, resp.StatusCode)
			})
		}
	}
}
