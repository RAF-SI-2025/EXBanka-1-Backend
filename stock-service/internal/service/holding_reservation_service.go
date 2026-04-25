package service

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// HoldingReservationService owns the reserve / release / partial-settle
// lifecycle of share quantities held on behalf of a sell-side client order.
// It is the quantity-based mirror of account-service's ReservationService.
//
// Semantics:
//
//   - Reserve: moves `qty` out of AvailableQuantity into ReservedQuantity.
//     Holding.Quantity is unchanged (the shares are still in the holding, just
//     not usable for other orders). Idempotent on order_id.
//   - PartialSettle: commits a partial fill. Decrements ReservedQuantity AND
//     Quantity by the settled qty — the shares physically leave the holding.
//     Idempotent on order_transaction_id.
//   - Release: returns the unsettled remainder of a reservation to
//     AvailableQuantity. Decrements ReservedQuantity; Quantity is unchanged.
//     No-op if the reservation is missing, released, or already settled.
//
// Invariant maintained at all times: AvailableQuantity = Quantity - ReservedQuantity.
//
// All operations run inside a db.Transaction with SELECT FOR UPDATE on the
// Holding row.
type HoldingReservationService struct {
	db          *gorm.DB
	holdingRepo *repository.HoldingRepository
	resRepo     *repository.HoldingReservationRepository
}

func NewHoldingReservationService(
	db *gorm.DB,
	holdingRepo *repository.HoldingRepository,
	resRepo *repository.HoldingReservationRepository,
) *HoldingReservationService {
	return &HoldingReservationService{db: db, holdingRepo: holdingRepo, resRepo: resRepo}
}

// ReserveHoldingResult is returned by Reserve.
type ReserveHoldingResult struct {
	ReservationID     uint64
	ReservedQuantity  int64
	AvailableQuantity int64
}

// ReleaseHoldingResult is returned by Release.
type ReleaseHoldingResult struct {
	ReleasedQuantity int64
	ReservedQuantity int64
}

// PartialSettleHoldingResult is returned by PartialSettle.
type PartialSettleHoldingResult struct {
	SettledQuantity   int64
	RemainingReserved int64
	QuantityAfter     int64
}

// Reserve locks `qty` shares of the given holding for `orderID`. Idempotent on
// orderID: retries with the same orderID return the existing reservation
// without double-counting. Returns codes.FailedPrecondition when the holding
// does not exist or available quantity is insufficient.
//
// Post-rollup (Part A): holdings aggregate per (user_id, system_type,
// security_type, security_id). The lookup no longer filters by account_id;
// proceeds on a sell fill are credited to the order's AccountID independently.
func (s *HoldingReservationService) Reserve(
	ctx context.Context,
	userID uint64,
	systemType, securityType string,
	securityID, orderID uint64,
	qty int64,
) (*ReserveHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	var out *ReserveHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var holding model.Holding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND system_type = ? AND security_type = ? AND security_id = ?",
				userID, systemType, securityType, securityID).
			First(&holding).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "holding not found")
			}
			return err
		}
		available := holding.Quantity - holding.ReservedQuantity
		if available < qty {
			return status.Errorf(codes.FailedPrecondition,
				"insufficient available quantity: have %d, need %d", available, qty)
		}
		oid := orderID
		res := &model.HoldingReservation{
			HoldingID: holding.ID,
			OrderID:   &oid,
			Quantity:  qty,
			Status:    model.HoldingReservationStatusActive,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			// Idempotent replay — return current state without mutating.
			out = &ReserveHoldingResult{
				ReservationID:     existing.ID,
				ReservedQuantity:  holding.ReservedQuantity,
				AvailableQuantity: holding.Quantity - holding.ReservedQuantity,
			}
			return nil
		}
		holding.ReservedQuantity += qty
		if err := tx.Save(&holding).Error; err != nil {
			return err
		}
		out = &ReserveHoldingResult{
			ReservationID:     res.ID,
			ReservedQuantity:  holding.ReservedQuantity,
			AvailableQuantity: holding.Quantity - holding.ReservedQuantity,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Release marks an active reservation as released and returns the remaining
// (quantity - settled) held shares back to AvailableQuantity. No-op if the
// reservation is missing, already released, or already settled — returning
// ReleasedQuantity=0 in that case.
func (s *HoldingReservationService) Release(ctx context.Context, orderID uint64) (*ReleaseHoldingResult, error) {
	var out *ReleaseHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				out = &ReleaseHoldingResult{ReleasedQuantity: 0, ReservedQuantity: 0}
				return nil
			}
			return err
		}
		if res.Status != model.HoldingReservationStatusActive {
			out = &ReleaseHoldingResult{ReleasedQuantity: 0, ReservedQuantity: 0}
			return nil
		}

		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		remaining := res.Quantity - settled
		if remaining < 0 {
			remaining = 0
		}

		var holding model.Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&holding, res.HoldingID).Error; err != nil {
			return err
		}
		holding.ReservedQuantity -= remaining
		if holding.ReservedQuantity < 0 {
			holding.ReservedQuantity = 0
		}
		if err := tx.Save(&holding).Error; err != nil {
			return err
		}

		res.Status = model.HoldingReservationStatusReleased
		res.UpdatedAt = time.Now()
		if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
			return err
		}
		out = &ReleaseHoldingResult{ReleasedQuantity: remaining, ReservedQuantity: holding.ReservedQuantity}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PartialSettle commits a partial fill: decrements both ReservedQuantity and
