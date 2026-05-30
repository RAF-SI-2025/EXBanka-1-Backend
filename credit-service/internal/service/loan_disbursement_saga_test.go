package service

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/shopspring/decimal"
)

// fakeSagaAccountClient is a minimal stub for accountpb.AccountServiceClient.
type fakeSagaAccountClient struct {
	failOnCredit bool
	credited     bool
	reversed     bool
}

func (f *fakeSagaAccountClient) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if f.failOnCredit && req.Amount != "" && req.Amount[0] != '-' {
		return nil, errors.New("simulated credit failure")
	}
	if req.Amount != "" && req.Amount[0] == '-' {
		f.reversed = true
	} else {
		f.credited = true
	}
	return &accountpb.AccountResponse{}, nil
}

func (f *fakeSagaAccountClient) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetAccountByNumber(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ReserveIncoming(_ context.Context, _ *accountpb.ReserveIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) CommitIncoming(_ context.Context, _ *accountpb.CommitIncomingRequest, _ ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ReleaseIncoming(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ListChangelog(_ context.Context, _ *accountpb.ListChangelogRequest, _ ...grpc.CallOption) (*accountpb.ListChangelogResponse, error) {
	panic("unexpected call")
}
func (f *fakeSagaAccountClient) ListAllChangelogs(_ context.Context, _ *accountpb.ListAllChangelogsRequest, _ ...grpc.CallOption) (*accountpb.ListAllChangelogsResponse, error) {
	return &accountpb.ListAllChangelogsResponse{}, nil
}

// fakeBankClient stubs the BankAccountServiceClient.
type fakeBankClient struct {
	debited    bool
	creditback bool
	failDebit  bool
}

func (f *fakeBankClient) DebitBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	if f.failDebit {
		return nil, errors.New("simulated debit failure")
	}
	f.debited = true
	return &accountpb.BankAccountOpResponse{}, nil
}
func (f *fakeBankClient) CreditBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	f.creditback = true
	return &accountpb.BankAccountOpResponse{}, nil
}
func (f *fakeBankClient) GetBankRSDAccount(_ context.Context, _ *accountpb.GetBankRSDAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeBankClient) ListBankAccounts(_ context.Context, _ *accountpb.ListBankAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListBankAccountsResponse, error) {
	panic("unexpected call")
}
func (f *fakeBankClient) CreateBankAccount(_ context.Context, _ *accountpb.CreateBankAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	panic("unexpected call")
}
func (f *fakeBankClient) DeleteBankAccount(_ context.Context, _ *accountpb.DeleteBankAccountRequest, _ ...grpc.CallOption) (*accountpb.DeleteBankAccountResponse, error) {
	panic("unexpected call")
}

// testLoan returns a minimal loan fixture for saga testing.
func testLoan() *model.Loan {
	return &model.Loan{
		ID:            42,
		AccountNumber: "111-borrower-account",
		Amount:        decimal.NewFromInt(10000),
		CurrencyCode:  "RSD",
		Status:        "approved",
		Version:       1,
	}
}

func TestDisburseLoanSaga_SuccessPath(t *testing.T) {
	t.Skip("Skeleton — full DB-backed test requires a real or in-memory GORM DB with SagaLog table; see A1.6 integration tests")
}

func TestDisburseLoanSaga_CreditFailureCompensates(t *testing.T) {
	t.Skip("Skeleton — full DB-backed test requires a real or in-memory GORM DB with SagaLog table; see A1.6 integration tests")
}

func TestDisburseLoanSaga_IdempotentOnActiveLoan(t *testing.T) {
	// If loan is already active the saga should short-circuit without calling any clients.
	bankClient := &fakeBankClient{}
	acctClient := &fakeSagaAccountClient{}

	s := NewLoanDisbursementSaga(bankClient, acctClient, nil, nil)
	loan := testLoan()
	loan.Status = "active"

	if err := s.Disburse(context.Background(), loan); err != nil {
		t.Fatalf("expected nil for already-active loan, got: %v", err)
	}
	if bankClient.debited {
		t.Fatal("bank should not be debited for an already-active loan")
	}
}

func (*fakeSagaAccountClient) ReserveOutgoing(context.Context, *accountpb.ReserveOutgoingRequest, ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
	return nil, nil
}
func (*fakeSagaAccountClient) SettleOutgoing(context.Context, *accountpb.SettleOutgoingRequest, ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
	return nil, nil
}
func (*fakeSagaAccountClient) ReleaseOutgoing(context.Context, *accountpb.ReleaseOutgoingRequest, ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
	return nil, nil
}
