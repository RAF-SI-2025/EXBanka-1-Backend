// Package main — adapter glue wiring stock-service repositories into
// the narrow interfaces consumed by OTCStockService.
//
// OTCStockServiceListingResolver wants two leaf methods (currency +
// ticker/name/stock_id by listing_id). The actual data lives across
// three repos (Listing → Stock → StockExchange), so we hide the
// chained lookup behind this adapter and pass it as the service's
// listing-resolver dependency.
//
// OTCStockAccountClient wraps the existing grpc.AccountClient with a
// GetAccount helper (the raw accountpb stub takes a request struct;
// the service's narrow interface takes a uint64 for terseness).
package main

import (
	"context"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	stockgrpc "github.com/exbanka/stock-service/internal/grpc"
	"github.com/exbanka/stock-service/internal/repository"
)

// stockAccountClientAdapter wraps grpc.AccountClient and adds the
// terse GetAccount(uint64) variant the OTCStockService consumes.
type stockAccountClientAdapter struct {
	wrapped *stockgrpc.AccountClient
}

func newStockAccountClientAdapter(c *stockgrpc.AccountClient) *stockAccountClientAdapter {
	return &stockAccountClientAdapter{wrapped: c}
}

func (a *stockAccountClientAdapter) ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode, idempotencyKey string) (*accountpb.ReserveFundsResponse, error) {
	return a.wrapped.ReserveFunds(ctx, accountID, orderID, amount, currencyCode, idempotencyKey)
}

func (a *stockAccountClientAdapter) ReleaseReservation(ctx context.Context, orderID uint64, idempotencyKey string) (*accountpb.ReleaseReservationResponse, error) {
	return a.wrapped.ReleaseReservation(ctx, orderID, idempotencyKey)
}

func (a *stockAccountClientAdapter) GetAccount(ctx context.Context, accountID uint64) (*accountpb.AccountResponse, error) {
	return a.wrapped.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: accountID})
}

type stockListingResolverAdapter struct {
	listings  *repository.ListingRepository
	stocks    *repository.StockRepository
	exchanges *repository.ExchangeRepository
}

// GetListingCurrency: Listing → ExchangeID → StockExchange.Currency.
func (a *stockListingResolverAdapter) GetListingCurrency(listingID uint64) (string, error) {
	l, err := a.listings.GetByID(listingID)
	if err != nil {
		return "", err
	}
	ex, err := a.exchanges.GetByID(l.ExchangeID)
	if err != nil {
		return "", err
	}
	return ex.Currency, nil
}

// GetListingTickerAndName: Listing → SecurityID → Stock.Ticker/Name.
// (Returns the stock ID too so OTCStockBuyOffer.StockID can be populated
// without a second lookup.) Only `security_type == "stock"` is supported
// here — futures/forex/options listings reject at the service-level
// validation before this is called.
func (a *stockListingResolverAdapter) GetListingTickerAndName(listingID uint64) (string, string, uint64, error) {
	l, err := a.listings.GetByID(listingID)
	if err != nil {
		return "", "", 0, err
	}
	s, err := a.stocks.GetByID(l.SecurityID)
	if err != nil {
		return "", "", 0, err
	}
	return s.Ticker, s.Name, s.ID, nil
}

// optionCurrencyResolverAdapter implements otccache.OptionCurrencyResolver
// AND handler.OptionCurrencyResolver by looking up the stock's listing →
// exchange → currency. The two interfaces share the same single-method
// shape (CurrencyForStock(stockID) (string, error)) so one adapter
// satisfies both.
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

// CurrencyForStock: Stock (lookup) → Listing.GetBySecurityIDAndType
// → ExchangeID → StockExchange.Currency. Falls back to "" if any
// lookup fails so the caller can default to "USD".
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
