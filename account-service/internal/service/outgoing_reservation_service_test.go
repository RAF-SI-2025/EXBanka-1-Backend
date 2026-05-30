package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

func newOutgoingReservationService(t *testing.T) (*OutgoingReservationService, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	accountRepo := repository.NewAccountRepository(db)
	resRepo := repository.NewOutgoingReservationRepository(db)
	return NewOutgoingReservationService(db, accountRepo, resRepo), db
}

func reloadAccount(t *testing.T, db *gorm.DB, accountNumber string) *model.Account {
	t.Helper()
	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", accountNumber).First(&acct).Error)
	return &acct
}

// TestReserveOutgoing_HoldsAvailableNotBalance verifies the reserve leg dips
// AvailableBalance only — Balance is untouched until settle.
func TestReserveOutgoing_HoldsAvailableNotBalance(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	res, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)
	assert.Equal(t, model.OutgoingReservationStatusPending, res.Status)

	acct := reloadAccount(t, db, "111-A")
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(1000)), "Balance must be untouched, got %s", acct.Balance)
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(700)), "AvailableBalance must drop by hold, got %s", acct.AvailableBalance)
}

// TestReserveOutgoing_Idempotent verifies a replayed key returns the same row
// and does not double-deduct AvailableBalance.
func TestReserveOutgoing_Idempotent(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)
	_, err = svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)

	acct := reloadAccount(t, db, "111-A")
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(700)), "replay must not double-deduct, got %s", acct.AvailableBalance)
}

// TestReserveOutgoing_InsufficientAvailable rejects a hold above available.
func TestReserveOutgoing_InsufficientAvailable(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(100), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// TestSettleOutgoing_DebitsBalanceAndWritesLedger verifies settle moves Balance
// down (Available already reduced), writes a debit ledger entry, and is idempotent.
func TestSettleOutgoing_DebitsBalanceAndWritesLedger(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)

	acct, err := svc.SettleOutgoing(context.Background(), "k1")
	require.NoError(t, err)
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(700)), "Balance must drop on settle, got %s", acct.Balance)

	reloaded := reloadAccount(t, db, "111-A")
	assert.True(t, reloaded.Balance.Equal(decimal.NewFromInt(700)))
	assert.True(t, reloaded.AvailableBalance.Equal(decimal.NewFromInt(700)), "Available stays at the post-reserve level, got %s", reloaded.AvailableBalance)

	var ledgerCount int64
	require.NoError(t, db.Model(&model.LedgerEntry{}).
		Where("account_number = ? AND entry_type = ?", "111-A", "debit").Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)

	// Idempotent settle: a second call is a no-op on Balance.
	_, err = svc.SettleOutgoing(context.Background(), "k1")
	require.NoError(t, err)
	reloaded = reloadAccount(t, db, "111-A")
	assert.True(t, reloaded.Balance.Equal(decimal.NewFromInt(700)), "double-settle must not re-debit, got %s", reloaded.Balance)
}

// TestReleaseOutgoing_RestoresAvailable verifies release returns the hold to
// AvailableBalance with no Balance movement and is idempotent.
func TestReleaseOutgoing_RestoresAvailable(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)

	require.NoError(t, svc.ReleaseOutgoing(context.Background(), "k1"))
	acct := reloadAccount(t, db, "111-A")
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(1000)), "Balance untouched, got %s", acct.Balance)
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(1000)), "Available restored, got %s", acct.AvailableBalance)

	// Idempotent: a second release is a no-op (no double-credit of Available).
	require.NoError(t, svc.ReleaseOutgoing(context.Background(), "k1"))
	acct = reloadAccount(t, db, "111-A")
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(1000)), "double-release must not over-credit, got %s", acct.AvailableBalance)
}

// TestSettleAfterRelease_Rejected verifies settle is refused once released —
// the timeout cron may release a stale hold and a late COMMIT must not re-debit.
func TestSettleAfterRelease_Rejected(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseOutgoing(context.Background(), "k1"))

	_, err = svc.SettleOutgoing(context.Background(), "k1")
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// TestListStalePending_RespectsCutoff verifies the cron sweep query only
// returns pending rows older than the cutoff.
func TestListStalePending_RespectsCutoff(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "k1")
	require.NoError(t, err)

	// Backdate the row so it's "stale".
	require.NoError(t, db.Model(&model.OutgoingReservation{}).
		Where("reservation_key = ?", "k1").
		Update("created_at", time.Now().UTC().Add(-30*time.Minute)).Error)

	// Cutoff 10m ago — the 30m-old row is stale.
	stale, err := svc.ListStalePending(time.Now().UTC().Add(-10*time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "k1", stale[0].ReservationKey)

	// A far-past cutoff (1h ago) excludes the 30m-old row.
	none, err := svc.ListStalePending(time.Now().UTC().Add(-1*time.Hour), 100)
	require.NoError(t, err)
	assert.Empty(t, none)
}

// TestOutgoingReservationTimeoutCron_ReleasesStaleHold verifies the time-safety
// backstop: a pending hold older than the TTL is released by the cron sweep
// (peer never sent COMMIT/ROLLBACK), returning funds to AvailableBalance. A
// fresh hold is left untouched.
func TestOutgoingReservationTimeoutCron_ReleasesStaleHold(t *testing.T) {
	svc, db := newOutgoingReservationService(t)
	seedAccount(t, db, "111-A", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	// Stale hold (backdated 30m) + fresh hold.
	_, err := svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(300), "RSD", "stale")
	require.NoError(t, err)
	_, err = svc.ReserveOutgoing(context.Background(), "111-A", decimal.NewFromInt(200), "RSD", "fresh")
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.OutgoingReservation{}).
		Where("reservation_key = ?", "stale").
		Update("created_at", time.Now().UTC().Add(-30*time.Minute)).Error)

	// AvailableBalance after both holds: 1000 - 300 - 200 = 500.
	acct := reloadAccount(t, db, "111-A")
	require.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(500)))

	cron := NewOutgoingReservationTimeoutCron(svc, 10*time.Minute, nilRegistry())
	require.NoError(t, cron.Sweep(context.Background()))

	// Stale hold released → +300 back; fresh hold untouched.
	acct = reloadAccount(t, db, "111-A")
	assert.True(t, acct.AvailableBalance.Equal(decimal.NewFromInt(800)), "stale hold should be released, got %s", acct.AvailableBalance)
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(1000)), "Balance never moved, got %s", acct.Balance)

	var staleRow model.OutgoingReservation
	require.NoError(t, db.Where("reservation_key = ?", "stale").First(&staleRow).Error)
	assert.Equal(t, model.OutgoingReservationStatusReleased, staleRow.Status)

	var freshRow model.OutgoingReservation
	require.NoError(t, db.Where("reservation_key = ?", "fresh").First(&freshRow).Error)
	assert.Equal(t, model.OutgoingReservationStatusPending, freshRow.Status)
}
