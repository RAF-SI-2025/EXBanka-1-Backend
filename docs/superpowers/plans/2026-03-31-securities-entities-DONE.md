# Securities Entities Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all four security types (Stock, FuturesContract, ForexPair, Option) as database entities in `stock-service`, with external API data providers, seed data, and the `SecurityGRPCService` handler that the API gateway calls.

**Architecture:** All four security types live in `stock-service`. Each type has a model, repository, and service layer. External data comes from:
- **Stocks:** AlphaVantage API (Company Overview endpoint for outstanding_shares, dividend_yield; Quote endpoint for price data)
- **Futures:** Seed from local JSON file (dummy data, as recommended by the spec)
- **Forex pairs:** Generated from the 8 supported currencies (8×7=56 pairs), exchange rates from existing `exchange-service` via gRPC
- **Options:** Generated algorithmically from stock data (Black-Scholes approach 2 from spec)

On startup, the service syncs security data. A periodic refresh goroutine updates prices every 15 minutes (configurable). When testing mode is enabled, no external API calls are made — cached local data is used.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, `shopspring/decimal`, `net/http` (API clients)

**Depends on:**
- Plan 1 (API surface) — proto definitions for `SecurityGRPCService` and all message types
- Plan 2 (exchanges & actuaries) — `StockExchange` model, `SystemSettingRepository` (testing mode), `ExchangeService.IsExchangeOpen()`

---

## File Structure

### New files to create

```
stock-service/
├── internal/
│   ├── model/
│   │   ├── stock.go                     # Stock entity
│   │   ├── futures_contract.go          # FuturesContract entity
│   │   ├── forex_pair.go                # ForexPair entity
│   │   └── option.go                    # Option entity
│   ├── repository/
│   │   ├── stock_repository.go          # Stock CRUD + filtered list
│   │   ├── futures_repository.go        # FuturesContract CRUD + filtered list
│   │   ├── forex_pair_repository.go     # ForexPair CRUD + filtered list
│   │   └── option_repository.go         # Option CRUD + filtered list by stock
│   ├── service/
│   │   ├── interfaces.go                # All repository interfaces
│   │   ├── security_service.go          # Business logic for securities (computed fields, queries)
│   │   └── security_sync.go            # Data sync/seed orchestration
│   ├── provider/
│   │   ├── alphavantage.go              # AlphaVantage API client (stocks)
│   │   └── security_seed.go            # JSON seed data loader (futures)
│   └── handler/
│       └── security_handler.go          # SecurityGRPCService implementation
├── data/
│   └── futures_seed.json               # Seed futures contracts (dummy data)
```

### Files to modify

```
stock-service/cmd/main.go               # Wire security models, repos, services, handler; add AutoMigrate; add sync goroutine
stock-service/internal/config/config.go  # Add ALPHAVANTAGE_API_KEY, SECURITY_SYNC_INTERVAL_MINUTES env vars
```

---

## Task 1: Add config entries for securities sync

**Files:**
- Modify: `stock-service/internal/config/config.go`

- [ ] **Step 1: Add new config fields**

In `stock-service/internal/config/config.go`, add these fields to the `Config` struct and `Load()`:

```go
type Config struct {
	// ... existing fields from Plan 2 ...
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	DBSslmode    string
	GRPCAddr     string
	KafkaBrokers string
	UserGRPCAddr     string
	AccountGRPCAddr  string
	ExchangeGRPCAddr string
	ExchangeCSVPath  string
	// New fields for securities
	AlphaVantageAPIKey       string
	SecuritySyncIntervalMins int
}

func Load() *Config {
	syncMins := 15
	if v := os.Getenv("SECURITY_SYNC_INTERVAL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			syncMins = n
		}
	}
	return &Config{
		DBHost:                   getEnv("STOCK_DB_HOST", "localhost"),
		DBPort:                   getEnv("STOCK_DB_PORT", "5440"),
		DBUser:                   getEnv("STOCK_DB_USER", "postgres"),
		DBPassword:               getEnv("STOCK_DB_PASSWORD", "postgres"),
		DBName:                   getEnv("STOCK_DB_NAME", "stock_db"),
		DBSslmode:                getEnv("STOCK_DB_SSLMODE", "disable"),
		GRPCAddr:                 getEnv("STOCK_GRPC_ADDR", ":50060"),
		KafkaBrokers:             getEnv("KAFKA_BROKERS", "localhost:9092"),
		UserGRPCAddr:             getEnv("USER_GRPC_ADDR", "localhost:50052"),
		AccountGRPCAddr:          getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr:         getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		ExchangeCSVPath:          getEnv("EXCHANGE_CSV_PATH", "data/exchanges.csv"),
		AlphaVantageAPIKey:       getEnv("ALPHAVANTAGE_API_KEY", ""),
		SecuritySyncIntervalMins: syncMins,
	}
}
```

Add `"strconv"` to imports.

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./cmd
```

Expected: BUILD SUCCESS (no new dependencies yet).

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/config/config.go
git commit -m "feat(stock-service): add AlphaVantage and sync interval config"
```

---

## Task 2: Define Stock model

**Files:**
- Create: `stock-service/internal/model/stock.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Stock represents an equity security (e.g., AAPL, MSFT).
// One stock is listed on one exchange in our system.
type Stock struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker            string          `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name              string          `gorm:"not null" json:"name"`
	OutstandingShares int64           `gorm:"not null;default:0" json:"outstanding_shares"`
	DividendYield     decimal.Decimal `gorm:"type:numeric(10,6);not null;default:0" json:"dividend_yield"`
	ExchangeID        uint64          `gorm:"index;not null" json:"exchange_id"`
	Exchange          StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Current price data (updated periodically)
	Price   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"price"`
	High    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"high"`
	Low     decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"low"`
	Change  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"change"`
	Volume  int64           `gorm:"not null;default:0" json:"volume"`
	Version int64           `gorm:"not null;default:1" json:"-"`
	// LastRefresh is the timestamp of the last price data update.
	LastRefresh time.Time `json:"last_refresh"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Stock) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", s.Version)
	s.Version++
	return nil
}

// ContractSize for stocks is always 1.
func (s *Stock) ContractSize() int64 {
	return 1
}

// MaintenanceMargin for stocks = 50% of Price.
func (s *Stock) MaintenanceMargin() decimal.Decimal {
	return s.Price.Mul(decimal.NewFromFloat(0.5))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (s *Stock) InitialMarginCost() decimal.Decimal {
	return s.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}

// MarketCap = OutstandingShares * Price.
func (s *Stock) MarketCap() decimal.Decimal {
	return s.Price.Mul(decimal.NewFromInt(s.OutstandingShares))
}
```

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/model/stock.go
git commit -m "feat(stock-service): add Stock model with margin and market cap computed fields"
```

---

## Task 3: Define FuturesContract model

**Files:**
- Create: `stock-service/internal/model/futures_contract.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// FuturesContract represents a futures contract (e.g., CLJ26 — Crude Oil April 2026).
// Each futures is listed on exactly one exchange.
type FuturesContract struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker         string          `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name           string          `gorm:"not null" json:"name"`
	ContractSize   int64           `gorm:"not null" json:"contract_size"`
	ContractUnit   string          `gorm:"size:20;not null" json:"contract_unit"`
	SettlementDate time.Time       `gorm:"not null;index" json:"settlement_date"`
	ExchangeID     uint64          `gorm:"index;not null" json:"exchange_id"`
	Exchange       StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Current price data
	Price       decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"price"`
	High        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"high"`
	Low         decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"low"`
	Change      decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"change"`
	Volume      int64           `gorm:"not null;default:0" json:"volume"`
	Version     int64           `gorm:"not null;default:1" json:"-"`
	LastRefresh time.Time       `json:"last_refresh"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (f *FuturesContract) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", f.Version)
	f.Version++
	return nil
}

