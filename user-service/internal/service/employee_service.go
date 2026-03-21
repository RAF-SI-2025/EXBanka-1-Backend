package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/user-service/internal/cache"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
)

type EmployeeService struct {
	repo     EmployeeRepo
	producer *kafkaprod.Producer
	cache    *cache.RedisCache
	roleSvc  *RoleService
}

func NewEmployeeService(repo EmployeeRepo, producer *kafkaprod.Producer, cache *cache.RedisCache, roleSvc *RoleService) *EmployeeService {
	return &EmployeeService{repo: repo, producer: producer, cache: cache, roleSvc: roleSvc}
}

// ResolvePermissions collects all unique permission codes for an employee
// from their assigned roles and additional permissions.
func (s *EmployeeService) ResolvePermissions(emp *model.Employee) []string {
	seen := make(map[string]bool)
	var codes []string
	for _, role := range emp.Roles {
		for _, perm := range role.Permissions {
			if !seen[perm.Code] {
				seen[perm.Code] = true
				codes = append(codes, perm.Code)
			}
		}
	}
	for _, perm := range emp.AdditionalPermissions {
		if !seen[perm.Code] {
			seen[perm.Code] = true
			codes = append(codes, perm.Code)
		}
	}
	return codes
}

func (s *EmployeeService) CreateEmployee(ctx context.Context, emp *model.Employee) error {
	// Validate role name: check DB if roleSvc available, fall back to legacy map check
	if s.roleSvc != nil {
		if emp.Role != "" && !s.roleSvc.ValidRole(emp.Role) {
			return errors.New("invalid role")
		}
	} else {
		if !validRoleLegacy(emp.Role) {
			return errors.New("invalid role")
		}
	}
	if err := ValidateJMBG(emp.JMBG); err != nil {
		return err
	}

	if err := s.repo.Create(emp); err != nil {
		return fmt.Errorf("create employee: %w", err)
	}

	// Associate role with the employee via the new many2many relationship
	if s.roleSvc != nil && emp.Role != "" {
		if err := s.repo.SetEmployeeRoles(emp.ID, []model.Role{{Name: emp.Role}}); err != nil {
			log.Printf("warn: failed to set employee roles after create: %v", err)
		}
	}

	roleNames := extractRoleNames(emp.Roles)
	if len(roleNames) == 0 && emp.Role != "" {
		roleNames = []string{emp.Role}
	}

	if s.producer != nil {
		if err := s.producer.PublishEmployeeCreated(ctx, kafkamsg.EmployeeCreatedMessage{
			EmployeeID: emp.ID,
			Email:      emp.Email,
			FirstName:  emp.FirstName,
			LastName:   emp.LastName,
			Roles:      roleNames,
		}); err != nil {
			log.Printf("warn: failed to publish employee-created event: %v", err)
		}
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

	emp, err := s.repo.GetByIDWithRoles(id)
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

func (s *EmployeeService) UpdateEmployee(ctx context.Context, id int64, updates map[string]interface{}) (*model.Employee, error) {
	emp, err := s.repo.GetByIDWithRoles(id)
	if err != nil {
		return nil, err
	}

	if role, ok := updates["role"].(string); ok {
		if s.roleSvc != nil {
			if !s.roleSvc.ValidRole(role) {
				return nil, errors.New("invalid role")
			}
		} else {
			if !validRoleLegacy(role) {
				return nil, errors.New("invalid role")
			}
		}
		emp.Role = role
		// Also update many2many role association
		if s.roleSvc != nil {
			if err := s.repo.SetEmployeeRoles(id, []model.Role{{Name: role}}); err != nil {
				log.Printf("warn: failed to update employee roles: %v", err)
			}
		}
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
	if v, ok := updates["jmbg"].(string); ok {
		if err := ValidateJMBG(v); err != nil {
			return nil, err
		}
		emp.JMBG = v
	}

	if err := s.repo.Update(emp); err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Delete(context.Background(), "employee:id:"+strconv.FormatInt(id, 10))
		_ = s.cache.Delete(context.Background(), "employee:email:"+emp.Email)
	}

	roleNames := extractRoleNames(emp.Roles)
	if len(roleNames) == 0 && emp.Role != "" {
		roleNames = []string{emp.Role}
	}

	if s.producer != nil {
		if err := s.producer.PublishEmployeeUpdated(ctx, kafkamsg.EmployeeCreatedMessage{
			EmployeeID: emp.ID,
			Email:      emp.Email,
			FirstName:  emp.FirstName,
			LastName:   emp.LastName,
			Roles:      roleNames,
		}); err != nil {
			log.Printf("warn: failed to publish employee-updated event: %v", err)
		}
	}

	return emp, nil
}

// SetEmployeeRoles replaces the roles associated with an employee.
func (s *EmployeeService) SetEmployeeRoles(ctx context.Context, employeeID int64, roleNames []string) error {
	if _, err := s.repo.GetByID(employeeID); err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	if s.roleSvc != nil {
		for _, name := range roleNames {
			if !s.roleSvc.ValidRole(name) {
				return fmt.Errorf("invalid role: %s", name)
			}
		}
	}

	roles, err := s.roleSvc.GetRolesByNames(roleNames)
	if err != nil {
		return err
	}

	if err := s.repo.SetEmployeeRoles(employeeID, roles); err != nil {
		return err
	}

	// Invalidate cache
	if s.cache != nil {
		_ = s.cache.Delete(ctx, "employee:id:"+strconv.FormatInt(employeeID, 10))
		emp, err2 := s.repo.GetByID(employeeID)
		if err2 == nil {
			_ = s.cache.Delete(ctx, "employee:email:"+emp.Email)
		}
	}
	return nil
}

// SetEmployeeAdditionalPermissions replaces the additional permissions for an employee.
func (s *EmployeeService) SetEmployeeAdditionalPermissions(ctx context.Context, employeeID int64, permCodes []string) error {
	if _, err := s.repo.GetByID(employeeID); err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	perms, err := s.roleSvc.GetPermissionsByCodes(permCodes)
	if err != nil {
		return err
	}

	if len(perms) != len(permCodes) {
		return errors.New("one or more permission codes are invalid")
	}

	if err := s.repo.SetAdditionalPermissions(employeeID, perms); err != nil {
		return err
	}

	// Invalidate cache
	if s.cache != nil {
		_ = s.cache.Delete(ctx, "employee:id:"+strconv.FormatInt(employeeID, 10))
		emp, err2 := s.repo.GetByID(employeeID)
		if err2 == nil {
			_ = s.cache.Delete(ctx, "employee:email:"+emp.Email)
		}
	}
	return nil
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

// validRoleLegacy is a fallback role check using the static map (used when roleSvc is nil in tests).
func validRoleLegacy(role string) bool {
	validRoles := map[string]bool{
		"EmployeeBasic":      true,
		"EmployeeAgent":      true,
		"EmployeeSupervisor": true,
		"EmployeeAdmin":      true,
	}
	return validRoles[role]
}

func extractRoleNames(roles []model.Role) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}
