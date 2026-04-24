package service

import (
	"context"
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

// --- mock account client that supports currency mismatch testing ----------------

type mockAccountClientForRequest struct {
	currency string // currency returned by GetAccountByNumber
}

func (m *mockAccountClientForRequest) GetAccountByNumber(_ context.Context, req *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{AccountNumber: req.AccountNumber, CurrencyCode: m.currency}, nil
}
func (m *mockAccountClientForRequest) UpdateBalance(_ context.Context, _ *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (m *mockAccountClientForRequest) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForRequest) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}

// --- helpers ------------------------------------------------------------------

func newLoanRequestTestDB(t *testing.T) *gorm.DB {
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

func buildLoanRequestSvc(t *testing.T, accountClient accountpb.AccountServiceClient) (*LoanRequestService, *gorm.DB) {
	t.Helper()
	db := newLoanRequestTestDB(t)
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

// --- CreateLoanRequest tests --------------------------------------------------

func TestCreateLoanRequest_CashType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(300000), CurrencyCode: "RSD",
		RepaymentPeriod: 36, AccountNumber: "ACC-001", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.NoError(t, err)
	assert.NotZero(t, req.ID, "loan request ID should be assigned after creation")
}

func TestCreateLoanRequest_HousingType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 2, LoanType: "housing", InterestType: "variable",
		Amount: decimal.NewFromInt(2000000), CurrencyCode: "RSD",
		RepaymentPeriod: 240, AccountNumber: "ACC-002", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.NoError(t, err)
	assert.NotZero(t, req.ID)
}

func TestCreateLoanRequest_AutoType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 3, LoanType: "auto", InterestType: "fixed",
		Amount: decimal.NewFromInt(500000), CurrencyCode: "RSD",
		RepaymentPeriod: 60, AccountNumber: "ACC-003", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.NoError(t, err)
	assert.NotZero(t, req.ID)
}

func TestCreateLoanRequest_RefinancingType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 4, LoanType: "refinancing", InterestType: "variable",
		Amount: decimal.NewFromInt(800000), CurrencyCode: "RSD",
		RepaymentPeriod: 48, AccountNumber: "ACC-004", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.NoError(t, err)
	assert.NotZero(t, req.ID)
}

func TestCreateLoanRequest_StudentType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 5, LoanType: "student", InterestType: "fixed",
		Amount: decimal.NewFromInt(200000), CurrencyCode: "RSD",
		RepaymentPeriod: 24, AccountNumber: "ACC-005", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.NoError(t, err)
	assert.NotZero(t, req.ID)
}

func TestCreateLoanRequest_InvalidRepaymentPeriod_Housing(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	// Housing allows 60,120,180,240,300,360 -- 36 is not valid
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "housing", InterestType: "fixed",
		Amount: decimal.NewFromInt(1000000), CurrencyCode: "RSD",
		RepaymentPeriod: 36, AccountNumber: "ACC-006", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed for housing loans")
}

func TestCreateLoanRequest_InvalidRepaymentPeriod_Cash(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	// Cash allows 12,24,36,48,60,72,84 -- 100 is not valid
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 100, AccountNumber: "ACC-007", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed for cash loans")
}

func TestCreateLoanRequest_AccountCurrencyMismatch(t *testing.T) {
	// Account returns EUR but loan requests RSD
	accountClient := &mockAccountClientForRequest{currency: "EUR"}
	svc, _ := buildLoanRequestSvc(t, accountClient)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-008", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must match account currency")
}

func TestCreateLoanRequest_InvalidLoanType(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "mortgage", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-009", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loan type must be one of")
}

func TestCreateLoanRequest_ZeroAmount(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.Zero, CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-010", Status: "pending",
	}
	err := svc.CreateLoanRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be greater than 0")
}

// --- ApproveLoanRequest tests -------------------------------------------------

func TestApproveLoanRequest_CreatesLoanAndInstallments(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)

	// Seed a pending loan request
	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(300000), CurrencyCode: "RSD",
		RepaymentPeriod: 36, AccountNumber: "ACC-100", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	loan, err := svc.ApproveLoanRequest(context.Background(), req.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "approved", loan.Status, "loan should be approved when no account client")
	assert.Equal(t, "cash", loan.LoanType)
	assert.Equal(t, 36, loan.RepaymentPeriod)
	assert.True(t, loan.Amount.Equal(decimal.NewFromInt(300000)))

	// Verify installments were created
	var installments []model.Installment
	require.NoError(t, db.Where("loan_id = ?", loan.ID).Find(&installments).Error)
	assert.Len(t, installments, 36, "36 installments should be generated for 36-month loan")

	// Verify loan request status was updated
	var updatedReq model.LoanRequest
	require.NoError(t, db.First(&updatedReq, req.ID).Error)
	assert.Equal(t, "approved", updatedReq.Status)
}

// --- RejectLoanRequest tests --------------------------------------------------

func TestRejectLoanRequest_SetsStatusRejected(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "housing", InterestType: "fixed",
		Amount: decimal.NewFromInt(5000000), CurrencyCode: "RSD",
		RepaymentPeriod: 360, AccountNumber: "ACC-200", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	rejected, err := svc.RejectLoanRequest(req.ID, 0, "")
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)

	// Verify DB
	var dbReq model.LoanRequest
	require.NoError(t, db.First(&dbReq, req.ID).Error)
	assert.Equal(t, "rejected", dbReq.Status)
}

func TestRejectLoanRequest_AlreadyRejected(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)

	req := &model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-201", Status: "rejected",
	}
	require.NoError(t, db.Create(req).Error)

	_, err := svc.RejectLoanRequest(req.ID, 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already rejected")
}
