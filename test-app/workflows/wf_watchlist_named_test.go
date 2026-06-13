//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_WatchlistNamedLists verifies SP6: a client can create named
// watchlists, list them (including the lazily-created default), and the legacy
// single-list endpoint still works against the default list.
func TestWF_WatchlistNamedLists(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	// Create two named lists.
	techResp, err := clientC.POST("/api/v3/me/watchlists", map[string]interface{}{"name": "tech"})
	if err != nil {
		t.Fatalf("create tech: %v", err)
	}
	if techResp.StatusCode == 404 {
		t.Skip("named-watchlist endpoints not deployed — skipping")
	}
	helpers.RequireStatus(t, techResp, 201)
	tech := helpers.RequireField(t, techResp, "watchlist").(map[string]interface{})
	techID := int(tech["id"].(float64))
	if tech["name"] != "tech" {
		t.Fatalf("name = %v, want tech", tech["name"])
	}

	favResp, err := clientC.POST("/api/v3/me/watchlists", map[string]interface{}{"name": "favorites"})
	if err != nil {
		t.Fatalf("create favorites: %v", err)
	}
	helpers.RequireStatus(t, favResp, 201)

	// List watchlists → at least default + tech + favorites (3).
	listResp, err := clientC.GET("/api/v3/me/watchlists")
	if err != nil {
		t.Fatalf("list watchlists: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	lists, _ := helpers.RequireField(t, listResp, "watchlists").([]interface{})
	if len(lists) < 3 {
		t.Fatalf("expected >=3 watchlists (default + 2 named), got %d", len(lists))
	}

	// Legacy single-list endpoint still works (operates on the default list).
	legacy, err := clientC.GET("/api/v3/me/watchlist")
	if err != nil {
		t.Fatalf("legacy list: %v", err)
	}
	helpers.RequireStatus(t, legacy, 200)
	helpers.RequireField(t, legacy, "items")

	// Per-list items endpoint responds for the tech list.
	items, err := clientC.GET(fmt.Sprintf("/api/v3/me/watchlists/%d/items", techID))
	if err != nil {
		t.Fatalf("list tech items: %v", err)
	}
	helpers.RequireStatus(t, items, 200)
	helpers.RequireField(t, items, "items")

	// Delete the tech list.
	del, err := clientC.DELETE(fmt.Sprintf("/api/v3/me/watchlists/%d", techID))
	if err != nil {
		t.Fatalf("delete tech: %v", err)
	}
	if del.StatusCode != 204 {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
}
