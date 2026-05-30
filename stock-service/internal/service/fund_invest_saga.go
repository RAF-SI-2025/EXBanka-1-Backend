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

// InvestInput captures the parameters of a single Invest call. ActorUserID +
// ActorSystemType identify the caller (employee or client). OnBehalfOfType
// disambiguates self vs bank — when "bank", the position is recorded against
// (1_000_000_000, "employee").
type InvestInput struct {
	FundID          uint64
	ActorUserID     uint64
	ActorSystemType string
	SourceAccountID uint64
	Amount          decimal.Decimal
	Currency        string
	OnBehalfOfType  string // "self" | "bank"
}

// Invest is the saga that:
//  1. Optionally converts the source amount to RSD via exchange-service.
//  2. Validates the minimum-contribution threshold.
//  3. Debits the source account.
//  4. Credits the fund's RSD account.
//  5. Upserts the (fund, owner) position by +AmountRSD.
//
// Failure of step (4) reverses (3). Failure of step (5) reverses (4) and (3).
// Driven by saga.Saga: each step declares its Forward + Backward; the
// executor walks completed steps in reverse on any forward failure. All
// money side effects use deterministic idempotency keys derived from the
// saga ID so retries after a crash are safe.
func (s *FundService) Invest(ctx context.Context, in InvestInput) (*model.FundContribution, error) {
	if s.sagaRepo == nil || s.accounts == nil || s.contribs == nil || s.positions == nil {
		return nil, errSagaDepsNotWired
	}
	fund, err := s.repo.GetByID(in.FundID)
	if err != nil {
		return nil, fmt.Errorf("fund not found: %v: %w", err, ErrFundNotFound)
	}
	if !fund.Active {
		return nil, fmt.Errorf("fund is inactive: %w", ErrFundInactive)
	}
	// Closed-end fund invariants (Celina 4 / req-closed-end-funds).
	// Closed funds only accept invest during the fundraising window;
	// post-fundraising they're locked down.
	if fund.FundType == model.FundTypeClosed {
		if fund.FundStatus != model.FundStatusFundraising {
			return nil, fmt.Errorf("closed fund is not in fundraising phase (status=%s): %w", fund.FundStatus, ErrFundInactive)
		}
		// If TargetAmountRSD is set, reject when this contribution would
		// exceed it. The fund's RSD account balance is the authoritative
		// running total.
	}

	// Resolve the position owner. Self-invest attributes the position to the
	// actor (client owner). Bank-on-behalf attributes the position to the bank
	// (OwnerType=bank, owner_id=NULL).
	posOwnerType, posOwnerID := model.OwnerFromLegacy(in.ActorUserID, in.ActorSystemType)
	if in.OnBehalfOfType == "bank" {
		posOwnerType, posOwnerID = model.OwnerBank, nil
	}

	amountRSD := in.Amount
	var fxRate *decimal.Decimal
	if in.Currency != "RSD" {
		if s.exchange == nil {
			return nil, errors.New("cross-currency invest requires exchange client")
		}
		conv, err := s.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: in.Currency,
			ToCurrency:   "RSD",
			Amount:       in.Amount.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("FX convert: %w", err)
		}
		amountRSD, _ = decimal.NewFromString(conv.ConvertedAmount)
		rate, _ := decimal.NewFromString(conv.EffectiveRate)
		fxRate = &rate
	}

	if amountRSD.LessThan(fund.MinimumContributionRSD) {
		return nil, fmt.Errorf("minimum_contribution_not_met: required %s RSD got %s: %w", fund.MinimumContributionRSD, amountRSD, ErrFundContributionBelowMin)
	}

	srcAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: in.SourceAccountID})
	if err != nil {
		return nil, fmt.Errorf("get source account: %w", err)
	}
	fundAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	if err != nil {
		return nil, fmt.Errorf("get fund account: %w", err)
	}

	sagaID := uuid.NewString()

	// Pending contribution row up-front so we can flip its status as the
	// saga progresses.
	contrib := &model.FundContribution{
		FundID:                  in.FundID,
		OwnerType:               posOwnerType,
		OwnerID:                 posOwnerID,
		Direction:               model.FundDirectionInvest,
		AmountNative:            in.Amount,
		NativeCurrency:          in.Currency,
		AmountRSD:               amountRSD,
		FxRate:                  fxRate,
		FeeRSD:                  decimal.Zero,
		SourceOrTargetAccountID: in.SourceAccountID,
		SagaID:                  sagaID,
		Status:                  model.FundContributionStatusPending,
	}
	if err := s.contribs.Create(contrib); err != nil {
		return nil, err
	}

	sg, state := s.buildInvestSaga(sagaID, investSagaParams{
		fundID: in.FundID, contribID: contrib.ID,
		posOwnerType: posOwnerType, posOwnerID: posOwnerID,
		srcAcctNumber: srcAcct.AccountNumber, fundAcctNumber: fundAcct.AccountNumber,
		amountNative: in.Amount, nativeCurrency: in.Currency, amountRSD: amountRSD,
	})
	if err := sg.Execute(ctx, state); err != nil {
		_ = s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		return nil, err
	}
	if err := s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted); err != nil {
		log.Printf("WARN: invest saga complete but contribution mark-completed failed: %v", err)
	}
	contrib.Status = model.FundContributionStatusCompleted

	if s.producer != nil {
		fxStr := ""
		if fxRate != nil {
			fxStr = fxRate.String()
		}
		payload := kafkamsg.StockFundInvestedMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			FundID:         in.FundID,
			OwnerType:      string(posOwnerType),
			OwnerID:        posOwnerID,
			AmountNative:   in.Amount.String(),
			NativeCurrency: in.Currency,
			AmountRSD:      amountRSD.String(),
			FxRate:         fxStr,
			SagaID:         sagaID,
			ContributionID: contrib.ID,
		}
		if data, err := json.Marshal(payload); err == nil {
			publishSagaEvent(ctx, s.outbox, s.outboxDB, s.producer, kafkamsg.TopicStockFundInvested, data, sagaID)
		}
	}

	return contrib, nil
}

