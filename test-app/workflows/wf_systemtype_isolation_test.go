//go:build integration
// +build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_OrderAccessIsolatedBySystemType is a regression guard for the
// cross-system-type authorization bug (see docs/Bugs.txt #5).
//
// Before the fix, stock-service's Order/Holding/CapitalGain rows were
// filtered by user_id alone. Because employees (user_db) and clients
// (client_db) have independent auto-increment PKs, an employee with
// id=N and a client with id=N routinely coexist. A client calling
// /api/v1/me/orders with user_id=N therefore saw every order ever
// placed by any employee who happened to share that numeric id.
//
// After the fix every user-scoped stock-service call also carries
// system_type, and the repositories AND it into the WHERE clause.
// This test exercises that boundary end-to-end:
//
//   - An employee (agent) places a buy order.
//   - A client (different system_type) logs in and calls /me/orders:
//     the employee's order must NOT appear.
//   - The client calls GET /me/orders/{employee_order_id}: must return
//     404 (not 403 — we don't want to leak row existence across
//     system_types).
//   - The client calls POST /me/orders/{employee_order_id}/cancel:
//     must return 404 for the same reason.
//
// Note on colliding IDs: the seeded admin employee is typically id=1
// and clients start at id=1 in their own DB, so "collision" is the
// common case, not a contrived one. We don't pin specific numeric ids
// here — the test only depends on ownership filtering being correct
// per system_type. The fixtures produce whatever ids they produce;
// the invariant is that an employee's order is never visible to a
// client through the /me/* surface.
func TestWF_OrderAccessIsolatedBySystemType(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Employee (EmployeeAgent) places a buy order.
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, listingID := getFirstStockListingID(t, agentC)

	// Employees need an account_id — use a bank account (any).
	bankAcctResp, err := adminC.GET("/api/v1/bank-accounts")
	if err != nil {
		t.Fatalf("get bank accounts: %v", err)
	}
	helpers.RequireStatus(t, bankAcctResp, 200)
	accts, ok := bankAcctResp.Body["accounts"].([]interface{})
	if !ok || len(accts) == 0 {
		t.Fatal("expected at least one bank account")
	}
	bankAcctID := uint64(accts[0].(map[string]interface{})["id"].(float64))

	createResp, err := agentC.POST("/api/v1/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
		"account_id":  bankAcctID,
	})
	if err != nil {
		t.Fatalf("agent create order: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	employeeOrderID := int(helpers.GetNumberField(t, createResp, "id"))
	t.Logf("employee order created id=%d", employeeOrderID)

	// Client logs in fresh (different system_type = "client").
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	// 1) GET /api/v1/me/orders — the client must NOT see the
	//    employee's order in their listing.
	listResp, err := clientC.GET("/api/v1/me/orders")
	if err != nil {
		t.Fatalf("client list /me/orders: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)

	// total_count should exclude the employee's order. A freshly
	// created client has no orders of their own, so total_count=0
	// is the expected value. If system_type filtering is broken the
	// count will include the employee's order (and the orders array
	// will contain it).
	if total, ok := listResp.Body["total_count"].(float64); ok {
		if total != 0 {
			t.Errorf("client /me/orders total_count=%v, expected 0 (employee's order must not leak across system_type)", total)
		}
	} else {
		t.Errorf("client /me/orders response missing total_count. body=%v", listResp.Body)
	}

	if orders, ok := listResp.Body["orders"].([]interface{}); ok {
		for _, o := range orders {
			m, ok := o.(map[string]interface{})
			if !ok {
				continue
			}
			if idVal, ok := m["id"].(float64); ok && int(idVal) == employeeOrderID {
				t.Errorf("client /me/orders leaked employee order id=%d", employeeOrderID)
			}
		}
	}

	// 2) GET /api/v1/me/orders/{employee_order_id} — must be 404,
	//    NOT 403. Returning 403 would leak the existence of a row
	//    owned by a different system_type; the fix deliberately
	//    maps cross-system accesses to NotFound.
	getResp, err := clientC.GET(fmt.Sprintf("/api/v1/me/orders/%d", employeeOrderID))
	if err != nil {
		t.Fatalf("client GET /me/orders/%d: %v", employeeOrderID, err)
	}
	if getResp.StatusCode != 404 {
		t.Errorf("client GET /me/orders/%d: expected 404, got %d (body=%v)",
			employeeOrderID, getResp.StatusCode, getResp.Body)
	}

	// 3) POST /api/v1/me/orders/{employee_order_id}/cancel — must
	//    also be 404 for the same reason.
	cancelResp, err := clientC.POST(fmt.Sprintf("/api/v1/me/orders/%d/cancel", employeeOrderID), nil)
	if err != nil {
		t.Fatalf("client POST /me/orders/%d/cancel: %v", employeeOrderID, err)
	}
	if cancelResp.StatusCode != 404 {
		t.Errorf("client POST /me/orders/%d/cancel: expected 404, got %d (body=%v)",
			employeeOrderID, cancelResp.StatusCode, cancelResp.Body)
	}

	t.Logf("PASS — system_type isolation holds across list + get + cancel")
}
