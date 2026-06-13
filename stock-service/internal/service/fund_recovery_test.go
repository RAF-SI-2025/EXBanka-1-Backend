package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestRecoverFundSaga_InvestForwardResumeOnce proves the no-human auto-resolve
// forward path for a fund invest: re-driving a completed/stranded saga leaves
// the position credited exactly once (completed forward steps are skipped on
// resume), even across repeated recovery invocations.
func TestRecoverFundSaga_InvestForwardResumeOnce(t *testing.T) {
	fx := newInvestSagaFixture(t)
	out, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(500), Currency: "RSD",
		OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("invest: %v", err)
	}

	for i := 0; i < 2; i++ {
		if rerr := fx.svc.RecoverFundSaga(context.Background(), out.SagaID, out.ID); rerr != nil {
			t.Fatalf("RecoverFundSaga #%d: %v", i, rerr)
		}
	}

	// No double debit/credit (completed steps skipped on resume).
	if !fx.accounts.sumDebited("USER").Equal(decimal.NewFromInt(500)) {
		t.Fatalf("source debit after recovery = %s, want 500 (no replay double-debit)", fx.accounts.sumDebited("USER"))
	}
	if !fx.accounts.sumCredited("FUND").Equal(decimal.NewFromInt(500)) {
		t.Fatalf("fund credit after recovery = %s, want 500", fx.accounts.sumCredited("FUND"))
	}
	posUID := uint64(99)
	pos, err := fx.pos.GetByFundAndOwner(fx.fund.ID, model.OwnerClient, &posUID)
	if err != nil || !pos.TotalContributedRSD.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("position after recovery = %+v err=%v, want 500", pos, err)
	}
}

// TestRecoverFundSaga_NoContribution_NoOp proves a clean no-op when the saga
// never created a contribution row (nothing committed to drive).
func TestRecoverFundSaga_NoContribution_NoOp(t *testing.T) {
	fx := newInvestSagaFixture(t)
	if err := fx.svc.RecoverFundSaga(context.Background(), "never-ran-fund-saga", 0); err != nil {
		t.Fatalf("RecoverFundSaga: %v", err)
	}
}
