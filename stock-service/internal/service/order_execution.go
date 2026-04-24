package service

import (
	"context"
	"fmt"
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
	baseCtx     context.Context
	orderRepo   OrderRepo
	txRepo      OrderTransactionRepo
	listingRepo ListingRepo
	settingRepo SettingRepo
	producer    OrderFilledPublisher
	fillHandler FillHandler // processes fills (holdings + account)
	mu          sync.Mutex
	activeJobs  map[uint64]context.CancelFunc // orderID -> cancel
}

// NewOrderExecutionEngine constructs the engine. baseCtx MUST be a long-lived
// context (typically the one created in cmd/main.go from context.Background()).
// It is used as the parent of every order-execution goroutine, decoupling fill
// execution from the lifetime of the gRPC request that triggered it. Fixes
// bug #3 in docs/Bugs.txt.
func NewOrderExecutionEngine(
	baseCtx context.Context,
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	producer OrderFilledPublisher,
	fillHandler FillHandler,
) *OrderExecutionEngine {
	return &OrderExecutionEngine{
		baseCtx:     baseCtx,
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
// The ctx passed in is ignored for goroutine lifetime; the engine uses its
// own baseCtx. The ctx parameter is kept for signature stability.
func (e *OrderExecutionEngine) Start(_ context.Context) {
	orders, err := e.orderRepo.ListActiveApproved()
	if err != nil {
		log.Printf("WARN: order engine: failed to list active orders: %v", err)
		return
	}

	for _, order := range orders {
		e.StartOrderExecution(e.baseCtx, order.ID)
	}
	log.Printf("order engine: started execution for %d active orders", len(orders))
}

// StartOrderExecution launches a background goroutine for a single order.
// The ctx parameter is ignored for the goroutine's lifetime; the goroutine
// always uses a context derived from the engine's baseCtx so it is not
// cancelled when the calling gRPC request ends. Fixes bug #3 in docs/Bugs.txt.
func (e *OrderExecutionEngine) StartOrderExecution(_ context.Context, orderID uint64) {
	e.mu.Lock()
	if _, exists := e.activeJobs[orderID]; exists {
		e.mu.Unlock()
		return // already running
	}

	orderCtx, cancel := context.WithCancel(e.baseCtx)
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

		// Process fill: update holdings and account balance. If the fill saga
		// returns an error we do NOT publish order_filled and do NOT advance the
		// order row — the saga layer handles retries/compensations internally,
		// so the next loop iteration will attempt the fill again. Fixes bug #4h
		// in docs/Bugs.txt.
		if e.fillHandler != nil {
			var fillErr error
			if order.Direction == "buy" {
				fillErr = e.fillHandler.ProcessBuyFill(order, txn)
			} else {
				fillErr = e.fillHandler.ProcessSellFill(order, txn)
			}
			if fillErr != nil {
				log.Printf("WARN: order engine: order %d fill %d failed: %v — will retry next iteration", orderID, txn.ID, fillErr)
				continue
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

		// Publish fill event to Kafka synchronously, AFTER the fill saga has
		// committed and the order row has been updated. Publishing is best-effort
		// observability — a kafka failure does not roll back the fill.
		if e.producer != nil {
			key := fmt.Sprintf("order-fill-%d", txn.ID)
			payload := map[string]any{
				"saga_id":          order.SagaID,
				"order_id":         orderID,
				"order_txn_id":     txn.ID,
				"user_id":          order.UserID,
				"direction":        order.Direction,
				"security_type":    order.SecurityType,
				"ticker":           order.Ticker,
				"filled_qty":       portionSize,
				"remaining_qty":    order.RemainingPortions,
				"price":            execPrice.StringFixed(4),
				"total_price":      txn.TotalPrice.StringFixed(4),
				"native_amount":    decStringPtr(txn.NativeAmount),
				"native_currency":  txn.NativeCurrency,
				"converted_amount": decStringPtr(txn.ConvertedAmount),
				"account_currency": txn.AccountCurrency,
				"fx_rate":          decStringPtr(txn.FxRate),
				"is_done":          order.IsDone,
				"kafka_key":        key,
				"timestamp":        time.Now().Unix(),
			}
			if err := e.producer.PublishOrderFilled(e.baseCtx, payload); err != nil {
				log.Printf("WARN: kafka publish failed for order %d fill %d: %v", orderID, txn.ID, err)
				// Do NOT fail the fill — Kafka is observability, not a
				// correctness gate. A future saga-level publish_kafka step
				// could be used to retry; acceptable for Phase 2.
			}
		}

		if order.IsDone {
			// Drop any slippage/commission buffer left on the reservation now
			// that every fill has settled. Buy-only — sells don't reserve
			// funds. Best-effort: errors are logged, not surfaced, because
			// the trade itself is already complete. A leftover reservation
			// can always be cleaned up on cancel or by the ledger recovery
			// sweep.
			if order.Direction == "buy" {
				if err := e.fillHandler.ReleaseResidualReservation(e.baseCtx, order.ID); err != nil {
					log.Printf("WARN: order engine: release residual reservation for order %d failed: %v", order.ID, err)
				}
			}
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
	// Fall back to listing.Price whenever the side-specific quote (High/ask for
	// buys, Low/bid for sells) is zero — happens on externally-sourced listings
	// where the source has only populated Price. Mirrors the pattern already in
	// OrderService.computeNativeReservation so the engine and placement saga
	// agree on price. Without this, market buys on such listings produced
	// txn.TotalPrice=0, which PartialSettleReservation rejected, and the engine
	// spun on failed fills forever.
	ask := listing.High
	if ask.IsZero() {
		ask = listing.Price
	}
	bid := listing.Low
	if bid.IsZero() {
		bid = listing.Price
	}
	switch order.OrderType {
	case "market", "stop":
		if order.Direction == "buy" {
			return ask
		}
		return bid
	case "limit", "stop_limit":
		if order.LimitValue == nil {
			return listing.Price
		}
		if order.Direction == "buy" {
			if order.LimitValue.LessThan(ask) {
				return *order.LimitValue
			}
			return ask
		}
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
	// Same residual-release hook as the fill loop: see the comment there.
	if order.Direction == "buy" {
		if err := e.fillHandler.ReleaseResidualReservation(e.baseCtx, order.ID); err != nil {
			log.Printf("WARN: order engine: release residual reservation for order %d failed: %v", order.ID, err)
		}
	}
}

// decStringPtr returns a string encoding of a nullable decimal pointer. Empty
// string for nil. Used for Kafka payload construction — consumers treat the
// empty string the same as "field not populated" (e.g. same-currency fills
// leave native_amount / converted_amount / fx_rate empty).
func decStringPtr(p *decimal.Decimal) string {
	if p == nil {
		return ""
	}
	return p.StringFixed(4)
}
