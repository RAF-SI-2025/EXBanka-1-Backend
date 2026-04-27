// user-service/internal/service/employee_service_test.go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid password", "Abcdef12", false},
		{"valid complex", "MyP@ssw0rd99", false},
		{"too short", "Ab1234", true},
		{"too long", "Abcdefghijklmnopqrstuvwxyz1234567", true},
		{"no uppercase", "abcdef12", true},
		{"no lowercase", "ABCDEF12", true},
		{"only one digit", "Abcdefg1", true},
		{"no digits", "Abcdefgh", true},
		{"empty", "", true},
		{"exactly 8 chars valid", "Abcdef12", false},
		{"exactly 32 chars valid", "Abcdefghijklmnopqrstuvwxyz123456", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "TestPass12", hash)

	// Different calls produce different hashes (bcrypt salt)
	hash2, err := HashPassword("TestPass12")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, hash2)
}

// mockRepo implements EmployeeRepo for testing
type mockRepo struct {
	employees map[int64]*model.Employee
	nextID    int64
	createErr error
}

func newMockRepo() *mockRepo {
	return &mockRepo{employees: make(map[int64]*model.Employee), nextID: 1}
}

func (m *mockRepo) Create(emp *model.Employee) error {
	if m.createErr != nil {
		return m.createErr
	}
	// Enforce email uniqueness
	for _, existing := range m.employees {
		if existing.Email == emp.Email {
			return errors.New("duplicate email")
		}
	}
	emp.ID = m.nextID
	m.nextID++
	m.employees[emp.ID] = emp
	return nil
}

