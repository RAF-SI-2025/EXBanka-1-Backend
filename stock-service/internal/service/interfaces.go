package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/source"
)

// SwitchableSyncService is the subset of SecuritySyncService that the admin
// handler depends on. Exposing it as an interface lets tests mock the
// orchestration without spinning up a real sync service.
type SwitchableSyncService interface {
	SwitchSource(ctx context.Context, newSource source.Source) error
	GetStatus() (status, lastErr string, startedAt time.Time, sourceName string)
}

type StockRepo interface {
	Create(stock *model.Stock) error
	GetByID(id uint64) (*model.Stock, error)
	GetByTicker(ticker string) (*model.Stock, error)
	Update(stock *model.Stock) error
	UpsertByTicker(stock *model.Stock) error
	List(filter repository.StockFilter) ([]model.Stock, int64, error)
	UpdatePriceByTicker(ticker string, price decimal.Decimal) error
}

type FuturesRepo interface {
	Create(f *model.FuturesContract) error
	GetByID(id uint64) (*model.FuturesContract, error)
	GetByTicker(ticker string) (*model.FuturesContract, error)
	Update(f *model.FuturesContract) error
	UpsertByTicker(f *model.FuturesContract) error
	List(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error)
	UpdatePriceByTicker(ticker string, price decimal.Decimal) error
}

type ForexPairRepo interface {
	Create(fp *model.ForexPair) error
	GetByID(id uint64) (*model.ForexPair, error)
	GetByTicker(ticker string) (*model.ForexPair, error)
	Update(fp *model.ForexPair) error
	UpsertByTicker(fp *model.ForexPair) error
	List(filter repository.ForexFilter) ([]model.ForexPair, int64, error)
	UpdatePriceByTicker(ticker string, rate decimal.Decimal) error
}

type OptionRepo interface {
	Create(o *model.Option) error
	GetByID(id uint64) (*model.Option, error)
	GetByTicker(ticker string) (*model.Option, error)
	Update(o *model.Option) error
	UpsertByTicker(o *model.Option) error
	List(filter repository.OptionFilter) ([]model.Option, int64, error)
	DeleteExpiredBefore(cutoff time.Time) (int64, error)
	SetListingID(optionID, listingID uint64) error
}

// ExchangeRepo is the exchange repository from Plan 2 (already defined).
// Re-declared here as an interface so security_service can depend on it.
type ExchangeRepo interface {
	GetByID(id uint64) (*model.StockExchange, error)
	GetByAcronym(acronym string) (*model.StockExchange, error)
	List(search string, page, pageSize int) ([]model.StockExchange, int64, error)
}

// SettingRepo is the system setting repository from Plan 2.
type SettingRepo interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

type ListingRepo interface {
	Create(listing *model.Listing) error
	GetByID(id uint64) (*model.Listing, error)
	GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error)
	ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error)
	Update(listing *model.Listing) error
	UpsertBySecurity(listing *model.Listing) error
	UpsertForOption(listing *model.Listing) (*model.Listing, error)
	ListAll() ([]model.Listing, error)
	ListBySecurityType(securityType string) ([]model.Listing, error)
	UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error
}

// Wiper is the interface for wiping all stock-service tables.
// A single-method interface so the sync service doesn't depend on the concrete WipeRepository.
type Wiper interface {
	WipeAll() error
}

type DailyPriceRepo interface {
	Create(info *model.ListingDailyPriceInfo) error
	UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error
	GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error)
}

type OrderRepo interface {
	Create(order *model.Order) error
	GetByID(id uint64) (*model.Order, error)
	Update(order *model.Order) error
	Delete(id uint64) error
	ListByUser(userID uint64, filter repository.OrderFilter) ([]model.Order, int64, error)
	ListAll(filter repository.OrderFilter) ([]model.Order, int64, error)
	ListActiveApproved() ([]model.Order, error)
}

type OrderTransactionRepo interface {
	Create(tx *model.OrderTransaction) error
	ListByOrderID(orderID uint64) ([]model.OrderTransaction, error)
}

// --- Portfolio ---

// Type aliases for filter/summary types defined in repository package.
type HoldingFilter = repository.HoldingFilter
type OTCFilter = repository.OTCFilter
type TaxFilter = repository.TaxFilter
type AccountGainSummary = repository.AccountGainSummary
type TaxUserSummary = repository.TaxUserSummary

type HoldingRepo interface {
	Upsert(holding *model.Holding) error // INSERT or UPDATE (weighted average)
	GetByID(id uint64) (*model.Holding, error)
	Update(holding *model.Holding) error
	Delete(id uint64) error
	GetByUserAndSecurity(userID uint64, securityType string, securityID uint64, accountID uint64) (*model.Holding, error)
	ListByUser(userID uint64, filter HoldingFilter) ([]model.Holding, int64, error)
	ListPublicOffers(filter OTCFilter) ([]model.Holding, int64, error)
	// FindOldestLongOptionHolding returns the oldest (by created_at) holding with
	// security_type="option", security_id=optionID, user_id=userID, quantity>0.
	// Returns (nil, nil) when no such holding exists.
	FindOldestLongOptionHolding(userID, optionID uint64) (*model.Holding, error)
}

// --- Tax ---

type CapitalGainRepo interface {
	Create(gain *model.CapitalGain) error
	ListByUser(userID uint64, page, pageSize int) ([]model.CapitalGain, int64, error)
	SumByUserMonth(userID uint64, year, month int) ([]AccountGainSummary, error) // grouped by account_id, currency
	SumByUserYear(userID uint64, year int) ([]AccountGainSummary, error)
}

type TaxCollectionRepo interface {
	Create(collection *model.TaxCollection) error
	SumByUserYear(userID uint64, year int) (decimal.Decimal, error) // total RSD collected
	SumByUserMonth(userID uint64, year, month int) (decimal.Decimal, error)
	GetLastCollection(userID uint64) (*model.TaxCollection, error)
	ListUsersWithGains(year, month int, filter TaxFilter) ([]TaxUserSummary, int64, error)
}

// --- Fill Handler (for order execution integration) ---

type FillHandler interface {
	ProcessBuyFill(order *model.Order, txn *model.OrderTransaction) error
	ProcessSellFill(order *model.Order, txn *model.OrderTransaction) error
}

// --- Name Resolver (for user name lookup) ---

type UserNameResolver func(userID uint64, systemType string) (firstName, lastName string, err error)
