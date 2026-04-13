//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestStockExchange_ListExchanges(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/v1/stock-exchanges")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "exchanges")
	helpers.RequireField(t, resp, "total_count")
}

func TestStockExchange_ListExchanges_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/v1/stock-exchanges")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestStockExchange_ListExchanges_SearchFilter(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/v1/stock-exchanges?search=NYSE")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestStockExchange_GetExchange(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	listResp, err := adminC.GET("/api/v1/stock-exchanges?page_size=1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	exchangesRaw, ok := listResp.Body["exchanges"].([]interface{})
	if !ok || len(exchangesRaw) == 0 {
		t.Skip("no exchanges seeded or exchanges field missing")
	}
	exchanges := exchangesRaw
	id := exchanges[0].(map[string]interface{})["id"].(float64)

	resp, err := adminC.GET("/api/v1/stock-exchanges/" + helpers.FormatID(int(id)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "name")
	helpers.RequireField(t, resp, "mic_code")
}

func TestStockExchange_GetExchange_NotFound(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	resp, err := adminC.GET("/api/v1/stock-exchanges/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 404)
}

func TestStockExchange_TestingMode_SetAndGet(t *testing.T) {
	adminC := loginAsAdmin(t)

	setResp, err := adminC.POST("/api/v1/stock-exchanges/testing-mode", map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, setResp, 200)
	helpers.RequireFieldEquals(t, setResp, "testing_mode", true)

	getResp, err := adminC.GET("/api/v1/stock-exchanges/testing-mode")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)
	helpers.RequireFieldEquals(t, getResp, "testing_mode", true)

	// Disable testing mode (cleanup)
	_, _ = adminC.POST("/api/v1/stock-exchanges/testing-mode", map[string]interface{}{
		"enabled": false,
	})
}

func TestStockExchange_TestingMode_RequiresSupervisor(t *testing.T) {
	t.Parallel()
	_, agentC, _ := setupAgentEmployee(t, loginAsAdmin(t))
	resp, err := agentC.POST("/api/v1/stock-exchanges/testing-mode", map[string]interface{}{
		"enabled": true,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}
