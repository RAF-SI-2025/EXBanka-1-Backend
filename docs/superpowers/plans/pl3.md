# Plan 11: Role Hierarchy Enforcement for Limit Operations

**Date:** 2026-04-04
**Spec ref:** `docs/superpowers/specs/2026-04-04-blueprints-biometrics-hierarchy-design.md` Section 3
**Scope:** Enforce role-rank hierarchy in user-service so lower-ranked employees cannot modify limits of equal-or-higher-ranked employees, and no one can modify their own limits.

---

## Current State

- Zero hierarchy enforcement. Anyone with `limits.manage` permission can change anyone's limits.
- Role names: `EmployeeBasic` (rank 1), `EmployeeAgent` (rank 2), `EmployeeSupervisor` (rank 3), `EmployeeAdmin` (rank 4).
- `changedBy` (caller ID) is already propagated via gRPC `x-changed-by` metadata and extracted in handlers via `changelog.ExtractChangedBy(ctx)`.
- `LimitService.SetEmployeeLimits` already receives `changedBy int64`.
- `LimitService.ApplyTemplate`, `ActuaryService.SetActuaryLimit`, `ActuaryService.ResetUsedLimit`, and `ActuaryService.SetNeedApproval` do NOT receive `changedBy` yet.
- The API gateway actuary handler (`api-gateway/internal/handler/actuary_handler.go`) does not forward `x-changed-by` metadata. It uses `c.Request.Context()` instead of `middleware.GRPCContextWithChangedBy(c)`.
- The API gateway limit handler's `ApplyLimitTemplate` also uses `c.Request.Context()` instead of `middleware.GRPCContextWithChangedBy(c)`.
- `ActuaryService` already has `empRepo ActuaryEmpRepo` (with `GetByIDWithRoles`) in its constructor.
- `LimitService` does NOT have `empRepo` -- it only has `limitRepo` and `templateRepo`. It needs `empRepo` added.

---

## Task 1: Create Shared Hierarchy Helper

### Files
- **Create:** `user-service/internal/service/hierarchy.go`
- **Create:** `user-service/internal/service/hierarchy_test.go`

### Steps

- [ ] **1.1** Create `user-service/internal/service/hierarchy.go` with the following content:

```go
package service

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/user-service/internal/model"
)

// roleRanks defines the hierarchy: higher number = higher rank.
var roleRanks = map[string]int{
	"EmployeeBasic":      1,
	"EmployeeAgent":      2,
	"EmployeeSupervisor": 3,
	"EmployeeAdmin":      4,
}

// maxRoleRank returns the highest rank from a list of roles.
// Returns 0 if no recognized roles are present.
func maxRoleRank(roles []model.Role) int {
	max := 0
	for _, r := range roles {
		if rank, ok := roleRanks[r.Name]; ok && rank > max {
			max = rank
		}
	}
	return max
}

// HierarchyEmpRepo is the minimal interface needed for hierarchy checks.
type HierarchyEmpRepo interface {
	GetByIDWithRoles(id int64) (*model.Employee, error)
}

// checkHierarchy verifies that callerID has strictly higher role rank than
// targetEmployeeID. Returns a gRPC PermissionDenied error on violation.
// Also blocks self-modification (callerID == targetEmployeeID).
func checkHierarchy(empRepo HierarchyEmpRepo, callerID, targetEmployeeID int64) error {
	if callerID == 0 {
		// System-initiated change (no caller metadata) -- allow.
		return nil
	}
	if callerID == targetEmployeeID {
		return status.Error(codes.PermissionDenied, "cannot modify own limits")
	}

	caller, err := empRepo.GetByIDWithRoles(callerID)
	if err != nil {
		return status.Error(codes.Internal, "failed to load caller employee")
	}

	target, err := empRepo.GetByIDWithRoles(targetEmployeeID)
	if err != nil {
		return status.Error(codes.Internal, "failed to load target employee")
	}

	callerRank := maxRoleRank(caller.Roles)
	targetRank := maxRoleRank(target.Roles)

	if callerRank <= targetRank {
		return status.Error(codes.PermissionDenied,
			"insufficient rank to modify this employee's limits")
	}
	return nil
}
```

- [ ] **1.2** Create `user-service/internal/service/hierarchy_test.go` with the following content:

```go
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
```

