package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

// FundOrderPlacer is the narrow OrderService surface the liquidation
// sub-saga needs. Tests stub it; production wires the real OrderService
// via WithLiquidation.
type FundOrderPlacer interface {
	CreateOrder(ctx context.Context, req CreateOrderRequest) (*model.Order, error)
}

// liquidationConfig governs how long the sub-saga waits for fund-side sell
// orders to land cash on the fund's RSD account before giving up.
type liquidationConfig struct {
	pollEvery time.Duration
	timeout   time.Duration
}

var defaultLiquidationConfig = liquidationConfig{
	pollEvery: 500 * time.Millisecond,
	timeout:   30 * time.Second,
}

// WithLiquidation wires the order placer used by the liquidation sub-saga.
// Without it, Redeem's insufficient-cash path returns ErrInsufficientFundCash
// directly instead of attempting to free cash by selling fund securities.
func (s *FundService) WithLiquidation(orders FundOrderPlacer) *FundService {
	cp := *s
	cp.orderPlacer = orders
	return &cp
}

// LiquidateAndAwait submits market sell orders against the fund's holdings
// (FIFO by holding created_at) until the projected proceeds cover deficitRSD,
// then polls the fund's RSD account until its available balance has grown by
// at least deficitRSD or the timeout elapses.
//
// The sub-saga is best-effort: holdings whose listing lookup or order
// placement fails are skipped (logged) and the next holding is tried. If at
// the end of the FIFO list the cash has still not reached the target the
// caller (Redeem) returns ErrInsufficientFundCash to the user.
func (s *FundService) LiquidateAndAwait(ctx context.Context, fund *model.InvestmentFund, deficitRSD decimal.Decimal, sagaID string) error {
	if s.orderPlacer == nil || s.holdings == nil || s.listingRepo == nil || s.accounts == nil {
		return errors.New("liquidation deps not wired")
	}
	if !deficitRSD.IsPositive() {
		return nil
	}

	// Snapshot the starting fund balance so we can poll for "increased by
	// at least deficitRSD" without race-vs-other-credits.
	startBal, err := s.fundCashRSD(ctx, fund)
	if err != nil {
		return fmt.Errorf("snapshot fund balance: %w", err)
	}
	target := startBal.Add(deficitRSD)

	holdings, err := s.holdings.ListByFundFIFO(fund.ID)
	if err != nil {
		return fmt.Errorf("list fund holdings: %w", err)
	}
	if len(holdings) == 0 {
		return ErrInsufficientFundCash
	}

	remaining := deficitRSD
	for _, h := range holdings {
		if remaining.LessThanOrEqual(decimal.Zero) {
			break
		}
		listing, err := s.listingRepo.GetBySecurityIDAndType(h.SecurityID, h.SecurityType)
		if err != nil || listing.Price.IsZero() {
			log.Printf("liquidation saga=%s: skip holding %d (listing lookup or zero price): %v", sagaID, h.ID, err)
			continue
		}
		priceRSD := listing.Price
		if listing.Exchange.Currency != "" && listing.Exchange.Currency != "RSD" && s.exchange != nil {
			conv, cerr := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: listing.Exchange.Currency,
				ToCurrency:   "RSD",
				Amount:       priceRSD.String(),
			})
			if cerr == nil {
				if d, perr := decimal.NewFromString(conv.ConvertedAmount); perr == nil {
					priceRSD = d
				}
			}
		}
		if priceRSD.IsZero() {
			continue
		}
		// Quantity to sell from this holding: ceil(remaining / priceRSD), capped
		// at h.Quantity. Round up so partial fills don't undershoot the target.
		qtyDecimal := remaining.Div(priceRSD).Ceil()
		qty := qtyDecimal.IntPart()
		if qty <= 0 {
			qty = 1
		}
		if qty > h.Quantity {
			qty = h.Quantity
		}
		if qty <= 0 {
			continue
		}
		if _, err := s.orderPlacer.CreateOrder(ctx, CreateOrderRequest{
			UserID:           bankSentinelUserID, // re-mapped inside CreateOrder anyway
			SystemType:       "employee",
			ListingID:        listing.ID,
			Direction:        "sell",
			OrderType:        "market",
			Quantity:         qty,
			AccountID:        fund.RSDAccountID,
			ActingEmployeeID: uint64(fund.ManagerEmployeeID),
			OnBehalfOfFundID: fund.ID,
		}); err != nil {
			log.Printf("liquidation saga=%s: place sell order for holding %d failed: %v", sagaID, h.ID, err)
			continue
		}
		// Optimistic projection — actual fill may differ; we re-poll the
		// fund balance below to confirm.
		remaining = remaining.Sub(decimal.NewFromInt(qty).Mul(priceRSD))
	}

	// Poll the fund's RSD account until cash >= target or timeout.
	cfg := defaultLiquidationConfig
	deadline := time.Now().Add(cfg.timeout)
	for {
		bal, err := s.fundCashRSD(ctx, fund)
		if err == nil && bal.GreaterThanOrEqual(target) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrInsufficientFundCash
		}
		select {
		case <-time.After(cfg.pollEvery):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// fundCashRSD reads the fund's RSD account available balance.
func (s *FundService) fundCashRSD(ctx context.Context, fund *model.InvestmentFund) (decimal.Decimal, error) {
	acct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	if err != nil {
		return decimal.Zero, err
	}
	bal, _ := decimal.NewFromString(acct.AvailableBalance)
	return bal, nil
}
