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
//     (selling securities to free cash) is a follow-up — until it lands,
//     this method rejects with ErrInsufficientFundCash.
//  4. Debits the fund RSD account by AmountRSD + Fee.
//  5. Credits the target account by AmountRSD - 0 (full amount; fee comes off
//     the fund, not the target — matches the design of "user receives the net
//     value of their share, fee is the fund's cost").
//  6. Credits the bank's RSD account by Fee (when fee > 0).
//  7. Decrements the position by AmountRSD.
//
// On any post-debit failure, compensations reverse the prior credits/debits.
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
		return nil, ErrInsufficientFundCash
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
	exec := NewSagaExecutor(s.sagaRepo, sagaID, 0, nil)

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
	debitMemo := fmt.Sprintf("Redeem fund #%d (saga=%s)", in.FundID, sagaID)
	debitKey := fmt.Sprintf("redeem-%s-debit-fund", sagaID)
	if err := exec.RunStep(ctx, "debit_fund", debitTotal, "RSD", nil, func() error {
		_, e := s.accounts.DebitAccount(ctx, fundAcct.AccountNumber, debitTotal, debitMemo, debitKey)
		return e
	}); err != nil {
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	creditMemo := fmt.Sprintf("Redemption from fund #%d (saga=%s)", in.FundID, sagaID)
	creditKey := fmt.Sprintf("redeem-%s-credit-target", sagaID)
	if err := exec.RunStep(ctx, "credit_target", in.AmountRSD, "RSD", nil, func() error {
		_, e := s.accounts.CreditAccount(ctx, targetAcct.AccountNumber, in.AmountRSD, creditMemo, creditKey)
		return e
	}); err != nil {
		_ = exec.RunCompensation(ctx, 0, "comp_credit_fund", func() error {
			_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, debitTotal,
				fmt.Sprintf("Comp redeem fund saga=%s", sagaID),
				fmt.Sprintf("redeem-%s-comp-fund", sagaID))
			return e
		})
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}

	if !feeRSD.IsZero() {
		feeKey := fmt.Sprintf("redeem-%s-fee", sagaID)
		if err := exec.RunStep(ctx, "credit_bank_fee", feeRSD, "RSD", nil, func() error {
			_, e := s.accounts.CreditAccount(ctx, bankRSDAcctNo, feeRSD,
				fmt.Sprintf("Fund redemption fee saga=%s", sagaID), feeKey)
			return e
		}); err != nil {
			// Best-effort rollback of target credit + fund debit. If any
			// compensation fails we leave breadcrumbs in saga_logs for
			// recovery and mark contribution failed.
			_ = exec.RunCompensation(ctx, 0, "comp_debit_target", func() error {
				_, e := s.accounts.DebitAccount(ctx, targetAcct.AccountNumber, in.AmountRSD,
					fmt.Sprintf("Comp redeem target saga=%s", sagaID),
					fmt.Sprintf("redeem-%s-comp-target", sagaID))
				return e
			})
			_ = exec.RunCompensation(ctx, 0, "comp_credit_fund", func() error {
				_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, debitTotal,
					fmt.Sprintf("Comp redeem fund saga=%s", sagaID),
					fmt.Sprintf("redeem-%s-comp-fund", sagaID))
				return e
			})
			_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
			return nil, err
		}
	}

	if err := exec.RunStep(ctx, "decrement_position", in.AmountRSD, "RSD", nil, func() error {
		return s.positions.DecrementContribution(in.FundID, posUserID, posSystemType, in.AmountRSD)
	}); err != nil {
		log.Printf("WARN: redeem position decrement failed for saga %s: %v (money already moved)", sagaID, err)
		// Don't reverse money flow — position bookkeeping is recoverable;
		// money is not. Mark contribution complete with a note.
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
