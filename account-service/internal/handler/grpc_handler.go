package handler

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/changelog"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// accountSvcFacade is the subset of *service.AccountService used by AccountGRPCHandler.
type accountSvcFacade interface {
	CreateAccount(account *model.Account) error
	GetAccount(id uint64) (*model.Account, error)
	GetAccountByNumber(accountNumber string) (*model.Account, error)
	ListAccountsByClient(clientID uint64, page, pageSize int) ([]model.Account, int64, error)
	ListAllAccounts(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error)
	UpdateAccountName(id, clientID uint64, newName string, changedBy int64) error
	UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error
	UpdateAccountStatus(id uint64, newStatus string, changedBy int64) error
	UpdateBalanceWithOpts(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error
}

// companySvcFacade is the subset of *service.CompanyService used by AccountGRPCHandler.
type companySvcFacade interface {
	Create(company *model.Company) error
	Get(id uint64) (*model.Company, error)
	Update(company *model.Company) error
}

// currencySvcFacade is the subset of *service.CurrencyService used by AccountGRPCHandler.
type currencySvcFacade interface {
	List() ([]model.Currency, error)
	GetByCode(code string) (*model.Currency, error)
}

// ledgerSvcFacade is the subset of *service.LedgerService used by AccountGRPCHandler.
type ledgerSvcFacade interface {
	GetLedgerEntries(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error)
}

// accountProducer is the subset of *kafkaprod.Producer used by AccountGRPCHandler.
type accountProducer interface {
	PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error
	PublishAccountStatusChanged(ctx context.Context, msg kafkaprod.AccountStatusChangedMsg) error
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
	SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error
}

type AccountGRPCHandler struct {
	pb.UnimplementedAccountServiceServer
	accountService      accountSvcFacade
	companyService      companySvcFacade
	currencyService     currencySvcFacade
	ledgerService       ledgerSvcFacade
	reservation         *ReservationHandler
	incomingReservation *service.IncomingReservationService
	producer            accountProducer
	clientClient        clientpb.ClientServiceClient
	// db + idem wire saga-step idempotency for handlers that follow the
	// IdempotencyRepository.Run pattern (UpdateBalance is the lighthouse
	// case). Other RPCs leave them nil and use their existing dedup paths.
	db   *gorm.DB
	idem *repository.IdempotencyRepository
}

func NewAccountGRPCHandler(
	accountService *service.AccountService,
	companyService *service.CompanyService,
	currencyService *service.CurrencyService,
	ledgerService *service.LedgerService,
	reservation *ReservationHandler,
	incomingReservation *service.IncomingReservationService,
	producer *kafkaprod.Producer,
	clientClient clientpb.ClientServiceClient,
	db *gorm.DB,
	idem *repository.IdempotencyRepository,
) *AccountGRPCHandler {
	return &AccountGRPCHandler{
		accountService:      accountService,
		companyService:      companyService,
		currencyService:     currencyService,
		ledgerService:       ledgerService,
		reservation:         reservation,
		incomingReservation: incomingReservation,
		producer:            producer,
		clientClient:        clientClient,
		db:                  db,
		idem:                idem,
	}
}

// ReserveFunds forwards to the reservation handler.
func (h *AccountGRPCHandler) ReserveFunds(ctx context.Context, req *pb.ReserveFundsRequest) (*pb.ReserveFundsResponse, error) {
	return h.reservation.ReserveFunds(ctx, req)
}

// ReleaseReservation forwards to the reservation handler.
func (h *AccountGRPCHandler) ReleaseReservation(ctx context.Context, req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	return h.reservation.ReleaseReservation(ctx, req)
}

// PartialSettleReservation forwards to the reservation handler.
func (h *AccountGRPCHandler) PartialSettleReservation(ctx context.Context, req *pb.PartialSettleReservationRequest) (*pb.PartialSettleReservationResponse, error) {
	return h.reservation.PartialSettleReservation(ctx, req)
}

// GetReservation forwards to the reservation handler.
func (h *AccountGRPCHandler) GetReservation(ctx context.Context, req *pb.GetReservationRequest) (*pb.GetReservationResponse, error) {
	return h.reservation.GetReservation(ctx, req)
}

// ReserveIncoming creates a pending credit reservation for an inter-bank
// inbound transfer. Does not change the account balance.
func (h *AccountGRPCHandler) ReserveIncoming(ctx context.Context, req *pb.ReserveIncomingRequest) (*pb.ReserveIncomingResponse, error) {
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	res, err := h.incomingReservation.ReserveIncoming(ctx, req.AccountNumber, amt, req.Currency, req.ReservationKey)
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	acct, _ := h.accountService.GetAccountByNumber(res.AccountNumber)
	balanceAfter := ""
	if acct != nil {
		balanceAfter = acct.Balance.StringFixed(4)
	}
	return &pb.ReserveIncomingResponse{ReservationKey: res.ReservationKey, BalanceAfter: balanceAfter}, nil
}

// CommitIncoming finalizes the credit and writes a ledger entry.
func (h *AccountGRPCHandler) CommitIncoming(ctx context.Context, req *pb.CommitIncomingRequest) (*pb.CommitIncomingResponse, error) {
	acct, err := h.incomingReservation.CommitIncoming(ctx, req.ReservationKey)
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	return &pb.CommitIncomingResponse{BalanceAfter: acct.Balance.StringFixed(4)}, nil
}

// ReleaseIncoming cancels a pending credit reservation.
func (h *AccountGRPCHandler) ReleaseIncoming(ctx context.Context, req *pb.ReleaseIncomingRequest) (*pb.ReleaseIncomingResponse, error) {
	if err := h.incomingReservation.ReleaseIncoming(ctx, req.ReservationKey); err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	return &pb.ReleaseIncomingResponse{Released: true}, nil
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
		return nil, err
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
		return nil, err
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) GetAccountByNumber(ctx context.Context, req *pb.GetAccountByNumberRequest) (*pb.AccountResponse, error) {
	account, err := h.accountService.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, err
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) ListAccountsByClient(ctx context.Context, req *pb.ListAccountsByClientRequest) (*pb.ListAccountsResponse, error) {
	accounts, total, err := h.accountService.ListAccountsByClient(
		req.ClientId, int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, err
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
		return nil, err
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
		return nil, err
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
		return nil, err
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
		return nil, err
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

// UpdateBalance is the lighthouse for the saga-step idempotency contract
// (Plan 2026-04-27 Task 8). The request MUST carry idempotency_key — saga
// steps may be retried after caller crash, compensator restart, or network
// timeout, and the cache lets retries return the cached AccountResponse
// without re-running the balance mutation.
//
// Two layers of dedup cooperate:
//   - IdempotencyRepository.Run caches the wire response under the key in
//     the idempotency_records table and returns it verbatim on retry.
//   - The existing repository.UpdateBalance partial unique index on
//     ledger_entries.idempotency_key remains the authoritative side-effect
//     dedup, so even bypasses of this handler stay safe.
//
// The Run cache claim is opened in its own outer transaction; the inner
// service call still opens its own balance transaction. Nested gorm
// transactions become savepoints, so a failure inside the business logic
// rolls back both the balance change AND the cache claim, leaving retries
// free to re-execute fresh.
func (h *AccountGRPCHandler) UpdateBalance(ctx context.Context, req *pb.UpdateBalanceRequest) (*pb.AccountResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		// Defensive: the constructor always wires both, but tests that
		// build the handler with the older signature would skip them.
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}

	var resp *pb.AccountResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.AccountResponse { return &pb.AccountResponse{} },
			func() (*pb.AccountResponse, error) {
				return h.executeUpdateBalance(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

// executeUpdateBalance is the original UpdateBalance body — extracted so
// the IdempotencyRepository.Run wrapper above can call it as the cached
// fn. It opens its own balance transaction inside the service layer.
func (h *AccountGRPCHandler) executeUpdateBalance(ctx context.Context, req *pb.UpdateBalanceRequest) (*pb.AccountResponse, error) {
	_ = ctx
	amount, _ := decimal.NewFromString(req.Amount)
	opts := repository.UpdateBalanceOpts{
		Memo:           req.GetMemo(),
		IdempotencyKey: req.GetIdempotencyKey(),
	}
	if err := h.accountService.UpdateBalanceWithOpts(req.AccountNumber, amount, req.UpdateAvailable, opts); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		return nil, err
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
		return nil, err
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) GetCompany(ctx context.Context, req *pb.GetCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, err
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) UpdateCompany(ctx context.Context, req *pb.UpdateCompanyRequest) (*pb.CompanyResponse, error) {
	company, err := h.companyService.Get(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "company not found")
		}
		return nil, err
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
		return nil, err
	}
	return toCompanyResponse(company), nil
}

func (h *AccountGRPCHandler) ListCurrencies(ctx context.Context, req *pb.ListCurrenciesRequest) (*pb.ListCurrenciesResponse, error) {
	currencies, err := h.currencyService.List()
	if err != nil {
		return nil, err
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
		return nil, err
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
		return nil, err
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
		ReservedBalance:  a.ReservedBalance.StringFixed(4),
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
