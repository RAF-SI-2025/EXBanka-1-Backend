package service

import (
	"context"
	"testing"
)

// TestRecoverPlacementSaga_ForwardResumeOnce proves the no-human auto-resolve
// forward path for order placement: re-driving a completed/stranded placement
// saga neither mints a duplicate order nor double-reserves funds (completed
// steps are skipped on resume), even across repeated recovery invocations.
func TestRecoverPlacementSaga_ForwardResumeOnce(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.SagaID == "" {
		t.Fatal("saga_id not populated")
	}

	for i := 0; i < 2; i++ {
		if rerr := fx.svc.RecoverPlacementSaga(context.Background(), order.SagaID, order.ID); rerr != nil {
			t.Fatalf("RecoverPlacementSaga #%d: %v", i, rerr)
		}
	}

	if len(fx.orderRepo.orders) != 1 {
		t.Fatalf("recovery minted duplicate order(s): %d rows, want 1", len(fx.orderRepo.orders))
	}
	if len(fx.accountClient.reserveCalls) != 1 {
		t.Fatalf("recovery double-reserved: %d ReserveFunds calls, want 1", len(fx.accountClient.reserveCalls))
	}
	got, err := fx.orderRepo.GetByID(order.ID)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if got.Status != "approved" {
		t.Fatalf("order status = %s, want approved", got.Status)
	}
}

// TestRecoverPlacementSaga_NoOrder_NoOp proves a clean no-op when the saga never
// persisted an order (crash before persist_order_pending committed).
func TestRecoverPlacementSaga_NoOrder_NoOp(t *testing.T) {
	fx := newOrderServiceFixture()
	if err := fx.svc.RecoverPlacementSaga(context.Background(), "never-ran-placement", 0); err != nil {
		t.Fatalf("RecoverPlacementSaga: %v", err)
	}
	if len(fx.orderRepo.orders) != 0 {
		t.Fatalf("no-op recovery created orders: %d", len(fx.orderRepo.orders))
	}
}
