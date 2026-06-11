package service

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/shared/orderkind"
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// buildAcceptSaga assembles the OTC contract-formation (accept) saga for the
// given contract under sagaID. Pure assembly: it recomputes every derived value
// (account snapshots, premium amounts, FX, idempotency keys) from the contract
// alone, so crash recovery can rebuild the identical saga from just
// (sagaID, contract) and re-drive it (RecoverAcceptNegotiationSaga). The four
// steps are:
//
//  1. reserve_and_contract — create the contract row (idempotent: only when it
//     does not yet exist) + reserve the seller's underlying shares.
//  2. reserve_premium — reserve the premium on the buyer's account.
//  3. settle_premium_buyer — debit the buyer's reservation (premium).
//  4. credit_premium_seller — credit the premium to the seller.
//
// Every forward step is idempotency-keyed (keyed reserve/settle/credit,
// idempotent contract create), and every state-changing step except the
// terminal credit has an inverse Backward, so a crash mid-accept can be either
// forward-resumed to completion or rolled back (release reservations + delete
// the contract), restoring the seller's shares and the buyer's funds.
//
// On the live first run the contract arrives with ID==0; step 1's Forward
// creates it and stamps state["order_id"]. On recovery the contract is loaded
// first, so ID is already known and order_id is stamped at build time too.
func (s *OTCOfferService) buildAcceptSaga(ctx context.Context, sagaID string, contract *model.OptionContract) (*saga.Saga, *saga.State, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil {
		return nil, nil, errOTCSagaDepsNotWired
	}

	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: contract.BuyerAccountID})
	if err != nil {
		return nil, nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: contract.SellerAccountID})
	if err != nil {
		return nil, nil, fmt.Errorf("get seller account: %w", err)
	}

	// Premium denomination follows the seller's account currency (captured on
	// the contract as PremiumCurrency at mint time). Cross-currency: convert the
	// buyer-side debit to the buyer's currency via exchange-service; the seller
	// credit stays in their currency.
	premiumSellerCcy := contract.PremiumPaid
	premiumCcy := contract.PremiumCurrency
	premiumBuyerCcy := premiumSellerCcy
	buyerCcy := buyerAcct.CurrencyCode
	if buyerCcy != premiumCcy {
		if s.exchange == nil {
			return nil, nil, svcerr.New(codes.Internal, "cross-currency OTC accept requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: premiumCcy, ToCurrency: buyerCcy,
			Amount: premiumSellerCcy.String(),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("FX premium convert: %w", err)
		}
		converted, err := decimal.NewFromString(conv.ConvertedAmount)
		if err != nil {
			return nil, nil, fmt.Errorf("FX premium convert: parse %q: %w", conv.ConvertedAmount, err)
		}
		premiumBuyerCcy = converted
	}

	qty := contract.Quantity.IntPart()

	state := saga.NewState()
	// Stamp the contract id as order_id on every persisted saga_logs row so
	// crash recovery can correlate. On the live first run the contract is not
	// yet created (ID==0); step 1's Forward sets it once the row exists.
	if contract.ID != 0 {
		state.Set("order_id", contract.ID)
	}
	state.Set("step:reserve_and_contract:amount", contract.Quantity)
	state.Set("step:reserve_premium:amount", premiumBuyerCcy)
	state.Set("step:reserve_premium:currency", buyerCcy)
	state.Set("step:settle_premium_buyer:amount", premiumBuyerCcy)
	state.Set("step:settle_premium_buyer:currency", buyerCcy)
	state.Set("step:credit_premium_seller:amount", premiumSellerCcy)
	state.Set("step:credit_premium_seller:currency", premiumCcy)

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: saga.StepReserveAndContract,
			Forward: func(ctx context.Context, st *saga.State) error {
				// Idempotent contract creation: the live first run mints the
				// contract here; a crash-recovery forward-resume finds it
				// already created (ID!=0) and skips the insert.
				createdNow := false
				if contract.ID == 0 {
					if err := s.contracts.Create(contract); err != nil {
						return err
					}
					createdNow = true
				}
				// Now that the contract row exists, stamp order_id so later
				// step rows carry it.
				st.Set("order_id", contract.ID)
				// THIS is the seller-can-deliver check + lock. Reserves the
				// underlying shares on the seller's holding; fails if the
				// seller no longer has enough free shares.
				if _, err := s.holdingRes.ReserveForOTCContract(ctx, contract.SellerOwnerType, contract.SellerOwnerID, "stock", contract.StockID, contract.ID, qty); err != nil {
					// The saga rollback does NOT run step 1's own Backward when
					// its Forward fails (the step never completed), so on the
					// live first run we clean up the orphan contract inline —
					// exactly as the pre-refactor code did. On recovery the
					// contract pre-existed (createdNow==false): a transient
					// reserve error must NOT delete a contract whose other saga
					// steps may already have moved money.
					if createdNow {
						_ = s.contracts.Delete(contract.ID)
					}
					return err
				}
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
				// THIS is the buyer-has-cash check + lock. Reserves the premium
				// on the buyer's account; fails if balance is insufficient.
				_, e := s.accounts.ReserveFunds(ctx, contract.BuyerAccountID, contract.ID, premiumBuyerCcy, buyerCcy,
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
				// order_transaction_id MUST be globally unique (account-service
				// enforces UNIQUE(order_transaction_id) on the settlements
				// table). Derive it from the saga id so it is unique AND
				// deterministic on retry/recovery.
				settleTxnID := computeSettleSeq(sagaID, contract.ID, 0)
				settleMemo := fmt.Sprintf("OTC premium for contract #%d", contract.ID)
				_, e := s.accounts.PartialSettleReservation(ctx, contract.ID, settleTxnID, premiumBuyerCcy, settleMemo,
					saga.IdempotencyKey(sagaID, saga.StepSettlePremiumBuyer), orderkind.OTCPremium)
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
				idemSeller := fmt.Sprintf("otc-accept-neg-%d-seller", contract.ID)
				creditMemo := fmt.Sprintf("OTC premium credit for contract #%d", contract.ID)
				_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, premiumSellerCcy, creditMemo, idemSeller)
				return e
			},
		})

	return sg, state, nil
}
