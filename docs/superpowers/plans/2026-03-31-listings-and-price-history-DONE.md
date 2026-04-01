# Listings & Price History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Listing entity (unified bridge between securities and exchanges), ListingDailyPriceInfo (historical daily snapshots), price history query support, and an end-of-day cron that snapshots prices.

**Architecture:** The Listing table is a unified bridge that maps each security (stock, futures, forex pair) to its exchange with a single `listing_id`. Orders (Plan 5) reference `listing_id` — this is the foreign key that ties an order to a specific security on a specific exchange. Options do not have listings (they are shown via stock detail per spec). Each Listing auto-creates from security seed data. `ListingDailyPriceInfo` stores one row per listing per day. An end-of-day cron snapshots current prices. The three price history RPCs stubbed in Plan 3 are implemented here.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, `shopspring/decimal`

**Depends on:**
- Plan 1 (API surface) — `GetPriceHistoryRequest`, `PriceHistoryResponse`, `PriceHistoryEntry` proto messages; `ListingInfo` proto message
- Plan 2 (exchanges) — `StockExchange` model, `ExchangeRepository`
- Plan 3 (securities) — `Stock`, `FuturesContract`, `ForexPair` models and repositories; `SecurityService`; `SecuritySyncService`

---

## Design Decisions

### Listing as a Bridge Table

The spec defines Listing as the mapping between a security and the exchange it trades on. In Plan 3, we stored `ExchangeID` and price data directly on each security model for convenience. The Listing table adds:

1. **Unified ID for orders** — `Order.ListingID` references one table instead of needing a polymorphic (security_type + security_id) pair
2. **SecurityType discriminator** — knows whether the listing points to a stock, futures, or forex pair
3. **Denormalized prices** — copies of current price data for fast order evaluation without JOINing to security tables
4. **History anchor** — `ListingDailyPriceInfo.ListingID` references this table

Prices are authoritative on the security models (updated by sync). Listing prices are updated from security models during each sync cycle.

### What Gets a Listing

| Security Type | Gets Listing? | Reason |
|---------------|--------------|--------|
| Stock | Yes | One stock → one exchange → one listing |
| Futures | Yes | One futures → one exchange → one listing |
| ForexPair | Yes | Approach 2: one forex pair → one exchange → one listing |
| Option | No | Displayed via stock detail page (spec: "Options nemaju listinge") |

### Price History Periods