// MaintenanceMargin for futures = ContractSize * Price * 10%.
func (f *FuturesContract) MaintenanceMargin() decimal.Decimal {
	return decimal.NewFromInt(f.ContractSize).Mul(f.Price).Mul(decimal.NewFromFloat(0.1))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (f *FuturesContract) InitialMarginCost() decimal.Decimal {
	return f.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/futures_contract.go
git commit -m "feat(stock-service): add FuturesContract model with margin computed fields"
```

---

## Task 4: Define ForexPair model

**Files:**
- Create: `stock-service/internal/model/forex_pair.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ForexPair represents a currency pair (e.g., EUR/USD).
// We support 8 currencies → 56 pairs.
// ForexPairs cannot be "owned" — buying a forex pair executes an instant currency
// exchange between accounts.
type ForexPair struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker        string          `gorm:"uniqueIndex;size:10;not null" json:"ticker"`
	Name          string          `gorm:"not null" json:"name"`
	BaseCurrency  string          `gorm:"size:3;not null;index" json:"base_currency"`
	QuoteCurrency string          `gorm:"size:3;not null;index" json:"quote_currency"`
	ExchangeRate  decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"exchange_rate"`
	Liquidity     string          `gorm:"size:10;not null;default:'medium'" json:"liquidity"`
	ExchangeID    uint64          `gorm:"index;not null" json:"exchange_id"`
	Exchange      StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Price data (for listing — price = exchange_rate in quote currency)
	High    decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"high"`
	Low     decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"low"`
	Change  decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"change"`
	Volume  int64           `gorm:"not null;default:0" json:"volume"`
	Version int64           `gorm:"not null;default:1" json:"-"`
	LastRefresh time.Time   `json:"last_refresh"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func (fp *ForexPair) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", fp.Version)
	fp.Version++
	return nil
}

// ContractSize for forex is standardly 1000.
func (fp *ForexPair) ContractSizeValue() int64 {
	return 1000
}

// MaintenanceMargin = ContractSize * Price * 10%.
func (fp *ForexPair) MaintenanceMargin() decimal.Decimal {
	return decimal.NewFromInt(fp.ContractSizeValue()).Mul(fp.ExchangeRate).Mul(decimal.NewFromFloat(0.1))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (fp *ForexPair) InitialMarginCost() decimal.Decimal {
	return fp.MaintenanceMargin().Mul(decimal.NewFromFloat(1.1))
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/forex_pair.go
git commit -m "feat(stock-service): add ForexPair model with 1000 contract size and margin fields"
```

---

## Task 5: Define Option model

**Files:**
- Create: `stock-service/internal/model/option.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Option represents a stock option contract (call or put).
// Options are always linked to a Stock. Contract size = 100 shares per option.
type Option struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Ticker            string          `gorm:"uniqueIndex;size:30;not null" json:"ticker"`
	Name              string          `gorm:"not null" json:"name"`
	StockID           uint64          `gorm:"index;not null" json:"stock_id"`
	Stock             Stock           `gorm:"foreignKey:StockID" json:"-"`
	OptionType        string          `gorm:"size:4;not null" json:"option_type"` // "call" or "put"
	StrikePrice       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"strike_price"`
	ImpliedVolatility decimal.Decimal `gorm:"type:numeric(10,6);not null;default:1" json:"implied_volatility"`
	Premium           decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"premium"`
	OpenInterest      int64           `gorm:"not null;default:0" json:"open_interest"`
	SettlementDate    time.Time       `gorm:"not null;index" json:"settlement_date"`
	Version           int64           `gorm:"not null;default:1" json:"-"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func (o *Option) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", o.Version)
	o.Version++
	return nil
}

// ContractSize for options is always 100 shares.
func (o *Option) ContractSizeValue() int64 {
	return 100
}

// MaintenanceMargin = ContractSize * 50% * Stock Price.
// Requires the stock's current price to be passed in.
func (o *Option) MaintenanceMargin(stockPrice decimal.Decimal) decimal.Decimal {
	return decimal.NewFromInt(o.ContractSizeValue()).Mul(stockPrice).Mul(decimal.NewFromFloat(0.5))
}

// InitialMarginCost = MaintenanceMargin * 1.1.
func (o *Option) InitialMarginCost(stockPrice decimal.Decimal) decimal.Decimal {
	return o.MaintenanceMargin(stockPrice).Mul(decimal.NewFromFloat(1.1))
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/option.go
git commit -m "feat(stock-service): add Option model with call/put types and margin computation"
```

---

## Task 6: Define repository interfaces

**Files:**
- Create: `stock-service/internal/service/interfaces.go`

- [ ] **Step 1: Write the interfaces**

These interfaces live in the `service` package (following user-service pattern) so the service layer depends only on abstractions.

```go
package service

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type StockRepo interface {
	Create(stock *model.Stock) error
	GetByID(id uint64) (*model.Stock, error)
	GetByTicker(ticker string) (*model.Stock, error)
	Update(stock *model.Stock) error
	UpsertByTicker(stock *model.Stock) error
	List(filter StockFilter) ([]model.Stock, int64, error)
}

type StockFilter struct {
	Search          string
	ExchangeAcronym string
	MinPrice        *decimal.Decimal
	MaxPrice        *decimal.Decimal
	MinVolume       *int64
	MaxVolume       *int64
	SortBy          string // "price", "volume", "change", "margin"
	SortOrder       string // "asc", "desc"
	Page            int
	PageSize        int
}

type FuturesRepo interface {
	Create(f *model.FuturesContract) error
	GetByID(id uint64) (*model.FuturesContract, error)
	GetByTicker(ticker string) (*model.FuturesContract, error)
	Update(f *model.FuturesContract) error
	UpsertByTicker(f *model.FuturesContract) error
	List(filter FuturesFilter) ([]model.FuturesContract, int64, error)
}

type FuturesFilter struct {
	Search              string
	ExchangeAcronym     string
	MinPrice            *decimal.Decimal
	MaxPrice            *decimal.Decimal
	MinVolume           *int64
	MaxVolume           *int64
	SettlementDateFrom  *time.Time
	SettlementDateTo    *time.Time
	SortBy              string
	SortOrder           string
	Page                int
	PageSize            int
}

type ForexPairRepo interface {
	Create(fp *model.ForexPair) error
	GetByID(id uint64) (*model.ForexPair, error)
	GetByTicker(ticker string) (*model.ForexPair, error)
	Update(fp *model.ForexPair) error
	UpsertByTicker(fp *model.ForexPair) error
	List(filter ForexFilter) ([]model.ForexPair, int64, error)
}

type ForexFilter struct {
	Search        string
	BaseCurrency  string
	QuoteCurrency string
	Liquidity     string
	SortBy        string
	SortOrder     string
	Page          int
	PageSize      int
}

type OptionRepo interface {
	Create(o *model.Option) error
	GetByID(id uint64) (*model.Option, error)
	GetByTicker(ticker string) (*model.Option, error)
	Update(o *model.Option) error
	UpsertByTicker(o *model.Option) error
	List(filter OptionFilter) ([]model.Option, int64, error)
	DeleteExpiredBefore(cutoff time.Time) (int64, error)
}

type OptionFilter struct {
	StockID        *uint64
	OptionType     string // "call", "put", "" (both)
	SettlementDate *time.Time
	MinStrike      *decimal.Decimal
	MaxStrike      *decimal.Decimal
	Page           int
	PageSize       int
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
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/interfaces.go
git commit -m "feat(stock-service): add repository interfaces for all security types"
```

---

## Task 7: Implement Stock repository

**Files:**
- Create: `stock-service/internal/repository/stock_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type StockRepository struct {
	db *gorm.DB
}

func NewStockRepository(db *gorm.DB) *StockRepository {
	return &StockRepository{db: db}
}

func (r *StockRepository) Create(stock *model.Stock) error {
	return r.db.Create(stock).Error
}

func (r *StockRepository) GetByID(id uint64) (*model.Stock, error) {
	var stock model.Stock
	if err := r.db.Preload("Exchange").First(&stock, id).Error; err != nil {
		return nil, err
	}
	return &stock, nil
}

func (r *StockRepository) GetByTicker(ticker string) (*model.Stock, error) {
	var stock model.Stock
	if err := r.db.Where("ticker = ?", ticker).First(&stock).Error; err != nil {
		return nil, err
	}
	return &stock, nil
}

func (r *StockRepository) Update(stock *model.Stock) error {
	result := r.db.Save(stock)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *StockRepository) UpsertByTicker(stock *model.Stock) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Stock
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", stock.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(stock).Error
			}
			return err
		}
		existing.Name = stock.Name
		existing.OutstandingShares = stock.OutstandingShares
		existing.DividendYield = stock.DividendYield
		existing.ExchangeID = stock.ExchangeID
		existing.Price = stock.Price
		existing.High = stock.High
		existing.Low = stock.Low
		existing.Change = stock.Change
		existing.Volume = stock.Volume
		existing.LastRefresh = stock.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *StockRepository) List(filter service.StockFilter) ([]model.Stock, int64, error) {
	var stocks []model.Stock
	var total int64

	q := r.db.Model(&model.Stock{}).Joins("JOIN stock_exchanges ON stock_exchanges.id = stocks.exchange_id")

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("stocks.ticker ILIKE ? OR stocks.name ILIKE ?", like, like)
	}
	if filter.ExchangeAcronym != "" {
		q = q.Where("stock_exchanges.acronym ILIKE ?", filter.ExchangeAcronym+"%")
	}
	if filter.MinPrice != nil {
		q = q.Where("stocks.price >= ?", *filter.MinPrice)
	}
	if filter.MaxPrice != nil {
		q = q.Where("stocks.price <= ?", *filter.MaxPrice)
	}
	if filter.MinVolume != nil {
		q = q.Where("stocks.volume >= ?", *filter.MinVolume)
	}
	if filter.MaxVolume != nil {
		q = q.Where("stocks.volume <= ?", *filter.MaxVolume)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applySorting(q, "stocks", filter.SortBy, filter.SortOrder)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&stocks).Error; err != nil {
		return nil, 0, err
	}
	return stocks, total, nil
}
```

- [ ] **Step 2: Create shared repository helpers**

Create `stock-service/internal/repository/helpers.go`:

```go
package repository

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another transaction")

var allowedSortColumns = map[string]string{
	"price":  "price",
	"volume": "volume",
	"change": "change",
	"margin": "price", // margin is derived from price; sort by price as proxy
}

func applySorting(q *gorm.DB, table, sortBy, sortOrder string) *gorm.DB {
	col, ok := allowedSortColumns[sortBy]
	if !ok {
		col = "price"
	}
	dir := "ASC"
	if sortOrder == "desc" {
		dir = "DESC"
	}
	return q.Order(fmt.Sprintf("%s.%s %s", table, col, dir))
}

func applyPagination(q *gorm.DB, page, pageSize int) *gorm.DB {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return q.Offset((page - 1) * pageSize).Limit(pageSize)
}
```

- [ ] **Step 3: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/repository/stock_repository.go \
        stock-service/internal/repository/helpers.go
git commit -m "feat(stock-service): add StockRepository with filtered list, upsert, and shared helpers"
```

---

## Task 8: Implement FuturesContract repository