- [ ] **1.3** Run tests:
```bash
cd user-service && go test ./internal/service/ -run TestMaxRoleRank -v
cd user-service && go test ./internal/service/ -run TestCheckHierarchy -v
```

- [ ] **1.4** Commit: `feat(user-service): add role hierarchy helper for limit operations`

---

## Task 2: Add Hierarchy Check to LimitService

### Files
- **Modify:** `user-service/internal/service/limit_service.go`
- **Modify:** `user-service/internal/handler/limit_handler.go`
- **Modify:** `api-gateway/internal/handler/limit_handler.go`
- **Modify:** `user-service/internal/service/limit_service_test.go`
- **Modify:** `user-service/cmd/main.go`

### Steps

- [ ] **2.1** Add `empRepo` field to `LimitService` and update its constructor.

In `user-service/internal/service/limit_service.go`, change the struct and constructor:

```go
// LimitService manages employee limits and limit templates.
type LimitService struct {
	limitRepo     EmployeeLimitRepo
	templateRepo  LimitTemplateRepo
	empRepo       HierarchyEmpRepo
	producer      *kafkaprod.Producer
	changelogRepo ChangelogRepo
}

func NewLimitService(limitRepo EmployeeLimitRepo, templateRepo LimitTemplateRepo, empRepo HierarchyEmpRepo, producer *kafkaprod.Producer, changelogRepo ...ChangelogRepo) *LimitService {
	svc := &LimitService{
		limitRepo:    limitRepo,
		templateRepo: templateRepo,
		empRepo:      empRepo,
		producer:     producer,
	}
	if len(changelogRepo) > 0 {
		svc.changelogRepo = changelogRepo[0]
	}
	return svc
}
```

- [ ] **2.2** Add hierarchy check to `SetEmployeeLimits`. Insert at the beginning of the method body (after line 43, before the existing `oldLimit` fetch):

```go
func (s *LimitService) SetEmployeeLimits(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, limit.EmployeeID); err != nil {
		return nil, err
	}

	// Fetch old limits for changelog.
	oldLimit, _ := s.limitRepo.GetByEmployeeID(limit.EmployeeID)
	// ... rest unchanged ...
```

- [ ] **2.3** Add `changedBy` parameter to `ApplyTemplate` and add hierarchy check. Change the signature and add the check:

```go
// ApplyTemplate copies template values to an employee's limit record.
func (s *LimitService) ApplyTemplate(ctx context.Context, employeeID int64, templateName string, changedBy int64) (*model.EmployeeLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	tmpl, err := s.templateRepo.GetByName(templateName)
	// ... rest unchanged ...
```

- [ ] **2.4** Update the gRPC handler `ApplyLimitTemplate` in `user-service/internal/handler/limit_handler.go` to extract and pass `changedBy`:

```go
func (h *LimitGRPCHandler) ApplyLimitTemplate(ctx context.Context, req *pb.ApplyLimitTemplateRequest) (*pb.EmployeeLimitResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.limitSvc.ApplyTemplate(ctx, req.EmployeeId, req.TemplateName, changedBy)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to apply limit template: %v", err)
	}
	return toEmployeeLimitResponse(result), nil
}
```

Note: The import for `"github.com/exbanka/contract/changelog"` is already present in `limit_handler.go`.

- [ ] **2.5** Update the API gateway's `ApplyLimitTemplate` in `api-gateway/internal/handler/limit_handler.go` to forward `x-changed-by` metadata. Change line 161 from `c.Request.Context()` to `middleware.GRPCContextWithChangedBy(c)`:

```go
func (h *LimitHandler) ApplyLimitTemplate(c *gin.Context) {
	// ... id parsing, body binding unchanged ...

	resp, err := h.empLimitClient.ApplyLimitTemplate(middleware.GRPCContextWithChangedBy(c), &userpb.ApplyLimitTemplateRequest{
		EmployeeId:   id,
		TemplateName: body.TemplateName,
	})
	// ... rest unchanged ...
```

- [ ] **2.6** Update `user-service/cmd/main.go` to pass `repo` (the `EmployeeRepository`) to `NewLimitService`. Change line 88 from:

```go
limitSvc := service.NewLimitService(employeeLimitRepo, limitTemplateRepo, producer, changelogRepo)
```

to:

```go
limitSvc := service.NewLimitService(employeeLimitRepo, limitTemplateRepo, repo, producer, changelogRepo)
```

