package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/changelog"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"),
		strings.Contains(msg, "is required"), strings.Contains(msg, "cannot use"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"),
		strings.Contains(msg, "already has"):
		return codes.AlreadyExists
	case strings.Contains(msg, "already "), strings.Contains(msg, "cannot delete"),
		strings.Contains(msg, "insufficient funds"), strings.Contains(msg, "limit exceeded"),
		strings.Contains(msg, "spending limit"), strings.Contains(msg, "is not a bank account"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

type AccountGRPCHandler struct {
	pb.UnimplementedAccountServiceServer
	accountService  *service.AccountService
	companyService  *service.CompanyService
	currencyService *service.CurrencyService
	ledgerService   *service.LedgerService
	producer        *kafkaprod.Producer
	clientClient    clientpb.ClientServiceClient
}

func NewAccountGRPCHandler(
	accountService *service.AccountService,
	companyService *service.CompanyService,
	currencyService *service.CurrencyService,
	ledgerService *service.LedgerService,
	producer *kafkaprod.Producer,
	clientClient clientpb.ClientServiceClient,
) *AccountGRPCHandler {
	return &AccountGRPCHandler{
		accountService:  accountService,
		companyService:  companyService,
		currencyService: currencyService,
		ledgerService:   ledgerService,
		producer:        producer,
		clientClient:    clientClient,
	}
}

func (h *AccountGRPCHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.AccountResponse, error) {
	initialBalance, _ := decimal.NewFromString(req.InitialBalance)
	account := &model.Account{
		OwnerID:          req.OwnerId,
		AccountKind:      req.AccountKind,
		AccountType:      req.AccountType,
		AccountCategory:  req.AccountCategory,
		CurrencyCode:     req.CurrencyCode,
		EmployeeID:       req.EmployeeId,
		Balance:          initialBalance,
		AvailableBalance: initialBalance,
		CompanyID:        req.CompanyId,
	}

	if err := h.accountService.CreateAccount(account); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create account: %v", err)
	}

	_ = h.producer.PublishAccountCreated(ctx, kafkamsg.AccountCreatedMessage{
		AccountNumber: account.AccountNumber,
		OwnerID:       account.OwnerID,
		AccountKind:   account.AccountKind,
		CurrencyCode:  account.CurrencyCode,
	})

	// General notification (no email)
	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  account.OwnerID,
		Type:    "account_created",
		Title:   "New Account Created",
		Message: fmt.Sprintf("Your new %s account (%s) has been created.", account.CurrencyCode, account.AccountNumber),
		RefType: "account",
		RefID:   account.ID,
	})

	// Send email notification to account owner.
	if h.clientClient != nil && h.producer != nil {
		clientResp, clientErr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: account.OwnerID})
		if clientErr == nil {
			emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        clientResp.Email,
				EmailType: kafkamsg.EmailTypeAccountCreated,
				Data: map[string]string{
					"account_number": account.AccountNumber,
					"account_name":   account.AccountName,
					"currency":       account.CurrencyCode,
				},
			})
			if emailErr != nil {
				log.Printf("warn: failed to send account creation email to %s: %v", clientResp.Email, emailErr)
			}
		} else {
			log.Printf("warn: failed to fetch client %d for account creation email: %v", account.OwnerID, clientErr)
		}
	}

	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) GetAccount(ctx context.Context, req *pb.GetAccountRequest) (*pb.AccountResponse, error) {
	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) GetAccountByNumber(ctx context.Context, req *pb.GetAccountByNumberRequest) (*pb.AccountResponse, error) {
	account, err := h.accountService.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) ListAccountsByClient(ctx context.Context, req *pb.ListAccountsByClientRequest) (*pb.ListAccountsResponse, error) {
	accounts, total, err := h.accountService.ListAccountsByClient(
		req.ClientId, int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list accounts: %v", err)
	}

	resp := &pb.ListAccountsResponse{Total: total, Accounts: make([]*pb.AccountResponse, 0, len(accounts))}
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
		return nil, status.Errorf(mapServiceError(err), "failed to list accounts: %v", err)
	}

	resp := &pb.ListAccountsResponse{Total: total, Accounts: make([]*pb.AccountResponse, 0, len(accounts))}
	for _, a := range accounts {
		a := a
		resp.Accounts = append(resp.Accounts, toAccountResponse(&a))
	}
	return resp, nil
}

