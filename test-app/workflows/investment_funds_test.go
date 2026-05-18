//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestInvestmentFunds_CreateAndList covers the happy-path:
//   - admin (has funds.manage) creates a fund
//   - admin lists funds → newly-created fund appears
//   - admin GETs the fund detail
//
// Requires the api-gateway and stock-service to be running with the v3
// surface (Celina 4 / investment-funds). On a stock-service that hasn't
// landed those endpoints yet the create call returns 404; we t.Skip in
// that case so the test isn't a CI false-positive.
func TestInvestmentFunds_CreateAndList(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	name := fmt.Sprintf("Alpha-%d", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"name":                     name,
		"description":              "integration-test fund",
		"minimum_contribution_rsd": "0",
	}
	resp, err := adminC.POST("/api/v3/investment-funds", createBody)
	if err != nil {
		t.Fatalf("create fund: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skipf("v3 investment-funds endpoints not deployed (got 404) — skipping")
	}
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d body=%v", resp.StatusCode, resp.Body)
	}
	fund, ok := resp.Body["fund"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing 'fund' object: %v", resp.Body)
	}
	fundID := int(fund["id"].(float64))
	if fund["name"] != name {
		t.Errorf("fund.name: got %v want %s", fund["name"], name)
	}
	if fund["active"] != true {
		t.Errorf("fund.active: got %v want true", fund["active"])
	}

	// List funds and check the new one is there.
	listResp, err := adminC.GET("/api/v3/investment-funds?search=" + name)
	if err != nil {
		t.Fatalf("list funds: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	funds, _ := listResp.Body["funds"].([]interface{})
	found := false
	for _, f := range funds {
		fm, _ := f.(map[string]interface{})
		if int(fm["id"].(float64)) == fundID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created fund %d not in list response", fundID)
	}

	// GET fund detail.
	detailResp, err := adminC.GET(fmt.Sprintf("/api/v3/investment-funds/%d", fundID))
	if err != nil {
		t.Fatalf("get fund: %v", err)
	}
	helpers.RequireStatus(t, detailResp, 200)
	detailFund, _ := detailResp.Body["fund"].(map[string]interface{})
	if int(detailFund["id"].(float64)) != fundID {
		t.Errorf("detail.fund.id mismatch: got %v want %d", detailFund["id"], fundID)
	}
}

// TestInvestmentFunds_DuplicateNameRejected asserts the name-uniqueness
// rule on FundRepository.assertNameAvailable surfaces as 409 conflict at
// the gateway.
func TestInvestmentFunds_DuplicateNameRejected(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	name := fmt.Sprintf("Beta-%d", time.Now().UnixNano())
	body := map[string]interface{}{"name": name, "description": ""}

	first, err := adminC.POST("/api/v3/investment-funds", body)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.StatusCode == 404 {
		t.Skip("v3 endpoints not deployed")
	}
	if first.StatusCode != 201 {
		t.Fatalf("first create expected 201, got %d", first.StatusCode)
	}
	second, err := adminC.POST("/api/v3/investment-funds", body)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if second.StatusCode != 409 {
		t.Errorf("expected 409 conflict on duplicate name, got %d body=%v", second.StatusCode, second.Body)
	}
}

// TestInvestmentFunds_ClientCannotCreate asserts the funds.manage RBAC gate.
// Clients hit the same /api/v3/investment-funds POST and get 403.
func TestInvestmentFunds_ClientCannotCreate(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.POST("/api/v3/investment-funds", map[string]interface{}{
		"name":        fmt.Sprintf("Gamma-%d", time.Now().UnixNano()),
		"description": "",
	})
	if err != nil {
		t.Fatalf("client create: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 endpoints not deployed")
	}
	// Either 401 (client auth not accepted on this protected route) or 403
	// (role lacks funds.manage) is acceptable. We reject anything that lets
	// the client through.
	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		t.Errorf("client should not be able to create fund, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// TestInvestmentFunds_MyPositionsEmptyForFreshClient asserts the
// /api/v3/me/investment-funds endpoint returns an empty positions list for
// a brand-new client who has not invested in any fund yet.
func TestInvestmentFunds_MyPositionsEmptyForFreshClient(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/v3/me/investment-funds")
	if err != nil {
		t.Fatalf("list my positions: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("v3 endpoints not deployed")
	}
	helpers.RequireStatus(t, resp, 200)
	// positions may be nil (no key) or [] — both are acceptable empty.
	if rawPositions, ok := resp.Body["positions"]; ok && rawPositions != nil {
		positions, _ := rawPositions.([]interface{})
		if len(positions) != 0 {
			t.Errorf("expected empty positions for new client, got %d", len(positions))
		}
	}
}
