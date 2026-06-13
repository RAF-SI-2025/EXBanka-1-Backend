//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// These tests target the OTC *stock* marketplace surface. Phase 8 moved it from
// the legacy /api/v3/otc/offers group to /api/v3/otc/stocks:
//   - GET  /api/v3/otc/stocks          → unified list (local + peer offers)
//   - POST /api/v3/otc/stocks/:id/buy  → buy a standing sell offer
//
// The old /api/v3/otc/offers paths now 404. (/api/v3/otc/options is the OTC
// *option* surface and is unrelated to these stock tests.)

func TestOTC_ListOffers(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/stocks")
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
	resp, err := c.GET("/api/v3/otc/stocks")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestOTC_ListOffers_FilterBySecurityType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/stocks?security_type=stock")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// TestOTC_ListOffers_InvalidSecurityType: the OTC stock list validates
// security_type against {stock, futures}; "option" is not a valid value for the
// stock surface and must be rejected with 400.
func TestOTC_ListOffers_InvalidSecurityType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/otc/stocks?security_type=option")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOTC_BuyOffer_InvalidQuantity(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/v3/otc/stocks/1/buy", map[string]interface{}{
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

	resp, err := agentC.POST("/api/v3/otc/stocks/1/buy", map[string]interface{}{
		"quantity": 5,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}