The `period` parameter maps to date ranges:
- `day` → last 1 day (intraday not tracked; returns today's snapshot if available)
- `week` → last 7 days
- `month` → last 30 days
- `year` → last 365 days
- `5y` → last 1825 days
- `all` → no date filter

---

## File Structure

### New files to create

```
stock-service/
├── internal/
│   ├── model/
│   │   ├── listing.go                      # Listing entity
│   │   └── listing_daily_price_info.go     # Daily price history entity
│   ├── repository/
│   │   ├── listing_repository.go           # Listing CRUD + bulk operations
│   │   └── listing_daily_price_repository.go # Price history queries
│   ├── service/
│   │   ├── listing_service.go              # Listing business logic + auto-creation
│   │   └── listing_cron.go                 # End-of-day price snapshot cron
```

### Files to modify

```
stock-service/internal/service/interfaces.go          # Add ListingRepo, DailyPriceRepo interfaces
stock-service/internal/service/security_sync.go        # Update listings after security sync
stock-service/internal/handler/security_handler.go     # Implement price history RPCs (replace stubs)
stock-service/cmd/main.go                              # Wire listing models, repos, services, cron
```

---

## Task 1: Define Listing model

**Files:**
- Create: `stock-service/internal/model/listing.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Listing is the bridge between a security and the exchange it trades on.
// Orders reference ListingID. Each stock/futures/forex pair has exactly one listing.
// Options do not have listings (displayed via stock detail).
type Listing struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	SecurityID   uint64          `gorm:"not null;index" json:"security_id"`
	SecurityType string          `gorm:"size:10;not null;index" json:"security_type"` // "stock", "futures", "forex"
	ExchangeID   uint64          `gorm:"not null;index" json:"exchange_id"`
	Exchange     StockExchange   `gorm:"foreignKey:ExchangeID" json:"-"`
	// Denormalized current price data (mirrored from security model during sync)
	Price       decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"price"`
	High        decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"high"`
	Low         decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"low"`
	Change      decimal.Decimal `gorm:"type:numeric(18,8);not null;default:0" json:"change"`
	Volume      int64           `gorm:"not null;default:0" json:"volume"`
	LastRefresh time.Time       `json:"last_refresh"`
	Version     int64           `gorm:"not null;default:1" json:"-"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (l *Listing) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", l.Version)
	l.Version++
	return nil
}

// UniqueIndex enforced at DB level via migration.
// GORM tag on struct is too long; we do this in AutoMigrate hook.
// Composite unique: (security_id, security_type) — one listing per security.
```

- [ ] **Step 2: Add unique composite index via migration**

This will be added in main.go after AutoMigrate:

```go
db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_listings_security_unique ON listings(security_id, security_type)")
```

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/model/listing.go
git commit -m "feat(stock-service): add Listing model as security-exchange bridge"
```

---

## Task 2: Define ListingDailyPriceInfo model

**Files:**
- Create: `stock-service/internal/model/listing_daily_price_info.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ListingDailyPriceInfo stores one row per listing per day.
// Populated by the end-of-day cron job.
type ListingDailyPriceInfo struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ListingID uint64          `gorm:"not null;index" json:"listing_id"`
	Listing   Listing         `gorm:"foreignKey:ListingID" json:"-"`
	Date      time.Time       `gorm:"type:date;not null;index" json:"date"`
	Price     decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"price"`
	High      decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"high"`
	Low       decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"low"`
	Change    decimal.Decimal `gorm:"type:numeric(18,8);not null" json:"change"`
	Volume    int64           `gorm:"not null;default:0" json:"volume"`
}

// Composite unique (listing_id, date) ensures one snapshot per listing per day.
// Added via DB migration in main.go.
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/listing_daily_price_info.go
git commit -m "feat(stock-service): add ListingDailyPriceInfo model for daily price history"
```

---

## Task 3: Add repository interfaces for listings

**Files:**
- Modify: `stock-service/internal/service/interfaces.go`

- [ ] **Step 1: Add listing interfaces**

Append to `stock-service/internal/service/interfaces.go`:

```go
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
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/interfaces.go
git commit -m "feat(stock-service): add ListingRepo and DailyPriceRepo interfaces"
```

---

## Task 4: Implement Listing repository

**Files:**
- Create: `stock-service/internal/repository/listing_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingRepository struct {
	db *gorm.DB
}

func NewListingRepository(db *gorm.DB) *ListingRepository {
	return &ListingRepository{db: db}
}

func (r *ListingRepository) Create(listing *model.Listing) error {
	return r.db.Create(listing).Error
}

func (r *ListingRepository) GetByID(id uint64) (*model.Listing, error) {
	var listing model.Listing
	if err := r.db.Preload("Exchange").First(&listing, id).Error; err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *ListingRepository) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	var listing model.Listing
	if err := r.db.Where("security_id = ? AND security_type = ?", securityID, securityType).
		Preload("Exchange").First(&listing).Error; err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *ListingRepository) Update(listing *model.Listing) error {
	result := r.db.Save(listing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *ListingRepository) UpsertBySecurity(listing *model.Listing) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Listing
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("security_id = ? AND security_type = ?", listing.SecurityID, listing.SecurityType).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(listing).Error
			}
			return err
		}
		// Update price data
		existing.ExchangeID = listing.ExchangeID
		existing.Price = listing.Price
		existing.High = listing.High
		existing.Low = listing.Low
		existing.Change = listing.Change
		existing.Volume = listing.Volume
		existing.LastRefresh = listing.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *ListingRepository) ListAll() ([]model.Listing, error) {
	var listings []model.Listing
	if err := r.db.Preload("Exchange").Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}

func (r *ListingRepository) ListBySecurityType(securityType string) ([]model.Listing, error) {
	var listings []model.Listing
	if err := r.db.Where("security_type = ?", securityType).
		Preload("Exchange").Find(&listings).Error; err != nil {
		return nil, err
	}
	return listings, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/listing_repository.go
git commit -m "feat(stock-service): add ListingRepository with upsert-by-security"
```

---

## Task 5: Implement ListingDailyPriceInfo repository

**Files:**
- Create: `stock-service/internal/repository/listing_daily_price_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingDailyPriceRepository struct {
	db *gorm.DB
}

func NewListingDailyPriceRepository(db *gorm.DB) *ListingDailyPriceRepository {
	return &ListingDailyPriceRepository{db: db}
}

func (r *ListingDailyPriceRepository) Create(info *model.ListingDailyPriceInfo) error {
	return r.db.Create(info).Error
}

func (r *ListingDailyPriceRepository) UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ListingDailyPriceInfo
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("listing_id = ? AND date = ?", info.ListingID, info.Date).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(info).Error
			}
			return err
		}
		existing.Price = info.Price
		existing.High = info.High
		existing.Low = info.Low
		existing.Change = info.Change
		existing.Volume = info.Volume
		return tx.Save(&existing).Error
	})
}

func (r *ListingDailyPriceRepository) GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	var history []model.ListingDailyPriceInfo
	var total int64

	q := r.db.Model(&model.ListingDailyPriceInfo{}).
		Where("listing_id = ?", listingID)

	if !from.IsZero() {
		q = q.Where("date >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where("date <= ?", to)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 365 {
		pageSize = 30
	}

	if err := q.Order("date DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&history).Error; err != nil {
		return nil, 0, err
	}
	return history, total, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/listing_daily_price_repository.go
git commit -m "feat(stock-service): add ListingDailyPriceRepository with period-based history queries"
```

---

## Task 6: Create ListingService

**Files:**
- Create: `stock-service/internal/service/listing_service.go`

This service handles:
1. Auto-creating listings from securities
2. Syncing listing prices from security models
3. Looking up listings for the price history handler

- [ ] **Step 1: Write the service**

```go
package service

import (
	"errors"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingService struct {
	listingRepo  ListingRepo
	dailyRepo    DailyPriceRepo
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
}

func NewListingService(
	listingRepo ListingRepo,
	dailyRepo DailyPriceRepo,
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
) *ListingService {
	return &ListingService{
		listingRepo:  listingRepo,
		dailyRepo:    dailyRepo,
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
	}
}

// SyncListingsFromSecurities creates or updates listings for all securities.
// Called after security seed/sync completes.
func (s *ListingService) SyncListingsFromSecurities() {
	s.syncStockListings()
	s.syncFuturesListings()
	s.syncForexListings()
}

func (s *ListingService) syncStockListings() {
	stocks, _, err := s.stockRepo.List(StockFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for listing sync: %v", err)
		return
	}
	count := 0
	for _, stock := range stocks {
		listing := &model.Listing{
			SecurityID:   stock.ID,
			SecurityType: "stock",
			ExchangeID:   stock.ExchangeID,
			Price:        stock.Price,
			High:         stock.High,
			Low:          stock.Low,
			Change:       stock.Change,
			Volume:       stock.Volume,
			LastRefresh:  stock.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for stock %s: %v", stock.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d stock listings", count)
}

func (s *ListingService) syncFuturesListings() {
	futures, _, err := s.futuresRepo.List(FuturesFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list futures for listing sync: %v", err)
		return
	}
	count := 0
	for _, f := range futures {
		listing := &model.Listing{
			SecurityID:   f.ID,
			SecurityType: "futures",
			ExchangeID:   f.ExchangeID,
			Price:        f.Price,
			High:         f.High,
			Low:          f.Low,
			Change:       f.Change,
			Volume:       f.Volume,
			LastRefresh:  f.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for futures %s: %v", f.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d futures listings", count)
}

func (s *ListingService) syncForexListings() {
	pairs, _, err := s.forexRepo.List(ForexFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list forex pairs for listing sync: %v", err)
		return
	}
	count := 0
	for _, fp := range pairs {
		listing := &model.Listing{
			SecurityID:   fp.ID,
			SecurityType: "forex",
			ExchangeID:   fp.ExchangeID,
			Price:        fp.ExchangeRate,
			High:         fp.High,
			Low:          fp.Low,
			Change:       fp.Change,
			Volume:       fp.Volume,
			LastRefresh:  fp.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for forex %s: %v", fp.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d forex listings", count)
}

// GetListing retrieves a listing by ID.
func (s *ListingService) GetListing(id uint64) (*model.Listing, error) {
	listing, err := s.listingRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("listing not found")
		}
		return nil, err
	}
	return listing, nil
}

// GetListingForSecurity finds the listing for a given security.
func (s *ListingService) GetListingForSecurity(securityID uint64, securityType string) (*model.Listing, error) {
	listing, err := s.listingRepo.GetBySecurityIDAndType(securityID, securityType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("listing not found for security")
		}
		return nil, err
	}
	return listing, nil
}

// GetPriceHistory retrieves daily price history for a listing within a period.
func (s *ListingService) GetPriceHistory(listingID uint64, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	from, to := periodToDateRange(period)
	return s.dailyRepo.GetHistory(listingID, from, to, page, pageSize)
}

// GetPriceHistoryForSecurity looks up the listing for a security, then gets history.
func (s *ListingService) GetPriceHistoryForSecurity(securityID uint64, securityType, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	listing, err := s.listingRepo.GetBySecurityIDAndType(securityID, securityType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, errors.New("listing not found for security")
		}
		return nil, 0, err
	}
	return s.GetPriceHistory(listing.ID, period, page, pageSize)
}

// periodToDateRange converts a period string to (from, to) dates.
func periodToDateRange(period string) (time.Time, time.Time) {
	now := time.Now()
	to := now

	switch period {
	case "day":
		return now.AddDate(0, 0, -1), to
	case "week":
		return now.AddDate(0, 0, -7), to
	case "month":
		return now.AddDate(0, -1, 0), to
	case "year":
		return now.AddDate(-1, 0, 0), to
	case "5y":
		return now.AddDate(-5, 0, 0), to
	case "all":
		return time.Time{}, to // zero time = no lower bound
	default:
		return now.AddDate(0, -1, 0), to // default: month
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/listing_service.go
git commit -m "feat(stock-service): add ListingService with auto-sync from securities and price history"
```

---

## Task 7: Create end-of-day price snapshot cron

**Files:**
- Create: `stock-service/internal/service/listing_cron.go`

This cron runs daily and snapshots the current price of every listing into `ListingDailyPriceInfo`.

- [ ] **Step 1: Write the cron service**

```go
package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

type ListingCronService struct {
	listingRepo ListingRepo
	dailyRepo   DailyPriceRepo
}

func NewListingCronService(listingRepo ListingRepo, dailyRepo DailyPriceRepo) *ListingCronService {
	return &ListingCronService{
		listingRepo: listingRepo,
		dailyRepo:   dailyRepo,
	}
}

// SnapshotDailyPrices takes the current price of every listing and saves it
// as today's daily price entry. Idempotent (upserts by listing+date).
func (c *ListingCronService) SnapshotDailyPrices() {
	listings, err := c.listingRepo.ListAll()
	if err != nil {
		log.Printf("WARN: listing cron: failed to list listings: %v", err)
		return
	}

	today := time.Now().Truncate(24 * time.Hour)
	count := 0
	for _, l := range listings {
		info := &model.ListingDailyPriceInfo{
			ListingID: l.ID,
			Date:      today,
			Price:     l.Price,
			High:      l.High,
			Low:       l.Low,
			Change:    l.Change,
			Volume:    l.Volume,
		}
		if err := c.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: listing cron: failed to snapshot listing %d: %v", l.ID, err)
			continue
		}
		count++
	}
	log.Printf("listing cron: snapshotted %d daily prices for %s", count, today.Format("2006-01-02"))
}

// StartDailyCron schedules the snapshot to run daily at 23:55 (just before midnight).
func (c *ListingCronService) StartDailyCron(ctx context.Context) {
	go func() {
		for {
			now := time.Now()
			// Next run at 23:55 today (or tomorrow if already past)
			next := time.Date(now.Year(), now.Month(), now.Day(), 23, 55, 0, 0, now.Location())
			if now.After(next) {
				next = next.AddDate(0, 0, 1)
			}
			waitDuration := time.Until(next)

			select {
			case <-time.After(waitDuration):
				log.Println("listing cron: running daily price snapshot")
				c.SnapshotDailyPrices()
			case <-ctx.Done():
				log.Println("listing cron: stopped")
				return
			}
		}
	}()
	log.Println("listing cron: scheduled daily at 23:55")
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/listing_cron.go
git commit -m "feat(stock-service): add daily price snapshot cron for listing history"
```

---

## Task 8: Implement price history in security handler

**Files:**
- Modify: `stock-service/internal/handler/security_handler.go`

Replace the three stub price history RPCs with real implementations that delegate to `ListingService`.

- [ ] **Step 1: Add ListingService dependency to SecurityHandler**

Update the `SecurityHandler` struct and constructor:

```go
type SecurityHandler struct {
	pb.UnimplementedSecurityGRPCServiceServer
	secSvc     *service.SecurityService
	listingSvc *service.ListingService
}

func NewSecurityHandler(secSvc *service.SecurityService, listingSvc *service.ListingService) *SecurityHandler {
	return &SecurityHandler{secSvc: secSvc, listingSvc: listingSvc}
}
```

- [ ] **Step 2: Implement GetStockHistory**

Replace the stub:

```go
func (h *SecurityHandler) GetStockHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "stock", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toPriceHistoryResponse(history, total), nil
}
```

- [ ] **Step 3: Implement GetFuturesHistory**

```go
func (h *SecurityHandler) GetFuturesHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "futures", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toPriceHistoryResponse(history, total), nil
}
```

- [ ] **Step 4: Implement GetForexPairHistory**

```go
func (h *SecurityHandler) GetForexPairHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "forex", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return toPriceHistoryResponse(history, total), nil
}
```

- [ ] **Step 5: Add the mapping helper**

```go
func toPriceHistoryResponse(history []model.ListingDailyPriceInfo, total int64) *pb.PriceHistoryResponse {
	entries := make([]*pb.PriceHistoryEntry, len(history))
	for i, h := range history {
		entries[i] = &pb.PriceHistoryEntry{
			Date:   h.Date.Format("2006-01-02"),
			Price:  h.Price.StringFixed(4),
			High:   h.High.StringFixed(4),
			Low:    h.Low.StringFixed(4),
			Change: h.Change.StringFixed(4),
			Volume: h.Volume,
		}
	}
	return &pb.PriceHistoryResponse{History: entries, TotalCount: total}
}
```

Add `"github.com/exbanka/stock-service/internal/model"` to imports if not already present.

- [ ] **Step 6: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/handler/security_handler.go
git commit -m "feat(stock-service): implement price history RPCs using ListingService"
```

---

## Task 9: Update SecuritySyncService to sync listings

**Files:**
- Modify: `stock-service/internal/service/security_sync.go`

After seeding securities, we need to also sync listings.

- [ ] **Step 1: Add ListingService to SecuritySyncService**

Add `listingSvc *ListingService` field and update the constructor:

```go
type SecuritySyncService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo ExchangeRepo
	settingRepo  SettingRepo
	avClient     *provider.AlphaVantageClient
	listingSvc   *ListingService
}

