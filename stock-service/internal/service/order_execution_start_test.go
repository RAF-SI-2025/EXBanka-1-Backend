package service

import (
	"context"
	"testing"
)

// TestEngine_Start_NoActiveOrders covers the success branch where there are
// no orders to start.
func TestEngine_Start_NoActiveOrders(t *testing.T) {
	engine := NewOrderExecutionEngine(
		context.Background(),
		&fakeBaseCtxOrderRepo{},
		&fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{},
		&fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
	engine.Start(context.Background())
}

// TestEngine_StopOrderExecution_NoOpForUnknownOrder covers the no-op branch
// where the order id is not active.
func TestEngine_StopOrderExecution_NoOpForUnknownOrder(t *testing.T) {
	engine := NewOrderExecutionEngine(
		context.Background(),
		&fakeBaseCtxOrderRepo{},
		&fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{},
		&fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
	engine.StopOrderExecution(9999) // no-op
}
