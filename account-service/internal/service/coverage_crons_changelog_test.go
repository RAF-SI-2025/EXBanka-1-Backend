package service

// Coverage for surfaces with no direct service-package tests yet:
//   - SpendingCronService.Start + runDailyReset/runMonthlyReset (driven via the
//     cronreg TriggerChan so the reset bodies run without waiting on the timer).
//   - OutgoingReservationTimeoutCron.Start + loop + WithTickInterval.
//   - ChangelogService.ListChangelog/ListAllChangelogs (validation + paging).
//   - AccountService.SetAccountCategory.

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/cronreg"
)

// waitForRunCount polls the registry until the named cron has executed at least
// `want` runs or the deadline elapses.
func waitForRunCount(t *testing.T, reg *cronreg.Registry, name string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := reg.Get(name)
		require.NoError(t, err)
		if info.RunCount >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("cron %q did not reach run count %d in time", name, want)
}

// ---------------------------------------------------------------------------
// SpendingCronService — driven via admin trigger
// ---------------------------------------------------------------------------

func TestSpendingCron_TriggerResetsCounters(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)

	a := seedAccount(t, db, "111000100000000401", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))
	// Pre-load spending counters (skip hooks to avoid the version guard).
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).
		Where("id = ?", a.ID).Updates(map[string]interface{}{
		"daily_spending":   decimal.NewFromInt(123),
		"monthly_spending": decimal.NewFromInt(456),
	}).Error)

	reg := cronreg.NewRegistry("test", nil)
	cron := NewSpendingCronService(repo, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cron.Start(ctx)

	require.NoError(t, reg.Trigger("daily-spending-reset", false, 0))
	waitForRunCount(t, reg, "daily-spending-reset", 1)

	require.NoError(t, reg.Trigger("monthly-spending-reset", false, 0))
	waitForRunCount(t, reg, "monthly-spending-reset", 1)

	got, err := repo.GetByID(a.ID)
	require.NoError(t, err)
	assert.True(t, got.DailySpending.IsZero(), "daily spending must be reset, got %s", got.DailySpending)
	assert.True(t, got.MonthlySpending.IsZero(), "monthly spending must be reset, got %s", got.MonthlySpending)
}

// ---------------------------------------------------------------------------
// OutgoingReservationTimeoutCron — Start + loop + WithTickInterval
// ---------------------------------------------------------------------------

func TestOutgoingReservationTimeoutCron_LoopReleasesViaTrigger(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "stale-loop")
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.OutgoingReservation{}).
		Where("reservation_key = ?", "stale-loop").
		Update("created_at", time.Now().UTC().Add(-30*time.Minute)).Error)

	reg := cronreg.NewRegistry("test", nil)
	cron := NewOutgoingReservationTimeoutCron(svc, 10*time.Minute, reg).
		WithTickInterval(time.Hour) // long tick so only the explicit trigger fires

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cron.Start(ctx)

	require.NoError(t, reg.Trigger("outgoing-reservation-timeout", false, 0))
	waitForRunCount(t, reg, "outgoing-reservation-timeout", 1)

	acct := reloadAccount(t, db, "111-A")
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(1000)),
		"stale hold should be released by the cron loop, got %s", acct.AvailableBalance)
}

func TestOutgoingReservationTimeoutCron_WithTickInterval_DefaultsAndOverride(t *testing.T) {
	svc, _ := newOutgoingReservationService(t)

	// ttl <= 0 falls back to the 10-minute default.
	cron := NewOutgoingReservationTimeoutCron(svc, 0, cronreg.NewRegistry("test", nil))
	assert.Equal(t, 10*time.Minute, cron.ttl)
	assert.Equal(t, time.Minute, cron.tick)

	// A non-positive override is ignored; a positive one takes effect.
	cron.WithTickInterval(0)
	assert.Equal(t, time.Minute, cron.tick)
	cron.WithTickInterval(15 * time.Second)
	assert.Equal(t, 15*time.Second, cron.tick)
}

// ---------------------------------------------------------------------------
// ChangelogService
// ---------------------------------------------------------------------------

func changelogServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	// SQLite does not understand the postgres-flavored `default:now()` clause on
	// model.Changelog.ChangedAt, so AutoMigrate(&model.Changelog{}) errors with a
	// "near (" syntax error. Create the table with SQLite-compatible DDL that
	// matches the live schema column-for-column (mirrors credit-service's
	// changelog repo test).
	require.NoError(t, db.Exec(`
		CREATE TABLE changelogs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			field_name TEXT,
			old_value TEXT,
			new_value TEXT,
			changed_by INTEGER NOT NULL,
			changed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			reason TEXT
		)
	`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX idx_changelog_entity ON changelogs (entity_type, entity_id)`).Error)
	return db
}

func TestChangelogService_ListChangelog_Validation(t *testing.T) {
	db := changelogServiceDB(t)
	svc := NewChangelogService(repository.NewChangelogRepository(db))

	_, _, err := svc.ListChangelog("", 1, 1, 10)
	require.Error(t, err)

	_, _, err = svc.ListChangelog("account", 0, 1, 10)
	require.Error(t, err)
}

func TestChangelogService_ListChangelog_PagingDefaults(t *testing.T) {
	db := changelogServiceDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "account", EntityID: 7, Action: "create", ChangedBy: 1, ChangedAt: time.Now(),
	}))

	// page<1 and pageSize<=0 are normalized to defaults.
	entries, total, err := svc.ListChangelog("account", 7, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, entries, 1)

	// pageSize over the cap is clamped (still returns the row).
	entries, _, err = svc.ListChangelog("account", 7, 1, 5000)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestChangelogService_ListAllChangelogs_PagingDefaults(t *testing.T) {
	db := changelogServiceDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	require.NoError(t, repo.CreateBatch([]changelog.Entry{
		{EntityType: "account", EntityID: 1, Action: "create", ChangedBy: 5, ChangedAt: time.Now()},
		{EntityType: "card", EntityID: 2, Action: "block", ChangedBy: 6, ChangedAt: time.Now()},
	}))

	entries, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, entries, 2)

	// Over-cap pageSize is clamped to 200.
	entries, _, err = svc.ListAllChangelogs(repository.ChangelogFilters{}, 1, 9999)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

// ---------------------------------------------------------------------------
// AccountService.SetAccountCategory
// ---------------------------------------------------------------------------

func TestAccountService_SetAccountCategory(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	require.NoError(t, svc.SetAccountCategory(acct.ID, "premium"))

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.Equal(t, "premium", got.AccountCategory)
}
