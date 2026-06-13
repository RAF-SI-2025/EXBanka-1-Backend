package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/cache"
	"github.com/exbanka/account-service/internal/repository"
)

// newTestCacheRC returns a real RedisCache backed by miniredis for invalidation tests.
func newTestCacheRC(t *testing.T) *cache.RedisCache {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestOutgoingReservation_EvictsAccountCache proves the cross-bank outgoing
// reserve→settle path invalidates the cached account, so GetAccountByNumber
// returns the FRESH balance instead of the value cached before the debit. This
// is the stale-balance bug Phase B closes: previously the outgoing service held
// no cache reference and never evicted.
func TestOutgoingReservation_EvictsAccountCache(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)
	outSvc := NewOutgoingReservationService(db, acctRepo, repository.NewOutgoingReservationRepository(db)).WithCache(rc)

	seedAccount(t, db, "111000100000777011", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))

	// Prime the cache with balance 1000.
	got, err := acctSvc.GetAccountByNumber("111000100000777011")
	require.NoError(t, err)
	require.True(t, got.Balance.Equal(decimal.NewFromInt(1000)))

	// Cross-bank debit of 300 through reserve→settle.
	_, err = outSvc.ReserveOutgoing(context.Background(), "111000100000777011", decimal.NewFromInt(300), "RSD", "ob-key-1")
	require.NoError(t, err)
	_, err = outSvc.SettleOutgoing(context.Background(), "ob-key-1")
	require.NoError(t, err)

	// The read must reflect the settled balance (700), not the stale cached 1000.
	fresh, err := acctSvc.GetAccountByNumber("111000100000777011")
	require.NoError(t, err)
	assert.True(t, fresh.Balance.Equal(decimal.NewFromInt(700)),
		"GetAccountByNumber must return the post-settle balance 700, got %s (stale cache)", fresh.Balance)
}

// TestIncomingReservation_EvictsAccountCache proves CommitIncoming (the credit
// leg) invalidates the cached account.
func TestIncomingReservation_EvictsAccountCache(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)
	inSvc := NewIncomingReservationService(db, acctRepo, repository.NewIncomingReservationRepository(db)).WithCache(rc)

	seedAccount(t, db, "111000100000888011", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))

	got, err := acctSvc.GetAccountByNumber("111000100000888011")
	require.NoError(t, err)
	require.True(t, got.Balance.Equal(decimal.NewFromInt(1000)))

	_, err = inSvc.ReserveIncoming(context.Background(), "111000100000888011", decimal.NewFromInt(250), "RSD", "ib-key-1")
	require.NoError(t, err)
	_, err = inSvc.CommitIncoming(context.Background(), "ib-key-1", "")
	require.NoError(t, err)

	fresh, err := acctSvc.GetAccountByNumber("111000100000888011")
	require.NoError(t, err)
	assert.True(t, fresh.Balance.Equal(decimal.NewFromInt(1250)),
		"GetAccountByNumber must return the post-commit balance 1250, got %s (stale cache)", fresh.Balance)
}