func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
	listingSvc *ListingService,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
		optionRepo:   optionRepo,
		exchangeRepo: exchangeRepo,
		settingRepo:  settingRepo,
		avClient:     avClient,
		listingSvc:   listingSvc,
	}
}
```

- [ ] **Step 2: Call listing sync after security seed**

Update `SeedAll()`:

```go
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.seedFutures(futuresSeedPath)
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.generateAllOptions()
	// Sync listings from the securities we just seeded
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
}
```

- [ ] **Step 3: Call listing sync after price refresh**

Update `RefreshPrices()`:

```go
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	// Update listing prices from refreshed security data
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
	log.Println("price refresh complete")
}
```

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/security_sync.go
git commit -m "feat(stock-service): sync listings after security seed and price refresh"
```

---

## Task 10: Wire listings into main.go

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Add AutoMigrate for listing models**

Add to the AutoMigrate call:

```go
if err := db.AutoMigrate(
	&model.StockExchange{},
	&model.SystemSetting{},
	&model.Stock{},
	&model.FuturesContract{},
	&model.ForexPair{},
	&model.Option{},
	&model.Listing{},
	&model.ListingDailyPriceInfo{},
); err != nil {
	log.Fatalf("auto-migrate failed: %v", err)
}

// Composite unique indexes
db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_listings_security_unique ON listings(security_id, security_type)")
db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_price_listing_date ON listing_daily_price_infos(listing_id, date)")
```

