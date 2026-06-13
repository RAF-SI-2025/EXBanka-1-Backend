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

// ---------------- Cross-bank SI-TX vote-time share hold (Celina-5) ----------

// ReserveForCrossBankNewTx places a real hold (raises reserved_quantity) so
// shares can't be sold before COMMIT — the spec's "rezervacija hartija".
func TestHoldingReservation_ReserveForCrossBankNewTx_HoldsShares(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	out, err := svc.ReserveForCrossBankNewTx(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, "222:idem-1", 30)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if out.ReservedQuantity != 30 || out.AvailableQuantity != 70 {
		t.Fatalf("reserved=%d available=%d want 30/70", out.ReservedQuantity, out.AvailableQuantity)
	}
	// The hold is real: the holding row's reserved_quantity rose.
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 30 {
		t.Fatalf("holding.reserved_quantity=%d want 30 (hold not persisted)", got.ReservedQuantity)
	}
}

func TestHoldingReservation_ReserveForCrossBankNewTx_Insufficient(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForCrossBankNewTx(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, "222:idem-x", 9999)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

func TestHoldingReservation_ReserveForCrossBankNewTx_Idempotent(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	a, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", h.Ticker, "222:dup", 10)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", h.Ticker, "222:dup", 10)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.ReservationID != b.ReservationID {
		t.Fatalf("replay made a new reservation (%d vs %d)", a.ReservationID, b.ReservationID)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 10 {
		t.Fatalf("replay double-held: reserved=%d want 10", got.ReservedQuantity)
	}
}

// Attach links the vote-time hold to the minted contract; the existing
// consume-by-contract-id path then settles it. Proves the end-to-end re-key.
func TestHoldingReservation_AttachThenConsume(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	if _, err := svc.ReserveForCrossBankNewTx(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, "222:attach", 20); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	const contractID = uint64(7001)
	if err := svc.AttachCrossBankReservationToContract(context.Background(), "222:attach", contractID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Idempotent re-attach.
	if err := svc.AttachCrossBankReservationToContract(context.Background(), "222:attach", contractID); err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	// Now the contract-id-keyed consume path works on the same reservation.
	if _, err := svc.ConsumeForPeerOptionContract(context.Background(), contractID, 20); err != nil {
		t.Fatalf("consume via attached contract id: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 80 {
		t.Fatalf("after consume quantity=%d want 80 (100-20)", got.Quantity)
	}
}

func TestHoldingReservation_AttachMissing_NotFound(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	err := svc.AttachCrossBankReservationToContract(context.Background(), "222:nope", 9)
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound (so COMMIT falls back to reserve), got %v", err)
	}
}

// Release returns the held shares on ROLLBACK.
func TestHoldingReservation_ReleaseForCrossBankNewTx(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	if _, err := svc.ReserveForCrossBankNewTx(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, "222:rb", 40); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	out, err := svc.ReleaseForCrossBankNewTx(context.Background(), "222:rb")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if out.ReleasedQuantity != 40 {
		t.Fatalf("released=%d want 40", out.ReleasedQuantity)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 0 {
		t.Fatalf("after release reserved=%d want 0", got.ReservedQuantity)
	}
	// Idempotent: releasing again is a no-op.
	out2, err := svc.ReleaseForCrossBankNewTx(context.Background(), "222:rb")
	if err != nil || out2.ReleasedQuantity != 0 {
		t.Fatalf("re-release not idempotent: out=%+v err=%v", out2, err)
	}
}
