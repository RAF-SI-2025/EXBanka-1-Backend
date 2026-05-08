package service

import (
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestOrderService_ListMyOrders_PassThrough exercises the simple list wrapper.
func TestOrderService_ListMyOrders_PassThrough(t *testing.T) {
	fx := newOrderServiceFixture()
	uid := uint64(7)
	rows, total, err := fx.svc.ListMyOrders(model.OwnerClient, &uid, repository.OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("got %d/%d", total, len(rows))
	}
}

func TestOrderService_ListAllOrders_PassThrough(t *testing.T) {
	fx := newOrderServiceFixture()
	rows, total, err := fx.svc.ListAllOrders(repository.OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("got %d/%d", total, len(rows))
	}
}

// TestIsAfterHours covers the empty-close-time branch (returns false) and
// the main path (does not panic).
func TestOrderService_IsAfterHours_NoCloseTime(t *testing.T) {
	fx := newOrderServiceFixture()
	listing := &model.Listing{Exchange: model.StockExchange{}}
	got := fx.svc.isAfterHours(listing)
	if got {
		t.Errorf("expected false when CloseTime empty")
	}
}

func TestOrderService_IsAfterHours_WithExchange(t *testing.T) {
	fx := newOrderServiceFixture()
	listing := &model.Listing{Exchange: model.StockExchange{
		OpenTime: "09:30", CloseTime: "16:00", TimeZone: "-5",
	}}
	// Result depends on wall-clock; just exercise both branches.
	_ = fx.svc.isAfterHours(listing)
}

// TestNewOrderService_NilSettings exercises the settings=nil → defaults branch.
func TestNewOrderService_NilSettings(t *testing.T) {
	svc := NewOrderService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if svc == nil {
		t.Fatal("nil")
	}
}
