// service_extra_coverage_test.go
//
// Additional unit tests added to raise overall service-package coverage past
// the original 55.4% baseline. Each test stays inside the unit-test contract
// (mocked repos, no infrastructure) and exercises code paths that the
// pre-existing tests did not reach: error branches, default-limit role tiers,
// blueprint validation negatives, role permission grant/revoke, the
// supervisor-demotion outbox emission, and the limit-template CRUD surface.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/contract/permissions"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// nilRegistry returns a no-op Registry for unit tests that don't need
// pause/trigger control (nil PauseStore is explicitly supported).
func nilRegistry() *cronreg.Registry {
	return cronreg.NewRegistry("test", nil)
}

// -----------------------------------------------------------------------------
// shared helpers
// -----------------------------------------------------------------------------

// recordingChangelogRepo is an in-memory ChangelogRepo that captures every
// entry written to it. Used to assert that service methods record audit rows.
type recordingChangelogRepo struct {
	created    []changelog.Entry
	createdAll [][]changelog.Entry
	createErr  error
}

func newRecordingChangelogRepo() *recordingChangelogRepo {
	return &recordingChangelogRepo{}
}

func (r *recordingChangelogRepo) Create(entry changelog.Entry) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.created = append(r.created, entry)
	return nil
}

func (r *recordingChangelogRepo) CreateBatch(entries []changelog.Entry) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.createdAll = append(r.createdAll, entries)
	return nil
}

func (r *recordingChangelogRepo) ListByEntity(_ string, _ int64, _, _ int) ([]model.Changelog, int64, error) {
	return nil, 0, nil
}

// recordingOutbox is an in-memory OutboxInserter that captures inserts.
type recordingOutbox struct {
	rows   []*model.OutboxEvent
	insErr error
}

func (r *recordingOutbox) Insert(e *model.OutboxEvent) error {
	if r.insErr != nil {
		return r.insErr
	}
	r.rows = append(r.rows, e)
	return nil
}

// -----------------------------------------------------------------------------
// EmployeeService — UpdateEmployee field coverage
// -----------------------------------------------------------------------------

func TestUpdateEmployee_AllScalarFieldsApplied(t *testing.T) {
	repo := newMockRepo()
	repo.employees[5] = &model.Employee{
		ID:       5,
		LastName: "Old",
		Gender:   "M",
		Phone:    "111",
		Address:  "addr1",
		Position: "junior",
		JMBG:     "0101990710024",
	}
	cl := newRecordingChangelogRepo()
	svc := NewEmployeeService(repo, nil, nil, nil, cl)

	updated, err := svc.UpdateEmployee(context.Background(), 5, map[string]interface{}{
		"last_name":  "New",
		"gender":     "F",
		"phone":      "222",
		"address":    "addr2",
		"position":   "senior",
		"department": "ops",
	}, 7)
	require.NoError(t, err)
	assert.Equal(t, "New", updated.LastName)
	assert.Equal(t, "F", updated.Gender)
	assert.Equal(t, "222", updated.Phone)
	assert.Equal(t, "addr2", updated.Address)
	assert.Equal(t, "senior", updated.Position)
	assert.Equal(t, "ops", updated.Department)

	// Changelog batch should record a row per changed field.
	require.Len(t, cl.createdAll, 1, "expected one batch")
	require.Len(t, cl.createdAll[0], 6, "expected one entry per changed field")
}

