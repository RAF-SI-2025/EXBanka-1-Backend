package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// ---- mockAccountClientForLoan -----------------------------------------------

type mockAccountClientForLoan struct {
	updateBalanceErr error
	calls            []string // account numbers
	amounts          []string // amounts passed to UpdateBalance
}

func (m *mockAccountClientForLoan) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	m.calls = append(m.calls, req.AccountNumber)
	m.amounts = append(m.amounts, req.Amount)
	return &accountpb.AccountResponse{}, m.updateBalanceErr
}
func (m *mockAccountClientForLoan) GetAccountByNumber(_ context.Context, req *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{AccountNumber: req.AccountNumber, CurrencyCode: "RSD"}, nil
}
func (m *mockAccountClientForLoan) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForLoan) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}

// ---- in-memory repos --------------------------------------------------------

type memLoanRequestRepo struct {
	requests map[uint64]*model.LoanRequest
	nextID   uint64
}

func newMemLoanRequestRepo() *memLoanRequestRepo {
	return &memLoanRequestRepo{requests: make(map[uint64]*model.LoanRequest), nextID: 1}
}
func (r *memLoanRequestRepo) Create(req *model.LoanRequest) error {
	req.ID = r.nextID
	r.nextID++
	cp := *req
	r.requests[req.ID] = &cp
	return nil
}
func (r *memLoanRequestRepo) GetByID(id uint64) (*model.LoanRequest, error) {
	if req, ok := r.requests[id]; ok {
		cp := *req
		return &cp, nil
	}
	return nil, errors.New("not found")
}
func (r *memLoanRequestRepo) Update(req *model.LoanRequest) error {
	cp := *req
	r.requests[req.ID] = &cp
	return nil
}
func (r *memLoanRequestRepo) List(_, _, _ string, _ uint64, _, _ int) ([]model.LoanRequest, int64, error) {
	return nil, 0, nil
}

type memLoanRepo struct {
	loans      map[uint64]*model.Loan
	nextID     uint64
	updateSeen []string
}

func newMemLoanRepo() *memLoanRepo {
	return &memLoanRepo{loans: make(map[uint64]*model.Loan), nextID: 1}
}
func (r *memLoanRepo) GenerateLoanNumber() string { return "LN0000000001" }
func (r *memLoanRepo) Create(loan *model.Loan) error {
	loan.ID = r.nextID
	r.nextID++
	cp := *loan
	r.loans[loan.ID] = &cp
	return nil
}
func (r *memLoanRepo) Update(loan *model.Loan) error {
	r.updateSeen = append(r.updateSeen, loan.Status)
	cp := *loan
	r.loans[loan.ID] = &cp
	return nil
}
func (r *memLoanRepo) GetByID(id uint64) (*model.Loan, error) {
	if l, ok := r.loans[id]; ok {
		cp := *l
		return &cp, nil
	}
	return nil, errors.New("not found")
}

type memInstallmentRepo struct{}

func (r *memInstallmentRepo) CreateBatch(_ []model.Installment) error { return nil }

// ---- helper -----------------------------------------------------------------

func buildDisbursementSvc(t *testing.T, accountClient accountpb.AccountServiceClient) (*LoanRequestService, *memLoanRequestRepo, *memLoanRepo) {
	t.Helper()
	loanReqRepo := newMemLoanRequestRepo()
	loanRepo := newMemLoanRepo()
	installRepo := &memInstallmentRepo{}
	rateConfigSvc := newTestRateConfigSvc(t)
	svc := NewLoanRequestService(loanReqRepo, loanRepo, installRepo, nil, accountClient, rateConfigSvc)
	return svc, loanReqRepo, loanRepo
}

func newTestRateConfigSvc(t *testing.T) *RateConfigService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.InterestRateTier{}, &model.BankMargin{}))
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo)
	require.NoError(t, svc.SeedDefaults())
	return svc
}

func seedPendingRequest(t *testing.T, repo *memLoanRequestRepo) *model.LoanRequest {
	t.Helper()
	req := &model.LoanRequest{
		ClientID:        1,
		LoanType:        "cash",
		InterestType:    "fixed",
		Amount:          decimal.NewFromInt(10000),
		CurrencyCode:    "RSD",
		RepaymentPeriod: 12,
		AccountNumber:   "ACC-TEST-001",
		Status:          "pending",
	}
	require.NoError(t, repo.Create(req))
	return req
}

// ---- tests ------------------------------------------------------------------

func TestApproveLoan_DisbursesOnSuccess(t *testing.T) {
	client := &mockAccountClientForLoan{updateBalanceErr: nil}
	svc, reqRepo, loanRepo := buildDisbursementSvc(t, client)

	req := seedPendingRequest(t, reqRepo)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "active", loan.Status, "loan status must be active when disbursement succeeds")

	require.Len(t, client.calls, 1, "UpdateBalance must be called exactly once")
	assert.Equal(t, "ACC-TEST-001", client.calls[0])

	require.Len(t, client.amounts, 1)
	assert.Equal(t, "10000.0000", client.amounts[0], "disbursement amount must match loan amount in StringFixed(4) format")

	require.NotEmpty(t, loanRepo.updateSeen)
	assert.Equal(t, "active", loanRepo.updateSeen[len(loanRepo.updateSeen)-1])
}

func TestApproveLoan_SoftFailOnDisbursementError(t *testing.T) {
	client := &mockAccountClientForLoan{updateBalanceErr: errors.New("account service unavailable")}
	svc, reqRepo, loanRepo := buildDisbursementSvc(t, client)

	req := seedPendingRequest(t, reqRepo)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err, "soft failure must not propagate as an error to the caller")
	assert.Equal(t, "disbursement_failed", loan.Status)

	require.NotEmpty(t, loanRepo.updateSeen)
	assert.Equal(t, "disbursement_failed", loanRepo.updateSeen[len(loanRepo.updateSeen)-1])

	persisted, err := loanRepo.GetByID(loan.ID)
	require.NoError(t, err)
	assert.Equal(t, "disbursement_failed", persisted.Status)
}

func TestApproveLoan_NilAccountClient(t *testing.T) {
	svc, reqRepo, loanRepo := buildDisbursementSvc(t, nil)
	req := seedPendingRequest(t, reqRepo)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "approved", loan.Status)
	assert.Empty(t, loanRepo.updateSeen, "loanRepo.Update must not be called when accountClient is nil")
}
