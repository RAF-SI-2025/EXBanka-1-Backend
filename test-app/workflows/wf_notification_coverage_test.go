//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_NotificationCoverage_LimitChange verifies SP5 D1: when an employee
// changes a client's limits, the client receives a LIMIT_CHANGED in-app
// notification. The publish→Kafka→consumer→DB hop is async, so the client's
// notification feed is polled.
func TestWF_NotificationCoverage_LimitChange(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)

	setResp, err := adminC.PUT(fmt.Sprintf("/api/v3/clients/%d/limits", clientID), map[string]interface{}{
		"daily_limit":    "100000.00",
		"monthly_limit":  "500000.00",
		"transfer_limit": "50000.00",
	})
	if err != nil {
		t.Fatalf("set limits: %v", err)
	}
	if setResp.StatusCode == 404 {
		t.Skip("client limits endpoint not deployed — skipping")
	}
	helpers.RequireStatus(t, setResp, 200)

	deadline := time.Now().Add(20 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		notifs, err := clientC.GET("/api/v3/me/notifications")
		if err != nil {
			t.Fatalf("get notifications: %v", err)
		}
		helpers.RequireStatus(t, notifs, 200)
		list, _ := helpers.RequireField(t, notifs, "notifications").([]interface{})
		for _, raw := range list {
			n, _ := raw.(map[string]interface{})
			if n["type"] == "LIMIT_CHANGED" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		t.Fatalf("client %d did not receive a LIMIT_CHANGED notification within timeout", clientID)
	}
}

// TestWF_NotificationCoverage_MobileActivationRequested verifies that requesting a
// mobile activation code creates a persistent in-app notification (polled by both
// web and mobile via GET /api/v3/me/notifications) in addition to the email — so
// the code is not email-only. The auth→Kafka→notification-consumer→DB hop is async,
// so the client's notification feed is polled.
func TestWF_NotificationCoverage_MobileActivationRequested(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, email := setupActivatedClient(t, adminC)

	// Request a mobile activation code for the client's own account.
	c := newClient()
	reqResp, err := c.POST("/api/v3/mobile/auth/request-activation", map[string]interface{}{
		"email": email,
	})
	if err != nil {
		t.Fatalf("request activation: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 200)

	deadline := time.Now().Add(20 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		notifs, err := clientC.GET("/api/v3/me/notifications")
		if err != nil {
			t.Fatalf("get notifications: %v", err)
		}
		helpers.RequireStatus(t, notifs, 200)
		list, _ := helpers.RequireField(t, notifs, "notifications").([]interface{})
		for _, raw := range list {
			n, _ := raw.(map[string]interface{})
			if n["type"] == "MOBILE_ACTIVATION_REQUESTED" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		t.Fatalf("client did not receive a MOBILE_ACTIVATION_REQUESTED notification within timeout")
	}
}
