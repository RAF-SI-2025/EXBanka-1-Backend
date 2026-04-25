package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/creditpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// --- stubs --------------------------------------------------------------------

type stubLoanRequestSvc struct {
	createFn   func(req *model.LoanRequest) error
	getFn      func(id uint64) (*model.LoanRequest, error)
	listFn     func(loanTypeFilter, accountFilter, statusFilter string, clientID uint64, page, pageSize int) ([]model.LoanRequest, int64, error)
	approveFn  func(ctx context.Context, requestID, employeeID uint64) (*model.Loan, error)
	rejectFn   func(requestID uint64, changedBy int64, reason string) (*model.LoanRequest, error)
	approveCnt int
	rejectCnt  int
	createCnt  int
}

func (s *stubLoanRequestSvc) CreateLoanRequest(req *model.LoanRequest) error {
	s.createCnt++
	if s.createFn != nil {
		return s.createFn(req)
	}
	req.ID = 1
	req.Status = "pending"
	return nil
}

func (s *stubLoanRequestSvc) GetLoanRequest(id uint64) (*model.LoanRequest, error) {
	if s.getFn != nil {
		return s.getFn(id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubLoanRequestSvc) ListLoanRequests(loanTypeFilter, accountFilter, statusFilter string, clientID uint64, page, pageSize int) ([]model.LoanRequest, int64, error) {
	if s.listFn != nil {
		return s.listFn(loanTypeFilter, accountFilter, statusFilter, clientID, page, pageSize)
	}
	return nil, 0, nil
}

func (s *stubLoanRequestSvc) ApproveLoanRequest(ctx context.Context, requestID, employeeID uint64) (*model.Loan, error) {
	s.approveCnt++
	if s.approveFn != nil {
		return s.approveFn(ctx, requestID, employeeID)
	}
	return nil, errors.New("not implemented")
}

func (s *stubLoanRequestSvc) RejectLoanRequest(requestID uint64, changedBy int64, reason string) (*model.LoanRequest, error) {
	s.rejectCnt++
	if s.rejectFn != nil {
		return s.rejectFn(requestID, changedBy, reason)
	}
	return nil, errors.New("not implemented")
}

type stubLoanSvc struct {
	getFn          func(id uint64) (*model.Loan, error)
	listByClientFn func(clientID uint64, page, pageSize int) ([]model.Loan, int64, error)
	listAllFn      func(loanTypeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.Loan, int64, error)
}

func (s *stubLoanSvc) GetLoan(id uint64) (*model.Loan, error) {
	if s.getFn != nil {
		return s.getFn(id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubLoanSvc) ListLoansByClient(clientID uint64, page, pageSize int) ([]model.Loan, int64, error) {
	if s.listByClientFn != nil {
		return s.listByClientFn(clientID, page, pageSize)
	}
	return nil, 0, nil
}

func (s *stubLoanSvc) ListAllLoans(loanTypeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.Loan, int64, error) {
	if s.listAllFn != nil {
		return s.listAllFn(loanTypeFilter, accountFilter, statusFilter, page, pageSize)
	}
	return nil, 0, nil
}

type stubInstallmentSvc struct {
	listFn func(loanID uint64) ([]model.Installment, error)
}

func (s *stubInstallmentSvc) GetInstallmentsByLoan(loanID uint64) ([]model.Installment, error) {
	if s.listFn != nil {
		return s.listFn(loanID)
	}
	return nil, nil
}

type stubRateConfigSvc struct {
	listTiersFn   func() ([]model.InterestRateTier, error)
	createTierFn  func(t *model.InterestRateTier) error
	updateTierFn  func(t *model.InterestRateTier) error
	deleteTierFn  func(id uint64) error
	listMarginsFn func() ([]model.BankMargin, error)
	updateMargFn  func(m *model.BankMargin) error
	applyVarFn    func(tierID uint64) (int, error)
}

func (s *stubRateConfigSvc) ListTiers() ([]model.InterestRateTier, error) {
	if s.listTiersFn != nil {
		return s.listTiersFn()
	}
	return nil, nil
}
func (s *stubRateConfigSvc) CreateTier(t *model.InterestRateTier) error {
	if s.createTierFn != nil {
		return s.createTierFn(t)
	}
	t.ID = 7
	t.Active = true
	return nil
}
func (s *stubRateConfigSvc) UpdateTier(t *model.InterestRateTier) error {
	if s.updateTierFn != nil {
		return s.updateTierFn(t)
	}
	return nil
}
func (s *stubRateConfigSvc) DeleteTier(id uint64) error {
	if s.deleteTierFn != nil {
		return s.deleteTierFn(id)
	}
	return nil
}
func (s *stubRateConfigSvc) ListMargins() ([]model.BankMargin, error) {
	if s.listMarginsFn != nil {
		return s.listMarginsFn()
	}
	return nil, nil
}
func (s *stubRateConfigSvc) UpdateMargin(m *model.BankMargin) error {
	if s.updateMargFn != nil {
		return s.updateMargFn(m)
	}
	return nil
}

// ApplyVariableRateUpdate matches rateConfigFacade. Tests use nil repos
// because the stub never invokes them.
func (s *stubRateConfigSvc) ApplyVariableRateUpdate(tierID uint64, _ *repository.LoanRepository, _ *repository.InstallmentRepository) (int, error) {
	if s.applyVarFn != nil {
		return s.applyVarFn(tierID)
	}
	return 0, nil
}

// stubProducer captures published events without writing to Kafka.
type stubProducer struct {
	requested  []kafkamsg.LoanStatusMessage
	approved   []kafkamsg.LoanStatusMessage
	rejected   []kafkamsg.LoanStatusMessage
	disbursed  []kafkamsg.LoanDisbursedMessage
	notifs     []kafkamsg.GeneralNotificationMessage
	publishErr error
}

func (p *stubProducer) PublishLoanRequested(_ context.Context, m kafkamsg.LoanStatusMessage) error {
	p.requested = append(p.requested, m)
	return p.publishErr
}
func (p *stubProducer) PublishLoanApproved(_ context.Context, m kafkamsg.LoanStatusMessage) error {
	p.approved = append(p.approved, m)
	return p.publishErr
}
func (p *stubProducer) PublishLoanRejected(_ context.Context, m kafkamsg.LoanStatusMessage) error {
	p.rejected = append(p.rejected, m)
	return p.publishErr
}
func (p *stubProducer) PublishLoanDisbursed(_ context.Context, m kafkamsg.LoanDisbursedMessage) error {
	p.disbursed = append(p.disbursed, m)
	return p.publishErr
}
func (p *stubProducer) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	p.notifs = append(p.notifs, m)
	return p.publishErr
}

// --- helpers ------------------------------------------------------------------

type testHandlerStubs struct {
	loanReq    *stubLoanRequestSvc
	loan       *stubLoanSvc
	install    *stubInstallmentSvc
	rateConfig *stubRateConfigSvc
	producer   *stubProducer
}

func newTestHandler() (*CreditGRPCHandler, *stubLoanRequestSvc, *stubLoanSvc, *stubInstallmentSvc, *stubProducer) {
	h, s := newTestHandlerWithRate()
	return h, s.loanReq, s.loan, s.install, s.producer
}

func newTestHandlerWithRate() (*CreditGRPCHandler, *testHandlerStubs) {
	s := &testHandlerStubs{
		loanReq:    &stubLoanRequestSvc{},
		loan:       &stubLoanSvc{},
		install:    &stubInstallmentSvc{},
		rateConfig: &stubRateConfigSvc{},
		producer:   &stubProducer{},
	}
	h := &CreditGRPCHandler{
		loanRequestService: s.loanReq,
		loanService:        s.loan,
		installmentService: s.install,
		rateConfigService:  s.rateConfig,
		producer:           s.producer,
	}
	return h, s
}

// --- mapServiceError ---------------------------------------------------------

func TestMapServiceError(t *testing.T) {
	cases := []struct {
		err  string
		code codes.Code
	}{
		{"loan not found", codes.NotFound},
		{"amount must be positive", codes.InvalidArgument},
		{"invalid loan type", codes.InvalidArgument},
		{"value must not be zero", codes.InvalidArgument},
		{"loan number already exists", codes.AlreadyExists},
		{"duplicate key", codes.AlreadyExists},
		{"loan request 1 is already approved", codes.FailedPrecondition},
		{"insufficient funds", codes.FailedPrecondition},
		{"amount exceeds limit", codes.FailedPrecondition},
		{"only pending requests can be approved", codes.FailedPrecondition},
		{"card locked", codes.ResourceExhausted},
		{"max attempts reached", codes.ResourceExhausted},
		{"permission denied", codes.PermissionDenied},
		{"forbidden access", codes.PermissionDenied},
		{"some random message", codes.Internal},
	}
	for _, tc := range cases {
		got := mapServiceError(errors.New(tc.err))
		assert.Equal(t, tc.code, got, "for error %q", tc.err)
	}
}

// --- CreateLoanRequest -------------------------------------------------------

func TestCreateLoanRequest_Success(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.createFn = func(req *model.LoanRequest) error {
		req.ID = 42
		req.Status = "pending"
		return nil
	}
	resp, err := h.CreateLoanRequest(context.Background(), &pb.CreateLoanRequestReq{
		ClientId:        1,
		LoanType:        "cash",
		InterestType:    "fixed",
		Amount:          "100000",
		CurrencyCode:    "RSD",
		MonthlySalary:   "50000",
		RepaymentPeriod: 24,
		AccountNumber:   "ACC-001",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(42), resp.Id)
	assert.Equal(t, "pending", resp.Status)
	assert.Len(t, prod.requested, 1, "PublishLoanRequested should be called once")
}

func TestCreateLoanRequest_ServiceError(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.createFn = func(_ *model.LoanRequest) error {
		return errors.New("loan type must be one of: cash")
	}
	_, err := h.CreateLoanRequest(context.Background(), &pb.CreateLoanRequestReq{
		ClientId: 1, LoanType: "x", InterestType: "fixed",
		Amount: "100", CurrencyCode: "RSD", RepaymentPeriod: 12, AccountNumber: "A",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Empty(t, prod.requested, "no event should be published on error")
}

// --- GetLoanRequest ----------------------------------------------------------

func TestGetLoanRequest_Success(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.getFn = func(id uint64) (*model.LoanRequest, error) {
		return &model.LoanRequest{
			ID: id, ClientID: 9, LoanType: "cash", InterestType: "fixed",
			Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
			RepaymentPeriod: 12, Status: "pending", AccountNumber: "ACC-1",
		}, nil
	}
	resp, err := h.GetLoanRequest(context.Background(), &pb.GetLoanRequestReq{Id: 100})
	require.NoError(t, err)
	assert.Equal(t, uint64(100), resp.Id)
	assert.Equal(t, "cash", resp.LoanType)
}

func TestGetLoanRequest_NotFound_ReturnsNotFoundCode(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.getFn = func(_ uint64) (*model.LoanRequest, error) {
		return nil, gorm.ErrRecordNotFound
	}
	_, err := h.GetLoanRequest(context.Background(), &pb.GetLoanRequestReq{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// --- ListLoanRequests --------------------------------------------------------

func TestListLoanRequests_Success(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.listFn = func(_, _, _ string, _ uint64, _, _ int) ([]model.LoanRequest, int64, error) {
		return []model.LoanRequest{
			{ID: 1, LoanType: "cash", InterestType: "fixed", Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD", AccountNumber: "A", Status: "pending"},
			{ID: 2, LoanType: "housing", InterestType: "variable", Amount: decimal.NewFromInt(2000), CurrencyCode: "RSD", AccountNumber: "B", Status: "pending"},
		}, 2, nil
	}
	resp, err := h.ListLoanRequests(context.Background(), &pb.ListLoanRequestsReq{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Len(t, resp.Requests, 2)
}

func TestListLoanRequests_ServiceError(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.listFn = func(_, _, _ string, _ uint64, _, _ int) ([]model.LoanRequest, int64, error) {
		return nil, 0, errors.New("db down")
	}
	_, err := h.ListLoanRequests(context.Background(), &pb.ListLoanRequestsReq{Page: 1, PageSize: 10})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- ApproveLoanRequest ------------------------------------------------------

func TestApproveLoanRequest_DisbursedSuccess(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.approveFn = func(_ context.Context, requestID, _ uint64) (*model.Loan, error) {
		return &model.Loan{
			ID: 50, LoanNumber: "LN0001", LoanType: "cash",
			AccountNumber: "ACC-1", Amount: decimal.NewFromInt(1000),
			CurrencyCode: "RSD", Status: "active", InterestType: "fixed",
			ClientID: 1,
		}, nil
	}
	lrSvc.getFn = func(_ uint64) (*model.LoanRequest, error) {
		return &model.LoanRequest{ID: 10, ClientID: 1, LoanType: "cash", Amount: decimal.NewFromInt(1000)}, nil
	}
	resp, err := h.ApproveLoanRequest(context.Background(), &pb.ApproveLoanRequestReq{RequestId: 10, EmployeeId: 5})
	require.NoError(t, err)
	assert.Equal(t, "active", resp.Status)
	assert.Len(t, prod.approved, 1, "loan-approved event should fire")
	assert.Len(t, prod.disbursed, 1, "loan-disbursed event should fire when status=active")
	assert.Len(t, prod.notifs, 1, "general notification should fire")
}

func TestApproveLoanRequest_ApprovedButNotDisbursed_NoDisbursedEvent(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.approveFn = func(_ context.Context, _, _ uint64) (*model.Loan, error) {
		return &model.Loan{
			ID: 51, LoanNumber: "LN0002", LoanType: "cash",
			AccountNumber: "ACC-2", Amount: decimal.NewFromInt(1000),
			CurrencyCode: "RSD", Status: "approved", InterestType: "fixed",
			ClientID: 1,
		}, nil
	}
	lrSvc.getFn = func(_ uint64) (*model.LoanRequest, error) {
		return &model.LoanRequest{ID: 10, ClientID: 1, LoanType: "cash", Amount: decimal.NewFromInt(1000)}, nil
	}
	_, err := h.ApproveLoanRequest(context.Background(), &pb.ApproveLoanRequestReq{RequestId: 10})
	require.NoError(t, err)
	assert.Len(t, prod.approved, 1)
	assert.Empty(t, prod.disbursed, "no disbursed event when status != active")
}

func TestApproveLoanRequest_NotFound(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.approveFn = func(_ context.Context, _, _ uint64) (*model.Loan, error) {
		return nil, gorm.ErrRecordNotFound
	}
	_, err := h.ApproveLoanRequest(context.Background(), &pb.ApproveLoanRequestReq{RequestId: 999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
	assert.Empty(t, prod.approved)
}

func TestApproveLoanRequest_ServiceError_FailedPrecondition(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.approveFn = func(_ context.Context, _, _ uint64) (*model.Loan, error) {
		return nil, errors.New("loan request 1 is already approved")
	}
	_, err := h.ApproveLoanRequest(context.Background(), &pb.ApproveLoanRequestReq{RequestId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// --- RejectLoanRequest -------------------------------------------------------

func TestRejectLoanRequest_Success(t *testing.T) {
	h, lrSvc, _, _, prod := newTestHandler()
	lrSvc.rejectFn = func(requestID uint64, _ int64, _ string) (*model.LoanRequest, error) {
		return &model.LoanRequest{
			ID: requestID, ClientID: 7, LoanType: "cash", InterestType: "fixed",
			Amount: decimal.NewFromInt(50000), CurrencyCode: "RSD",
			Status: "rejected", AccountNumber: "X",
		}, nil
	}
	resp, err := h.RejectLoanRequest(context.Background(), &pb.RejectLoanRequestReq{RequestId: 5})
	require.NoError(t, err)
	assert.Equal(t, "rejected", resp.Status)
	assert.Len(t, prod.rejected, 1)
	assert.Len(t, prod.notifs, 1)
}

func TestRejectLoanRequest_NotFound(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.rejectFn = func(_ uint64, _ int64, _ string) (*model.LoanRequest, error) {
		return nil, gorm.ErrRecordNotFound
	}
	_, err := h.RejectLoanRequest(context.Background(), &pb.RejectLoanRequestReq{RequestId: 99})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// --- GetLoan / ListLoansByClient / ListAllLoans ------------------------------

func TestGetLoan_Success(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(id uint64) (*model.Loan, error) {
		return &model.Loan{
			ID: id, LoanNumber: "LN-X", LoanType: "cash",
			AccountNumber: "A", Amount: decimal.NewFromInt(1000),
			CurrencyCode: "RSD", Status: "active", InterestType: "fixed", ClientID: 1,
		}, nil
	}
	resp, err := h.GetLoan(context.Background(), &pb.GetLoanReq{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.Id)
	assert.Equal(t, "LN-X", resp.LoanNumber)
}

func TestGetLoan_NotFound(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(_ uint64) (*model.Loan, error) { return nil, gorm.ErrRecordNotFound }
	_, err := h.GetLoan(context.Background(), &pb.GetLoanReq{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestListLoansByClient_Success(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.listByClientFn = func(_ uint64, _, _ int) ([]model.Loan, int64, error) {
		return []model.Loan{
			{ID: 1, LoanNumber: "LN1", LoanType: "cash", Status: "active", Amount: decimal.NewFromInt(100), CurrencyCode: "RSD", InterestType: "fixed", AccountNumber: "A", ClientID: 1},
		}, 1, nil
	}
	resp, err := h.ListLoansByClient(context.Background(), &pb.ListLoansByClientReq{ClientId: 1, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Len(t, resp.Loans, 1)
}

func TestListLoansByClient_ServiceError(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.listByClientFn = func(_ uint64, _, _ int) ([]model.Loan, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, err := h.ListLoansByClient(context.Background(), &pb.ListLoansByClientReq{ClientId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestListAllLoans_Success(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.listAllFn = func(_, _, _ string, _, _ int) ([]model.Loan, int64, error) {
		return []model.Loan{
			{ID: 1, LoanNumber: "LN1", LoanType: "cash", Status: "active", Amount: decimal.NewFromInt(100), CurrencyCode: "RSD", InterestType: "fixed", AccountNumber: "A", ClientID: 1},
			{ID: 2, LoanNumber: "LN2", LoanType: "housing", Status: "active", Amount: decimal.NewFromInt(200), CurrencyCode: "RSD", InterestType: "fixed", AccountNumber: "B", ClientID: 2},
		}, 2, nil
	}
	resp, err := h.ListAllLoans(context.Background(), &pb.ListAllLoansReq{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Len(t, resp.Loans, 2)
}

func TestListAllLoans_ServiceError(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.listAllFn = func(_, _, _ string, _, _ int) ([]model.Loan, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, err := h.ListAllLoans(context.Background(), &pb.ListAllLoansReq{})
	require.Error(t, err)
}

// --- GetInstallmentsByLoan ---------------------------------------------------

func TestGetInstallmentsByLoan_Success(t *testing.T) {
	h, _, _, iSvc, _ := newTestHandler()
	now := time.Now()
	actual := now.Add(-24 * time.Hour)
	iSvc.listFn = func(_ uint64) ([]model.Installment, error) {
		return []model.Installment{
			{ID: 1, LoanID: 100, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD", ExpectedDate: now, Status: "unpaid"},
			{ID: 2, LoanID: 100, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD", ExpectedDate: now, Status: "paid", ActualDate: &actual},
		}, nil
	}
	resp, err := h.GetInstallmentsByLoan(context.Background(), &pb.GetInstallmentsByLoanReq{LoanId: 100})
	require.NoError(t, err)
	assert.Len(t, resp.Installments, 2)
	// installment[1] has actual_date populated
	assert.NotEmpty(t, resp.Installments[1].ActualDate)
	assert.Empty(t, resp.Installments[0].ActualDate)
}

func TestGetInstallmentsByLoan_ServiceError(t *testing.T) {
	h, _, _, iSvc, _ := newTestHandler()
	iSvc.listFn = func(_ uint64) ([]model.Installment, error) {
		return nil, errors.New("boom")
	}
	_, err := h.GetInstallmentsByLoan(context.Background(), &pb.GetInstallmentsByLoanReq{LoanId: 1})
	require.Error(t, err)
}

// --- Rate config / margin handler RPCs ---------------------------------------

func TestListInterestRateTiers_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.listTiersFn = func() ([]model.InterestRateTier, error) {
		return []model.InterestRateTier{
			{ID: 1, AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(100), FixedRate: decimal.NewFromFloat(5), VariableBase: decimal.NewFromFloat(5), Active: true},
		}, nil
	}
	resp, err := h.ListInterestRateTiers(context.Background(), &pb.ListInterestRateTiersRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Tiers, 1)
	assert.Equal(t, uint64(1), resp.Tiers[0].Id)
}

func TestListInterestRateTiers_Error(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.listTiersFn = func() ([]model.InterestRateTier, error) {
		return nil, errors.New("db down")
	}
	_, err := h.ListInterestRateTiers(context.Background(), &pb.ListInterestRateTiersRequest{})
	require.Error(t, err)
}

func TestCreateInterestRateTier_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.createTierFn = func(t *model.InterestRateTier) error {
		t.ID = 99
		t.Active = true
		return nil
	}
	resp, err := h.CreateInterestRateTier(context.Background(), &pb.CreateInterestRateTierRequest{
		AmountFrom: "0", AmountTo: "100000", FixedRate: "5.00", VariableBase: "5.00",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(99), resp.Id)
	assert.True(t, resp.Active)
}

func TestCreateInterestRateTier_ValidationError(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.createTierFn = func(_ *model.InterestRateTier) error {
		return errors.New("fixed_rate must not be negative")
	}
	_, err := h.CreateInterestRateTier(context.Background(), &pb.CreateInterestRateTierRequest{
		AmountFrom: "0", AmountTo: "100", FixedRate: "-1", VariableBase: "5",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateInterestRateTier_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.updateTierFn = func(t *model.InterestRateTier) error {
		t.Active = true
		return nil
	}
	resp, err := h.UpdateInterestRateTier(context.Background(), &pb.UpdateInterestRateTierRequest{
		Id: 5, AmountFrom: "0", AmountTo: "1000", FixedRate: "6", VariableBase: "6",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.Id)
}

func TestUpdateInterestRateTier_NotFound(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.updateTierFn = func(_ *model.InterestRateTier) error {
		return errors.New("interest rate tier 5 not found")
	}
	_, err := h.UpdateInterestRateTier(context.Background(), &pb.UpdateInterestRateTierRequest{
		Id: 5, AmountFrom: "0", AmountTo: "1000", FixedRate: "6", VariableBase: "6",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDeleteInterestRateTier_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	called := uint64(0)
	s.rateConfig.deleteTierFn = func(id uint64) error {
		called = id
		return nil
	}
	resp, err := h.DeleteInterestRateTier(context.Background(), &pb.DeleteInterestRateTierRequest{Id: 17})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, uint64(17), called)
}

func TestDeleteInterestRateTier_NotFound(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.deleteTierFn = func(_ uint64) error {
		return errors.New("interest rate tier 17 not found")
	}
	_, err := h.DeleteInterestRateTier(context.Background(), &pb.DeleteInterestRateTierRequest{Id: 17})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestListBankMargins_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.listMarginsFn = func() ([]model.BankMargin, error) {
		return []model.BankMargin{
			{ID: 1, LoanType: "cash", Margin: decimal.NewFromFloat(1.5), Active: true},
			{ID: 2, LoanType: "housing", Margin: decimal.NewFromFloat(1.25), Active: true},
		}, nil
	}
	resp, err := h.ListBankMargins(context.Background(), &pb.ListBankMarginsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Margins, 2)
}

func TestListBankMargins_Error(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.listMarginsFn = func() ([]model.BankMargin, error) {
		return nil, errors.New("db down")
	}
	_, err := h.ListBankMargins(context.Background(), &pb.ListBankMarginsRequest{})
	require.Error(t, err)
}

func TestUpdateBankMargin_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.updateMargFn = func(m *model.BankMargin) error {
		m.LoanType = "cash"
		m.Active = true
		return nil
	}
	resp, err := h.UpdateBankMargin(context.Background(), &pb.UpdateBankMarginRequest{Id: 1, Margin: "2.0"})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), resp.Id)
}

func TestUpdateBankMargin_NegativeRejected(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.updateMargFn = func(_ *model.BankMargin) error {
		return errors.New("margin must not be negative")
	}
	_, err := h.UpdateBankMargin(context.Background(), &pb.UpdateBankMarginRequest{Id: 1, Margin: "-1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestApplyVariableRateUpdate_Success(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.applyVarFn = func(tierID uint64) (int, error) {
		assert.Equal(t, uint64(3), tierID)
		return 7, nil
	}
	resp, err := h.ApplyVariableRateUpdate(context.Background(), &pb.ApplyVariableRateUpdateRequest{TierId: 3})
	require.NoError(t, err)
	assert.Equal(t, int32(7), resp.AffectedLoans)
}

func TestApplyVariableRateUpdate_TierNotFound(t *testing.T) {
	h, s := newTestHandlerWithRate()
	s.rateConfig.applyVarFn = func(_ uint64) (int, error) {
		return 0, errors.New("interest rate tier 99 not found")
	}
	_, err := h.ApplyVariableRateUpdate(context.Background(), &pb.ApplyVariableRateUpdateRequest{TierId: 99})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// --- Compile-time interface assertions ---------------------------------------

var (
	_ loanRequestFacade = (*stubLoanRequestSvc)(nil)
	_ loanFacade        = (*stubLoanSvc)(nil)
	_ installmentFacade = (*stubInstallmentSvc)(nil)
	_ rateConfigFacade  = (*stubRateConfigSvc)(nil)
	_ creditProducer    = (*stubProducer)(nil)
	// Ensure repository import stays referenced after build optimizations.
	_ = (*repository.LoanRepository)(nil)
)