- [ ] **Step 2: Create listing repos and services**

After the security repos:

```go
listingRepo := repository.NewListingRepository(db)
dailyPriceRepo := repository.NewListingDailyPriceRepository(db)

listingSvc := service.NewListingService(listingRepo, dailyPriceRepo, stockRepo, futuresRepo, forexRepo)
```

- [ ] **Step 3: Update SecuritySyncService constructor**

Pass `listingSvc` as the new parameter:

```go
syncSvc := service.NewSecuritySyncService(
	stockRepo, futuresRepo, forexRepo, optionRepo,
	exchangeRepo, settingRepo, avClient, listingSvc,
)
```

- [ ] **Step 4: Update SecurityHandler constructor**

```go
securityHandler := handler.NewSecurityHandler(secSvc, listingSvc)
```

- [ ] **Step 5: Start listing cron**

After starting the periodic refresh:

```go
listingCron := service.NewListingCronService(listingRepo, dailyPriceRepo)
listingCron.StartDailyCron(ctx)
```

- [ ] **Step 6: Verify build**

```bash
cd stock-service && go build ./cmd
```

Expected: BUILD SUCCESS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): wire listing models, repos, services, and daily cron into main"
```

---

## Task 11: Add Kafka events for listing operations

**Files:**
- Modify: `contract/kafka/messages.go`
- Modify: `stock-service/internal/kafka/producer.go`

- [ ] **Step 1: Add listing event topic and message**

In `contract/kafka/messages.go`:

```go
const (
	// ... existing topics ...
	TopicListingUpdated = "stock.listing-updated"
)

