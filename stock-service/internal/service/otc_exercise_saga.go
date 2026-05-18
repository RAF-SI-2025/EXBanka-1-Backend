package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/orderkind"
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// ExerciseInput captures the parameters of an Exercise call. The caller is
// always the buyer; the buyer- and seller-side account IDs are read straight
// off the contract (bound at accept time).
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
}

// ExerciseContract runs the exercise saga (§6.2 of spec):
//
//  1. reserve_strike — ReserveFunds on buyer for strike_amount.
//  2. settle_strike_buyer — PartialSettle on buyer (debit strike).
//  3. credit_strike_seller — CreditAccount on seller (proceeds).
//  4. consume_seller_holding — decrement seller's holding.
//
// Driven by saga.Saga: each step's Backward handles its own rollback
// when a later step fails. The buyer-holding upsert + contract.Save +
// kafka publish run AFTER the saga because their failure must NOT
// reverse the seller's already-decremented holding (shares moved, money
// settled — only operational reconciliation can recover).
func (s *OTCOfferService) ExerciseContract(ctx context.Context, in ExerciseInput) (*model.OptionContract, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil || s.holdingRepo == nil {
		return nil, errOTCSagaDepsNotWired
	}

	c, err := s.contracts.GetByID(in.ContractID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.OptionContractStatusActive {
		return nil, errors.New("contract is not active")
	}

	if !c.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("contract has expired (settlement_date <= today)")
	}
	// Only the contract buyer may exercise. Comparison runs through the
	// (owner_type, owner_id) identity, derived from the actor's legacy pair.
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
	if c.BuyerOwnerType != actorOwnerType || !ownerIDEqual(c.BuyerOwnerID, actorOwnerID) {
		return nil, errors.New("only the contract buyer can exercise")
	}

	if c.BuyerAccountID == 0 || c.SellerAccountID == 0 {
		return nil, errors.New("contract has no bound accounts")
	}

	// Snapshot the seller's cost basis BEFORE the saga consumes their
	// holding — needed by the post-saga CapitalGain write so the seller's
	// realised P/L on the strike sale is recorded. Done up here (not
	// inside StepConsumeSellerHolding) so a fetch failure can short-
	// circuit the exercise cleanly before any money moves. Skipped when
	// neither the capital-gain repo nor the holdings lookup is wired
	// (legacy test wiring) — the saga still runs, just without the P/L
	// row, matching pre-fix behaviour.
	var sellerCostBasis decimal.Decimal
	sellerCostBasisKnown := false
	if s.capitalGainRepo != nil && s.holdings != nil {
		sellerHolding, herr := s.holdings.GetByOwnerAndSecurity(
			c.SellerOwnerType, c.SellerOwnerID, "stock", c.StockID,
		)
		if herr != nil {
			// Seller's holding row may have already been consumed by a
			// retried exercise; in that case AveragePrice is unknowable
			// and we proceed without writing CapitalGain (logged below).
			log.Printf("WARN: OTC exercise contract=%d: seller holding lookup failed (capital gain will be skipped): %v", c.ID, herr)
		} else if sellerHolding != nil {
			sellerCostBasis = sellerHolding.AveragePrice
			sellerCostBasisKnown = true
		}
	}
	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: c.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: c.SellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
	// Strike is denominated in the seller's currency. For cross-currency
	// exercises the buyer-side debit runs in the buyer's currency at the
	// live rate; the seller is credited in their currency.
	strikeSellerCcy := c.Quantity.Mul(c.StrikePrice)
	strikeCcy := sellerAcct.CurrencyCode
	strikeBuyerCcy := strikeSellerCcy
	buyerCcy := buyerAcct.CurrencyCode
	if buyerCcy != strikeCcy {
		if s.exchange == nil {
			return nil, errors.New("cross-currency OTC exercise requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: strikeCcy,
			ToCurrency:   buyerCcy,
			Amount:       strikeSellerCcy.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("FX strike convert: %w", err)
		}
		converted, err := decimal.NewFromString(conv.ConvertedAmount)
		if err != nil {
			return nil, fmt.Errorf("FX strike convert: parse %q: %w", conv.ConvertedAmount, err)
		}
		strikeBuyerCcy = converted
	}

	sagaID := uuid.NewString()
	// Synthetic txn ID for ConsumeForOTCContract idempotency: derived from
	// the contract ID so retries land in the same row.
	syntheticTxnID := c.ID + 1_000_000_000_000
	qty := c.Quantity.IntPart()

	settleMemo := fmt.Sprintf("OTC strike for contract #%d", c.ID)
	idemSeller := fmt.Sprintf("otc-exercise-%d-seller", c.ID)
	creditMemo := fmt.Sprintf("OTC strike credit for contract #%d", c.ID)
	compBuyerMemo := fmt.Sprintf("Compensating OTC strike #%d", c.ID)
	compBuyerKey := fmt.Sprintf("otc-exercise-%d-comp-buyer", c.ID)
	compSellerMemo := fmt.Sprintf("Compensating OTC strike credit #%d", c.ID)
	compSellerKey := fmt.Sprintf("otc-exercise-%d-comp-seller", c.ID)

	state := saga.NewState()
	state.Set("step:reserve_strike:amount", strikeBuyerCcy)
	state.Set("step:reserve_strike:currency", buyerCcy)
	state.Set("step:settle_strike_buyer:amount", strikeBuyerCcy)
	state.Set("step:settle_strike_buyer:currency", buyerCcy)
	state.Set("step:credit_strike_seller:amount", strikeSellerCcy)
	state.Set("step:credit_strike_seller:currency", strikeCcy)
	state.Set("step:consume_seller_holding:amount", c.Quantity)

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: saga.StepReserveStrike,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReserveFunds(ctx, c.BuyerAccountID, syntheticTxnID, strikeBuyerCcy, buyerCcy,
					saga.IdempotencyKey(sagaID, saga.StepReserveStrike), orderkind.OTCStrike)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReleaseReservation(ctx, syntheticTxnID,
					saga.IdempotencyKey(sagaID, saga.StepReserveStrike)+":compensate", orderkind.OTCStrike)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepSettleStrikeBuyer,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.PartialSettleReservation(ctx, syntheticTxnID, 1, strikeBuyerCcy, settleMemo,
					saga.IdempotencyKey(sagaID, saga.StepSettleStrikeBuyer), orderkind.OTCStrike)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Reverse buyer debit (credit it back in buyer's currency).
				_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, strikeBuyerCcy, compBuyerMemo, compBuyerKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepCreditStrikeSeller,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, strikeSellerCcy, creditMemo, idemSeller)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, sellerAcct.AccountNumber, strikeSellerCcy, compSellerMemo, compSellerKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepConsumeSellerHolding,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.holdingRes.ConsumeForOTCContract(ctx, c.ID, qty, syntheticTxnID)
				return e
			},
			// Pivot: shares physically moved between portfolios. The
			// post-saga buyer-holding upsert + contract.Save are
			// best-effort and must NOT trigger a rollback past this step.
			Pivot: true,
		})

	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

	// Post-saga best-effort: buyer holding upsert + contract.Save + publish.
	// At this point money + seller-side shares have moved; failure here is
	// logged loud and left for manual reconciliation.
	//
	// Fix 2026-05-16: previously omitted Ticker / Name / ListingID, which
	// left the buyer-credit holding with empty display fields — making it
	// invisible/untradeable in the FE (no ticker → can't construct a sell
	// order or render in the holdings list, can't make-public on the
	// downstream OTC stock marketplace because lookups go through the
	// ticker). Resolve the metadata now via stockMeta (optional dep);
	// when unwired, only Ticker (from the contract) is populated and
	// Name/ListingID stay empty — same as before but at least the row
	// has its ticker now.
	buyerHolding := &model.Holding{
		OwnerType:    c.BuyerOwnerType,
		OwnerID:      c.BuyerOwnerID,
		SecurityType: "stock",
		SecurityID:   c.StockID,
		Ticker:       c.Ticker,
		Quantity:     qty,
		AveragePrice: c.StrikePrice,
		AccountID:    c.BuyerAccountID,
	}
	if s.stockMeta != nil {
		if stk, gerr := s.stockMeta.GetStockByID(c.StockID); gerr == nil && stk != nil {
			buyerHolding.Name = stk.Name
			// Defensive: trust the contract's ticker but if it's empty
			// (older contract row), fall back to the stock's ticker.
			if buyerHolding.Ticker == "" {
				buyerHolding.Ticker = stk.Ticker
			}
		} else if gerr != nil {
			log.Printf("WARN: OTC exercise saga=%s: stockMeta.GetStockByID(%d) failed (name will be blank): %v", sagaID, c.StockID, gerr)
		}
		if lst, lerr := s.stockMeta.GetListingBySecurityIDAndType(c.StockID, "stock"); lerr == nil && lst != nil {
			buyerHolding.ListingID = lst.ID
		} else if lerr != nil {
			log.Printf("WARN: OTC exercise saga=%s: stockMeta.GetListingBySecurityIDAndType(%d) failed (listing_id will be 0): %v", sagaID, c.StockID, lerr)
		}
	}
	if err := s.holdingRepo.Upsert(ctx, buyerHolding); err != nil {
		log.Printf("CRITICAL: OTC exercise saga=%s: buyer holding upsert failed (money moved, shares left seller): %v", sagaID, err)
	}

	// Seller-side realised P/L: strike price × qty - cost basis × qty.
	// Mirrors PortfolioService.recordCapitalGain and OTCService.BuyOffer's
	// capital-gain emission so a user who buys at X and writes a call
	// that gets exercised at Y sees the (Y - X) gain in their portfolio
	// summary. Best-effort: a failure here does NOT reverse the saga
	// (shares + strike money have already moved).
	if s.capitalGainRepo != nil && sellerCostBasisKnown {
		gain := c.StrikePrice.Sub(sellerCostBasis).Mul(decimal.NewFromInt(qty))
		cg := &model.CapitalGain{
			OwnerType:        c.SellerOwnerType,
			OwnerID:          c.SellerOwnerID,
			OTC:              true,
			SecurityType:     "stock",
			Ticker:           c.Ticker,
			Quantity:         qty,
			BuyPricePerUnit:  sellerCostBasis,
			SellPricePerUnit: c.StrikePrice,
			TotalGain:        gain,
			Currency:         c.StrikeCurrency,
			AccountID:        c.SellerAccountID,
			TaxYear:          time.Now().Year(),
			TaxMonth:         int(time.Now().Month()),
		}
		if cgErr := s.capitalGainRepo.Create(cg); cgErr != nil {
			log.Printf("WARN: OTC exercise saga=%s: seller capital gain create failed (money/shares already moved): %v", sagaID, cgErr)
		}
	} else if s.capitalGainRepo != nil && !sellerCostBasisKnown {
		log.Printf("WARN: OTC exercise saga=%s: seller capital gain skipped (cost basis unknown — holding lookup failed pre-saga)", sagaID)
	}

	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExercised
	c.ExercisedAt = &now
	if err := s.contracts.Save(c); err != nil {
		log.Printf("WARN: OTC exercise saga=%s: contract.Save failed: %v", sagaID, err)
	}

	// Post-saga Kafka publish via outbox when wired so a crash between
	// business commit and Kafka send doesn't drop the
	// otc.contract-exercised event. Falls back to direct PublishRaw when
	// the outbox isn't wired.
	payload := kafkamsg.OTCContractExercisedMessage{
		MessageID:         uuid.NewString(),
		OccurredAt:        now.Format(time.RFC3339),
		ContractID:        c.ID,
		Buyer:             kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID},
		Seller:            kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID},
		StrikeAmountPaid:  strikeSellerCcy.String(),
		SharesTransferred: decimal.NewFromInt(qty).String(),
		ExercisedAt:       now.Format(time.RFC3339),
	}
	if data, err := json.Marshal(payload); err == nil {
		s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractExercised, data, sagaID)
	}

	// In-app notifications to both client parties (no-op for bank parties /
	// nil notifier). Best-effort — money + shares already moved.
	exData := map[string]string{
		"ticker": c.Ticker, "shares_transferred": decimal.NewFromInt(qty).String(),
		"strike_amount_paid": strikeSellerCcy.String(),
	}
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID}, "OTC_CONTRACT_EXERCISED", "otc_contract", c.ID, exData)
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID}, "OTC_CONTRACT_EXERCISED", "otc_contract", c.ID, exData)

	return c, nil
}
