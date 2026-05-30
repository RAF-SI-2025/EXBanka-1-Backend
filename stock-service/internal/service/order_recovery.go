package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// RecoverPlacementSaga auto-resolves a crash-stranded order-placement saga with
// no human intervention. Called by the saga-recovery reconciler for any stuck
// placement step; idempotent.
//
// The order row (assembled with every needed field and stamped with sagaID
// before the saga runs) is the recovery anchor:
//   - No order for this saga → it crashed before persist_order_pending
//     committed; nothing is reservable/committed, so this is a clean no-op (the
//     user can re-place).
//   - Otherwise rebuild the identical saga from the persisted order + its
//     listing, then Compensate when it was already rolling back (delete order,
//     release reservation) or Execute to forward-resume to approved/pending.
//     persist is an idempotent mint and the reservation step is keyed, so the
//     replay is safe.
//
// The actuary used-limit bump and Kafka/notification side effects from the
// request path are intentionally skipped on recovery: the bump is best-effort +
// non-idempotent (the order already stamps the amount/actuary-id for
// reconciliation), and notifications are best-effort.
func (s *OrderService) RecoverPlacementSaga(ctx context.Context, sagaID string, orderID uint64) error {
	if sagaID == "" {
		return errors.New("recover placement saga: empty sagaID")
	}
	order, err := s.orderRepo.GetBySagaID(sagaID)
	if errors.Is(err, gorm.ErrRecordNotFound) || order == nil {
		return nil // never persisted — nothing to drive or roll back
	}
	if err != nil {
		return fmt.Errorf("recover placement saga %s: load order: %w", sagaID, err)
	}

	// Resolve the listing (GetBySagaID preloads it; fall back to a direct load).
	listing := &order.Listing
	if listing.ID == 0 {
		l, lerr := s.listingRepo.GetByID(order.ListingID)
		if lerr != nil {
			return fmt.Errorf("recover placement saga %s: load listing %d: %w", sagaID, order.ListingID, lerr)
		}
		listing = l
	}

	reserveAmount := decimal.Zero
	if order.ReservationAmount != nil {
		reserveAmount = *order.ReservationAmount
	}
	// systemType is reconstructed from the persisted owner (the request's
	// system_type isn't stored). Bank-owned (incl. fund orders) → "bank";
	// otherwise "client". This only feeds the finalize step's ApprovedBy label
	// and the legacy actuary fallback (which is gated on system_type=="employee"
	// and so never fires on recovery — the actuary gate still runs via the
	// persisted ActingEmployeeID).
	systemType := "client"
	if order.OwnerType == model.OwnerBank {
		systemType = "bank"
	}
	var actingEmployeeID uint64
	if order.ActingEmployeeID != nil {
		actingEmployeeID = *order.ActingEmployeeID
	}

	var result placementResult
	sg, state := s.buildPlacementSaga(sagaID, placementParams{
		order:            order,
		listing:          listing,
		orderOwnerType:   order.OwnerType,
		orderOwnerID:     order.OwnerID,
		direction:        order.Direction,
		quantity:         order.Quantity,
		accountID:        order.AccountID,
		reserveAmount:    reserveAmount,
		reserveCurrency:  order.ReservationCurrency,
		actingEmployeeID: actingEmployeeID,
		systemType:       systemType,
	}, &result)

	rollingBack := false
	if chk, ok := s.sagaRepo.(sagaCompensationChecker); ok {
		has, herr := chk.HasCompensations(sagaID)
		if herr != nil {
			return fmt.Errorf("recover placement saga %s: compensation check: %w", sagaID, herr)
		}
		rollingBack = has
	}
	if rollingBack {
		return sg.Compensate(ctx, state)
	}
	return sg.Execute(ctx, state)
}