This works because `repository.EmployeeRepository` already implements `GetByIDWithRoles(id int64) (*model.Employee, error)`, which satisfies the `HierarchyEmpRepo` interface.

- [ ] **2.7** Update `user-service/internal/service/limit_service_test.go`. All calls to `NewLimitService` need the new `empRepo` parameter. Since the existing tests pass `changedBy=0` (system-initiated), the hierarchy check passes for them. Add a mock and update constructor calls:

Add a `mockLimitEmpRepo` (reuse `mockHierarchyEmpRepo` from the same package, or define a new one if needed -- since they are in the same package, `mockHierarchyEmpRepo` from `hierarchy_test.go` is accessible).

Update every `NewLimitService` call from:
```go
svc := NewLimitService(limitRepo, templateRepo, nil)
```
to:
```go
empRepo := newMockHierarchyEmpRepo()
svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)
```

Update `TestApplyTemplate_Success` and `TestApplyTemplate_NotFound` calls to pass `changedBy=0`:
```go
result, err := svc.ApplyTemplate(context.Background(), 99, "BasicTeller", 0)
```
```go
_, err := svc.ApplyTemplate(context.Background(), 99, "NonExistentTemplate", 0)
```

Add new hierarchy enforcement tests:

```go
func TestSetEmployeeLimits_HierarchyBlocked(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	// Caller is Agent (rank 2), target is Supervisor (rank 3) -- should be blocked
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[200] = &model.Employee{ID: 200, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

	limit := model.EmployeeLimit{
		EmployeeID:            200,
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	_, err := svc.SetEmployeeLimits(context.Background(), limit, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient rank")
}

func TestSetEmployeeLimits_SelfBlocked(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAdmin"}}}

	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

	limit := model.EmployeeLimit{
		EmployeeID:            100,
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	_, err := svc.SetEmployeeLimits(context.Background(), limit, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot modify own limits")
}

func TestSetEmployeeLimits_HierarchyAllowed(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	// Caller is Admin (rank 4), target is Agent (rank 2) -- should be allowed
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAdmin"}}}
	empRepo.employees[200] = &model.Employee{ID: 200, Roles: []model.Role{{Name: "EmployeeAgent"}}}

	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

	limit := model.EmployeeLimit{
		EmployeeID:            200,
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	result, err := svc.SetEmployeeLimits(context.Background(), limit, 100)
	assert.NoError(t, err)
	assert.Equal(t, int64(200), result.EmployeeID)
}

func TestApplyTemplate_HierarchyBlocked(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	// Caller is Agent (rank 2), target is Supervisor (rank 3)
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[200] = &model.Employee{ID: 200, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	tmpl := &model.LimitTemplate{
		Name:                  "BasicTeller",
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	_ = templateRepo.Create(tmpl)

	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)
	_, err := svc.ApplyTemplate(context.Background(), 200, "BasicTeller", 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient rank")
}
```

- [ ] **2.8** Run tests:
```bash
cd user-service && go test ./internal/service/ -run TestSetEmployeeLimits -v
cd user-service && go test ./internal/service/ -run TestApplyTemplate -v
cd user-service && go test ./internal/service/ -v
```

- [ ] **2.9** Commit: `feat(user-service): add hierarchy enforcement to LimitService`

---

## Task 3: Add Hierarchy Check to ActuaryService

### Files
- **Modify:** `user-service/internal/service/actuary_service.go`
- **Modify:** `user-service/internal/handler/actuary_handler.go`
- **Modify:** `api-gateway/internal/handler/actuary_handler.go`
- **Modify:** `user-service/internal/service/actuary_service_test.go`

### Steps

- [ ] **3.1** Add `changedBy` parameter to `SetActuaryLimit`, `ResetUsedLimit`, and `SetNeedApproval` in `user-service/internal/service/actuary_service.go`, and add hierarchy checks. The `empRepo` is already available as `s.empRepo` (type `ActuaryEmpRepo`, which has `GetByIDWithRoles`).

Note: `ActuaryEmpRepo` interface matches `HierarchyEmpRepo` (both have `GetByIDWithRoles(id int64) (*model.Employee, error)`). The `checkHierarchy` function accepts `HierarchyEmpRepo`, and since `ActuaryEmpRepo` has the same method signature, any value implementing `ActuaryEmpRepo` also implements `HierarchyEmpRepo`. So `s.empRepo` can be passed directly.

Update `SetActuaryLimit`:

