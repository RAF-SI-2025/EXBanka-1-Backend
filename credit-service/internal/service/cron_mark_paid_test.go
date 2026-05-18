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

// mockCronAccountClient tracks UpdateBalance calls and can optionally fail.
type mockCronAccountClient struct {
	calls []struct{ account, amount string }
	err   error
}

func (m *mockCronAccountClient) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	m.calls = append(m.calls, struct{ account, amount string }{req.AccountNumber, req.Amount})
	return &accountpb.AccountResponse{}, m.err
}
func (m *mockCronAccountClient) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetAccountByNumber(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ReserveIncoming(_ context.Context, _ *accountpb.ReserveIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) CommitIncoming(_ context.Context, _ *accountpb.CommitIncomingRequest, _ ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ReleaseIncoming(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	return nil, nil
}
func (m *mockCronAccountClient) ListChangelog(_ context.Context, _ *accountpb.ListChangelogRequest, _ ...grpc.CallOption) (*accountpb.ListChangelogResponse, error) {
	return nil, nil
}

func newCronTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Loan{}, &model.Installment{}))
	return db
}

// TestProcessInstallment_MarkPaidFailure_Compensates verifies that when
// MarkInstallmentPaid fails after a successful debit+credit, the cron service
// reverses both transfers so the next run can retry cleanly.
func TestProcessInstallment_MarkPaidFailure_Compensates(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-TEST-001",
		ClientID:      1,
		AccountNumber: "ACC-BORROWER-001",
		Status:        "active",
		CurrencyCode:  "RSD",
		InterestType:  "fixed",
		Amount:        decimal.NewFromInt(10000),
		RemainingDebt: decimal.NewFromInt(10000),
	}
	require.NoError(t, db.Create(loan).Error)
	inst := &model.Installment{
		LoanID:       loan.ID,
		Amount:       decimal.NewFromInt(1000),
		CurrencyCode: "RSD",
		Status:       "unpaid",
	}
	require.NoError(t, db.Create(inst).Error)

	// Delete the installment row so MarkInstallmentPaid fails (can't find the row)
	db.Delete(&model.Installment{}, inst.ID)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db)

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Must have 4 balance calls in order:
	//   0: debit borrower (negative, loan.AccountNumber)
	//   1: credit bank   (positive, bankRSDAccount)
	//   2: compensate bank — reverse the credit (negative, bankRSDAccount)
	//   3: compensate borrower — reverse the debit (positive, loan.AccountNumber)
	require.Len(t, accountClient.calls, 4,
		"expected debit + bank_credit + bank_reversal + client_reversal")
	assert.Equal(t, "BANK-RSD-001", accountClient.calls[2].account,
		"3rd call must be bank-credit reversal (debit bank RSD account back)")
	assert.Equal(t, "ACC-BORROWER-001", accountClient.calls[3].account,
		"4th call must be borrower-debit reversal (credit back to borrower)")
	assert.Equal(t, "-1000.0000", accountClient.calls[2].amount,
		"bank reversal amount must be negative installment amount")
	assert.Equal(t, "1000.0000", accountClient.calls[3].amount,
		"client reversal amount must be positive installment amount")
}
