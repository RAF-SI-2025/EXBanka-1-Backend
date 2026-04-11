//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestActuary_ListActuaries(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.GET("/api/actuaries")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "actuaries")
	helpers.RequireField(t, resp, "total_count")
}

func TestActuary_ListActuaries_AgentCannot(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/actuaries")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

func TestActuary_SetLimit(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	agentID, _, _ := setupAgentEmployee(t, adminC)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.PUT(fmt.Sprintf("/api/actuaries/%d/limit", agentID), map[string]interface{}{
		"limit": "200000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestActuary_SetLimit_EmptyValue(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.PUT("/api/actuaries/1/limit", map[string]interface{}{
		"limit": "",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestActuary_ResetLimit(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	agentID, _, _ := setupAgentEmployee(t, adminC)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.POST(fmt.Sprintf("/api/actuaries/%d/reset-limit", agentID), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestActuary_SetNeedApproval(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	agentID, _, _ := setupAgentEmployee(t, adminC)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.PUT(fmt.Sprintf("/api/actuaries/%d/approval", agentID), map[string]interface{}{
		"need_approval": true,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestActuary_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/actuaries")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}
