package service

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------- ReserveForOTCContract ----------------

func TestHoldingReservationService_ReserveForOTCContract_HappyPath(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	out, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 333, 12)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.ReservedQuantity != 12 {
		t.Errorf("reserved=%d", out.ReservedQuantity)
	}
}

func TestHoldingReservationService_ReserveForOTCContract_BadQty(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", 1, 333, 0)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestHoldingReservationService_ReserveForOTCContract_HoldingMissing(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	uid := uint64(99)
	_, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", 99, 333, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

func TestHoldingReservationService_ReserveForOTCContract_Insufficient(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 333, 9999)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

func TestHoldingReservationService_ReserveForOTCContract_Idempotent(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	first, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 444, 5)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 444, 5)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ReservationID != second.ReservationID {
		t.Errorf("ids differ")
	}
}

// ---------------- ReleaseForOTCContract ----------------

func TestHoldingReservationService_ReleaseForOTCContract_HappyPath(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 555, 7)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	out, err := svc.ReleaseForOTCContract(context.Background(), 555)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if out.ReleasedQuantity != 7 {
		t.Errorf("released=%d", out.ReleasedQuantity)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 0 {
		t.Errorf("expected reserved=0 after release")
	}
}

func TestHoldingReservationService_ReleaseForOTCContract_NoReservation(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	out, err := svc.ReleaseForOTCContract(context.Background(), 9999)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.ReleasedQuantity != 0 {
		t.Errorf("expected 0, got %d", out.ReleasedQuantity)
	}
}

// ---------------- ConsumeForOTCContract ----------------

func TestHoldingReservationService_ConsumeForOTCContract_HappyPath(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 666, 5)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := svc.ConsumeForOTCContract(context.Background(), 666, 5, 1); err != nil {
		t.Fatalf("consume: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 95 || got.ReservedQuantity != 0 {
		t.Errorf("post-consume holding qty=%d reserved=%d", got.Quantity, got.ReservedQuantity)
	}
}

// TestHoldingReservationService_RestoreForOTCContract_RoundTrip is the
// pivot-removal compensator test: after a full consume, RestoreForOTCContract
// must return the shares (Quantity) and their reservation (ReservedQuantity)
// to the seller, reactivate the reservation, and be idempotent on retry.
func TestHoldingReservationService_RestoreForOTCContract_RoundTrip(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	if _, err := svc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.SecurityID, 777, 5); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := svc.ConsumeForOTCContract(context.Background(), 777, 5, 42); err != nil {
		t.Fatalf("consume: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 95 || got.ReservedQuantity != 0 {
		t.Fatalf("post-consume qty=%d reserved=%d (want 95/0)", got.Quantity, got.ReservedQuantity)
	}

	// Restore returns shares + reservation.
	if err := svc.RestoreForOTCContract(context.Background(), 777, 42); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, _ = holdingRepo.GetByID(h.ID)
	if got.Quantity != 100 || got.ReservedQuantity != 5 {
		t.Errorf("post-restore qty=%d reserved=%d (want 100/5)", got.Quantity, got.ReservedQuantity)
	}

	// Idempotent: a second restore (settlement row already deleted) is a no-op.
	if err := svc.RestoreForOTCContract(context.Background(), 777, 42); err != nil {
		t.Fatalf("restore #2: %v", err)
	}
	got, _ = holdingRepo.GetByID(h.ID)
	if got.Quantity != 100 || got.ReservedQuantity != 5 {
		t.Errorf("post-2nd-restore qty=%d reserved=%d (want 100/5)", got.Quantity, got.ReservedQuantity)
	}

	// Reservation reactivated → it can be consumed again (new txn id).
	if _, err := svc.ConsumeForOTCContract(context.Background(), 777, 5, 43); err != nil {
		t.Fatalf("re-consume after restore: %v", err)
	}
}

func TestHoldingReservationService_ConsumeForOTCContract_BadQty(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.ConsumeForOTCContract(context.Background(), 1, 0, 1)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestHoldingReservationService_ConsumeForOTCContract_NoReservation(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.ConsumeForOTCContract(context.Background(), 9999, 1, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}