```go
func (s *ActuaryService) SetActuaryLimit(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	if limitAmount.IsNegative() {
		return nil, errors.New("limit must not be negative")
	}
	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.Limit = limitAmount
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "limit_set")
	return limit, nil
}
```

Update `ResetUsedLimit`:

```go
func (s *ActuaryService) ResetUsedLimit(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.UsedLimit = decimal.NewFromInt(0)
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "used_limit_reset")
	return limit, nil
}
```

Update `SetNeedApproval`:

```go
func (s *ActuaryService) SetNeedApproval(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.NeedApproval = needApproval
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "need_approval_changed")
	return limit, nil
}
```

Note: `UpdateUsedLimit` is NOT modified -- it is called by stock-service internally (machine-to-machine), not by employees, so hierarchy enforcement does not apply.

- [ ] **3.2** Update the gRPC handler `user-service/internal/handler/actuary_handler.go` to extract `changedBy` and pass it through. Add the import for `"github.com/exbanka/contract/changelog"`.

Update `SetActuaryLimit`:

```go
func (h *ActuaryGRPCHandler) SetActuaryLimit(ctx context.Context, req *pb.SetActuaryLimitRequest) (*pb.ActuaryInfo, error) {
	limitVal, err := decimal.NewFromString(req.Limit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid limit value: %v", err)
	}
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.SetActuaryLimit(ctx, int64(req.Id), limitVal, changedBy)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toActuaryInfoFromLimit(result), nil
}
```

Update `ResetActuaryUsedLimit`:

```go
func (h *ActuaryGRPCHandler) ResetActuaryUsedLimit(ctx context.Context, req *pb.ResetActuaryUsedLimitRequest) (*pb.ActuaryInfo, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.ResetUsedLimit(ctx, int64(req.Id), changedBy)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toActuaryInfoFromLimit(result), nil
}
```

Update `SetNeedApproval`:

```go
func (h *ActuaryGRPCHandler) SetNeedApproval(ctx context.Context, req *pb.SetNeedApprovalRequest) (*pb.ActuaryInfo, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.SetNeedApproval(ctx, int64(req.Id), req.NeedApproval, changedBy)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toActuaryInfoFromLimit(result), nil
}
```

- [ ] **3.3** Update the API gateway `api-gateway/internal/handler/actuary_handler.go` to forward `x-changed-by` metadata on all three mutating calls. Add the import for `"github.com/exbanka/api-gateway/internal/middleware"`.

Change `SetActuaryLimit` -- replace `c.Request.Context()` with `middleware.GRPCContextWithChangedBy(c)`:

```go
func (h *ActuaryHandler) SetActuaryLimit(c *gin.Context) {
	// ... id parsing, body binding unchanged ...

	resp, err := h.client.SetActuaryLimit(middleware.GRPCContextWithChangedBy(c), &userpb.SetActuaryLimitRequest{
		Id: id, Limit: req.Limit,
	})
	// ... rest unchanged ...
```

Change `ResetActuaryLimit` -- replace `c.Request.Context()` with `middleware.GRPCContextWithChangedBy(c)`:

```go
func (h *ActuaryHandler) ResetActuaryLimit(c *gin.Context) {
	// ... id parsing unchanged ...

	resp, err := h.client.ResetActuaryUsedLimit(middleware.GRPCContextWithChangedBy(c), &userpb.ResetActuaryUsedLimitRequest{Id: id})
	// ... rest unchanged ...
```

Change `SetNeedApproval` -- replace `c.Request.Context()` with `middleware.GRPCContextWithChangedBy(c)`:

```go
func (h *ActuaryHandler) SetNeedApproval(c *gin.Context) {
	// ... id parsing, body binding unchanged ...

	resp, err := h.client.SetNeedApproval(middleware.GRPCContextWithChangedBy(c), &userpb.SetNeedApprovalRequest{
		Id: id, NeedApproval: req.NeedApproval,
	})
	// ... rest unchanged ...
```

- [ ] **3.4** Update `user-service/internal/service/actuary_service_test.go`. All existing calls to `SetActuaryLimit`, `ResetUsedLimit`, and `SetNeedApproval` need the new `changedBy` parameter appended. Since existing tests pass `changedBy=0` (system-initiated), hierarchy check is bypassed.

