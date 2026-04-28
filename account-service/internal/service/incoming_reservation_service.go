package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	shared "github.com/exbanka/contract/shared"
)

// IncomingReservationService owns the credit-side reservation lifecycle for
// inter-bank inbound transfers. Lifecycle:
//
//   - ReserveIncoming: writes a pending row but does NOT touch the account
//     balance. Idempotent on reservation_key.
//   - CommitIncoming: locks the destination account, adds amount to Balance
//     and AvailableBalance, writes a credit ledger entry, and marks the
//     reservation committed. Idempotent: a second call after a successful
//     commit returns the current account state without re-applying.
//   - ReleaseIncoming: marks pending as released. No balance impact.
//
// The deferred-credit semantics (credit only on Commit) are what guarantee
// the receiver's Commit can never fail for "balance reasons" — the credit
// only adds, it never subtracts.
type IncomingReservationService struct {
	db          *gorm.DB
	accountRepo *repository.AccountRepository
	resRepo     *repository.IncomingReservationRepository
}

func NewIncomingReservationService(
	db *gorm.DB,
	accountRepo *repository.AccountRepository,
	resRepo *repository.IncomingReservationRepository,
) *IncomingReservationService {
	return &IncomingReservationService{db: db, accountRepo: accountRepo, resRepo: resRepo}
}

// ReserveIncoming creates a pending credit reservation. Idempotent on
// reservation_key: a second call with the same key returns the existing row
// without inserting a duplicate. Returns codes.NotFound if the destination
// account does not exist, codes.FailedPrecondition if the account is
// inactive or its currency does not match.
func (s *IncomingReservationService) ReserveIncoming(
	ctx context.Context,
	accountNumber string,
	amount decimal.Decimal,
	currency, key string,
) (*model.IncomingReservation, error) {
	if !amount.IsPositive() {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	if existing, err := s.resRepo.GetByKey(key); err == nil {
		return existing, nil
	}

	acct, err := s.accountRepo.GetByNumber(accountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "account_not_found")
		}
		return nil, err
	}
	if acct.Status != "active" {
		return nil, status.Error(codes.FailedPrecondition, "account_inactive")
	}
	if acct.CurrencyCode != currency {
		return nil, status.Errorf(codes.FailedPrecondition,
			"currency_not_supported: account %s is %s, requested %s",
			accountNumber, acct.CurrencyCode, currency)
	}

	res := &model.IncomingReservation{
		AccountNumber:  accountNumber,
		Amount:         amount,
		Currency:       currency,
		ReservationKey: key,
		Status:         model.IncomingReservationStatusPending,
	}
	if err := s.resRepo.Create(res); err != nil {
		// Race: another caller inserted the same key between our GetByKey
		// and Create. Re-fetch and return the now-existing row to keep the
		// API idempotent.
		if existing, getErr := s.resRepo.GetByKey(key); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return res, nil
}

// CommitIncoming finalizes a pending reservation by crediting the account
// and writing a ledger entry. Idempotent on reservation_key.
func (s *IncomingReservationService) CommitIncoming(ctx context.Context, key string) (*model.Account, error) {
	res, err := s.resRepo.GetByKey(key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "incoming_reservation not found")
		}
		return nil, err
	}
	if res.Status == model.IncomingReservationStatusCommitted {
		return s.accountRepo.GetByNumber(res.AccountNumber)
	}
	if res.Status != model.IncomingReservationStatusPending {
		return nil, status.Errorf(codes.FailedPrecondition, "incoming_reservation already %s", res.Status)
	}

	var out *model.Account
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var acct model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_number = ?", res.AccountNumber).
			First(&acct).Error; err != nil {
			return err
		}
		before := acct.Balance
		acct.Balance = acct.Balance.Add(res.Amount)
		acct.AvailableBalance = acct.AvailableBalance.Add(res.Amount)
		// Account has a Version field + BeforeUpdate hook that adds
		// WHERE version=?. Without checking RowsAffected, an optimistic
		// lock conflict here would silently lose the credit while still
		// marking the reservation committed below — exactly the
		// TestInterBank_IncomingSuccess failure mode.
		saveRes := tx.Save(&acct)
		if saveRes.Error != nil {
			return saveRes.Error
		}
		if saveRes.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		entry := &model.LedgerEntry{
			AccountNumber:  res.AccountNumber,
			EntryType:      "credit",
			Amount:         res.Amount,
			BalanceBefore:  before,
			BalanceAfter:   acct.Balance,
			Description:    fmt.Sprintf("Inter-bank credit (tx=%s)", res.ReservationKey),
			ReferenceID:    res.ReservationKey,
			ReferenceType:  "interbank_credit",
			IdempotencyKey: "incoming-" + res.ReservationKey,
		}
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		// Mark committed in the same transaction so a crash between the
		// ledger write and the status update can be replayed by re-running
		// CommitIncoming (idempotency_key on ledger_entries dedupes).
		upd := tx.Model(&model.IncomingReservation{}).
			Where("reservation_key = ? AND status = ?", res.ReservationKey, model.IncomingReservationStatusPending).
			Updates(map[string]any{"status": model.IncomingReservationStatusCommitted})
		if upd.Error != nil {
			return upd.Error
		}
		out = &acct
		return nil
	})
	return out, err
}

// ReleaseIncoming marks a pending reservation as released. No balance impact.
// Idempotent on reservation_key (no-op on non-pending rows).
func (s *IncomingReservationService) ReleaseIncoming(ctx context.Context, key string) error {
	res, err := s.resRepo.GetByKey(key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Error(codes.NotFound, "incoming_reservation not found")
		}
		return err
	}
	if res.Status != model.IncomingReservationStatusPending {
		return nil
	}
	return s.resRepo.MarkReleased(key)
}
