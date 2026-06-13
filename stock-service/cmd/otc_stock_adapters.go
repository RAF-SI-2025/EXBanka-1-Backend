// Package main — adapter glue wiring stock-service repositories into the narrow
// interfaces consumed by the OTC OPTION paths: the exercise saga's stock-meta
// resolver (Name + ListingID for the buyer-credit holding upsert) and the
// cross-bank option cache's currency resolver.
package main

import (
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// otcStockMetaAdapter satisfies service.OTCStockMetaResolver — the OTC option
// exercise saga uses it to resolve Name + ListingID for the buyer-credit holding
// upsert. Without these fields, the new holding row is invisible/untradeable in
// the FE (no ticker lookup → can't place a sell order). See 2026-05-16 fix in
// otc_exercise_saga.go.
type otcStockMetaAdapter struct {
	stocks   *repository.StockRepository
	listings *repository.ListingRepository
}

func (a *otcStockMetaAdapter) GetStockByID(id uint64) (*model.Stock, error) {
	return a.stocks.GetByID(id)
}

func (a *otcStockMetaAdapter) GetListingBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	return a.listings.GetBySecurityIDAndType(securityID, securityType)
}

// optionCurrencyResolverAdapter implements otccache.OptionCurrencyResolver by
// looking up the stock's listing → exchange → currency.
type optionCurrencyResolverAdapter struct {
	listings  *repository.ListingRepository
	stocks    *repository.StockRepository
	exchanges *repository.ExchangeRepository
}

func newOptionCurrencyResolverAdapter(
	listings *repository.ListingRepository,
	stocks *repository.StockRepository,
	exchanges *repository.ExchangeRepository,
) *optionCurrencyResolverAdapter {
	return &optionCurrencyResolverAdapter{listings: listings, stocks: stocks, exchanges: exchanges}
}

// CurrencyForStock: Stock (lookup) → Listing.GetBySecurityIDAndType → ExchangeID
// → StockExchange.Currency. Falls back to "" if any lookup fails so the caller
// can default to "USD".
func (a *optionCurrencyResolverAdapter) CurrencyForStock(stockID uint64) (string, error) {
	listing, err := a.listings.GetBySecurityIDAndType(stockID, "stock")
	if err != nil {
		return "", err
	}
	if listing == nil {
		return "", nil
	}
	ex, err := a.exchanges.GetByID(listing.ExchangeID)
	if err != nil {
		return "", err
	}
	return ex.Currency, nil
}
