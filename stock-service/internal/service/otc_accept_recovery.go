package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

// ownerToLegacy converts a (OwnerType, *ownerID) pair back to the legacy
// (systemType, userID) form the accept saga stamps on the offer's
// LastModifiedBy audit fields. Inverse of model.OwnerFromLegacy. Employee
// on-behalf accepts were already rewritten to the client identity at accept
// time, so the recovered audit shows the client principal — acceptable, since
// the contract is already minted.
func ownerToLegacy(ownerType model.OwnerType, ownerID *uint64) (string, int64) {
	if ownerType == model.OwnerBank {
		return string(model.OwnerBank), 0
	}
	var id int64
	if ownerID != nil {
		id = int64(*ownerID)
	}
	return "client", id
}

// RecoverAcceptSaga auto-resolves a crash-stranded OTC accept saga with no
// human intervention. Called by the saga-recovery reconciler for any stuck
// accept step; idempotent.
//
//   - If no contract was minted for this saga (crash before step 1 committed),
//     nothing was persisted to drive — return nil and let the reconciler mark
//     the row terminal.
//   - Otherwise rebuild the saga from the persisted contract + offer under the
//     same sagaID, then Compensate if it was already rolling back (compensation
//     rows present) else Execute (forward-resume → minted ACTIVE contract). All
//     forward steps are idempotent (idempotency-keyed money + idempotent
//     contract mint + ON CONFLICT capital gains), so replay is safe.
//
// offerID (the stuck row's order_id) is accepted for symmetry/logging; the
// contract is located by sagaID so a partial original run is reused rather than
// duplicated.
func (s *OTCOfferService) RecoverAcceptSaga(ctx context.Context, sagaID string, offerID uint64) error {
	if sagaID == "" {
		return errors.New("recover accept saga: empty sagaID")
	}
	contract, err := s.contracts.GetBySagaID(sagaID)
	if errors.Is(err, gorm.ErrRecordNotFound) || contract == nil {
		// No contract minted — nothing committed to resolve.
		return nil
	}
	if err != nil {
		return fmt.Errorf("recover accept saga %s: load contract: %w", sagaID, err)
	}

	o, err := s.offers.GetByID(contract.OfferID)
	if err != nil {
		return fmt.Errorf("recover accept saga %s: load offer %d: %w", sagaID, contract.OfferID, err)
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: contract.BuyerAccountID})
	if err != nil {
		return fmt.Errorf("recover accept saga %s: get buyer account: %w", sagaID, err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: contract.SellerAccountID})
	if err != nil {
		return fmt.Errorf("recover accept saga %s: get seller account: %w", sagaID, err)
	}

	premiumSellerCcy := contract.PremiumPaid
	premiumCcy := contract.PremiumCurrency
	buyerCcy := buyerAcct.CurrencyCode
	premiumBuyerCcy := premiumSellerCcy
	if buyerCcy != premiumCcy {
		// Recompute the buyer-side amount. Any rate drift versus the original
		// accept is collapsed by the per-step idempotency keys on forward-resume
		// (the original settle/reserve already landed under the same key).
		if s.exchange == nil {
			return fmt.Errorf("recover accept saga %s: cross-currency needs exchange client", sagaID)
		}
		conv, cerr := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: premiumCcy, ToCurrency: buyerCcy, Amount: premiumSellerCcy.String(),
		})
		if cerr != nil {
			return fmt.Errorf("recover accept saga %s: FX convert: %w", sagaID, cerr)
		}
		converted, perr := decimal.NewFromString(conv.ConvertedAmount)
		if perr != nil {
			return fmt.Errorf("recover accept saga %s: FX parse %q: %w", sagaID, conv.ConvertedAmount, perr)
		}
		premiumBuyerCcy = converted
	}

	// The acceptor (whose principal stamps the offer's LastModifiedBy) is the
	// party opposite the initiator: buyer on a sell_initiated offer, seller on a
	// buy_initiated one.
	var actorType string
	var actorID int64
	if o.Direction == model.OTCDirectionSellInitiated {
		actorType, actorID = ownerToLegacy(contract.BuyerOwnerType, contract.BuyerOwnerID)
	} else {
		actorType, actorID = ownerToLegacy(contract.SellerOwnerType, contract.SellerOwnerID)
	}

	sg, state := s.buildAcceptSaga(ctx, sagaID, acceptSagaParams{
		offer: o, contract: contract,
		buyerOwnerType: contract.BuyerOwnerType, buyerOwnerID: contract.BuyerOwnerID,
		sellerOwnerType: contract.SellerOwnerType, sellerOwnerID: contract.SellerOwnerID,
		buyerAccountID: contract.BuyerAccountID, sellerAccountID: contract.SellerAccountID,
		buyerAcctNumber: buyerAcct.AccountNumber, sellerAcctNumber: sellerAcct.AccountNumber,
		premiumSellerCcy: premiumSellerCcy, premiumBuyerCcy: premiumBuyerCcy,
		premiumCcy: premiumCcy, buyerCcy: buyerCcy,
		actorSystemType: actorType, actorUserID: actorID,
		qty: contract.Quantity.IntPart(),
	})

	rollingBack := false
	if chk, ok := s.sagaRepo.(sagaCompensationChecker); ok {
		has, herr := chk.HasCompensations(sagaID)
		if herr != nil {
			return fmt.Errorf("recover accept saga %s: compensation check: %w", sagaID, herr)
		}
		rollingBack = has
	}
	if rollingBack {
		return sg.Compensate(ctx, state)
	}
	return sg.Execute(ctx, state)
}