func TestUpdateEmployee_RepoUpdateError(t *testing.T) {
	repo := &errUpdateEmployeeRepo{mockRepo: *newMockRepo(), updateErr: errors.New("write failed")}
	repo.employees[1] = &model.Employee{ID: 1, JMBG: "0101990710024"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.UpdateEmployee(context.Background(), 1, map[string]interface{}{"last_name": "X"}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

func TestUpdateEmployee_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewEmployeeService(repo, nil, nil, nil)

	_, err := svc.UpdateEmployee(context.Background(), 999, map[string]interface{}{"last_name": "X"}, 0)
	assert.Error(t, err)
}

// errUpdateEmployeeRepo wraps the standard mockRepo with a configurable Update error.
type errUpdateEmployeeRepo struct {
	mockRepo
	updateErr error
}

func (m *errUpdateEmployeeRepo) Update(emp *model.Employee) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	return m.mockRepo.Update(emp)
}

// -----------------------------------------------------------------------------
// EmployeeService — GetByIDs / ListEmployees variations
// -----------------------------------------------------------------------------

func TestGetByIDs_ReturnsRequestedSubset(t *testing.T) {
	repo := newMockRepo()
	repo.employees[1] = &model.Employee{ID: 1, FirstName: "A"}
	repo.employees[2] = &model.Employee{ID: 2, FirstName: "B"}
	repo.employees[3] = &model.Employee{ID: 3, FirstName: "C"}
	svc := NewEmployeeService(repo, nil, nil, nil)

	got, err := svc.GetByIDs([]int64{1, 3, 999})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// -----------------------------------------------------------------------------
// EmployeeService — SetEmployeeRoles / Permissions error branches
// -----------------------------------------------------------------------------

func TestSetEmployeeRoles_UnknownRoleRejected(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	emp := &model.Employee{Email: "x@x.com", JMBG: "0101990710024"}
	_ = repo.Create(emp)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{"NoSuchRole"}, 0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestSetEmployeeRoles_EmployeeNotFound(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	err := svc.SetEmployeeRoles(context.Background(), 999, []string{"EmployeeBasic"}, 0)
	assert.Error(t, err)
}

func TestSetEmployeeAdditionalPermissions_UnknownCodeRejected(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	emp := &model.Employee{Email: "y@x.com", JMBG: "0101990710024"}
	_ = repo.Create(emp)
	// Seed at least one perm so list is non-empty but the requested one is missing.
	_ = permRepo.Create(&model.Permission{Code: "clients.read.all"})

	err := svc.SetEmployeeAdditionalPermissions(context.Background(), emp.ID,
		[]string{"clients.read.all", "does.not.exist"}, 0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionNotInCatalog)
}

func TestSetEmployeeAdditionalPermissions_EmployeeNotFound(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	svc := NewEmployeeService(repo, nil, nil, roleSvc)

	err := svc.SetEmployeeAdditionalPermissions(context.Background(), 999, []string{"clients.read.all"}, 0)
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// EmployeeService — supervisor-demoted outbox emission
// -----------------------------------------------------------------------------

func TestEmitSupervisorDemoted_OutboxRowEmittedOnFundsLoss(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)

	// Seed default roles (Supervisor has funds.manage.catalog).
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	// Pre-create employee with EmployeeSupervisor role to seed funds.manage.catalog.
	supervisorRole, err := roleRepo.GetByName("EmployeeSupervisor")
	require.NoError(t, err)
	basicRole, err := roleRepo.GetByName("EmployeeBasic")
	require.NoError(t, err)

	emp := &model.Employee{
		Email: "demote@x.com",
		JMBG:  "0101990710024",
		Roles: []model.Role{*supervisorRole},
	}
	_ = repo.Create(emp)

	outbox := &recordingOutbox{}
	svc := NewEmployeeService(repo, nil, nil, roleSvc).WithOutbox(outbox)

	// Demote: replace roles with Basic. funds.manage.catalog goes away.
	err = svc.SetEmployeeRoles(context.Background(), emp.ID, []string{basicRole.Name}, 99)
	require.NoError(t, err)

	require.Len(t, outbox.rows, 1, "expected one supervisor-demoted outbox row")
	row := outbox.rows[0]
	assert.Equal(t, "user.supervisor-demoted", row.EventType)
	assert.NotEmpty(t, row.Payload)
}

func TestEmitSupervisorDemoted_NoOpWhenNoOutbox(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	supervisorRole, _ := roleRepo.GetByName("EmployeeSupervisor")
	basicRole, _ := roleRepo.GetByName("EmployeeBasic")
	emp := &model.Employee{Email: "x@x.com", JMBG: "0101990710024", Roles: []model.Role{*supervisorRole}}
	_ = repo.Create(emp)

	// No outbox attached — call must not panic.
	svc := NewEmployeeService(repo, nil, nil, roleSvc)
	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{basicRole.Name}, 0)
	assert.NoError(t, err)
}

func TestEmitSupervisorDemoted_NoEmissionWhenFundsRetained(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	supervisorRole, _ := roleRepo.GetByName("EmployeeSupervisor")
	adminRole, _ := roleRepo.GetByName("EmployeeAdmin")

	emp := &model.Employee{Email: "z@x.com", JMBG: "0101990710024", Roles: []model.Role{*supervisorRole}}
	_ = repo.Create(emp)

	outbox := &recordingOutbox{}
	svc := NewEmployeeService(repo, nil, nil, roleSvc).WithOutbox(outbox)

	// Promote to admin: funds.manage.catalog still held (admin has all perms).
	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{adminRole.Name}, 1)
	require.NoError(t, err)
	assert.Len(t, outbox.rows, 0, "no outbox emission when funds.manage.catalog is retained")
}

func TestEmitSupervisorDemoted_NoEmissionWhenFundsNeverHeld(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	basicRole, _ := roleRepo.GetByName("EmployeeBasic")
	agentRole, _ := roleRepo.GetByName("EmployeeAgent")
	emp := &model.Employee{Email: "n@x.com", JMBG: "0101990710024", Roles: []model.Role{*basicRole}}
	_ = repo.Create(emp)

	outbox := &recordingOutbox{}
	svc := NewEmployeeService(repo, nil, nil, roleSvc).WithOutbox(outbox)

	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{agentRole.Name}, 1)
	require.NoError(t, err)
	assert.Len(t, outbox.rows, 0, "no outbox emission when funds.manage.catalog was never held")
}