Update every call site:
- `svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(5000))` becomes `svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(5000), 0)`
- `svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(-100))` becomes `svc.SetActuaryLimit(context.Background(), 10, decimal.NewFromInt(-100), 0)`
- `svc.ResetUsedLimit(context.Background(), 50)` becomes `svc.ResetUsedLimit(context.Background(), 50, 0)`
- `svc.ResetUsedLimit(context.Background(), 999)` becomes `svc.ResetUsedLimit(context.Background(), 999, 0)`
- `svc.SetNeedApproval(context.Background(), 80, false)` becomes `svc.SetNeedApproval(context.Background(), 80, false, 0)`
- `svc.SetActuaryLimit(context.Background(), 90, decimal.NewFromInt(1000))` becomes `svc.SetActuaryLimit(context.Background(), 90, decimal.NewFromInt(1000), 0)`
- `svc.SetActuaryLimit(context.Background(), 100, decimal.NewFromInt(1000))` becomes `svc.SetActuaryLimit(context.Background(), 100, decimal.NewFromInt(1000), 0)`

Add new hierarchy enforcement tests:

```go
func TestSetActuaryLimit_HierarchyBlocked(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	// Caller is Agent (rank 2), target is Supervisor (rank 3)
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[200] = &model.Employee{ID: 200, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 200, decimal.NewFromInt(5000), 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient rank")
}

func TestSetActuaryLimit_SelfBlocked(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 100, decimal.NewFromInt(5000), 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot modify own limits")
}

func TestSetActuaryLimit_HierarchyAllowed(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	// Caller is Admin (rank 4), target is Agent (rank 2)
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAdmin"}}}
	empRepo.employees[200] = agentEmployee(200)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	result, err := svc.SetActuaryLimit(context.Background(), 200, decimal.NewFromInt(5000), 100)
	assert.NoError(t, err)
	assert.True(t, result.Limit.Equal(decimal.NewFromInt(5000)))
}

func TestResetUsedLimit_HierarchyBlocked(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[200] = supervisorEmployee(200)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.ResetUsedLimit(context.Background(), 200, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient rank")
}

func TestSetNeedApproval_HierarchyBlocked(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[100] = &model.Employee{ID: 100, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[200] = supervisorEmployee(200)

	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetNeedApproval(context.Background(), 200, false, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient rank")
}
```

- [ ] **3.5** Run tests:
```bash
cd user-service && go test ./internal/service/ -v
```

- [ ] **3.6** Build all affected services to verify compilation:
```bash
cd user-service && go build ./cmd
cd api-gateway && go build ./cmd
```

- [ ] **3.7** Commit: `feat(user-service): add hierarchy enforcement to ActuaryService`

---

## Task 4: Update Documentation

### Files
- **Modify:** `docs/api/REST_API.md`
- **Modify:** `Specification.md`

### Steps

- [ ] **4.1** Update `docs/api/REST_API.md` -- add a note to the Employee Limits and Actuary Management sections:

In the **Set Employee Limits** (`PUT /api/employees/:id/limits`) section, add under error responses:

```
| 403 | `forbidden` | Caller's role rank is not higher than target's | `"insufficient rank to modify this employee's limits"` |
| 403 | `forbidden` | Caller is the target employee | `"cannot modify own limits"` |
```

In the **Apply Limit Template** (`POST /api/employees/:id/limits/template`) section, add the same 403 entries.

In the **Set Actuary Limit** (`PUT /api/actuaries/:id/limit`) section, add the same 403 entries.

In the **Reset Actuary Used Limit** (`POST /api/actuaries/:id/reset-used-limit`) section, add the same 403 entries.

In the **Set Actuary Need Approval** (`PUT /api/actuaries/:id/need-approval`) section, add the same 403 entries.

Add a general note in the relevant sections:

```
> **Hierarchy enforcement:** The caller's highest role rank must be strictly greater than the
> target employee's highest role rank. Role ranks: EmployeeBasic (1), EmployeeAgent (2),
> EmployeeSupervisor (3), EmployeeAdmin (4). Self-modification is also blocked.
```

- [ ] **4.2** Update `Specification.md` to document the hierarchy enforcement rules in the appropriate section (business rules or role/permissions section):

