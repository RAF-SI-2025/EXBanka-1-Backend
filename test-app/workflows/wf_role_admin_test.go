//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Granular role-permission admin API (Plan D Task 8) ---
//
// These tests exercise POST/DELETE /api/v3/roles/:id/permissions —
// the granular per-permission grant/revoke endpoints introduced alongside the
// codegened permission catalog. Validation against the catalog is enforced at
// the service layer (sentinel ErrPermissionNotInCatalog) and translated to
// gRPC InvalidArgument by the user-service handler, which surfaces as HTTP 400
// at the gateway.
//
// The path parameter is now a numeric role ID (v3 route standardization,
// 2026-04-28). Tests create throwaway roles and extract their ID from the
// create response (201 body), then use the ID in subsequent calls.
//
// Granting/revoking permissions on a default role mid-test would impact other
// parallel tests. To stay isolated, every test creates its own throwaway role
// first. Cleanup is not strictly necessary because each role uses a unique
// generated name.

// uniqueRoleName returns a role name unique to this test run.
func uniqueRoleName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, helpers.DateOfBirthUnix())
}

func TestAdmin_AssignPermissionToRole_Success(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleAssignOK")
	createResp, err := admin.POST("/api/v3/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for granular assign",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	resp, err := admin.POST(fmt.Sprintf("/api/v3/roles/%d/permissions", int(roleID)), map[string]interface{}{
		"permission": "clients.read.all",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("expected 204, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_AssignPermissionToRole_Idempotent(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleAssignIdem")
	createResp, err := admin.POST("/api/v3/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for idempotent assign",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	for i := 0; i < 2; i++ {
		resp, err := admin.POST(fmt.Sprintf("/api/v3/roles/%d/permissions", int(roleID)), map[string]interface{}{
			"permission": "accounts.read.all",
		})
		if err != nil {
			t.Fatalf("assign call %d: %v", i, err)
		}
		if resp.StatusCode != 204 {
			t.Errorf("call %d: expected 204, got %d body=%s", i, resp.StatusCode, string(resp.RawBody))
		}
	}
}

func TestAdmin_AssignPermissionToRole_NotInCatalog(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleAssignBad")
	createResp, err := admin.POST("/api/v3/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for catalog rejection",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	resp, err := admin.POST(fmt.Sprintf("/api/v3/roles/%d/permissions", int(roleID)), map[string]interface{}{
		"permission": "totally.fake.permission",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for catalog rejection, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_AssignPermissionToRole_RoleNotFound(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	// Use a very large ID that is extremely unlikely to exist.
	resp, err := admin.POST("/api/v3/roles/999999999/permissions", map[string]interface{}{
		"permission": "clients.read.all",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for missing role, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_RevokePermissionFromRole_Success(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleRevokeOK")
	createResp, err := admin.POST("/api/v3/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for revoke",
		"permission_codes": []string{"clients.read.all"},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	resp, err := admin.DELETE(fmt.Sprintf("/api/v3/roles/%d/permissions/clients.read.all", int(roleID)))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("expected 204, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_RevokePermissionFromRole_Idempotent(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleRevokeIdem")
	createResp, err := admin.POST("/api/v3/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for idempotent revoke",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	roleID := helpers.GetNumberField(t, createResp, "id")

	// Revoking a permission that was never granted should be a 204 no-op.
	resp, err := admin.DELETE(fmt.Sprintf("/api/v3/roles/%d/permissions/accounts.read.all", int(roleID)))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("expected 204 for idempotent revoke, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_RevokePermissionFromRole_RoleNotFound(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	// Use a very large ID that is extremely unlikely to exist.
	resp, err := admin.DELETE("/api/v3/roles/999999999/permissions/clients.read.all")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for missing role, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
}

func TestAdmin_AssignPermission_NoAuth(t *testing.T) {
	t.Parallel()
	c := newClient() // no token
	// Use a real-looking but non-existent role ID — auth check happens before DB lookup.
	resp, err := c.POST("/api/v3/roles/1/permissions", map[string]interface{}{
		"permission": "clients.read.all",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