**Files:**
- Create: `stock-service/internal/repository/futures_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type FuturesRepository struct {
	db *gorm.DB
}

func NewFuturesRepository(db *gorm.DB) *FuturesRepository {
	return &FuturesRepository{db: db}
}

func (r *FuturesRepository) Create(f *model.FuturesContract) error {
	return r.db.Create(f).Error
}

func (r *FuturesRepository) GetByID(id uint64) (*model.FuturesContract, error) {
	var f model.FuturesContract
	if err := r.db.Preload("Exchange").First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FuturesRepository) GetByTicker(ticker string) (*model.FuturesContract, error) {
	var f model.FuturesContract
	if err := r.db.Where("ticker = ?", ticker).First(&f).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FuturesRepository) Update(f *model.FuturesContract) error {
	result := r.db.Save(f)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *FuturesRepository) UpsertByTicker(f *model.FuturesContract) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.FuturesContract
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", f.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(f).Error
			}
			return err
		}
		existing.Name = f.Name
		existing.ContractSize = f.ContractSize
		existing.ContractUnit = f.ContractUnit
		existing.SettlementDate = f.SettlementDate
		existing.ExchangeID = f.ExchangeID
		existing.Price = f.Price
		existing.High = f.High
		existing.Low = f.Low
		existing.Change = f.Change
		existing.Volume = f.Volume
		existing.LastRefresh = f.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *FuturesRepository) List(filter service.FuturesFilter) ([]model.FuturesContract, int64, error) {
	var futures []model.FuturesContract
	var total int64

	q := r.db.Model(&model.FuturesContract{}).
		Joins("JOIN stock_exchanges ON stock_exchanges.id = futures_contracts.exchange_id")

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("futures_contracts.ticker ILIKE ? OR futures_contracts.name ILIKE ?", like, like)
	}
	if filter.ExchangeAcronym != "" {
		q = q.Where("stock_exchanges.acronym ILIKE ?", filter.ExchangeAcronym+"%")
	}
	if filter.MinPrice != nil {
		q = q.Where("futures_contracts.price >= ?", *filter.MinPrice)
	}
	if filter.MaxPrice != nil {
		q = q.Where("futures_contracts.price <= ?", *filter.MaxPrice)
	}
	if filter.MinVolume != nil {
		q = q.Where("futures_contracts.volume >= ?", *filter.MinVolume)
	}
	if filter.MaxVolume != nil {
		q = q.Where("futures_contracts.volume <= ?", *filter.MaxVolume)
	}
	if filter.SettlementDateFrom != nil {
		q = q.Where("futures_contracts.settlement_date >= ?", *filter.SettlementDateFrom)
	}
	if filter.SettlementDateTo != nil {
		q = q.Where("futures_contracts.settlement_date <= ?", *filter.SettlementDateTo)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applySorting(q, "futures_contracts", filter.SortBy, filter.SortOrder)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&futures).Error; err != nil {
		return nil, 0, err
	}
	return futures, total, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/futures_repository.go
git commit -m "feat(stock-service): add FuturesRepository with settlement date filtering"
```

---

## Task 9: Implement ForexPair repository

**Files:**
- Create: `stock-service/internal/repository/forex_pair_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type ForexPairRepository struct {
	db *gorm.DB
}

func NewForexPairRepository(db *gorm.DB) *ForexPairRepository {
	return &ForexPairRepository{db: db}
}

func (r *ForexPairRepository) Create(fp *model.ForexPair) error {
	return r.db.Create(fp).Error
}

func (r *ForexPairRepository) GetByID(id uint64) (*model.ForexPair, error) {
	var fp model.ForexPair
	if err := r.db.Preload("Exchange").First(&fp, id).Error; err != nil {
		return nil, err
	}
	return &fp, nil
}

func (r *ForexPairRepository) GetByTicker(ticker string) (*model.ForexPair, error) {
	var fp model.ForexPair
	if err := r.db.Where("ticker = ?", ticker).First(&fp).Error; err != nil {
		return nil, err
	}
	return &fp, nil
}

func (r *ForexPairRepository) Update(fp *model.ForexPair) error {
	result := r.db.Save(fp)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *ForexPairRepository) UpsertByTicker(fp *model.ForexPair) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ForexPair
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", fp.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(fp).Error
			}
			return err
		}
		existing.Name = fp.Name
		existing.BaseCurrency = fp.BaseCurrency
		existing.QuoteCurrency = fp.QuoteCurrency
		existing.ExchangeRate = fp.ExchangeRate
		existing.Liquidity = fp.Liquidity
		existing.ExchangeID = fp.ExchangeID
		existing.High = fp.High
		existing.Low = fp.Low
		existing.Change = fp.Change
		existing.Volume = fp.Volume
		existing.LastRefresh = fp.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *ForexPairRepository) List(filter service.ForexFilter) ([]model.ForexPair, int64, error) {
	var pairs []model.ForexPair
	var total int64

	q := r.db.Model(&model.ForexPair{})

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("ticker ILIKE ? OR name ILIKE ?", like, like)
	}
	if filter.BaseCurrency != "" {
		q = q.Where("base_currency = ?", filter.BaseCurrency)
	}
	if filter.QuoteCurrency != "" {
		q = q.Where("quote_currency = ?", filter.QuoteCurrency)
	}
	if filter.Liquidity != "" {
		q = q.Where("liquidity = ?", filter.Liquidity)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	sortCol := "exchange_rate"
	if filter.SortBy == "volume" {
		sortCol = "volume"
	} else if filter.SortBy == "change" {
		sortCol = "change"
	}
	dir := "ASC"
	if filter.SortOrder == "desc" {
		dir = "DESC"
	}
	q = q.Order(sortCol + " " + dir)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&pairs).Error; err != nil {
		return nil, 0, err
	}
	return pairs, total, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/forex_pair_repository.go
git commit -m "feat(stock-service): add ForexPairRepository with currency filtering"
```

---

## Task 10: Implement Option repository

**Files:**
- Create: `stock-service/internal/repository/option_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type OptionRepository struct {
	db *gorm.DB
}

func NewOptionRepository(db *gorm.DB) *OptionRepository {
	return &OptionRepository{db: db}
}

func (r *OptionRepository) Create(o *model.Option) error {
	return r.db.Create(o).Error
}

func (r *OptionRepository) GetByID(id uint64) (*model.Option, error) {
	var o model.Option
	if err := r.db.Preload("Stock").First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OptionRepository) GetByTicker(ticker string) (*model.Option, error) {
	var o model.Option
	if err := r.db.Where("ticker = ?", ticker).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OptionRepository) Update(o *model.Option) error {
	result := r.db.Save(o)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *OptionRepository) UpsertByTicker(o *model.Option) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Option
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", o.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(o).Error
			}
			return err
		}
		existing.Name = o.Name
		existing.OptionType = o.OptionType
		existing.StrikePrice = o.StrikePrice
		existing.ImpliedVolatility = o.ImpliedVolatility
		existing.Premium = o.Premium
		existing.OpenInterest = o.OpenInterest
		existing.SettlementDate = o.SettlementDate
		return tx.Save(&existing).Error
	})
}

func (r *OptionRepository) List(filter service.OptionFilter) ([]model.Option, int64, error) {
	var options []model.Option
	var total int64

	q := r.db.Model(&model.Option{})

	if filter.StockID != nil {
		q = q.Where("stock_id = ?", *filter.StockID)
	}
	if filter.OptionType != "" {
		q = q.Where("option_type = ?", filter.OptionType)
	}
	if filter.SettlementDate != nil {
		q = q.Where("DATE(settlement_date) = DATE(?)", *filter.SettlementDate)
	}
	if filter.MinStrike != nil {
		q = q.Where("strike_price >= ?", *filter.MinStrike)
	}
	if filter.MaxStrike != nil {
		q = q.Where("strike_price <= ?", *filter.MaxStrike)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = q.Order("strike_price ASC, option_type ASC")
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Stock").Find(&options).Error; err != nil {
		return nil, 0, err
	}
	return options, total, nil
}

func (r *OptionRepository) DeleteExpiredBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("settlement_date < ?", cutoff).Delete(&model.Option{})
	return result.RowsAffected, result.Error
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/option_repository.go
git commit -m "feat(stock-service): add OptionRepository with stock-based filtering and expiry cleanup"
```

---

## Task 11: Create AlphaVantage API provider

**Files:**
- Create: `stock-service/internal/provider/alphavantage.go`

This provider fetches stock data from the AlphaVantage API. It supports two endpoints:
- **GLOBAL_QUOTE** — current price, high, low, change, volume
- **OVERVIEW** — outstanding shares, dividend yield, exchange

- [ ] **Step 1: Write the provider**