// Quantity on the holding by `qty`. The shares physically leave the holding at
// this point (analogous to Balance dropping in account-service PartialSettle).
// Idempotent on orderTransactionID via ON CONFLICT DO NOTHING on the
// settlements table. Returns codes.FailedPrecondition if the reservation is
// missing, inactive, or if the settlement would exceed reservation.quantity.
func (s *HoldingReservationService) PartialSettle(
	ctx context.Context,
	orderID, orderTransactionID uint64,
	qty int64,
) (*PartialSettleHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	var out *PartialSettleHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOrderIDForUpdate(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "holding reservation not found")
			}
			return err
		}
		if res.Status != model.HoldingReservationStatusActive {
			return status.Errorf(codes.FailedPrecondition, "reservation status=%s", res.Status)
		}

		var holding model.Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&holding, res.HoldingID).Error; err != nil {
			return err
		}

		settled, err := s.resRepo.WithTx(tx).SumSettlements(res.ID)
		if err != nil {
			return err
		}
		if settled+qty > res.Quantity {
			return status.Errorf(codes.FailedPrecondition,
				"settlement %d would exceed reservation %d", settled+qty, res.Quantity)
		}

		settlement := &model.HoldingReservationSettlement{
			HoldingReservationID: res.ID,
			OrderTransactionID:   orderTransactionID,
			Quantity:             qty,
			CreatedAt:            time.Now(),
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
			// holding state without mutating.
			out = &PartialSettleHoldingResult{
				SettledQuantity:   qty,
				RemainingReserved: holding.ReservedQuantity,
				QuantityAfter:     holding.Quantity,
			}
			return nil
		}

		// First-time settle: the shares physically leave the holding.
		holding.ReservedQuantity -= qty
		if holding.ReservedQuantity < 0 {
			holding.ReservedQuantity = 0
		}
		holding.Quantity -= qty
		if err := tx.Save(&holding).Error; err != nil {
			return err
		}

		// Fully filled? Transition reservation status to settled.
		if settled+qty == res.Quantity {
			res.Status = model.HoldingReservationStatusSettled
			res.UpdatedAt = time.Now()
			if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
				return err
			}
		}

		out = &PartialSettleHoldingResult{
			SettledQuantity:   qty,
			RemainingReserved: holding.ReservedQuantity,
			QuantityAfter:     holding.Quantity,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
