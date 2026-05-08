package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func newLoginAttemptFixture(t *testing.T) *LoginAttemptRepository {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.LoginAttempt{}, &model.AccountLock{})
	return NewLoginAttemptRepository(db)
}

func TestLoginAttemptRepository_RecordAttempt(t *testing.T) {
	repo := newLoginAttemptFixture(t)
	require.NoError(t, repo.RecordAttempt("u@test.com", "1.2.3.4", "ua", "browser", true))

	attempts, err := repo.ListRecentByEmail("u@test.com", 10)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)
	assert.True(t, attempts[0].Success)
	assert.Equal(t, "browser", attempts[0].DeviceType)
}

func TestLoginAttemptRepository_CountRecentFailedAttempts(t *testing.T) {
	repo := newLoginAttemptFixture(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.RecordAttempt("u@test.com", "ip", "ua", "browser", false))
	}
	require.NoError(t, repo.RecordAttempt("u@test.com", "ip", "ua", "browser", true))

	count, err := repo.CountRecentFailedAttempts("u@test.com", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count, "must count only failed attempts")

	count, err = repo.CountRecentFailedAttempts("ghost@test.com", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestLoginAttemptRepository_LockUnlock(t *testing.T) {
	repo := newLoginAttemptFixture(t)
	require.NoError(t, repo.LockAccount("u@test.com", 30*time.Minute))

	lock, err := repo.GetActiveLock("u@test.com")
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.Equal(t, "u@test.com", lock.Email)
	assert.True(t, lock.ExpiresAt.After(time.Now()))

	// Unlock manually
	require.NoError(t, repo.UnlockAccount("u@test.com"))

	lock, err = repo.GetActiveLock("u@test.com")
	require.NoError(t, err)
	assert.Nil(t, lock, "after UnlockAccount, GetActiveLock should be nil")
}

func TestLoginAttemptRepository_GetActiveLock_NoLock(t *testing.T) {
	repo := newLoginAttemptFixture(t)
	lock, err := repo.GetActiveLock("u@test.com")
	require.NoError(t, err)
	assert.Nil(t, lock)
}

func TestLoginAttemptRepository_ListRecentByEmail_OrdersNewestFirst(t *testing.T) {
	repo := newLoginAttemptFixture(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.RecordAttempt("u@test.com", "ip", "ua", "browser", i%2 == 0))
		// staggered timestamps via tiny sleep — sqlite resolution is millisecond,
		// so we just rely on insertion order plus desc-by-created_at.
		time.Sleep(2 * time.Millisecond)
	}

	got, err := repo.ListRecentByEmail("u@test.com", 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	for i := 0; i < len(got)-1; i++ {
		assert.False(t, got[i].CreatedAt.Before(got[i+1].CreatedAt), "results must be ordered newest first")
	}
}
