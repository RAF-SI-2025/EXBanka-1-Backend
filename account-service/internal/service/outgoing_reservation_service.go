package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	shared "github.com/exbanka/contract/shared"
)

// OutgoingReservationService owns the debit-side reservation lifecycle for
// cross-bank (SI-TX) money DEBIT legs — the spec's reserve-then-settle shape
// (Celina-5 OTC SAGA step 1 / transfer §2), debit-side mirror of
// IncomingReservationService:
//
//   - ReserveOutgoing: AvailableBalance -= amount (Balance unchanged); pending
//     row. The HOLD: money can't be spent elsewhere but hasn't left yet.
//     Idempotent on reservation_key.
//   - SettleOutgoing: the commit — Balance -= amount (Available already
//     reduced), writes a debit ledger entry, marks settled. Idempotent.
//   - ReleaseOutgoing: NO vote / rollback / timeout — AvailableBalance +=
//     amount (Balance untouched), marks released. No ledger entry. Idempotent.
//
// Net effect: reserve→settle = a clean debit; reserve→release = a no-op on the
// customer's Balance, with only AvailableBalance dipping during the in-flight
// window.
type OutgoingReservationService struct {
	db          *gorm.DB
	accountRepo *repository.AccountRepository
	resRepo     *repository.OutgoingReservationRepository
}

func NewOutgoingReservationService(
	db *gorm.DB,
	accountRepo *repository.AccountRepository,
	resRepo *repository.OutgoingReservationRepository,
) *OutgoingReservationService {
	return &OutgoingReservationService{db: db, accountRepo: accountRepo, resRepo: resRepo}
}

// ReserveOutgoing places a hold: reduces AvailableBalance (not Balance) under a
// FOR UPDATE lock after verifying sufficiency, and writes a pending row.
// Idempotent on key. Returns FailedPrecondition for insufficient available
// balance / inactive account / currency mismatch (caller maps to a NO vote),
// NotFound if the account is missing.
func (s *OutgoingReservationService) ReserveOutgoing(
	ctx context.Context,
	accountNumber string,
	amount decimal.Decimal,
	currency, key string,
) (*model.OutgoingReservation, error) {
	if !amount.IsPositive() {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	if existing, err := s.resRepo.GetByKey(key); err == nil {
		return existing, nil // idempotent replay
	}

	var out *model.OutgoingReservation
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var acct model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_number = ?", accountNumber).First(&acct).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "account_not_found")
			}
			return err
		}
		if acct.Status != "active" {
			return status.Error(codes.FailedPrecondition, "account_inactive")
		}
		if acct.CurrencyCode != currency {
			return status.Errorf(codes.FailedPrecondition,
				"currency_not_supported: account %s is %s, requested %s",
				accountNumber, acct.CurrencyCode, currency)
		}
		if acct.AvailableBalance.LessThan(amount) {
			return status.Errorf(codes.FailedPrecondition,
				"insufficient available balance: have %s, need %s", acct.AvailableBalance, amount)
		}
		acct.AvailableBalance = acct.AvailableBalance.Sub(amount)
		saveRes := tx.Save(&acct)
		if saveRes.Error != nil {
			return saveRes.Error
		}
		if saveRes.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		res := &model.OutgoingReservation{
			AccountNumber:  accountNumber,
			Amount:         amount,
			Currency:       currency,
			ReservationKey: key,
			Status:         model.OutgoingReservationStatusPending,
		}
		if err := s.resRepo.WithTx(tx).Create(res); err != nil {
			return err
		}
		out = res
		return nil
	})
	if err != nil {
		// Race: a concurrent caller inserted the same key. Re-fetch for idempotency.
		if existing, getErr := s.resRepo.GetByKey(key); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return out, nil
}

// SettleOutgoing finalizes a pending reservation: debits Balance (Available was
// already reduced at reserve), writes a debit ledger entry, marks settled.
// Idempotent on key — a second call after settle returns nil.
func (s *OutgoingReservationService) SettleOutgoing(ctx context.Context, key string) (*model.Account, error) {
	res, err := s.resRepo.GetByKey(key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "outgoing_reservation not found")
		}
		return nil, err
	}
	if res.Status == model.OutgoingReservationStatusSettled {
		return s.accountRepo.GetByNumber(res.AccountNumber)
	}
	if res.Status != model.OutgoingReservationStatusPending {
		return nil, status.Errorf(codes.FailedPrecondition, "outgoing_reservation already %s", res.Status)
	}

	var outAcct *model.Account
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var acct model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_number = ?", res.AccountNumber).First(&acct).Error; err != nil {
			return err
		}
		before := acct.Balance
		// AvailableBalance was already reduced at reserve; settle only moves
		// Balance down to match (the money now actually leaves).
		acct.Balance = acct.Balance.Sub(res.Amount)
		if !acct.IsBankAccount {
			acct.DailySpending = acct.DailySpending.Add(res.Amount)
			acct.MonthlySpending = acct.MonthlySpending.Add(res.Amount)
		}
		saveRes := tx.Save(&acct)
		if saveRes.Error != nil {
			return saveRes.Error
		}
		if saveRes.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		entry := &model.LedgerEntry{
			AccountNumber:  res.AccountNumber,
			EntryType:      "debit",
			Amount:         res.Amount,
			BalanceBefore:  before,
			BalanceAfter:   acct.Balance,
			Description:    fmt.Sprintf("Inter-bank debit (tx=%s)", res.ReservationKey),
			ReferenceID:    res.ReservationKey,
			ReferenceType:  "interbank_debit",
			IdempotencyKey: "outgoing-" + res.ReservationKey,
		}
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		if err := s.resRepo.WithTx(tx).MarkSettled(tx, res.ReservationKey); err != nil {
			return err
		}
		outAcct = &acct
		return nil
	})
	return outAcct, err
}

// ReleaseOutgoing returns a pending hold to AvailableBalance (Balance never
// changed) and marks the row released. Idempotent: no-op on non-pending rows.
// Used on NO vote / ROLLBACK_TX / timeout.
func (s *OutgoingReservationService) ReleaseOutgoing(ctx context.Context, key string) error {
	res, err := s.resRepo.GetByKey(key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Error(codes.NotFound, "outgoing_reservation not found")
		}
		return err
	}
	if res.Status != model.OutgoingReservationStatusPending {
		return nil // already settled/released — idempotent
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var acct model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_number = ?", res.AccountNumber).First(&acct).Error; err != nil {
			return err
		}
		acct.AvailableBalance = acct.AvailableBalance.Add(res.Amount)
		saveRes := tx.Save(&acct)
		if saveRes.Error != nil {
			return saveRes.Error
		}
		if saveRes.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		return s.resRepo.WithTx(tx).MarkReleased(tx, res.ReservationKey)
	})
}

// ListStalePending exposes the repo sweep for the timeout cron.
func (s *OutgoingReservationService) ListStalePending(before time.Time, limit int) ([]model.OutgoingReservation, error) {
	return s.resRepo.ListStalePendingOlderThan(before, limit)
}