func (h *AccountGRPCHandler) UpdateAccountName(ctx context.Context, req *pb.UpdateAccountNameRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountName(req.Id, req.ClientId, req.NewName, changedBy); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found or not owned by client")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update account name: %v", err)
	}

	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) UpdateAccountLimits(ctx context.Context, req *pb.UpdateAccountLimitsRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountLimits(req.Id, req.DailyLimit, req.MonthlyLimit, changedBy); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update account limits: %v", err)
	}

	account, err := h.accountService.GetAccount(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch updated account: %v", err)
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) UpdateAccountStatus(ctx context.Context, req *pb.UpdateAccountStatusRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountStatus(req.Id, req.Status, changedBy); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update account status: %v", err)
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
	amount, _ := decimal.NewFromString(req.Amount)
	if err := h.accountService.UpdateBalance(req.AccountNumber, amount, req.UpdateAvailable); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update balance: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "failed to create company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) GetCompany(ctx context.Context, req *pb.GetCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) UpdateCompany(ctx context.Context, req *pb.UpdateCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get company: %v", err)
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
		return nil, status.Errorf(mapServiceError(err), "failed to update company: %v", err)
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) ListCurrencies(ctx context.Context, req *pb.ListCurrenciesRequest) (*pb.ListCurrenciesResponse, error) {
	currencies, err := h.currencyService.List()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list currencies: %v", err)
	}

	resp := &pb.ListCurrenciesResponse{Currencies: make([]*pb.CurrencyResponse, 0, len(currencies))}
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
		return nil, status.Errorf(mapServiceError(err), "failed to get currency: %v", err)
	}
	return toCurrencyResponse(currency), nil
}

func (h *AccountGRPCHandler) GetLedgerEntries(ctx context.Context, req *pb.GetLedgerEntriesRequest) (*pb.GetLedgerEntriesResponse, error) {
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	entries, total, err := h.ledgerService.GetLedgerEntries(req.AccountNumber, page, pageSize)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to get ledger entries: %v", err)
	}

	resp := &pb.GetLedgerEntriesResponse{TotalCount: total, Entries: make([]*pb.LedgerEntryResponse, 0, len(entries))}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, &pb.LedgerEntryResponse{
			Id:            e.ID,
			AccountNumber: e.AccountNumber,
			EntryType:     e.EntryType,
			Amount:        e.Amount.StringFixed(4),
			BalanceBefore: e.BalanceBefore.StringFixed(4),
			BalanceAfter:  e.BalanceAfter.StringFixed(4),
			Description:   e.Description,
			ReferenceId:   e.ReferenceID,
			ReferenceType: e.ReferenceType,
			CreatedAt:     e.CreatedAt.Unix(),
		})
	}
	return resp, nil
}

func toAccountResponse(a *model.Account) *pb.AccountResponse {
	resp := &pb.AccountResponse{
		Id:               a.ID,
		AccountNumber:    a.AccountNumber,
		AccountName:      a.AccountName,
		OwnerId:          a.OwnerID,
		OwnerName:        a.OwnerName,
		Balance:          a.Balance.StringFixed(4),
		AvailableBalance: a.AvailableBalance.StringFixed(4),
		EmployeeId:       a.EmployeeID,
		CreatedAt:        a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		ExpiresAt:        a.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CurrencyCode:     a.CurrencyCode,
		Status:           a.Status,
		AccountKind:      a.AccountKind,
		AccountType:      a.AccountType,
		AccountCategory:  a.AccountCategory,
		MaintenanceFee:   a.MaintenanceFee.StringFixed(4),
		DailyLimit:       a.DailyLimit.StringFixed(4),
		MonthlyLimit:     a.MonthlyLimit.StringFixed(4),
		DailySpending:    a.DailySpending.StringFixed(4),
		MonthlySpending:  a.MonthlySpending.StringFixed(4),
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
