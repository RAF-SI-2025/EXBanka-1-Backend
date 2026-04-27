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
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// ExerciseInput captures the parameters of an Exercise call. The caller
// (always the buyer) supplies the buyer-side and seller-side account IDs.
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
	BuyerAccountID  uint64 // buyer's account that pays the strike (and gets shares)
	SellerAccountID uint64 // seller's account that receives the strike funds
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

	// Cross-bank dispatch: hand off to the 5-phase distributed exercise
	// saga when buyer/seller live on different banks.
	if s.crossbankExercise != nil && c.IsCrossBank() {
		return s.crossbankExercise(ctx, in)
	}
	if !c.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("contract has expired (settlement_date <= today)")
	}
	if c.BuyerUserID != in.ActorUserID || c.BuyerSystemType != in.ActorSystemType {
		return nil, errors.New("only the contract buyer can exercise")
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SellerAccountID})
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
	state.Set("step:consume_seller_holding:currency", "shares")

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: "reserve_strike",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, syntheticTxnID, strikeBuyerCcy, buyerCcy)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReleaseReservation(ctx, syntheticTxnID)
				return e
			},
		}).
		Add(saga.Step{
			Name: "settle_strike_buyer",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.PartialSettleReservation(ctx, syntheticTxnID, 1, strikeBuyerCcy, settleMemo)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Reverse buyer debit (credit it back in buyer's currency).
				_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, strikeBuyerCcy, compBuyerMemo, compBuyerKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: "credit_strike_seller",
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
			Name: "consume_seller_holding",
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
	if err := s.holdingRepo.Upsert(&model.Holding{
		UserID:       uint64(c.BuyerUserID),
		SystemType:   c.BuyerSystemType,
		SecurityType: "stock",
		SecurityID:   c.StockID,
		Quantity:     qty,
		AveragePrice: c.StrikePrice,
		AccountID:    in.BuyerAccountID,
	}); err != nil {
		log.Printf("CRITICAL: OTC exercise saga=%s: buyer holding upsert failed (money moved, shares left seller): %v", sagaID, err)
	}

	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExercised
	c.ExercisedAt = &now
	if err := s.contracts.Save(c); err != nil {
		log.Printf("WARN: OTC exercise saga=%s: contract.Save failed: %v", sagaID, err)
	}

	if s.producer != nil {
		payload := kafkamsg.OTCContractExercisedMessage{
			MessageID:         uuid.NewString(),
			OccurredAt:        now.Format(time.RFC3339),
			ContractID:        c.ID,
			Buyer:             kafkamsg.OTCParty{UserID: c.BuyerUserID, SystemType: c.BuyerSystemType},
			Seller:            kafkamsg.OTCParty{UserID: c.SellerUserID, SystemType: c.SellerSystemType},
			StrikeAmountPaid:  strikeSellerCcy.String(),
			SharesTransferred: decimal.NewFromInt(qty).String(),
			ExercisedAt:       now.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractExercised, data)
		}
	}
	return c, nil
}
