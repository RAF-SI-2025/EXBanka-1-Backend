// user-service/internal/service/role_service_seed_test.go
package service_test

import (
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
	"gorm.io/gorm"
)

// TestSeedRolesAndPermissions_OnlyOnEmptyTable asserts the slim seed only
// inserts default role-permission mappings on a truly fresh DB (no roles
// in the roles table). Once any role exists — implying the admin or a
// previous startup has set things up — re-running the seed must be a no-op
// so that runtime grants/revokes survive restarts.
func TestSeedRolesAndPermissions_OnlyOnEmptyTable(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Permission{}, &model.Role{}, &model.Employee{})

	roleRepo := repository.NewRoleRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	svc := service.NewRoleService(roleRepo, permRepo).WithDB(db)

	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	// At least one role with permissions must exist after the first seed.
	var firstCount int64
	db.Table("role_permissions").Count(&firstCount)
	if firstCount == 0 {
		t.Fatal("expected seeded role_permissions rows")
	}

	// Simulate an admin grant — add an extra row to a seeded role.
	var role model.Role
	if err := db.Where("name = ?", "EmployeeBasic").First(&role).Error; err != nil {
		t.Fatalf("load EmployeeBasic: %v", err)
	}
	extra := model.Permission{Code: "extra.thing.any", Description: "extra", Category: "test"}
	if err := db.Create(&extra).Error; err != nil {
		t.Fatalf("create extra perm: %v", err)
	}
	if err := db.Exec("INSERT INTO role_permissions (role_id, permission_id) VALUES (?, ?)", role.ID, extra.ID).Error; err != nil {
		t.Fatalf("insert extra grant: %v", err)
	}

	var afterAdminCount int64
	db.Table("role_permissions").Count(&afterAdminCount)
	if afterAdminCount != firstCount+1 {
		t.Fatalf("expected admin row to land: got %d, want %d", afterAdminCount, firstCount+1)
	}

	// Second seed call must NOT touch the table.
	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var finalCount int64
	db.Table("role_permissions").Count(&finalCount)
	if finalCount != afterAdminCount {
		t.Errorf("seed re-ran on non-empty table: count=%d, expected=%d", finalCount, afterAdminCount)
	}
}

// loadAgentPermCodes returns the set of permission codes currently granted to
// the EmployeeAgent role in db.
func loadAgentPermCodes(t *testing.T, db *gorm.DB) map[string]bool {
	t.Helper()
	var role model.Role
	if err := db.Preload("Permissions").Where("name = ?", "EmployeeAgent").First(&role).Error; err != nil {
		t.Fatalf("load EmployeeAgent: %v", err)
	}
	codes := make(map[string]bool, len(role.Permissions))
	for _, p := range role.Permissions {
		codes[p.Code] = true
	}
	return codes
}

// TestSeedRolesAndPermissions_EmployeeAgentGetsOTCPerms_FreshDB verifies that a
// clean seed grants the EmployeeAgent role both OTC permissions it needs for
// full OTC trading (otc.read.all, otc.trade.expire) alongside its pre-existing
// grants.
func TestSeedRolesAndPermissions_EmployeeAgentGetsOTCPerms_FreshDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Permission{}, &model.Role{}, &model.Employee{})

	roleRepo := repository.NewRoleRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	svc := service.NewRoleService(roleRepo, permRepo).WithDB(db)

	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	codes := loadAgentPermCodes(t, db)
	for _, want := range []string{
		"otc.read.all",
		"otc.trade.expire",
		"otc.trade.accept",     // pre-existing
		"securities.trade.any", // pre-existing
	} {
		if !codes[want] {
			t.Errorf("fresh seed: EmployeeAgent missing %q", want)
		}
	}
}

// TestSeedRolesAndPermissions_BackfillsAgentOTCPerms_ExistingDB simulates an
// already-deployed DB whose one-time role seeding ran before the EmployeeAgent
// OTC grants existed: the EmployeeAgent role already has some grants (so the
// mapping seed is skipped) but is missing otc.read.all/otc.trade.expire, and a
// human admin has added an extra permission. Re-running the seed must backfill
// the two OTC grants WITHOUT removing the admin's customization or any existing
// grant (additive-only).
func TestSeedRolesAndPermissions_BackfillsAgentOTCPerms_ExistingDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Permission{}, &model.Role{}, &model.Employee{})

	roleRepo := repository.NewRoleRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	svc := service.NewRoleService(roleRepo, permRepo).WithDB(db)

	// Old-deployment state: EmployeeAgent with a subset of its grants, plus a
	// non-catalog permission an admin added at runtime — but WITHOUT the new
	// OTC grants.
	existing := []model.Permission{
		{Code: "securities.trade.any", Description: "x", Category: "securities"},
		{Code: "otc.trade.accept", Description: "x", Category: "otc"},
		{Code: "custom.admin.grant", Description: "admin customization", Category: "custom"},
	}
	for i := range existing {
		if err := db.Create(&existing[i]).Error; err != nil {
			t.Fatalf("create perm %s: %v", existing[i].Code, err)
		}
	}
	agent := model.Role{Name: "EmployeeAgent", Description: "EmployeeAgent default role"}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatalf("create EmployeeAgent: %v", err)
	}
	if err := db.Model(&agent).Association("Permissions").Replace(existing); err != nil {
		t.Fatalf("attach existing perms: %v", err)
	}

	// Sanity: role_permissions has rows, so the one-time mapping seed is skipped
	// and only the additive backfill can change the EmployeeAgent grants.
	var rp int64
	db.Table("role_permissions").Count(&rp)
	if rp == 0 {
		t.Fatal("expected pre-existing role_permissions rows")
	}

	if err := svc.SeedRolesAndPermissions(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	codes := loadAgentPermCodes(t, db)
	// New OTC grants backfilled.
	for _, want := range []string{"otc.read.all", "otc.trade.expire"} {
		if !codes[want] {
			t.Errorf("backfill: EmployeeAgent missing %q", want)
		}
	}
	// Additive-only: every pre-existing grant (incl. the admin customization)
	// survives.
	for _, keep := range []string{"securities.trade.any", "otc.trade.accept", "custom.admin.grant"} {
		if !codes[keep] {
			t.Errorf("backfill removed pre-existing grant %q", keep)
		}
	}
}
