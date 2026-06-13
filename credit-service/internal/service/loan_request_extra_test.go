package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/credit-service/internal/model"
)

// ---- mockEmployeeLimitClient ------------------------------------------------

type mockEmployeeLimitClient struct {
	limits *userpb.EmployeeLimitResponse
	err    error
}

func (m *mockEmployeeLimitClient) GetEmployeeLimits(_ context.Context, _ *userpb.EmployeeLimitRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return m.limits, m.err
}
func (m *mockEmployeeLimitClient) SetEmployeeLimits(_ context.Context, _ *userpb.SetEmployeeLimitsRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return nil, nil
}
func (m *mockEmployeeLimitClient) ApplyLimitTemplate(_ context.Context, _ *userpb.ApplyLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return nil, nil
}
func (m *mockEmployeeLimitClient) ListLimitTemplates(_ context.Context, _ *userpb.ListLimitTemplatesRequest, _ ...grpc.CallOption) (*userpb.ListLimitTemplatesResponse, error) {
	return nil, nil
}
func (m *mockEmployeeLimitClient) CreateLimitTemplate(_ context.Context, _ *userpb.CreateLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.LimitTemplateResponse, error) {
	return nil, nil
}

func TestApproveLoanRequest_ExceedsEmployeeApprovalLimit(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	limitClient := &mockEmployeeLimitClient{
		limits: &userpb.EmployeeLimitResponse{MaxLoanApprovalAmount: "100000"},
	}
	svc.limitClient = limitClient

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(500000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-LIMIT-1", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	// Employee with max-approval 100k tries to approve 500k loan -> fails
	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 7)
	require.Error(t, err)
	assert.Nil(t, loan)
	assert.True(t, errors.Is(err, ErrAmountExceedsApprovalLimit), "expected ErrAmountExceedsApprovalLimit, got %v", err)
}

func TestApproveLoanRequest_WithinEmployeeApprovalLimit(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	limitClient := &mockEmployeeLimitClient{
		limits: &userpb.EmployeeLimitResponse{MaxLoanApprovalAmount: "1000000"},
	}
	svc.limitClient = limitClient

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-LIMIT-2", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 7)
	require.NoError(t, err)
	require.NotNil(t, loan)
	assert.Equal(t, "approved", loan.Status)
}

func TestApproveLoanRequest_LimitClientErrorAllowsApproval(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	// gRPC error from limit client must NOT block approval (advisory check).
	limitClient := &mockEmployeeLimitClient{err: errors.New("limit lookup failed")}
	svc.limitClient = limitClient

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-LIMIT-3", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 7)
	require.NoError(t, err)
	require.NotNil(t, loan)
}

func TestApproveLoanRequest_ZeroLimitTreatedAsUnset(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	// "0" should be treated as "no limit" (skip the check).
	limitClient := &mockEmployeeLimitClient{
		limits: &userpb.EmployeeLimitResponse{MaxLoanApprovalAmount: "0"},
	}
	svc.limitClient = limitClient

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(10000000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-LIMIT-4", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 7)
	require.NoError(t, err)
	require.NotNil(t, loan)
}

func TestApproveLoanRequest_NotFound(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	loan, err := svc.ApproveLoanRequest(context.Background(), 999_999, 0)
	require.Error(t, err)
	assert.Nil(t, loan)
	assert.True(t, errors.Is(err, ErrLoanRequestNotFound))
}

func TestApproveLoanRequest_AlreadyApproved(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-A", Status: "approved",
	}
	require.NoError(t, db.Create(req).Error)

	_, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLoanRequestNotPending))
}

func TestRejectLoanRequest_NotFound(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	rej, err := svc.RejectLoanRequest(99_999, 0, "")
	require.Error(t, err)
	assert.Nil(t, rej)
	assert.True(t, errors.Is(err, ErrLoanRequestNotFound))
}

