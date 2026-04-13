package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type OrderService struct {
	orderRepo    OrderRepo
	txRepo       OrderTransactionRepo
	listingRepo  ListingRepo
	settingRepo  SettingRepo
	securityRepo SecurityLookupRepo
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
	actingEmployeeID uint64,
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
		ActingEmployeeID:  actingEmployeeID,
		LastModification:  time.Now(),
	}

	if err := s.orderRepo.Create(order); err != nil {
		return nil, err
	}
	StockOrderTotal.WithLabelValues(orderType, status).Inc()

	// Publish Kafka event after successful creation
	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderCreated(context.Background(), buildOrderEvent(order)) }()
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

	// Check if settlement date has passed (for futures)
	if s.isSettlementExpired(order) {
		return nil, errors.New("cannot approve: settlement date has passed")
	}

	order.Status = "approved"
	order.ApprovedBy = supervisorName
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}
	StockOrderTotal.WithLabelValues(order.OrderType, "approved").Inc()

	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderApproved(context.Background(), buildOrderEvent(order)) }()
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
	StockOrderTotal.WithLabelValues(order.OrderType, "declined").Inc()

	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderDeclined(context.Background(), buildOrderEvent(order)) }()
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
		go func() { _ = s.producer.PublishOrderCancelled(context.Background(), buildOrderEvent(order)) }()
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
func (s *OrderService) ListMyOrders(userID uint64, filter repository.OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListByUser(userID, filter)
}

// ListAllOrders returns paginated orders for supervisor view.
func (s *OrderService) ListAllOrders(filter repository.OrderFilter) ([]model.Order, int64, error) {
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
	closeH, closeM := parseTimeHM(listing.Exchange.CloseTime)
	offset := parseTimezoneOffsetSafe(listing.Exchange.TimeZone)
	loc := time.FixedZone("ex", offset*3600)
	now := time.Now().In(loc)

	closeMinutes := closeH*60 + closeM
	nowMinutes := now.Hour()*60 + now.Minute()

	return nowMinutes >= closeMinutes && nowMinutes < closeMinutes+240
}

func (s *OrderService) isSettlementExpired(order *model.Order) bool {
	if s.securityRepo == nil {
		return false
	}

	switch order.SecurityType {
	case "futures":
		settlementDate, err := s.securityRepo.GetFuturesSettlementDate(order.ListingID)
		if err != nil {
			return false // if we can't look up, don't block
		}
		return time.Now().After(settlementDate)
	default:
		return false // stocks and forex have no settlement date
	}
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
		"quantity":      order.Quantity,
		"status":        order.Status,
		"timestamp":     time.Now().Unix(),
	}
}

// parseTimeHM parses "09:30" to (9, 30).
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
