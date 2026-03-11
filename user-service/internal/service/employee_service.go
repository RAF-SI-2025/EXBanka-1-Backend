package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/exbanka/user-service/internal/cache"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
)

type EmployeeService struct {
	repo     *repository.EmployeeRepository
	producer *kafkaprod.Producer
	cache    *cache.RedisCache
}

func NewEmployeeService(repo *repository.EmployeeRepository, producer *kafkaprod.Producer, cache *cache.RedisCache) *EmployeeService {
	return &EmployeeService{repo: repo, producer: producer, cache: cache}
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
	cacheKey := "employee:id:" + strconv.FormatInt(id, 10)
	if s.cache != nil {
		var cached model.Employee
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			return &cached, nil
		}
	}

	emp, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Set(context.Background(), cacheKey, emp, 5*time.Minute)
	}
	return emp, nil
}

func (s *EmployeeService) GetByEmail(email string) (*model.Employee, error) {
	cacheKey := "employee:email:" + email
	if s.cache != nil {
		var cached model.Employee
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			return &cached, nil
		}
	}

	emp, err := s.repo.GetByEmail(email)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Set(context.Background(), cacheKey, emp, 5*time.Minute)
	}
	return emp, nil
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
	if s.cache != nil {
		_ = s.cache.Delete(context.Background(), "employee:id:"+strconv.FormatInt(id, 10))
		_ = s.cache.Delete(context.Background(), "employee:email:"+emp.Email)
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
	if len(password) < 8 || len(password) > 32 {
		return errors.New("password must be 8-32 characters")
	}
	digits := 0
	hasUpper := false
	hasLower := false
	for _, c := range password {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		}
	}
	if digits < 2 || !hasUpper || !hasLower {
		return errors.New("password must have at least 2 digits, 1 uppercase and 1 lowercase letter")
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
