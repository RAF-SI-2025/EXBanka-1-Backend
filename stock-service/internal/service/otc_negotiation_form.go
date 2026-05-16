// Package service — MintContractFromAcceptedNegotiation is the contract-
// formation primitive used by OTCNegotiationService.AcceptNegotiation
// (Phase 9). It mirrors the existing legacy OTCOfferService.Accept saga
// but is driven by an OTCNegotiation snapshot (qty/strike/premium/
// settlement) rather than the parent OTCOffer's posted terms — the two
// can diverge after counter-offers in the parallel-chains model.
//
// Safety guarantees enforced (the user's "cannot sell what you don't
// have / cannot buy if you don't have money" invariant):
//
//   - Step 1 reserve_and_contract: ReserveForOTCContract on the
//     seller's holding atomically — if the seller no longer has the
//     shares, the entire saga aborts and the contract row is deleted
//     in the Backward path.
//   - Step 2 reserve_premium: ReserveFunds on the buyer's account
//     atomically — if the buyer's balance has dropped below the
//     premium, the saga aborts and the seller's share reservation
//     is released.
//   - Step 3 settle_premium_buyer: PartialSettleReservation only
//     succeeds against an existing reservation; can't double-charge.
//   - Step 4 credit_premium_seller: idempotent CreditAccount.
//
// Failure of any step triggers compensation in reverse order. The
// caller (OTCNegotiationService) is responsible for marking the
// negotiation's status (typically to "failed") when this returns an
// error so the front-end sees a coherent state.
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

// MintFromNegotiationInput drives MintContractFromAcceptedNegotiation.
type MintFromNegotiationInput struct {
	Parent             *model.OTCOffer
	Negotiation        *model.OTCNegotiation
	AcceptorOwnerType  model.OwnerType
	AcceptorOwnerID    *uint64
	AcceptorAccountID  uint64
	ActorPrincipalType string
	ActorPrincipalID   uint64
}

