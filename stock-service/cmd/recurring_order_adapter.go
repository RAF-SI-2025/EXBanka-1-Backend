// Package main — adapter wiring *service.OrderService into the narrow
// orderPlacer interface the recurring-order cron consumes.
//
// The cron hands the placer a service.PlaceMarketInput (owner_type/owner_id +
// listing/account/side/quantity); OrderService.CreateOrder wants the richer
// CreateOrderRequest (legacy user_id/system_type pair + order_type). This
// adapter reshapes the former into the latter as a Market order so a
// recurring template materialises into a real placement saga on each tick.
package main

import (
	"context"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// orderCreator is the subset of *service.OrderService the recurring-order
// placer needs. Narrowed to keep the adapter unit-testable without a fully
// wired OrderService.
type orderCreator interface {
	CreateOrder(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error)
}

// recurringOrderPlacerAdapter satisfies the cron's orderPlacer interface by
// translating each tick into a Market CreateOrder call.
type recurringOrderPlacerAdapter struct {
	orders orderCreator
}

func newRecurringOrderPlacerAdapter(orders orderCreator) *recurringOrderPlacerAdapter {
	return &recurringOrderPlacerAdapter{orders: orders}
}

func (a *recurringOrderPlacerAdapter) PlaceMarketOrder(ctx context.Context, in service.PlaceMarketInput) error {
	_, err := a.orders.CreateOrder(ctx, recurringInputToCreateOrder(in))
	return err
}

// recurringInputToCreateOrder maps the cron's owner_type/owner_id shape back
// onto the legacy (user_id, system_type) pair CreateOrder still consumes:
// bank owners carry system_type="bank" and no user id; client owners carry
// system_type="client" and the client id. Always a Market order.
func recurringInputToCreateOrder(in service.PlaceMarketInput) service.CreateOrderRequest {
	req := service.CreateOrderRequest{
		ListingID: in.ListingID,
		Direction: in.Side,
		OrderType: "market",
		Quantity:  in.Quantity,
		AccountID: in.AccountID,
	}
	if in.OwnerType == model.OwnerBank {
		req.SystemType = string(model.OwnerBank)
	} else {
		req.SystemType = string(model.OwnerClient)
		req.UserID = model.OwnerIDOrZero(in.OwnerID)
	}
	return req
}
