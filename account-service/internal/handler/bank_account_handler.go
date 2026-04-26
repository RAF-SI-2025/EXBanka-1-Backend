package handler

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// bankAccountSvcFacade is the subset of *service.AccountService used by BankAccountGRPCHandler.
type bankAccountSvcFacade interface {
	CreateBankAccount(currencyCode, accountKind, accountName string, initialBalance decimal.Decimal) (*model.Account, error)
	ListBankAccounts() ([]model.Account, error)
	DeleteBankAccount(id uint64) error
	GetBankRSDAccount() (*model.Account, error)
	DebitBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error)
	CreditBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error)
}

// bankProducer is the subset of *kafkaprod.Producer used by BankAccountGRPCHandler.
type bankProducer interface {
	PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error
}

type BankAccountGRPCHandler struct {
	pb.UnimplementedBankAccountServiceServer
	accountSvc bankAccountSvcFacade
	producer   bankProducer
}

func NewBankAccountGRPCHandler(accountSvc *service.AccountService, producer *kafkaprod.Producer) *BankAccountGRPCHandler {
	return &BankAccountGRPCHandler{accountSvc: accountSvc, producer: producer}
}

// newBankAccountHandlerForTest constructs a BankAccountGRPCHandler with
// interface-typed dependencies for use in unit tests.
func newBankAccountHandlerForTest(accountSvc bankAccountSvcFacade, producer bankProducer) *BankAccountGRPCHandler {
	return &BankAccountGRPCHandler{accountSvc: accountSvc, producer: producer}
}

func (h *BankAccountGRPCHandler) CreateBankAccount(ctx context.Context, req *pb.CreateBankAccountRequest) (*pb.AccountResponse, error) {
	account, err := h.accountSvc.CreateBankAccount(req.CurrencyCode, req.AccountKind, req.AccountName, decimal.Zero)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create bank account: %v", err)
	}
	_ = h.producer.PublishAccountCreated(ctx, kafkamsg.AccountCreatedMessage{
		AccountNumber: account.AccountNumber,
		OwnerID:       account.OwnerID,
		AccountKind:   account.AccountKind,
		CurrencyCode:  account.CurrencyCode,
	})
	return toAccountResponse(account), nil
}

func (h *BankAccountGRPCHandler) ListBankAccounts(ctx context.Context, req *pb.ListBankAccountsRequest) (*pb.ListBankAccountsResponse, error) {
	accounts, err := h.accountSvc.ListBankAccounts()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list bank accounts: %v", err)
	}
	resp := &pb.ListBankAccountsResponse{Accounts: make([]*pb.AccountResponse, 0, len(accounts))}
	for _, a := range accounts {
		a := a
		resp.Accounts = append(resp.Accounts, toAccountResponse(&a))
	}
	return resp, nil
}

func (h *BankAccountGRPCHandler) DeleteBankAccount(ctx context.Context, req *pb.DeleteBankAccountRequest) (*pb.DeleteBankAccountResponse, error) {
	if err := h.accountSvc.DeleteBankAccount(req.Id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "bank account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "%s", err.Error())
	}
	return &pb.DeleteBankAccountResponse{Success: true, Message: "bank account deleted"}, nil
}

func (h *BankAccountGRPCHandler) GetBankRSDAccount(ctx context.Context, req *pb.GetBankRSDAccountRequest) (*pb.AccountResponse, error) {
	account, err := h.accountSvc.GetBankRSDAccount()
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "no bank RSD account found: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *BankAccountGRPCHandler) DebitBankAccount(ctx context.Context, req *pb.BankAccountOpRequest) (*pb.BankAccountOpResponse, error) {
	res, err := h.accountSvc.DebitBankAccount(ctx, req.Currency, req.Amount, req.Reference, req.Reason)
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientBankLiquidity) {
			return nil, status.Errorf(codes.FailedPrecondition, "bank has insufficient liquidity in %s", req.Currency)
		}
		if errors.Is(err, repository.ErrBankAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "no bank sentinel account for currency %s", req.Currency)
		}
		return nil, status.Errorf(codes.Internal, "debit bank: %v", err)
	}
	return &pb.BankAccountOpResponse{
		AccountNumber: res.AccountNumber,
		NewBalance:    res.NewBalance,
		Replayed:      res.Replayed,
	}, nil
}

func (h *BankAccountGRPCHandler) CreditBankAccount(ctx context.Context, req *pb.BankAccountOpRequest) (*pb.BankAccountOpResponse, error) {
	res, err := h.accountSvc.CreditBankAccount(ctx, req.Currency, req.Amount, req.Reference, req.Reason)
	if err != nil {
		if errors.Is(err, repository.ErrBankAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "no bank sentinel account for currency %s", req.Currency)
		}
		return nil, status.Errorf(codes.Internal, "credit bank: %v", err)
	}
	return &pb.BankAccountOpResponse{
		AccountNumber: res.AccountNumber,
		NewBalance:    res.NewBalance,
		Replayed:      res.Replayed,
	}, nil
}
