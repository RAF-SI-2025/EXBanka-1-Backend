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
	"github.com/exbanka/stock-service/internal/model"
)

// AcceptInput captures the parameters of an Accept call. AccountIDs are
// passed in by the caller (the gateway resolves them from the user's session
// or from request body); same-currency-only flow until cross-currency lands.
type AcceptInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	BuyerAccountID  uint64 // buyer's account that pays the premium
	SellerAccountID uint64 // seller's account that receives the premium
}

// Accept runs the premium-payment saga (§6.1 of spec):
//
//  1. validate_offer (status, last-mover, settlement-date in future)
//  2. reserve_seller_shares + create OptionContract (single tx)
//  3. ReserveFunds on buyer for the premium
//  4. PartialSettleReservation on buyer (debits the premium)
//  5. CreditAccount on seller for the same premium
//  6. mark_offer_accepted + append revision
//  7. publish kafka
//
// On failure of step N, compensations reverse steps N-1..2. Same-currency
// only: if buyer and seller account currencies differ this returns an error
// for now (TODO: hook in exchange-service.Convert).
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

	// Cross-bank dispatch: if the offer involves a remote bank and a
	// dispatcher is wired, hand off to the 5-phase distributed saga. Same-
	// bank offers fall through to the intra-bank flow below.
	if s.crossbankAccept != nil && s.ownBankCode != "" && model.IsCrossBankOffer(o, s.ownBankCode) {
		return s.crossbankAccept(ctx, in)
	}
	if o.LastModifiedByUserID == in.ActorUserID && o.LastModifiedBySystemType == in.ActorSystemType {
		return nil, errors.New("you cannot accept your own most recent terms")
	}
	if !o.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date is not in the future")
	}

	buyerID, buyerType, sellerID, sellerType := identifyOTCBuyerSeller(o, in.ActorUserID, in.ActorSystemType)

	// Resolve accounts.
	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SellerAccountID})
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
	exec := NewSagaExecutor(s.sagaRepo, sagaID, 0, nil)
	qty := o.Quantity.IntPart()

	// Step 2: create contract + reserve seller's holding.
	contract := &model.OptionContract{
		OfferID: o.ID, BuyerUserID: buyerID, BuyerSystemType: buyerType,
		SellerUserID: sellerID, SellerSystemType: sellerType,
		StockID: o.StockID, Quantity: o.Quantity, StrikePrice: o.StrikePrice,
		PremiumPaid: o.Premium, PremiumCurrency: premiumCcy, StrikeCurrency: premiumCcy,
		SettlementDate: o.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
	}
	_ = premiumSellerCcy // alias used for clarity in the cross-currency math
	if err := exec.RunStep(ctx, "reserve_and_contract", o.Quantity, "shares", nil, func() error {
		if err := s.contracts.Create(contract); err != nil {
			return err
		}
		if _, err := s.holdingRes.ReserveForOTCContract(ctx, uint64(sellerID), sellerType, "stock", o.StockID, contract.ID, qty); err != nil {
			_ = s.contracts.Delete(contract.ID)
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Step 3: reserve premium on buyer (in buyer's currency).
	if err := exec.RunStep(ctx, "reserve_premium", premiumBuyerCcy, buyerCcy, nil, func() error {
		_, e := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			_, _ = s.holdingRes.ReleaseForOTCContract(ctx, contract.ID)
			return s.contracts.Delete(contract.ID)
		})
		return nil, err
	}

	// Step 4: settle premium debit on buyer (in buyer's currency).
	memo := fmt.Sprintf("OTC premium for contract #%d", contract.ID)
	if err := exec.RunStep(ctx, "settle_premium_buyer", premiumBuyerCcy, buyerCcy, nil, func() error {
		_, e := s.accounts.PartialSettleReservation(ctx, contract.ID, 1, premiumBuyerCcy, memo)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_premium", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, contract.ID)
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			_, _ = s.holdingRes.ReleaseForOTCContract(ctx, contract.ID)
			return s.contracts.Delete(contract.ID)
		})
		return nil, err
	}

	// Step 5: credit seller in their currency.
	idemSeller := fmt.Sprintf("otc-accept-%d-seller", contract.ID)
	creditMemo := fmt.Sprintf("OTC premium credit for contract #%d", contract.ID)
	if err := exec.RunStep(ctx, "credit_premium_seller", premiumSellerCcy, premiumCcy, nil, func() error {
		_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, premiumSellerCcy, creditMemo, idemSeller)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, premiumBuyerCcy,
				fmt.Sprintf("Compensating OTC premium #%d", contract.ID),
				fmt.Sprintf("otc-accept-%d-comp-buyer", contract.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "drop_contract", func() error {
			_, _ = s.holdingRes.ReleaseForOTCContract(ctx, contract.ID)
			return s.contracts.Delete(contract.ID)
		})
		return nil, err
	}

	// Step 6: mark offer accepted + append revision.
	revNum, _ := s.revisions.NextRevisionNumber(o.ID)
	o.Status = model.OTCOfferStatusAccepted
	o.LastModifiedByUserID = in.ActorUserID
	o.LastModifiedBySystemType = in.ActorSystemType
	if err := s.offers.Save(o); err != nil {
		log.Printf("WARN: OTC accept saga=%s: offer.Save failed (money already moved): %v", sagaID, err)
	}
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType, Action: model.OTCActionAccept,
	})

	// Step 7: publish kafka.
	if s.producer != nil {
		payload := kafkamsg.OTCContractCreatedMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			ContractID:     contract.ID,
			OfferID:        o.ID,
			Buyer:          kafkamsg.OTCParty{UserID: buyerID, SystemType: buyerType},
			Seller:         kafkamsg.OTCParty{UserID: sellerID, SystemType: sellerType},
			Quantity:       contract.Quantity.String(),
			StrikePrice:    contract.StrikePrice.String(),
			PremiumPaid:    contract.PremiumPaid.String(),
			SettlementDate: contract.SettlementDate.Format("2006-01-02"),
			PremiumPaidAt:  contract.PremiumPaidAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractCreated, data)
		}
	}
	return contract, nil
}

func identifyOTCBuyerSeller(o *model.OTCOffer, actorID int64, actorType string) (buyerID int64, buyerType string, sellerID int64, sellerType string) {
	if o.Direction == model.OTCDirectionSellInitiated {
		sellerID, sellerType = o.InitiatorUserID, o.InitiatorSystemType
		buyerID, buyerType = actorID, actorType
	} else {
		buyerID, buyerType = o.InitiatorUserID, o.InitiatorSystemType
		sellerID, sellerType = actorID, actorType
	}
	return
}
