package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// abortFillHandler always fails the fill and records release calls, letting
// us exercise the engine's "persistent failure → abort + release" path.
type abortFillHandler struct {
	mu       sync.Mutex
	buyCalls int
	released int
}

func (h *abortFillHandler) ProcessBuyFill(_ *model.Order, _ *model.OrderTransaction) error {
	h.mu.Lock()
	h.buyCalls++
	h.mu.Unlock()
	return errors.New("settlement would exceed reservation")
}
func (h *abortFillHandler) ProcessSellFill(_ *model.Order, _ *model.OrderTransaction) error {
	return errors.New("boom")
}
func (h *abortFillHandler) ReleaseResidualReservation(_ context.Context, _ uint64) error {
	h.mu.Lock()
	h.released++
	h.mu.Unlock()
	return nil
}

// abortTxRepo records Create/Delete so the test can assert that each failed
// fill attempt's speculative transaction row is cleaned up (no phantom txns).
type abortTxRepo struct {
	mu      sync.Mutex
	creates int
	deletes int
	nextID  uint64
}

func (r *abortTxRepo) Create(tx *model.OrderTransaction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creates++
	r.nextID++
	tx.ID = r.nextID
	return nil
}
func (r *abortTxRepo) GetByID(_ uint64) (*model.OrderTransaction, error) {
	return nil, errors.New("not found")
}
func (r *abortTxRepo) Update(_ *model.OrderTransaction) error { return nil }
func (r *abortTxRepo) Delete(_ uint64) error {
	r.mu.Lock()
	r.deletes++
	r.mu.Unlock()
	return nil
}
func (r *abortTxRepo) ListByOrderID(_ uint64) ([]model.OrderTransaction, error) {
	return nil, nil
}

// TestEngine_ExecuteOrder_PersistentFillFailure_Aborts verifies that when the
// fill keeps failing, the engine stops after maxConsecutiveFillFailures rather
// than looping forever: it cleans up each failed attempt's transaction row,
// releases the reservation, and marks the order cancelled+done.
func TestEngine_ExecuteOrder_PersistentFillFailure_Aborts(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uid := uint64(7)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID: 42, Status: "approved", IsDone: false,
		Direction: "buy", RemainingPortions: 3, Quantity: 3,
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 7, OrderType: "market", ContractSize: 1,
		AfterHours: false,
	}}
	// Huge volume → calculateWaitTime floors to ~0-1s per iteration, keeping
	// the test fast even across maxConsecutiveFillFailures attempts.
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID: 7, Volume: 1_000_000_000, Price: decimal.NewFromInt(100),
		High: decimal.NewFromInt(100), Low: decimal.NewFromInt(100),
	}}
	fill := &abortFillHandler{}
	txRepo := &abortTxRepo{}

	engine := NewOrderExecutionEngine(
		baseCtx, orderRepo, txRepo, listingRepo,
		&fakeBaseCtxSettingRepo{}, fakeBaseCtxPublisher{}, fill,
	)

	engine.executeOrder(baseCtx, 42)

	if orderRepo.order.Status != "cancelled" {
		t.Errorf("want status cancelled after abort, got %q", orderRepo.order.Status)
	}
	if !orderRepo.order.IsDone {
		t.Error("want IsDone=true after abort")
	}
	if fill.buyCalls != maxConsecutiveFillFailures {
		t.Errorf("want exactly %d fill attempts, got %d", maxConsecutiveFillFailures, fill.buyCalls)
	}
	if txRepo.creates != txRepo.deletes {
		t.Errorf("every failed-attempt txn must be deleted: creates=%d deletes=%d", txRepo.creates, txRepo.deletes)
	}
	if fill.released == 0 {
		t.Error("want the reservation released on abort")
	}
}
