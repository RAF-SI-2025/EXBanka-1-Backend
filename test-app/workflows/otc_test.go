//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestOTC_ListOffers(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/offers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "offers")
	helpers.RequireField(t, resp, "total_count")
}

func TestOTC_ListOffers_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/v3/otc/offers")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestOTC_ListOffers_FilterBySecurityType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/offers?security_type=stock")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestOTC_ListOffers_InvalidSecurityType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/offers?security_type=option")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOTC_BuyOffer_InvalidQuantity(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/v3/otc/offers/1/buy", map[string]interface{}{
		"quantity":   0,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOTC_BuyOffer_MissingAccountID(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/v3/otc/offers/1/buy", map[string]interface{}{
		"quantity": 5,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}