// MintContractFromAcceptedNegotiation runs the contract-formation saga
// against a negotiation that has already been state-flipped to
// "accepted" by OTCNegotiationService.AcceptNegotiation. Returns the
// minted OptionContract on success; the caller is responsible for
// any negotiation-status compensation on error.
func (s *OTCOfferService) MintContractFromAcceptedNegotiation(ctx context.Context, in MintFromNegotiationInput) (*model.OptionContract, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil {
		return nil, errOTCSagaDepsNotWired
	}
	parent := in.Parent
	neg := in.Negotiation
	if parent == nil || neg == nil {
		return nil, errors.New("parent and negotiation are required")
	}
	// Settlement must still be in the future. If the parent has aged
	// out between create and accept, refuse to mint.
	if !neg.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date is not in the future")
	}

	// Resolve buyer/seller from parent.Direction. The PARENT POSTER
	// is the side they declared at create (seller for sell_initiated,
	// buyer for buy_initiated); the BIDDER (chain opener) takes the
	// opposite side; the ACCEPTOR is whoever ran AcceptNegotiation —
	// could be either the bidder OR the parent poster.
	posterOwnerType := parent.InitiatorOwnerType
	posterOwnerID := parent.InitiatorOwnerID
	posterAccountID := parent.InitiatorAccountID
	bidderOwnerType := neg.BidderOwnerType
	bidderOwnerID := neg.BidderOwnerID
	bidderAccountID := neg.BidderAccountID

	var (
		buyerOwnerType, sellerOwnerType model.OwnerType
		buyerOwnerID, sellerOwnerID     *uint64
		buyerAccountID, sellerAccountID uint64
	)
	if parent.Direction == model.OTCDirectionSellInitiated {
		// Poster is selling; bidder is buying.
		sellerOwnerType, sellerOwnerID, sellerAccountID = posterOwnerType, posterOwnerID, posterAccountID
		buyerOwnerType, buyerOwnerID, buyerAccountID = bidderOwnerType, bidderOwnerID, bidderAccountID
	} else {
		// Poster is buying; bidder is selling.
		buyerOwnerType, buyerOwnerID, buyerAccountID = posterOwnerType, posterOwnerID, posterAccountID
		sellerOwnerType, sellerOwnerID, sellerAccountID = bidderOwnerType, bidderOwnerID, bidderAccountID
	}
	// The acceptor's account overrides whichever side they're on. The
	// accept TX already verified the acceptor is one of the two
	// parties; here we just bind the acceptor's chosen account to
	// their side.
	if ownerMatches(buyerOwnerType, buyerOwnerID, in.AcceptorOwnerType, in.AcceptorOwnerID) {
		buyerAccountID = in.AcceptorAccountID
	} else {
		sellerAccountID = in.AcceptorAccountID
	}
	if buyerAccountID == 0 || sellerAccountID == 0 {
		return nil, errors.New("both buyer and seller accounts must be bound")
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: buyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: sellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}

	// Premium denomination follows seller's account currency. Cross-
	// currency: convert the buyer-side debit to the buyer's currency
	// via exchange-service; seller credit stays in their currency.
	premiumSellerCcy := neg.Premium
	premiumCcy := sellerAcct.CurrencyCode
	premiumBuyerCcy := premiumSellerCcy
	buyerCcy := buyerAcct.CurrencyCode
	if buyerCcy != premiumCcy {
		if s.exchange == nil {
			return nil, errors.New("cross-currency OTC accept requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: premiumCcy, ToCurrency: buyerCcy,
			Amount: premiumSellerCcy.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("FX premium convert: %w", err)
		}
		converted, err := decimal.NewFromString(conv.ConvertedAmount)
		if err != nil {
			return nil, fmt.Errorf("FX premium convert: parse %q: %w", conv.ConvertedAmount, err)
		}
		premiumBuyerCcy = converted
	}

	sagaID := uuid.NewString()
	qty := neg.Quantity.IntPart()

	contract := &model.OptionContract{
		OfferID:         parent.ID,
		BuyerOwnerType:  buyerOwnerType,
		BuyerOwnerID:    buyerOwnerID,
		SellerOwnerType: sellerOwnerType,
		SellerOwnerID:   sellerOwnerID,
		StockID:         parent.StockID,
		Ticker:          parent.Ticker,
		Quantity:        neg.Quantity,
		StrikePrice:     neg.StrikePrice,
		PremiumPaid:     neg.Premium,
		PremiumCurrency: premiumCcy,
		StrikeCurrency:  premiumCcy,
		SettlementDate:  neg.SettlementDate,
		Status:          model.OptionContractStatusActive,
		SagaID:          sagaID,
		PremiumPaidAt:   time.Now().UTC(),
		BuyerAccountID:  buyerAccountID,
		SellerAccountID: sellerAccountID,
	}

	settleMemo := "OTC premium for contract"
	idemSeller := ""
	creditMemo := ""

	state := saga.NewState()
	state.Set("step:reserve_and_contract:amount", neg.Quantity)
	state.Set("step:reserve_and_contract:currency", "shares")
	state.Set("step:reserve_premium:amount", premiumBuyerCcy)
	state.Set("step:reserve_premium:currency", buyerCcy)
	state.Set("step:settle_premium_buyer:amount", premiumBuyerCcy)
	state.Set("step:settle_premium_buyer:currency", buyerCcy)
	state.Set("step:credit_premium_seller:amount", premiumSellerCcy)
	state.Set("step:credit_premium_seller:currency", premiumCcy)

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: saga.StepReserveAndContract,
			Forward: func(ctx context.Context, _ *saga.State) error {
				if err := s.contracts.Create(contract); err != nil {
					return err
				}
				// THIS is the seller-can-deliver check + lock. Reserves
				// the underlying shares on the seller's holding; fails
				// if the seller no longer has enough free shares.
				if _, err := s.holdingRes.ReserveForOTCContract(ctx, sellerOwnerType, sellerOwnerID, "stock", parent.StockID, contract.ID, qty); err != nil {
					_ = s.contracts.Delete(contract.ID)
					return err
				}
				settleMemo = fmt.Sprintf("OTC premium for contract #%d (negotiation #%d)", contract.ID, neg.ID)
				idemSeller = fmt.Sprintf("otc-accept-neg-%d-seller", contract.ID)
				creditMemo = fmt.Sprintf("OTC premium credit for contract #%d", contract.ID)
				return nil
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, _ = s.holdingRes.ReleaseForOTCContract(ctx, contract.ID)
				return s.contracts.Delete(contract.ID)
			},
		}).
		Add(saga.Step{
			Name: saga.StepReservePremium,
			Forward: func(ctx context.Context, _ *saga.State) error {
				// THIS is the buyer-has-cash check + lock. Reserves
				// the premium on the buyer's account; fails if balance
				// is insufficient.
				_, e := s.accounts.ReserveFunds(ctx, buyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium))
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReleaseReservation(ctx, contract.ID,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium)+":compensate")
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepSettlePremiumBuyer,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.PartialSettleReservation(ctx, contract.ID, 1, premiumBuyerCcy, settleMemo,
					saga.IdempotencyKey(sagaID, saga.StepSettlePremiumBuyer))
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, premiumBuyerCcy,
					fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
					fmt.Sprintf("otc-accept-neg-%d-comp-buyer", contract.ID))
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepCreditPremiumSeller,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, premiumSellerCcy, creditMemo, idemSeller)
				return e
			},
		})

	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

	// Post-saga: publish Kafka + in-app notifications. Best-effort —
	// money already moved.
	payload := kafkamsg.OTCContractCreatedMessage{
		MessageID:      uuid.NewString(),
		OccurredAt:     time.Now().UTC().Format(time.RFC3339),
		ContractID:     contract.ID,
		OfferID:        parent.ID,
		Buyer:          kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID},
		Seller:         kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID},
		Quantity:       contract.Quantity.String(),
		StrikePrice:    contract.StrikePrice.String(),
		PremiumPaid:    contract.PremiumPaid.String(),
		SettlementDate: contract.SettlementDate.Format("2006-01-02"),
		PremiumPaidAt:  contract.PremiumPaidAt.Format(time.RFC3339),
	}
	if data, err := json.Marshal(payload); err == nil {
		s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractCreated, data, sagaID)
	} else {
		log.Printf("WARN: OTC accept(neg=%d) marshal kafka: %v", neg.ID, err)
	}
	ccData := map[string]string{
		"ticker":       contract.Ticker,
		"quantity":     contract.Quantity.String(),
		"strike_price": contract.StrikePrice.String(),
		"premium_paid": contract.PremiumPaid.String(),
	}
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)

	return contract, nil
}