```go
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const alphaVantageBaseURL = "https://www.alphavantage.co/query"

type AlphaVantageClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewAlphaVantageClient(apiKey string) *AlphaVantageClient {
	return &AlphaVantageClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// QuoteData represents parsed price data from the GLOBAL_QUOTE endpoint.
type QuoteData struct {
	Price  decimal.Decimal
	High   decimal.Decimal
	Low    decimal.Decimal
	Change decimal.Decimal
	Volume int64
}

// OverviewData represents parsed company overview from the OVERVIEW endpoint.
type OverviewData struct {
	Name              string
	Exchange          string
	OutstandingShares int64
	DividendYield     decimal.Decimal
}

// FetchQuote retrieves real-time price data for a stock ticker.
func (c *AlphaVantageClient) FetchQuote(ticker string) (*QuoteData, error) {
	url := fmt.Sprintf("%s?function=GLOBAL_QUOTE&symbol=%s&apikey=%s",
		alphaVantageBaseURL, ticker, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch quote for %s: %w", ticker, err)
	}

	var raw struct {
		GlobalQuote struct {
			Price  string `json:"05. price"`
			High   string `json:"03. high"`
			Low    string `json:"04. low"`
			Change string `json:"09. change"`
			Volume string `json:"06. volume"`
		} `json:"Global Quote"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse quote for %s: %w", ticker, err)
	}

	q := raw.GlobalQuote
	price, _ := decimal.NewFromString(q.Price)
	high, _ := decimal.NewFromString(q.High)
	low, _ := decimal.NewFromString(q.Low)
	change, _ := decimal.NewFromString(q.Change)
	vol, _ := strconv.ParseInt(strings.TrimSpace(q.Volume), 10, 64)

	return &QuoteData{
		Price:  price,
		High:   high,
		Low:    low,
		Change: change,
		Volume: vol,
	}, nil
}

// FetchOverview retrieves company overview data for a stock ticker.
func (c *AlphaVantageClient) FetchOverview(ticker string) (*OverviewData, error) {
	url := fmt.Sprintf("%s?function=OVERVIEW&symbol=%s&apikey=%s",
		alphaVantageBaseURL, ticker, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch overview for %s: %w", ticker, err)
	}

	var raw struct {
		Name              string `json:"Name"`
		Exchange          string `json:"Exchange"`
		SharesOutstanding string `json:"SharesOutstanding"`
		DividendYield     string `json:"DividendYield"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse overview for %s: %w", ticker, err)
	}

	shares, _ := strconv.ParseInt(raw.SharesOutstanding, 10, 64)
	divYield, _ := decimal.NewFromString(raw.DividendYield)

	return &OverviewData{
		Name:              raw.Name,
		Exchange:          raw.Exchange,
		OutstandingShares: shares,
		DividendYield:     divYield,
	}, nil
}

// FetchStockData fetches both quote and overview, merging into a partial Stock model.
func (c *AlphaVantageClient) FetchStockData(ticker string) (*model.Stock, error) {
	quote, err := c.FetchQuote(ticker)
	if err != nil {
		return nil, err
	}
	overview, err := c.FetchOverview(ticker)
	if err != nil {
		return nil, err
	}

	return &model.Stock{
		Ticker:            ticker,
		Name:              overview.Name,
		OutstandingShares: overview.OutstandingShares,
		DividendYield:     overview.DividendYield,
		Price:             quote.Price,
		High:              quote.High,
		Low:               quote.Low,
		Change:            quote.Change,
		Volume:            quote.Volume,
		LastRefresh:       time.Now(),
	}, nil
}

func (c *AlphaVantageClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/provider/alphavantage.go
git commit -m "feat(stock-service): add AlphaVantage API client for stock quotes and overviews"
```

---

## Task 12: Create futures seed data loader

**Files:**
- Create: `stock-service/data/futures_seed.json`
- Create: `stock-service/internal/provider/security_seed.go`

Per the spec recommendation, futures data comes from dummy data rather than a live API. We store seed data as a JSON file.

- [ ] **Step 1: Create the seed JSON file**

Create `stock-service/data/futures_seed.json`:

```json
[
  {
    "ticker": "CLJ26",
    "name": "Crude Oil Futures Apr 2026",
    "contract_size": 1000,
    "contract_unit": "barrel",
    "settlement_date": "2026-04-30",
    "exchange_acronym": "NYMEX",
    "price": 72.50,
    "high": 73.20,
    "low": 71.80,
    "volume": 120000
  },
  {
    "ticker": "CLK26",
    "name": "Crude Oil Futures May 2026",
    "contract_size": 1000,
    "contract_unit": "barrel",
    "settlement_date": "2026-05-29",
    "exchange_acronym": "NYMEX",
    "price": 73.10,
    "high": 74.00,
    "low": 72.50,
    "volume": 95000
  },
  {
    "ticker": "GCJ26",
    "name": "Gold Futures Apr 2026",
    "contract_size": 100,
    "contract_unit": "troy_ounce",
    "settlement_date": "2026-04-28",
    "exchange_acronym": "CME",
    "price": 2045.30,
    "high": 2060.00,
    "low": 2030.00,
    "volume": 80000
  },
  {
    "ticker": "GCM26",
    "name": "Gold Futures Jun 2026",
    "contract_size": 100,
    "contract_unit": "troy_ounce",
    "settlement_date": "2026-06-26",
    "exchange_acronym": "CME",
    "price": 2055.80,
    "high": 2070.00,
    "low": 2040.00,
    "volume": 65000
  },
  {
    "ticker": "SIK26",
    "name": "Silver Futures May 2026",
    "contract_size": 5000,
    "contract_unit": "troy_ounce",
    "settlement_date": "2026-05-27",
    "exchange_acronym": "CME",
    "price": 24.85,
    "high": 25.10,
    "low": 24.50,
    "volume": 45000
  },
  {
    "ticker": "NGJ26",
    "name": "Natural Gas Futures Apr 2026",
    "contract_size": 10000,
    "contract_unit": "mmbtu",
    "settlement_date": "2026-04-28",
    "exchange_acronym": "NYMEX",
    "price": 3.45,
    "high": 3.55,
    "low": 3.35,
    "volume": 110000
  },
  {
    "ticker": "ZCK26",
    "name": "Corn Futures May 2026",
    "contract_size": 5000,
    "contract_unit": "bushel",
    "settlement_date": "2026-05-14",
    "exchange_acronym": "CBOT",
    "price": 4.52,
    "high": 4.60,
    "low": 4.45,
    "volume": 70000
  },
  {
    "ticker": "ZWN26",
    "name": "Wheat Futures Jul 2026",
    "contract_size": 5000,
    "contract_unit": "bushel",
    "settlement_date": "2026-07-14",
    "exchange_acronym": "CBOT",
    "price": 5.78,
    "high": 5.90,
    "low": 5.65,
    "volume": 55000
  },
  {
    "ticker": "ZSN26",
    "name": "Soybean Futures Jul 2026",
    "contract_size": 5000,
    "contract_unit": "bushel",
    "settlement_date": "2026-07-14",
    "exchange_acronym": "CBOT",
    "price": 12.35,
    "high": 12.50,
    "low": 12.20,
    "volume": 60000
  },
  {
    "ticker": "HGK26",
    "name": "Copper Futures May 2026",
    "contract_size": 25000,
    "contract_unit": "kilogram",
    "settlement_date": "2026-05-27",
    "exchange_acronym": "CME",
    "price": 4.12,
    "high": 4.20,
    "low": 4.05,
    "volume": 40000
  }
]
```

- [ ] **Step 2: Write the seed loader**

Create `stock-service/internal/provider/security_seed.go`:

```go
package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type futuresSeedEntry struct {
	Ticker          string  `json:"ticker"`
	Name            string  `json:"name"`
	ContractSize    int64   `json:"contract_size"`
	ContractUnit    string  `json:"contract_unit"`
	SettlementDate  string  `json:"settlement_date"`
	ExchangeAcronym string  `json:"exchange_acronym"`
	Price           float64 `json:"price"`
	High            float64 `json:"high"`
	Low             float64 `json:"low"`
	Volume          int64   `json:"volume"`
}

// LoadFuturesFromJSON reads futures seed data from a JSON file.
// Returns partial FuturesContract models (ExchangeID must be resolved by caller).
func LoadFuturesFromJSON(path string) ([]FuturesSeedRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read futures seed file: %w", err)
	}

	var entries []futuresSeedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse futures seed json: %w", err)
	}

	rows := make([]FuturesSeedRow, 0, len(entries))
	for _, e := range entries {
		settlement, err := time.Parse("2006-01-02", e.SettlementDate)
		if err != nil {
			return nil, fmt.Errorf("parse settlement date for %s: %w", e.Ticker, err)
		}
		rows = append(rows, FuturesSeedRow{
			Contract: model.FuturesContract{
				Ticker:         e.Ticker,
				Name:           e.Name,
				ContractSize:   e.ContractSize,
				ContractUnit:   e.ContractUnit,
				SettlementDate: settlement,
				Price:          decimal.NewFromFloat(e.Price),
				High:           decimal.NewFromFloat(e.High),
				Low:            decimal.NewFromFloat(e.Low),
				Change:         decimal.NewFromFloat(e.High - e.Low),
				Volume:         e.Volume,
				LastRefresh:    time.Now(),
			},
			ExchangeAcronym: e.ExchangeAcronym,
		})
	}
	return rows, nil
}

// FuturesSeedRow holds a partial FuturesContract plus the exchange acronym
// (to be resolved to an ExchangeID by the caller).
type FuturesSeedRow struct {
	Contract        model.FuturesContract
	ExchangeAcronym string
}

// DefaultStockTickers is the list of well-known tickers to sync from AlphaVantage.
// We sync these on startup and periodically. Additional tickers can be added later.
var DefaultStockTickers = []string{
	"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA",
	"META", "NVDA", "JPM", "V", "JNJ",
	"WMT", "PG", "MA", "UNH", "HD",
	"DIS", "NFLX", "ADBE", "CRM", "PYPL",
}
```

- [ ] **Step 3: Commit**

```bash
git add stock-service/data/futures_seed.json \
        stock-service/internal/provider/security_seed.go
git commit -m "feat(stock-service): add futures JSON seed data and stock ticker list"
```

---

## Task 13: Create SecurityService business logic

**Files:**
- Create: `stock-service/internal/service/security_service.go`

This is the main service that the gRPC handler calls. It orchestrates queries, applies computed fields, and delegates to repositories.

- [ ] **Step 1: Write the service**

```go
package service

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type SecurityService struct {
	stockRepo   StockRepo
	futuresRepo FuturesRepo
	forexRepo   ForexPairRepo
	optionRepo  OptionRepo
	exchangeRepo ExchangeRepo
}

func NewSecurityService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
) *SecurityService {
	return &SecurityService{
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
		optionRepo:   optionRepo,
		exchangeRepo: exchangeRepo,
	}
}

// --- Stocks ---

func (s *SecurityService) ListStocks(filter StockFilter) ([]model.Stock, int64, error) {
	return s.stockRepo.List(filter)
}

func (s *SecurityService) GetStock(id uint64) (*model.Stock, error) {
	stock, err := s.stockRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("stock not found")
		}
		return nil, err
	}
	return stock, nil
}

// GetStockWithOptions returns a stock with its associated options.
func (s *SecurityService) GetStockWithOptions(id uint64) (*model.Stock, []model.Option, error) {
	stock, err := s.GetStock(id)
	if err != nil {
		return nil, nil, err
	}

	stockID := stock.ID
	options, _, err := s.optionRepo.List(OptionFilter{
		StockID:  &stockID,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		return nil, nil, err
	}
	return stock, options, nil
}

// --- Futures ---

func (s *SecurityService) ListFutures(filter FuturesFilter) ([]model.FuturesContract, int64, error) {
	return s.futuresRepo.List(filter)
}

func (s *SecurityService) GetFutures(id uint64) (*model.FuturesContract, error) {
	f, err := s.futuresRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("futures contract not found")
		}
		return nil, err
	}
	return f, nil
}

// --- Forex ---

func (s *SecurityService) ListForexPairs(filter ForexFilter) ([]model.ForexPair, int64, error) {
	return s.forexRepo.List(filter)
}

func (s *SecurityService) GetForexPair(id uint64) (*model.ForexPair, error) {
	fp, err := s.forexRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("forex pair not found")
		}
		return nil, err
	}
	return fp, nil
}

// --- Options ---

func (s *SecurityService) ListOptions(filter OptionFilter) ([]model.Option, int64, error) {
	return s.optionRepo.List(filter)
}

func (s *SecurityService) GetOption(id uint64) (*model.Option, error) {
	o, err := s.optionRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("option not found")
		}
		return nil, err
	}
	return o, nil
}

// --- Computed Fields Helpers ---

// StockChangePercent = 100 * Change / (Price - Change).
func StockChangePercent(price, change decimal.Decimal) decimal.Decimal {
	base := price.Sub(change)
	if base.IsZero() {
		return decimal.Zero
	}
	return change.Mul(decimal.NewFromInt(100)).Div(base)
}

// GenerateOptionsForStock creates option contracts for a stock using the
// algorithmic approach (Approach 2 from spec):
// 1. Generate settlement dates: every 6 days for 30 days, then 6 more at 30-day intervals
// 2. Strike prices: 5 above and 5 below rounded stock price
// 3. For each date × strike: one CALL + one PUT
func GenerateOptionsForStock(stock *model.Stock) []model.Option {
	if stock.Price.IsZero() {
		return nil
	}

	now := time.Now()
	dates := generateSettlementDates(now)
	strikes := generateStrikePrices(stock.Price)

	var options []model.Option
	for _, date := range dates {
		for _, strike := range strikes {
			dateStr := date.Format("060102")
			strikeCents := strike.Mul(decimal.NewFromInt(100)).IntPart()

			// CALL option
			callTicker := fmt.Sprintf("%s%sC%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            callTicker,
				Name:              fmt.Sprintf("%s Call %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "call",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "call"),
				OpenInterest:      0,
				SettlementDate:    date,
			})

			// PUT option
			putTicker := fmt.Sprintf("%s%sP%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            putTicker,
				Name:              fmt.Sprintf("%s Put %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "put",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "put"),
				OpenInterest:      0,
				SettlementDate:    date,
			})
		}
	}
	return options
}

func generateSettlementDates(now time.Time) []time.Time {
	var dates []time.Time
	// Phase 1: every 6 days until 30 days out
	d := now.AddDate(0, 0, 6)
	for d.Sub(now).Hours()/24 <= 30 {
		dates = append(dates, d)
		d = d.AddDate(0, 0, 6)
	}
	// Phase 2: 6 more dates at 30-day intervals
	last := dates[len(dates)-1]
	for i := 0; i < 6; i++ {
		last = last.AddDate(0, 0, 30)
		dates = append(dates, last)
	}
	return dates
}

func generateStrikePrices(currentPrice decimal.Decimal) []decimal.Decimal {
	rounded := currentPrice.Round(0)
	var strikes []decimal.Decimal
	for i := -5; i <= 5; i++ {
		strikes = append(strikes, rounded.Add(decimal.NewFromInt(int64(i))))
	}
	return strikes
}

// estimatePremium is a simplified premium estimation.
// A proper Black-Scholes implementation could replace this.
func estimatePremium(stockPrice, strikePrice decimal.Decimal, settlementDate time.Time, optionType string) decimal.Decimal {
	daysToExpiry := decimal.NewFromFloat(time.Until(settlementDate).Hours() / 24)
	if daysToExpiry.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}

	// Intrinsic value
	var intrinsic decimal.Decimal
	if optionType == "call" {
		intrinsic = stockPrice.Sub(strikePrice)
	} else {
		intrinsic = strikePrice.Sub(stockPrice)
	}
	if intrinsic.IsNegative() {
		intrinsic = decimal.Zero
	}

	// Time value: rough approximation = stockPrice * 0.01 * sqrt(days/365)
	timeRatio := daysToExpiry.Div(decimal.NewFromInt(365))
	// Approximate sqrt via Babylonian method for one iteration
	sqrtApprox := timeRatio.Add(decimal.NewFromInt(1)).Div(decimal.NewFromInt(2))
	timeValue := stockPrice.Mul(decimal.NewFromFloat(0.01)).Mul(sqrtApprox)

	return intrinsic.Add(timeValue).Round(2)
}
```

Add `"fmt"` to imports (it's used in ticker generation).

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/service/security_service.go
git commit -m "feat(stock-service): add SecurityService with stock/futures/forex/option queries and option generation"
```

---

## Task 14: Create security data sync orchestrator

**Files:**
- Create: `stock-service/internal/service/security_sync.go`

This orchestrates the initial seed and periodic refresh. On startup:
1. Seed futures from JSON
2. Sync stocks from AlphaVantage (or skip if testing mode / no API key)
3. Generate forex pairs from supported currencies
4. Generate options from existing stocks

Periodic refresh updates prices only (does not re-create entities).

- [ ] **Step 1: Write the sync service**

```go
package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
)

// SupportedCurrencies matches the 8 currencies supported by exchange-service.
var SupportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

type SecuritySyncService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo ExchangeRepo
	settingRepo  SettingRepo
	avClient     *provider.AlphaVantageClient
}

func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
		optionRepo:   optionRepo,
		exchangeRepo: exchangeRepo,
		settingRepo:  settingRepo,
		avClient:     avClient,
	}
}

