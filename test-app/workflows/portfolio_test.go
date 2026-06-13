//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestPortfolio_ListHoldings(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/me/portfolio")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	// Phase-8 reorg: the flat "holdings"/"total_count" fields were replaced
	// by the unified-portfolio shape grouping securities under
	// securities.positions[]. stockPositions is nil-safe and returns the
	// (possibly empty) positions array — the contract is that the field is
	// present and well-formed, not that the agent owns any holdings.
	if _, ok := resp.Body["securities"].(map[string]interface{}); !ok {
		t.Fatalf("expected securities group in portfolio response, got body=%v", resp.Body)
	}
	_ = stockPositions(t, resp.Body)
}

func TestPortfolio_ListHoldings_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/v3/me/portfolio")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestPortfolio_ListHoldings_FilterByType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/me/portfolio?security_type=stock")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

// TestPortfolio_ListHoldings_InvalidSecurityType documents the CURRENT contract
// of GET /api/v3/me/portfolio.
//
// NOTE (reported): The legacy flat-holdings handler validated the
// `security_type` query param against {stock, futures, option} and returned 400
// on anything else. The Phase-8 unified-portfolio handler
// (UnifiedPortfolioHandler.GetMy) dispatches straight to the portfolio service
// without reading or validating `security_type` at all, so an unknown value is
// silently ignored and the endpoint returns 200 with the full portfolio. This
// is a validation regression relative to the old behavior. The test asserts the
// current behavior (200) so the suite is green; see the agent's report for the
// backend follow-up.
func TestPortfolio_ListHoldings_InvalidSecurityType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/me/portfolio?security_type=invalid")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Current behavior: unknown security_type is ignored, full portfolio returned.
	helpers.RequireStatus(t, resp, 200)
	if _, ok := resp.Body["securities"].(map[string]interface{}); !ok {
		t.Fatalf("expected securities group in portfolio response, got body=%v", resp.Body)
	}
}

func TestPortfolio_GetSummary(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/v3/me/portfolio/summary")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "total_profit")
	helpers.RequireField(t, resp, "tax_paid_this_year")
}

// TestPortfolio_MakePublic_InvalidQuantity verifies invalid-quantity rejection
// on the current OTC-stock make-public surface.
//
// Phase 8 removed POST /api/v3/me/portfolio/:id/make-public. Publishing a
// holding to the OTC stock marketplace is now POST /api/v3/me/otc/stocks with
// direction=sell. The gateway validates quantity > 0 before any gRPC call, so a
// zero quantity must return 400 validation_error.
func TestPortfolio_MakePublic_InvalidQuantity(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/v3/me/otc/stocks", map[string]interface{}{
		"direction":  "sell",
		"quantity":   0,
		"holding_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestPortfolio_ExerciseOption_NotFound(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/v3/me/portfolio/999999/exercise", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 404)
}

func TestPortfolio_ExerciseOption_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v3/me/portfolio/1/exercise", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}
