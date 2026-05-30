package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	shared "github.com/exbanka/contract/shared"
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
//
// AveragePriceBefore captures the seller's cost basis snapshot at the
// instant the consume runs (under the row lock), so callers can write a
// realised CapitalGain row without a second DB read race. Populated by
// ConsumeForPeerOptionContract; left zero by paths that don't need it.
type PartialSettleHoldingResult struct {
	SettledQuantity    int64
	RemainingReserved  int64
	QuantityAfter      int64
	AveragePriceBefore decimal.Decimal
	// AlreadySettled is true when this call was a replay: the settlement
	// for this synthetic txn id already existed, so no shares moved this
	// time. Callers that perform non-idempotent follow-up writes (e.g. a
	// realised CapitalGain row) MUST skip them when this is true, or a
	// retried exercise would duplicate those rows.
	AlreadySettled bool
}

// Reserve locks `qty` shares of the given holding for `orderID`. Idempotent on
// orderID: retries with the same orderID return the existing reservation
// without double-counting. Returns codes.FailedPrecondition when the holding
// does not exist or available quantity is insufficient.
//
// Post-rollup (Part A): holdings aggregate per (owner_type, owner_id,
// security_type, security_id). The lookup no longer filters by account_id;
// proceeds on a sell fill are credited to the order's AccountID independently.
func (s *HoldingReservationService) Reserve(
	ctx context.Context,
	ownerType model.OwnerType,
	ownerID *uint64,
	securityType string,
	securityID, orderID uint64,
	qty int64,
) (*ReserveHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	var out *ReserveHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var holding model.Holding
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_type = ? AND security_id = ?", securityType, securityID)
		if ownerID == nil {
			q = q.Where("owner_type = ? AND owner_id IS NULL", ownerType)
		} else {
			q = q.Where("owner_type = ? AND owner_id = ?", ownerType, *ownerID)
		}
		err := q.First(&holding).Error
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// ---------------------------------------------------------------------------
// OTC option-contract variants of Reserve / Release / Consume
// ---------------------------------------------------------------------------

// ReserveForPeerOptionContract locks `qty` shares of the seller's stock
// holding against a cross-bank (Celina-5 SI-TX) OTC option contract.
// Mirror of ReserveForOTCContract, keyed on PeerOptionContractID instead
// of OTCContractID; idempotent on contract ID.
//
// Looks up the seller's holding by (security_type, ticker) under
// SELECT FOR UPDATE, verifies sufficient available quantity, increments
// ReservedQuantity, and writes a HoldingReservation row pointing at the
// peer_option_contracts row. Returns FailedPrecondition with
// "holding not found" or "insufficient available quantity" so the caller
// can map to an SI-TX NoVote reason if needed at a later phase (today
// the reservation runs at COMMIT_TX time, but the same contract guards
// settlement).
func (s *HoldingReservationService) ReserveForPeerOptionContract(
	ctx context.Context,
	sellerOwnerType model.OwnerType,
	sellerOwnerID *uint64,
	securityType, ticker string,
	peerOptionContractID uint64,
	qty int64,
) (*ReserveHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	// Tickers across banks may arrive in mixed case via SI-TX (the
	// peer's quoting convention is theirs, not ours). Normalize to
	// upper for the holdings lookup so "csco" from a peer still
	// matches our local "CSCO" holding row. (Fix #4, 2026-05-16.)
	ticker = strings.ToUpper(ticker)
	var out *ReserveHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var holding model.Holding
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_type = ? AND ticker = ?", securityType, ticker)
		if sellerOwnerID == nil {
			q = q.Where("owner_type = ? AND owner_id IS NULL", sellerOwnerType)
		} else {
			q = q.Where("owner_type = ? AND owner_id = ?", sellerOwnerType, *sellerOwnerID)
		}
		err := q.First(&holding).Error
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
		cid := peerOptionContractID
		res := &model.HoldingReservation{
			HoldingID:            holding.ID,
			PeerOptionContractID: &cid,
			Quantity:             qty,
			Status:               model.HoldingReservationStatusActive,
			CreatedAt:            time.Now(),
			UpdatedAt:            time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			out = &ReserveHoldingResult{
				ReservationID:     existing.ID,
				ReservedQuantity:  holding.ReservedQuantity,
				AvailableQuantity: holding.Quantity - holding.ReservedQuantity,
			}
			return nil
		}
		holding.ReservedQuantity += qty
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// ReserveForOTCContract locks `qty` shares of the seller's stock holding
// under an OTC option contract. Mirror of Reserve, keyed on OTCContractID
// instead of OrderID; idempotent on contract ID.
func (s *HoldingReservationService) ReserveForOTCContract(
	ctx context.Context,
	sellerOwnerType model.OwnerType,
	sellerOwnerID *uint64,
	securityType string,
	securityID, otcContractID uint64,
	qty int64,
) (*ReserveHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	var out *ReserveHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var holding model.Holding
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_type = ? AND security_id = ?", securityType, securityID)
		if sellerOwnerID == nil {
			q = q.Where("owner_type = ? AND owner_id IS NULL", sellerOwnerType)
		} else {
			q = q.Where("owner_type = ? AND owner_id = ?", sellerOwnerType, *sellerOwnerID)
		}
		err := q.First(&holding).Error
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
		cid := otcContractID
		res := &model.HoldingReservation{
			HoldingID:     holding.ID,
			OTCContractID: &cid,
			Quantity:      qty,
			Status:        model.HoldingReservationStatusActive,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			out = &ReserveHoldingResult{
				ReservationID:     existing.ID,
				ReservedQuantity:  holding.ReservedQuantity,
				AvailableQuantity: holding.Quantity - holding.ReservedQuantity,
			}
			return nil
		}
		holding.ReservedQuantity += qty
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// ReleaseForOTCContract releases an OTC reservation. Used on contract expiry
// (seller keeps premium, shares unlock) and on accept-saga compensation paths.
func (s *HoldingReservationService) ReleaseForOTCContract(ctx context.Context, otcContractID uint64) (*ReleaseHoldingResult, error) {
	var out *ReleaseHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOTCContractIDForUpdate(otcContractID)
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// ConsumeForOTCContract physically transfers `qty` shares from the seller's
// holding (same shape as PartialSettle) under the OTC contract. Used by the
// exercise saga after the buyer's strike-funds debit lands. Idempotent via
// the existing settlements table (synthetic order_transaction_id derived
// from the contract ID).
func (s *HoldingReservationService) ConsumeForOTCContract(
	ctx context.Context,
	otcContractID uint64,
	qty int64,
	syntheticTxnID uint64,
) (*PartialSettleHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	var out *PartialSettleHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByOTCContractIDForUpdate(otcContractID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "OTC reservation not found")
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
			OrderTransactionID:   syntheticTxnID,
			Quantity:             qty,
			CreatedAt:            time.Now(),
		}
		createResult := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "order_transaction_id"}},
			DoNothing: true,
		}).Create(settlement)
		if createResult.Error != nil {
			return createResult.Error
		}
		if createResult.RowsAffected == 0 {
			out = &PartialSettleHoldingResult{
				SettledQuantity:   qty,
				RemainingReserved: holding.ReservedQuantity,
				QuantityAfter:     holding.Quantity,
			}
			return nil
		}
		holding.ReservedQuantity -= qty
		if holding.ReservedQuantity < 0 {
			holding.ReservedQuantity = 0
		}
		holding.Quantity -= qty
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
			return err
		}
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

