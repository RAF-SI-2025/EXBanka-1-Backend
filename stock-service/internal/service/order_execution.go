package service

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	contract "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// OrderFilledPublisher abstracts Kafka event publishing for order fill events.
type OrderFilledPublisher interface {
	PublishOrderFilled(ctx context.Context, msg interface{}) error
	PublishGeneralNotification(ctx context.Context, msg contract.GeneralNotificationMessage) error
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

		// Re-evaluate after-hours against the CURRENT testing-mode setting
		// rather than the value stamped on the order row at placement
		// time. When an admin flips testing_mode=true after an order was
		// queued under real market hours, the stored AfterHours=true
		// would otherwise add +30 min per portion (bug surfaced
		// 2026-05-15: old orders don't fill in test mode).
		afterHours := order.AfterHours
		if afterHours && e.settingRepo != nil {
			if v, _ := e.settingRepo.Get("testing_mode"); v == "true" {
				afterHours = false
			}
		}
		waitSeconds := e.calculateWaitTime(listing.Volume, remaining, afterHours)

		select {
		case <-time.After(time.Duration(waitSeconds) * time.Second):
		case <-ctx.Done():
			return
		}

		// Execute this portion
		execPrice := e.getExecutionPrice(order, listing)

		// Defence in depth: never commit a limit / stop_limit fill that
		// violates the user's price condition. isOrderTriggered should
		// have already filtered this out, but the market quote may have
		// moved during the per-portion wait above, and we re-read the
		// listing in any case. Skip this iteration and try again.
		if !execPriceAllowed(order, execPrice) {
			select {
			case <-time.After(30 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}

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
				"saga_id":      order.SagaID,
				"order_id":     orderID,
				"order_txn_id": txn.ID,
				// Legacy "user_id" key retained for downstream compatibility;
				// owner_type/owner_id are the source of truth.
				"user_id":          model.OwnerIDOrZero(order.OwnerID),
				"owner_type":       string(order.OwnerType),
				"owner_id":         order.OwnerID,
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

			// Emit an in-app notification per fill — partial while the order
			// still has portions remaining, full once it completes. Only
			// client-owned orders get a notification; bank-owned orders have
			// no end user to notify.
			if order.OwnerType == model.OwnerClient && order.OwnerID != nil {
				notifType := "ORDER_PARTIALLY_FILLED"
				data := map[string]string{
					"direction":       order.Direction,
					"filled_quantity": strconv.FormatInt(portionSize, 10),
					"ticker":          order.Ticker,
				}
				if order.IsDone {
					notifType = "ORDER_FILLED"
					data = map[string]string{
						"direction": order.Direction,
						"quantity":  strconv.FormatInt(order.Quantity, 10),
						"ticker":    order.Ticker,
					}
				}
				_ = e.producer.PublishGeneralNotification(e.baseCtx, contract.GeneralNotificationMessage{
					UserID:  *order.OwnerID,
					Type:    notifType,
					Data:    data,
					RefType: "order",
					RefID:   order.ID,
				})
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
	case "market":
		return true
	case "limit":
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return false
		}
		return limitConditionMet(order, listing)
	case "stop":
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return false
		}
		return stopConditionMet(order, listing)
	case "stop_limit":
		listing, err := e.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return false
		}
		// Stop-limit fires only when the stop trigger has been crossed AND
		// the live quote sits on the right side of the limit. Skipping the
		// limit check after a stop trigger let stop-limits fill at any
		// price once the stop was hit — defeating the limit's protection.
		return stopConditionMet(order, listing) && limitConditionMet(order, listing)
	}
	return true
}

// stopConditionMet reports whether the stop trigger price has been crossed for
// a stop / stop_limit order. Buy stops activate when the intraday high reaches
// the stop level; sell stops activate when the intraday low reaches it. A nil
// StopValue is treated as "no stop" (immediately triggered) so the rest of the
// engine behaves as a plain limit.
func stopConditionMet(order *model.Order, listing *model.Listing) bool {
	if order.StopValue == nil {
		return true
	}
	if order.Direction == "buy" {
		return listing.High.GreaterThanOrEqual(*order.StopValue)
	}
	return listing.Low.LessThanOrEqual(*order.StopValue)
}

// limitConditionMet reports whether the live quote satisfies a limit /
// stop_limit order's LimitValue. Buy-limits require ask <= LimitValue
// (we won't pay more than the user authorised); sell-limits require
// bid >= LimitValue (we won't sell for less). A nil LimitValue means the
// order is missing its price condition — treated as "not triggered" so a
// malformed limit never fills.
func limitConditionMet(order *model.Order, listing *model.Listing) bool {
	if order.LimitValue == nil {
		return false
	}
	ask, bid := sideQuotes(listing)
	if order.Direction == "buy" {
		return ask.LessThanOrEqual(*order.LimitValue)
	}
	return bid.GreaterThanOrEqual(*order.LimitValue)
}

// execPriceAllowed reports whether a proposed execution price respects a
// limit / stop_limit order's LimitValue. Market and stop orders fill at any
// price; limit/stop_limit must not cross the user's authorised price.
func execPriceAllowed(order *model.Order, execPrice decimal.Decimal) bool {
	switch order.OrderType {
	case "limit", "stop_limit":
		if order.LimitValue == nil {
			return false
		}
		if order.Direction == "buy" {
			return execPrice.LessThanOrEqual(*order.LimitValue)
		}
		return execPrice.GreaterThanOrEqual(*order.LimitValue)
	}
	return true
}

// sideQuotes returns (ask, bid) for a listing, falling back to listing.Price
// when the side-specific quote is zero — matches the pattern in
// getExecutionPrice so the trigger check and the price calc agree on which
// number is "the current ask" / "the current bid".
func sideQuotes(listing *model.Listing) (decimal.Decimal, decimal.Decimal) {
	ask := listing.High
	if ask.IsZero() {
		ask = listing.Price
	}
	bid := listing.Low
	if bid.IsZero() {
		bid = listing.Price
	}
	return ask, bid
}

// getExecutionPrice returns the price the next fill will execute at: the live
// ask for buys, the live bid for sells. The limit/stop_limit price condition
// is enforced upstream in isOrderTriggered, so getExecutionPrice no longer
// clamps to LimitValue — previously a buy-limit at 100 facing an ask of 120
// returned 100, fabricating a fill at a price no real seller would offer.
// Falls back to listing.Price whenever the side-specific quote is zero,
// matching OrderService.computeNativeReservation so the engine and placement
// saga agree on the price.
func (e *OrderExecutionEngine) getExecutionPrice(order *model.Order, listing *model.Listing) decimal.Decimal {
	ask, bid := sideQuotes(listing)
	if order.Direction == "buy" {
		return ask
	}
	return bid
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
