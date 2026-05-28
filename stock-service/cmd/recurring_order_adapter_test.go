package main

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// stubOrderCreator records the CreateOrder request it receives so the
// recurring-order placer adapter's input mapping can be asserted, and
// returns a canned error to verify propagation.
type stubOrderCreator struct {
	got  service.CreateOrderRequest
	err  error
	call int
}

func (s *stubOrderCreator) CreateOrder(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
	s.call++
	s.got = req
	return &model.Order{}, s.err
}

// TestRecurringInputToCreateOrder_Client maps a client-owned recurring
// template tick into a client Market CreateOrderRequest.
func TestRecurringInputToCreateOrder_Client(t *testing.T) {
	owner := uint64(42)
	got := recurringInputToCreateOrder(service.PlaceMarketInput{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 7,
		AccountID: 9,
		Side:      "buy",
		Quantity:  3,
	})
	if got.SystemType != "client" {
		t.Errorf("SystemType: got %q want client", got.SystemType)
	}
	if got.UserID != 42 {
		t.Errorf("UserID: got %d want 42", got.UserID)
	}
	if got.OrderType != "market" {
		t.Errorf("OrderType: got %q want market", got.OrderType)
	}
	if got.Direction != "buy" {
		t.Errorf("Direction: got %q want buy", got.Direction)
	}
	if got.ListingID != 7 || got.AccountID != 9 || got.Quantity != 3 {
		t.Errorf("listing/account/qty mismatch: %+v", got)
	}
}

// TestRecurringInputToCreateOrder_Bank maps a bank-owned (employee-created)
// template tick into a bank Market CreateOrderRequest with no UserID.
func TestRecurringInputToCreateOrder_Bank(t *testing.T) {
	got := recurringInputToCreateOrder(service.PlaceMarketInput{
		OwnerType: model.OwnerBank,
		OwnerID:   nil,
		ListingID: 1,
		AccountID: 2,
		Side:      "sell",
		Quantity:  5,
	})
	if got.SystemType != "bank" {
		t.Errorf("SystemType: got %q want bank", got.SystemType)
	}
	if got.UserID != 0 {
		t.Errorf("UserID: got %d want 0 for bank order", got.UserID)
	}
	if got.OrderType != "market" {
		t.Errorf("OrderType: got %q want market", got.OrderType)
	}
	if got.Direction != "sell" {
		t.Errorf("Direction: got %q want sell", got.Direction)
	}
}

// TestRecurringOrderPlacerAdapter_PlacesMappedOrder verifies the adapter
// forwards the mapped request to CreateOrder and propagates its error.
func TestRecurringOrderPlacerAdapter_PlacesMappedOrder(t *testing.T) {
	stub := &stubOrderCreator{}
	a := &recurringOrderPlacerAdapter{orders: stub}
	owner := uint64(11)
	err := a.PlaceMarketOrder(context.Background(), service.PlaceMarketInput{
		OwnerType: model.OwnerClient,
		OwnerID:   &owner,
		ListingID: 4,
		AccountID: 6,
		Side:      "buy",
		Quantity:  2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.call != 1 {
		t.Fatalf("CreateOrder calls: got %d want 1", stub.call)
	}
	if stub.got.ListingID != 4 || stub.got.UserID != 11 || stub.got.OrderType != "market" {
		t.Errorf("forwarded request mismatch: %+v", stub.got)
	}

	stub.err = errors.New("insufficient funds")
	if err := a.PlaceMarketOrder(context.Background(), service.PlaceMarketInput{
		OwnerType: model.OwnerBank,
		ListingID: 1,
		AccountID: 1,
		Side:      "buy",
		Quantity:  1,
	}); err == nil {
		t.Fatal("expected CreateOrder error to propagate")
	}
}