// RestoreForOTCContract reverses ConsumeForOTCContract: it returns the
// consumed shares to the seller's holding (restoring both Quantity and
// ReservedQuantity), deletes the settlement row keyed by syntheticTxnID, and
// reactivates the reservation if it had been marked settled. This is the
// backward compensator for the exercise saga's consume_seller_holding step
// (pivot removal — 2026-05-29): once it exists, a failure in any later step
// can fully unwind the share transfer instead of being treated as
// irrecoverable.
//
// Idempotent: if no settlement row exists for syntheticTxnID (the consume
// never ran, or this restore already ran) it is a no-op, so the saga's
// backward pass can be retried until success per the SAGA spec.
func (s *HoldingReservationService) RestoreForOTCContract(
	ctx context.Context,
	otcContractID uint64,
	syntheticTxnID uint64,
) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var settlement model.HoldingReservationSettlement
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_transaction_id = ?", syntheticTxnID).
			First(&settlement).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // nothing consumed under this key — idempotent no-op
		}
		if err != nil {
			return err
		}
		res, err := s.resRepo.WithTx(tx).GetByOTCContractIDForUpdate(otcContractID)
		if err != nil {
			return err
		}
		var holding model.Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&holding, res.HoldingID).Error; err != nil {
			return err
		}
		// Reverse the consume: shares (and their reservation) return to the seller.
		holding.Quantity += settlement.Quantity
		holding.ReservedQuantity += settlement.Quantity
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
			return err
		}
		if err := tx.Delete(&settlement).Error; err != nil {
			return err
		}
		// A fully-consumed reservation was marked Settled; reactivate it so the
		// contract can be exercised again after this compensation.
		if res.Status == model.HoldingReservationStatusSettled {
			res.Status = model.HoldingReservationStatusActive
			res.UpdatedAt = time.Now()
			if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Cross-bank OTC option-contract variants (Celina-5 SI-TX exercise)
// ---------------------------------------------------------------------------

// ConsumeForPeerOptionContract is the cross-bank analogue of
// ConsumeForOTCContract: physically transfers `qty` shares OUT of the
// seller's holding by settling the reservation pinned to a
// peer_option_contracts row. Called by the SI-TX exercise flow's
// COMMIT_TX handler on the seller's bank.
//
// Idempotent via the holding_reservation_settlements table; the
// synthetic order_transaction_id is the peer contract id + a constant
// offset to keep the namespace disjoint from intra-bank order
// transaction ids.
func (s *HoldingReservationService) ConsumeForPeerOptionContract(
	ctx context.Context,
	peerOptionContractID uint64,
	qty int64,
) (*PartialSettleHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	const peerOTCSettlementOffset = uint64(1_000_000_000_000_000) // 1e15 — separates peer contract ids from order tx ids
	syntheticTxnID := peerOptionContractID + peerOTCSettlementOffset
	var out *PartialSettleHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Fix R1 (2026-05-16): use the SELECT FOR UPDATE variant so a
		// concurrent ReleaseForPeerOptionContract on the same row can't
		// pass the active-status check after we've already started
		// settling — without the row lock, both flows would decrement
		// holding.reserved_quantity for the same shares.
		res, err := s.resRepo.WithTx(tx).GetByPeerOptionContractIDForUpdate(peerOptionContractID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "peer option reservation not found")
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
			OrderTransactionID:   syntheticTxnID,
			Quantity:             qty,
			CreatedAt:            time.Now(),
		}
		createResult := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "order_transaction_id"}},
			DoNothing: true,
		}).Create(settlement)
		if createResult.Error != nil {
			return createResult.Error
		}
		if createResult.RowsAffected == 0 {
			out = &PartialSettleHoldingResult{
				SettledQuantity:    qty,
				RemainingReserved:  holding.ReservedQuantity,
				QuantityAfter:      holding.Quantity,
				AveragePriceBefore: holding.AveragePrice,
				AlreadySettled:     true,
			}
			return nil
		}
		// Snapshot the seller's cost basis BEFORE the holding row mutates
		// so the caller can write a CapitalGain row for the realised
		// strike-priced sale. Cost basis doesn't change as shares leave
		// (weighted average is preserved), but capturing here keeps the
		// snapshot inside the row lock so a concurrent buy/sell can't
		// shift AveragePrice between consume and CG write.
		costBasisBefore := holding.AveragePrice
		holding.ReservedQuantity -= qty
		if holding.ReservedQuantity < 0 {
			holding.ReservedQuantity = 0
		}
		holding.Quantity -= qty
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
			return err
		}
		if settled+qty == res.Quantity {
			res.Status = model.HoldingReservationStatusSettled
			res.UpdatedAt = time.Now()
			if err := s.resRepo.WithTx(tx).UpdateStatus(res); err != nil {
				return err
			}
		}
		out = &PartialSettleHoldingResult{
			SettledQuantity:    qty,
			RemainingReserved:  holding.ReservedQuantity,
			QuantityAfter:      holding.Quantity,
			AveragePriceBefore: costBasisBefore,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseForPeerOptionContract is the cross-bank analogue of
// ReleaseForOTCContract: releases the reservation without settling
// (used on rollback paths or expiry without exercise). The shares
// remain on the seller's holding, just unlocked.
func (s *HoldingReservationService) ReleaseForPeerOptionContract(ctx context.Context, peerOptionContractID uint64) (*ReleaseHoldingResult, error) {
	var out *ReleaseHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Fix R1 (2026-05-16): use the SELECT FOR UPDATE variant —
		// see ConsumeForPeerOptionContract for the full rationale.
		res, err := s.resRepo.WithTx(tx).GetByPeerOptionContractIDForUpdate(peerOptionContractID)
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// ReserveForCrossBankNewTx locks `qty` shares of the seller's stock holding at
// SI-TX NEW_TX (vote) time, keyed on the cross-bank transaction identity
// (crossbankTxID = "<peerCode>:<idem>") because the peer_option_contracts row
// does not exist yet — it is minted only at COMMIT_TX. This is the spec's
// "rezervacija hartija" step (Celina-5 OTC SAGA step 2): the seller's shares
// must be HELD when the bank votes YES, not merely checked, so they cannot be
// sold in the window before COMMIT. Mirror of ReserveForPeerOptionContract,
// keyed on CrossbankTxID; idempotent on the identity.
//
// At COMMIT the reservation is linked to the contract via
// AttachCrossBankReservationToContract; on ROLLBACK it is released via
// ReleaseForCrossBankNewTx.
func (s *HoldingReservationService) ReserveForCrossBankNewTx(
	ctx context.Context,
	sellerOwnerType model.OwnerType,
	sellerOwnerID *uint64,
	securityType, ticker, crossbankTxID string,
	qty int64,
) (*ReserveHoldingResult, error) {
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	if crossbankTxID == "" {
		return nil, status.Error(codes.InvalidArgument, "crossbankTxID required")
	}
	// Tickers across banks may arrive in mixed case via SI-TX; normalize for
	// the holdings lookup (same as ReserveForPeerOptionContract, Fix #4).
	ticker = strings.ToUpper(ticker)
	var out *ReserveHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var holding model.Holding
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_type = ? AND ticker = ?", securityType, ticker)
		if sellerOwnerID == nil {
			q = q.Where("owner_type = ? AND owner_id IS NULL", sellerOwnerType)
		} else {
			q = q.Where("owner_type = ? AND owner_id = ?", sellerOwnerType, *sellerOwnerID)
		}
		err := q.First(&holding).Error
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
		ctxID := crossbankTxID
		res := &model.HoldingReservation{
			HoldingID:     holding.ID,
			CrossbankTxID: &ctxID,
			Quantity:      qty,
			Status:        model.HoldingReservationStatusActive,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		inserted, existing, err := s.resRepo.WithTx(tx).InsertIfAbsent(res)
		if err != nil {
			return err
		}
		if !inserted {
			out = &ReserveHoldingResult{
				ReservationID:     existing.ID,
				ReservedQuantity:  holding.ReservedQuantity,
				AvailableQuantity: holding.Quantity - holding.ReservedQuantity,
			}
			return nil
		}
		holding.ReservedQuantity += qty
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// AttachCrossBankReservationToContract links a vote-time crossbank_tx_id
// reservation to the peer_option_contracts row minted at COMMIT_TX, by setting
// its PeerOptionContractID. After this, the existing consume/release-by-
// contract-id settlement & exercise paths operate on the same reservation
// unchanged — no shares move here, the hold is simply re-keyed.
//
// Idempotent: a no-op if the reservation is already attached to this contract,
// or if no crossbank reservation exists (returns NotFound so the caller can
// fall back to reserve-at-commit during rollout). Runs under FOR UPDATE.
func (s *HoldingReservationService) AttachCrossBankReservationToContract(ctx context.Context, crossbankTxID string, peerOptionContractID uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByCrossbankTxIDForUpdate(crossbankTxID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Map to a gRPC NotFound so the COMMIT caller can detect "no
				// vote-time hold" and fall back to reserve-at-commit.
				return status.Error(codes.NotFound, "no vote-time share reservation for this crossbank_tx_id")
			}
			return err
		}
		if res.PeerOptionContractID != nil && *res.PeerOptionContractID == peerOptionContractID {
			return nil // already attached — idempotent
		}
		cid := peerOptionContractID
		res.PeerOptionContractID = &cid
		res.UpdatedAt = time.Now()
		return s.resRepo.WithTx(tx).UpdateStatus(res)
	})
}

// ReleaseForCrossBankNewTx releases a vote-time crossbank_tx_id share
// reservation (used on SI-TX ROLLBACK_TX when the trade voted YES but never
// committed, so no peer_option_contracts row was minted). Mirror of
// ReleaseForPeerOptionContract keyed on CrossbankTxID. No-op (released=0) when
// the reservation is missing or already non-active, so rollback is idempotent.
func (s *HoldingReservationService) ReleaseForCrossBankNewTx(ctx context.Context, crossbankTxID string) (*ReleaseHoldingResult, error) {
	var out *ReleaseHoldingResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res, err := s.resRepo.WithTx(tx).GetByCrossbankTxIDForUpdate(crossbankTxID)
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
		if err := shared.CheckRowsAffected(tx.Save(&holding)); err != nil {
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

// CreditBuyerHoldingForPeerOption credits the buyer's holding when a
// cross-bank exercise lands on the buyer's bank. Finds an existing
// holding by (owner, ticker) and increments quantity, or creates a
// new holding row when the buyer didn't previously hold the stock.
//
// NOTE: this is the raw, NON-idempotent credit (each call adds qty). The
// exercise flow must go through ExerciseBuyerCreditForPeerOption, which
// guards it with the peer_option_contracts status under a row lock so a
// replayed exercise can't double-credit. This bare method is retained as a
// building block and for unit tests of the crediting math.
//
// strikePrice is the per-share price the buyer paid on exercise and
// becomes the new holding's AveragePrice (when this is the buyer's
// first holding for the ticker) or is folded into the running
// weighted average (when the buyer already held shares). Premium paid
// at acceptance is accounted for separately as a CapitalGain row with
// SecurityType="option" — it does NOT belong in cost basis.
func (s *HoldingReservationService) CreditBuyerHoldingForPeerOption(
	ctx context.Context,
	ownerType model.OwnerType,
	ownerID *uint64,
	ticker string,
	qty int64,
	strikePrice decimal.Decimal,
) error {
	if qty <= 0 {
		return status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return creditBuyerHoldingTx(tx, ownerType, ownerID, ticker, qty, strikePrice)
	})
}

// ExerciseBuyerCreditForPeerOption credits the buyer's gained shares AND
// transitions the peer_option_contracts row to "exercised" in a single
// transaction, using the contract status (read under a row lock) as the
// idempotency guard. A replayed cross-bank exercise (duplicate COMMIT_TX)
// finds status="exercised" and is a no-op, so the buyer's shares are never
// credited twice. This closes the gap where the bare credit and a separate
// status flip could be interrupted between the two, double-crediting on retry.
func (s *HoldingReservationService) ExerciseBuyerCreditForPeerOption(
	ctx context.Context,
	peerOptionContractID uint64,
	ownerType model.OwnerType,
	ownerID *uint64,
	ticker string,
	qty int64,
	strikePrice decimal.Decimal,
) error {
	if qty <= 0 {
		return status.Error(codes.InvalidArgument, "qty must be > 0")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var contract model.PeerOptionContract
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&contract, peerOptionContractID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "peer option contract not found")
			}
			return err
		}
		if contract.Status == "exercised" {
			return nil // already credited + exercised by a prior committed attempt
		}
		// "exercising" is the buyer-side claim set at InitiateOptionExercise to
		// serialise concurrent exercises; "active" covers the unclaimed path.
		if contract.Status != "active" && contract.Status != "exercising" {
			return status.Errorf(codes.FailedPrecondition, "cannot exercise contract in status %q", contract.Status)
		}
		if err := creditBuyerHoldingTx(tx, ownerType, ownerID, ticker, qty, strikePrice); err != nil {
			return err
		}
		return tx.Model(&model.PeerOptionContract{}).
			Where("id = ?", contract.ID).
			Update("status", "exercised").Error
	})
}

