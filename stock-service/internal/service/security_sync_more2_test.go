package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestGenerateAllOptions_NoListingSkipped covers the branch where a stock has no
// listing row, so option generation is skipped for it.
func TestGenerateAllOptions_NoListingSkipped(t *testing.T) {
	stockRepo := &syncMockStockRepo{stocks: []model.Stock{{ID: 1, Ticker: "AAPL", Name: "Apple", Price: decimal.NewFromInt(180)}}}
	listingRepo := newSyncMockListingRepo() // intentionally empty → FindByStock finds nothing
	optionRepo := newSyncMockOptionRepo()
	listingSvc := NewListingService(listingRepo, nil, stockRepo, nil, nil)

	svc := NewSecuritySyncService(
		stockRepo, nil, nil, optionRepo, nil, &syncMockSettingRepo{},
		listingSvc, nil, nil, nil, nil, &stubSource{name: "generated"}, nil,
	)
	svc.GenerateAllOptionsForTest() // must not panic; the stock is skipped (no listing)

	// No options were generated for the listing-less stock.
	if len(optionRepo.options) != 0 {
		t.Errorf("expected no options when the stock has no listing, got %d", len(optionRepo.options))
	}
}

// TestSwitchSource_SimulatorAsyncReseed covers the asynchronous reseed path
// (non-"generated" source): the switch returns immediately and the background
// goroutine drives status back to idle.
func TestSwitchSource_SimulatorAsyncReseed(t *testing.T) {
	svc := buildSwitchSvc(t, &syncMockSettingRepo{}, &fakeWiper{}, &stubSource{name: "external"})
	if err := svc.SwitchSource(context.Background(), &stubSource{name: "simulator"}); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// Poll until the async reseed completes (status returns to idle).
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _, _, src := svc.GetStatus()
		if status == "idle" && src == "simulator" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("async reseed did not reach idle: status=%s src=%s", status, src)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