Add a subsection "Role Hierarchy Enforcement" documenting:
- The rank map: EmployeeBasic=1, EmployeeAgent=2, EmployeeSupervisor=3, EmployeeAdmin=4
- The rule: caller's max role rank must be strictly greater than target's max role rank
- Self-modification is blocked
- Which endpoints are protected: SetEmployeeLimits, ApplyTemplate, SetActuaryLimit, ResetUsedLimit, SetNeedApproval
- Client limit operations are exempt (clients have no role rank)
- System-initiated changes (changedBy=0) bypass the check

- [ ] **4.3** Commit: `docs: document role hierarchy enforcement for limit operations`

---

## Summary of All Changes

| File | Change |
|------|--------|
| `user-service/internal/service/hierarchy.go` | **New.** `roleRanks` map, `maxRoleRank()`, `HierarchyEmpRepo` interface, `checkHierarchy()` |
| `user-service/internal/service/hierarchy_test.go` | **New.** Tests for `maxRoleRank` and `checkHierarchy` |
| `user-service/internal/service/limit_service.go` | Add `empRepo HierarchyEmpRepo` field, update constructor signature, add `checkHierarchy` call to `SetEmployeeLimits`, add `changedBy` param + `checkHierarchy` call to `ApplyTemplate` |
| `user-service/internal/service/limit_service_test.go` | Update constructor calls (add `empRepo`), update `ApplyTemplate` calls (add `changedBy`), add 4 hierarchy tests |
| `user-service/internal/service/actuary_service.go` | Add `changedBy int64` param to `SetActuaryLimit`, `ResetUsedLimit`, `SetNeedApproval`; add `checkHierarchy` call to each |
| `user-service/internal/service/actuary_service_test.go` | Update all call sites (add `changedBy=0`), add 5 hierarchy tests |
| `user-service/internal/handler/limit_handler.go` | Extract `changedBy` in `ApplyLimitTemplate` handler |
| `user-service/internal/handler/actuary_handler.go` | Extract `changedBy` in `SetActuaryLimit`, `ResetActuaryUsedLimit`, `SetNeedApproval` handlers; add `changelog` import |
| `user-service/cmd/main.go` | Pass `repo` to `NewLimitService` constructor |
| `api-gateway/internal/handler/actuary_handler.go` | Use `middleware.GRPCContextWithChangedBy(c)` in 3 mutating methods; add `middleware` import |
| `api-gateway/internal/handler/limit_handler.go` | Use `middleware.GRPCContextWithChangedBy(c)` in `ApplyLimitTemplate` |
| `docs/api/REST_API.md` | Document 403 hierarchy errors on 5 endpoints |
| `Specification.md` | Add Role Hierarchy Enforcement section |

---

## Error Mapping Note

The `mapServiceError` function in `user-service/internal/handler/grpc_handler.go` (line 36) already maps error messages containing `"permission"` to `codes.PermissionDenied`. Since the `checkHierarchy` function returns `status.Error(codes.PermissionDenied, ...)`, the gRPC status code is already set correctly and `mapServiceError` is not invoked for these errors (gRPC status errors pass through directly). However, if the error were to be re-wrapped as a plain error, the fallback would still work because "insufficient rank" does not match, but "cannot modify own limits" contains no matching keyword -- so it is important to return `status.Error` directly from `checkHierarchy`, not plain `errors.New`.

The gateway's `handleGRPCError` function reads the gRPC status code and maps `PermissionDenied` to HTTP 403, which is the correct behavior.

---

## Testing Checklist

- [ ] `cd user-service && go test ./internal/service/ -run TestMaxRoleRank -v` -- all pass
- [ ] `cd user-service && go test ./internal/service/ -run TestCheckHierarchy -v` -- all pass
- [ ] `cd user-service && go test ./internal/service/ -run TestSetEmployeeLimits -v` -- all pass (including hierarchy tests)
- [ ] `cd user-service && go test ./internal/service/ -run TestApplyTemplate -v` -- all pass (including hierarchy tests)
- [ ] `cd user-service && go test ./internal/service/ -run TestSetActuaryLimit -v` -- all pass (including hierarchy tests)
- [ ] `cd user-service && go test ./internal/service/ -run TestResetUsedLimit -v` -- all pass
- [ ] `cd user-service && go test ./internal/service/ -run TestSetNeedApproval -v` -- all pass
- [ ] `cd user-service && go test ./internal/service/ -v` -- full suite passes
- [ ] `cd user-service && go build ./cmd` -- compiles
- [ ] `cd api-gateway && go build ./cmd` -- compiles
- [ ] `make test` -- all tests across all services pass