// SeedAll runs the full initial data seed.
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.seedFutures(futuresSeedPath)
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.generateAllOptions()
}

// RefreshPrices updates price data for all securities.
// Called periodically by the refresh goroutine.
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	// Futures: prices are static from seed data (no live API per spec recommendation)
	// Forex: rates could be refreshed from exchange-service, but that's handled by
	//        the exchange-service's own sync. We just re-read rates on demand.
	log.Println("price refresh complete")
}

// StartPeriodicRefresh launches a background goroutine that refreshes prices.
func (s *SecuritySyncService) StartPeriodicRefresh(ctx context.Context, intervalMins int) {
	if intervalMins <= 0 {
		intervalMins = 15
	}
	ticker := time.NewTicker(time.Duration(intervalMins) * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.RefreshPrices(ctx)
			case <-ctx.Done():
				log.Println("stopping periodic security price refresh")
				return
			}
		}
	}()
	log.Printf("periodic security price refresh started (every %d min)", intervalMins)
}

func (s *SecuritySyncService) isTestingMode() bool {
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

// --- Stocks ---

func (s *SecuritySyncService) syncStocks(ctx context.Context) {
	if s.avClient == nil {
		log.Println("WARN: no AlphaVantage API key — skipping stock sync, using seed data if available")
		s.seedDefaultStocks()
		return
	}

	for _, ticker := range provider.DefaultStockTickers {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stockData, err := s.avClient.FetchStockData(ticker)
		if err != nil {
			log.Printf("WARN: failed to fetch stock %s: %v", ticker, err)
			continue
		}

		// Resolve exchange
		exchangeAcronym := "NYSE" // default
		overview, err := s.avClient.FetchOverview(ticker)
		if err == nil && overview.Exchange != "" {
			exchangeAcronym = overview.Exchange
		}
		exchange, err := s.exchangeRepo.GetByAcronym(exchangeAcronym)
		if err != nil {
			log.Printf("WARN: exchange %s not found for stock %s, skipping", exchangeAcronym, ticker)
			continue
		}
		stockData.ExchangeID = exchange.ID

		if err := s.stockRepo.UpsertByTicker(stockData); err != nil {
			log.Printf("WARN: failed to upsert stock %s: %v", ticker, err)
		}

		// Rate limit: AlphaVantage free tier allows 5 calls/min
		time.Sleep(12 * time.Second)
	}
	log.Printf("synced %d stock tickers from AlphaVantage", len(provider.DefaultStockTickers))
}

func (s *SecuritySyncService) syncStockPrices(ctx context.Context) {
	if s.avClient == nil {
		return
	}

	stocks, _, err := s.stockRepo.List(StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for price refresh: %v", err)
		return
	}

	for _, stock := range stocks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		quote, err := s.avClient.FetchQuote(stock.Ticker)
		if err != nil {
			log.Printf("WARN: failed to refresh price for %s: %v", stock.Ticker, err)
			continue
		}

		stock.Price = quote.Price
		stock.High = quote.High
		stock.Low = quote.Low
		stock.Change = quote.Change
		stock.Volume = quote.Volume
		stock.LastRefresh = time.Now()

		if err := s.stockRepo.UpsertByTicker(&stock); err != nil {
			log.Printf("WARN: failed to update stock %s price: %v", stock.Ticker, err)
		}

		time.Sleep(12 * time.Second) // rate limit
	}
}

