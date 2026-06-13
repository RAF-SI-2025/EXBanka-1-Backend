package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type fundStatsFix struct {
	svc      *FundService
	db       *gorm.DB
	repo     *repository.FundRepository
	pos      *repository.ClientFundPositionRepository
	accounts *fakeFundAccountClient
	exch     *fakeFundExchangeClient
	snapRepo *repository.FundValueSnapshotRepository
	fund     *model.InvestmentFund
}

func newFundStatsFix(t *testing.T) *fundStatsFix {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{}, &model.ClientFundPosition{}, &model.FundPositionSettlement{},
		&model.FundHolding{}, &model.Listing{}, &model.StockExchange{},
		&model.FundContribution{}, &model.FundValueSnapshot{}, &model.FundDividendPayment{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	positions := repository.NewClientFundPositionRepository(db)
	holdings := repository.NewFundHoldingRepository(db)
	contribs := repository.NewFundContributionRepository(db)
	listingRepo := repository.NewListingRepository(db)
	snapRepo := repository.NewFundValueSnapshotRepository(db)
	accounts := newFakeFundAccountClient()
	exch := &fakeFundExchangeClient{convert: "200"} // 1 USD share → 200 RSD
	bac := &fakeBankAccountClient{nextID: 8000}

	svc := NewFundService(repo, bac, nil).
		WithSaga(newFakeSagaRepo(), accounts, exch, contribs, positions, holdings, nil, nil).
		WithPositionReads(listingRepo).
		WithSnapshots(snapRepo, 2).
		WithDividendRepo(repository.NewFundDividendPaymentRepository(db))

	fund := &model.InvestmentFund{Name: "Stat", ManagerEmployeeID: 9, RSDAccountID: 7001, Active: true}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(7001, "FUND", "1000")

	// USD exchange + a USD-priced stock listing the fund holds.
	ex := &model.StockExchange{ID: 1, Acronym: "NYSE", MICCode: "XNYS", Currency: "USD"}
	if err := db.Create(ex).Error; err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	listing := &model.Listing{ID: 1, SecurityID: 10, SecurityType: "stock", ExchangeID: 1, Price: decimal.NewFromInt(2)}
	if err := db.Create(listing).Error; err != nil {
		t.Fatalf("create listing: %v", err)
	}
	fh := &model.FundHolding{FundID: fund.ID, SecurityType: "stock", SecurityID: 10, Quantity: 5, AveragePriceRSD: decimal.NewFromInt(150)}
	if err := db.Create(fh).Error; err != nil {
		t.Fatalf("create fh: %v", err)
	}

	return &fundStatsFix{svc: svc, db: db, repo: repo, pos: positions, accounts: accounts, exch: exch, snapRepo: snapRepo, fund: fund}
}

func TestFundValueRSD_WithFXConversion(t *testing.T) {
	fx := newFundStatsFix(t)
	// cash 1000 + 5 shares × 200 RSD (converted from USD) = 2000.
	v, err := fx.svc.fundValueRSD(context.Background(), fx.fund)
	if err != nil {
		t.Fatalf("fundValueRSD: %v", err)
	}
	if !v.Equal(decimal.NewFromInt(2000)) {
		t.Errorf("fund value = %s, want 2000", v)
	}
}

func TestListMyPositionsDTO_RichRow(t *testing.T) {
	fx := newFundStatsFix(t)
	uid := uint64(7)
	if err := fx.pos.IncrementContribution(fx.fund.ID, model.OwnerClient, &uid, decimal.NewFromInt(500), 1); err != nil {
		t.Fatalf("contribute: %v", err)
	}
	rows, err := fx.svc.ListMyPositionsDTO(context.Background(), model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	// Sole investor → pct 1.0; current value = fund value 2000; profit 1500.
	if !r.PercentageFund.Equal(decimal.NewFromInt(1)) {
		t.Errorf("pct = %s, want 1", r.PercentageFund)
	}
	if !r.CurrentValueRSD.Equal(decimal.NewFromInt(2000)) {
		t.Errorf("current value = %s, want 2000", r.CurrentValueRSD)
	}
	if !r.ProfitRSD.Equal(decimal.NewFromInt(1500)) {
		t.Errorf("profit = %s, want 1500", r.ProfitRSD)
	}
}

func TestFundHoldingsSnapshot(t *testing.T) {
	fx := newFundStatsFix(t)
	snaps, err := fx.svc.FundHoldingsSnapshot(context.Background(), fx.fund)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("want 1 holding snap, got %d", len(snaps))
	}
	if !snaps[0].CurrentPriceRSD.Equal(decimal.NewFromInt(2)) {
		t.Errorf("current price = %s, want 2", snaps[0].CurrentPriceRSD)
	}
	if !snaps[0].CurrentValueRSD.Equal(decimal.NewFromInt(10)) {
		t.Errorf("current value = %s, want 10 (5×2)", snaps[0].CurrentValueRSD)
	}
}

func TestFundHoldingsSnapshot_NoHoldingsRepo(t *testing.T) {
	fx := newFundFixture(t)
	snaps, err := fx.svc.FundHoldingsSnapshot(context.Background(), &model.InvestmentFund{ID: 1})
	if err != nil || snaps != nil {
		t.Errorf("want nil,nil when holdings repo not wired; got %v %v", snaps, err)
	}
}

func TestStatistics_WithDividends(t *testing.T) {
	fx := newFundStatsFix(t)
	uid := uint64(7)
	if err := fx.pos.IncrementContribution(fx.fund.ID, model.OwnerClient, &uid, decimal.NewFromInt(500), 1); err != nil {
		t.Fatalf("contribute: %v", err)
	}
	stat, err := fx.svc.Statistics(context.Background(), fx.fund)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stat.InvestorCount != 1 {
		t.Errorf("investor count = %d, want 1", stat.InvestorCount)
	}
	if !stat.TotalContributedRSD.Equal(decimal.NewFromInt(500)) {
		t.Errorf("contributed = %s, want 500", stat.TotalContributedRSD)
	}
	if !stat.LiquidRSDBal.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("liquid = %s, want 1000", stat.LiquidRSDBal)
	}
	// holdings value: 5 × 2 (listing price, no FX in Statistics) = 10.
	if !stat.TotalHoldingsValueRSD.Equal(decimal.NewFromInt(10)) {
		t.Errorf("holdings value = %s, want 10", stat.TotalHoldingsValueRSD)
	}
}

