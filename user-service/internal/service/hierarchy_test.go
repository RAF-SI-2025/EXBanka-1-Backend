package service

import (
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// --- mockHierarchyEmpRepo ---

type mockHierarchyEmpRepo struct {
	employees map[int64]*model.Employee
}

func newMockHierarchyEmpRepo() *mockHierarchyEmpRepo {
	return &mockHierarchyEmpRepo{employees: make(map[int64]*model.Employee)}
}

func (m *mockHierarchyEmpRepo) GetByIDWithRoles(id int64) (*model.Employee, error) {
	emp, ok := m.employees[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return emp, nil
}

// --- maxRoleRank tests ---

func TestMaxRoleRank_SingleRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    []model.Role
		expected int
	}{
		{"basic", []model.Role{{Name: "EmployeeBasic"}}, 1},
		{"agent", []model.Role{{Name: "EmployeeAgent"}}, 2},
		{"supervisor", []model.Role{{Name: "EmployeeSupervisor"}}, 3},
		{"admin", []model.Role{{Name: "EmployeeAdmin"}}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, maxRoleRank(tt.roles))
		})
	}
}

func TestMaxRoleRank_MultipleRoles(t *testing.T) {
	roles := []model.Role{
		{Name: "EmployeeBasic"},
		{Name: "EmployeeSupervisor"},
		{Name: "EmployeeAgent"},
	}
	assert.Equal(t, 3, maxRoleRank(roles))
}

func TestMaxRoleRank_NoRoles(t *testing.T) {
	assert.Equal(t, 0, maxRoleRank(nil))
	assert.Equal(t, 0, maxRoleRank([]model.Role{}))
}

func TestMaxRoleRank_UnknownRole(t *testing.T) {
	roles := []model.Role{{Name: "UnknownRole"}}
	assert.Equal(t, 0, maxRoleRank(roles))
}

// --- checkHierarchy tests ---

func TestCheckHierarchy_AdminCanManageSupervisor(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAdmin"}}}
	repo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	err := checkHierarchy(repo, 1, 2)
	assert.NoError(t, err)
}

func TestCheckHierarchy_SupervisorCanManageAgent(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}
	repo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeAgent"}}}

	err := checkHierarchy(repo, 1, 2)
	assert.NoError(t, err)
}

func TestCheckHierarchy_AgentCannotManageSupervisor(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	repo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	err := checkHierarchy(repo, 1, 2)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "insufficient rank")
}

func TestCheckHierarchy_EqualRankBlocked(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}
	repo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	err := checkHierarchy(repo, 1, 2)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "insufficient rank")
}

func TestCheckHierarchy_SelfModificationBlocked(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAdmin"}}}

	err := checkHierarchy(repo, 1, 1)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "cannot modify own limits")
}

func TestCheckHierarchy_ZeroCallerAllowed(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	// callerID=0 means system-initiated, should pass
	err := checkHierarchy(repo, 0, 5)
	assert.NoError(t, err)
}

func TestCheckHierarchy_CallerNotFound(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeBasic"}}}

	err := checkHierarchy(repo, 999, 2)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestCheckHierarchy_TargetNotFound(t *testing.T) {
	repo := newMockHierarchyEmpRepo()
	repo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAdmin"}}}

	err := checkHierarchy(repo, 1, 999)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
