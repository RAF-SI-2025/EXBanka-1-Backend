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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	// OnBehalfOfFundID, when non-zero, signals that this exercise is on behalf
	// of a fund (E2, Plan E). The manager-only check is enforced in the handler
	// before this input is constructed. When set, acquired shares land in
	// fund_holdings instead of the buyer's personal holdings.
	OnBehalfOfFundID uint64
}

// ExerciseContract runs the exercise saga (§6.2 of spec):
//
//  1. reserve_strike — ReserveFunds on buyer for strike_amount.
//  2. settle_strike_buyer — PartialSettle on buyer (debit strike).
//  3. credit_strike_seller — CreditAccount on seller (proceeds).
//  4. consume_seller_holding — decrement seller's holding. [Pivot]
//  5. upsert_buyer_holding — credit the buyer with the acquired shares.
//  6. record_seller_strike_gain — write the seller's capital-gain row.
//  7. record_buyer_exercise_cost — write the buyer's capital-gain row.
//  8. mark_contract_exercised — flip contract.Status = exercised.
//  9. publish_otc_exercise_event — Kafka + in-app notifications.
//
// There is NO pivot (removed 2026-05-29). Every state-changing step has an
// inverse Backward, so a failure at any forward step unwinds the whole flow in
// reverse — matching the SAGA spec's symmetric C5…C1. This is safe because
// compensation runs synchronously inside Execute, before ExerciseContract
// returns: the buyer cannot act on the briefly-credited shares mid-flight, so
// returning them to the seller (consume's Backward) and removing them from the
// buyer (upsert's Backward) fully restores prior state. The money steps
// (reserve/settle/credit) already had inverse Backwards. Net invariants: per
// currency SUM(available+reserved) and per symbol SUM(quantity+reserved) are
// unchanged on any Compensated outcome, and the contract stays "active" unless
// the flow Completes.
func (s *OTCOfferService) ExerciseContract(ctx context.Context, in ExerciseInput) (*model.OptionContract, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil || s.holdingRepo == nil {
		return nil, errOTCSagaDepsNotWired
	}

	c, err := s.contracts.GetByID(in.ContractID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.OptionContractStatusActive {
		return nil, status.Error(codes.FailedPrecondition, "contract is not active")
	}

	if !c.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, status.Error(codes.FailedPrecondition, "contract has expired (settlement_date <= today)")
	}
	// Only the contract buyer may exercise. Comparison runs through the
	// (owner_type, owner_id) identity, derived from the actor's legacy pair.
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
	if c.BuyerOwnerType != actorOwnerType || !ownerIDEqual(c.BuyerOwnerID, actorOwnerID) {
		return nil, status.Error(codes.PermissionDenied, "only the contract buyer can exercise")
	}

	if c.BuyerAccountID == 0 || c.SellerAccountID == 0 {
		return nil, status.Error(codes.FailedPrecondition, "contract has no bound accounts")
	}

	sagaID := uuid.NewString()
	sg, state, err := s.buildExerciseSaga(ctx, sagaID, c)
	if err != nil {
		return nil, err
	}
	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}
	return c, nil
}

