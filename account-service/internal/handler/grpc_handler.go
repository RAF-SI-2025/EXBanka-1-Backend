package handler

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/identity"
)

// ownsAccount reports whether the gRPC caller (from identity metadata) may access
// an account owned by ownerID. OWN-1: client → own only; employee-on-behalf →
// bound client; employee/admin + trusted service → allowed. A false result is
// mapped by callers to ErrAccountNotFound (don't leak existence across tenants).
func ownsAccount(ctx context.Context, ownerID uint64) bool {
	return identity.FromIncoming(ctx).OwnsResource(int64(ownerID))
}

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

type AccountGRPCHandler struct {
	pb.UnimplementedAccountServiceServer
	accountService      accountSvcFacade
	companyService      companySvcFacade
	currencyService     currencySvcFacade
	ledgerService       ledgerSvcFacade
	reservation         *ReservationHandler
	incomingReservation *service.IncomingReservationService
	outgoingReservation *service.OutgoingReservationService
	changelogService    *service.ChangelogService
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
	outgoingReservation *service.OutgoingReservationService,
	db *gorm.DB,
	idem *repository.IdempotencyRepository,
	changelogService *service.ChangelogService,
) *AccountGRPCHandler {
	return &AccountGRPCHandler{
		accountService:      accountService,
		companyService:      companyService,
		currencyService:     currencyService,
		ledgerService:       ledgerService,
		reservation:         reservation,
		incomingReservation: incomingReservation,
		outgoingReservation: outgoingReservation,
		changelogService:    changelogService,
		db:                  db,
		idem:                idem,
	}
}