// -----------------------------------------------------------------------------
// EmployeeService — WithOutbox returns an independent copy
// -----------------------------------------------------------------------------

func TestWithOutbox_ReturnsCopy(t *testing.T) {
	repo := newMockRepo()
	original := NewEmployeeService(repo, nil, nil, nil)
	outbox := &recordingOutbox{}
	wired := original.WithOutbox(outbox)

	assert.NotSame(t, original, wired, "WithOutbox should return a distinct *EmployeeService")
	// Original outboxRepo should remain nil.
	assert.Nil(t, original.outboxRepo)
	assert.Equal(t, OutboxInserter(outbox), wired.outboxRepo)
}

// -----------------------------------------------------------------------------
// RoleService — AssignPermissionToRole / RevokePermissionFromRole
// -----------------------------------------------------------------------------

func TestAssignPermissionToRole_RoleNotFound(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	err := svc.AssignPermissionToRole(999, string(permissions.Catalog[0]))
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestAssignPermissionToRole_NotInCatalog(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "TestRole"}
	_ = roleRepo.Create(role)
	svc := NewRoleService(roleRepo, permRepo)

	err := svc.AssignPermissionToRole(role.ID, "not.a.real.permission.code.123")
	assert.ErrorIs(t, err, ErrPermissionNotInCatalog)
}

func TestAssignPermissionToRole_AppendsAndPublishes(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "EmployeeAgent"}
	_ = roleRepo.Create(role)
	roleRepo.employeesByRole[role.ID] = []int64{77}

	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	target := string(permissions.Catalog[0])

	err := svc.AssignPermissionToRole(role.ID, target)
	require.NoError(t, err)

	updatedRole, _ := roleRepo.GetByID(role.ID)
	require.Len(t, updatedRole.Permissions, 1)
	assert.Equal(t, target, updatedRole.Permissions[0].Code)

	require.Len(t, pub.calls, 1)
	assert.Equal(t, "assign_permission_to_role", pub.calls[0].Source)
}

func TestAssignPermissionToRole_Idempotent(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	target := string(permissions.Catalog[0])
	_ = permRepo.Create(&model.Permission{Code: target})
	role := &model.Role{Name: "Agent", Permissions: []model.Permission{{Code: target}}}
	_ = roleRepo.Create(role)

	svc := NewRoleService(roleRepo, permRepo)
	err := svc.AssignPermissionToRole(role.ID, target)
	require.NoError(t, err)

	got, _ := roleRepo.GetByID(role.ID)
	assert.Len(t, got.Permissions, 1, "duplicate assignment should be a no-op")
}

func TestRevokePermissionFromRole_RoleNotFound(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	err := svc.RevokePermissionFromRole(999, "clients.read.all")
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestRevokePermissionFromRole_NoOpWhenAbsent(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "Agent", Permissions: []model.Permission{{Code: "a.b.c"}}}
	_ = roleRepo.Create(role)
	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	err := svc.RevokePermissionFromRole(role.ID, "x.y.z")
	require.NoError(t, err)
	assert.Len(t, pub.calls, 0, "no event when nothing was revoked")
}

func TestRevokePermissionFromRole_RemovesAndPublishes(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{
		Name:        "Agent",
		Permissions: []model.Permission{{Code: "a.b.c"}, {Code: "x.y.z"}},
	}
	_ = roleRepo.Create(role)
	roleRepo.employeesByRole[role.ID] = []int64{1, 2}
	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	err := svc.RevokePermissionFromRole(role.ID, "a.b.c")
	require.NoError(t, err)

	got, _ := roleRepo.GetByID(role.ID)
	require.Len(t, got.Permissions, 1)
	assert.Equal(t, "x.y.z", got.Permissions[0].Code)
	require.Len(t, pub.calls, 1)
	assert.Equal(t, "revoke_permission_from_role", pub.calls[0].Source)
}

func TestRoleService_GetRoleAndListRoles(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "X"}
	_ = roleRepo.Create(role)
	svc := NewRoleService(roleRepo, permRepo)

	got, err := svc.GetRole(role.ID)
	require.NoError(t, err)
	assert.Equal(t, "X", got.Name)

	all, err := svc.ListRoles()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestRoleService_GetRole_NotFound(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	_, err := svc.GetRole(123)
	assert.Error(t, err)
}

func TestRoleService_PublishRolePermissionsChanged_NoEmployeesSkipsPublish(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "Empty"}
	_ = roleRepo.Create(role)
	// employeesByRole is empty for this role.

	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	err := svc.UpdateRolePermissions(role.ID, []string{})
	require.NoError(t, err)
	assert.Len(t, pub.calls, 0, "publish must be skipped when no employees hold the role")
}

func TestRoleService_CreateRole_PublishesEvent(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	target := string(permissions.Catalog[0])
	_ = permRepo.Create(&model.Permission{Code: target})

	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	role, err := svc.CreateRole("FreshRole", "desc", []string{target})
	require.NoError(t, err)
	require.NotNil(t, role)

	// New role has no employees yet → publish skipped.
	assert.Len(t, pub.calls, 0)
}

