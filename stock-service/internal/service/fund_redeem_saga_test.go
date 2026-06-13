package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type fakeFundSettings struct {
	values map[string]decimal.Decimal
	err    error
}

func (f *fakeFundSettings) GetDecimal(key string) (decimal.Decimal, error) {
	if f.err != nil {
		return decimal.Zero, f.err
	}
	v, ok := f.values[key]
	if !ok {
		return decimal.Zero, errors.New("not found")
	}
	return v, nil
}

type redeemFixture struct {
	svc      *FundService
	repo     *repository.FundRepository
	pos      *repository.ClientFundPositionRepository
	accounts *fakeFundAccountClient
	fund     *model.InvestmentFund
}

func newRedeemFixture(t *testing.T, settings FundSettings, withBankFn bool) *redeemFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{},
		&model.ClientFundPosition{},
		&model.FundPositionSettlement{},
		&model.FundContribution{},
		&model.FundHolding{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	contribs := repository.NewFundContributionRepository(db)
	positions := repository.NewClientFundPositionRepository(db)
	holdings := repository.NewFundHoldingRepository(db)
	sagaRepo := newFakeSagaRepo()
	accounts := newFakeFundAccountClient()
	bac := &fakeBankAccountClient{nextID: 4000}

	var bankFn func(context.Context) (string, uint64, error)
	if withBankFn {
		bankFn = func(_ context.Context) (string, uint64, error) {
			return "BANK-RSD", 9001, nil
		}
	}

	svc := NewFundService(repo, bac, nil).
		WithSaga(sagaRepo, accounts, nil, contribs, positions, holdings, settings, bankFn)

	fund := &model.InvestmentFund{
		Name: "Alpha", ManagerEmployeeID: 25,
		MinimumContributionRSD: decimal.Zero, RSDAccountID: 4001, Active: true,
	}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(4001, "FUND", "10000")
	accounts.addAccount(5001, "USER", "0")
	accounts.addAccount(9001, "BANK-RSD", "0")

	return &redeemFixture{svc: svc, repo: repo, pos: positions, accounts: accounts, fund: fund}
}

func seedPosition(t *testing.T, pos *repository.ClientFundPositionRepository, fundID, uid uint64, amount decimal.Decimal) {
	t.Helper()
	if err := pos.IncrementContribution(fundID, model.OwnerClient, &uid, amount, fundID*1000+uid); err != nil {
		t.Fatalf("seed position: %v", err)
	}
}

func TestRedeem_HappyPath_NoFee(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	seedPosition(t, fx.pos, fx.fund.ID, 99, decimal.NewFromInt(1000))

	out, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(400), TargetAccountID: 5001, OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if out.Status != model.FundContributionStatusCompleted {
		t.Errorf("status = %s, want completed", out.Status)
	}
	if !fx.accounts.sumDebited("FUND").Equal(decimal.NewFromInt(400)) {
		t.Errorf("fund debit = %s, want 400", fx.accounts.sumDebited("FUND"))
	}
	if !fx.accounts.sumCredited("USER").Equal(decimal.NewFromInt(400)) {
		t.Errorf("user credit = %s, want 400", fx.accounts.sumCredited("USER"))
	}
	// Position decremented by 400 → 600 remains.
	uid := uint64(99)
	p, err := fx.pos.GetByFundAndOwner(fx.fund.ID, model.OwnerClient, &uid)
	if err != nil || !p.TotalContributedRSD.Equal(decimal.NewFromInt(600)) {
		t.Errorf("position = %+v err=%v, want 600 remaining", p, err)
	}
}

func TestRedeem_HappyPath_WithFee(t *testing.T) {
	settings := &fakeFundSettings{values: map[string]decimal.Decimal{
		"fund_redemption_fee_pct": decimal.NewFromFloat(0.01),
	}}
	fx := newRedeemFixture(t, settings, true)
	seedPosition(t, fx.pos, fx.fund.ID, 99, decimal.NewFromInt(1000))

	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(400), TargetAccountID: 5001, OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	// Fee = 400 * 0.01 = 4. Fund debited 404; bank credited 4.
	if !fx.accounts.sumDebited("FUND").Equal(decimal.NewFromInt(404)) {
		t.Errorf("fund debit = %s, want 404", fx.accounts.sumDebited("FUND"))
	}
	if !fx.accounts.sumCredited("BANK-RSD").Equal(decimal.NewFromInt(4)) {
		t.Errorf("bank fee credit = %s, want 4", fx.accounts.sumCredited("BANK-RSD"))
	}
}

func TestRedeem_FundNotFound(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: 9999, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(100), TargetAccountID: 5001,
	})
	if !errors.Is(err, ErrFundNotFound) {
		t.Fatalf("want ErrFundNotFound, got %v", err)
	}
}

func TestRedeem_NonPositiveAmount(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.Zero, TargetAccountID: 5001,
	})
	if !errors.Is(err, ErrFundInvalidInput) {
		t.Fatalf("want ErrFundInvalidInput, got %v", err)
	}
}

func TestRedeem_NoPosition(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	// No position seeded.
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(100), TargetAccountID: 5001,
	})
	if err == nil {
		t.Fatalf("expected no-position error")
	}
}

func TestRedeem_ExceedsPosition(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	seedPosition(t, fx.pos, fx.fund.ID, 99, decimal.NewFromInt(100))
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(500), TargetAccountID: 5001,
	})
	if !errors.Is(err, ErrFundExceedsPosition) {
		t.Fatalf("want ErrFundExceedsPosition, got %v", err)
	}
}

func TestRedeem_InactiveFund(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	fx.fund.Active = false
	if err := fx.repo.Save(fx.fund); err != nil {
		t.Fatalf("save: %v", err)
	}
	seedPosition(t, fx.pos, fx.fund.ID, 99, decimal.NewFromInt(1000))
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(100), TargetAccountID: 5001,
	})
	if !errors.Is(err, ErrFundInactive) {
		t.Fatalf("want ErrFundInactive, got %v", err)
	}
}

func TestRedeem_InsufficientCash_NoLiquidationWired(t *testing.T) {
	fx := newRedeemFixture(t, nil, false)
	// Drop the fund balance below the requested amount. With no liquidation
	// deps wired, the insufficient-cash branch fails fast.
	fx.accounts.accounts[4001].AvailableBalance = "50"
	seedPosition(t, fx.pos, fx.fund.ID, 99, decimal.NewFromInt(1000))
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		AmountRSD: decimal.NewFromInt(400), TargetAccountID: 5001,
	})
	if err == nil {
		t.Fatalf("expected error on insufficient cash without liquidation wired")
	}
}