// ReserveFunds wraps the reservation handler in the saga-step idempotency
// contract. See UpdateBalance for a full explanation of the two-layer
// dedup strategy.
func (h *AccountGRPCHandler) ReserveFunds(ctx context.Context, req *pb.ReserveFundsRequest) (*pb.ReserveFundsResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReserveFundsResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReserveFundsResponse { return &pb.ReserveFundsResponse{} },
			func() (*pb.ReserveFundsResponse, error) {
				return h.reservation.ReserveFunds(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

// ReleaseReservation wraps the reservation handler in the saga-step
// idempotency contract.
func (h *AccountGRPCHandler) ReleaseReservation(ctx context.Context, req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReleaseReservationResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReleaseReservationResponse { return &pb.ReleaseReservationResponse{} },
			func() (*pb.ReleaseReservationResponse, error) {
				return h.reservation.ReleaseReservation(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

// PartialSettleReservation wraps the reservation handler in the saga-step
// idempotency contract. NOTE: order_transaction_id remains the authoritative
// domain dedup key inside the service; idempotency_key here is the response
// cache for retried saga steps.
func (h *AccountGRPCHandler) PartialSettleReservation(ctx context.Context, req *pb.PartialSettleReservationRequest) (*pb.PartialSettleReservationResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.PartialSettleReservationResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.PartialSettleReservationResponse { return &pb.PartialSettleReservationResponse{} },
			func() (*pb.PartialSettleReservationResponse, error) {
				return h.reservation.PartialSettleReservation(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

// GetReservation forwards to the reservation handler. Read-only — no
// idempotency wrap needed.
func (h *AccountGRPCHandler) GetReservation(ctx context.Context, req *pb.GetReservationRequest) (*pb.GetReservationResponse, error) {
	return h.reservation.GetReservation(ctx, req)
}

// ReserveIncoming creates a pending credit reservation for an inter-bank
// inbound transfer. Does not change the account balance. Wrapped in the
// saga-step idempotency contract; reservation_key remains the authoritative
// domain dedup key on the service side.
func (h *AccountGRPCHandler) ReserveIncoming(ctx context.Context, req *pb.ReserveIncomingRequest) (*pb.ReserveIncomingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReserveIncomingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReserveIncomingResponse { return &pb.ReserveIncomingResponse{} },
			func() (*pb.ReserveIncomingResponse, error) {
				return h.executeReserveIncoming(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeReserveIncoming(ctx context.Context, req *pb.ReserveIncomingRequest) (*pb.ReserveIncomingResponse, error) {
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

// CommitIncoming finalizes the credit and writes a ledger entry. Wrapped
// in the saga-step idempotency contract.
func (h *AccountGRPCHandler) CommitIncoming(ctx context.Context, req *pb.CommitIncomingRequest) (*pb.CommitIncomingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.CommitIncomingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.CommitIncomingResponse { return &pb.CommitIncomingResponse{} },
			func() (*pb.CommitIncomingResponse, error) {
				return h.executeCommitIncoming(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeCommitIncoming(ctx context.Context, req *pb.CommitIncomingRequest) (*pb.CommitIncomingResponse, error) {
	acct, err := h.incomingReservation.CommitIncoming(ctx, req.ReservationKey, req.GetMemo())
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	return &pb.CommitIncomingResponse{BalanceAfter: acct.Balance.StringFixed(4)}, nil
}

// ReleaseIncoming cancels a pending credit reservation. Wrapped in the
// saga-step idempotency contract.
func (h *AccountGRPCHandler) ReleaseIncoming(ctx context.Context, req *pb.ReleaseIncomingRequest) (*pb.ReleaseIncomingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReleaseIncomingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReleaseIncomingResponse { return &pb.ReleaseIncomingResponse{} },
			func() (*pb.ReleaseIncomingResponse, error) {
				return h.executeReleaseIncoming(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeReleaseIncoming(ctx context.Context, req *pb.ReleaseIncomingRequest) (*pb.ReleaseIncomingResponse, error) {
	if err := h.incomingReservation.ReleaseIncoming(ctx, req.ReservationKey); err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	return &pb.ReleaseIncomingResponse{Released: true}, nil
}

// ReserveOutgoing places a debit-side hold for a cross-bank money DEBIT leg:
// reduces AvailableBalance (not Balance) and writes a pending row. Wrapped in
// the saga-step idempotency contract; the service is also idempotent on
// reservation_key.
func (h *AccountGRPCHandler) ReserveOutgoing(ctx context.Context, req *pb.ReserveOutgoingRequest) (*pb.ReserveOutgoingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReserveOutgoingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReserveOutgoingResponse { return &pb.ReserveOutgoingResponse{} },
			func() (*pb.ReserveOutgoingResponse, error) {
				return h.executeReserveOutgoing(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeReserveOutgoing(ctx context.Context, req *pb.ReserveOutgoingRequest) (*pb.ReserveOutgoingResponse, error) {
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	res, err := h.outgoingReservation.ReserveOutgoing(ctx, req.AccountNumber, amt, req.Currency, req.ReservationKey)
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	acct, _ := h.accountService.GetAccountByNumber(res.AccountNumber)
	availableAfter := ""
	if acct != nil {
		availableAfter = acct.AvailableBalance.StringFixed(4)
	}
	return &pb.ReserveOutgoingResponse{ReservationKey: res.ReservationKey, AvailableAfter: availableAfter}, nil
}

// SettleOutgoing finalizes a pending debit hold: Balance -= amount, writes the
// debit ledger entry, marks settled. Wrapped in the saga-step idempotency
// contract.
func (h *AccountGRPCHandler) SettleOutgoing(ctx context.Context, req *pb.SettleOutgoingRequest) (*pb.SettleOutgoingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.SettleOutgoingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.SettleOutgoingResponse { return &pb.SettleOutgoingResponse{} },
			func() (*pb.SettleOutgoingResponse, error) {
				return h.executeSettleOutgoing(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeSettleOutgoing(ctx context.Context, req *pb.SettleOutgoingRequest) (*pb.SettleOutgoingResponse, error) {
	acct, err := h.outgoingReservation.SettleOutgoing(ctx, req.ReservationKey)
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	balanceAfter := ""
	if acct != nil {
		balanceAfter = acct.Balance.StringFixed(4)
	}
	return &pb.SettleOutgoingResponse{BalanceAfter: balanceAfter}, nil
}

// ReleaseOutgoing cancels a pending debit hold (NO vote / ROLLBACK_TX /
// timeout): AvailableBalance += amount, marks released. Wrapped in the
// saga-step idempotency contract.
func (h *AccountGRPCHandler) ReleaseOutgoing(ctx context.Context, req *pb.ReleaseOutgoingRequest) (*pb.ReleaseOutgoingResponse, error) {
	if req.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *pb.ReleaseOutgoingResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, req.GetIdempotencyKey(),
			func() *pb.ReleaseOutgoingResponse { return &pb.ReleaseOutgoingResponse{} },
			func() (*pb.ReleaseOutgoingResponse, error) {
				return h.executeReleaseOutgoing(ctx, req)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *AccountGRPCHandler) executeReleaseOutgoing(ctx context.Context, req *pb.ReleaseOutgoingRequest) (*pb.ReleaseOutgoingResponse, error) {
	if err := h.outgoingReservation.ReleaseOutgoing(ctx, req.ReservationKey); err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, err
	}
	return &pb.ReleaseOutgoingResponse{Released: true}, nil
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
	// Events (account-created + in-app notification + welcome email) are published
	// by the service layer (see AccountService.emitAccountCreated).
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
	// OWN-1: a client may only read its own account (others → 404, no leak).
	if !ownsAccount(ctx, account.OwnerID) {
		return nil, service.ErrAccountNotFound
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
	if !ownsAccount(ctx, account.OwnerID) {
		return nil, service.ErrAccountNotFound
	}
	return toAccountResponse(account), nil
}

func (h *AccountGRPCHandler) ListAccountsByClient(ctx context.Context, req *pb.ListAccountsByClientRequest) (*pb.ListAccountsResponse, error) {
	// OWN-1: a client may only list its own accounts.
	if !ownsAccount(ctx, req.ClientId) {
		return nil, service.ErrForbidden
	}
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
	// Domain event + notification published by the service layer.
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
	// Domain event + notification published by the service layer.
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
	// Domain event + notification published by the service layer.
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

// ListChangelog returns paginated audit-log entries for an entity.
func (h *AccountGRPCHandler) ListChangelog(ctx context.Context, req *pb.ListChangelogRequest) (*pb.ListChangelogResponse, error) {
	entries, total, err := h.changelogService.ListChangelog(req.GetEntityType(), req.GetEntityId(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	protoEntries := make([]*pb.ChangelogEntry, len(entries))
	for i, e := range entries {
		protoEntries[i] = &pb.ChangelogEntry{
			Id:         e.ID,
			EntityType: e.EntityType,
			EntityId:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt.Unix(),
			Reason:     e.Reason,
		}
	}
	return &pb.ListChangelogResponse{Entries: protoEntries, Total: total}, nil
}

// ListAllChangelogs returns paginated audit-log entries across all entities
// (global view, admin-only).
func (h *AccountGRPCHandler) ListAllChangelogs(ctx context.Context, req *pb.ListAllChangelogsRequest) (*pb.ListAllChangelogsResponse, error) {
	page := int(req.GetPage())
	pageSize := int(req.GetPageSize())
	filters := repository.ChangelogFilters{
		Since:   req.GetSince(),
		Until:   req.GetUntil(),
		ActorID: req.GetActorId(),
		Action:  req.GetAction(),
	}
	entries, total, err := h.changelogService.ListAllChangelogs(filters, page, pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	protoEntries := make([]*pb.ChangelogEntry, len(entries))
	for i, e := range entries {
		protoEntries[i] = &pb.ChangelogEntry{
			Id:         e.ID,
			EntityType: e.EntityType,
			EntityId:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt.Unix(),
			Reason:     e.Reason,
		}
	}
	return &pb.ListAllChangelogsResponse{
		Entries:  protoEntries,
		Total:    total,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}, nil
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

	// OWN-1: a client may only read the ledger of an account it owns. Resolve the
	// account's owner first (cheap indexed lookup) and gate on it.
	acct, err := h.accountService.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrAccountNotFound
		}
		return nil, err
	}
	if !ownsAccount(ctx, acct.OwnerID) {
		return nil, service.ErrAccountNotFound
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
