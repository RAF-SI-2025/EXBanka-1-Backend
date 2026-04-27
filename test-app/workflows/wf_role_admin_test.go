//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Granular role-permission admin API (Plan D Task 8) ---
//
// These tests exercise POST/DELETE /api/v1/roles/:role_name/permissions —
// the granular per-permission grant/revoke endpoints introduced alongside the
// codegened permission catalog. Validation against the catalog is enforced at
// the service layer (sentinel ErrPermissionNotInCatalog) and translated to
// gRPC InvalidArgument by the user-service handler, which surfaces as HTTP 400
// at the gateway.
//
// Granting/revoking permissions on a default role mid-test would impact other
// parallel tests (revocation Kafka event, JWT cache invalidation). To stay
// isolated, every test creates its own throwaway role first and then targets
// that role for the assign/revoke. Cleanup is not strictly necessary because
// each role uses a unique generated name.

// uniqueRoleName returns a role name unique to this test run.
func uniqueRoleName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, helpers.DateOfBirthUnix())
}

func TestAdmin_AssignPermissionToRole_Success(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	roleName := uniqueRoleName("RoleAssignOK")
	createResp, err := admin.POST("/api/v1/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for granular assign",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)

	resp, err := admin.POST(fmt.Sprintf("/api/v1/roles/%s/permissions", roleName), map[string]interface{}{
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
	createResp, err := admin.POST("/api/v1/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for idempotent assign",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)

	for i := 0; i < 2; i++ {
		resp, err := admin.POST(fmt.Sprintf("/api/v1/roles/%s/permissions", roleName), map[string]interface{}{
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
	createResp, err := admin.POST("/api/v1/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for catalog rejection",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)

	resp, err := admin.POST(fmt.Sprintf("/api/v1/roles/%s/permissions", roleName), map[string]interface{}{
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

	resp, err := admin.POST("/api/v1/roles/NoSuchRoleEver_xyz/permissions", map[string]interface{}{
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
	createResp, err := admin.POST("/api/v1/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for revoke",
		"permission_codes": []string{"clients.read.all"},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)

	resp, err := admin.DELETE(fmt.Sprintf("/api/v1/roles/%s/permissions/clients.read.all", roleName))
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
	createResp, err := admin.POST("/api/v1/roles", map[string]interface{}{
		"name":             roleName,
		"description":      "test role for idempotent revoke",
		"permission_codes": []string{},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)

	// Revoking a permission that was never granted should be a 204 no-op.
	resp, err := admin.DELETE(fmt.Sprintf("/api/v1/roles/%s/permissions/accounts.read.all", roleName))
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

	resp, err := admin.DELETE("/api/v1/roles/NoSuchRoleEver_xyz/permissions/clients.read.all")
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
	resp, err := c.POST("/api/v1/roles/EmployeeBasic/permissions", map[string]interface{}{
		"permission": "clients.read.all",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
