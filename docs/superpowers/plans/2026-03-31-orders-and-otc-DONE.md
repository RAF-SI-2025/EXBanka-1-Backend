# Orders & OTC Trading Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement order creation, approval, execution engine (Market, Limit, Stop, Stop-Limit), OTC trading, and the supervisor order review portal in `stock-service`.

**Architecture:** Orders are the core trading mechanic. A user creates an order specifying a listing, direction (buy/sell), type, and quantity. The system determines whether approval is needed (agent limits), then simulates execution by splitting the order into portions over time. Execution is driven by a background goroutine per active order. OTC trading allows users to buy stocks from other users who have made shares "public" in their portfolio.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, Kafka (segmentio/kafka-go), `shopspring/decimal`

**Depends on:**
- Plan 1 (API surface) — `OrderGRPCService`, `OTCGRPCService` proto definitions
- Plan 2 (exchanges) — `ExchangeService.IsExchangeOpen()`, testing mode
- Plan 3 (securities) — Security models, repositories
- Plan 4 (listings) — `Listing` model, `ListingService`

---

## Design Decisions

### Order Approval Logic

An order needs supervisor approval when **all of these** are true:
1. The user is an **agent** (employee with EmployeeAgent role) — NOT a client or supervisor
2. AND one of: user has `need_approval = true`, OR order total exceeds remaining daily limit, OR daily limit is already exhausted

Client orders are **auto-approved** (spec: "Klijent postavlja Ordere ali njegovi orderi su automatski Approved").
Supervisor orders are **auto-approved** (they approve their own).

### Commission Rules

| Order Type | Commission |
|------------|------------|
| Market | min(14% × approximate_price, $7) |
| Limit | min(24% × approximate_price, $12) |
| Stop | same as Market (becomes Market on trigger) |
| Stop-Limit | same as Limit (becomes Limit on trigger) |

Commission is credited to the bank's account in the same currency.

### Order Execution Simulation

Orders execute in portions over time (simulating real market):
1. A background goroutine picks a random portion `n ∈ [1, remaining_quantity]`
2. Waits `Random(0, 24*60 / (Volume / remaining_quantity))` seconds
3. Executes that portion at the current price (per order type rules)
4. Records an `OrderTransaction`
5. Repeats until fully filled or cancelled
6. **After hours:** adds 30 extra minutes per portion wait
7. **All or None (AON):** executes all at once or not at all

### OTC Trading

Stocks in a user's portfolio can be made "public" (Plan 6 implements the make-public action on holdings). The OTC portal shows all public shares. A buyer can purchase from an OTC offer, which directly transfers ownership without going through the exchange order book.

---

## File Structure

### New files to create

```
stock-service/
├── internal/
│   ├── model/
│   │   ├── order.go                      # Order entity
│   │   ├── order_transaction.go          # OrderTransaction entity (execution records)
│   │   └── otc_offer.go                  # OTC offer (view of public holdings)
│   ├── repository/
│   │   ├── order_repository.go           # Order CRUD + filtered list
│   │   └── order_transaction_repository.go # Transaction records
│   ├── service/
│   │   ├── order_service.go              # Order business logic (create, approve, cancel)
│   │   ├── order_execution.go            # Background execution engine
│   │   └── otc_service.go               # OTC trading logic
│   └── handler/
│       ├── order_handler.go              # OrderGRPCService implementation
│       └── otc_handler.go               # OTCGRPCService implementation
```

### Files to modify

```
stock-service/internal/service/interfaces.go     # Add OrderRepo, OrderTransactionRepo interfaces
stock-service/internal/kafka/producer.go          # Add order event publishing
stock-service/cmd/main.go                         # Wire order/OTC models, repos, services, handlers
contract/kafka/messages.go                        # Add order event topics
docker-compose.yml                                # Add STOCK_GRPC_ADDR to user-service (for limit checks)
```

---

## Task 1: Define Order model

**Files:**
- Create: `stock-service/internal/model/order.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Order represents a buy or sell order for a security listing.
type Order struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID            uint64          `gorm:"not null;index" json:"user_id"`
	SystemType        string          `gorm:"size:10;not null" json:"system_type"` // "employee" or "client"
	ListingID         uint64          `gorm:"not null;index" json:"listing_id"`
	Listing           Listing         `gorm:"foreignKey:ListingID" json:"-"`
	HoldingID         *uint64         `gorm:"index" json:"holding_id"` // for sell orders, references portfolio holding
	SecurityType      string          `gorm:"size:10;not null" json:"security_type"`
	Ticker            string          `gorm:"size:30;not null" json:"ticker"`
	Direction         string          `gorm:"size:4;not null" json:"direction"` // "buy" or "sell"
	OrderType         string          `gorm:"size:10;not null" json:"order_type"` // "market", "limit", "stop", "stop_limit"
	Quantity          int64           `gorm:"not null" json:"quantity"`
	ContractSize      int64           `gorm:"not null;default:1" json:"contract_size"`
	PricePerUnit      decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
	ApproximatePrice  decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"approximate_price"`
	Commission        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"commission"`
	LimitValue        *decimal.Decimal `gorm:"type:numeric(18,4)" json:"limit_value"`
	StopValue         *decimal.Decimal `gorm:"type:numeric(18,4)" json:"stop_value"`
	Status            string          `gorm:"size:10;not null;default:'pending';index" json:"status"` // "pending", "approved", "declined", "cancelled"
	ApprovedBy        string          `gorm:"size:100" json:"approved_by"` // supervisor name or "no need for approval"
	IsDone            bool            `gorm:"not null;default:false;index" json:"is_done"`
	RemainingPortions int64           `gorm:"not null" json:"remaining_portions"`
	AfterHours        bool            `gorm:"not null;default:false" json:"after_hours"`
	AllOrNone         bool            `gorm:"not null;default:false" json:"all_or_none"`
	Margin            bool            `gorm:"not null;default:false" json:"margin"`
	AccountID         uint64          `gorm:"not null" json:"account_id"`
	Version           int64           `gorm:"not null;default:1" json:"-"`
	LastModification  time.Time       `json:"last_modification"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func (o *Order) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", o.Version)
	o.Version++
	return nil
}