type ListingUpdatedMessage struct {
	ListingID    uint64 `json:"listing_id"`
	SecurityType string `json:"security_type"`
	SecurityID   uint64 `json:"security_id"`
	Price        string `json:"price"`
	Timestamp    int64  `json:"timestamp"`
}
```

- [ ] **Step 2: Add publish method to producer**

In `stock-service/internal/kafka/producer.go`:

```go
func (p *Producer) PublishListingUpdated(ctx context.Context, msg contract.ListingUpdatedMessage) error {
	return p.publish(ctx, contract.TopicListingUpdated, msg)
}
```

- [ ] **Step 3: Add topic to EnsureTopics in main.go**

Update the `EnsureTopics` call:

```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers, "stock.security-synced", "stock.listing-updated")
```

- [ ] **Step 4: Commit**

```bash
git add contract/kafka/messages.go stock-service/internal/kafka/producer.go stock-service/cmd/main.go
git commit -m "feat(stock-service): add Kafka events for listing price updates"
```

---

## Task 12: Seed initial price history on startup

**Files:**
- Modify: `stock-service/internal/service/listing_cron.go`

On first startup, there is no price history data. We seed one snapshot for "today" immediately after listing sync, so the price history endpoint has at least one data point.

- [ ] **Step 1: Add SeedInitialSnapshot method**

```go
// SeedInitialSnapshot creates today's price snapshot for all listings if no
// history exists yet. Called once at startup after listings are synced.
func (c *ListingCronService) SeedInitialSnapshot() {
	listings, err := c.listingRepo.ListAll()
	if err != nil {
		log.Printf("WARN: listing cron: failed to seed initial snapshot: %v", err)
		return
	}

	today := time.Now().Truncate(24 * time.Hour)
	count := 0
	for _, l := range listings {
		// Check if today's snapshot already exists
		existing, _, _ := c.dailyRepo.GetHistory(l.ID, today, today, 1, 1)
		if len(existing) > 0 {
			continue
		}
		info := &model.ListingDailyPriceInfo{
			ListingID: l.ID,
			Date:      today,
			Price:     l.Price,
			High:      l.High,
			Low:       l.Low,
			Change:    l.Change,
			Volume:    l.Volume,
		}
		if err := c.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: listing cron: failed to seed snapshot for listing %d: %v", l.ID, err)
			continue
		}
		count++
	}
	if count > 0 {
		log.Printf("listing cron: seeded %d initial price snapshots", count)
	}
}
```

- [ ] **Step 2: Call SeedInitialSnapshot in main.go after sync**

In `main.go`, after the `syncSvc.SeedAll()` goroutine finishes (or right after listing cron creation):

```go
// Seed initial price history after listings are created
go func() {
	// Wait for seed to complete (the seed goroutine runs async)
	time.Sleep(5 * time.Second)
	listingCron.SeedInitialSnapshot()
}()
```

- [ ] **Step 3: Commit**

```bash
git add stock-service/internal/service/listing_cron.go stock-service/cmd/main.go
git commit -m "feat(stock-service): seed initial price history snapshot on startup"
```

---

## Task 13: Add derived data calculations

**Files:**
- Create: `stock-service/internal/service/listing_derived.go`
- Modify: `stock-service/internal/service/interfaces.go`
- Modify: `stock-service/internal/handler/security_handler.go`

The spec requires several computed fields on listings. These are not stored in the DB — they are calculated on-the-fly from listing + security data and returned via the gRPC `ListingInfo` message.

### Formulas (per spec)

| Field | Formula |
|-------|---------|
| **ChangePercent** | `100 × Change / (Price − Change)` |
| **DollarVolume** | `Volume × Price` |
| **NominalValue** | `ContractSize × Price` |
| **MaintenanceMargin** | security-type-specific (see below) |
| **InitialMarginCost** | `MaintenanceMargin × 1.1` |

**ContractSize** per security type:
- Stock: `1`
- Futures: from `FuturesContract.ContractSize` field
- ForexPair: `1000` (standard, configurable)
- Option: `100` (standardized for stocks)

**MaintenanceMargin** per security type:
- Stock: `50% × Price`
- Futures: `ContractSize × Price × 10%`
- ForexPair: `ContractSize × Price × 10%`
- Option: `ContractSize × 50% × StockPrice` (stock's current price)

- [ ] **Step 1: Write the derived data calculator**

```go
package service