// seedDefaultStocks creates placeholder stocks when no API key is configured.
func (s *SecuritySyncService) seedDefaultStocks() {
	nyse, _ := s.exchangeRepo.GetByAcronym("NYSE")
	nasdaq, _ := s.exchangeRepo.GetByAcronym("NASDAQ")

	defaults := []model.Stock{
		{Ticker: "AAPL", Name: "Apple Inc.", OutstandingShares: 15000000000, DividendYield: decimal.NewFromFloat(0.005), Price: decimal.NewFromFloat(165.00), High: decimal.NewFromFloat(167.50), Low: decimal.NewFromFloat(163.20), Change: decimal.NewFromFloat(-2.30), Volume: 50000},
		{Ticker: "MSFT", Name: "Microsoft Corporation", OutstandingShares: 7400000000, DividendYield: decimal.NewFromFloat(0.008), Price: decimal.NewFromFloat(420.00), High: decimal.NewFromFloat(425.00), Low: decimal.NewFromFloat(418.00), Change: decimal.NewFromFloat(2.00), Volume: 35000},
		{Ticker: "GOOGL", Name: "Alphabet Inc.", OutstandingShares: 5900000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(175.00), High: decimal.NewFromFloat(177.00), Low: decimal.NewFromFloat(173.50), Change: decimal.NewFromFloat(1.50), Volume: 28000},
		{Ticker: "AMZN", Name: "Amazon.com Inc.", OutstandingShares: 10300000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(185.00), High: decimal.NewFromFloat(187.00), Low: decimal.NewFromFloat(183.00), Change: decimal.NewFromFloat(-1.00), Volume: 40000},
		{Ticker: "TSLA", Name: "Tesla Inc.", OutstandingShares: 3200000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(175.00), High: decimal.NewFromFloat(180.00), Low: decimal.NewFromFloat(170.00), Change: decimal.NewFromFloat(5.00), Volume: 60000},
		{Ticker: "META", Name: "Meta Platforms Inc.", OutstandingShares: 2570000000, DividendYield: decimal.NewFromFloat(0.004), Price: decimal.NewFromFloat(500.00), High: decimal.NewFromFloat(505.00), Low: decimal.NewFromFloat(495.00), Change: decimal.NewFromFloat(3.00), Volume: 25000},
		{Ticker: "NVDA", Name: "NVIDIA Corporation", OutstandingShares: 24500000000, DividendYield: decimal.NewFromFloat(0.0003), Price: decimal.NewFromFloat(950.00), High: decimal.NewFromFloat(960.00), Low: decimal.NewFromFloat(940.00), Change: decimal.NewFromFloat(10.00), Volume: 45000},
		{Ticker: "JPM", Name: "JPMorgan Chase & Co.", OutstandingShares: 2870000000, DividendYield: decimal.NewFromFloat(0.022), Price: decimal.NewFromFloat(200.00), High: decimal.NewFromFloat(202.00), Low: decimal.NewFromFloat(198.00), Change: decimal.NewFromFloat(1.00), Volume: 15000},
	}

	for i := range defaults {
		defaults[i].LastRefresh = time.Now()
		// Assign to NYSE for most, NASDAQ for tech names
		if nyse != nil {
			defaults[i].ExchangeID = nyse.ID
		}
		if nasdaq != nil && (defaults[i].Ticker == "AAPL" || defaults[i].Ticker == "MSFT" ||
			defaults[i].Ticker == "GOOGL" || defaults[i].Ticker == "AMZN" ||
			defaults[i].Ticker == "TSLA" || defaults[i].Ticker == "META" || defaults[i].Ticker == "NVDA") {
			defaults[i].ExchangeID = nasdaq.ID
		}
		if err := s.stockRepo.UpsertByTicker(&defaults[i]); err != nil {
			log.Printf("WARN: failed to seed stock %s: %v", defaults[i].Ticker, err)
		}
	}
	log.Printf("seeded %d default stocks", len(defaults))
}

// --- Futures ---

func (s *SecuritySyncService) seedFutures(seedPath string) {
	rows, err := provider.LoadFuturesFromJSON(seedPath)
	if err != nil {
		log.Printf("WARN: failed to load futures seed data: %v", err)
		return
	}

	for _, row := range rows {
		exchange, err := s.exchangeRepo.GetByAcronym(row.ExchangeAcronym)
		if err != nil {
			log.Printf("WARN: exchange %s not found for futures %s, skipping",
				row.ExchangeAcronym, row.Contract.Ticker)
			continue
		}
		row.Contract.ExchangeID = exchange.ID
		if err := s.futuresRepo.UpsertByTicker(&row.Contract); err != nil {
			log.Printf("WARN: failed to upsert futures %s: %v", row.Contract.Ticker, err)
		}
	}
	log.Printf("seeded %d futures contracts from JSON", len(rows))
}

// --- Forex Pairs ---

func (s *SecuritySyncService) seedForexPairs() {
	// Find the FOREX exchange. If none exists, use the first available exchange.
	forexExchange, err := s.exchangeRepo.GetByAcronym("FOREX")
	if err != nil {
		// Create a synthetic FOREX exchange entry
		log.Println("WARN: no FOREX exchange found — forex pairs will not have exchange association")
		return
	}

	count := 0
	for _, base := range SupportedCurrencies {
		for _, quote := range SupportedCurrencies {
			if base == quote {
				continue
			}
			ticker := fmt.Sprintf("%s/%s", base, quote)
			name := fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote))

			// Determine liquidity based on common pair popularity
			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}

			fp := &model.ForexPair{
				Ticker:        ticker,
				Name:          name,
				BaseCurrency:  base,
				QuoteCurrency: quote,
				ExchangeRate:  decimal.Zero, // will be updated from exchange-service rates
				Liquidity:     liquidity,
				ExchangeID:    forexExchange.ID,
				LastRefresh:   time.Now(),
			}

			if err := s.forexRepo.UpsertByTicker(fp); err != nil {
				log.Printf("WARN: failed to upsert forex pair %s: %v", ticker, err)
			}
			count++
		}
	}
	log.Printf("seeded %d forex pairs", count)
}