// NeedsApproval returns true if this order must go through supervisor approval.
// Client orders and supervisor orders are auto-approved.
func (o *Order) IsAutoApproved() bool {
	return o.SystemType == "client"
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/order.go
git commit -m "feat(stock-service): add Order model with all order types and flags"
```

---

## Task 2: Define OrderTransaction model

**Files:**
- Create: `stock-service/internal/model/order_transaction.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// OrderTransaction records one executed portion of an order.
// An order may have multiple transactions (partial fills).
type OrderTransaction struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID      uint64          `gorm:"not null;index" json:"order_id"`
	Order        Order           `gorm:"foreignKey:OrderID" json:"-"`
	Quantity     int64           `gorm:"not null" json:"quantity"`
	PricePerUnit decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
	TotalPrice   decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"total_price"`
	ExecutedAt   time.Time       `gorm:"not null" json:"executed_at"`
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/model/order_transaction.go
git commit -m "feat(stock-service): add OrderTransaction model for partial order fills"
```

---

## Task 3: Add order repository interfaces

**Files:**
- Modify: `stock-service/internal/service/interfaces.go`

- [ ] **Step 1: Add order interfaces**

Append to `stock-service/internal/service/interfaces.go`:

```go
type OrderRepo interface {
	Create(order *model.Order) error
	GetByID(id uint64) (*model.Order, error)
	Update(order *model.Order) error
	ListByUser(userID uint64, filter OrderFilter) ([]model.Order, int64, error)
	ListAll(filter OrderFilter) ([]model.Order, int64, error)
	ListActiveApproved() ([]model.Order, error)
}

type OrderFilter struct {
	Status    string // "pending", "approved", "declined", "done"
	Direction string // "buy", "sell"
	OrderType string // "market", "limit", "stop", "stop_limit"
	AgentEmail string // for supervisor view (needs join to user-service, resolved by caller)
	Page      int
	PageSize  int
}

type OrderTransactionRepo interface {
	Create(tx *model.OrderTransaction) error
	ListByOrderID(orderID uint64) ([]model.OrderTransaction, error)
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/interfaces.go
git commit -m "feat(stock-service): add OrderRepo and OrderTransactionRepo interfaces"
```

---

## Task 4: Implement Order repository

**Files:**
- Create: `stock-service/internal/repository/order_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

func (r *OrderRepository) Create(order *model.Order) error {
	return r.db.Create(order).Error
}

func (r *OrderRepository) GetByID(id uint64) (*model.Order, error) {
	var order model.Order
	if err := r.db.Preload("Listing").Preload("Listing.Exchange").First(&order, id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepository) Update(order *model.Order) error {
	result := r.db.Save(order)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *OrderRepository) ListByUser(userID uint64, filter service.OrderFilter) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	q := r.db.Model(&model.Order{}).Where("user_id = ?", userID)
	q = applyOrderFilters(q, filter)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("created_at DESC").Preload("Listing").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (r *OrderRepository) ListAll(filter service.OrderFilter) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	q := r.db.Model(&model.Order{})
	q = applyOrderFilters(q, filter)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Order("created_at DESC").Preload("Listing").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (r *OrderRepository) ListActiveApproved() ([]model.Order, error) {
	var orders []model.Order
	err := r.db.Where("status = ? AND is_done = ?", "approved", false).
		Preload("Listing").Preload("Listing.Exchange").
		Find(&orders).Error
	return orders, err
}

func applyOrderFilters(q *gorm.DB, filter service.OrderFilter) *gorm.DB {
	if filter.Status != "" {
		if filter.Status == "done" {
			q = q.Where("is_done = ?", true)
		} else {
			q = q.Where("status = ?", filter.Status)
		}
	}
	if filter.Direction != "" {
		q = q.Where("direction = ?", filter.Direction)
	}
	if filter.OrderType != "" {
		q = q.Where("order_type = ?", filter.OrderType)
	}
	return q
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/order_repository.go
git commit -m "feat(stock-service): add OrderRepository with user/supervisor list queries"
```

---

## Task 5: Implement OrderTransaction repository

**Files:**
- Create: `stock-service/internal/repository/order_transaction_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

type OrderTransactionRepository struct {
	db *gorm.DB
}

func NewOrderTransactionRepository(db *gorm.DB) *OrderTransactionRepository {
	return &OrderTransactionRepository{db: db}
}

func (r *OrderTransactionRepository) Create(tx *model.OrderTransaction) error {
	return r.db.Create(tx).Error
}

func (r *OrderTransactionRepository) ListByOrderID(orderID uint64) ([]model.OrderTransaction, error) {
	var txns []model.OrderTransaction
	if err := r.db.Where("order_id = ?", orderID).
		Order("executed_at ASC").Find(&txns).Error; err != nil {
		return nil, err
	}
	return txns, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/repository/order_transaction_repository.go
git commit -m "feat(stock-service): add OrderTransactionRepository"
```

---

## Task 6: Create OrderService

**Files:**
- Create: `stock-service/internal/service/order_service.go`

This handles order creation, validation, approval, decline, and cancellation.

- [ ] **Step 1: Write the service**

```go
package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OrderService struct {
	orderRepo    OrderRepo
	txRepo       OrderTransactionRepo
	listingRepo  ListingRepo
	settingRepo  SettingRepo
	securityRepo SecurityLookupRepo // for settlement date validation
	producer     OrderEventPublisher
}

// OrderEventPublisher abstracts Kafka event publishing for orders.
type OrderEventPublisher interface {
	PublishOrderCreated(ctx context.Context, msg interface{}) error
	PublishOrderApproved(ctx context.Context, msg interface{}) error
	PublishOrderDeclined(ctx context.Context, msg interface{}) error
	PublishOrderCancelled(ctx context.Context, msg interface{}) error
}

// SecurityLookupRepo provides settlement date lookups for validation.
type SecurityLookupRepo interface {
	GetFuturesSettlementDate(securityID uint64) (time.Time, error)
	GetOptionSettlementDate(securityID uint64) (time.Time, error)
}

func NewOrderService(
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	securityRepo SecurityLookupRepo,
	producer OrderEventPublisher,
) *OrderService {
	return &OrderService{
		orderRepo:    orderRepo,
		txRepo:       txRepo,
		listingRepo:  listingRepo,
		settingRepo:  settingRepo,
		securityRepo: securityRepo,
		producer:     producer,
	}
}

// CreateOrder validates and creates a new order.
func (s *OrderService) CreateOrder(
	userID uint64,
	systemType string,
	listingID uint64,
	holdingID *uint64,
	direction string,
	orderType string,
	quantity int64,
	limitValue *decimal.Decimal,
	stopValue *decimal.Decimal,
	allOrNone bool,
	margin bool,
	accountID uint64,
) (*model.Order, error) {
	// Validate listing exists
	listing, err := s.listingRepo.GetByID(listingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("listing not found")
		}
		return nil, err
	}

	// Validate order type requires correct fields
	if (orderType == "limit" || orderType == "stop_limit") && limitValue == nil {
		return nil, errors.New("limit_value required for limit/stop_limit orders")
	}
	if (orderType == "stop" || orderType == "stop_limit") && stopValue == nil {
		return nil, errors.New("stop_value required for stop/stop_limit orders")
	}

	// Determine contract size from security type
	contractSize := int64(1) // default for stocks
	switch listing.SecurityType {
	case "futures":
		// Contract size comes from the futures model — for now use listing price context
		contractSize = 1 // adjusted by handler that has security model access
	case "forex":
		contractSize = 1000
	}

	// Calculate price per unit based on order type
	pricePerUnit := listing.Price // default: market price
	if orderType == "limit" || orderType == "stop_limit" {
		if limitValue != nil {
			pricePerUnit = *limitValue
		}
	} else if orderType == "stop" {
		if stopValue != nil {
			pricePerUnit = *stopValue
		}
	}

	// Calculate approximate price
	approxPrice := decimal.NewFromInt(contractSize).Mul(pricePerUnit).Mul(decimal.NewFromInt(quantity))

	// Calculate commission
	commission := calculateCommission(orderType, approxPrice)

	// Check after-hours
	afterHours := false
	if !s.isTestingMode() {
		afterHours = s.isAfterHours(listing)
	}

	// Determine approval
	status := "pending"
	approvedBy := ""
	if systemType == "client" {
		status = "approved"
		approvedBy = "no need for approval"
	}
	// Note: agent limit checks are done by the handler/gateway, which calls
	// user-service to check actuary limits. If auto-approved, status = "approved".

	order := &model.Order{
		UserID:            userID,
		SystemType:        systemType,
		ListingID:         listingID,
		HoldingID:         holdingID,
		SecurityType:      listing.SecurityType,
		Ticker:            "", // set by handler from security model
		Direction:         direction,
		OrderType:         orderType,
		Quantity:          quantity,
		ContractSize:      contractSize,
		PricePerUnit:      pricePerUnit,
		ApproximatePrice:  approxPrice,
		Commission:        commission,
		LimitValue:        limitValue,
		StopValue:         stopValue,
		Status:            status,
		ApprovedBy:        approvedBy,
		IsDone:            false,
		RemainingPortions: quantity,
		AfterHours:        afterHours,
		AllOrNone:         allOrNone,
		Margin:            margin,
		AccountID:         accountID,
		LastModification:  time.Now(),
	}

	if err := s.orderRepo.Create(order); err != nil {
		return nil, err
	}

	// Publish Kafka event after successful creation
	if s.producer != nil {
		go s.producer.PublishOrderCreated(context.Background(), buildOrderEvent(order))
	}

	return order, nil
}

// ApproveOrder sets an order to "approved" status.
func (s *OrderService) ApproveOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.Status != "pending" {
		return nil, errors.New("order is not pending")
	}

	// Check if settlement date has passed (for futures/options)
	if s.isSettlementExpired(order) {
		return nil, errors.New("cannot approve: settlement date has passed")
	}

	order.Status = "approved"
	order.ApprovedBy = supervisorName
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}

	if s.producer != nil {
		go s.producer.PublishOrderApproved(context.Background(), buildOrderEvent(order))
	}

	return order, nil
}

// DeclineOrder sets an order to "declined" status.
func (s *OrderService) DeclineOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.Status != "pending" {
		return nil, errors.New("order is not pending")
	}

	order.Status = "declined"
	order.ApprovedBy = supervisorName
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}

	if s.producer != nil {
		go s.producer.PublishOrderDeclined(context.Background(), buildOrderEvent(order))
	}

	return order, nil
}

// CancelOrder cancels an unfilled (or partially filled) order.
func (s *OrderService) CancelOrder(orderID, userID uint64) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.UserID != userID {
		return nil, errors.New("order does not belong to user")
	}
	if order.IsDone {
		return nil, errors.New("order is already completed")
	}
	if order.Status == "declined" || order.Status == "cancelled" {
		return nil, errors.New("order is already declined/cancelled")
	}

	order.Status = "cancelled"
	order.IsDone = true
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}

	if s.producer != nil {
		go s.producer.PublishOrderCancelled(context.Background(), buildOrderEvent(order))
	}

	return order, nil
}

// GetOrder retrieves an order with ownership check.
func (s *OrderService) GetOrder(orderID, userID uint64) (*model.Order, []model.OrderTransaction, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("order not found")
		}
		return nil, nil, err
	}
	if order.UserID != userID {
		return nil, nil, errors.New("order does not belong to user")
	}

	txns, err := s.txRepo.ListByOrderID(orderID)
	if err != nil {
		return nil, nil, err
	}
	return order, txns, nil
}

// ListMyOrders returns paginated orders for a user.
func (s *OrderService) ListMyOrders(userID uint64, filter OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListByUser(userID, filter)
}

// ListAllOrders returns paginated orders for supervisor view.
func (s *OrderService) ListAllOrders(filter OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListAll(filter)
}

// --- Helpers ---

// calculateCommission computes the order commission.
// Market/Stop: min(14% × price, $7)
// Limit/Stop-Limit: min(24% × price, $12)
func calculateCommission(orderType string, approxPrice decimal.Decimal) decimal.Decimal {
	switch orderType {
	case "limit", "stop_limit":
		pct := approxPrice.Mul(decimal.NewFromFloat(0.24))
		cap := decimal.NewFromFloat(12)
		if pct.LessThan(cap) {
			return pct.Round(2)
		}
		return cap
	default: // "market", "stop"
		pct := approxPrice.Mul(decimal.NewFromFloat(0.14))
		cap := decimal.NewFromFloat(7)
		if pct.LessThan(cap) {
			return pct.Round(2)
		}
		return cap
	}
}

func (s *OrderService) isTestingMode() bool {
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

func (s *OrderService) isAfterHours(listing *model.Listing) bool {
	if listing.Exchange.CloseTime == "" {
		return false
	}
	// Check if less than 4 hours since close
	closeH, closeM := parseTimeHM(listing.Exchange.CloseTime)
	offset := parseTimezoneOffsetSafe(listing.Exchange.TimeZone)
	loc := time.FixedZone("ex", offset*3600)
	now := time.Now().In(loc)

	closeMinutes := closeH*60 + closeM
	nowMinutes := now.Hour()*60 + now.Minute()

	if nowMinutes >= closeMinutes && nowMinutes < closeMinutes+240 {
		return true
	}
	return false
}

func (s *OrderService) isSettlementExpired(order *model.Order) bool {
	if s.securityRepo == nil {
		return false
	}

	var settlementDate time.Time
	var err error

	switch order.SecurityType {
	case "futures":
		settlementDate, err = s.securityRepo.GetFuturesSettlementDate(order.ListingID)
	default:
		return false // stocks and forex have no settlement date
	}

	if err != nil {
		return false // if we can't look up, don't block
	}
	return time.Now().After(settlementDate)
}

// buildOrderEvent creates a Kafka event message from an order.
func buildOrderEvent(order *model.Order) map[string]interface{} {
	return map[string]interface{}{
		"order_id":      order.ID,
		"user_id":       order.UserID,
		"direction":     order.Direction,
		"order_type":    order.OrderType,
		"security_type": order.SecurityType,
		"ticker":        order.Ticker,
		"quantity":       order.Quantity,
		"status":        order.Status,
		"timestamp":     time.Now().Unix(),
	}
}

// parseTimeHM parses "09:30" to (9, 30). Exported for reuse.
func parseTimeHM(t string) (int, int) {
	if len(t) < 5 {
		return 0, 0
	}
	h := int(t[0]-'0')*10 + int(t[1]-'0')
	m := int(t[3]-'0')*10 + int(t[4]-'0')
	return h, m
}

func parseTimezoneOffsetSafe(tz string) int {
	val := 0
	negative := false
	for _, c := range tz {
		if c == '-' {
			negative = true
		} else if c == '+' {
			continue
		} else if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
		}
	}
	if negative {
		val = -val
	}
	return val
}

// Abs returns the absolute value.
func Abs(x int) int {
	return int(math.Abs(float64(x)))
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/order_service.go
git commit -m "feat(stock-service): add OrderService with create, approve, decline, cancel, commission calc"
```

---

## Task 7: Create order execution engine

**Files:**
- Create: `stock-service/internal/service/order_execution.go`

This is the background goroutine that simulates order execution over time.

- [ ] **Step 1: Write the execution engine**

```go
package service

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// OrderFilledPublisher abstracts Kafka event publishing for order fill events.
type OrderFilledPublisher interface {
	PublishOrderFilled(ctx context.Context, msg interface{}) error
}

type OrderExecutionEngine struct {
	orderRepo   OrderRepo
	txRepo      OrderTransactionRepo
	listingRepo ListingRepo
	settingRepo SettingRepo
	producer    OrderFilledPublisher
	mu          sync.Mutex
	activeJobs  map[uint64]context.CancelFunc // orderID -> cancel
}

func NewOrderExecutionEngine(
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	producer OrderFilledPublisher,
) *OrderExecutionEngine {
	return &OrderExecutionEngine{
		orderRepo:   orderRepo,
		txRepo:      txRepo,
		listingRepo: listingRepo,
		settingRepo: settingRepo,
		producer:    producer,
		activeJobs:  make(map[uint64]context.CancelFunc),
	}
}

// Start begins processing all active approved orders.
// Should be called once at startup and whenever an order is approved.
func (e *OrderExecutionEngine) Start(ctx context.Context) {
	// Load all active approved orders and start execution goroutines
	orders, err := e.orderRepo.ListActiveApproved()
	if err != nil {
		log.Printf("WARN: order engine: failed to list active orders: %v", err)
		return
	}

	for _, order := range orders {
		e.StartOrderExecution(ctx, order.ID)
	}
	log.Printf("order engine: started execution for %d active orders", len(orders))
}

// StartOrderExecution launches a background goroutine for a single order.
func (e *OrderExecutionEngine) StartOrderExecution(ctx context.Context, orderID uint64) {
	e.mu.Lock()
	if _, exists := e.activeJobs[orderID]; exists {
		e.mu.Unlock()
		return // already running
	}

	orderCtx, cancel := context.WithCancel(ctx)
	e.activeJobs[orderID] = cancel
	e.mu.Unlock()

	go e.executeOrder(orderCtx, orderID)
}

// StopOrderExecution cancels the execution goroutine for an order.
func (e *OrderExecutionEngine) StopOrderExecution(orderID uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, exists := e.activeJobs[orderID]; exists {
		cancel()
		delete(e.activeJobs, orderID)
	}
}

func (e *OrderExecutionEngine) executeOrder(ctx context.Context, orderID uint64) {
	defer func() {
		e.mu.Lock()
		delete(e.activeJobs, orderID)
		e.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		order, err := e.orderRepo.GetByID(orderID)
		if err != nil {
			log.Printf("WARN: order engine: order %d not found: %v", orderID, err)
			return
		}

		if order.IsDone || order.Status != "approved" {
			return
		}

		remaining := order.RemainingPortions
		if remaining <= 0 {
			e.markDone(order)
			return
		}

		// Check if stop/stop-limit trigger conditions are met
		if !e.isOrderTriggered(order) {
			// Wait and re-check
			select {
			case <-time.After(30 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}

		// Determine portion size
		var portionSize int64
		if order.AllOrNone {
			portionSize = remaining // all at once
		} else {
			portionSize = rand.Int63n(remaining) + 1 // random [1, remaining]
		}

		// Calculate wait time
		// Interval = Random(0, 24*60 / (Volume / remaining)) seconds
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			log.Printf("WARN: order engine: listing %d not found: %v", order.ListingID, err)
			return
		}

		waitSeconds := e.calculateWaitTime(listing.Volume, remaining, order.AfterHours)

		select {
		case <-time.After(time.Duration(waitSeconds) * time.Second):
		case <-ctx.Done():
			return
		}

		// Execute this portion
		execPrice := e.getExecutionPrice(order, listing)

		txn := &model.OrderTransaction{
			OrderID:      orderID,
			Quantity:     portionSize,
			PricePerUnit: execPrice,
			TotalPrice:   execPrice.Mul(decimal.NewFromInt(portionSize)).Mul(decimal.NewFromInt(order.ContractSize)),
			ExecutedAt:   time.Now(),
		}

		if err := e.txRepo.Create(txn); err != nil {
			log.Printf("WARN: order engine: failed to record txn for order %d: %v", orderID, err)
			continue
		}

		// Update order
		order.RemainingPortions -= portionSize
		order.LastModification = time.Now()
		if order.RemainingPortions <= 0 {
			order.IsDone = true
		}

		if err := e.orderRepo.Update(order); err != nil {
			log.Printf("WARN: order engine: failed to update order %d: %v", orderID, err)
		}

		log.Printf("order engine: order %d filled %d/%d at %s",
			orderID, portionSize, order.Quantity, execPrice.StringFixed(4))

		// Publish fill event to Kafka
		if e.producer != nil {
			go e.producer.PublishOrderFilled(context.Background(), map[string]interface{}{
				"order_id":      orderID,
				"user_id":       order.UserID,
				"direction":     order.Direction,
				"security_type": order.SecurityType,
				"ticker":        order.Ticker,
				"filled_qty":    portionSize,
				"remaining_qty": order.RemainingPortions,
				"price":         execPrice.StringFixed(4),
				"total_price":   txn.TotalPrice.StringFixed(4),
				"is_done":       order.IsDone,
				"timestamp":     time.Now().Unix(),
			})
		}

		if order.IsDone {
			return
		}
	}
}

func (e *OrderExecutionEngine) isOrderTriggered(order *model.Order) bool {
	switch order.OrderType {
	case "market", "limit":
		return true // always triggered
	case "stop":
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return false
		}
		if order.StopValue == nil {
			return true
		}
		if order.Direction == "buy" {
			// Buy stop: triggered when ask >= stop value
			return listing.High.GreaterThanOrEqual(*order.StopValue)
		}
		// Sell stop: triggered when bid <= stop value
		return listing.Low.LessThanOrEqual(*order.StopValue)
	case "stop_limit":
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return false
		}
		if order.StopValue == nil {
			return true
		}
		if order.Direction == "buy" {
			return listing.High.GreaterThanOrEqual(*order.StopValue)
		}
		return listing.Low.LessThanOrEqual(*order.StopValue)
	}
	return true
}

func (e *OrderExecutionEngine) getExecutionPrice(order *model.Order, listing *model.Listing) decimal.Decimal {
	switch order.OrderType {
	case "market", "stop":
		if order.Direction == "buy" {
			return listing.High // Ask/High
		}
		return listing.Low // Bid/Low
	case "limit", "stop_limit":
		if order.LimitValue == nil {
			return listing.Price
		}
		if order.Direction == "buy" {
			// Buy limit: execute at min(limit, ask)
			ask := listing.High
			if order.LimitValue.LessThan(ask) {
				return *order.LimitValue
			}
			return ask
		}
		// Sell limit: execute at max(limit, bid)
		bid := listing.Low
		if order.LimitValue.GreaterThan(bid) {
			return *order.LimitValue
		}
		return bid
	}
	return listing.Price
}

func (e *OrderExecutionEngine) calculateWaitTime(volume, remaining int64, afterHours bool) int64 {
	if volume <= 0 {
		volume = 1
	}
	if remaining <= 0 {
		remaining = 1
	}
	maxSeconds := int64(24 * 60 / (float64(volume) / float64(remaining)))
	if maxSeconds <= 0 {
		maxSeconds = 1
	}
	// For testing/dev, cap at 60 seconds to keep things responsive
	if maxSeconds > 60 {
		maxSeconds = 60
	}
	waitSeconds := rand.Int63n(maxSeconds + 1)

	if afterHours {
		waitSeconds += 30 * 60 // add 30 minutes
	}

	return waitSeconds
}

func (e *OrderExecutionEngine) markDone(order *model.Order) {
	order.IsDone = true
	order.LastModification = time.Now()
	if err := e.orderRepo.Update(order); err != nil {
		log.Printf("WARN: order engine: failed to mark order %d done: %v", order.ID, err)
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/order_execution.go
git commit -m "feat(stock-service): add order execution engine with partial fills and trigger logic"
```

---

## Task 8: Create OTC service

**Files:**
- Create: `stock-service/internal/service/otc_service.go`

OTC trading doesn't use a separate model — it queries holdings where `public_quantity > 0` (implemented in Plan 6). For now, we define the service interface. The actual OTC logic depends on the Portfolio/Holdings model from Plan 6.

- [ ] **Step 1: Write the OTC service**

```go
package service

import (
	"errors"
)

// OTCService handles over-the-counter trading.
// OTC offers are portfolio holdings with public_quantity > 0.
// The actual Holding model is defined in Plan 6 (Portfolio).
// This service provides the business logic shell.
type OTCService struct {
	// holdingRepo will be wired in Plan 6
	// orderRepo is used for recording OTC transactions
	orderRepo OrderRepo
	txRepo    OrderTransactionRepo
}

func NewOTCService(orderRepo OrderRepo, txRepo OrderTransactionRepo) *OTCService {
	return &OTCService{
		orderRepo: orderRepo,
		txRepo:    txRepo,
	}
}

// ListOffers returns public holdings available for OTC purchase.
// Stub: will be fully implemented in Plan 6 when Holding model exists.
func (s *OTCService) ListOffers(securityType, ticker string, page, pageSize int) (interface{}, int64, error) {
	// Plan 6 will implement this by querying holdings with public_quantity > 0
	return nil, 0, errors.New("OTC offers: implemented in Plan 6 (Portfolio)")
}

// BuyOffer purchases shares from an OTC offer.
// Stub: will be fully implemented in Plan 6 when Holding model exists.
func (s *OTCService) BuyOffer(offerID, buyerID uint64, systemType string, quantity int64, accountID uint64) (interface{}, error) {
	// Plan 6 will implement the actual transfer logic
	return nil, errors.New("OTC buy: implemented in Plan 6 (Portfolio)")
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/service/otc_service.go
git commit -m "feat(stock-service): add OTC service shell (fully wired in Plan 6)"
```

---

## Task 9: Create OrderGRPCService handler

**Files:**
- Create: `stock-service/internal/handler/order_handler.go`

- [ ] **Step 1: Write the handler**

```go
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type OrderHandler struct {
	pb.UnimplementedOrderGRPCServiceServer
	orderSvc *service.OrderService
	execEngine *service.OrderExecutionEngine
}

func NewOrderHandler(orderSvc *service.OrderService, execEngine *service.OrderExecutionEngine) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc, execEngine: execEngine}
}

func (h *OrderHandler) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.Order, error) {
	var limitVal *decimal.Decimal
	if req.LimitValue != nil {
		v, err := decimal.NewFromString(*req.LimitValue)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid limit_value")
		}
		limitVal = &v
	}

	var stopVal *decimal.Decimal
	if req.StopValue != nil {
		v, err := decimal.NewFromString(*req.StopValue)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid stop_value")
		}
		stopVal = &v
	}

	var holdingID *uint64
	if req.HoldingId > 0 {
		v := req.HoldingId
		holdingID = &v
	}

	order, err := h.orderSvc.CreateOrder(
		req.UserId, req.SystemType, req.ListingId, holdingID,
		req.Direction, req.OrderType, req.Quantity,
		limitVal, stopVal, req.AllOrNone, req.Margin, req.AccountId,
	)
	if err != nil {
		return nil, mapOrderError(err)
	}

	// If auto-approved, start execution
	if order.Status == "approved" {
		h.execEngine.StartOrderExecution(ctx, order.ID)
	}

	return toOrderProto(order), nil
}

func (h *OrderHandler) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.OrderDetail, error) {
	order, txns, err := h.orderSvc.GetOrder(req.Id, req.UserId)
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderDetailProto(order, txns), nil
}

func (h *OrderHandler) ListMyOrders(ctx context.Context, req *pb.ListMyOrdersRequest) (*pb.ListOrdersResponse, error) {
	filter := service.OrderFilter{
		Status:    req.Status,
		Direction: req.Direction,
		OrderType: req.OrderType,
		Page:      int(req.Page),
		PageSize:  int(req.PageSize),
	}
	orders, total, err := h.orderSvc.ListMyOrders(req.UserId, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *OrderHandler) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.CancelOrder(req.Id, req.UserId)
	if err != nil {
		return nil, mapOrderError(err)
	}
	h.execEngine.StopOrderExecution(order.ID)
	return toOrderProto(order), nil
}

func (h *OrderHandler) ListOrders(ctx context.Context, req *pb.ListOrdersRequest) (*pb.ListOrdersResponse, error) {
	filter := service.OrderFilter{
		Status:     req.Status,
		AgentEmail: req.AgentEmail,
		Direction:  req.Direction,
		OrderType:  req.OrderType,
		Page:       int(req.Page),
		PageSize:   int(req.PageSize),
	}
	orders, total, err := h.orderSvc.ListAllOrders(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *OrderHandler) ApproveOrder(ctx context.Context, req *pb.ApproveOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.ApproveOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	// Start execution now that it's approved
	h.execEngine.StartOrderExecution(ctx, order.ID)
	return toOrderProto(order), nil
}

func (h *OrderHandler) DeclineOrder(ctx context.Context, req *pb.DeclineOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.DeclineOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderProto(order), nil
}

// --- Mapping helpers ---

func mapOrderError(err error) error {
	switch err.Error() {
	case "order not found", "listing not found":
		return status.Error(codes.NotFound, err.Error())
	case "order does not belong to user":
		return status.Error(codes.PermissionDenied, err.Error())
	case "order is not pending", "order is already completed",
		"order is already declined/cancelled",
		"cannot approve: settlement date has passed":
		return status.Error(codes.FailedPrecondition, err.Error())
	case "limit_value required for limit/stop_limit orders",
		"stop_value required for stop/stop_limit orders":
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toOrderProto(o *model.Order) *pb.Order {
	order := &pb.Order{
		Id:                o.ID,
		UserId:            o.UserID,
		ListingId:         o.ListingID,
		SecurityType:      o.SecurityType,
		Ticker:            o.Ticker,
		Direction:         o.Direction,
		OrderType:         o.OrderType,
		Quantity:          o.Quantity,
		ContractSize:      o.ContractSize,
		PricePerUnit:      o.PricePerUnit.StringFixed(4),
		ApproximatePrice:  o.ApproximatePrice.StringFixed(4),
		Commission:        o.Commission.StringFixed(2),
		Status:            o.Status,
		ApprovedBy:        o.ApprovedBy,
		IsDone:            o.IsDone,
		RemainingPortions: o.RemainingPortions,
		AfterHours:        o.AfterHours,
		AllOrNone:         o.AllOrNone,
		Margin:            o.Margin,
		AccountId:         o.AccountID,
		LastModification:  o.LastModification.Format("2006-01-02T15:04:05Z"),
		CreatedAt:         o.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if o.HoldingID != nil {
		order.HoldingId = *o.HoldingID
	}
	if o.LimitValue != nil {
		s := o.LimitValue.StringFixed(4)
		order.LimitValue = &s
	}
	if o.StopValue != nil {
		s := o.StopValue.StringFixed(4)
		order.StopValue = &s
	}
	return order
}

func toOrderDetailProto(o *model.Order, txns []model.OrderTransaction) *pb.OrderDetail {
	orderProto := toOrderProto(o)
	txnProtos := make([]*pb.OrderTransaction, len(txns))
	for i, t := range txns {
		txnProtos[i] = &pb.OrderTransaction{
			Id:           t.ID,
			Quantity:     t.Quantity,
			PricePerUnit: t.PricePerUnit.StringFixed(4),
			TotalPrice:   t.TotalPrice.StringFixed(4),
			ExecutedAt:   t.ExecutedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return &pb.OrderDetail{Order: orderProto, Transactions: txnProtos}
}

func toOrderListResponse(orders []model.Order, total int64) *pb.ListOrdersResponse {
	protos := make([]*pb.Order, len(orders))
	for i, o := range orders {
		protos[i] = toOrderProto(&o)
	}
	return &pb.ListOrdersResponse{Orders: protos, TotalCount: total}
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/handler/order_handler.go
git commit -m "feat(stock-service): add OrderGRPCService handler with all order RPCs"
```

---

## Task 10: Create OTCGRPCService handler

**Files:**
- Create: `stock-service/internal/handler/otc_handler.go`

Stub handler that will be fully implemented in Plan 6 when Holdings model exists.

- [ ] **Step 1: Write the handler stub**

```go
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

type OTCHandler struct {
	pb.UnimplementedOTCGRPCServiceServer
	otcSvc *service.OTCService
}

func NewOTCHandler(otcSvc *service.OTCService) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}

func (h *OTCHandler) ListOffers(ctx context.Context, req *pb.ListOTCOffersRequest) (*pb.ListOTCOffersResponse, error) {
	// Stub: fully implemented in Plan 6 (Portfolio & Holdings)
	return nil, status.Error(codes.Unimplemented, "OTC offers: pending Plan 6 implementation")
}

func (h *OTCHandler) BuyOffer(ctx context.Context, req *pb.BuyOTCOfferRequest) (*pb.OTCTransaction, error) {
	// Stub: fully implemented in Plan 6 (Portfolio & Holdings)
	return nil, status.Error(codes.Unimplemented, "OTC buy: pending Plan 6 implementation")
}
```

- [ ] **Step 2: Commit**

```bash
git add stock-service/internal/handler/otc_handler.go
git commit -m "feat(stock-service): add OTC handler stub (implemented in Plan 6)"
```

---

## Task 11: Add Kafka events for orders

**Files:**
- Modify: `contract/kafka/messages.go`
- Modify: `stock-service/internal/kafka/producer.go`

- [ ] **Step 1: Add order event topics and messages**

In `contract/kafka/messages.go`:

```go
const (
	// ... existing topics ...
	TopicOrderCreated  = "stock.order-created"
	TopicOrderApproved = "stock.order-approved"
	TopicOrderDeclined = "stock.order-declined"
	TopicOrderFilled   = "stock.order-filled"
	TopicOrderCancelled = "stock.order-cancelled"
)

type OrderEventMessage struct {
	OrderID      uint64 `json:"order_id"`
	UserID       uint64 `json:"user_id"`
	Direction    string `json:"direction"`
	OrderType    string `json:"order_type"`
	SecurityType string `json:"security_type"`
	Ticker       string `json:"ticker"`
	Quantity     int64  `json:"quantity"`
	Status       string `json:"status"`
	Timestamp    int64  `json:"timestamp"`
}
```

- [ ] **Step 2: Add publish methods**

In `stock-service/internal/kafka/producer.go`:

```go
func (p *Producer) PublishOrderCreated(ctx context.Context, msg contract.OrderEventMessage) error {
	return p.publish(ctx, contract.TopicOrderCreated, msg)
}

func (p *Producer) PublishOrderApproved(ctx context.Context, msg contract.OrderEventMessage) error {
	return p.publish(ctx, contract.TopicOrderApproved, msg)
}

func (p *Producer) PublishOrderDeclined(ctx context.Context, msg contract.OrderEventMessage) error {
	return p.publish(ctx, contract.TopicOrderDeclined, msg)
}

func (p *Producer) PublishOrderFilled(ctx context.Context, msg contract.OrderEventMessage) error {
	return p.publish(ctx, contract.TopicOrderFilled, msg)
}

func (p *Producer) PublishOrderCancelled(ctx context.Context, msg contract.OrderEventMessage) error {
	return p.publish(ctx, contract.TopicOrderCancelled, msg)
}
```

- [ ] **Step 3: Update EnsureTopics**

```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers,
	"stock.security-synced", "stock.listing-updated",
	"stock.order-created", "stock.order-approved", "stock.order-declined",
	"stock.order-filled", "stock.order-cancelled",
)
```

- [ ] **Step 4: Commit**

```bash
git add contract/kafka/messages.go stock-service/internal/kafka/producer.go
git commit -m "feat(stock-service): add Kafka events for order lifecycle"
```

---

## Task 12: Wire orders and OTC into main.go

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Add AutoMigrate for order models**

```go
if err := db.AutoMigrate(
	// ... existing models ...
	&model.Order{},
	&model.OrderTransaction{},
); err != nil {
	log.Fatalf("auto-migrate failed: %v", err)
}
```

- [ ] **Step 2: Create order repos, services, handlers**

```go
// Repositories
orderRepo := repository.NewOrderRepository(db)
orderTxRepo := repository.NewOrderTransactionRepository(db)

// Services
// securityLookup implements SecurityLookupRepo by composing futures + option repos
securityLookup := service.NewSecurityLookupAdapter(futuresRepo, optionRepo)
orderSvc := service.NewOrderService(orderRepo, orderTxRepo, listingRepo, settingRepo, securityLookup, kafkaProducer)
execEngine := service.NewOrderExecutionEngine(orderRepo, orderTxRepo, listingRepo, settingRepo, kafkaProducer)
otcSvc := service.NewOTCService(orderRepo, orderTxRepo)

// Start execution engine for active orders
execEngine.Start(ctx)

// Handlers
orderHandler := handler.NewOrderHandler(orderSvc, execEngine)
pb.RegisterOrderGRPCServiceServer(grpcServer, orderHandler)

otcHandler := handler.NewOTCHandler(otcSvc)
pb.RegisterOTCGRPCServiceServer(grpcServer, otcHandler)
```

- [ ] **Step 3: Verify build**

```bash
cd stock-service && go build ./cmd
```

Expected: BUILD SUCCESS.

- [ ] **Step 4: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): wire order and OTC models, repos, services, execution engine into main"
```

---

## Task 13: Verify full build

- [ ] **Step 1: Run go mod tidy and build**

```bash
cd stock-service && go mod tidy && go build ./...
```

Expected: BUILD SUCCESS.

- [ ] **Step 2: Full repo build**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 3: Commit dependencies**

```bash
git add stock-service/go.sum stock-service/go.mod
git commit -m "chore(stock-service): update dependencies after orders implementation"
```

---

## Design Notes

### Order State Machine

```
                    ┌──────────┐
                    │ Created  │
                    └────┬─────┘
                         │
              ┌──────────┴──────────┐
              │                     │
    Client/Supervisor           Agent
    (auto-approved)           (may need approval)
              │                     │
              ▼              ┌──────┴──────┐
         ┌─────────┐        │   Pending    │
         │ Approved │        └──────┬──────┘
         └────┬────┘         ┌──────┴──────┐
              │              │             │
              │         ┌────▼────┐   ┌────▼─────┐
              │         │Approved │   │ Declined  │
              │         └────┬────┘   └──────────┘
              │              │
              └──────┬───────┘
                     │
              ┌──────▼──────┐
              │  Executing   │ (background goroutine)
              │  (portions)  │
              └──────┬──────┘
              ┌──────┴──────┐
              │             │
        ┌─────▼────┐  ┌────▼─────┐
        │   Done   │  │Cancelled │
        └──────────┘  └──────────┘