import (
	"github.com/shopspring/decimal"
)

// DerivedListingData holds computed fields for a listing.
// These are NOT stored in the DB — calculated on-the-fly from listing + security data.
type DerivedListingData struct {
	ContractSize      int64           `json:"contract_size"`
	MaintenanceMargin decimal.Decimal `json:"maintenance_margin"`
	InitialMarginCost decimal.Decimal `json:"initial_margin_cost"`
	ChangePercent     decimal.Decimal `json:"change_percent"`
	DollarVolume      decimal.Decimal `json:"dollar_volume"`
	NominalValue      decimal.Decimal `json:"nominal_value"`
	MarketCap         decimal.Decimal `json:"market_cap,omitempty"` // stocks only
}

// CalculateDerivedData computes all derived fields for a listing.
// securityType: "stock", "futures", "forex"
// price: listing current price
// change: listing change value
// volume: listing volume
// contractSizeOverride: used for futures (from FuturesContract.ContractSize). 0 = use default.
// outstandingShares: stocks only (for MarketCap). 0 = not applicable.
// stockPrice: options only (underlying stock price for margin). Zero = not applicable.
func CalculateDerivedData(
	securityType string,
	price decimal.Decimal,
	change decimal.Decimal,
	volume int64,
	contractSizeOverride int64,
	outstandingShares int64,
	stockPrice decimal.Decimal,
) DerivedListingData {
	d := DerivedListingData{}

	// Determine ContractSize
	switch securityType {
	case "stock":
		d.ContractSize = 1
	case "futures":
		if contractSizeOverride > 0 {
			d.ContractSize = contractSizeOverride
		} else {
			d.ContractSize = 1
		}
	case "forex":
		d.ContractSize = 1000
	default:
		d.ContractSize = 1
	}
	cs := decimal.NewFromInt(d.ContractSize)

	// MaintenanceMargin (per security type)
	switch securityType {
	case "stock":
		// 50% × Price
		d.MaintenanceMargin = price.Mul(decimal.NewFromFloat(0.50))
	case "futures":
		// ContractSize × Price × 10%
		d.MaintenanceMargin = cs.Mul(price).Mul(decimal.NewFromFloat(0.10))
	case "forex":
		// ContractSize × Price × 10%
		d.MaintenanceMargin = cs.Mul(price).Mul(decimal.NewFromFloat(0.10))
	default:
		d.MaintenanceMargin = decimal.Zero
	}

	// InitialMarginCost = MaintenanceMargin × 1.1
	d.InitialMarginCost = d.MaintenanceMargin.Mul(decimal.NewFromFloat(1.1))

	// ChangePercent = 100 × Change / (Price − Change)
	denominator := price.Sub(change)
	if !denominator.IsZero() {
		d.ChangePercent = change.Mul(decimal.NewFromInt(100)).Div(denominator).Round(4)
	}

	// DollarVolume = Volume × Price
	d.DollarVolume = decimal.NewFromInt(volume).Mul(price)

	// NominalValue = ContractSize × Price
	d.NominalValue = cs.Mul(price)

	// MarketCap = OutstandingShares × Price (stocks only)
	if securityType == "stock" && outstandingShares > 0 {
		d.MarketCap = decimal.NewFromInt(outstandingShares).Mul(price)
	}

	return d
}

