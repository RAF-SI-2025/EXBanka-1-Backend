package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/service"
)

type BankAccountGRPCHandler struct {
	pb.UnimplementedBankAccountServiceServer
	accountSvc *service.AccountService
	producer   *kafkaprod.Producer
}

func NewBankAccountGRPCHandler(accountSvc *service.AccountService, producer *kafkaprod.Producer) *BankAccountGRPCHandler {
	return &BankAccountGRPCHandler{accountSvc: accountSvc, producer: producer}
}

func (h *BankAccountGRPCHandler) CreateBankAccount(ctx context.Context, req *pb.CreateBankAccountRequest) (*pb.AccountResponse, error) {
	account, err := h.accountSvc.CreateBankAccount(req.CurrencyCode, req.AccountKind, req.AccountName)
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
	resp := &pb.ListBankAccountsResponse{}
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
