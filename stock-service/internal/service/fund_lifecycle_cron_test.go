package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
)

func openLifecycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func ptime(tm time.Time) *time.Time { return &tm }

// closedFund builds a valid closed-end fund whose dates satisfy BeforeSave.
func closedFund(id uint64, status model.FundStatus, fr, fe, md time.Time) *model.InvestmentFund {
	return &model.InvestmentFund{
		ID: id, Name: "F" + kafkaUint(id), ManagerEmployeeID: int64(id), RSDAccountID: id + 100,
		FundType: model.FundTypeClosed, FundStatus: status,
		FundraisingStart: ptime(fr), FundraisingEnd: ptime(fe), MaturityDate: ptime(md),
		TargetAmountRSD: decimal.NewFromInt(100000),
	}
}

func TestFundLifecycleCron_AllTransitions(t *testing.T) {
	db := openLifecycleDB(t)
	notif := &fakeRecurringNotifier{}
	reg := cronreg.NewRegistry("test", nil)
	cron := NewFundLifecycleCron(db, notif, 0, reg)

	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fe := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	// 1: fundraising with end in the past → active.
	f1 := closedFund(1, model.FundStatusFundraising, past, fe, future)
	// 2: open (closed-type) with start in past → fundraising.
	f2 := closedFund(2, model.FundStatusOpen, past, future, future.AddDate(1, 0, 0))
	// 3: active with maturity in past → matured.
	f3 := closedFund(3, model.FundStatusActive, past, fe, mid)
	// 4: matured with grace-end in past → liquidated.
	f4 := closedFund(4, model.FundStatusMatured, past, fe, mid)
	f4.MaturityGraceEnd = ptime(mid.Add(7 * 24 * time.Hour))
	// 5: fundraising, end in future → no transition.
	f5 := closedFund(5, model.FundStatusFundraising, past, future, future.AddDate(1, 0, 0))

	for _, f := range []*model.InvestmentFund{f1, f2, f3, f4, f5} {
		if err := db.Create(f).Error; err != nil {
			t.Fatalf("create fund %d: %v", f.ID, err)
		}
	}

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cron.tick(context.Background(), now)

	reload := func(id uint64) model.InvestmentFund {
		var f model.InvestmentFund
		if err := db.First(&f, id).Error; err != nil {
			t.Fatalf("reload %d: %v", id, err)
		}
		return f
	}

	if got := reload(1).FundStatus; got != model.FundStatusActive {
		t.Errorf("f1 = %s, want active", got)
	}
	if got := reload(2).FundStatus; got != model.FundStatusFundraising {
		t.Errorf("f2 = %s, want fundraising", got)
	}
	r3 := reload(3)
	if r3.FundStatus != model.FundStatusMatured {
		t.Errorf("f3 = %s, want matured", r3.FundStatus)
	}
	if r3.MaturityGraceEnd == nil {
		t.Errorf("f3 grace end not set")
	}
	r4 := reload(4)
	if r4.FundStatus != model.FundStatusLiquidated || r4.Active {
		t.Errorf("f4 = %s active=%v, want liquidated+inactive", r4.FundStatus, r4.Active)
	}
	if got := reload(5).FundStatus; got != model.FundStatusFundraising {
		t.Errorf("f5 = %s, want unchanged fundraising", got)
	}

	// Four transitions → four notifications.
	if len(notif.msgs) != 4 {
		t.Errorf("notifications = %d, want 4", len(notif.msgs))
	}
}

func TestFundLifecycleCron_NilNotifierNoPanic(t *testing.T) {
	db := openLifecycleDB(t)
	reg := cronreg.NewRegistry("test", nil)
	cron := NewFundLifecycleCron(db, nil, time.Minute, reg)
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f := closedFund(1, model.FundStatusFundraising, past, mid, future)
	if err := db.Create(f).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	cron.tick(context.Background(), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	var reloaded model.InvestmentFund
	_ = db.First(&reloaded, 1)
	if reloaded.FundStatus != model.FundStatusActive {
		t.Errorf("expected active transition even without notifier, got %s", reloaded.FundStatus)
	}
}