// -----------------------------------------------------------------------------
// RoleService — SeedRolesAndPermissions no-op without DB
// -----------------------------------------------------------------------------

func TestSeedRolesAndPermissions_NoDBIsNoOp(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo) // no DB wired

	err := svc.SeedRolesAndPermissions()
	assert.NoError(t, err)
	all, _ := roleRepo.List()
	assert.Empty(t, all, "no DB → seeding must be a no-op")
}

// -----------------------------------------------------------------------------
// LimitService — applyDefaultLimits (all role tiers) via GetEmployeeLimits
// -----------------------------------------------------------------------------

func TestGetEmployeeLimits_RoleBasedDefaults(t *testing.T) {
	cases := []struct {
		role           string
		expectedLoan   int64
		expectedSingle int64
	}{
		{"EmployeeBasic", 50_000, 100_000},
		{"EmployeeAgent", 500_000, 1_000_000},
		{"EmployeeSupervisor", 5_000_000, 10_000_000},
		{"EmployeeAdmin", 999_999_999, 999_999_999},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			limitRepo := newMockEmployeeLimitRepo()
			templateRepo := newMockLimitTemplateRepo()
			empRepo := newMockHierarchyEmpRepo()
			empRepo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: c.role}}}
			svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

			limit, err := svc.GetEmployeeLimits(1)
			require.NoError(t, err)
			assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(c.expectedLoan)),
				"loan amount mismatch for %s: got %s", c.role, limit.MaxLoanApprovalAmount)
			assert.True(t, limit.MaxSingleTransaction.Equal(decimal.NewFromInt(c.expectedSingle)),
				"single tx mismatch for %s: got %s", c.role, limit.MaxSingleTransaction)
		})
	}
}

func TestGetEmployeeLimits_ExistingRecordSkipsDefaults(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	limitRepo.limits[10] = &model.EmployeeLimit{
		ID:                    99, // non-zero ID — defaults must NOT apply
		EmployeeID:            10,
		MaxLoanApprovalAmount: decimal.NewFromInt(7),
	}
	empRepo := newMockHierarchyEmpRepo()
	empRepo.employees[10] = &model.Employee{ID: 10, Roles: []model.Role{{Name: "EmployeeAdmin"}}}
	svc := NewLimitService(limitRepo, newMockLimitTemplateRepo(), empRepo, nil)

	limit, err := svc.GetEmployeeLimits(10)
	require.NoError(t, err)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(7)),
		"defaults must not be applied when a real record exists")
}

