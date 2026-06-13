package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/source"
)

// erroringSource fails every fetch so the sync service's WARN-and-continue
// branches are exercised. Name "generated" routes RefreshPrices through the
// Source abstraction (not the external API path).
type erroringSource struct{ name string }

func (s *erroringSource) Name() string { return s.name }
func (s *erroringSource) FetchExchanges(context.Context) ([]model.StockExchange, error) {
	return nil, errors.New("exchanges down")
}
func (s *erroringSource) FetchStocks(context.Context) ([]source.StockWithListing, error) {
	return nil, errors.New("stocks down")
}
func (s *erroringSource) FetchFutures(context.Context) ([]source.FuturesWithListing, error) {
	return nil, errors.New("futures down")
}
func (s *erroringSource) FetchForex(context.Context) ([]source.ForexWithListing, error) {
	return nil, errors.New("forex down")
}
func (s *erroringSource) FetchOptions(context.Context, *model.Stock) ([]model.Option, error) {
	return nil, errors.New("options down")
}
func (s *erroringSource) RefreshPrices(context.Context) error { return errors.New("refresh down") }

func newErroringSyncSvc(t *testing.T, src source.Source) *SecuritySyncService {
	t.Helper()
	stockRepo := &syncMockStockRepo{}
	futuresRepo := &syncMockFuturesRepo{}
	forexRepo := &syncMockForexRepo{}
	listingRepo := newSyncMockListingRepo()
	listingSvc := NewListingService(listingRepo, nil, stockRepo, futuresRepo, forexRepo)
	return NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, newSyncMockOptionRepo(),
		nil, &syncMockSettingRepo{}, listingSvc, nil, nil, nil, nil, src, nil,
	)
}

func TestSecuritySync_SeedAll_AllFetchErrors(t *testing.T) {
	svc := newErroringSyncSvc(t, &erroringSource{name: "generated"})
	// Every fetch fails → each WARN-and-continue branch is exercised; no panic.
	svc.SeedAll(context.Background(), "")
}

func TestSecuritySync_RefreshPrices_GeneratedSourceErrors(t *testing.T) {
	svc := newErroringSyncSvc(t, &erroringSource{name: "generated"})
	// RefreshPrices logs the source-refresh failure then re-syncs (all error).
	svc.RefreshPrices(context.Background())
}

func TestSecuritySync_RefreshSimulatorPrices_FetchErrors(t *testing.T) {
	svc := newErroringSyncSvc(t, &erroringSource{name: "simulator"})
	svc.refreshSimulatorPrices(context.Background())
}

func TestSecuritySync_RefreshPrices_UnknownSource(t *testing.T) {
	svc := newErroringSyncSvc(t, &erroringSource{name: "weird"})
	// Unknown source name → the default WARN-and-skip branch.
	svc.RefreshPrices(context.Background())
}
