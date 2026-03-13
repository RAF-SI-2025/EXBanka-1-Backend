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
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/service"
)

type AccountGRPCHandler struct {
	pb.UnimplementedAccountServiceServer
	accountService  *service.AccountService
	companyService  *service.CompanyService
	currencyService *service.CurrencyService
	producer        *kafkaprod.Producer
}

func NewAccountGRPCHandler(
	accountService *service.AccountService,
	companyService *service.CompanyService,
	currencyService *service.CurrencyService,
	producer *kafkaprod.Producer,
) *AccountGRPCHandler {
	return &AccountGRPCHandler{
		accountService:  accountService,
		companyService:  companyService,
		currencyService: currencyService,
		producer:        producer,
	}
}

func (h *AccountGRPCHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.AccountResponse, error) {
	account := &model.Account{
		OwnerID:         req.OwnerId,
		AccountKind:     req.AccountKind,
		AccountType:     req.AccountType,
		AccountCategory: req.AccountCategory,
		CurrencyCode:    req.CurrencyCode,
		EmployeeID:      req.EmployeeId,
		Balance:         req.InitialBalance,
		AvailableBalance: req.InitialBalance,
		CompanyID:       req.CompanyId,
	}

	if err := h.accountService.CreateAccount(account); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	_ = h.producer.PublishAccountCreated(ctx, kafkamsg.AccountCreatedMessage{
		AccountNumber: account.AccountNumber,
		OwnerID:       account.OwnerID,
		AccountKind:   account.AccountKind,
		CurrencyCode:  account.CurrencyCode,
	})

	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) GetAccount(ctx context.Context, req *pb.GetAccountRequest) (*pb.AccountResponse, error) {
	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) GetAccountByNumber(ctx context.Context, req *pb.GetAccountByNumberRequest) (*pb.AccountResponse, error) {
	account, err := h.accountService.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) ListAccountsByClient(ctx context.Context, req *pb.ListAccountsByClientRequest) (*pb.ListAccountsResponse, error) {
	accounts, total, err := h.accountService.ListAccountsByClient(
		req.ClientId, int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	resp := &pb.ListAccountsResponse{Total: total}
	for _, a := range accounts {
		a := a
		resp.Accounts = append(resp.Accounts, toAccountResponse(&a))
	}
	return resp, nil
}

func (h *AccountGRPCHandler) ListAllAccounts(ctx context.Context, req *pb.ListAllAccountsRequest) (*pb.ListAccountsResponse, error) {
	accounts, total, err := h.accountService.ListAllAccounts(
		req.NameFilter, req.AccountNumberFilter, req.TypeFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	resp := &pb.ListAccountsResponse{Total: total}
	for _, a := range accounts {
		a := a
		resp.Accounts = append(resp.Accounts, toAccountResponse(&a))
	}
	return resp, nil
}

func (h *AccountGRPCHandler) UpdateAccountName(ctx context.Context, req *pb.UpdateAccountNameRequest) (*pb.AccountResponse, error) {
	if err := h.accountService.UpdateAccountName(req.Id, req.ClientId, req.NewName); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found or not owned by client")
		}
		return nil, status.Errorf(codes.Internal, "failed to update account name: %v", err)
	}

	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) UpdateAccountLimits(ctx context.Context, req *pb.UpdateAccountLimitsRequest) (*pb.AccountResponse, error) {
	if err := h.accountService.UpdateAccountLimits(req.Id, req.DailyLimit, req.MonthlyLimit); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.InvalidArgument, "failed to update account limits: %v", err)
	}

	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) UpdateAccountStatus(ctx context.Context, req *pb.UpdateAccountStatusRequest) (*pb.AccountResponse, error) {
	if err := h.accountService.UpdateAccountStatus(req.Id, req.Status); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.InvalidArgument, "failed to update account status: %v", err)
	}

	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}

	_ = h.producer.PublishAccountStatusChanged(ctx, kafkaprod.AccountStatusChangedMsg{
		AccountNumber: account.AccountNumber,
		Status:        account.Status,
	})

	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) UpdateBalance(ctx context.Context, req *pb.UpdateBalanceRequest) (*pb.AccountResponse, error) {
	if err := h.accountService.UpdateBalance(req.AccountNumber, req.Amount, req.UpdateAvailable); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to update balance: %v", err)
	}

	account, err := h.accountService.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) CreateCompany(ctx context.Context, req *pb.CreateCompanyRequest) (*pb.CompanyResponse, error) {
	company := &model.Company{
		CompanyName:        req.CompanyName,
		RegistrationNumber: req.RegistrationNumber,
		TaxNumber:          req.TaxNumber,
		ActivityCode:       req.ActivityCode,
		Address:            req.Address,
		OwnerID:            req.OwnerId,
	}

	if err := h.companyService.Create(company); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) GetCompany(ctx context.Context, req *pb.GetCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) UpdateCompany(ctx context.Context, req *pb.UpdateCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get company: %v", err)
	}

	if req.CompanyName != nil {
		company.CompanyName = *req.CompanyName
	}
	if req.ActivityCode != nil {
		company.ActivityCode = *req.ActivityCode
	}
	if req.Address != nil {
		company.Address = *req.Address
	}
	if req.OwnerId != nil {
		company.OwnerID = *req.OwnerId
	}

	if err := h.companyService.Update(company); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) ListCurrencies(ctx context.Context, req *pb.ListCurrenciesRequest) (*pb.ListCurrenciesResponse, error) {
	currencies, err := h.currencyService.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list currencies: %v", err)
	}

	resp := &pb.ListCurrenciesResponse{}
	for _, c := range currencies {
		c := c
		resp.Currencies = append(resp.Currencies, toCurrencyResponse(&c))
	}
	return resp, nil
}

