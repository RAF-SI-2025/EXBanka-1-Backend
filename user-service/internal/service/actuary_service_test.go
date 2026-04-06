// user-service/internal/service/actuary_service_test.go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// --- Mock ActuaryRepo ---

type mockActuaryRepo struct {
	limits  map[int64]*model.ActuaryLimit
	byEmpID map[int64]*model.ActuaryLimit
	nextID  int64
	saveErr error
}

func newMockActuaryRepo() *mockActuaryRepo {
	return &mockActuaryRepo{
		limits:  make(map[int64]*model.ActuaryLimit),
		byEmpID: make(map[int64]*model.ActuaryLimit),
		nextID:  1,
	}
}

func (m *mockActuaryRepo) Create(limit *model.ActuaryLimit) error {
	limit.ID = m.nextID
	m.nextID++
	copy := *limit
	m.limits[copy.ID] = &copy
	m.byEmpID[copy.EmployeeID] = m.limits[copy.ID]
	return nil
}

func (m *mockActuaryRepo) GetByID(id int64) (*model.ActuaryLimit, error) {
	if l, ok := m.limits[id]; ok {
		return l, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockActuaryRepo) GetByEmployeeID(employeeID int64) (*model.ActuaryLimit, error) {
	if l, ok := m.byEmpID[employeeID]; ok {
		return l, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockActuaryRepo) Save(limit *model.ActuaryLimit) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	copy := *limit
	m.limits[copy.ID] = &copy
	m.byEmpID[copy.EmployeeID] = m.limits[copy.ID]
	return nil
}

func (m *mockActuaryRepo) ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
	return nil, 0, nil
}

func (m *mockActuaryRepo) Upsert(limit *model.ActuaryLimit) error {
	if existing, ok := m.byEmpID[limit.EmployeeID]; ok {
		existing.Limit = limit.Limit
		existing.NeedApproval = limit.NeedApproval
		return nil
	}
	return m.Create(limit)
}

// --- Mock ActuaryEmpRepo ---

type mockActuaryEmpRepo struct {
	employees map[int64]*model.Employee
}

func newMockActuaryEmpRepo() *mockActuaryEmpRepo {
	return &mockActuaryEmpRepo{employees: make(map[int64]*model.Employee)}
}

func (m *mockActuaryEmpRepo) GetByIDWithRoles(id int64) (*model.Employee, error) {
	emp, ok := m.employees[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return emp, nil
}

// agentEmployee creates an employee with EmployeeAgent role.
func agentEmployee(id int64) *model.Employee {
	return &model.Employee{
		ID:        id,
		FirstName: "Agent",
		LastName:  "Smith",
		Email:     "agent@example.com",
		Roles:     []model.Role{{Name: "EmployeeAgent"}},
	}
}

// supervisorEmployee creates an employee with EmployeeSupervisor role.
func supervisorEmployee(id int64) *model.Employee {
	return &model.Employee{
		ID:        id,
		FirstName: "Super",
		LastName:  "Visor",
		Email:     "supervisor@example.com",
		Roles:     []model.Role{{Name: "EmployeeSupervisor"}},
	}
}

// --- Tests ---

// TestSetActuaryLimit_Persisted verifies that setting a limit persists the value.
func TestSetActuaryLimit_Persisted(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[10] = agentEmployee(10)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	limit, err := svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(5000), 0)
	assert.NoError(t, err)
	assert.NotNil(t, limit)
	assert.Equal(t, int64(10), limit.EmployeeID)
	assert.True(t, limit.Limit.Equal(decimal.NewFromInt(5000)), "Limit should be 5000")

	// Verify the persisted value in the repo
	persisted, err := actuaryRepo.GetByEmployeeID(10)
	assert.NoError(t, err)
	assert.True(t, persisted.Limit.Equal(decimal.NewFromInt(5000)), "Persisted limit should be 5000")
}

// TestSetActuaryLimit_NegativeRejected ensures a negative limit returns an error.
func TestSetActuaryLimit_NegativeRejected(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[10] = agentEmployee(10)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(-100), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "negative")
}

// TestGetActuaryInfo_ReturnsLimitData verifies GetActuaryInfo returns limit and employee.
func TestGetActuaryInfo_ReturnsLimitData(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	emp := agentEmployee(20)
	empRepo.employees[20] = emp

	// Pre-seed a limit row
	_ = actuaryRepo.Create(&model.ActuaryLimit{
		EmployeeID:   20,
		Limit:        decimal.NewFromInt(10000),
		UsedLimit:    decimal.NewFromInt(2500),
		NeedApproval: true,
	})

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	limit, returnedEmp, err := svc.GetActuaryInfo(20)
	assert.NoError(t, err)
	assert.NotNil(t, limit)
	assert.NotNil(t, returnedEmp)
	assert.True(t, limit.Limit.Equal(decimal.NewFromInt(10000)), "Limit should be 10000")
	assert.True(t, limit.UsedLimit.Equal(decimal.NewFromInt(2500)), "UsedLimit should be 2500")
	assert.Equal(t, int64(20), limit.EmployeeID)
	assert.Equal(t, int64(20), returnedEmp.ID)
}

