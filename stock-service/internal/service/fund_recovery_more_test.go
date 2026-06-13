package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type recoveryFix struct {
	svc      *FundService
	contribs *repository.FundContributionRepository
	pos      *repository.ClientFundPositionRepository
	holdings *repository.FundHoldingRepository
	saga     *fakeSagaRepo
	accounts *fakeFundAccountClient
	fund     *model.InvestmentFund
}

func newRecoveryFix(t *testing.T) *recoveryFix {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{}, &model.ClientFundPosition{}, &model.FundPositionSettlement{},
		&model.FundContribution{}, &model.FundHolding{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	contribs := repository.NewFundContributionRepository(db)
	positions := repository.NewClientFundPositionRepository(db)
	holdings := repository.NewFundHoldingRepository(db)
	saga := newFakeSagaRepo()
	accounts := newFakeFundAccountClient()
	bac := &fakeBankAccountClient{nextID: 9000}

	svc := NewFundService(repo, bac, nil).
		WithSaga(saga, accounts, nil, contribs, positions, holdings, nil, nil)

	fund := &model.InvestmentFund{Name: "Rec", ManagerEmployeeID: 1, RSDAccountID: 4001, Active: true}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(4001, "FUND", "10000")
	accounts.addAccount(5001, "USER", "5000")

	return &recoveryFix{svc: svc, contribs: contribs, pos: positions, holdings: holdings, saga: saga, accounts: accounts, fund: fund}
}

func (fx *recoveryFix) seedContrib(t *testing.T, direction, sagaID string) *model.FundContribution {
	t.Helper()
	uid := uint64(99)
	c := &model.FundContribution{
		FundID: fx.fund.ID, OwnerType: model.OwnerClient, OwnerID: &uid,
		Direction: direction, AmountNative: decimal.NewFromInt(500), NativeCurrency: "RSD",
		AmountRSD: decimal.NewFromInt(500), SourceOrTargetAccountID: 5001,
		SagaID: sagaID, Status: model.FundContributionStatusPending,
	}
	if err := fx.contribs.Create(c); err != nil {
		t.Fatalf("seed contrib: %v", err)
	}
	return c
}

func TestRecoverFundSaga_EmptySagaID(t *testing.T) {
	fx := newRecoveryFix(t)
	if err := fx.svc.RecoverFundSaga(context.Background(), "", 0); err == nil {
		t.Fatal("expected error on empty sagaID")
	}
}

func TestRecoverFundSaga_NoContribution(t *testing.T) {
	fx := newRecoveryFix(t)
	if err := fx.svc.RecoverFundSaga(context.Background(), "nonexistent", 0); err != nil {
		t.Fatalf("missing contribution should be a no-op, got %v", err)
	}
}

func TestRecoverFundSaga_InvestForwardResume(t *testing.T) {
	fx := newRecoveryFix(t)
	c := fx.seedContrib(t, model.FundDirectionInvest, "inv-1")
	if err := fx.svc.RecoverFundSaga(context.Background(), "inv-1", c.ID); err != nil {
		t.Fatalf("recover: %v", err)
	}
	// USER debited 500, FUND credited 500.
	if !fx.accounts.sumDebited("USER").Equal(decimal.NewFromInt(500)) {
		t.Errorf("USER debit = %s, want 500", fx.accounts.sumDebited("USER"))
	}
	if !fx.accounts.sumCredited("FUND").Equal(decimal.NewFromInt(500)) {
		t.Errorf("FUND credit = %s, want 500", fx.accounts.sumCredited("FUND"))
	}
	reloaded, _ := fx.contribs.GetBySagaID("inv-1")
	if reloaded.Status != model.FundContributionStatusCompleted {
		t.Errorf("status = %s, want completed", reloaded.Status)
	}
}

func TestRecoverFundSaga_InvestRollback(t *testing.T) {
	fx := newRecoveryFix(t)
	fx.saga.hasComp = true // forces the Compensate (rollback) direction
	c := fx.seedContrib(t, model.FundDirectionInvest, "inv-rb")
	if err := fx.svc.RecoverFundSaga(context.Background(), "inv-rb", c.ID); err != nil {
		t.Fatalf("recover rollback: %v", err)
	}
	reloaded, _ := fx.contribs.GetBySagaID("inv-rb")
	if reloaded.Status != model.FundContributionStatusFailed {
		t.Errorf("status = %s, want failed after rollback", reloaded.Status)
	}
}

func TestRecoverFundSaga_RedeemRollback(t *testing.T) {
	fx := newRecoveryFix(t)
	fx.saga.hasComp = true
	c := fx.seedContrib(t, model.FundDirectionRedeem, "red-rb")
	if err := fx.svc.RecoverFundSaga(context.Background(), "red-rb", c.ID); err != nil {
		t.Fatalf("recover redeem rollback: %v", err)
	}
	reloaded, _ := fx.contribs.GetBySagaID("red-rb")
	if reloaded.Status != model.FundContributionStatusFailed {
		t.Errorf("status = %s, want failed after rollback", reloaded.Status)
	}
}

func TestRecoverFundSaga_RedeemWithFee(t *testing.T) {
	// Build a fund service whose redeem recovery resolves the bank RSD account
	// (exercises the fee-credit branch of RecoverFundSaga).
	fx := newRecoveryFix(t)
	fx.accounts.addAccount(9001, "BANK-RSD", "0")
	bankFn := func(context.Context) (string, uint64, error) { return "BANK-RSD", 9001, nil }
	// Re-wire the saga with settings + bankFn while keeping the same repos/db
	// by rebuilding the service against the same fund repo isn't exposed; instead
	// seed a fee'd contribution and recover via a freshly-wired service sharing
	// the same accounts.
	uid := uint64(99)
	c := &model.FundContribution{
		FundID: fx.fund.ID, OwnerType: model.OwnerClient, OwnerID: &uid,
		Direction: model.FundDirectionRedeem, AmountNative: decimal.NewFromInt(500), NativeCurrency: "RSD",
		AmountRSD: decimal.NewFromInt(500), FeeRSD: decimal.NewFromInt(5),
		SourceOrTargetAccountID: 5001, SagaID: "red-fee", Status: model.FundContributionStatusPending,
	}
	if err := fx.contribs.Create(c); err != nil {
		t.Fatalf("seed fee contrib: %v", err)
	}
	svc := fx.svc.WithSaga(fx.saga, fx.accounts, nil,
		fx.contribs, fx.pos, fx.holdings, nil, bankFn)
	if err := svc.RecoverFundSaga(context.Background(), "red-fee", c.ID); err != nil {
		t.Fatalf("recover redeem with fee: %v", err)
	}
	// Fee credited to the bank RSD account.
	if !fx.accounts.sumCredited("BANK-RSD").Equal(decimal.NewFromInt(5)) {
		t.Errorf("bank fee credit = %s, want 5", fx.accounts.sumCredited("BANK-RSD"))
	}
}

func TestRecoverFundSaga_RedeemForwardResume(t *testing.T) {
	fx := newRecoveryFix(t)
	// Seed a position so the post-saga decrement succeeds.
	uid := uint64(99)
	// Use a distinct settlement id so it doesn't collide with the redeem
	// contribution's own id (which drives the post-saga decrement).
	if err := fx.pos.IncrementContribution(fx.fund.ID, model.OwnerClient, &uid, decimal.NewFromInt(1000), 777); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	c := fx.seedContrib(t, model.FundDirectionRedeem, "red-1")
	if err := fx.svc.RecoverFundSaga(context.Background(), "red-1", c.ID); err != nil {
		t.Fatalf("recover redeem: %v", err)
	}
	// FUND debited 500, USER (target) credited 500.
	if !fx.accounts.sumDebited("FUND").Equal(decimal.NewFromInt(500)) {
		t.Errorf("FUND debit = %s, want 500", fx.accounts.sumDebited("FUND"))
	}
	if !fx.accounts.sumCredited("USER").Equal(decimal.NewFromInt(500)) {
		t.Errorf("USER credit = %s, want 500", fx.accounts.sumCredited("USER"))
	}
	reloaded, _ := fx.contribs.GetBySagaID("red-1")
	if reloaded.Status != model.FundContributionStatusCompleted {
		t.Errorf("status = %s, want completed", reloaded.Status)
	}
	// Position decremented from 1000 to 500.
	p, _ := fx.pos.GetByFundAndOwner(fx.fund.ID, model.OwnerClient, &uid)
	if !p.TotalContributedRSD.Equal(decimal.NewFromInt(500)) {
		t.Errorf("position = %s, want 500", p.TotalContributedRSD)
	}
}
