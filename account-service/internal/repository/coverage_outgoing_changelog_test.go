package repository

// Direct repository-package coverage for surfaces exercised only indirectly by
// the service package (which does not count toward this package's profile):
// OutgoingReservationRepository (debit-side SI-TX holds), ChangelogRepository.ListAll
// (global audit view with filters), AccountReservationRepository.DeleteSettlements,
// and the AccountReservation optimistic-lock conflict path.

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/shared"
)

// ---------------------------------------------------------------------------
// OutgoingReservationRepository
// ---------------------------------------------------------------------------

func outgoingResDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.OutgoingReservation{}))
	return db
}

func TestOutgoingReservationRepo_CreateGetByKey(t *testing.T) {
	db := outgoingResDB(t)
	repo := NewOutgoingReservationRepository(db)

	res := &model.OutgoingReservation{
		AccountNumber:  "111-A",
		Amount:         decimal.NewFromInt(300),
		Currency:       "RSD",
		ReservationKey: "ork-1",
		Status:         model.OutgoingReservationStatusPending,
		CreatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repo.Create(res))
	require.NotZero(t, res.ID)

	got, err := repo.GetByKey("ork-1")
	require.NoError(t, err)
	assert.Equal(t, res.ID, got.ID)
	assert.True(t, got.Amount.Equal(decimal.NewFromInt(300)))

	// Missing key surfaces gorm.ErrRecordNotFound.
	_, err = repo.GetByKey("nope")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOutgoingReservationRepo_MarkSettled_Idempotent(t *testing.T) {
	db := outgoingResDB(t)
	repo := NewOutgoingReservationRepository(db)

	require.NoError(t, repo.Create(&model.OutgoingReservation{
		AccountNumber: "111-A", Amount: decimal.NewFromInt(100), Currency: "RSD",
		ReservationKey: "ork-settle", Status: model.OutgoingReservationStatusPending,
		CreatedAt: time.Now().UTC(),
	}))

	// Pending → settled.
	require.NoError(t, repo.MarkSettled(db, "ork-settle"))
	got, _ := repo.GetByKey("ork-settle")
	assert.Equal(t, model.OutgoingReservationStatusSettled, got.Status)

	// Second settle on a non-pending row is a no-op (RowsAffected=0, no error).
	require.NoError(t, repo.MarkSettled(db, "ork-settle"))
	got2, _ := repo.GetByKey("ork-settle")
	assert.Equal(t, model.OutgoingReservationStatusSettled, got2.Status)
}

func TestOutgoingReservationRepo_MarkReleased_Idempotent(t *testing.T) {
	db := outgoingResDB(t)
	repo := NewOutgoingReservationRepository(db)

	require.NoError(t, repo.Create(&model.OutgoingReservation{
		AccountNumber: "111-A", Amount: decimal.NewFromInt(100), Currency: "RSD",
		ReservationKey: "ork-rel", Status: model.OutgoingReservationStatusPending,
		CreatedAt: time.Now().UTC(),
	}))

	require.NoError(t, repo.MarkReleased(db, "ork-rel"))
	got, _ := repo.GetByKey("ork-rel")
	assert.Equal(t, model.OutgoingReservationStatusReleased, got.Status)

	// A released row cannot be settled — guard keeps it released.
	require.NoError(t, repo.MarkSettled(db, "ork-rel"))
	got2, _ := repo.GetByKey("ork-rel")
	assert.Equal(t, model.OutgoingReservationStatusReleased, got2.Status)
}

func TestOutgoingReservationRepo_WithTx_Create(t *testing.T) {
	db := outgoingResDB(t)
	repo := NewOutgoingReservationRepository(db)

	err := db.Transaction(func(tx *gorm.DB) error {
		return repo.WithTx(tx).Create(&model.OutgoingReservation{
			AccountNumber: "111-A", Amount: decimal.NewFromInt(50), Currency: "RSD",
			ReservationKey: "ork-tx", Status: model.OutgoingReservationStatusPending,
			CreatedAt: time.Now().UTC(),
		})
	})
	require.NoError(t, err)
	got, err := repo.GetByKey("ork-tx")
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestOutgoingReservationRepo_ListStalePendingOlderThan(t *testing.T) {
	db := outgoingResDB(t)
	repo := NewOutgoingReservationRepository(db)

	// Stale pending row.
	require.NoError(t, repo.Create(&model.OutgoingReservation{
		AccountNumber: "111-A", Amount: decimal.NewFromInt(10), Currency: "RSD",
		ReservationKey: "ork-stale", Status: model.OutgoingReservationStatusPending,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}))
	// Fresh pending row — excluded.
	require.NoError(t, repo.Create(&model.OutgoingReservation{
		AccountNumber: "111-A", Amount: decimal.NewFromInt(10), Currency: "RSD",
		ReservationKey: "ork-fresh", Status: model.OutgoingReservationStatusPending,
		CreatedAt: time.Now().UTC(),
	}))
	// Stale but already settled — excluded (status filter).
	require.NoError(t, repo.Create(&model.OutgoingReservation{
		AccountNumber: "111-A", Amount: decimal.NewFromInt(10), Currency: "RSD",
		ReservationKey: "ork-settled", Status: model.OutgoingReservationStatusSettled,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}))

	stale, err := repo.ListStalePendingOlderThan(time.Now().UTC().Add(-10*time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "ork-stale", stale[0].ReservationKey)
}

// ---------------------------------------------------------------------------
// ChangelogRepository.ListAll — global audit view + filters
// ---------------------------------------------------------------------------

func TestChangelogRepo_ListAll_Filters(t *testing.T) {
	db := changelogTestDB(t)
	repo := NewChangelogRepository(db)

	// Use local-zoned times: production filters with time.Unix(...) which yields
	// local wall-clock, so stored rows must share that zone for a deterministic
	// string comparison in SQLite regardless of the host timezone.
	base := time.Now().Truncate(time.Hour).Add(-6 * time.Hour)
	require.NoError(t, repo.CreateBatch([]changelog.Entry{
		{EntityType: "account", EntityID: 1, Action: "create", ChangedBy: 5, ChangedAt: base},
		{EntityType: "account", EntityID: 2, Action: "update", ChangedBy: 6, ChangedAt: base.Add(1 * time.Hour)},
		{EntityType: "card", EntityID: 3, Action: "block", ChangedBy: 5, ChangedAt: base.Add(2 * time.Hour)},
	}))

	// No filters → all three, newest first.
	all, total, err := repo.ListAll(ChangelogFilters{}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, all, 3)
	assert.Equal(t, "block", all[0].Action, "ListAll orders by changed_at DESC")

	// Actor filter.
	byActor, total, err := repo.ListAll(ChangelogFilters{ActorID: 5}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, byActor, 2)

	// Action filter.
	byAction, total, err := repo.ListAll(ChangelogFilters{Action: "update"}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, byAction, 1)
	assert.Equal(t, "update", byAction[0].Action)

	// Time window filter (Since/Until) — only the middle row.
	windowed, total, err := repo.ListAll(ChangelogFilters{
		Since: base.Add(30 * time.Minute).Unix(),
		Until: base.Add(90 * time.Minute).Unix(),
	}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, windowed, 1)
	assert.Equal(t, int64(2), windowed[0].EntityID)
}

// ---------------------------------------------------------------------------
// AccountReservationRepository.DeleteSettlements + optimistic-lock conflict
// ---------------------------------------------------------------------------

func TestReservationRepo_DeleteSettlements(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 6001, Amount: decimal.NewFromInt(1000),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	require.NoError(t, repo.Create(r))
	require.NoError(t, repo.CreateSettlement(&model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9101, Amount: decimal.NewFromInt(100),
	}))
	require.NoError(t, repo.CreateSettlement(&model.AccountReservationSettlement{
		ReservationID: r.ID, OrderTransactionID: 9102, Amount: decimal.NewFromInt(200),
	}))

	// Clearing settlements resets the settled total to zero.
	require.NoError(t, repo.DeleteSettlements(r.ID))
	got, err := repo.ListSettlements(r.ID)
	require.NoError(t, err)
	assert.Empty(t, got)

	sum, err := repo.SumSettlements(r.ID)
	require.NoError(t, err)
	assert.True(t, sum.IsZero())
}

func TestReservationRepo_UpdateStatus_OptimisticLockConflict(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 6101, Amount: decimal.NewFromInt(500),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	require.NoError(t, repo.Create(r))

	// Take a stale snapshot at the current version.
	stale, err := repo.GetByOrderID(6101, "")
	require.NoError(t, err)

	// A concurrent winner bumps the version.
	winner, err := repo.GetByOrderID(6101, "")
	require.NoError(t, err)
	winner.Status = model.ReservationStatusReleased
	require.NoError(t, repo.UpdateStatus(winner))

	// The stale writer must lose: RowsAffected==0 → ErrOptimisticLock.
	stale.Status = model.ReservationStatusSettled
	err = repo.UpdateStatus(stale)
	require.ErrorIs(t, err, shared.ErrOptimisticLock)
}
