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
		return nil, fmt.Errorf("fund not found: %w", err)
	}
	if !fund.Active {
		return nil, errors.New("fund is inactive")
	}

	posUserID, posSystemType := in.ActorUserID, in.ActorSystemType
	if in.OnBehalfOfType == "bank" {
		posUserID, posSystemType = bankSentinelUserID, "employee"
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
		return nil, fmt.Errorf("minimum_contribution_not_met: required %s RSD got %s", fund.MinimumContributionRSD, amountRSD)
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
		UserID:                  posUserID,
		SystemType:              posSystemType,
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

	// Pre-compute deterministic memos / idempotency keys so forward and
	// backward closures share them (account-service dedups by key).
	debitMemo := fmt.Sprintf("Invest in fund #%d (saga=%s)", in.FundID, sagaID)
	debitKey := fmt.Sprintf("invest-%s-debit-source", sagaID)
	creditMemo := fmt.Sprintf("Contribution from %s #%d (saga=%s)", in.ActorSystemType, in.ActorUserID, sagaID)
	creditKey := fmt.Sprintf("invest-%s-credit-fund", sagaID)
	compSrcMemo := fmt.Sprintf("Comp invest src saga=%s", sagaID)
	compSrcKey := fmt.Sprintf("invest-%s-comp-source", sagaID)
	compFundMemo := fmt.Sprintf("Comp invest fund saga=%s", sagaID)
	compFundKey := fmt.Sprintf("invest-%s-comp-fund", sagaID)

	state := saga.NewState()
	state.Set("step:debit_source:amount", in.Amount)
	state.Set("step:debit_source:currency", in.Currency)
	state.Set("step:credit_fund:amount", amountRSD)
	state.Set("step:credit_fund:currency", "RSD")
	state.Set("step:upsert_position:amount", amountRSD)
	state.Set("step:upsert_position:currency", "RSD")

	sg := saga.NewSagaWithID(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: saga.StepDebitSource,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, srcAcct.AccountNumber, in.Amount, debitMemo, debitKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, srcAcct.AccountNumber, in.Amount, compSrcMemo, compSrcKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepCreditFund,
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.CreditAccount(ctx, fundAcct.AccountNumber, amountRSD, creditMemo, creditKey)
				return e
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, e := s.accounts.DebitAccount(ctx, fundAcct.AccountNumber, amountRSD, compFundMemo, compFundKey)
				return e
			},
		}).
		Add(saga.Step{
			Name: saga.StepUpsertPosition,
			Forward: func(ctx context.Context, _ *saga.State) error {
				return s.positions.IncrementContribution(in.FundID, posUserID, posSystemType, amountRSD)
			},
			// Last step: nothing to roll back to, so no Backward needed.
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
			UserID:         posUserID,
			SystemType:     posSystemType,
			AmountNative:   in.Amount.String(),
			NativeCurrency: in.Currency,
			AmountRSD:      amountRSD.String(),
			FxRate:         fxStr,
			SagaID:         sagaID,
			ContributionID: contrib.ID,
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicStockFundInvested, data)
		}
	}

	return contrib, nil
}
