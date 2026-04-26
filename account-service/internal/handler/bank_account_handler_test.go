package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	pb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type mockBankAccountSvc struct {
	createFn func(currencyCode, accountKind, accountName string, initialBalance decimal.Decimal) (*model.Account, error)
	listFn   func() ([]model.Account, error)
	deleteFn func(id uint64) error
	getRSDFn func() (*model.Account, error)
	debitFn  func(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error)
	creditFn func(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error)
}

func (m *mockBankAccountSvc) CreateBankAccount(currencyCode, accountKind, accountName string, initialBalance decimal.Decimal) (*model.Account, error) {
	if m.createFn != nil {
		return m.createFn(currencyCode, accountKind, accountName, initialBalance)
	}
	return &model.Account{AccountNumber: "BANK-001", CurrencyCode: currencyCode}, nil
}

func (m *mockBankAccountSvc) ListBankAccounts() ([]model.Account, error) {
	if m.listFn != nil {
		return m.listFn()
	}
	return nil, nil
}

func (m *mockBankAccountSvc) DeleteBankAccount(id uint64) error {
	if m.deleteFn != nil {
		return m.deleteFn(id)
	}
	return nil
}

func (m *mockBankAccountSvc) GetBankRSDAccount() (*model.Account, error) {
	if m.getRSDFn != nil {
		return m.getRSDFn()
	}
	return &model.Account{AccountNumber: "BANK-RSD-001", CurrencyCode: "RSD"}, nil
}

func (m *mockBankAccountSvc) DebitBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	if m.debitFn != nil {
		return m.debitFn(ctx, currency, amountStr, reference, reason)
	}
	return &repository.BankOpResult{AccountNumber: "BANK-001", NewBalance: "0"}, nil
}

func (m *mockBankAccountSvc) CreditBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	if m.creditFn != nil {
		return m.creditFn(ctx, currency, amountStr, reference, reason)
	}
	return &repository.BankOpResult{AccountNumber: "BANK-001", NewBalance: "0"}, nil
}

type mockBankProducer struct {
	created []kafkamsg.AccountCreatedMessage
	err     error
}

func (m *mockBankProducer) PublishAccountCreated(_ context.Context, msg kafkamsg.AccountCreatedMessage) error {
	m.created = append(m.created, msg)
	return m.err
}

// ---------------------------------------------------------------------------
// CreateBankAccount
// ---------------------------------------------------------------------------

func TestBankAccountHandler_CreateBankAccount_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		createFn: func(cc, kind, name string, _ decimal.Decimal) (*model.Account, error) {
			return &model.Account{
				ID: 1, AccountNumber: "BANK-EUR-001",
				OwnerID: 1_000_000_000, CurrencyCode: cc, AccountKind: kind, AccountName: name,
			}, nil
		},
	}
	prod := &mockBankProducer{}
	h := newBankAccountHandlerForTest(svc, prod)
	resp, err := h.CreateBankAccount(context.Background(), &pb.CreateBankAccountRequest{
		CurrencyCode: "EUR", AccountKind: "current", AccountName: "Bank EUR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AccountNumber != "BANK-EUR-001" {
		t.Errorf("unexpected account number: %s", resp.AccountNumber)
	}
	if len(prod.created) != 1 {
		t.Errorf("expected 1 produced event, got %d", len(prod.created))
	}
}

