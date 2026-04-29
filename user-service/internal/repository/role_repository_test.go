package repository

import (
	"sort"
	"testing"
	"time"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
)

// TestRoleRepository_ListEmployeeIDsByRole inserts two roles and three employees
// with overlapping role assignments and asserts the join-table lookup returns the
// right IDs (sorted client-side for deterministic comparison) plus a non-nil empty
// slice for an unknown role.
func TestRoleRepository_ListEmployeeIDsByRole(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{})

	roleA := model.Role{Name: "RoleA"}
	roleB := model.Role{Name: "RoleB"}
	if err := db.Create(&roleA).Error; err != nil {
		t.Fatalf("create roleA: %v", err)
	}
	if err := db.Create(&roleB).Error; err != nil {
		t.Fatalf("create roleB: %v", err)
	}

	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	emp1 := model.Employee{Email: "e1@test", FirstName: "E", LastName: "1", JMBG: "1111111111111", Username: "e1user", DateOfBirth: dob, Roles: []model.Role{roleA, roleB}}
	emp2 := model.Employee{Email: "e2@test", FirstName: "E", LastName: "2", JMBG: "2222222222222", Username: "e2user", DateOfBirth: dob, Roles: []model.Role{roleA}}
	emp3 := model.Employee{Email: "e3@test", FirstName: "E", LastName: "3", JMBG: "3333333333333", Username: "e3user", DateOfBirth: dob, Roles: []model.Role{roleB}}
	for _, e := range []*model.Employee{&emp1, &emp2, &emp3} {
		if err := db.Create(e).Error; err != nil {
			t.Fatalf("create employee: %v", err)
		}
	}

	repo := NewRoleRepository(db)

	idsA, err := repo.ListEmployeeIDsByRole(roleA.ID)
	if err != nil {
		t.Fatalf("list roleA: %v", err)
	}
	sort.Slice(idsA, func(i, j int) bool { return idsA[i] < idsA[j] })
	if len(idsA) != 2 || idsA[0] != emp1.ID || idsA[1] != emp2.ID {
		t.Errorf("roleA ids: got %v, want [%d %d]", idsA, emp1.ID, emp2.ID)
	}

	idsB, err := repo.ListEmployeeIDsByRole(roleB.ID)
	if err != nil {
		t.Fatalf("list roleB: %v", err)
	}
	sort.Slice(idsB, func(i, j int) bool { return idsB[i] < idsB[j] })
	if len(idsB) != 2 || idsB[0] != emp1.ID || idsB[1] != emp3.ID {
		t.Errorf("roleB ids: got %v, want [%d %d]", idsB, emp1.ID, emp3.ID)
	}

	emptyIDs, err := repo.ListEmployeeIDsByRole(999999)
	if err != nil {
		t.Fatalf("list missing role: %v", err)
	}
	if emptyIDs == nil || len(emptyIDs) != 0 {
		t.Errorf("missing role: got %v, want non-nil empty slice", emptyIDs)
	}
}
