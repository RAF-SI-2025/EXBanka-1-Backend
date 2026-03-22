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

	resp, err := c.POST("/api/accounts", map[string]interface{}{
		"owner_id":      clientID,
		"account_kind":  "current",
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
			_, acctNum := createTestAccountForCards(t, c)

			el := kafka.NewEventListener(cfg.KafkaBrokers)
			el.Start()
			defer el.Stop()

			resp, err := c.POST("/api/cards", map[string]interface{}{
				"account_number": acctNum,
				"card_brand":     brand,
				"owner_type":     "client",
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
	c := loginAsAdmin(t)
	_, acctNum := createTestAccountForCards(t, c)

	resp, err := c.POST("/api/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "discover", // invalid
		"owner_type":     "client",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid brand")
	}
}

func TestCard_BlockUnblockDeactivate(t *testing.T) {
	c := loginAsAdmin(t)
	_, acctNum := createTestAccountForCards(t, c)

	createResp, err := c.POST("/api/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "visa",
		"owner_type":     "client",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	// Block
	resp, err := c.PUT(fmt.Sprintf("/api/cards/%d/block", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Unblock
	resp, err = c.PUT(fmt.Sprintf("/api/cards/%d/unblock", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Deactivate
	resp, err = c.PUT(fmt.Sprintf("/api/cards/%d/deactivate", cardID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestCard_VirtualCardSingleUse(t *testing.T) {
	// Virtual card creation is a client-only route, needs client auth.
	// Test that unauthenticated access fails.
	c := newClient()
	resp, err := c.POST("/api/cards/virtual", map[string]interface{}{
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
	// PIN operations are client-only routes.
	// Test that employee tokens can't access these routes.
	c := loginAsAdmin(t) // employee token
	resp, err := c.POST("/api/cards/1/pin", map[string]interface{}{
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
	c := loginAsAdmin(t)
	_, acctNum := createTestAccountForCards(t, c)

	createResp, err := c.POST("/api/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "visa",
		"owner_type":     "client",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	cardID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := c.GET(fmt.Sprintf("/api/cards/%d", cardID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestCard_ListByAccount(t *testing.T) {
	c := loginAsAdmin(t)
	_, acctNum := createTestAccountForCards(t, c)

	// Create a card
	_, err := c.POST("/api/cards", map[string]interface{}{
		"account_number": acctNum,
		"card_brand":     "mastercard",
		"owner_type":     "client",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	resp, err := c.GET(fmt.Sprintf("/api/cards/account/%s", acctNum))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}
