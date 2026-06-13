package service

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/model"
)

func reserveOTC(t *testing.T, svc *HoldingReservationService, secID, contractID uint64, qty int64) {
	t.Helper()
	uid := uint64(1)
	if _, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", secID, contractID, qty); err != nil {
		t.Fatalf("reserve: %v", err)
	}
}

func TestConsumeForOTCContract_SettlementExceeds(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	reserveOTC(t, svc, h.SecurityID, 1011, 5)
	// Consuming 6 against a 5-unit reservation exceeds it.
	_, err := svc.ConsumeForOTCContract(context.Background(), 1011, 6, 9001)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition on over-consume, got %v", err)
	}
}

func TestConsumeForOTCContract_IdempotentReplay(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	reserveOTC(t, svc, h.SecurityID, 1012, 10)
	if _, err := svc.ConsumeForOTCContract(context.Background(), 1012, 5, 7777); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	afterFirst, _ := holdingRepo.GetByID(h.ID)
	// Re-consume with the SAME synthetic txn id → ON CONFLICT DO NOTHING replay.
	if _, err := svc.ConsumeForOTCContract(context.Background(), 1012, 5, 7777); err != nil {
		t.Fatalf("replay consume: %v", err)
	}
	afterReplay, _ := holdingRepo.GetByID(h.ID)
	if afterReplay.Quantity != afterFirst.Quantity {
		t.Errorf("replay must not double-consume: %d vs %d", afterReplay.Quantity, afterFirst.Quantity)
	}
}

func TestConsumeForOTCContract_NotActive(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	reserveOTC(t, svc, h.SecurityID, 1013, 5)
	// Release the reservation → status becomes released (not active).
	if _, err := svc.ReleaseForOTCContract(context.Background(), 1013); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, err := svc.ConsumeForOTCContract(context.Background(), 1013, 5, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition on inactive reservation, got %v", err)
	}
}

func TestReleaseForOTCContract_NotActive_NoOp(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	reserveOTC(t, svc, h.SecurityID, 1014, 5)
	if _, err := svc.ReleaseForOTCContract(context.Background(), 1014); err != nil {
		t.Fatalf("first release: %v", err)
	}
	// Releasing an already-released reservation → no-op (0,0).
	out, err := svc.ReleaseForOTCContract(context.Background(), 1014)
	if err != nil {
		t.Fatalf("second release: %v", err)
	}
	if out.ReleasedQuantity != 0 {
		t.Errorf("second release should be a no-op, got %d", out.ReleasedQuantity)
	}
}

func TestConsumeForOTCContract_FullyConsumesMarksSettled(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	reserveOTC(t, svc, h.SecurityID, 1015, 6)
	// Consume the whole reservation → reservation transitions to settled.
	if _, err := svc.ConsumeForOTCContract(context.Background(), 1015, 6, 5555); err != nil {
		t.Fatalf("consume: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 94 || got.ReservedQuantity != 0 {
		t.Errorf("post full-consume qty=%d reserved=%d, want 94/0", got.Quantity, got.ReservedQuantity)
	}
	// A further consume on the now-settled reservation is rejected.
	_, err := svc.ConsumeForOTCContract(context.Background(), 1015, 1, 6666)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("settled reservation should reject further consume, got %v", err)
	}
}