func TestGetEmployeeLimits_RepoErrorPropagates(t *testing.T) {
	limitRepo := &errLimitRepo{getErr: errors.New("db down")}
	svc := NewLimitService(limitRepo, newMockLimitTemplateRepo(), newMockHierarchyEmpRepo(), nil)

	_, err := svc.GetEmployeeLimits(5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// errLimitRepo is a minimal EmployeeLimitRepo whose GetByEmployeeID always errors.
type errLimitRepo struct {
	getErr    error
	upsertErr error
}

func (e *errLimitRepo) Create(*model.EmployeeLimit) error                   { return nil }
func (e *errLimitRepo) GetByEmployeeID(int64) (*model.EmployeeLimit, error) { return nil, e.getErr }
func (e *errLimitRepo) Update(*model.EmployeeLimit) error                   { return nil }
func (e *errLimitRepo) Delete(int64) error                                  { return nil }
func (e *errLimitRepo) Upsert(l *model.EmployeeLimit) error                 { return e.upsertErr }
func (e *errLimitRepo) ResetDailyUsedLimits() error                         { return nil }

// -----------------------------------------------------------------------------
// LimitService — Set/Apply error paths and template CRUD
// -----------------------------------------------------------------------------

func TestSetEmployeeLimits_HierarchyDenied(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	empRepo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}
	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

	_, err := svc.SetEmployeeLimits(context.Background(), model.EmployeeLimit{EmployeeID: 2}, 1)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestSetEmployeeLimits_RecordsChangelogWhenOldExists(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	limitRepo.limits[5] = &model.EmployeeLimit{
		EmployeeID:            5,
		MaxLoanApprovalAmount: decimal.NewFromInt(1),
		MaxSingleTransaction:  decimal.NewFromInt(2),
		MaxDailyTransaction:   decimal.NewFromInt(3),
		MaxClientDailyLimit:   decimal.NewFromInt(4),
		MaxClientMonthlyLimit: decimal.NewFromInt(5),
	}
	cl := newRecordingChangelogRepo()
	svc := NewLimitService(limitRepo, newMockLimitTemplateRepo(), newMockHierarchyEmpRepo(), nil, cl)

	_, err := svc.SetEmployeeLimits(context.Background(), model.EmployeeLimit{
		EmployeeID:            5,
		MaxLoanApprovalAmount: decimal.NewFromInt(100),
		MaxSingleTransaction:  decimal.NewFromInt(200),
		MaxDailyTransaction:   decimal.NewFromInt(300),
		MaxClientDailyLimit:   decimal.NewFromInt(400),
		MaxClientMonthlyLimit: decimal.NewFromInt(500),
	}, 0)
	require.NoError(t, err)
	require.Len(t, cl.createdAll, 1)
	assert.NotEmpty(t, cl.createdAll[0])
}

func TestApplyTemplate_HierarchyDenied(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	empRepo := newMockHierarchyEmpRepo()
	empRepo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeAgent"}}}
	empRepo.employees[2] = &model.Employee{ID: 2, Roles: []model.Role{{Name: "EmployeeSupervisor"}}}
	svc := NewLimitService(limitRepo, templateRepo, empRepo, nil)

	_, err := svc.ApplyTemplate(context.Background(), 2, "BasicTeller", 1)
	assert.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestLimitService_TemplateCRUD(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)

	// Create
	created, err := svc.CreateTemplate(context.Background(), model.LimitTemplate{
		Name:                  "Custom",
		Description:           "custom desc",
		MaxLoanApprovalAmount: decimal.NewFromInt(1000),
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.NotZero(t, created.ID)
	assert.Equal(t, "Custom", created.Name)

	// List
	all, err := svc.ListTemplates()
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// Update
	created.Description = "updated"
	created.MaxLoanApprovalAmount = decimal.NewFromInt(9999)
	updated, err := svc.UpdateTemplate(context.Background(), *created)
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Description)
	assert.True(t, updated.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(9999)))

	// Delete
	err = svc.DeleteTemplate(context.Background(), created.ID)
	require.NoError(t, err)
	all, _ = svc.ListTemplates()
	assert.Empty(t, all)

	// Update of missing template returns error
	_, err = svc.UpdateTemplate(context.Background(), model.LimitTemplate{ID: 9999, Name: "ghost"})
	assert.Error(t, err)

	// Delete of missing template returns error
	err = svc.DeleteTemplate(context.Background(), 9999)
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// BlueprintService — additional validation, applied-event side effects, errors
// -----------------------------------------------------------------------------

func TestCreateBlueprint_NameRequired(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	_, err := svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidBlueprint)
}

func TestCreateBlueprint_ActuaryValidation(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// missing limit
	_, err := svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "A", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{}),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "limit is required")

	// non-decimal limit
	_, err = svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "A2", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "abc"}),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "limit must be a valid decimal")

	// valid actuary
	created, err := svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "OK", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000", NeedApproval: false}),
	})
	require.NoError(t, err)
	assert.NotZero(t, created.ID)
}

func TestCreateBlueprint_ClientValidation(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// missing field
	_, err := svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "C", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{DailyLimit: "1", MonthlyLimit: "1"}),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transfer_limit")

	// non-decimal field
	_, err = svc.CreateBlueprint(context.Background(), model.LimitBlueprint{
		Name: "C2", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{DailyLimit: "x", MonthlyLimit: "1", TransferLimit: "1"}),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daily_limit")
}

func TestListBlueprints_InvalidTypeFilter(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	_, err := svc.ListBlueprints("nope")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidBlueprint)
}

