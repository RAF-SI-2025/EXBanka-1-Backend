package service

import (
	"time"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type StockRepo interface {
	Create(stock *model.Stock) error
	GetByID(id uint64) (*model.Stock, error)
	GetByTicker(ticker string) (*model.Stock, error)
	Update(stock *model.Stock) error
	UpsertByTicker(stock *model.Stock) error
	List(filter repository.StockFilter) ([]model.Stock, int64, error)
}

type FuturesRepo interface {
	Create(f *model.FuturesContract) error
	GetByID(id uint64) (*model.FuturesContract, error)
	GetByTicker(ticker string) (*model.FuturesContract, error)
	Update(f *model.FuturesContract) error
	UpsertByTicker(f *model.FuturesContract) error
	List(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error)
}

type ForexPairRepo interface {
	Create(fp *model.ForexPair) error
	GetByID(id uint64) (*model.ForexPair, error)
	GetByTicker(ticker string) (*model.ForexPair, error)
	Update(fp *model.ForexPair) error
	UpsertByTicker(fp *model.ForexPair) error
	List(filter repository.ForexFilter) ([]model.ForexPair, int64, error)
}

type OptionRepo interface {
	Create(o *model.Option) error
	GetByID(id uint64) (*model.Option, error)
	GetByTicker(ticker string) (*model.Option, error)
	Update(o *model.Option) error
	UpsertByTicker(o *model.Option) error
	List(filter repository.OptionFilter) ([]model.Option, int64, error)
	DeleteExpiredBefore(cutoff time.Time) (int64, error)
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
	Update(listing *model.Listing) error
	UpsertBySecurity(listing *model.Listing) error
	ListAll() ([]model.Listing, error)
	ListBySecurityType(securityType string) ([]model.Listing, error)
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
	ListByUser(userID uint64, filter repository.OrderFilter) ([]model.Order, int64, error)
	ListAll(filter repository.OrderFilter) ([]model.Order, int64, error)
	ListActiveApproved() ([]model.Order, error)
}

type OrderTransactionRepo interface {
	Create(tx *model.OrderTransaction) error
	ListByOrderID(orderID uint64) ([]model.OrderTransaction, error)
}