func TestBankAccountHandler_CreateBankAccount_ServiceError(t *testing.T) {
	svc := &mockBankAccountSvc{
		createFn: func(_, _, _ string, _ decimal.Decimal) (*model.Account, error) {
			return nil, errors.New("invalid currency code")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.CreateBankAccount(context.Background(), &pb.CreateBankAccountRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ListBankAccounts
// ---------------------------------------------------------------------------

func TestBankAccountHandler_ListBankAccounts_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		listFn: func() ([]model.Account, error) {
			return []model.Account{
				{ID: 1, AccountNumber: "BANK-RSD-001", CurrencyCode: "RSD"},
				{ID: 2, AccountNumber: "BANK-EUR-001", CurrencyCode: "EUR"},
			}, nil
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	resp, err := h.ListBankAccounts(context.Background(), &pb.ListBankAccountsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Accounts) != 2 {
		t.Errorf("expected 2 accounts, got %d", len(resp.Accounts))
	}
}

func TestBankAccountHandler_ListBankAccounts_Error(t *testing.T) {
	svc := &mockBankAccountSvc{
		listFn: func() ([]model.Account, error) {
			return nil, errors.New("db down")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.ListBankAccounts(context.Background(), &pb.ListBankAccountsRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// DeleteBankAccount
// ---------------------------------------------------------------------------

func TestBankAccountHandler_DeleteBankAccount_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		deleteFn: func(_ uint64) error { return nil },
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	resp, err := h.DeleteBankAccount(context.Background(), &pb.DeleteBankAccountRequest{Id: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
}

func TestBankAccountHandler_DeleteBankAccount_NotFound(t *testing.T) {
	svc := &mockBankAccountSvc{
		deleteFn: func(_ uint64) error { return gorm.ErrRecordNotFound },
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.DeleteBankAccount(context.Background(), &pb.DeleteBankAccountRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestBankAccountHandler_DeleteBankAccount_Constraint(t *testing.T) {
	svc := &mockBankAccountSvc{
		deleteFn: func(_ uint64) error {
			return errors.New("bank must have at least one RSD account")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.DeleteBankAccount(context.Background(), &pb.DeleteBankAccountRequest{Id: 1})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal (default), got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetBankRSDAccount
// ---------------------------------------------------------------------------

func TestBankAccountHandler_GetBankRSDAccount_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		getRSDFn: func() (*model.Account, error) {
			return &model.Account{ID: 1, AccountNumber: "BANK-RSD-001", CurrencyCode: "RSD"}, nil
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	resp, err := h.GetBankRSDAccount(context.Background(), &pb.GetBankRSDAccountRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.CurrencyCode != "RSD" {
		t.Errorf("expected RSD, got %s", resp.CurrencyCode)
	}
}

func TestBankAccountHandler_GetBankRSDAccount_NotFound(t *testing.T) {
	svc := &mockBankAccountSvc{
		getRSDFn: func() (*model.Account, error) {
			return nil, errors.New("no bank RSD account configured")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.GetBankRSDAccount(context.Background(), &pb.GetBankRSDAccountRequest{})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// DebitBankAccount
// ---------------------------------------------------------------------------

func TestBankAccountHandler_DebitBankAccount_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		debitFn: func(_ context.Context, currency, amount, _, _ string) (*repository.BankOpResult, error) {
			return &repository.BankOpResult{AccountNumber: "BANK-" + currency, NewBalance: "9000"}, nil
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	resp, err := h.DebitBankAccount(context.Background(), &pb.BankAccountOpRequest{
		Currency: "EUR", Amount: "1000", Reference: "ref-1", Reason: "test debit",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.NewBalance != "9000" {
		t.Errorf("unexpected new balance: %s", resp.NewBalance)
	}
}

func TestBankAccountHandler_DebitBankAccount_InsufficientLiquidity(t *testing.T) {
	svc := &mockBankAccountSvc{
		debitFn: func(_ context.Context, _, _, _, _ string) (*repository.BankOpResult, error) {
			return nil, repository.ErrInsufficientBankLiquidity
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.DebitBankAccount(context.Background(), &pb.BankAccountOpRequest{Currency: "EUR", Amount: "100"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestBankAccountHandler_DebitBankAccount_NoBankAccount(t *testing.T) {
	svc := &mockBankAccountSvc{
		debitFn: func(_ context.Context, _, _, _, _ string) (*repository.BankOpResult, error) {
			return nil, repository.ErrBankAccountNotFound
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.DebitBankAccount(context.Background(), &pb.BankAccountOpRequest{Currency: "JPY", Amount: "100"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestBankAccountHandler_DebitBankAccount_OtherError(t *testing.T) {
	svc := &mockBankAccountSvc{
		debitFn: func(_ context.Context, _, _, _, _ string) (*repository.BankOpResult, error) {
			return nil, errors.New("connection lost")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.DebitBankAccount(context.Background(), &pb.BankAccountOpRequest{Currency: "EUR", Amount: "100"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// CreditBankAccount
// ---------------------------------------------------------------------------

func TestBankAccountHandler_CreditBankAccount_Success(t *testing.T) {
	svc := &mockBankAccountSvc{
		creditFn: func(_ context.Context, currency, _, _, _ string) (*repository.BankOpResult, error) {
			return &repository.BankOpResult{AccountNumber: "BANK-" + currency, NewBalance: "11000"}, nil
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	resp, err := h.CreditBankAccount(context.Background(), &pb.BankAccountOpRequest{
		Currency: "EUR", Amount: "1000", Reference: "ref-1", Reason: "test credit",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.NewBalance != "11000" {
		t.Errorf("unexpected new balance: %s", resp.NewBalance)
	}
}

func TestBankAccountHandler_CreditBankAccount_NoBankAccount(t *testing.T) {
	svc := &mockBankAccountSvc{
		creditFn: func(_ context.Context, _, _, _, _ string) (*repository.BankOpResult, error) {
			return nil, repository.ErrBankAccountNotFound
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.CreditBankAccount(context.Background(), &pb.BankAccountOpRequest{Currency: "JPY", Amount: "100"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestBankAccountHandler_CreditBankAccount_OtherError(t *testing.T) {
	svc := &mockBankAccountSvc{
		creditFn: func(_ context.Context, _, _, _, _ string) (*repository.BankOpResult, error) {
			return nil, errors.New("connection lost")
		},
	}
	h := newBankAccountHandlerForTest(svc, &mockBankProducer{})
	_, err := h.CreditBankAccount(context.Background(), &pb.BankAccountOpRequest{Currency: "EUR", Amount: "100"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}
