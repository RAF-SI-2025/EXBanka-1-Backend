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
)

// ReservationService owns the reserve / release / partial-settle lifecycle of
// funds held on behalf of a client order. Semantics:
//
//   - Reserve: moves `amount` out of AvailableBalance into ReservedBalance.
//     Balance is unchanged (the money is still "on the account", just not
//     spendable via other channels).
//   - PartialSettle: commits a partial fill. Decrements ReservedBalance AND
//     Balance by the settled amount. AvailableBalance is not touched (it
//     was already decremented at reserve time). Writes a debit ledger entry
//     so fills appear in the account's transaction history.
//   - Release: returns the remaining (unsettled) hold back to AvailableBalance.
//     Decrements ReservedBalance, increments AvailableBalance; Balance
//     unchanged.
//
// Invariant maintained at all times: AvailableBalance = Balance - ReservedBalance.
//
// All operations run inside a single DB transaction with SELECT FOR UPDATE on
// the account row. Reserve is idempotent on order_id. PartialSettle is
// idempotent on order_transaction_id.
type ReservationService struct {
	db          *gorm.DB
	accountRepo *repository.AccountRepository
	resRepo     *repository.AccountReservationRepository
	ledgerRepo  *repository.LedgerRepository
}

func NewReservationService(
	db *gorm.DB,
	accountRepo *repository.AccountRepository,
	resRepo *repository.AccountReservationRepository,
	ledgerRepo *repository.LedgerRepository,
) *ReservationService {
	return &ReservationService{db: db, accountRepo: accountRepo, resRepo: resRepo, ledgerRepo: ledgerRepo}
}

// ReserveFundsResult is returned by ReserveFunds.
type ReserveFundsResult struct {
	ReservationID    uint64
	ReservedBalance  decimal.Decimal
	AvailableBalance decimal.Decimal
}

// ReleaseResult is returned by ReleaseReservation.
type ReleaseResult struct {
	ReleasedAmount  decimal.Decimal
	ReservedBalance decimal.Decimal
}

// PartialSettleResult is returned by PartialSettleReservation.
type PartialSettleResult struct {
	SettledAmount     decimal.Decimal
	RemainingReserved decimal.Decimal
	BalanceAfter      decimal.Decimal
	LedgerEntryID     uint64
}

// ReserveFunds holds `amount` on the account for `orderID`. Idempotent on
// orderID: retries with the same orderID return the existing reservation
// without double-counting. Returns codes.FailedPrecondition when available
// balance is insufficient or the currency does not match the account.
// Returns codes.NotFound if the account does not exist.
func (s *ReservationService) ReserveFunds(ctx context.Context, orderID, accountID uint64, amount decimal.Decimal, currencyCode string) (*ReserveFundsResult, error) {
	if amount.Sign() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	var out *ReserveFundsResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "account not found")
			}
			return err
		}
		if acc.CurrencyCode != currencyCode {
			return status.Errorf(codes.FailedPrecondition, "currency mismatch: account=%s reserve=%s", acc.CurrencyCode, currencyCode)
		}
		if acc.AvailableBalance.LessThan(amount) {
			return status.Errorf(codes.FailedPrecondition, "insufficient available balance: have %s, need %s", acc.AvailableBalance, amount)
		}
		res := &model.AccountReservation{
			AccountID:    acc.ID,
			OrderID:      orderID,
			Amount:       amount,
			CurrencyCode: currencyCode,
			Status:       model.ReservationStatusActive,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			// Idempotent replay — return current state without mutating.
			out = &ReserveFundsResult{
				ReservationID:    existing.ID,
				ReservedBalance:  acc.ReservedBalance,
				AvailableBalance: acc.AvailableBalance,
			}
			return nil
		}
		acc.ReservedBalance = acc.ReservedBalance.Add(amount)
		acc.AvailableBalance = acc.AvailableBalance.Sub(amount)
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}
		out = &ReserveFundsResult{
			ReservationID:    res.ID,
			ReservedBalance:  acc.ReservedBalance,
			AvailableBalance: acc.AvailableBalance,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseReservation marks an active reservation as released and returns the
// remaining (amount - settled) held funds back to AvailableBalance. No-op if
// the reservation is missing, already released, or already settled — returning
// ReleasedAmount=0 in that case.
func (s *ReservationService) ReleaseReservation(ctx context.Context, orderID uint64) (*ReleaseResult, error) {
	var out *ReleaseResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				out = &ReleaseResult{ReleasedAmount: decimal.Zero, ReservedBalance: decimal.Zero}
				return nil
			}
			return err
		}
		if res.Status != model.ReservationStatusActive {
			out = &ReleaseResult{ReleasedAmount: decimal.Zero, ReservedBalance: decimal.Zero}
			return nil
		}

		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		remaining := res.Amount.Sub(settled)
		if remaining.Sign() < 0 {
			remaining = decimal.Zero
		}

		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, res.AccountID).Error; err != nil {
			return err
		}
		acc.ReservedBalance = acc.ReservedBalance.Sub(remaining)
		if acc.ReservedBalance.Sign() < 0 {
			acc.ReservedBalance = decimal.Zero
		}
		acc.AvailableBalance = acc.AvailableBalance.Add(remaining)
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}

		res.Status = model.ReservationStatusReleased
		res.UpdatedAt = time.Now()
		if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
			return err
		}

		out = &ReleaseResult{ReleasedAmount: remaining, ReservedBalance: acc.ReservedBalance}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PartialSettleReservation commits a partial fill: decrements both
