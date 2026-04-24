package source

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// Source is the abstraction over how stock-service acquires securities data.
// At any moment there is exactly one active Source. Switching replaces the
// reference under a mutex owned by the sync service.
type Source interface {
	// Name returns the canonical identifier used in system_settings and the
	// admin switch endpoint. One of: "external", "generated", "simulator".
	Name() string

	// FetchExchanges returns the full list of exchanges the source exposes.
	FetchExchanges(ctx context.Context) ([]model.StockExchange, error)

	// FetchStocks returns stocks with their initial listing data.
	FetchStocks(ctx context.Context) ([]StockWithListing, error)

	// FetchFutures returns futures contracts with their initial listing data.
	FetchFutures(ctx context.Context) ([]FuturesWithListing, error)

	// FetchForex returns forex pairs with their initial listing data.
	FetchForex(ctx context.Context) ([]ForexWithListing, error)

	// FetchOptions returns options for a single underlying stock. The caller
	// passes the already-persisted stock so implementations have the ID and
	// price available.
	FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error)

	// RefreshPrices updates price fields on already-seeded securities. This
	// is called by the sync service's background loop. Implementations that
	// do not need periodic refresh may return nil.
	RefreshPrices(ctx context.Context) error
}

// StockWithListing pairs a stock model with its initial listing attributes
// so the caller can persist a securities row and its listing row together.
type StockWithListing struct {
	Stock       model.Stock
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}

// FuturesWithListing pairs a futures contract with its initial listing attributes.
type FuturesWithListing struct {
	Futures     model.FuturesContract
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}

// ForexWithListing pairs a forex pair with its initial listing attributes.
type ForexWithListing struct {
	Forex       model.ForexPair
	ExchangeID  uint64
	Price       decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      int64
	LastRefresh time.Time
}
