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

// AcceptInput captures the parameters of an Accept call.
type AcceptInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	// AcceptorAccountID is the accepting party's own account. The other
	// side's account is read from offer.InitiatorAccountID. Which is buyer
	// vs seller is decided by offer.direction.
	AcceptorAccountID uint64
}

// Accept runs the premium-payment saga (§6.1 of spec):
//
//  1. reserve_and_contract — single tx: create OptionContract + reserve
//     seller's holding.
//  2. reserve_premium — ReserveFunds on buyer for the premium.
//  3. settle_premium_buyer — PartialSettleReservation (debits the premium).
//  4. credit_premium_seller — CreditAccount on seller for the premium.
//
// Driven by saga.Saga: each step's Backward handles its own rollback
// when a later step fails. Steps 5 (mark offer accepted) and 6 (publish
// kafka) run AFTER the saga since their failure must not reverse the
// money flow that already settled.
func (s *OTCOfferService) Accept(ctx context.Context, in AcceptInput) (*model.OptionContract, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil {
		return nil, errOTCSagaDepsNotWired
	}

	o, err := s.offers.GetByID(in.OfferID)
	if err != nil {
		return nil, err
	}
	if o.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}

	// Self-accept guard: compare on the principal who last modified the offer.
	// LastModifiedByPrincipal is the audit field carrying the acting user/
	// employee for both same-bank and on-behalf paths.
	if int64(o.LastModifiedByPrincipalID) == in.ActorUserID && o.LastModifiedByPrincipalType == in.ActorSystemType {
		return nil, errors.New("you cannot accept your own most recent terms")
	}
	if !o.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date is not in the future")
	}

	buyerOwnerType, buyerOwnerID, sellerOwnerType, sellerOwnerID := identifyOTCBuyerSellerOwners(o, in.ActorUserID, in.ActorSystemType)

	// The initiator bound their account at create; the acceptor binds theirs
	// now. Which is buyer vs seller follows offer.direction: on a
	// sell_initiated offer the initiator is the seller and the acceptor the
	// buyer; on a buy_initiated offer it is the reverse.
	var buyerAccountID, sellerAccountID uint64
	if o.Direction == model.OTCDirectionSellInitiated {
		sellerAccountID = o.InitiatorAccountID
		buyerAccountID = in.AcceptorAccountID
	} else {
		buyerAccountID = o.InitiatorAccountID
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
	// Premium is denominated in the seller's currency. For cross-currency
	// accepts the buyer-side debit (reserve + settle) runs in the buyer's
	// currency at the live exchange rate; the seller is credited in their
	// currency. Same-currency flows skip the conversion entirely.
	premiumSellerCcy := o.Premium
	premiumCcy := sellerAcct.CurrencyCode
	premiumBuyerCcy := premiumSellerCcy
	buyerCcy := buyerAcct.CurrencyCode
	if buyerCcy != premiumCcy {
		if s.exchange == nil {
			return nil, errors.New("cross-currency OTC accept requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: premiumCcy,
			ToCurrency:   buyerCcy,
			Amount:       premiumSellerCcy.String(),
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
	qty := o.Quantity.IntPart()

	contract := &model.OptionContract{
		OfferID:         o.ID,
		BuyerOwnerType:  buyerOwnerType,
		BuyerOwnerID:    buyerOwnerID,
		SellerOwnerType: sellerOwnerType,
		SellerOwnerID:   sellerOwnerID,
		StockID:         o.StockID, Ticker: o.Ticker, Quantity: o.Quantity, StrikePrice: o.StrikePrice,
		PremiumPaid: o.Premium, PremiumCurrency: premiumCcy, StrikeCurrency: premiumCcy,
		SettlementDate: o.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID:  buyerAccountID,
		SellerAccountID: sellerAccountID,
	}

	// Pre-compute idempotency keys + memos. The contract.ID is unknown
	// until step 1 runs; capture it via a local var the later closures
	// reference. Step 1 always runs first so subsequent forwards/backwards
	// see the populated id.
	settleMemo := "OTC premium for contract"
	idemSeller := ""
	creditMemo := ""

	state := saga.NewState()
	state.Set("step:reserve_and_contract:amount", o.Quantity)
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
				if _, err := s.holdingRes.ReserveForOTCContract(ctx, sellerOwnerType, sellerOwnerID, "stock", o.StockID, contract.ID, qty); err != nil {
					_ = s.contracts.Delete(contract.ID)
					return err
				}
				// Re-derive memos that depend on contract.ID now that we have it.
				settleMemo = fmt.Sprintf("OTC premium for contract #%d", contract.ID)
				idemSeller = fmt.Sprintf("otc-accept-%d-seller", contract.ID)
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
				_, e := s.accounts.ReserveFunds(ctx, buyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium), orderkind.OTCPremium)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.ReleaseReservation(ctx, contract.ID,
					saga.IdempotencyKey(sagaID, saga.StepReservePremium)+":compensate", orderkind.OTCPremium)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepSettlePremiumBuyer,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.PartialSettleReservation(ctx, contract.ID, 1, premiumBuyerCcy, settleMemo,
					saga.IdempotencyKey(sagaID, saga.StepSettlePremiumBuyer), orderkind.OTCPremium)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Settlement debited buyer; reverse with a credit back to
				// the buyer's account (the reservation row is already
				// consumed, so ReleaseReservation no-ops).
				_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, premiumBuyerCcy,
					fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
					fmt.Sprintf("otc-accept-%d-comp-buyer", contract.ID))
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepCreditPremiumSeller,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, premiumSellerCcy, creditMemo, idemSeller)
				return e
			},
			// Last money step in the saga. No Backward needed.
		})

	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

	// Post-saga: mark offer accepted + append revision + publish kafka.
	// These are best-effort because money already moved.
	revNum, _ := s.revisions.NextRevisionNumber(o.ID)
	o.Status = model.OTCOfferStatusAccepted
	// Audit trail: record the principal who accepted (matches the
	// LastModifiedByPrincipal* convention used elsewhere in this service).
	o.LastModifiedByPrincipalType = in.ActorSystemType
	o.LastModifiedByPrincipalID = uint64(in.ActorUserID)
	if err := s.offers.Save(o); err != nil {
		log.Printf("WARN: OTC accept saga=%s: offer.Save failed (money already moved): %v", sagaID, err)
	}
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByPrincipalType: in.ActorSystemType,
		ModifiedByPrincipalID:   uint64(in.ActorUserID),
		Action:                  model.OTCActionAccept,
	})

	// Option premium realised P/L (SecurityType="option"). Writer
	// receives the premium → +TotalGain; buyer pays the premium →
	// −TotalGain. Both are realised at acceptance, NOT at exercise
	// or expiry, so:
	//   - if the option later expires worthless, no further entry is
	//     needed (premium loss/gain already booked correctly);
	//   - if the option is later exercised, the strike-priced stock
	//     transfer realises stock P/L on top via the exercise saga
	//     (writer's stock CG; buyer's later stock sell), giving
	//     correct end-to-end totals.
	// Best-effort: a CG write failure logs WARN and does not reverse
	// the saga (money already moved).
	if s.capitalGainRepo != nil {
		now := time.Now()
		writerCG := &model.CapitalGain{
			OwnerType:        sellerOwnerType,
			OwnerID:          sellerOwnerID,
			OTC:              true,
			SecurityType:     "option",
			Ticker:           contract.Ticker,
			Quantity:         qty,
			BuyPricePerUnit:  decimal.Zero,
			SellPricePerUnit: o.Premium.Div(decimal.NewFromInt(qty)),
			TotalGain:        o.Premium,
			Currency:         premiumCcy,
			AccountID:        sellerAccountID,
			TaxYear:          now.Year(),
			TaxMonth:         int(now.Month()),
		}
		buyerCG := &model.CapitalGain{
			OwnerType:        buyerOwnerType,
			OwnerID:          buyerOwnerID,
			OTC:              true,
			SecurityType:     "option",
			Ticker:           contract.Ticker,
			Quantity:         qty,
			BuyPricePerUnit:  o.Premium.Div(decimal.NewFromInt(qty)),
			SellPricePerUnit: decimal.Zero,
			TotalGain:        o.Premium.Neg(),
			Currency:         premiumCcy,
			AccountID:        buyerAccountID,
			TaxYear:          now.Year(),
			TaxMonth:         int(now.Month()),
		}
		if cgErr := s.capitalGainRepo.Create(writerCG); cgErr != nil {
			log.Printf("WARN: OTC accept saga=%s: writer premium capital gain create failed: %v", sagaID, cgErr)
		}
		if cgErr := s.capitalGainRepo.Create(buyerCG); cgErr != nil {
			log.Printf("WARN: OTC accept saga=%s: buyer premium capital gain create failed: %v", sagaID, cgErr)
		}
	}

	// Post-saga Kafka publish goes through the transactional outbox when
	// wired so a crash between business commit and Kafka send doesn't drop
	// the otc.contract-created event. Falls back to direct PublishRaw
	// when the outbox isn't wired (legacy unit-test paths).
	payload := kafkamsg.OTCContractCreatedMessage{
		MessageID:      uuid.NewString(),
		OccurredAt:     time.Now().UTC().Format(time.RFC3339),
		ContractID:     contract.ID,
		OfferID:        o.ID,
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
	}

	// In-app notifications to both client parties (no-op for bank parties /
	// nil notifier). Best-effort — money already moved.
	ccData := map[string]string{
		"ticker": contract.Ticker, "quantity": contract.Quantity.String(),
		"strike_price": contract.StrikePrice.String(), "premium_paid": contract.PremiumPaid.String(),
	}
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)
	s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)

	return contract, nil
}

// identifyOTCBuyerSellerOwners maps a triggering actor (the principal who
// called Accept) plus the offer's initiator role to the buyer and seller
// owners of the resulting option contract.
func identifyOTCBuyerSellerOwners(o *model.OTCOffer, actorID int64, actorType string) (buyerOwnerType model.OwnerType, buyerOwnerID *uint64, sellerOwnerType model.OwnerType, sellerOwnerID *uint64) {
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(actorID), actorType)
	if o.Direction == model.OTCDirectionSellInitiated {
		sellerOwnerType, sellerOwnerID = o.InitiatorOwnerType, o.InitiatorOwnerID
		buyerOwnerType, buyerOwnerID = actorOwnerType, actorOwnerID
	} else {
		buyerOwnerType, buyerOwnerID = o.InitiatorOwnerType, o.InitiatorOwnerID
		sellerOwnerType, sellerOwnerID = actorOwnerType, actorOwnerID
	}
	return
}
