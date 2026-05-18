package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/source"
)

// TestSecuritySyncService_StartPeriodicRefresh_Cancels exercises the periodic
// refresh goroutine lifecycle. Cancels ctx immediately so we don't wait for
// the first tick.
func TestSecuritySyncService_StartPeriodicRefresh_Cancels(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	ctx, cancel := context.WithCancel(context.Background())
	svc.StartPeriodicRefresh(ctx, 5)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestSecuritySyncService_StartPeriodicRefresh_DefaultsInterval covers the
// intervalMins<=0 fallback branch.
func TestSecuritySyncService_StartPeriodicRefresh_DefaultsInterval(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	ctx, cancel := context.WithCancel(context.Background())
	svc.StartPeriodicRefresh(ctx, 0)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestSecuritySyncService_StartSimulatorRefreshLoopIfActive_NotSimulator covers
// the no-op branch when the active source is not the simulator.
func TestSecuritySyncService_StartSimulatorRefreshLoopIfActive_NotSimulator(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	svc.StartSimulatorRefreshLoopIfActive() // no-op
}

// TestSecuritySyncService_SyncExchanges_WithStaticSource covers the success
// path: the static source returns no exchanges and the function logs OK.
func TestSecuritySyncService_SyncExchanges_StaticEmpty(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	svc.syncExchanges() // no-op
}

// TestSecuritySyncService_RefreshSimulatorPrices smokes the helper that drives
// the periodic simulator refresh tick. The stub source returns empty slices so
// no price-update calls land but the for-loops are exercised.
func TestSecuritySyncService_RefreshSimulatorPrices(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	svc.refreshSimulatorPrices(context.Background())
}

// TestSecuritySyncService_RefreshSimulatorPrices_WithData wires up a stub that
// returns one stock/futures/forex so the inner loops execute.
func TestSecuritySyncService_RefreshSimulatorPrices_WithData(t *testing.T) {
	src := &stubSource{
		name:    "stub",
		stocks:  []source.StockWithListing{{Stock: model.Stock{Ticker: "AAPL"}, Price: decimal.NewFromInt(150)}},
		futures: []source.FuturesWithListing{{Futures: model.FuturesContract{Ticker: "CLJ26"}, Price: decimal.NewFromInt(70)}},
		forex:   []source.ForexWithListing{{Forex: model.ForexPair{Ticker: "EURUSD"}, Price: decimal.NewFromFloat(1.1)}},
	}
	svc := newSyncServiceForRefreshTest(t, src)
	svc.refreshSimulatorPrices(context.Background())
}

// TestSecuritySyncService_SyncStockPrices_NoAVClient covers the early-exit
// branch when avClient is nil.
func TestSecuritySyncService_SyncStockPrices_NoAVClient(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	// avClient is nil in this fixture.
	svc.syncStockPrices(context.Background())
}

// TestSecuritySyncService_SyncStocks covers the main syncStocks loop with a
// stub source that returns one stock.
func TestSecuritySyncService_SyncStocks(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	svc.syncStocks(context.Background())
}

// TestSecuritySyncService_SeedFuturesAndForex covers the futures/forex seed loops.
func TestSecuritySyncService_SeedFuturesAndForex(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	svc.seedFutures("/dev/null")
	svc.seedForexPairs()
}

// TestSecuritySyncService_RefreshForexRates_NoFinnhub covers the no-client branch.
func TestSecuritySyncService_RefreshForexRates_NoFinnhub(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "stub"})
	// finnhubClient nil.
	svc.refreshForexRates()
}

// StartSimulatorRefreshLoopIfActive: when source is named "simulator" it
// kicks off a 3s loop; we cancel via SwitchSource which calls refreshCtx().
func TestSecuritySyncService_StartSimulatorRefreshLoopIfActive_SimulatorSource(t *testing.T) {
	svc := newSyncServiceForRefreshTest(t, &stubSource{name: "simulator"})
	svc.StartSimulatorRefreshLoopIfActive()
	// Stop the loop so the goroutine doesn't outlive the test.
	if svc.refreshCtx != nil {
		svc.refreshCtx()
	}
	time.Sleep(50 * time.Millisecond)
}