// CalculateOptionDerivedData computes derived fields for an option.
// Options don't have listings, but the data is needed for the option detail view.
func CalculateOptionDerivedData(
	premium decimal.Decimal,
	stockPrice decimal.Decimal,
) DerivedListingData {
	contractSize := int64(100) // standardized for stock options
	cs := decimal.NewFromInt(contractSize)

	// MaintenanceMargin = ContractSize × 50% × StockPrice
	maintenanceMargin := cs.Mul(stockPrice).Mul(decimal.NewFromFloat(0.50))

	return DerivedListingData{
		ContractSize:      contractSize,
		MaintenanceMargin: maintenanceMargin,
		InitialMarginCost: maintenanceMargin.Mul(decimal.NewFromFloat(1.1)),
		NominalValue:      cs.Mul(premium),
	}
}
```

- [ ] **Step 2: Add DerivedListingData to ListingService**

Add a method to `listing_service.go` that computes derived data for a listing by looking up the underlying security:

```go
// GetDerivedData computes derived listing data by looking up the underlying security.
func (s *ListingService) GetDerivedData(listing *model.Listing) DerivedListingData {
	var contractSizeOverride int64
	var outstandingShares int64

	switch listing.SecurityType {
	case "futures":
		if f, err := s.futuresRepo.GetByID(listing.SecurityID); err == nil {
			contractSizeOverride = f.ContractSize
		}
	case "stock":
		if st, err := s.stockRepo.GetByID(listing.SecurityID); err == nil {
			outstandingShares = st.OutstandingShares
		}
	}

	return CalculateDerivedData(
		listing.SecurityType,
		listing.Price,
		listing.Change,
		listing.Volume,
		contractSizeOverride,
		outstandingShares,
		decimal.Zero, // stockPrice only for options
	)
}
```

Add `GetByID` to the `StockRepo` and `FuturesRepo` interfaces if not already present.

- [ ] **Step 3: Include derived data in gRPC responses**

Update the `toPriceHistoryResponse` and listing-related response builders in `security_handler.go` to include derived fields. When the gateway handler calls `ListStocks`, `ListFutures`, or `ListForex`, the response should include a `ListingInfo` block with the derived data.

In the SecurityHandler, add a helper that takes a listing and returns derived data:

```go
func toDerivedDataProto(d service.DerivedListingData) *pb.DerivedData {
	return &pb.DerivedData{
		ContractSize:      d.ContractSize,
		MaintenanceMargin: d.MaintenanceMargin.StringFixed(4),
		InitialMarginCost: d.InitialMarginCost.StringFixed(4),
		ChangePercent:     d.ChangePercent.StringFixed(4),
		DollarVolume:      d.DollarVolume.StringFixed(4),
		NominalValue:      d.NominalValue.StringFixed(4),
		MarketCap:         d.MarketCap.StringFixed(4),
	}
}
```

Note: This requires adding a `DerivedData` message to the proto definition in Plan 1. Add to `stock.proto`:

```protobuf
message DerivedData {
  int64  contract_size      = 1;
  string maintenance_margin = 2;
  string initial_margin_cost = 3;
  string change_percent     = 4;
  string dollar_volume      = 5;
  string nominal_value      = 6;
  string market_cap         = 7; // stocks only, empty for others
}
```

And include it in `ListingInfo`:

```protobuf
message ListingInfo {
  // ... existing fields ...
  DerivedData derived = 10;
}
```

- [ ] **Step 4: Verify build**

```bash
cd stock-service && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/listing_derived.go stock-service/internal/service/listing_service.go stock-service/internal/handler/security_handler.go
git commit -m "feat(stock-service): add derived data calculations (margin, contract size, change%, volume)"
```

---

## Task 14: Verify full build

**Files:** None (verification only)

- [ ] **Step 1: Run go mod tidy and build**

```bash
cd stock-service && go mod tidy && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 2: Run full repo build**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 3: Commit any dependency changes**

```bash
git add stock-service/go.sum stock-service/go.mod
git commit -m "chore(stock-service): update dependencies after listings implementation"
```

---

## Design Notes

### Listing ↔ Security Relationship

```
Stock (id=1)       → Listing (id=1, security_id=1, security_type="stock")
FuturesContract (id=1) → Listing (id=2, security_id=1, security_type="futures")
ForexPair (id=1)   → Listing (id=3, security_id=1, security_type="forex")
Option (id=1)      → NO LISTING (displayed via stock detail)
```

### Order → Listing Reference (for Plan 5)

When creating an order, the user provides `listing_id`. The Order service:
1. Looks up the Listing to get `security_type`, `security_id`, `exchange_id`
2. Uses `security_type` to route to the correct security model for margin/price checks
3. Checks exchange hours via `ExchangeService.IsExchangeOpen(listing.ExchangeID)`

### Volume Tracking

The `Volume` field in `ListingDailyPriceInfo` tracks the number of securities traded *in our system* that day (per spec: "Broj prodatih/kupovanih hartija tokom dana u nasem sistemu"). Plan 5 (Orders) will increment this when order transactions execute. For now, volume comes from the security model's synced data.
