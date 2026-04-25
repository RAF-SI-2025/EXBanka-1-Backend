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
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// ExerciseInput captures the parameters of an Exercise call. The caller
// (always the buyer) supplies the buyer-side and seller-side account IDs;
// same-currency-only flow.
type ExerciseInput struct {
	ContractID      uint64
	ActorUserID     int64
	ActorSystemType string
	BuyerAccountID  uint64 // buyer's account that pays the strike (and gets shares)
	SellerAccountID uint64 // seller's account that receives the strike funds
}

// ExerciseContract runs the exercise saga (§6.2 of spec):
//
//  1. validate (caller is buyer; status=ACTIVE; settlement_date in future)
//  2. ReserveFunds on buyer for strike_amount = quantity × strike_price
//  3. PartialSettle on buyer (debit strike from buyer's account)
//  4. CreditAccount on seller (proceeds)
//  5. ConsumeForOTCContract (decrement seller's holding)
//  6. Upsert buyer's holding (+qty)
//  7. Mark contract EXERCISED + publish kafka
//
// Compensations on each post-step failure roll back the prior side effects.
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
	if c.BuyerUserID != in.ActorUserID || c.BuyerSystemType != in.ActorSystemType {
		return nil, errors.New("only the contract buyer can exercise")
	}

	// Resolve accounts.
	buyerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.BuyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	sellerAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SellerAccountID})
	if err != nil {
		return nil, fmt.Errorf("get seller account: %w", err)
	}
	if buyerAcct.CurrencyCode != sellerAcct.CurrencyCode {
		return nil, errors.New("cross-currency OTC exercise not yet supported (TODO)")
	}
	strikeAmt := c.Quantity.Mul(c.StrikePrice)
	strikeCcy := sellerAcct.CurrencyCode

	sagaID := uuid.NewString()
	exec := NewSagaExecutor(s.sagaRepo, sagaID, 0, nil)
	// Synthetic txn ID for ConsumeForOTCContract idempotency: derived from
	// the contract ID so retries land in the same row.
	syntheticTxnID := c.ID + 1_000_000_000_000

	// Step 2: reserve buyer strike funds.
	if err := exec.RunStep(ctx, "reserve_strike", strikeAmt, strikeCcy, nil, func() error {
		_, e := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, syntheticTxnID, strikeAmt, strikeCcy)
		return e
	}); err != nil {
		return nil, err
	}

	// Step 3: settle buyer debit.
	memo := fmt.Sprintf("OTC strike for contract #%d", c.ID)
	if err := exec.RunStep(ctx, "settle_strike_buyer", strikeAmt, strikeCcy, nil, func() error {
		_, e := s.accounts.PartialSettleReservation(ctx, syntheticTxnID, 1, strikeAmt, memo)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "release_strike", func() error {
			_, e := s.accounts.ReleaseReservation(ctx, syntheticTxnID)
			return e
		})
		return nil, err
	}

	// Step 4: credit seller.
	idemSeller := fmt.Sprintf("otc-exercise-%d-seller", c.ID)
	creditMemo := fmt.Sprintf("OTC strike credit for contract #%d", c.ID)
	if err := exec.RunStep(ctx, "credit_strike_seller", strikeAmt, strikeCcy, nil, func() error {
		_, e := s.accounts.CreditAccount(ctx, sellerAcct.AccountNumber, strikeAmt, creditMemo, idemSeller)
		return e
	}); err != nil {
		// Reverse buyer debit (credit it back).
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, strikeAmt,
				fmt.Sprintf("Compensating OTC strike #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-buyer", c.ID))
			return e
		})
		return nil, err
	}

	// Step 5: consume seller's holding reservation (transfer shares out).
	qty := c.Quantity.IntPart()
	if _, err := s.holdingRes.ConsumeForOTCContract(ctx, c.ID, qty, syntheticTxnID); err != nil {
		// Best-effort reverse the seller credit + buyer debit.
		_ = exec.RunCompensation(ctx, 0, "compensate_seller_credit", func() error {
			_, e := s.accounts.DebitAccount(ctx, sellerAcct.AccountNumber, strikeAmt,
				fmt.Sprintf("Compensating OTC strike credit #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-seller", c.ID))
			return e
		})
		_ = exec.RunCompensation(ctx, 0, "compensate_buyer_credit", func() error {
			_, e := s.accounts.CreditAccount(ctx, buyerAcct.AccountNumber, strikeAmt,
				fmt.Sprintf("Compensating OTC strike #%d", c.ID),
				fmt.Sprintf("otc-exercise-%d-comp-buyer", c.ID))
			return e
		})
		return nil, fmt.Errorf("consume seller holding: %w", err)
	}

	// Step 6: upsert buyer's holding (+qty). Best-effort — at this point
	// money + seller-side shares have moved; if the buyer-holding upsert
	// fails we log loud and leave the state for manual reconciliation.
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

	// Step 7: mark contract EXERCISED + publish kafka.
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
			StrikeAmountPaid:  strikeAmt.String(),
			SharesTransferred: decimal.NewFromInt(qty).String(),
			ExercisedAt:       now.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractExercised, data)
		}
	}
	return c, nil
}
