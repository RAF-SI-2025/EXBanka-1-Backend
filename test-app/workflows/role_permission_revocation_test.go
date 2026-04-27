//go:build integration

package workflows

import (
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth asserts that
// changing a role's permission set forces every employee currently holding the
// role to re-authenticate. We log in as an Agent (which has clients.read by
// default), confirm the agent can list clients, then strip clients.read from
// the EmployeeAgent role via PUT /api/v1/roles/<id>/permissions. The agent's
// next call to a clients.read endpoint must return 401 with the "access token
// has been revoked" message because their iat predates the revocation epoch.
//
// We restore the original permission set at the end so other tests using the
// EmployeeAgent role aren't affected.
func TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth(t *testing.T) {
	// NOT t.Parallel() — this test mutates a shared role and would race with
	// any other test that depends on the EmployeeAgent permission set.

	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	// Sanity: agent can list clients (clients.read is in EmployeeAgent default).
	preResp, err := agentC.GET("/api/v1/clients?page=1&page_size=1")
	if err != nil {
		t.Fatalf("pre-revoke list: %v", err)
	}
	helpers.RequireStatus(t, preResp, 200)

	// Find the EmployeeAgent role ID.
	rolesResp, err := adminC.GET("/api/v1/roles")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	helpers.RequireStatus(t, rolesResp, 200)
	roles, _ := rolesResp.Body["roles"].([]interface{})
	var agentRoleID int
	for _, r := range roles {
		m, _ := r.(map[string]interface{})
		if m["name"] == "EmployeeAgent" {
			agentRoleID = int(m["id"].(float64))
			break
		}
	}
	if agentRoleID == 0 {
		t.Fatal("EmployeeAgent role not found")
	}

	// The full default EmployeeAgent permission set, captured here so we can
	// restore it after the test. Keep this list in sync with
	// user-service/internal/service/role_service.go DefaultRolePermissions.
	defaultAgentPerms := []string{
		"clients.create.any", "clients.read.all", "clients.update.profile",
		"accounts.create.current", "accounts.read.all", "accounts.update.name",
		"cards.create.physical", "cards.read.all", "cards.block.any", "cards.approve.physical",
		"accounts.read.all",
		"credits.read.all", "credits.approve.cash",
		"securities.trade.any", "securities.read.holdings_all",
		"orders.place.on_behalf_client",
	}

	t.Cleanup(func() {
		// Restore default perms even if the test fails partway through.
		_, _ = adminC.PUT("/api/v1/roles/"+strconv.Itoa(agentRoleID)+"/permissions", map[string]interface{}{
			"permission_codes": defaultAgentPerms,
		})
	})

	// Strip clients.read by replacing the perm set with one that only keeps a
	// harmless permission (securities.read). The empty list could be rejected
	// or behave specially, so we keep one permission.
	updResp, err := adminC.PUT("/api/v1/roles/"+strconv.Itoa(agentRoleID)+"/permissions", map[string]interface{}{
		"permission_codes": []string{"securities.read.holdings_all"},
	})
	if err != nil {
		t.Fatalf("update perms: %v", err)
	}
	helpers.RequireStatus(t, updResp, 200)

	// Give Kafka + consumer a moment. 2s is the budget; if it's flaky, bump
	// once to 4s and stop.
	time.Sleep(2 * time.Second)

	// Now the agent's existing access token is older than the revocation
	// epoch. Their next call to a permission-gated endpoint must return 401.
	postResp, err := agentC.GET("/api/v1/clients?page=1&page_size=1")
	if err != nil {
		t.Fatalf("post-revoke list: %v", err)
	}
	if postResp.StatusCode != 401 {
		t.Errorf("expected 401 after role-perm change, got %d body=%v", postResp.StatusCode, postResp.Body)
	}
}