// buildExerciseSaga assembles the exercise saga for contract c under the given
// sagaID. Pure assembly: it recomputes every derived value (account snapshots,
// strike amounts, FX, seller cost basis, idempotency keys) from the contract
// alone, so crash recovery can rebuild the identical saga from just
// (sagaID, contractID) and re-drive it (RecoverExerciseSaga). Request-time
// gates (status, expiry, actor permission) live in ExerciseContract, not here,
// so recovery is not blocked by a since-expired contract. state["order_id"] is
// set to the contract id so every persisted saga_logs row carries it for the
// recovery lookup.
func (s *OTCOfferService) buildExerciseSaga(ctx context.Context, sagaID string, c *model.OptionContract) (*saga.Saga, *saga.State, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.holdingRes == nil || s.holdingRepo == nil {
		return nil, nil, errOTCSagaDepsNotWired
	}

	// Snapshot the seller's cost basis BEFORE the saga consumes their
	// holding — needed by the post-pivot capital-gain step so the seller's
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
		return nil, nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: c.SellerAccountID})
	if err != nil {
		return nil, nil, fmt.Errorf("get seller account: %w", err)
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
			return nil, nil, errors.New("cross-currency OTC exercise requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: strikeCcy,
			ToCurrency:   buyerCcy,
			Amount:       strikeSellerCcy.String(),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("FX strike convert: %w", err)
		}
		converted, err := decimal.NewFromString(conv.ConvertedAmount)
		if err != nil {
			return nil, nil, fmt.Errorf("FX strike convert: parse %q: %w", conv.ConvertedAmount, err)
		}
		strikeBuyerCcy = converted
	}

	// Deterministic idempotency keys for capital-gain rows so their Backward
	// closures can delete the exact row by key (idempotent on retry).
	sellerStrikeGainKey := fmt.Sprintf("%s:seller-strike-cg", sagaID)

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
	// Contract-scoped marker key making the buyer-credit step idempotent on
	// replay (saga retry / crash recovery). Forward and Backward share it so
	// the credit lands exactly once and its reversal pairs up exactly.
	buyerCreditKey := fmt.Sprintf("otc-exercise-buyer-credit-%d", c.ID)

	// Build buyer holding with metadata (resolved once outside the saga steps
	// to avoid repeated lookups on retry).
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

	state := saga.NewState()
	// Stamp the contract id as order_id on every persisted saga_logs row so
	// crash recovery can rebuild this saga from just (sagaID, contractID).
	state.Set("order_id", c.ID)
	state.Set("step:reserve_strike:amount", strikeBuyerCcy)
	state.Set("step:reserve_strike:currency", buyerCcy)
	state.Set("step:settle_strike_buyer:amount", strikeBuyerCcy)
	state.Set("step:settle_strike_buyer:currency", buyerCcy)
	state.Set("step:credit_strike_seller:amount", strikeSellerCcy)
	state.Set("step:credit_strike_seller:currency", strikeCcy)
	state.Set("step:consume_seller_holding:amount", c.Quantity)

	exercisedAt := time.Now().UTC()

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
			// Pivot removed (2026-05-29): the share transfer is fully reversible
			// within the synchronous saga — compensation runs before Exercise
			// returns, so the buyer cannot act on the shares mid-flight. The
			// backward step returns the consumed shares (and their reservation)
			// to the seller, so a failure in any later step (F5 etc.) fully
			// unwinds rather than leaving money moved without shares.
			Backward: func(ctx context.Context, _ *saga.State) error {
				return s.holdingRes.RestoreForOTCContract(ctx, c.ID, syntheticTxnID)
			},
		}).
		Add(saga.Step{
			Name: saga.StepUpsertBuyerHolding,
			Forward: func(ctx context.Context, _ *saga.State) error {
				// E2: if the contract was placed on behalf of a fund, credit
				// fund_holdings instead of the buyer's personal holdings.
				if c.OnBehalfOfFundID != nil && *c.OnBehalfOfFundID != 0 && s.fundHoldingRepo != nil {
					fh := &model.FundHolding{
						FundID:          *c.OnBehalfOfFundID,
						SecurityType:    "stock",
						SecurityID:      c.StockID,
						Quantity:        qty,
						AveragePriceRSD: c.StrikePrice,
					}
					return s.fundHoldingRepo.UpsertIdempotent(fh, buyerCreditKey)
				}
				return s.holdingRepo.UpsertIdempotent(ctx, buyerHolding, buyerCreditKey)
			},
			// Reverse the buyer credit: remove the acquired shares again so a
			// later-step failure fully unwinds the transfer (pivot removal —
			// 2026-05-29). Mirrors the forward's fund-vs-personal branch; both
			// decrement helpers no-op when the row is absent, so the backward
			// pass is idempotent on retry.
			Backward: func(ctx context.Context, _ *saga.State) error {
				if c.OnBehalfOfFundID != nil && *c.OnBehalfOfFundID != 0 && s.fundHoldingRepo != nil {
					return s.fundHoldingRepo.DecrementForFundSecurityIdempotent(*c.OnBehalfOfFundID, "stock", c.StockID, qty, buyerCreditKey)
				}
				return s.holdingRepo.DecrementForOwnerIdempotent(ctx, c.BuyerOwnerType, c.BuyerOwnerID, "stock", c.StockID, qty, buyerCreditKey)
			},
		}).
		Add(saga.Step{
			Name: saga.StepRecordSellerStrikeGain,
			Forward: func(ctx context.Context, _ *saga.State) error {
				if s.capitalGainRepo == nil || !sellerCostBasisKnown {
					if s.capitalGainRepo != nil && !sellerCostBasisKnown {
						log.Printf("WARN: OTC exercise saga=%s: seller capital gain skipped (cost basis unknown — holding lookup failed pre-saga)", sagaID)
					}
					return nil
				}
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
					TaxYear:          exercisedAt.Year(),
					TaxMonth:         int(exercisedAt.Month()),
					IdempotencyKey:   &sellerStrikeGainKey,
				}
				return s.capitalGainRepo.Create(cg)
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Delete the seller's capital-gain row by its deterministic key.
				// This step is post-pivot: the stocks already transferred, but
				// if a later post-pivot step fails (e.g. mark_contract_exercised)
				// and rollback unwinds through here, the CG row must be removed
				// because the exercise is not yet finalized. Without deletion the
				// accounting ledger permanently over-reports the seller's gain
				// even though the contract was never marked exercised.
				if s.capitalGainRepo == nil {
					return nil
				}
				return s.capitalGainRepo.DeleteByIdempotencyKey(sellerStrikeGainKey)
			},
		}).
		Add(saga.Step{
			Name: saga.StepRecordBuyerExerciseCost,
			Forward: func(ctx context.Context, _ *saga.State) error {
				// The buyer pays the strike price to acquire shares — this is
				// their cost basis, not a "loss" in P/L terms. We record it as
				// a zero-gain entry (buy price = strike price, sell price = 0)
				// so future portfolio analysis can compute realised gain when
				// the buyer eventually sells. This mirrors how PortfolioService
				// records buy-fill cost basis.
				// Note: recording the cost basis as a capital-gain row with
				// TotalGain=0 is consistent with the existing OTC-accept pattern
				// (buyer's premium cost row also has TotalGain negative = cost).
				// Skip if no repo wired.
				return nil
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				return nil
			},
		}).
		Add(saga.Step{
			Name: saga.StepMarkContractExercised,
			Forward: func(ctx context.Context, _ *saga.State) error {
				c.Status = model.OptionContractStatusExercised
				c.ExercisedAt = &exercisedAt
				return s.contracts.Save(c)
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Re-fetch the contract to get the current version before saving.
				// Using the captured `c` directly after a partial Save failure
				// leaves the in-memory Version stale (BeforeUpdate already
				// incremented it), causing the next retry to hit RowsAffected==0
				// (ErrOptimisticLock) and permanently stuck compensation.
				freshC, fetchErr := s.contracts.GetByID(c.ID)
				if fetchErr != nil {
					return fetchErr
				}
				// Restore to active so the exercise can be retried.
				freshC.Status = model.OptionContractStatusActive
				freshC.ExercisedAt = nil
				return s.contracts.Save(freshC)
			},
		}).
		Add(saga.Step{
			Name: saga.StepPublishOTCExercise,
			Forward: func(ctx context.Context, _ *saga.State) error {
				payload := kafkamsg.OTCContractExercisedMessage{
					MessageID:         uuid.NewString(),
					OccurredAt:        exercisedAt.Format(time.RFC3339),
					ContractID:        c.ID,
					Buyer:             kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID},
					Seller:            kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID},
					StrikeAmountPaid:  strikeSellerCcy.String(),
					SharesTransferred: decimal.NewFromInt(qty).String(),
					ExercisedAt:       exercisedAt.Format(time.RFC3339),
				}
				if data, merr := json.Marshal(payload); merr == nil {
					s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractExercised, data, sagaID)
				}
				// In-app notifications — best-effort.
				exData := map[string]string{
					"ticker": c.Ticker, "shares_transferred": decimal.NewFromInt(qty).String(),
					"strike_amount_paid": strikeSellerCcy.String(),
				}
				s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID}, "OTC_CONTRACT_EXERCISED", "otc_contract", c.ID, exData)
				s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID}, "OTC_CONTRACT_EXERCISED", "otc_contract", c.ID, exData)
				return nil
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				// Publish step backward: no-op. Past the pivot, and outbox
				// ensures delivery — we don't need to undo a publish.
				return nil
			},
		})

	return sg, state, nil
}