```

### Execution Timing Formula

Per spec: `Interval = Random(0, 24 * 60 / (Volume / remaining_quantity))` seconds

For development/testing we cap at 60 seconds max wait to keep things responsive. After-hours orders add 30 minutes per portion.

### Agent Limit Integration

The gateway handler (Plan 1) calls `user-service` ActuaryService to:
1. Check if the agent has `need_approval = true`
2. Check if `used_limit + order_total > limit`
3. If either is true, the order stays in `pending` status

When the order is approved by a supervisor, the gateway calls `stock-service` ApproveOrder, which triggers execution. The agent's `used_limit` is updated by the gateway after order creation.

### Kafka Event Integration

OrderService publishes Kafka events for every state change:
- `stock.order-created` — after successful `CreateOrder`
- `stock.order-approved` — after `ApproveOrder`
- `stock.order-declined` — after `DeclineOrder`
- `stock.order-cancelled` — after `CancelOrder`
- `stock.order-filled` — after each OrderTransaction in the execution engine (published by the engine)

Events are published asynchronously (`go producer.Publish...`) after the DB transaction commits. The `OrderEventPublisher` interface abstracts the Kafka producer for testability.

### Settlement Date Validation

The `SecurityLookupRepo` interface provides settlement date lookups for futures contracts. When approving an order, the service checks if the settlement date has passed — if so, only decline is allowed. The `SecurityLookupAdapter` composes the existing `FuturesRepo` and `OptionRepo` to implement this interface without adding new DB queries.
