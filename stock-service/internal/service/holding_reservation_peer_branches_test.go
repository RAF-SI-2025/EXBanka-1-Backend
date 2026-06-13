package service

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/model"
)

func reservePeer(t *testing.T, svc *HoldingReservationService, ticker string, peerContractID uint64, qty int64) {
	t.Helper()
	uid := uint64(1)
	if _, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", ticker, peerContractID, qty); err != nil {
		t.Fatalf("reserve peer: %v", err)
	}
}

func TestConsumeForPeerOption_NotFound(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.ConsumeForPeerOptionContract(context.Background(), 9999, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition for missing reservation, got %v", err)
	}
}

func TestConsumeForPeerOption_BadQty(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.ConsumeForPeerOptionContract(context.Background(), 1, 0)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for qty<=0, got %v", err)
	}
}

func TestConsumeForPeerOption_SettlementExceeds(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	reservePeer(t, svc, h.Ticker, 2011, 5)
	_, err := svc.ConsumeForPeerOptionContract(context.Background(), 2011, 6)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition on over-consume, got %v", err)
	}
}

func TestConsumeForPeerOption_FullConsumeThenInactive(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	reservePeer(t, svc, h.Ticker, 2012, 5)
	if _, err := svc.ConsumeForPeerOptionContract(context.Background(), 2012, 5); err != nil {
		t.Fatalf("consume: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 95 || got.ReservedQuantity != 0 {
		t.Errorf("post-consume qty=%d reserved=%d, want 95/0", got.Quantity, got.ReservedQuantity)
	}
	// Reservation is now settled → a further consume is rejected.
	if _, err := svc.ConsumeForPeerOptionContract(context.Background(), 2012, 1); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("settled reservation should reject consume, got %v", err)
	}
}

func TestReleaseForPeerOption_NotFound_And_NotActive(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	// Missing reservation → no-op (0,0).
	out, err := svc.ReleaseForPeerOptionContract(context.Background(), 9999)
	if err != nil || out.ReleasedQuantity != 0 {
		t.Fatalf("missing peer reservation should be no-op, got %+v err=%v", out, err)
	}
	// Reserve then release; a second release is a no-op.
	reservePeer(t, svc, h.Ticker, 2013, 4)
	if _, err := svc.ReleaseForPeerOptionContract(context.Background(), 2013); err != nil {
		t.Fatalf("first release: %v", err)
	}
	out, err = svc.ReleaseForPeerOptionContract(context.Background(), 2013)
	if err != nil || out.ReleasedQuantity != 0 {
		t.Fatalf("second release should be no-op, got %+v err=%v", out, err)
	}
}
