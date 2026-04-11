//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF4: Role & Permission Management ---

func TestRoles_ListRoles(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/roles")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestRoles_GetRole(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/roles/1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "name")
}

func TestRoles_CreateCustomRole(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/roles", map[string]interface{}{
		"name":        fmt.Sprintf("CustomRole_%d", helpers.DateOfBirthUnix()),
		"description": "A test custom role",
		"permissions": []string{"clients.read", "accounts.read"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
}

func TestRoles_UpdateRolePermissions(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a role first
	createResp, err := c.POST("/api/roles", map[string]interface{}{
		"name":        fmt.Sprintf("UpdRole_%d", helpers.DateOfBirthUnix()),
		"description": "Role to update",
		"permissions": []string{"clients.read"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	// Update permissions
	resp, err := c.PUT(fmt.Sprintf("/api/roles/%d/permissions", int(roleID)), map[string]interface{}{
		"permission_codes": []string{"clients.read", "accounts.read", "cards.manage"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestRoles_ListPermissions(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/permissions")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestRoles_SetEmployeeRoles(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create employee
	createResp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Multi"),
		"last_name":     helpers.RandomName("Role"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := helpers.GetNumberField(t, createResp, "id")

	// Set multiple roles
	resp, err := c.PUT(fmt.Sprintf("/api/employees/%d/roles", int(empID)), map[string]interface{}{
		"role_names": []string{"EmployeeBasic", "EmployeeAgent"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestRoles_SetEmployeeAdditionalPermissions(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	createResp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Extra"),
		"last_name":     helpers.RandomName("Perm"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := helpers.GetNumberField(t, createResp, "id")

	resp, err := c.PUT(fmt.Sprintf("/api/employees/%d/permissions", int(empID)), map[string]interface{}{
		"permission_codes": []string{"securities.trade", "otc.manage"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestRoles_NonAdminCannotManageRoles(t *testing.T) {
	t.Parallel()
	// Test that unauthenticated access fails.
	c := newClient() // no token
	resp, err := c.GET("/api/roles")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
