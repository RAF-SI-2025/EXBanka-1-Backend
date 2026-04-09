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
	client := &mockAccountClientForLoan{updateBalanceErr: nil}
	svc, db := buildDisbursementSvc(t, client)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "active", loan.Status, "loan status must be active when disbursement succeeds")

	// Verify persisted status in DB.
	var dbLoan model.Loan
	require.NoError(t, db.First(&dbLoan, loan.ID).Error)
	assert.Equal(t, "active", dbLoan.Status)

	require.Len(t, client.calls, 1, "UpdateBalance must be called exactly once")
	assert.Equal(t, "ACC-TEST-001", client.calls[0])

	require.Len(t, client.amounts, 1)
	assert.Equal(t, "10000.0000", client.amounts[0], "disbursement amount must match loan amount in StringFixed(4) format")
}

func TestApproveLoan_SoftFailOnDisbursementError(t *testing.T) {
	client := &mockAccountClientForLoan{updateBalanceErr: errors.New("account service unavailable")}
	svc, db := buildDisbursementSvc(t, client)
	req := seedPendingRequest(t, db)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err, "soft failure must not propagate as an error to the caller")
	assert.Equal(t, "disbursement_failed", loan.Status)

	// Verify persisted status in DB.
	var dbLoan model.Loan
	require.NoError(t, db.First(&dbLoan, loan.ID).Error)
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
