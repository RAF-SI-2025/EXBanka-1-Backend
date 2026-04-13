package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// ---- mockBankAccountClientForLoan -------------------------------------------

type mockBankAccountClientForLoan struct {
	debitErr    error
	creditErr   error
	debitCalls  []string // references passed to DebitBankAccount
	debitAmounts []string // amounts passed to DebitBankAccount
	creditCalls  []string // references passed to CreditBankAccount (compensation tracking)
}

func (m *mockBankAccountClientForLoan) DebitBankAccount(_ context.Context, req *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	m.debitCalls = append(m.debitCalls, req.Reference)
	m.debitAmounts = append(m.debitAmounts, req.Amount)
	return &accountpb.BankAccountOpResponse{}, m.debitErr
}
func (m *mockBankAccountClientForLoan) CreditBankAccount(_ context.Context, req *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	m.creditCalls = append(m.creditCalls, req.Reference)
	return &accountpb.BankAccountOpResponse{}, m.creditErr
}
func (m *mockBankAccountClientForLoan) CreateBankAccount(_ context.Context, _ *accountpb.CreateBankAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClientForLoan) ListBankAccounts(_ context.Context, _ *accountpb.ListBankAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListBankAccountsResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClientForLoan) DeleteBankAccount(_ context.Context, _ *accountpb.DeleteBankAccountRequest, _ ...grpc.CallOption) (*accountpb.DeleteBankAccountResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClientForLoan) GetBankRSDAccount(_ context.Context, _ *accountpb.GetBankRSDAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}

// ---- test DB & helpers -------------------------------------------------------

func newDisbursementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.LoanRequest{}, &model.Loan{}, &model.Installment{},
		&model.InterestRateTier{}, &model.BankMargin{},
	))
	return db
}

func buildDisbursementSvc(t *testing.T, accountClient accountpb.AccountServiceClient) (*LoanRequestService, *gorm.DB) {
	t.Helper()
	db := newDisbursementTestDB(t)
	loanReqRepo := repository.NewLoanRequestRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installRepo := repository.NewInstallmentRepository(db)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	rateConfigSvc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, rateConfigSvc.SeedDefaults())
	svc := NewLoanRequestService(loanReqRepo, loanRepo, installRepo, nil, accountClient, rateConfigSvc, db)
	return svc, db
}

func buildDisbursementSvcWithBank(t *testing.T, accountClient accountpb.AccountServiceClient, bankClient accountpb.BankAccountServiceClient) (*LoanRequestService, *gorm.DB) {
	t.Helper()
	svc, db := buildDisbursementSvc(t, accountClient)
	if bankClient != nil {
		svc.SetBankAccountClient(bankClient)
	}
	return svc, db
}

func seedPendingRequest(t *testing.T, db *gorm.DB) *model.LoanRequest {
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
	require.NoError(t, db.Create(req).Error)
	return req
}

// ---- tests ------------------------------------------------------------------

func TestApproveLoan_DisbursesOnSuccess(t *testing.T) {
	accountClient := &mockAccountClientForLoan{updateBalanceErr: nil}
	bankClient := &mockBankAccountClientForLoan{}
	svc, db := buildDisbursementSvcWithBank(t, accountClient, bankClient)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "active", loan.Status)

	var dbLoan model.Loan
	require.NoError(t, db.First(&dbLoan, loan.ID).Error)
	assert.Equal(t, "active", dbLoan.Status)

	// Bank must be debited first with a deterministic reference.
	require.Len(t, bankClient.debitCalls, 1, "DebitBankAccount must be called exactly once")
	expectedRef := fmt.Sprintf("loan-disbursement:%d", loan.ID)
	assert.Equal(t, expectedRef, bankClient.debitCalls[0])
	require.Len(t, bankClient.debitAmounts, 1)
	assert.Equal(t, "10000.0000", bankClient.debitAmounts[0])

	// Borrower credited next.
	require.Len(t, accountClient.calls, 1, "UpdateBalance must be called exactly once on success")
	assert.Equal(t, "ACC-TEST-001", accountClient.calls[0])

	// No compensation.
	assert.Empty(t, bankClient.creditCalls, "no compensation on happy path")
}

func TestApproveLoan_InsufficientBankLiquidity_PropagatesAndMarksFailed(t *testing.T) {
	accountClient := &mockAccountClientForLoan{}
	bankClient := &mockBankAccountClientForLoan{
		debitErr: status.Error(codes.FailedPrecondition, "bank has insufficient liquidity in RSD"),
	}
	svc, db := buildDisbursementSvcWithBank(t, accountClient, bankClient)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.Error(t, err, "insufficient liquidity must propagate")
	assert.Nil(t, loan, "loan return value is unused when disbursement fails")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Borrower must NOT be credited.
	assert.Empty(t, accountClient.calls, "UpdateBalance must not be called when bank debit fails")

	// Loan must exist in DB with disbursement_failed status.
	var dbLoan model.Loan
	require.NoError(t, db.Where("client_id = ?", 1).First(&dbLoan).Error)
	assert.Equal(t, "disbursement_failed", dbLoan.Status)

	// No compensation — nothing was debited.
	assert.Empty(t, bankClient.creditCalls, "no compensation needed when bank debit failed")
}

func TestApproveLoan_BorrowerCreditFails_CompensatesAndMarksFailed(t *testing.T) {
	accountClient := &mockAccountClientForLoan{updateBalanceErr: errors.New("downstream failure")}
	bankClient := &mockBankAccountClientForLoan{}
	svc, db := buildDisbursementSvcWithBank(t, accountClient, bankClient)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.Error(t, err, "borrower credit failure must propagate")
	assert.Nil(t, loan)

	// Bank was debited once.
	require.Len(t, bankClient.debitCalls, 1)

	// Compensation was invoked — bank was credited back with the SAME reference (idempotency key).
	require.Len(t, bankClient.creditCalls, 1, "compensation must call CreditBankAccount once")
	assert.Equal(t, bankClient.debitCalls[0], bankClient.creditCalls[0], "compensation must reuse same reference")

	// Loan flagged disbursement_failed.
	var dbLoan model.Loan
	require.NoError(t, db.Where("client_id = ?", 1).First(&dbLoan).Error)
	assert.Equal(t, "disbursement_failed", dbLoan.Status)
}

func TestApproveLoan_NilAccountClient(t *testing.T) {
	svc, db := buildDisbursementSvc(t, nil)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "approved", loan.Status)

	var count int64
	db.Model(&model.Loan{}).Count(&count)
	assert.Equal(t, int64(1), count, "loan must be created even when accountClient is nil")
}