// ReservedBalance and Balance by `amount`. AvailableBalance is unchanged
// (the money was already removed from "available" at reserve time).
// Idempotent on orderTransactionID via ON CONFLICT DO NOTHING on the
// settlements table. Writes a debit ledger entry so the settlement appears
// in the account's transaction history the same way a regular debit would.
// Returns codes.FailedPrecondition if the reservation is missing, inactive,
// or if the settlement would exceed the reserved amount.
func (s *ReservationService) PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*PartialSettleResult, error) {
	if amount.Sign() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	var out *PartialSettleResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "reservation not found")
			}
			return err
		}
		if res.Status != model.ReservationStatusActive {
			return status.Errorf(codes.FailedPrecondition, "reservation status=%s", res.Status)
		}

		var acc model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, res.AccountID).Error; err != nil {
			return err
		}

		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		if settled.Add(amount).GreaterThan(res.Amount) {
			return status.Errorf(codes.FailedPrecondition, "settlement %s would exceed reservation %s", settled.Add(amount), res.Amount)
		}

		settlement := &model.AccountReservationSettlement{
			ReservationID:      res.ID,
			OrderTransactionID: orderTransactionID,
			Amount:             amount,
			CreatedAt:          time.Now(),
		}
		// ON CONFLICT(order_transaction_id) DO NOTHING — idempotency guard.
		createResult := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "order_transaction_id"}},
			DoNothing: true,
		}).Create(settlement)
		if createResult.Error != nil {
			return createResult.Error
		}
		if createResult.RowsAffected == 0 {
			// Replay of a settlement that already landed. Return current
			// account state without mutating.
			out = &PartialSettleResult{
				SettledAmount:     amount,
				RemainingReserved: acc.ReservedBalance,
				BalanceAfter:      acc.Balance,
				LedgerEntryID:     0,
			}
			return nil
		}

		// First-time settle: move money + write ledger entry.
		before := acc.Balance
		acc.ReservedBalance = acc.ReservedBalance.Sub(amount)
		if acc.ReservedBalance.Sign() < 0 {
			acc.ReservedBalance = decimal.Zero
		}
		acc.Balance = acc.Balance.Sub(amount)
		if err := tx.Save(&acc).Error; err != nil {
			return err
		}

		// Write a ledger entry matching the shape produced by DebitWithLock
		// so the fill appears in /api/v1/me/accounts/{id}/transactions.
		entry := &model.LedgerEntry{
			AccountNumber: acc.AccountNumber,
			EntryType:     "debit",
			Amount:        amount,
			BalanceBefore: before,
			BalanceAfter:  acc.Balance,
			Description:   memo,
			ReferenceID:   fmt.Sprintf("order-%d-txn-%d", orderID, orderTransactionID),
			ReferenceType: "reservation_settlement",
		}
		if err := tx.Create(entry).Error; err != nil {
			return fmt.Errorf("ledger entry: %w", err)
		}

		// Fully filled? Transition reservation status to settled.
		if settled.Add(amount).Equal(res.Amount) {
			res.Status = model.ReservationStatusSettled
			res.UpdatedAt = time.Now()
			if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
				return err
			}
		}

		out = &PartialSettleResult{
			SettledAmount:     amount,
			RemainingReserved: acc.ReservedBalance,
			BalanceAfter:      acc.Balance,
			LedgerEntryID:     entry.ID,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetReservation is read-only; used by stock-service to reconcile after a
// crash. Returns (status, amount, settled_total, settled_txn_ids, exists, err).
// When the reservation does not exist, returns exists=false and no error.
func (s *ReservationService) GetReservation(ctx context.Context, orderID uint64) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error) {
	res, err := s.resRepo.GetByOrderID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", decimal.Zero, decimal.Zero, nil, false, nil
		}
		return "", decimal.Zero, decimal.Zero, nil, false, err
	}
	children, err := s.resRepo.ListSettlements(res.ID)
	if err != nil {
		return "", decimal.Zero, decimal.Zero, nil, false, err
	}
	total := decimal.Zero
	ids := make([]uint64, len(children))
	for i, c := range children {
		total = total.Add(c.Amount)
		ids[i] = c.OrderTransactionID
	}
	return res.Status, res.Amount, total, ids, true, nil
}
