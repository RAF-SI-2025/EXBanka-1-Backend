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

func (m *mockRepo) GetByEmail(email string) (*model.Employee, error) {
	for _, emp := range m.employees {
		if emp.Email == email {
			return emp, nil
		}
	}
	return nil, errors.New("not found")
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

func (m *mockRepo) SetPassword(userID int64, hash string) error {
	emp, ok := m.employees[userID]
	if !ok {
		return errors.New("not found")
	}
	emp.PasswordHash = hash
	emp.Activated = true
	return nil
}

func (m *mockRepo) List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	var result []model.Employee
	for _, emp := range m.employees {
		result = append(result, *emp)
	}
	return result, int64(len(result)), nil
}

func TestCreateEmployee_Valid(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		FirstName: "John",
		LastName:  "Doe",
		Email:     "john@example.com",
		Username:  "johndoe",
		Role:      "EmployeeBasic",
		JMBG:      "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), emp.ID)
	assert.False(t, emp.Activated)
	assert.Empty(t, emp.PasswordHash)
	assert.NotEmpty(t, emp.Salt)
}

func TestCreateEmployee_InvalidRole(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		Role: "InvalidRole",
		JMBG: "0101990710024",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestCreateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	emp := &model.Employee{
		Role: "EmployeeBasic",
		JMBG: "123",
	}
	err := svc.CreateEmployee(context.Background(), emp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestGetEmployee(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, FirstName: "Jane", Email: "jane@example.com"}
	svc := NewEmployeeService(repo, nil, nil)

	emp, err := svc.GetEmployee(1)
	assert.NoError(t, err)
	assert.Equal(t, "Jane", emp.FirstName)
}

func TestGetEmployee_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.GetEmployee(999)
	assert.Error(t, err)
}

func TestUpdateEmployee_InvalidRole(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Role: "EmployeeBasic"}
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.UpdateEmployee(1, map[string]interface{}{"role": "BadRole"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestUpdateEmployee_InvalidJMBG(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, Role: "EmployeeBasic", JMBG: "0101990710024"}
	svc := NewEmployeeService(repo, nil, nil)

	_, err := svc.UpdateEmployee(1, map[string]interface{}{"jmbg": "bad"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JMBG")
}

func TestValidateCredentials_Valid(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       true,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	emp, valid := svc.ValidateCredentials("test@test.com", "ValidPass12")
	assert.True(t, valid)
	assert.NotNil(t, emp)
}

func TestValidateCredentials_WrongPassword(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       true,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	_, valid := svc.ValidateCredentials("test@test.com", "WrongPass12")
	assert.False(t, valid)
}

func TestValidateCredentials_InactiveUser(t *testing.T) {
	hash, _ := HashPassword("ValidPass12")
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{
		ID:           1,
		Email:        "test@test.com",
		PasswordHash: hash,
		Active:       false,
		Activated:    true,
	}
	svc := NewEmployeeService(repo, nil, nil)

	_, valid := svc.ValidateCredentials("test@test.com", "ValidPass12")
	assert.False(t, valid)
}