func currencyName(code string) string {
	names := map[string]string{
		"RSD": "Serbian Dinar", "EUR": "Euro", "CHF": "Swiss Franc",
		"USD": "US Dollar", "GBP": "British Pound", "JPY": "Japanese Yen",
		"CAD": "Canadian Dollar", "AUD": "Australian Dollar",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return code
}

func isMajorPair(base, quote string) bool {
	majors := map[string]bool{"EUR": true, "USD": true, "GBP": true, "JPY": true}
	return majors[base] && majors[quote]
}

func isExoticPair(base, quote string) bool {
	exotic := map[string]bool{"RSD": true}
	return exotic[base] || exotic[quote]
}

// --- Options ---

func (s *SecuritySyncService) generateAllOptions() {
	stocks, _, err := s.stockRepo.List(StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for option generation: %v", err)
		return
	}

	totalGenerated := 0
	for _, stock := range stocks {
		stock := stock
		options := GenerateOptionsForStock(&stock)
		for _, opt := range options {
			opt := opt
			if err := s.optionRepo.UpsertByTicker(&opt); err != nil {
				log.Printf("WARN: failed to upsert option %s: %v", opt.Ticker, err)
			}
		}
		totalGenerated += len(options)
	}

	// Clean up expired options
	deleted, err := s.optionRepo.DeleteExpiredBefore(time.Now())
	if err != nil {
		log.Printf("WARN: failed to clean expired options: %v", err)
	}

	log.Printf("generated %d options for %d stocks, cleaned %d expired", totalGenerated, len(stocks), deleted)
}
```

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/service/security_sync.go
git commit -m "feat(stock-service): add SecuritySyncService for startup seed and periodic price refresh"
```

---

## Task 15: Create SecurityGRPCService handler

**Files:**
- Create: `stock-service/internal/handler/security_handler.go`

**Depends on:** Plan 1, Task 2 (stock.proto compiled to `contract/stockpb`). Run `make proto` before this task if not done yet.

- [ ] **Step 1: Write the gRPC handler**

```go
package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type SecurityHandler struct {
	pb.UnimplementedSecurityGRPCServiceServer
	secSvc *service.SecurityService
}

func NewSecurityHandler(secSvc *service.SecurityService) *SecurityHandler {
	return &SecurityHandler{secSvc: secSvc}
}

// --- Stocks ---

func (h *SecurityHandler) ListStocks(ctx context.Context, req *pb.ListStocksRequest) (*pb.ListStocksResponse, error) {
	filter := service.StockFilter{
		Search:          req.Search,
		ExchangeAcronym: req.ExchangeAcronym,
		SortBy:          req.SortBy,
		SortOrder:       req.SortOrder,
		Page:            int(req.Page),
		PageSize:        int(req.PageSize),
	}
	if req.MinPrice != "" {
		v, err := decimal.NewFromString(req.MinPrice)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid min_price")
		}
		filter.MinPrice = &v
	}
	if req.MaxPrice != "" {
		v, err := decimal.NewFromString(req.MaxPrice)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid max_price")
		}
		filter.MaxPrice = &v
	}
	if req.MinVolume > 0 {
		v := req.MinVolume
		filter.MinVolume = &v
	}
	if req.MaxVolume > 0 {
		v := req.MaxVolume
		filter.MaxVolume = &v
	}

	stocks, total, err := h.secSvc.ListStocks(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	items := make([]*pb.StockItem, len(stocks))
	for i, s := range stocks {
		items[i] = toStockItem(&s)
	}
	return &pb.ListStocksResponse{Stocks: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetStock(ctx context.Context, req *pb.GetStockRequest) (*pb.StockDetail, error) {
	stock, options, err := h.secSvc.GetStockWithOptions(req.Id)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toStockDetail(stock, options), nil
}

func (h *SecurityHandler) GetStockHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	// Price history is implemented in Plan 4 (Listings).
	// Stub: return empty until Plan 4 is implemented.
	return &pb.PriceHistoryResponse{History: nil, TotalCount: 0}, nil
}

// --- Futures ---

func (h *SecurityHandler) ListFutures(ctx context.Context, req *pb.ListFuturesRequest) (*pb.ListFuturesResponse, error) {
	filter := service.FuturesFilter{
		Search:          req.Search,
		ExchangeAcronym: req.ExchangeAcronym,
		SortBy:          req.SortBy,
		SortOrder:       req.SortOrder,
		Page:            int(req.Page),
		PageSize:        int(req.PageSize),
	}
	if req.MinPrice != "" {
		v, _ := decimal.NewFromString(req.MinPrice)
		filter.MinPrice = &v
	}
	if req.MaxPrice != "" {
		v, _ := decimal.NewFromString(req.MaxPrice)
		filter.MaxPrice = &v
	}
	if req.MinVolume > 0 {
		v := req.MinVolume
		filter.MinVolume = &v
	}
	if req.MaxVolume > 0 {
		v := req.MaxVolume
		filter.MaxVolume = &v
	}
	if req.SettlementDateFrom != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDateFrom)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date_from")
		}
		filter.SettlementDateFrom = &t
	}
	if req.SettlementDateTo != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDateTo)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date_to")
		}
		filter.SettlementDateTo = &t
	}

	futures, total, err := h.secSvc.ListFutures(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	items := make([]*pb.FuturesItem, len(futures))
	for i, f := range futures {
		items[i] = toFuturesItem(&f)
	}
	return &pb.ListFuturesResponse{Futures: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetFutures(ctx context.Context, req *pb.GetFuturesRequest) (*pb.FuturesDetail, error) {
	f, err := h.secSvc.GetFutures(req.Id)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toFuturesDetail(f), nil
}

func (h *SecurityHandler) GetFuturesHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	// Price history is implemented in Plan 4 (Listings).
	return &pb.PriceHistoryResponse{History: nil, TotalCount: 0}, nil
}

// --- Forex ---

func (h *SecurityHandler) ListForexPairs(ctx context.Context, req *pb.ListForexPairsRequest) (*pb.ListForexPairsResponse, error) {
	filter := service.ForexFilter{
		Search:        req.Search,
		BaseCurrency:  req.BaseCurrency,
		QuoteCurrency: req.QuoteCurrency,
		Liquidity:     req.Liquidity,
		SortBy:        req.SortBy,
		SortOrder:     req.SortOrder,
		Page:          int(req.Page),
		PageSize:      int(req.PageSize),
	}

	pairs, total, err := h.secSvc.ListForexPairs(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	items := make([]*pb.ForexPairItem, len(pairs))
	for i, fp := range pairs {
		items[i] = toForexPairItem(&fp)
	}
	return &pb.ListForexPairsResponse{ForexPairs: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetForexPair(ctx context.Context, req *pb.GetForexPairRequest) (*pb.ForexPairDetail, error) {
	fp, err := h.secSvc.GetForexPair(req.Id)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toForexPairDetail(fp), nil
}

func (h *SecurityHandler) GetForexPairHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	// Price history is implemented in Plan 4 (Listings).
	return &pb.PriceHistoryResponse{History: nil, TotalCount: 0}, nil
}

// --- Options ---

func (h *SecurityHandler) ListOptions(ctx context.Context, req *pb.ListOptionsRequest) (*pb.ListOptionsResponse, error) {
	filter := service.OptionFilter{
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	if req.StockId > 0 {
		v := req.StockId
		filter.StockID = &v
	}
	if req.OptionType != "" {
		filter.OptionType = req.OptionType
	}
	if req.SettlementDate != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDate)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date")
		}
		filter.SettlementDate = &t
	}
	if req.MinStrike != "" {
		v, _ := decimal.NewFromString(req.MinStrike)
		filter.MinStrike = &v
	}
	if req.MaxStrike != "" {
		v, _ := decimal.NewFromString(req.MaxStrike)
		filter.MaxStrike = &v
	}

	options, total, err := h.secSvc.ListOptions(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	items := make([]*pb.OptionItem, len(options))
	for i, o := range options {
		items[i] = toOptionItem(&o)
	}
	return &pb.ListOptionsResponse{Options: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetOption(ctx context.Context, req *pb.GetOptionRequest) (*pb.OptionDetail, error) {
	o, err := h.secSvc.GetOption(req.Id)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toOptionDetail(o), nil
}

// --- Mapping helpers ---

func mapServiceError(err error) error {
	switch err.Error() {
	case "stock not found", "futures contract not found", "forex pair not found", "option not found":
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toListingInfo(exchangeID uint64, exchangeAcronym string, price, high, low, change decimal.Decimal, volume int64, initialMarginCost decimal.Decimal, lastRefresh time.Time) *pb.ListingInfo {
	changePercent := service.StockChangePercent(price, change)
	return &pb.ListingInfo{
		ExchangeId:        exchangeID,
		ExchangeAcronym:   exchangeAcronym,
		Price:             price.StringFixed(4),
		High:              high.StringFixed(4),
		Low:               low.StringFixed(4),
		Change:            change.StringFixed(4),
		ChangePercent:     changePercent.StringFixed(2),
		Volume:            volume,
		InitialMarginCost: initialMarginCost.StringFixed(2),
		LastRefresh:       lastRefresh.Format(time.RFC3339),
	}
}

func toStockItem(s *model.Stock) *pb.StockItem {
	return &pb.StockItem{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		Listing: toListingInfo(
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
	}
}

func toStockDetail(s *model.Stock, options []model.Option) *pb.StockDetail {
	optItems := make([]*pb.OptionItem, len(options))
	for i, o := range options {
		optItems[i] = toOptionItem(&o)
	}
	return &pb.StockDetail{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		MarketCap:         s.MarketCap().StringFixed(2),
		Listing: toListingInfo(
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
		Options: optItems,
	}
}

func toFuturesItem(f *model.FuturesContract) *pb.FuturesItem {
	return &pb.FuturesItem{
		Id:             f.ID,
		Ticker:         f.Ticker,
		Name:           f.Name,
		ContractSize:   f.ContractSize,
		ContractUnit:   f.ContractUnit,
		SettlementDate: f.SettlementDate.Format("2006-01-02"),
		Listing: toListingInfo(
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toFuturesDetail(f *model.FuturesContract) *pb.FuturesDetail {
	return &pb.FuturesDetail{
		Id:                f.ID,
		Ticker:            f.Ticker,
		Name:              f.Name,
		ContractSize:      f.ContractSize,
		ContractUnit:      f.ContractUnit,
		SettlementDate:    f.SettlementDate.Format("2006-01-02"),
		MaintenanceMargin: f.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toForexPairItem(fp *model.ForexPair) *pb.ForexPairItem {
	return &pb.ForexPairItem{
		Id:            fp.ID,
		Ticker:        fp.Ticker,
		Name:          fp.Name,
		BaseCurrency:  fp.BaseCurrency,
		QuoteCurrency: fp.QuoteCurrency,
		ExchangeRate:  fp.ExchangeRate.StringFixed(8),
		Liquidity:     fp.Liquidity,
		ContractSize:  fp.ContractSizeValue(),
		Listing: toListingInfo(
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}

func toForexPairDetail(fp *model.ForexPair) *pb.ForexPairDetail {
	return &pb.ForexPairDetail{
		Id:                fp.ID,
		Ticker:            fp.Ticker,
		Name:              fp.Name,
		BaseCurrency:      fp.BaseCurrency,
		QuoteCurrency:     fp.QuoteCurrency,
		ExchangeRate:      fp.ExchangeRate.StringFixed(8),
		Liquidity:         fp.Liquidity,
		ContractSize:      fp.ContractSizeValue(),
		MaintenanceMargin: fp.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}

func toOptionItem(o *model.Option) *pb.OptionItem {
	stockPrice := decimal.Zero
	if o.Stock.ID > 0 {
		stockPrice = o.Stock.Price
	}
	return &pb.OptionItem{
		Id:                o.ID,
		Ticker:            o.Ticker,
		Name:              o.Name,
		StockTicker:       o.Stock.Ticker,
		StockListingId:    o.StockID,
		OptionType:        o.OptionType,
		StrikePrice:       o.StrikePrice.StringFixed(4),
		ImpliedVolatility: o.ImpliedVolatility.StringFixed(6),
		Premium:           o.Premium.StringFixed(2),
		OpenInterest:      o.OpenInterest,
		SettlementDate:    o.SettlementDate.Format("2006-01-02"),
		ContractSize:      o.ContractSizeValue(),
		InitialMarginCost: o.InitialMarginCost(stockPrice).StringFixed(2),
	}
}

func toOptionDetail(o *model.Option) *pb.OptionDetail {
	stockPrice := decimal.Zero
	if o.Stock.ID > 0 {
		stockPrice = o.Stock.Price
	}
	return &pb.OptionDetail{
		Id:                o.ID,
		Ticker:            o.Ticker,
		Name:              o.Name,
		StockTicker:       o.Stock.Ticker,
		StockListingId:    o.StockID,
		OptionType:        o.OptionType,
		StrikePrice:       o.StrikePrice.StringFixed(4),
		ImpliedVolatility: o.ImpliedVolatility.StringFixed(6),
		Premium:           o.Premium.StringFixed(2),
		OpenInterest:      o.OpenInterest,
		SettlementDate:    o.SettlementDate.Format("2006-01-02"),
		ContractSize:      o.ContractSizeValue(),
		MaintenanceMargin: o.MaintenanceMargin(stockPrice).StringFixed(2),
		InitialMarginCost: o.InitialMarginCost(stockPrice).StringFixed(2),
	}
}
```

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS (assuming `contract/stockpb` is generated).

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/handler/security_handler.go
git commit -m "feat(stock-service): add SecurityGRPCService handler with all security type RPCs"
```

---

## Task 16: Wire securities into main.go

**Files:**
- Modify: `stock-service/cmd/main.go`

This task adds AutoMigrate for the 4 new models, creates repositories, services, the security handler, registers it on the gRPC server, and starts the sync goroutine.

- [ ] **Step 1: Update main.go**

Update `stock-service/cmd/main.go` to wire everything together. After the existing exchange wiring from Plan 2:

```go
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/config"
	"github.com/exbanka/stock-service/internal/handler"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func main() {
	cfg := config.Load()

	// --- Database ---
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// AutoMigrate all models
	if err := db.AutoMigrate(
		&model.StockExchange{},
		&model.SystemSetting{},
		&model.Stock{},
		&model.FuturesContract{},
		&model.ForexPair{},
		&model.Option{},
	); err != nil {
		log.Fatalf("auto-migrate failed: %v", err)
	}

	// --- Kafka ---
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()
	kafkaprod.EnsureTopics(cfg.KafkaBrokers, "stock.security-synced")

	// --- Repositories ---
	exchangeRepo := repository.NewExchangeRepository(db)
	settingRepo := repository.NewSystemSettingRepository(db)
	stockRepo := repository.NewStockRepository(db)
	futuresRepo := repository.NewFuturesRepository(db)
	forexRepo := repository.NewForexPairRepository(db)
	optionRepo := repository.NewOptionRepository(db)

	// --- Services ---
	exchangeSvc := service.NewExchangeService(exchangeRepo, settingRepo)

	// Seed exchanges from CSV
	if err := exchangeSvc.SeedExchanges(cfg.ExchangeCSVPath); err != nil {
		log.Printf("WARN: failed to seed exchanges: %v", err)
	}

	secSvc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo)

	// AlphaVantage client (nil if no API key)
	var avClient *provider.AlphaVantageClient
	if cfg.AlphaVantageAPIKey != "" {
		avClient = provider.NewAlphaVantageClient(cfg.AlphaVantageAPIKey)
	}

	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo, avClient,
	)

	// --- Seed securities ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		syncSvc.SeedAll(ctx, "data/futures_seed.json")
	}()

	// Start periodic price refresh
	syncSvc.StartPeriodicRefresh(ctx, cfg.SecuritySyncIntervalMins)

	// --- gRPC Server ---
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	// Register handlers
	exchangeHandler := handler.NewExchangeHandler(exchangeSvc)
	pb.RegisterStockExchangeGRPCServiceServer(grpcServer, exchangeHandler)

	securityHandler := handler.NewSecurityHandler(secSvc)
	pb.RegisterSecurityGRPCServiceServer(grpcServer, securityHandler)

	shared.RegisterHealthCheck(grpcServer, "stock-service")

	// --- Graceful shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down stock-service...")
		cancel()
		grpcServer.GracefulStop()
	}()

	log.Printf("stock-service listening on %s", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}
}
```

- [ ] **Step 2: Verify build**

```bash
cd stock-service && go build ./cmd
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): wire security models, repos, services, sync, and gRPC handlers into main"
```

---

## Task 17: Add FOREX synthetic exchange to CSV seed

**Files:**
- Modify: `stock-service/data/exchanges.csv`

The forex pair seeder needs a FOREX exchange entry. Add it to the CSV.

- [ ] **Step 1: Add FOREX row to exchanges.csv**

Append this line to `stock-service/data/exchanges.csv`:

```csv
Foreign Exchange Market,FOREX,XFOR,Global,USD,0,00:00,23:59,,
```

This is a synthetic 24-hour "exchange" representing the decentralized forex market.

- [ ] **Step 2: Commit**

```bash
git add stock-service/data/exchanges.csv
git commit -m "feat(stock-service): add FOREX synthetic exchange to CSV seed data"
```

---

## Task 18: Update docker-compose.yml with new env vars

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add new environment variables to stock-service**

In the `stock-service` section of `docker-compose.yml`, add:

```yaml
    environment:
      # ... existing vars from Plan 2 ...
      ALPHAVANTAGE_API_KEY: ${ALPHAVANTAGE_API_KEY:-}
      SECURITY_SYNC_INTERVAL_MINUTES: "15"