func (m *mockRepo) GetByID(id int64) (*model.Employee, error) {
	emp, ok := m.employees[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return emp, nil
}

func (m *mockRepo) GetByIDWithRoles(id int64) (*model.Employee, error) {
	return m.GetByID(id)
}

func (m *mockRepo) GetByIDs(ids []int64) ([]model.Employee, error) {
	out := make([]model.Employee, 0, len(ids))
	for _, id := range ids {
		if emp, ok := m.employees[id]; ok {
			out = append(out, *emp)
		}
	}
	return out, nil
}

func (m *mockRepo) GetByEmail(email string) (*model.Employee, error) {
	for _, emp := range m.employees {
		if emp.Email == email {
			return emp, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockRepo) GetByEmailWithRoles(email string) (*model.Employee, error) {
	return m.GetByEmail(email)
}

func (m *mockRepo) GetByJMBG(jmbg string) (*model.Employee, error) {
	for _, emp := range m.employees {
		if emp.JMBG == jmbg {
			return emp, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockRepo) Update(emp *model.Employee) error {
	m.employees[emp.ID] = emp
	return nil
}

func (m *mockRepo) List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	var result []model.Employee
	for _, emp := range m.employees {
		result = append(result, *emp)
	}
	return result, int64(len(result)), nil
}

func (m *mockRepo) SetEmployeeRoles(employeeID int64, roles []model.Role) error {
	emp, ok := m.employees[employeeID]
	if !ok {
		return errors.New("not found")
	}
	emp.Roles = roles
	return nil
}

func (m *mockRepo) SetAdditionalPermissions(employeeID int64, perms []model.Permission) error {
	emp, ok := m.employees[employeeID]
	if !ok {
		return errors.New("not found")
	}
	emp.AdditionalPermissions = perms
	return nil
}

func TestCreateEmployee_Valid(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp := &model.Employee{
		FirstName: "John",
		LastName:  "Doe",
		Email:     "john@example.com",
		Username:  "johndoe",
		JMBG:      "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), emp.ID)

	// Verify all fields were persisted in the repo
	persisted, err := repo.GetByID(1)
	assert.NoError(t, err)
	assert.Equal(t, "John", persisted.FirstName, "FirstName should be persisted")
	assert.Equal(t, "Doe", persisted.LastName, "LastName should be persisted")
	assert.Equal(t, "john@example.com", persisted.Email, "Email should be persisted")
	assert.Equal(t, "johndoe", persisted.Username, "Username should be persisted")
	assert.Equal(t, "0101990710024", persisted.JMBG, "JMBG should be persisted")
}

func TestCreateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp := &model.Employee{
		JMBG: "123",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestGetEmployee(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, FirstName: "Jane", Email: "jane@example.com"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp, err := svc.GetEmployee(1)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), emp.ID, "ID should match")
	assert.Equal(t, "Jane", emp.FirstName, "FirstName should match")
	assert.Equal(t, "jane@example.com", emp.Email, "Email should match")
}

func TestGetEmployee_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.GetEmployee(999)
	assert.Error(t, err)
}

func TestGetEmployeeByEmail_Found(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Email: "alice@example.com", FirstName: "Alice"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp, err := svc.GetEmployeeByEmail("alice@example.com")
	assert.NoError(t, err)
	assert.Equal(t, "Alice", emp.FirstName)
}

func TestGetEmployeeByEmail_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.GetEmployeeByEmail("missing@example.com")
	assert.Error(t, err)
}

func TestListEmployees_ReturnsAll(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Email: "a@x.com"}
	repo.employees[2] = &model.Employee{ID: 2, Email: "b@x.com"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	emps, total, err := svc.ListEmployees("", "", "", 1, 10)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, emps, 2)
}

func TestUpdateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, JMBG: "0101990710024"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.UpdateEmployee(context.Background(), 1, map[string]interface{}{"jmbg": "bad"}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestSetEmployeeRoles(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	// Create an employee first
	emp := &model.Employee{
		FirstName: "Alice",
		LastName:  "Smith",
		Email:     "alice@example.com",
		Username:  "asmith",
		JMBG:      "0101990710024",
	}
	_ = repo.Create(emp)

	// Seed roles into the mock repos using the codegened catalog.
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{"EmployeeAgent"}, 0)
	assert.NoError(t, err)

	updated, err := repo.GetByID(emp.ID)
	assert.NoError(t, err)
	assert.NotEmpty(t, updated.Roles)
	assert.Equal(t, "EmployeeAgent", updated.Roles[0].Name)
}

func TestSetEmployeeAdditionalPermissions(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	// Create an employee
	emp := &model.Employee{
		FirstName: "Bob",
		LastName:  "Jones",
		Email:     "bob@example.com",
		Username:  "bjones",
		JMBG:      "0201990710025",
	}
	_ = repo.Create(emp)

	// Seed permissions into the mock repos using the codegened catalog.
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	err := svc.SetEmployeeAdditionalPermissions(context.Background(), emp.ID,
		[]string{"clients.read.all", "securities.trade.any"}, 0)
	assert.NoError(t, err)

	updated, err := repo.GetByID(emp.ID)
	assert.NoError(t, err)
	assert.Len(t, updated.AdditionalPermissions, 2)
}

func TestResolvePermissions(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp := &model.Employee{
		Roles: []model.Role{
			{
				Name: "EmployeeBasic",
				Permissions: []model.Permission{
					{Code: "clients.read"},
					{Code: "accounts.read"},
				},
			},
		},
		AdditionalPermissions: []model.Permission{
			{Code: "securities.trade"},
			{Code: "clients.read"}, // duplicate — should be deduplicated
		},
	}

	perms := svc.ResolvePermissions(emp)
	assert.Contains(t, perms, "clients.read")
	assert.Contains(t, perms, "accounts.read")
	assert.Contains(t, perms, "securities.trade")
	// "clients.read" should appear only once
	count := 0
	for _, p := range perms {
		if p == "clients.read" {
			count++
		}
	}
	assert.Equal(t, 1, count, "clients.read should appear exactly once")
}

// TestCreateEmployee_DuplicateEmail verifies that creating two employees with the same
// email is rejected by the repository layer.
func TestCreateEmployee_DuplicateEmail(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	emp1 := &model.Employee{
		FirstName: "Alice",
		LastName:  "A",
		Email:     "shared@example.com",
		Username:  "alice",
		JMBG:      "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp1)
	assert.NoError(t, err, "first employee should be created successfully")

	emp2 := &model.Employee{
		FirstName: "Bob",
		LastName:  "B",
		Email:     "shared@example.com", // same email
		Username:  "bob",
		JMBG:      "0201990710025",
	}
	err = svc.CreateEmployee(context.Background(), emp2)
	assert.Error(t, err, "second employee with duplicate email should be rejected")
	assert.Contains(t, err.Error(), "duplicate email")
}

// TestUpdateEmployee_EmailImmutable verifies that passing an "email" key in the updates
// map has no effect — email is not an accepted update field.
func TestUpdateEmployee_EmailImmutable(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:    1,
		Email: "original@example.com",
		JMBG:  "0101990710024",
	}
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.UpdateEmployee(context.Background(), 1, map[string]interface{}{
		"email":     "changed@example.com",
		"last_name": "Updated",
	}, 0)
	assert.NoError(t, err, "UpdateEmployee with email key should not return an error")

	persisted, err := repo.GetByID(1)
	assert.NoError(t, err)
	assert.Equal(t, "original@example.com", persisted.Email,
		"email should not be changed by UpdateEmployee")
	assert.Equal(t, "Updated", persisted.LastName,
		"other fields (last_name) should be updated normally")
}

// TestUpdateEmployee_JMBGValidIsApplied verifies that a valid new JMBG is accepted and applied.
func TestUpdateEmployee_JMBGValidIsApplied(t *testing.T) {
	repo := newMockRepo()
	repo.employees[2] = &model.Employee{
		ID:   2,
		JMBG: "0101990710024",
	}
	svc := NewEmployeeService(repo, nil, nil, nil)

	updated, err := svc.UpdateEmployee(context.Background(), 2, map[string]interface{}{
		"jmbg": "0201990710025",
	}, 0)
	assert.NoError(t, err)
	assert.Equal(t, "0201990710025", updated.JMBG, "valid JMBG should be applied")

	persisted, err := repo.GetByID(2)
	assert.NoError(t, err)
	assert.Equal(t, "0201990710025", persisted.JMBG)
}

// TestUpdateEmployee_JMBGAbsentRetainsOriginal verifies that omitting the jmbg key leaves
// the original JMBG unchanged.
func TestUpdateEmployee_JMBGAbsentRetainsOriginal(t *testing.T) {
	repo := newMockRepo()
	repo.employees[3] = &model.Employee{
		ID:   3,
		JMBG: "0101990710024",
	}
	svc := NewEmployeeService(repo, nil, nil, nil)

	updated, err := svc.UpdateEmployee(context.Background(), 3, map[string]interface{}{
		"last_name": "Smith",
	}, 0)
	assert.NoError(t, err)
	assert.Equal(t, "0101990710024", updated.JMBG, "JMBG should be unchanged when not in updates")
}
