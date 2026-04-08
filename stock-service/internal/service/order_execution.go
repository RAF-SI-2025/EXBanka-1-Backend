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
	fillHandler FillHandler // processes fills (holdings + account)
	mu          sync.Mutex
	activeJobs  map[uint64]context.CancelFunc // orderID -> cancel
}

func NewOrderExecutionEngine(
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	producer OrderFilledPublisher,
	fillHandler FillHandler,
) *OrderExecutionEngine {
	return &OrderExecutionEngine{
		orderRepo:   orderRepo,
		txRepo:      txRepo,
		listingRepo: listingRepo,
		settingRepo: settingRepo,
		producer:    producer,
		fillHandler: fillHandler,
		activeJobs:  make(map[uint64]context.CancelFunc),
	}
}

// Start begins processing all active approved orders.
// Should be called once at startup and whenever an order is approved.
func (e *OrderExecutionEngine) Start(ctx context.Context) {
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

		// Process fill: update holdings and account balance
		if e.fillHandler != nil {
			if order.Direction == "buy" {
				if fillErr := e.fillHandler.ProcessBuyFill(order, txn); fillErr != nil {
					log.Printf("WARN: order engine: buy fill processing failed for order %d: %v", orderID, fillErr)
				}
			} else {
				if fillErr := e.fillHandler.ProcessSellFill(order, txn); fillErr != nil {
					log.Printf("WARN: order engine: sell fill processing failed for order %d: %v", orderID, fillErr)
				}
			}
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
			go func() {
				_ = e.producer.PublishOrderFilled(context.Background(), map[string]interface{}{
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
			}()
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
			return listing.High.GreaterThanOrEqual(*order.StopValue)
		}
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
			ask := listing.High
			if order.LimitValue.LessThan(ask) {
				return *order.LimitValue
			}
			return ask
		}
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
