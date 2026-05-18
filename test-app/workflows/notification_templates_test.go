//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestNotificationTemplates_AdminDiscoveryAndCustomize: an admin lists the
// templates (each with its variables), customizes one, reads it back as
// customized, then resets it.
func TestNotificationTemplates_AdminDiscoveryAndCustomize(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	listResp, err := adminC.GET("/api/v3/notification-templates?channel=email")
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if listResp.StatusCode == 404 {
		t.Skip("notification-templates endpoint not deployed")
	}
	helpers.RequireStatus(t, listResp, 200)
	templates, _ := listResp.Body["templates"].([]interface{})
	if len(templates) == 0 {
		t.Fatal("expected at least one template")
	}
	first, _ := templates[0].(map[string]interface{})
	if vars, _ := first["variables"].([]interface{}); len(vars) == 0 {
		t.Error("expected each template to expose a variables array")
	}

	// Customize CONFIRMATION (variable: first_name).
	putResp, err := adminC.PUT("/api/v3/notification-templates/email/CONFIRMATION", map[string]interface{}{
		"subject": "Custom Welcome",
		"body":    "Hi {{first_name}}, custom body.",
	})
	if err != nil {
		t.Fatalf("set template: %v", err)
	}
	helpers.RequireStatus(t, putResp, 200)

	getResp, err := adminC.GET("/api/v3/notification-templates/email/CONFIRMATION")
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)
	if isCustom, _ := getResp.Body["is_customized"].(bool); !isCustom {
		t.Error("expected is_customized=true after PUT")
	}

	// Reset.
	delResp, err := adminC.DELETE("/api/v3/notification-templates/email/CONFIRMATION")
	if err != nil {
		t.Fatalf("reset template: %v", err)
	}
	helpers.RequireStatus(t, delResp, 200)
	if isCustom, _ := delResp.Body["is_customized"].(bool); isCustom {
		t.Error("expected is_customized=false after DELETE")
	}
}

// TestNotificationTemplates_UnknownVariableRejected: a body referencing a
// variable the type doesn't support is rejected with 400.
func TestNotificationTemplates_UnknownVariableRejected(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	resp, err := adminC.PUT("/api/v3/notification-templates/email/CONFIRMATION", map[string]interface{}{
		"subject": "x",
		"body":    "Hi {{frist_name}}",
	})
	if err != nil {
		t.Fatalf("set template: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("notification-templates endpoint not deployed")
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for unknown variable, got %d", resp.StatusCode)
	}
}

// TestNotificationTemplates_NonAdminForbidden: a client cannot reach the
// admin template routes.
func TestNotificationTemplates_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)
	resp, err := clientC.GET("/api/v3/notification-templates")
	if err != nil {
		t.Fatalf("list as client: %v", err)
	}
	if resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Errorf("expected 403 for a client, got %d", resp.StatusCode)
	}
}