func TestRejectLoanRequest_WithReasonAndChangelog(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	// changelog repo is optional; passing it exercises the entry-write branch.

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-REJ", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	rej, err := svc.RejectLoanRequest(req.ID, 7, "insufficient income")
	require.NoError(t, err)
	require.NotNil(t, rej)
	assert.Equal(t, "rejected", rej.Status)
}

func TestCreateLoanRequest_NegativeAmount(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(-1000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-NEG", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidAmount))
}

func TestCreateLoanRequest_InvalidInterestType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "weird",
		Amount: decimal.NewFromInt(10000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-IT", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInterestType))
}

func TestValidateRepaymentPeriod_UnknownLoanType(t *testing.T) {
	err := validateRepaymentPeriod("unknown", 12)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidLoanType))
}

func TestValidateRepaymentPeriod_AllAllowedCashPeriods(t *testing.T) {
	for _, p := range []int{12, 24, 36, 48, 60, 72, 84} {
		require.NoError(t, validateRepaymentPeriod("cash", p))
	}
}

// TestApproveLoanRequest_ReplicaHit_RejectsOverLimit proves that the approval gate
// fires via the REPLICA path (no gRPC call) when the loan amount exceeds the
// employee's MaxLoanApprovalAmount stored in the local read-model.
func TestApproveLoanRequest_ReplicaHit_RejectsOverLimit(t *testing.T) {
	const empID uint64 = 42

	// Replica returns a row with limit 100 000 for any employee lookup.
	replica := &stubLimitReplica{
		row: model.EmployeeLimitReplica{
			EmployeeID:            empID,
			MaxLoanApprovalAmount: decimal.NewFromInt(100000),
			Version:               5,
		},
	}
	// countingLimitClient must NOT be called on a replica hit.
	grpcCounter := &countingLimitClient{
		resp: &userpb.EmployeeLimitResponse{MaxLoanApprovalAmount: "9999999"},
	}

	svc, db := buildLoanRequestSvc(t, nil)
	svc.limitReplica = replica
	svc.limitClient = grpcCounter

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(500000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-REPLICA-REJECT", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, empID)
	require.Error(t, err)
	assert.Nil(t, loan)
	assert.True(t, errors.Is(err, ErrAmountExceedsApprovalLimit),
		"expected ErrAmountExceedsApprovalLimit, got %v", err)
	assert.Equal(t, 0, grpcCounter.calls,
		"gRPC GetEmployeeLimits must NOT be called when replica returns a hit")
}

// TestApproveLoanRequest_ReplicaHit_WithinLimit_Allows proves that the approval
// gate passes (no ErrAmountExceedsApprovalLimit) via the REPLICA path when the
// loan amount is within the employee's MaxLoanApprovalAmount.
func TestApproveLoanRequest_ReplicaHit_WithinLimit_Allows(t *testing.T) {
	const empID uint64 = 43

	replica := &stubLimitReplica{
		row: model.EmployeeLimitReplica{
			EmployeeID:            empID,
			MaxLoanApprovalAmount: decimal.NewFromInt(1000000),
			Version:               3,
		},
	}
	grpcCounter := &countingLimitClient{
		resp: &userpb.EmployeeLimitResponse{MaxLoanApprovalAmount: "1"},
	}

	svc, db := buildLoanRequestSvc(t, nil)
	svc.limitReplica = replica
	svc.limitClient = grpcCounter

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(500000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-REPLICA-ALLOW", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, empID)

	// The gate must NOT fire. The approval may succeed or fail for unrelated
	// reasons (e.g., account-service unavailable in test), but it must NOT
	// be ErrAmountExceedsApprovalLimit.
	assert.False(t, errors.Is(err, ErrAmountExceedsApprovalLimit),
		"replica-hit within-limit must not block approval, got %v", err)
	// With the test harness (no account client, disbursement saga nil), approval
	// fully succeeds — assert the positive outcome too.
	require.NoError(t, err)
	require.NotNil(t, loan)
	assert.Equal(t, "approved", loan.Status)
	assert.Equal(t, 0, grpcCounter.calls,
		"gRPC GetEmployeeLimits must NOT be called when replica returns a hit")
}
