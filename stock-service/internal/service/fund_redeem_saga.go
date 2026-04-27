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
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// RedeemInput captures the parameters of a single Redeem call.
type RedeemInput struct {
	FundID          uint64
	ActorUserID     uint64
	ActorSystemType string
	AmountRSD       decimal.Decimal
	TargetAccountID uint64
	OnBehalfOfType  string // "self" | "bank"
}

// Redeem is the saga that:
//  1. Validates the caller's position can cover the requested AmountRSD.
//  2. Computes a redemption fee (bank pays 0; clients pay fund_redemption_fee_pct).
//  3. Requires fund RSD account balance >= AmountRSD + Fee. Liquidation
//     (selling securities to free cash) is invoked when the fund is short.
//  4. Debits the fund RSD account by AmountRSD + Fee.
//  5. Credits the target account by AmountRSD.
//  6. Credits the bank's RSD account by Fee (when fee > 0).
//  7. Decrements the position by AmountRSD (best-effort, post-saga).
//
// Steps 4-6 run inside saga.Saga so any post-debit failure auto-reverses
// prior steps. Step 7 runs outside the saga because position bookkeeping
// failure must NOT reverse the money flow that already settled — money is
// not recoverable, position rows are.
func (s *FundService) Redeem(ctx context.Context, in RedeemInput) (*model.FundContribution, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.contribs == nil || s.positions == nil {
		return nil, errSagaDepsNotWired
	}
	fund, err := s.repo.GetByID(in.FundID)
	if err != nil {
		return nil, fmt.Errorf("fund not found: %w", err)
	}
	if !fund.Active {
		return nil, errors.New("fund is inactive")
	}
	if in.AmountRSD.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("amount_rsd must be positive")
	}

	posUserID, posSystemType := in.ActorUserID, in.ActorSystemType
	if in.OnBehalfOfType == "bank" {
		posUserID, posSystemType = bankSentinelUserID, "employee"
	}
	pos, err := s.positions.GetByOwner(in.FundID, posUserID, posSystemType)
	if err != nil {
		return nil, fmt.Errorf("no position: %w", err)
	}
	if in.AmountRSD.GreaterThan(pos.TotalContributedRSD) {
		return nil, errors.New("amount_rsd exceeds position contribution (mark-to-market reads not yet wired)")
	}

	feeRSD := decimal.Zero
	if posSystemType == "client" && s.settings != nil {
		feePct, err := s.settings.GetDecimal("fund_redemption_fee_pct")
		if err == nil && !feePct.IsZero() {
			feeRSD = in.AmountRSD.Mul(feePct).Round(4)
		}
	}

	fundAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	if err != nil {
		return nil, fmt.Errorf("get fund account: %w", err)
	}
	cashAvail, _ := decimal.NewFromString(fundAcct.AvailableBalance)
	if cashAvail.LessThan(in.AmountRSD.Add(feeRSD)) {
		// Try the liquidation sub-saga: sell fund securities (FIFO) until
		// cash covers the deficit. Returns ErrInsufficientFundCash if the
		// fund's holdings can't free enough cash within the timeout.
		deficit := in.AmountRSD.Add(feeRSD).Sub(cashAvail)
		if err := s.LiquidateAndAwait(ctx, fund, deficit, "liq-redeem"); err != nil {
			return nil, err
		}
		// Re-fetch cash after liquidation — saga continues against the new balance.
		fundAcct, err = s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
		if err != nil {
			return nil, fmt.Errorf("get fund account post-liquidation: %w", err)
		}
	}

	targetAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.TargetAccountID})
	if err != nil {
		return nil, fmt.Errorf("get target account: %w", err)
	}

	var bankRSDAcctNo string
	if !feeRSD.IsZero() {
		if s.bankRSDAccountFn == nil {
			return nil, errors.New("bank RSD account resolver not wired (required for fee credit)")
		}
		acctNo, _, err := s.bankRSDAccountFn(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve bank RSD account: %w", err)
		}
		bankRSDAcctNo = acctNo
	}

	sagaID := uuid.NewString()

	contrib := &model.FundContribution{
		FundID:                  in.FundID,
		UserID:                  posUserID,
		SystemType:              posSystemType,
		Direction:               model.FundDirectionRedeem,
		AmountNative:            in.AmountRSD,
		NativeCurrency:          "RSD",
		AmountRSD:               in.AmountRSD,
		FeeRSD:                  feeRSD,
		SourceOrTargetAccountID: in.TargetAccountID,
		SagaID:                  sagaID,
		Status:                  model.FundContributionStatusPending,
	}
	if err := s.contribs.Create(contrib); err != nil {
		return nil, err
	}

	debitTotal := in.AmountRSD.Add(feeRSD)

	// Pre-compute idempotency keys + memos shared by forward and backward.
	debitMemo := fmt.Sprintf("Redeem fund #%d (saga=%s)", in.FundID, sagaID)
	debitKey := fmt.Sprintf("redeem-%s-debit-fund", sagaID)
	creditMemo := fmt.Sprintf("Redemption from fund #%d (saga=%s)", in.FundID, sagaID)
	creditKey := fmt.Sprintf("redeem-%s-credit-target", sagaID)
	compFundMemo := fmt.Sprintf("Comp redeem fund saga=%s", sagaID)
	compFundKey := fmt.Sprintf("redeem-%s-comp-fund", sagaID)
	compTargetMemo := fmt.Sprintf("Comp redeem target saga=%s", sagaID)
	compTargetKey := fmt.Sprintf("redeem-%s-comp-target", sagaID)
	feeKey := fmt.Sprintf("redeem-%s-fee", sagaID)
	feeMemo := fmt.Sprintf("Fund redemption fee saga=%s", sagaID)

	state := saga.NewState()
	state.Set("step:debit_fund:amount", debitTotal)
	state.Set("step:debit_fund:currency", "RSD")
	state.Set("step:credit_target:amount", in.AmountRSD)
	state.Set("step:credit_target:currency", "RSD")
	if !feeRSD.IsZero() {
		state.Set("step:credit_bank_fee:amount", feeRSD)
		state.Set("step:credit_bank_fee:currency", "RSD")
	}

	sg := saga.NewSaga(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: "debit_fund",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, fundAcct.AccountNumber, debitTotal, debitMemo, debitKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, debitTotal, compFundMemo, compFundKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: "credit_target",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, targetAcct.AccountNumber, in.AmountRSD, creditMemo, creditKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, targetAcct.AccountNumber, in.AmountRSD, compTargetMemo, compTargetKey)
				return e
			},
		}).
		AddIf(!feeRSD.IsZero(), saga.Step{
			Name: "credit_bank_fee",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, bankRSDAcctNo, feeRSD, feeMemo, feeKey)
				return e
			},
			// Last money step. Nothing after it to fail, so no Backward needed.
		})

	if err := sg.Execute(ctx, state); err != nil {
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	// Position decrement is best-effort: money already moved, and position
	// rows are recoverable from the saga log if this fails. Do NOT include
	// in the saga — a decrement failure must not reverse the redemption.
	if err := s.positions.DecrementContribution(in.FundID, posUserID, posSystemType, in.AmountRSD); err != nil {
		log.Printf("WARN: redeem position decrement failed for saga %s: %v (money already moved)", sagaID, err)
	}

	if err := s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted); err != nil {
		log.Printf("WARN: redeem complete but mark-completed failed: %v", err)
	}
	contrib.Status = model.FundContributionStatusCompleted

	if s.producer != nil {
		payload := kafkamsg.StockFundRedeemedMessage{
			MessageID:       uuid.NewString(),
			OccurredAt:      time.Now().UTC().Format(time.RFC3339),
			FundID:          in.FundID,
			UserID:          posUserID,
			SystemType:      posSystemType,
			AmountRSD:       in.AmountRSD.String(),
			FeeRSD:          feeRSD.String(),
			TargetAccountID: in.TargetAccountID,
			SagaID:          sagaID,
			ContributionID:  contrib.ID,
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicStockFundRedeemed, data)
		}
	}

	return contrib, nil
}

// ErrInsufficientFundCash is returned when a redemption requires more cash
// than the fund's RSD account holds. The follow-up liquidation sub-saga
// (Task 15) sells securities to bridge the gap; until it lands, callers
// receive this sentinel and clients see HTTP 409.
var ErrInsufficientFundCash = errors.New("insufficient_fund_cash")