func (h *AccountGRPCHandler) GetCurrency(ctx context.Context, req *pb.GetCurrencyRequest) (*pb.CurrencyResponse, error) {
	currency, err := h.currencyService.GetByCode(req.Code)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "currency not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get currency: %v", err)
	}
	return toCurrencyResponse(currency), nil
}

func toAccountResponse(a *model.Account) *pb.AccountResponse {
	resp := &pb.AccountResponse{
		Id:               a.ID,
		AccountNumber:    a.AccountNumber,
		AccountName:      a.AccountName,
		OwnerId:          a.OwnerID,
		OwnerName:        a.OwnerName,
		Balance:          a.Balance,
		AvailableBalance: a.AvailableBalance,
		EmployeeId:       a.EmployeeID,
		CreatedAt:        a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		ExpiresAt:        a.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CurrencyCode:     a.CurrencyCode,
		Status:           a.Status,
		AccountKind:      a.AccountKind,
		AccountType:      a.AccountType,
		AccountCategory:  a.AccountCategory,
		MaintenanceFee:   a.MaintenanceFee,
		DailyLimit:       a.DailyLimit,
		MonthlyLimit:     a.MonthlyLimit,
		DailySpending:    a.DailySpending,
		MonthlySpending:  a.MonthlySpending,
		CompanyId:        a.CompanyID,
	}
	return resp
}

func toCompanyResponse(c *model.Company) *pb.CompanyResponse {
	return &pb.CompanyResponse{
		Id:                 c.ID,
		CompanyName:        c.CompanyName,
		RegistrationNumber: c.RegistrationNumber,
		TaxNumber:          c.TaxNumber,
		ActivityCode:       c.ActivityCode,
		Address:            c.Address,
		OwnerId:            c.OwnerID,
		CreatedAt:          c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toCurrencyResponse(c *model.Currency) *pb.CurrencyResponse {
	return &pb.CurrencyResponse{
		Id:          c.ID,
		Name:        c.Name,
		Code:        c.Code,
		Symbol:      c.Symbol,
		Country:     c.Country,
		Description: c.Description,
		Active:      c.Active,
	}
}
