package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"golang.org/x/crypto/bcrypt"

	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
)

var passwordRegex = regexp.MustCompile(`^(?=(?:.*[0-9]){2,})(?=.*[a-z])(?=.*[A-Z]).{8,32}$`)

type EmployeeService struct {
	repo     *repository.EmployeeRepository
	producer *kafkaprod.Producer
}

func NewEmployeeService(repo *repository.EmployeeRepository, producer *kafkaprod.Producer) *EmployeeService {
	return &EmployeeService{repo: repo, producer: producer}
}

func (s *EmployeeService) CreateEmployee(ctx context.Context, emp *model.Employee) error {
	if !ValidRole(emp.Role) {
		return errors.New("invalid role")
	}

	salt := generateSalt()
	emp.Salt = salt
	emp.PasswordHash = "" // no password until activation
	emp.Activated = false

	if err := s.repo.Create(emp); err != nil {
		return fmt.Errorf("create employee: %w", err)
	}

	return nil
}

func (s *EmployeeService) GetEmployee(id int64) (*model.Employee, error) {
	return s.repo.GetByID(id)
}

func (s *EmployeeService) GetByEmail(email string) (*model.Employee, error) {
	return s.repo.GetByEmail(email)
}

func (s *EmployeeService) ListEmployees(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	return s.repo.List(emailFilter, nameFilter, positionFilter, page, pageSize)
}

func (s *EmployeeService) UpdateEmployee(id int64, updates map[string]interface{}) (*model.Employee, error) {
	emp, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if role, ok := updates["role"].(string); ok {
		if !ValidRole(role) {
			return nil, errors.New("invalid role")
		}
		emp.Role = role
	}
	if v, ok := updates["last_name"].(string); ok {
		emp.LastName = v
	}
	if v, ok := updates["gender"].(string); ok {
		emp.Gender = v
	}
	if v, ok := updates["phone"].(string); ok {
		emp.Phone = v
	}
	if v, ok := updates["address"].(string); ok {
		emp.Address = v
	}
	if v, ok := updates["position"].(string); ok {
		emp.Position = v
	}
	if v, ok := updates["department"].(string); ok {
		emp.Department = v
	}
	if v, ok := updates["active"].(*bool); ok {
		emp.Active = *v
	}
	if v, ok := updates["active"].(bool); ok {
		emp.Active = v
	}

	if err := s.repo.Update(emp); err != nil {
		return nil, err
	}
	return emp, nil
}

func (s *EmployeeService) ValidateCredentials(email, password string) (*model.Employee, bool) {
	emp, err := s.repo.GetByEmail(email)
	if err != nil || !emp.Active || !emp.Activated {
		return nil, false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(emp.PasswordHash), []byte(password)); err != nil {
		return nil, false
	}
	return emp, true
}

func (s *EmployeeService) SetPassword(userID int64, hash string) error {
	return s.repo.SetPassword(userID, hash)
}

func ValidatePassword(password string) error {
	if !passwordRegex.MatchString(password) {
		return errors.New("password must be 8-32 chars with at least 2 digits, 1 uppercase and 1 lowercase letter")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func generateSalt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