func TestFundHistory_And_AverageHistory(t *testing.T) {
	fx := newFundStatsFix(t)
	// Not wired → nil.
	plain := NewFundService(fx.repo, nil, nil)
	if plain.FundHistory(fx.fund.ID) != nil {
		t.Errorf("FundHistory should be nil when snapshots not wired")
	}
	if plain.AverageHistory() != nil {
		t.Errorf("AverageHistory should be nil when snapshots not wired")
	}

	// Seed snapshots for two funds.
	seedMonthly(t, fx.snapRepo, fx.fund.ID, []float64{100, 120, 150})
	f2 := &model.InvestmentFund{Name: "Second", ManagerEmployeeID: 1, Active: true, RSDAccountID: 7002}
	if err := fx.repo.Create(f2); err != nil {
		t.Fatalf("create f2: %v", err)
	}
	seedMonthly(t, fx.snapRepo, f2.ID, []float64{200, 220, 260})

	hist := fx.svc.FundHistory(fx.fund.ID)
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}

	avg := fx.svc.AverageHistory()
	if len(avg) != 3 {
		t.Fatalf("average history len = %d, want 3", len(avg))
	}
	// First date: both indexed to 100 → mean 100.
	if !avg[0].ValueRSD.Equal(decimal.NewFromInt(100)) {
		t.Errorf("avg[0] = %s, want 100", avg[0].ValueRSD)
	}
}

func TestIsMetricSort_And_MetricSortValue(t *testing.T) {
	for _, s := range []string{"annualized_return", "volatility", "reward_to_variability", "max_drawdown"} {
		if !IsMetricSort(s) {
			t.Errorf("IsMetricSort(%q) should be true", s)
		}
	}
	if IsMetricSort("name") {
		t.Errorf("IsMetricSort(name) should be false")
	}
	m := FundMetrics{
		AnnualizedReturnPct: decimal.NewFromInt(10),
		VolatilityPct:       decimal.NewFromInt(5),
		RewardToVariability: decimal.NewFromInt(2),
		MaxDrawdownPct:      decimal.NewFromInt(8),
	}
	for _, tc := range []struct {
		by   string
		want float64
		ok   bool
	}{
		{"annualized_return", 10, true},
		{"volatility", 5, true},
		{"reward_to_variability", 2, true},
		{"max_drawdown", 8, true},
		{"unknown", 0, false},
	} {
		got, ok := metricSortValue(m, tc.by)
		if ok != tc.ok || got != tc.want {
			t.Errorf("metricSortValue(%q) = %v,%v want %v,%v", tc.by, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFundSnapshotNextRunAt(t *testing.T) {
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	// Later today.
	got := fundSnapshotNextRunAt(now, "23:50")
	if got.Day() != 1 || got.Hour() != 23 || got.Minute() != 50 {
		t.Errorf("next run = %v, want today 23:50", got)
	}
	// Earlier than now → rolls to tomorrow.
	got = fundSnapshotNextRunAt(now, "09:00")
	if got.Day() != 2 {
		t.Errorf("next run = %v, want tomorrow", got)
	}
	// Invalid hhmm falls back to 23:50.
	got = fundSnapshotNextRunAt(now, "not-a-time")
	if got.Hour() != 23 || got.Minute() != 50 {
		t.Errorf("invalid hhmm fallback = %v, want 23:50", got)
	}
}

func TestFundSnapshotCron_StartDailyCron_RunsStartupThenStops(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// SQLite :memory: isolates tables per-connection. The StartDailyCron startup
	// goroutine can otherwise open a fresh (table-less) connection and silently
	// fail its upsert. Pin to a single connection so the migration is visible
	// to every code path.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.FundValueSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	snapRepo := repository.NewFundValueSnapshotRepository(db)
	src := &fakeFundSnapshotSource{
		funds: []model.InvestmentFund{{ID: 1}},
		stats: map[uint64]FundStatistics{1: {TotalValueRSD: decimal.NewFromInt(500)}},
	}
	// Schedule far in the future so the only thing that runs is the startup pass.
	cr := NewFundSnapshotCron(src, snapRepo, "23:59", nilRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	cr.StartDailyCron(ctx)

	// Poll until the startup snapshot lands.
	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int64
		db.Model(&model.FundValueSnapshot{}).Count(&count)
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("startup snapshot did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
}
