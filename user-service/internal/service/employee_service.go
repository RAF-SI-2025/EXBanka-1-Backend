package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/exbanka/contract/changelog"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/user-service/internal/cache"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
)

type EmployeeService struct {
	repo          EmployeeRepo
	producer      *kafkaprod.Producer
	cache         *cache.RedisCache
	roleSvc       *RoleService
	changelogRepo ChangelogRepo
}

func NewEmployeeService(repo EmployeeRepo, producer *kafkaprod.Producer, cache *cache.RedisCache, roleSvc *RoleService, changelogRepo ...ChangelogRepo) *EmployeeService {
	svc := &EmployeeService{repo: repo, producer: producer, cache: cache, roleSvc: roleSvc}
	if len(changelogRepo) > 0 {
		svc.changelogRepo = changelogRepo[0]
	}
	return svc
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
	if err := ValidateJMBG(emp.JMBG); err != nil {
		return err
	}

	if err := s.repo.Create(emp); err != nil {
		return fmt.Errorf("create employee: %w", err)
	}
	UserEmployeeCreatedTotal.Inc()

	roleNames := extractRoleNames(emp.Roles)

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

func (s *EmployeeService) GetEmployeeByEmail(email string) (*model.Employee, error) {
	return s.repo.GetByEmail(email)
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

func (s *EmployeeService) UpdateEmployee(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
	emp, err := s.repo.GetByIDWithRoles(id)
	if err != nil {
		return nil, err
	}

	// Build changelog field changes before applying updates.
	var changes []changelog.FieldChange
	if v, ok := updates["last_name"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "last_name", OldValue: emp.LastName, NewValue: v})
		emp.LastName = v
	}
	if v, ok := updates["gender"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "gender", OldValue: emp.Gender, NewValue: v})
		emp.Gender = v
	}
	if v, ok := updates["phone"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "phone", OldValue: emp.Phone, NewValue: v})
		emp.Phone = v
	}
	if v, ok := updates["address"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "address", OldValue: emp.Address, NewValue: v})
		emp.Address = v
	}
	if v, ok := updates["position"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "position", OldValue: emp.Position, NewValue: v})
		emp.Position = v
	}
	if v, ok := updates["department"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "department", OldValue: emp.Department, NewValue: v})
		emp.Department = v
	}
	if v, ok := updates["jmbg"].(string); ok {
		if err := ValidateJMBG(v); err != nil {
			return nil, err
		}
		changes = append(changes, changelog.FieldChange{Field: "jmbg", OldValue: emp.JMBG, NewValue: v})
		emp.JMBG = v
	}

	if err := s.repo.Update(emp); err != nil {
		return nil, err
	}

	// Record changelog after successful mutation.
	entries := changelog.Diff("employee", id, changedBy, "", changes)
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}
	if s.cache != nil {
		_ = s.cache.Delete(context.Background(), "employee:id:"+strconv.FormatInt(id, 10))
		_ = s.cache.Delete(context.Background(), "employee:email:"+emp.Email)
	}

	roleNames := extractRoleNames(emp.Roles)

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
func (s *EmployeeService) SetEmployeeRoles(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
	emp, err := s.repo.GetByIDWithRoles(employeeID)
	if err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	// Capture old roles for changelog.
	oldRoles := extractRoleNames(emp.Roles)

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
	UserRoleChangesTotal.Inc()

	// Record changelog.
	if s.changelogRepo != nil {
		entry := changelog.Entry{
			EntityType: "employee",
			EntityID:   employeeID,
			Action:     changelog.ActionUpdate,
			FieldName:  "roles",
			OldValue:   changelog.ToJSON(oldRoles),
			NewValue:   changelog.ToJSON(roleNames),
			ChangedBy:  changedBy,
			ChangedAt:  time.Now(),
		}
		_ = s.changelogRepo.Create(entry)
	}

	// Invalidate cache
	if s.cache != nil {
		_ = s.cache.Delete(ctx, "employee:id:"+strconv.FormatInt(employeeID, 10))
		emp2, err2 := s.repo.GetByID(employeeID)
		if err2 == nil {
			_ = s.cache.Delete(ctx, "employee:email:"+emp2.Email)
		}
	}
	return nil
}

// SetEmployeeAdditionalPermissions replaces the additional permissions for an employee.
func (s *EmployeeService) SetEmployeeAdditionalPermissions(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
	emp, err := s.repo.GetByIDWithRoles(employeeID)
	if err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	// Capture old permissions for changelog.
	oldPerms := make([]string, 0, len(emp.AdditionalPermissions))
	for _, p := range emp.AdditionalPermissions {
		oldPerms = append(oldPerms, p.Code)
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

	// Record changelog.
	if s.changelogRepo != nil {
		entry := changelog.Entry{
			EntityType: "employee",
			EntityID:   employeeID,
			Action:     changelog.ActionUpdate,
			FieldName:  "additional_permissions",
			OldValue:   changelog.ToJSON(oldPerms),
			NewValue:   changelog.ToJSON(permCodes),
			ChangedBy:  changedBy,
			ChangedAt:  time.Now(),
		}
		_ = s.changelogRepo.Create(entry)
	}

	// Invalidate cache
	if s.cache != nil {
		_ = s.cache.Delete(ctx, "employee:id:"+strconv.FormatInt(employeeID, 10))
		emp2, err2 := s.repo.GetByID(employeeID)
		if err2 == nil {
			_ = s.cache.Delete(ctx, "employee:email:"+emp2.Email)
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

func extractRoleNames(roles []model.Role) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}