func TestApplyBlueprint_ClientWithoutGRPCClient(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil) // nil clientClient

	bp := model.LimitBlueprint{
		Name: "C", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{
			DailyLimit: "1", MonthlyLimit: "1", TransferLimit: "1",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 1, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client-service gRPC client not configured")
}

func TestApplyBlueprint_ClientGRPCError(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	clientClient := &mockClientLimitClient{err: errors.New("rpc broke")}
	svc := NewBlueprintService(bpRepo, nil, nil, clientClient, nil, nil)

	bp := model.LimitBlueprint{
		Name: "ClientErr", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{
			DailyLimit: "1", MonthlyLimit: "2", TransferLimit: "3",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 11, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rpc broke")
}

func TestApplyBlueprint_RecordsChangelog(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	cl := newRecordingChangelogRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, cl)

	bp := model.LimitBlueprint{
		Name: "WithChangelog", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 8, 99)
	require.NoError(t, err)
	require.Len(t, cl.createdAll, 1)
	assert.Equal(t, "blueprint_applied", cl.createdAll[0][0].FieldName)
	// changelog.Diff stores values as JSON so a string round-trips through quotes.
	assert.Contains(t, cl.createdAll[0][0].NewValue, "WithChangelog")
}

func TestApplyBlueprint_EmployeeMalformedJSON(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, nil)

	// Insert a blueprint directly bypassing validateValues so we can corrupt the payload.
	bp := &model.LimitBlueprint{
		Name: "Corrupt", Type: model.BlueprintTypeEmployee,
		Values: datatypes.JSON("not-json"),
	}
	require.NoError(t, bpRepo.Create(bp))

	err := svc.ApplyBlueprint(context.Background(), bp.ID, 1, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

func TestApplyBlueprint_ActuaryMalformedJSON(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	actuaryRepo := newMockActuaryRepoWithUpsert()
	svc := NewBlueprintService(bpRepo, nil, actuaryRepo, nil, nil, nil)

	bp := &model.LimitBlueprint{
		Name: "ActCorrupt", Type: model.BlueprintTypeActuary,
		Values: datatypes.JSON("garbage"),
	}
	require.NoError(t, bpRepo.Create(bp))

	err := svc.ApplyBlueprint(context.Background(), bp.ID, 1, 1)
	assert.Error(t, err)
}

func TestApplyBlueprint_ClientMalformedJSON(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	cc := &mockClientLimitClient{}
	svc := NewBlueprintService(bpRepo, nil, nil, cc, nil, nil)

	bp := &model.LimitBlueprint{
		Name: "ClientCorrupt", Type: model.BlueprintTypeClient,
		Values: datatypes.JSON("nope"),
	}
	require.NoError(t, bpRepo.Create(bp))

	err := svc.ApplyBlueprint(context.Background(), bp.ID, 1, 1)
	assert.Error(t, err)
	assert.Len(t, cc.calls, 0, "no gRPC call should be made on parse error")
}

func TestApplyBlueprint_UnknownType(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// Insert a blueprint with an unknown type (validation bypassed).
	bp := &model.LimitBlueprint{
		Name: "Unknown", Type: "weird-type",
		Values: datatypes.JSON(`{}`),
	}
	require.NoError(t, bpRepo.Create(bp))

	err := svc.ApplyBlueprint(context.Background(), bp.ID, 1, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown blueprint type")
}

func TestUpdateBlueprint_NotFound(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	_, err := svc.UpdateBlueprint(context.Background(), 99999, "n", "d", json.RawMessage(`{}`))
	assert.Error(t, err)
}

func TestUpdateBlueprint_InvalidValuesRejected(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "U", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000"}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	// Updating with malformed JSON should fail validateValues.
	_, err := svc.UpdateBlueprint(context.Background(), created.ID, "U", "d",
		json.RawMessage(`{"limit":"not-a-decimal"}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "limit must be a valid decimal")
}

func TestDeleteBlueprint_NotFound(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	err := svc.DeleteBlueprint(context.Background(), 9999)
	assert.Error(t, err)
}

func TestSeedFromTemplates_PropagatesNonNotFoundErr(t *testing.T) {
	bpRepo := &errBlueprintRepo{
		mockBlueprintRepo: *newMockBlueprintRepo(),
		getByNameErr:      errors.New("DB down"),
	}
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	err := svc.SeedFromTemplates([]model.LimitTemplate{{Name: "X"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DB down")
}

// errBlueprintRepo lets us inject a non-NotFound error from GetByNameAndType so
// SeedFromTemplates can be exercised on the err-propagation branch.
type errBlueprintRepo struct {
	mockBlueprintRepo
	getByNameErr error
}

func (e *errBlueprintRepo) GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error) {
	if e.getByNameErr != nil && !errors.Is(e.getByNameErr, gorm.ErrRecordNotFound) {
		return nil, e.getByNameErr
	}
	return e.mockBlueprintRepo.GetByNameAndType(name, bpType)
}

// -----------------------------------------------------------------------------
// ActuaryService — BackfillDefaultLimits and supplemental branches
// -----------------------------------------------------------------------------

func TestActuaryService_ListActuariesPassthrough(t *testing.T) {
	actuaryRepo := &listingActuaryRepo{
		rows:  []model.ActuaryRow{{EmployeeID: 1}, {EmployeeID: 2}},
		total: 2,
	}
	svc := NewActuaryService(actuaryRepo, newMockActuaryEmpRepo(), nil)

	rows, total, err := svc.ListActuaries("", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)
}

// listingActuaryRepo is an ActuaryRepo whose ListActuaries returns canned rows.
type listingActuaryRepo struct {
	mockActuaryRepo
	rows  []model.ActuaryRow
	total int64
	err   error
}

func newListingActuaryRepoFromMock(m *mockActuaryRepo) *listingActuaryRepo {
	return &listingActuaryRepo{mockActuaryRepo: *m}
}

func (l *listingActuaryRepo) ListActuaries(string, string, int, int) ([]model.ActuaryRow, int64, error) {
	return l.rows, l.total, l.err
}

func TestActuaryService_BackfillDefaultLimits_SeedsZeroRows(t *testing.T) {
	mock := newMockActuaryRepo()
	// Pre-create an actuary limit row with Limit=0.
	_ = mock.Create(&model.ActuaryLimit{EmployeeID: 1, Limit: decimal.NewFromInt(0)})

	listingRepo := newListingActuaryRepoFromMock(mock)
	listingRepo.rows = []model.ActuaryRow{{EmployeeID: 1, Limit: 0}}
	listingRepo.total = 1

	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[1] = agentEmployee(1)

	svc := NewActuaryService(listingRepo, empRepo, nil)
	err := svc.BackfillDefaultLimits()
	require.NoError(t, err)

	persisted, err := mock.GetByEmployeeID(1)
	require.NoError(t, err)
	assert.True(t, persisted.Limit.Equal(decimal.NewFromInt(5_000_000)),
		"agent default 5M should be backfilled, got %s", persisted.Limit)
}

func TestActuaryService_BackfillDefaultLimits_SkipsNonZero(t *testing.T) {
	mock := newMockActuaryRepo()
	listingRepo := newListingActuaryRepoFromMock(mock)
	// Row with non-zero Limit must be skipped.
	listingRepo.rows = []model.ActuaryRow{{EmployeeID: 7, Limit: 12345}}
	listingRepo.total = 1

	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[7] = agentEmployee(7)

	svc := NewActuaryService(listingRepo, empRepo, nil)
	err := svc.BackfillDefaultLimits()
	require.NoError(t, err)
	// Nothing was upserted because the row was skipped — no record in mock.
	_, err = mock.GetByEmployeeID(7)
	assert.Error(t, err, "no backfill should occur for non-zero limits")
}

func TestActuaryService_BackfillDefaultLimits_ListError(t *testing.T) {
	mock := newMockActuaryRepo()
	listingRepo := newListingActuaryRepoFromMock(mock)
	listingRepo.err = errors.New("list failed")

	svc := NewActuaryService(listingRepo, newMockActuaryEmpRepo(), nil)
	err := svc.BackfillDefaultLimits()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list failed")
}

func TestActuaryService_HierarchyDeniedOnSet(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[1] = agentEmployee(1)
	empRepo.employees[2] = supervisorEmployee(2) // target outranks caller
	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	_, err := svc.SetActuaryLimit(context.Background(), 2, decimal.NewFromInt(1), 1)
	assert.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

// -----------------------------------------------------------------------------
// LimitCronService — goroutine starts and respects ctx cancellation
// -----------------------------------------------------------------------------

func TestLimitCronService_StartHonorsCancellation(t *testing.T) {
	repo := newMockEmployeeLimitRepo()
	svc := NewLimitCronService(repo, nilRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	// Cancel immediately. The goroutine should observe ctx.Done() at the
	// next select tick and return without invoking the reset.
	cancel()
	// Give the scheduler a moment to react. We don't assert a specific
	// behavior beyond non-blocking — Start must return synchronously.
}

func TestNewLimitCronService_NotNil(t *testing.T) {
	repo := newMockEmployeeLimitRepo()
	svc := NewLimitCronService(repo, nilRegistry())
	assert.NotNil(t, svc)
}

// -----------------------------------------------------------------------------
// EmployeeService — changelog recording on role / additional-permission updates
// -----------------------------------------------------------------------------

func TestSetEmployeeRoles_RecordsChangelog(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	cl := newRecordingChangelogRepo()
	svc := NewEmployeeService(repo, nil, nil, roleSvc, cl)

	emp := &model.Employee{Email: "cl@x.com", JMBG: "0101990710024"}
	_ = repo.Create(emp)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	err := svc.SetEmployeeRoles(context.Background(), emp.ID, []string{"EmployeeBasic"}, 7)
	require.NoError(t, err)

	require.Len(t, cl.created, 1)
	assert.Equal(t, "employee", cl.created[0].EntityType)
	assert.Equal(t, "roles", cl.created[0].FieldName)
}

func TestSetEmployeeAdditionalPermissions_RecordsChangelog(t *testing.T) {
	repo := newMockRepo()
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	roleSvc := NewRoleService(roleRepo, permRepo)
	cl := newRecordingChangelogRepo()
	svc := NewEmployeeService(repo, nil, nil, roleSvc, cl)

	emp := &model.Employee{Email: "ap@x.com", JMBG: "0101990710024"}
	_ = repo.Create(emp)
	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	err := svc.SetEmployeeAdditionalPermissions(context.Background(), emp.ID, []string{"clients.read.all"}, 7)
	require.NoError(t, err)

	require.Len(t, cl.created, 1)
	assert.Equal(t, "additional_permissions", cl.created[0].FieldName)
}

// -----------------------------------------------------------------------------
// LimitService — apply template repo errors
// -----------------------------------------------------------------------------

func TestApplyTemplate_UpsertError(t *testing.T) {
	limitRepo := &errLimitRepo{upsertErr: errors.New("upsert failed")}
	templateRepo := newMockLimitTemplateRepo()
	tmpl := &model.LimitTemplate{Name: "T", MaxLoanApprovalAmount: decimal.NewFromInt(1)}
	_ = templateRepo.Create(tmpl)

	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)
	_, err := svc.ApplyTemplate(context.Background(), 5, "T", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upsert failed")
}

func TestSetEmployeeLimits_UpsertError(t *testing.T) {
	limitRepo := &errLimitRepo{upsertErr: errors.New("write boom")}
	svc := NewLimitService(limitRepo, newMockLimitTemplateRepo(), newMockHierarchyEmpRepo(), nil)

	_, err := svc.SetEmployeeLimits(context.Background(), model.EmployeeLimit{EmployeeID: 5}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write boom")
}

// -----------------------------------------------------------------------------
// ActuaryService — additional repo error / persistence branches
// -----------------------------------------------------------------------------

func TestActuaryService_ResetUsedLimit_SaveError(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[1] = agentEmployee(1)
	_ = actuaryRepo.Create(&model.ActuaryLimit{EmployeeID: 1, UsedLimit: decimal.NewFromInt(10)})
	actuaryRepo.saveErr = errors.New("save failed")

	svc := NewActuaryService(actuaryRepo, empRepo, nil)
	_, err := svc.ResetUsedLimit(context.Background(), 1, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "save failed")
}

func TestActuaryService_SetNeedApproval_SaveError(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[1] = agentEmployee(1)
	_ = actuaryRepo.Create(&model.ActuaryLimit{EmployeeID: 1})
	actuaryRepo.saveErr = errors.New("save failed")

	svc := NewActuaryService(actuaryRepo, empRepo, nil)
	_, err := svc.SetNeedApproval(context.Background(), 1, true, 0)
	assert.Error(t, err)
}

func TestActuaryService_UpdateUsedLimit_NotFound(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	svc := NewActuaryService(actuaryRepo, newMockActuaryEmpRepo(), nil)

	_, err := svc.UpdateUsedLimit(context.Background(), 9999, decimal.NewFromInt(1))
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// ActuaryService.BackfillDefaultLimits — additional branches
// -----------------------------------------------------------------------------

func TestBackfill_EmployeeLookupFailureIsSkipped(t *testing.T) {
	mock := newMockActuaryRepo()
	listingRepo := newListingActuaryRepoFromMock(mock)
	listingRepo.rows = []model.ActuaryRow{{EmployeeID: 42, Limit: 0}}
	listingRepo.total = 1

	// empRepo is empty — lookup of employee 42 fails. Backfill should
	// log + skip, not error out.
	svc := NewActuaryService(listingRepo, newMockActuaryEmpRepo(), nil)
	err := svc.BackfillDefaultLimits()
	require.NoError(t, err, "backfill should not error when an employee lookup fails")
}

func TestBackfill_DefaultZeroSkips(t *testing.T) {
	mock := newMockActuaryRepo()
	listingRepo := newListingActuaryRepoFromMock(mock)
	listingRepo.rows = []model.ActuaryRow{{EmployeeID: 1, Limit: 0}}
	listingRepo.total = 1

	empRepo := newMockActuaryEmpRepo()
	// Employee with EmployeeBasic role → defaultActuaryLimit returns 0 → skip.
	empRepo.employees[1] = &model.Employee{ID: 1, Roles: []model.Role{{Name: "EmployeeBasic"}}}

	svc := NewActuaryService(listingRepo, empRepo, nil)
	err := svc.BackfillDefaultLimits()
	require.NoError(t, err)
}

// -----------------------------------------------------------------------------
// ApplyTemplate — non-NotFound DB error from templateRepo
// -----------------------------------------------------------------------------

func TestApplyTemplate_TemplateRepoNonNotFoundErr(t *testing.T) {
	templateRepo := &errTemplateRepo{getByNameErr: errors.New("db down")}
	svc := NewLimitService(newMockEmployeeLimitRepo(), templateRepo, newMockHierarchyEmpRepo(), nil)

	_, err := svc.ApplyTemplate(context.Background(), 1, "Anything", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
	assert.NotErrorIs(t, err, ErrTemplateNotFound, "non-NotFound errors must not be remapped")
}

// errTemplateRepo lets us inject a non-NotFound error from GetByName.
type errTemplateRepo struct {
	mockLimitTemplateRepo
	getByNameErr error
}

func (e *errTemplateRepo) GetByName(name string) (*model.LimitTemplate, error) {
	if e.getByNameErr != nil {
		return nil, e.getByNameErr
	}
	return e.mockLimitTemplateRepo.GetByName(name)
}

func TestActuaryService_AdminGetsUnlimitedDefault(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	empRepo := newMockActuaryEmpRepo()
	empRepo.employees[10] = &model.Employee{
		ID:    10,
		Roles: []model.Role{{Name: "EmployeeAdmin"}},
	}
	svc := NewActuaryService(actuaryRepo, empRepo, nil)

	limit, _, err := svc.GetActuaryInfo(10)
	require.NoError(t, err)
	assert.True(t, limit.Limit.Equal(decimal.NewFromInt(999_999_999)))
	assert.False(t, limit.NeedApproval, "admin treated as supervisor → NeedApproval=false")
}
