// handler_extra_test.go — additional handler-layer tests covering surface
// previously left at 0% (ListEmployeeFullNames, ListChangelog, NewXXXHandler
// constructors) and a couple of error branches missed by the existing suites.
package handler

import (
	"context"
	"errors"
	"testing"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

// extEmpSvc extends mockEmpSvc-style behavior with a configurable GetByIDs
// implementation so ListEmployeeFullNames can be exercised directly.
type extEmpSvc struct {
	mockEmpSvc
	getByIDsFn func(ids []int64) ([]model.Employee, error)
}

func (m *extEmpSvc) GetByIDs(ids []int64) ([]model.Employee, error) {
	if m.getByIDsFn != nil {
		return m.getByIDsFn(ids)
	}
	return nil, nil
}

// -----------------------------------------------------------------------------
// ListEmployeeFullNames
// -----------------------------------------------------------------------------

func TestListEmployeeFullNames_EmptyInputReturnsEmptyMap(t *testing.T) {
	h := newUserHandlerForTest(&extEmpSvc{}, &mockRoleSvc{})

	resp, err := h.ListEmployeeFullNames(context.Background(),
		&pb.ListEmployeeFullNamesRequest{EmployeeIds: nil})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.NamesById)
}

func TestListEmployeeFullNames_HappyPath(t *testing.T) {
	svc := &extEmpSvc{
		getByIDsFn: func(ids []int64) ([]model.Employee, error) {
			return []model.Employee{
				{ID: 1, FirstName: "Ana", LastName: "Jovic"},
				{ID: 2, FirstName: "Marko", LastName: "Petrovic"},
			}, nil
		},
	}
	h := newUserHandlerForTest(svc, &mockRoleSvc{})

	resp, err := h.ListEmployeeFullNames(context.Background(),
		&pb.ListEmployeeFullNamesRequest{EmployeeIds: []int64{1, 2}})
	require.NoError(t, err)
	assert.Equal(t, "Ana Jovic", resp.NamesById[1])
	assert.Equal(t, "Marko Petrovic", resp.NamesById[2])
}

func TestListEmployeeFullNames_ServiceError(t *testing.T) {
	svc := &extEmpSvc{
		getByIDsFn: func(ids []int64) ([]model.Employee, error) {
			return nil, errors.New("db down")
		},
	}
	h := newUserHandlerForTest(svc, &mockRoleSvc{})

	_, err := h.ListEmployeeFullNames(context.Background(),
		&pb.ListEmployeeFullNamesRequest{EmployeeIds: []int64{1}})
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, grpcCode(err))
}

// -----------------------------------------------------------------------------
// SetEmployeeRoles — additional GetEmployee error branch
// -----------------------------------------------------------------------------

func TestSetEmployeeRoles_GetEmployeeFails(t *testing.T) {
	emp := &mockEmpSvc{
		setRolesFn: func(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
			return nil
		},
		getFn: func(id int64) (*model.Employee, error) {
			return nil, errors.New("not found")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.SetEmployeeRoles(context.Background(), &pb.SetEmployeeRolesRequest{
		EmployeeId: 5,
		RoleNames:  []string{"EmployeeBasic"},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.NotFound, grpcCode(err))
}

func TestSetEmployeeAdditionalPermissions_GetEmployeeFails(t *testing.T) {
	emp := &mockEmpSvc{
		setPermsFn: func(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
			return nil
		},
		getFn: func(id int64) (*model.Employee, error) {
			return nil, errors.New("missing")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.SetEmployeeAdditionalPermissions(context.Background(), &pb.SetEmployeePermissionsRequest{
		EmployeeId:      7,
		PermissionCodes: []string{"clients.read.all"},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.NotFound, grpcCode(err))
}

// -----------------------------------------------------------------------------
// UpdateRolePermissions — GetRole-after-update error
// -----------------------------------------------------------------------------

func TestUpdateRolePermissions_GetRoleAfterFails(t *testing.T) {
	roleSvc := &mockRoleSvc{
		updateRoleFn: func(roleID int64, perms []string) error { return nil },
		getRoleFn: func(id int64) (*model.Role, error) {
			return nil, errors.New("vanished")
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.UpdateRolePermissions(context.Background(), &pb.UpdateRolePermissionsRequest{RoleId: 1})
	assert.Error(t, err)
	assert.Equal(t, codes.NotFound, grpcCode(err))
}

// -----------------------------------------------------------------------------
// ListChangelog — exercised via the concrete service constructor.
// -----------------------------------------------------------------------------

func TestListChangelog_ValidationError(t *testing.T) {
	// Concrete service is fine to wire here: validation rejects empty
	// entity_type before the repo is touched.
	clSvc := service.NewChangelogService(nil)
	h := &UserGRPCHandler{changelogService: clSvc}

	_, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "",
		EntityId:   1,
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, grpcCode(err))
}

// -----------------------------------------------------------------------------
// Constructor smoke tests — ensure NewXXXGRPCHandler returns non-nil.
// -----------------------------------------------------------------------------

func TestConstructors_ReturnNonNil(t *testing.T) {
	// All handler constructors take pointer-typed concrete dependencies;
	// passing nil is fine because the returned struct is just a wrapper.
	assert.NotNil(t, NewUserGRPCHandler(nil, nil, nil))
	assert.NotNil(t, NewBlueprintGRPCHandler(nil))
	assert.NotNil(t, NewLimitGRPCHandler(nil))
	assert.NotNil(t, NewActuaryGRPCHandler(nil))
}

// -----------------------------------------------------------------------------
// LimitHandler — ApplyLimitTemplate / ListLimitTemplates additional branch
// -----------------------------------------------------------------------------

func TestSetEmployeeLimits_AllInvalidDecimals(t *testing.T) {
	cases := []struct {
		name string
		req  *pb.SetEmployeeLimitsRequest
	}{
		{"max_single_transaction", &pb.SetEmployeeLimitsRequest{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "bad",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}},
		{"max_daily_transaction", &pb.SetEmployeeLimitsRequest{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "bad", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}},
		{"max_client_daily_limit", &pb.SetEmployeeLimitsRequest{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "bad", MaxClientMonthlyLimit: "1",
		}},
		{"max_client_monthly_limit", &pb.SetEmployeeLimitsRequest{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "bad",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLimitHandlerForTest(&mockLimitSvc{})
			_, err := h.SetEmployeeLimits(context.Background(), tc.req)
			assert.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, grpcCode(err))
		})
	}
}

func TestCreateLimitTemplate_AllInvalidDecimals(t *testing.T) {
	cases := []struct {
		name string
		req  *pb.CreateLimitTemplateRequest
	}{
		{"max_single_transaction", &pb.CreateLimitTemplateRequest{
			Name: "x", MaxLoanApprovalAmount: "1", MaxSingleTransaction: "bad",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}},
		{"max_daily_transaction", &pb.CreateLimitTemplateRequest{
			Name: "x", MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "bad", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}},
		{"max_client_daily_limit", &pb.CreateLimitTemplateRequest{
			Name: "x", MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "bad", MaxClientMonthlyLimit: "1",
		}},
		{"max_client_monthly_limit", &pb.CreateLimitTemplateRequest{
			Name: "x", MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "bad",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLimitHandlerForTest(&mockLimitSvc{})
			_, err := h.CreateLimitTemplate(context.Background(), tc.req)
			assert.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, grpcCode(err))
		})
	}
}