// creditBuyerHoldingTx performs the buyer holding credit (weighted-average
// cost basis, or a new holding row) inside the caller's transaction.
// Extracted so the bare CreditBuyerHoldingForPeerOption and the idempotent
// ExerciseBuyerCreditForPeerOption share identical crediting math.
func creditBuyerHoldingTx(
	tx *gorm.DB,
	ownerType model.OwnerType,
	ownerID *uint64,
	ticker string,
	qty int64,
	strikePrice decimal.Decimal,
) error {
	var holding model.Holding
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("security_type = ? AND ticker = ?", "stock", ticker)
	if ownerID == nil {
		q = q.Where("owner_type = ? AND owner_id IS NULL", ownerType)
	} else {
		q = q.Where("owner_type = ? AND owner_id = ?", ownerType, *ownerID)
	}
	err := q.First(&holding).Error
	if err == nil {
		// Existing position: weighted-average cost basis across the
		// pre-existing shares and the strike-priced shares we're
		// crediting now. Mirrors HoldingRepository.Upsert math so
		// the cross-bank exercise produces the same cost basis a
		// matching market buy at the strike would have.
		oldTotal := holding.AveragePrice.Mul(decimal.NewFromInt(holding.Quantity))
		newTotal := strikePrice.Mul(decimal.NewFromInt(qty))
		totalQty := holding.Quantity + qty
		holding.AveragePrice = oldTotal.Add(newTotal).Div(decimal.NewFromInt(totalQty))
		holding.Quantity = totalQty
		return shared.CheckRowsAffected(tx.Save(&holding))
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// New holding row — populate the required scaffolding so the
	// row passes existing not-null + unique constraints. Listing
	// id and security id default to 0 here; subsequent reads will
	// resolve them when the user interacts with the holding via
	// portfolio endpoints. AveragePrice = strike: the user paid
	// the strike per share to acquire these, so later sells use
	// the strike as cost basis (premium tracked separately via
	// the option CG row written at acceptance).
	newHolding := model.Holding{
		OwnerType:        ownerType,
		OwnerID:          ownerID,
		SecurityType:     "stock",
		SecurityID:       0,
		ListingID:        0,
		Ticker:           ticker,
		Name:             ticker,
		Quantity:         qty,
		ReservedQuantity: 0,
		AveragePrice:     strikePrice,
	}
	return tx.Create(&newHolding).Error
}