// TestGetActuaryInfo_AutoCreatesMissingLimit verifies that GetActuaryInfo auto-creates a
// limit row when the employee is an actuary but has no existing limit record.
func TestGetActuaryInfo_AutoCreatesMissingLimit(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[30] = agentEmployee(30)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	limit, returnedEmp, err := svc.GetActuaryInfo(30)
	assert.NoError(t, err)
	assert.NotNil(t, limit)
	assert.NotNil(t, returnedEmp)
	assert.True(t, limit.Limit.IsZero(), "auto-created limit should be 0")
	assert.True(t, limit.UsedLimit.IsZero(), "auto-created used limit should be 0")
	// Agent (non-supervisor) should have NeedApproval = true
	assert.True(t, limit.NeedApproval, "agent should have NeedApproval=true by default")
}

// TestGetActuaryInfo_NotActuary verifies that a non-actuary employee returns an error.
func TestGetActuaryInfo_NotActuary(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[40] = &model.Employee{
		ID:    40,
		Roles: []model.Role{{Name: "EmployeeBasic"}},
	}

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, _, err := svc.GetActuaryInfo(40)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not an actuary")
}

// TestResetUsedLimit_SetsToZero verifies that ResetUsedLimit zeroes the used limit.
func TestResetUsedLimit_SetsToZero(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	emp := agentEmployee(50)
	empRepo.employees[50] = emp

	// Pre-seed a limit with non-zero used amount
	_ = actuaryRepo.Create(&model.ActuaryLimit{
		EmployeeID: 50,
		Limit:      decimal.NewFromInt(8000),
		UsedLimit:  decimal.NewFromInt(3000),
	})

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	result, err := svc.ResetUsedLimit(context.Background(), 50, 0)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.UsedLimit.IsZero(), "UsedLimit should be 0 after reset")

	// Verify persisted state
	persisted, err := actuaryRepo.GetByEmployeeID(50)
	assert.NoError(t, err)
	assert.True(t, persisted.UsedLimit.IsZero(), "Persisted UsedLimit should be 0")
}

// TestResetUsedLimit_EmployeeNotFound verifies that resetting a non-existent employee fails.
func TestResetUsedLimit_EmployeeNotFound(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	// No employee seeded

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.ResetUsedLimit(context.Background(), 999, 0)
	assert.Error(t, err)
}

// TestSupervisorLimit_NeedApprovalFalse verifies supervisors get NeedApproval=false by default.
func TestSupervisorLimit_NeedApprovalFalse(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[60] = supervisorEmployee(60)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	limit, _, err := svc.GetActuaryInfo(60)
	assert.NoError(t, err)
	assert.False(t, limit.NeedApproval, "supervisor should have NeedApproval=false by default")
}

// TestUpdateUsedLimit_Accumulates verifies UpdateUsedLimit adds to existing used limit.
func TestUpdateUsedLimit_Accumulates(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[70] = agentEmployee(70)

	_ = actuaryRepo.Create(&model.ActuaryLimit{
		EmployeeID: 70,
		Limit:      decimal.NewFromInt(10000),
		UsedLimit:  decimal.NewFromInt(1000),
	})

	// Retrieve the created limit's ID for UpdateUsedLimit
	existing, err := actuaryRepo.GetByEmployeeID(70)
	assert.NoError(t, err)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	result, err := svc.UpdateUsedLimit(context.Background(), existing.ID, decimal.NewFromInt(500))
	assert.NoError(t, err)
	assert.True(t, result.UsedLimit.Equal(decimal.NewFromInt(1500)), "UsedLimit should be 1000+500=1500")
}

// TestSetNeedApproval verifies that NeedApproval can be toggled.
func TestSetNeedApproval(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[80] = agentEmployee(80)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	// First call creates the limit row; agents default to NeedApproval=true
	result, err := svc.SetNeedApproval(context.Background(), 80, false, 0)
	assert.NoError(t, err)
	assert.False(t, result.NeedApproval)

	persisted, err := actuaryRepo.GetByEmployeeID(80)
	assert.NoError(t, err)
	assert.False(t, persisted.NeedApproval)
}

// TestSetActuaryLimit_NonActuary verifies that setting a limit for a non-actuary fails.
func TestSetActuaryLimit_NonActuary(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[90] = &model.Employee{
		ID:    90,
		Roles: []model.Role{{Name: "EmployeeBasic"}},
	}

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 90, decimal.NewFromInt(1000), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not an actuary")
}

// TestSetActuaryLimit_SaveError verifies that a repo save error is propagated.
func TestSetActuaryLimit_SaveError(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[100] = agentEmployee(100)

	// Pre-create a limit so we don't hit the Create path
	_ = actuaryRepo.Create(&model.ActuaryLimit{EmployeeID: 100})
	actuaryRepo.saveErr = errors.New("db write failed")

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 100, decimal.NewFromInt(1000), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db write failed")
}