```

- [ ] **Step 2: Commit**

```bash
git add docker-compose.yml
git commit -m "feat(stock-service): add AlphaVantage and sync config to docker-compose"
```

---

## Task 19: Publish Kafka events for security sync

**Files:**
- Modify: `stock-service/internal/kafka/producer.go`
- Modify: `contract/kafka/messages.go`

Per CLAUDE.md: all significant actions must publish Kafka events.

- [ ] **Step 1: Add security event topics and messages to contract**

In `contract/kafka/messages.go`, add:

```go
const (
	// ... existing topics ...
	TopicSecuritySynced = "stock.security-synced"
)

type SecuritySyncedMessage struct {
	SecurityType string `json:"security_type"` // "stock", "futures", "forex", "option"
	Ticker       string `json:"ticker"`
	Action       string `json:"action"` // "created", "updated", "deleted"
	Timestamp    int64  `json:"timestamp"`
}
```

- [ ] **Step 2: Add publish method to stock-service producer**

In `stock-service/internal/kafka/producer.go`, add:

```go
func (p *Producer) PublishSecuritySynced(ctx context.Context, msg contract.SecuritySyncedMessage) error {
	return p.publish(ctx, contract.TopicSecuritySynced, msg)
}
```

Ensure the import includes `contract "github.com/exbanka/contract/kafka"`.

- [ ] **Step 3: Commit**

```bash
git add contract/kafka/messages.go stock-service/internal/kafka/producer.go
git commit -m "feat(stock-service): add Kafka events for security sync operations"
```

---

## Task 20: Verify full build and run tests

**Files:** None (verification only)

- [ ] **Step 1: Run go mod tidy**

```bash
cd stock-service && go mod tidy
```

- [ ] **Step 2: Run build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Run full repo build**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: All existing tests pass (new securities code has no tests yet — those come with the test-app in Plan 1 execution).

- [ ] **Step 5: Commit any go.sum changes**

```bash
git add stock-service/go.sum stock-service/go.mod
git commit -m "chore(stock-service): update go.sum after securities implementation"
```

---

## Design Notes

### Computed Fields (from spec, requirement 33.md)

| Security | ContractSize | MaintenanceMargin | InitialMarginCost |
|----------|-------------|-------------------|-------------------|
| Stock | 1 | 50% × Price | MaintenanceMargin × 1.1 |
| Futures | From seed data | ContractSize × Price × 10% | MaintenanceMargin × 1.1 |
| ForexPair | 1000 (standard) | ContractSize × Price × 10% | MaintenanceMargin × 1.1 |
| Option | 100 | ContractSize × 50% × StockPrice | MaintenanceMargin × 1.1 |

Additional derived fields:
- **ChangePercent** = 100 × Change / (Price − Change)
- **MarketCap** (stocks only) = OutstandingShares × Price
- **DollarVolume** = Volume × (Price) — computed by frontend if needed
- **NominalValue** (futures/forex) = ContractSize × Price — computed by frontend if needed

### Data Refresh Strategy

1. **On startup:** Full seed from local data (CSV/JSON) + external API (if key provided)
2. **Every 15 min (configurable):** Refresh prices from external API (stocks only, others are static)
3. **Testing mode:** No external API calls; cached local data used
4. **User-triggered refresh:** Frontend calls the existing GET endpoints — data is always current from last refresh

### Option Generation Algorithm (from spec Approach 2)

1. Settlement dates: 6 days apart for 30 days, then 6 more dates at 30-day intervals
2. Strike prices: current price rounded to integer ± 5
3. For each (date, strike): one CALL + one PUT
4. Premium: simplified intrinsic + time value estimation
5. Options are regenerated on each sync; expired options are deleted

### Interface Satisfaction

The concrete repositories (StockRepository, etc.) satisfy the interfaces in `service/interfaces.go` through Go's structural typing. No explicit `implements` is needed. The interfaces exist so the service layer can be tested with mocks.
