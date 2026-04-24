package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// --- test doubles — define only what the baseCtx test needs ---

type fakeBaseCtxOrderRepo struct{ order *model.Order }

func (r *fakeBaseCtxOrderRepo) Create(_ *model.Order) error { return nil }
func (r *fakeBaseCtxOrderRepo) GetByID(id uint64) (*model.Order, error) {
	if r.order == nil || r.order.ID != id {
		return nil, errors.New("not found")
	}
	return r.order, nil
}
func (r *fakeBaseCtxOrderRepo) Update(o *model.Order) error {
	r.order = o
	return nil
}
func (r *fakeBaseCtxOrderRepo) Delete(_ uint64) error { return nil }
func (r *fakeBaseCtxOrderRepo) GetByIDWithOwner(id, userID uint64, systemType string) (*model.Order, error) {
	if r.order == nil || r.order.ID != id {
		return nil, errors.New("not found")
	}
	if r.order.UserID != userID || r.order.SystemType != systemType {
		return nil, errors.New("not found")
	}
	return r.order, nil
}
func (r *fakeBaseCtxOrderRepo) ListByUser(_ uint64, _ string, _ repository.OrderFilter) ([]model.Order, int64, error) {
	return nil, 0, nil
}
func (r *fakeBaseCtxOrderRepo) ListAll(_ repository.OrderFilter) ([]model.Order, int64, error) {
	return nil, 0, nil
}
func (r *fakeBaseCtxOrderRepo) ListActiveApproved() ([]model.Order, error) { return nil, nil }

type fakeBaseCtxTxRepo struct{ created chan uint64 }

func (r *fakeBaseCtxTxRepo) Create(txn *model.OrderTransaction) error {
	select {
	case r.created <- txn.OrderID:
	default:
	}
	return nil
}
func (r *fakeBaseCtxTxRepo) Update(_ *model.OrderTransaction) error { return nil }
func (r *fakeBaseCtxTxRepo) ListByOrderID(_ uint64) ([]model.OrderTransaction, error) {
	return nil, nil
}

type fakeBaseCtxListingRepo struct{ l *model.Listing }

func (r *fakeBaseCtxListingRepo) Create(_ *model.Listing) error { return nil }
func (r *fakeBaseCtxListingRepo) GetByID(_ uint64) (*model.Listing, error) { return r.l, nil }
func (r *fakeBaseCtxListingRepo) GetBySecurityIDAndType(_ uint64, _ string) (*model.Listing, error) {
	return nil, nil
}
func (r *fakeBaseCtxListingRepo) ListBySecurityIDsAndType(_ []uint64, _ string) ([]model.Listing, error) {
	return nil, nil
}
func (r *fakeBaseCtxListingRepo) Update(_ *model.Listing) error          { return nil }
func (r *fakeBaseCtxListingRepo) UpsertBySecurity(_ *model.Listing) error { return nil }
func (r *fakeBaseCtxListingRepo) UpsertForOption(_ *model.Listing) (*model.Listing, error) {
	return nil, nil
}
func (r *fakeBaseCtxListingRepo) ListAll() ([]model.Listing, error) { return nil, nil }
func (r *fakeBaseCtxListingRepo) ListBySecurityType(_ string) ([]model.Listing, error) {
	return nil, nil
}
func (r *fakeBaseCtxListingRepo) UpdatePriceByTicker(_, _ string, _, _, _ decimal.Decimal) error {
	return nil
}

type fakeBaseCtxSettingRepo struct{}

func (r *fakeBaseCtxSettingRepo) Get(_ string) (string, error) { return "", nil }
func (r *fakeBaseCtxSettingRepo) Set(_, _ string) error        { return nil }

type fakeBaseCtxFillHandler struct{ filled chan struct{} }

func (h *fakeBaseCtxFillHandler) ProcessBuyFill(_ *model.Order, _ *model.OrderTransaction) error {
	// Signal once even if called multiple times.
	select {
	case <-h.filled:
	default:
		close(h.filled)
	}
	return nil
}
func (h *fakeBaseCtxFillHandler) ProcessSellFill(_ *model.Order, _ *model.OrderTransaction) error {
	select {
	case <-h.filled:
	default:
		close(h.filled)
	}
	return nil
}

func (h *fakeBaseCtxFillHandler) ReleaseResidualReservation(_ context.Context, _ uint64) error {
	return nil
}

type fakeBaseCtxPublisher struct{}

func (fakeBaseCtxPublisher) PublishOrderFilled(_ context.Context, _ interface{}) error { return nil }

// TestStartOrderExecution_UsesBaseCtx_NotRequestCtx is the regression test for bug #3.
// Before: StartOrderExecution derived its goroutine ctx from the caller's ctx (the
// gRPC request ctx), which was cancelled the moment CreateOrder returned. The
// goroutine exited on its first select without running a fill.
// After: the engine uses its own baseCtx so a cancelled request ctx has no effect.
func TestStartOrderExecution_UsesBaseCtx_NotRequestCtx(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                42,
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		RemainingPortions: 1,
		Quantity:          1,
		AllOrNone:         true,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         7,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     7,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &fakeBaseCtxFillHandler{filled: make(chan struct{})}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, fillHandler,
	)

	callerCtx, callerCancel := context.WithCancel(context.Background())
	callerCancel() // simulate the gRPC request returning.

	engine.StartOrderExecution(callerCtx, 42)

	select {
	case <-fillHandler.filled:
		// Good: fill ran even though caller ctx was cancelled.
	case <-time.After(3 * time.Second):
		t.Fatalf("order did not fill within 3s — goroutine likely exited because it used caller ctx")
	}
}
