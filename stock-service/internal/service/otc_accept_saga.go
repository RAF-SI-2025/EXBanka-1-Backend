package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
//  5. mark_offer_accepted — set offer.Status = accepted + append revision.
//  6. record_seller_premium_gain — capital-gain row for the seller's premium income.
//  7. record_buyer_premium_cost — capital-gain row for the buyer's premium cost.
//  8. publish_otc_accepted_event — Kafka publish (via outbox) + in-app notifications.
//
// All eight steps are part of the saga. Steps 5-8 have backward
// compensations so a failure in any of them rolls the saga back cleanly.
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

	// Flags guarded by saga step lifecycle — used by the capital-gain and
	// publish steps that run after the money flow.
	// sellerCGCreated / buyerCGCreated track whether the CG rows were
	// inserted so their Backward can delete them idempotently.
	sellerCGKey := fmt.Sprintf("%s:seller-premium-cg", sagaID)
	buyerCGKey := fmt.Sprintf("%s:buyer-premium-cg", sagaID)

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
			// Last money step before the post-saga operational steps.
			// Backward is a debit to undo the seller credit.
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, sellerAcct.AccountNumber, premiumSellerCcy,
					fmt.Sprintf("Compensating OTC seller premium #%d", contract.ID),
					fmt.Sprintf("otc-accept-%d-comp-seller", contract.ID))
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepMarkOfferAccepted,
			Forward: func(ctx context.Context, _ *saga.State) error {
				revNum, _ := s.revisions.NextRevisionNumber(o.ID)
				o.Status = model.OTCOfferStatusAccepted
				o.LastModifiedByPrincipalType = in.ActorSystemType
				o.LastModifiedByPrincipalID = uint64(in.ActorUserID)
				if err := s.offers.Save(o); err != nil {
					return err
				}
				_ = s.revisions.Append(&model.OTCOfferRevision{
					OfferID: o.ID, RevisionNumber: revNum,
					Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
					ModifiedByPrincipalType: in.ActorSystemType,
					ModifiedByPrincipalID:   uint64(in.ActorUserID),
					Action:                  model.OTCActionAccept,
				})
				return nil
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Re-fetch the offer to get the current version before saving.
				// If we use the captured `o` directly after a failed Save attempt,
				// the in-memory Version has already been incremented by BeforeUpdate,
				// so the next retry's Save hits RowsAffected==0 (ErrOptimisticLock)
				// and the compensation is permanently stuck.
				fresh, fetchErr := s.offers.GetByID(o.ID)
				if fetchErr != nil {
					return fetchErr
				}
				fresh.Status = model.OTCOfferStatusPending
				return s.offers.Save(fresh)
			},
		}).
		Add(saga.Step{
			Name: saga.StepRecordSellerPremiumGain,
			Forward: func(ctx context.Context, _ *saga.State) error {
				if s.capitalGainRepo == nil {
					return nil
				}
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
					IdempotencyKey:   &sellerCGKey,
				}
				return s.capitalGainRepo.Create(writerCG)
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Delete the capital-gain row by its deterministic idempotency
				// key. Without this deletion, the seller's income ledger
				// permanently over-reports the premium even though the money was
				// reversed. DeleteByIdempotencyKey is a no-op when the row
				// doesn't exist, making it safe on retry.
				if s.capitalGainRepo == nil {
					return nil
				}
				return s.capitalGainRepo.DeleteByIdempotencyKey(sellerCGKey)
			},
		}).
		Add(saga.Step{
			Name: saga.StepRecordBuyerPremiumCost,
			Forward: func(ctx context.Context, _ *saga.State) error {
				if s.capitalGainRepo == nil {
					return nil
				}
				now := time.Now()
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
					IdempotencyKey:   &buyerCGKey,
				}
				return s.capitalGainRepo.Create(buyerCG)
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Delete the buyer's capital-gain cost row for the same reason:
				// the premium debit was reversed, so this row should not exist.
				if s.capitalGainRepo == nil {
					return nil
				}
				return s.capitalGainRepo.DeleteByIdempotencyKey(buyerCGKey)
			},
		}).
		Add(saga.Step{
			Name: saga.StepPublishOTCAccepted,
			Forward: func(ctx context.Context, _ *saga.State) error {
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
				if data, merr := json.Marshal(payload); merr == nil {
					s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractCreated, data, sagaID)
				}
				// In-app notifications to both client parties. Best-effort failure
				// does NOT roll back the saga because:
				//   (a) The outbox guarantees Kafka delivery;
				//   (b) In-app notifications are idempotent at the consumer.
				// A failure here means the saga's Execute call returns an error,
				// which DOES trigger compensation — but notifications are
				// best-effort only so we do not return their error.
				ccData := map[string]string{
					"ticker": contract.Ticker, "quantity": contract.Quantity.String(),
					"strike_price": contract.StrikePrice.String(), "premium_paid": contract.PremiumPaid.String(),
				}
				s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)
				s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID}, "OTC_CONTRACT_CREATED", "otc_contract", contract.ID, ccData)
				return nil
			},
			// Publish is a best-effort step; backward is a no-op since
			// the outbox entry (if enqueued) will be superseded by the
			// compensation events that the earlier saga steps emit.
			Backward: func(ctx context.Context, _ *saga.State) error {
				return nil
			},
		})

	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

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
