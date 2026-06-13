package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type fakeFundInvester struct {
	calls []InvestInput
	err   error
}

func (f *fakeFundInvester) Invest(_ context.Context, in InvestInput) (*model.FundContribution, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	return &model.FundContribution{}, nil
}

func openRecurringFundDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.RecurringFundInvestment{}, &model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newRecurringFundSvc(t *testing.T, invester fundInvester, notifier recurringOrderNotifier) (*RecurringFundService, *gorm.DB) {
	db := openRecurringFundDB(t)
	// Seed an open fund (id 1) with min contribution 100.
	fund := &model.InvestmentFund{ID: 1, Name: "Open", ManagerEmployeeID: 1, RSDAccountID: 50, MinimumContributionRSD: decimal.NewFromInt(100)}
	if err := db.Create(fund).Error; err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	svc := NewRecurringFundService(
		repository.NewRecurringFundInvestmentRepository(db),
		repository.NewFundRepository(db),
		invester, notifier,
	)
	return svc, db
}

func baseRecurringFund() *model.RecurringFundInvestment {
	return &model.RecurringFundInvestment{
		ClientID: 7, FundID: 1, AmountRSD: decimal.NewFromInt(500),
		SourceAccountID: 200, DayOfMonth: 15, Active: true,
	}
}

func TestRecurringFund_Create_OK(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == 0 || row.NextRun.IsZero() {
		t.Errorf("expected id and next_run set")
	}
}

func TestRecurringFund_Create_FundNotFound(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	row.FundID = 999
	if err := svc.Create(row); !errors.Is(err, ErrRecurringFundFundNotFound) {
		t.Fatalf("want fund-not-found, got %v", err)
	}
}

func TestRecurringFund_Create_BelowMinimum(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	row.AmountRSD = decimal.NewFromInt(50) // below min 100
	if err := svc.Create(row); !errors.Is(err, ErrRecurringFundBelowMinimum) {
		t.Fatalf("want below-minimum, got %v", err)
	}
}

func TestRecurringFund_Create_ClosedFundNotFundraising(t *testing.T) {
	svc, db := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	// Seed a closed, active (not fundraising) fund.
	fs := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fe := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	md := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	closed := &model.InvestmentFund{
		ID: 2, Name: "Closed", ManagerEmployeeID: 1, RSDAccountID: 60,
		MinimumContributionRSD: decimal.NewFromInt(100),
		FundType:               model.FundTypeClosed, FundStatus: model.FundStatusActive,
		FundraisingStart: &fs, FundraisingEnd: &fe, MaturityDate: &md,
		TargetAmountRSD: decimal.NewFromInt(100000),
	}
	if err := db.Create(closed).Error; err != nil {
		t.Fatalf("seed closed: %v", err)
	}
	row := baseRecurringFund()
	row.FundID = 2
	if err := svc.Create(row); !errors.Is(err, ErrRecurringFundFundNotEligible) {
		t.Fatalf("want not-eligible, got %v", err)
	}
}

func TestRecurringFund_Get_OwnershipMismatch(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Get(row.ID, 999); !errors.Is(err, ErrRecurringFundNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := svc.Get(424242, 7); !errors.Is(err, ErrRecurringFundNotFound) {
		t.Fatalf("want not found for missing id, got %v", err)
	}
}

func TestRecurringFund_PauseResumeCancel(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Pause(row.ID, 7); err != nil {
		t.Fatalf("pause: %v", err)
	}
	g, _ := svc.Get(row.ID, 7)
	if g.Active {
		t.Errorf("expected paused (inactive)")
	}
	if err := svc.Resume(row.ID, 7); err != nil {
		t.Fatalf("resume: %v", err)
	}
	g, _ = svc.Get(row.ID, 7)
	if !g.Active {
		t.Errorf("expected active after resume")
	}
	if err := svc.Cancel(row.ID, 7); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := svc.Get(row.ID, 7); !errors.Is(err, ErrRecurringFundNotFound) {
		t.Errorf("expected gone after cancel, got %v", err)
	}
}

func TestRecurringFund_ListMy(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, &fakeFundInvester{}, nil)
	row := baseRecurringFund()
	if err := svc.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows, err := svc.ListMy(7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d, want 1", len(rows))
	}
}

func TestRecurringFund_RunDue_NilInvester(t *testing.T) {
	svc, _ := newRecurringFundSvc(t, nil, nil)
	svc.RunDue(context.Background(), time.Now()) // no panic, no-op
}

func TestRecurringFund_RunDue_Executes(t *testing.T) {
	inv := &fakeFundInvester{}
	notif := &fakeRecurringNotifier{}
	svc, db := newRecurringFundSvc(t, inv, notif)
	row := baseRecurringFund()
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	svc.RunDue(context.Background(), now)

	if len(inv.calls) != 1 || !inv.calls[0].Amount.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("invest not called correctly: %+v", inv.calls)
	}
	if len(notif.msgs) != 1 || notif.msgs[0].Type != "FUND_RECURRING_EXECUTED" {
		t.Fatalf("want executed notification, got %+v", notif.msgs)
	}
	var reloaded model.RecurringFundInvestment
	if err := db.First(&reloaded, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.LastRun == nil || !reloaded.NextRun.After(now) {
		t.Errorf("next/last run not advanced")
	}
}

func TestRecurringFund_RunDue_InvestErrorSkips(t *testing.T) {
	inv := &fakeFundInvester{err: errors.New("insufficient funds")}
	notif := &fakeRecurringNotifier{}
	svc, db := newRecurringFundSvc(t, inv, notif)
	row := baseRecurringFund()
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.RunDue(context.Background(), time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC))
	if len(notif.msgs) != 1 || notif.msgs[0].Type != "FUND_RECURRING_SKIPPED" {
		t.Fatalf("want skipped notification, got %+v", notif.msgs)
	}
	if notif.msgs[0].Data["reason"] != "insufficient funds" {
		t.Errorf("reason = %q", notif.msgs[0].Data["reason"])
	}
}

func TestRecurringFund_RunOne_FundNotFoundSkips(t *testing.T) {
	inv := &fakeFundInvester{}
	notif := &fakeRecurringNotifier{}
	svc, db := newRecurringFundSvc(t, inv, notif)
	row := baseRecurringFund()
	row.FundID = 4242 // not present
	row.NextRun = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Bypass Create's eligibility check by inserting directly.
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.runOne(context.Background(), row, time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC))
	if len(inv.calls) != 0 {
		t.Errorf("invest should not be called when fund missing")
	}
	if len(notif.msgs) != 1 || notif.msgs[0].Data["reason"] != "fund_not_found" {
		t.Fatalf("want fund_not_found skip, got %+v", notif.msgs)
	}
}
