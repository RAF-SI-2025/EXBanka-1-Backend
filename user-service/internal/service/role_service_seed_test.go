// user-service/internal/service/role_service_seed_test.go
package service_test

import (
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
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