// investSagaParams bundles what buildInvestSaga needs, so Invest (from the
// request) and RecoverInvestSaga (from the persisted contribution) build the
// identical saga.
type investSagaParams struct {
	fundID         uint64
	contribID      uint64
	posOwnerType   model.OwnerType
	posOwnerID     *uint64
	srcAcctNumber  string
	fundAcctNumber string
	amountNative   decimal.Decimal
	nativeCurrency string
	amountRSD      decimal.Decimal
}

// buildInvestSaga assembles the fund-invest saga under sagaID. All three steps
// are idempotent (keyed debit/credit + contrib-id-keyed position upsert), so a
// crash-recovery forward-resume replays safely. state["order_id"]=contribID so
// saga_logs rows carry it for the recovery lookup.
func (s *FundService) buildInvestSaga(sagaID string, p investSagaParams) (*saga.Saga, *saga.State) {
	debitMemo := fmt.Sprintf("Invest in fund #%d (saga=%s)", p.fundID, sagaID)
	debitKey := fmt.Sprintf("invest-%s-debit-source", sagaID)
	creditMemo := fmt.Sprintf("Contribution to fund #%d (saga=%s)", p.fundID, sagaID)
	creditKey := fmt.Sprintf("invest-%s-credit-fund", sagaID)
	compSrcMemo := fmt.Sprintf("Comp invest src saga=%s", sagaID)
	compSrcKey := fmt.Sprintf("invest-%s-comp-source", sagaID)
	compFundMemo := fmt.Sprintf("Comp invest fund saga=%s", sagaID)
	compFundKey := fmt.Sprintf("invest-%s-comp-fund", sagaID)

	state := saga.NewState()
	state.Set("order_id", p.contribID)
	state.Set("step:debit_source:amount", p.amountNative)
	state.Set("step:debit_source:currency", p.nativeCurrency)
	state.Set("step:credit_fund:amount", p.amountRSD)
	state.Set("step:credit_fund:currency", "RSD")
	state.Set("step:upsert_position:amount", p.amountRSD)
	state.Set("step:upsert_position:currency", "RSD")

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: saga.StepDebitSource,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, p.srcAcctNumber, p.amountNative, debitMemo, debitKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, p.srcAcctNumber, p.amountNative, compSrcMemo, compSrcKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepCreditFund,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, p.fundAcctNumber, p.amountRSD, creditMemo, creditKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, p.fundAcctNumber, p.amountRSD, compFundMemo, compFundKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepUpsertPosition,
			Forward: func(ctx context.Context, _ *saga.State) error {
				// Fix R2 (2026-05-16): pass contrib.ID so the repository's
				// idempotency ledger (fund_position_settlements) can
				// short-circuit a retry instead of double-counting.
				return s.positions.IncrementContribution(p.fundID, p.posOwnerType, p.posOwnerID, p.amountRSD, p.contribID)
			},
			// Last step: nothing to roll back to, so no Backward needed.
		})

	return sg, state
}
