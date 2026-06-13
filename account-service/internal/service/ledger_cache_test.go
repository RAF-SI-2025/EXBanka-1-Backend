package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/repository"
)

// TestLedgerCredit_EvictsBothCacheKeys proves a direct ledger credit — the path
// OTC premium credits, the cross-bank "otc-accept" premium credit, fees, and
// interest all flow through — invalidates BOTH cached account keys (by-number
// AND by-id), so a subsequent read by either key returns the fresh balance.
// Closes the stale-balance bug where LedgerService held no cache reference and
// never evicted (a GET /me/accounts/:id read kept serving the pre-credit value).
func TestLedgerCredit_EvictsBothCacheKeys(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db).WithCache(rc)

	acct := seedAccount(t, db, "111000100000999011", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))

	// Prime BOTH caches (by number and by id) with balance 1000.
	byNum, err := acctSvc.GetAccountByNumber("111000100000999011")
	require.NoError(t, err)
	require.True(t, byNum.Balance.Equal(decimal.NewFromInt(1000)))
	byID, err := acctSvc.GetAccount(acct.ID)
	require.NoError(t, err)
	require.True(t, byID.Balance.Equal(decimal.NewFromInt(1000)))

	// Direct ledger credit of 150 (OTC-premium-like).
	require.NoError(t, ledgerSvc.Credit(context.Background(), "111000100000999011",
		decimal.NewFromInt(150), "OTC premium credit", "neg-1", "otc"))

	// Both read paths must reflect 1150 (not the stale cached 1000).
	freshNum, err := acctSvc.GetAccountByNumber("111000100000999011")
	require.NoError(t, err)
	assert.True(t, freshNum.Balance.Equal(decimal.NewFromInt(1150)),
		"by-number read stale: %s (cache not evicted)", freshNum.Balance)
	freshID, err := acctSvc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.True(t, freshID.Balance.Equal(decimal.NewFromInt(1150)),
		"by-id read stale: %s (id cache key not evicted)", freshID.Balance)
}

// TestUpdateBalance_EvictsBothCacheKeys is the regression test for the actual
// stale-balance bug: the OTC premium credit flows through CreditAccount →
// UpdateBalance gRPC → AccountService.UpdateBalanceWithOpts, which previously
// evicted only the by-NUMBER cache key (id=0), leaving account:id:N stale so a
// GET /me/accounts/:id read kept serving the pre-credit balance.
func TestUpdateBalance_EvictsBothCacheKeys(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)

	acct := seedAccount(t, db, "111000100000999033", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))

	// Prime BOTH read caches with 1000.
	byNum, err := acctSvc.GetAccountByNumber("111000100000999033")
	require.NoError(t, err)
	require.True(t, byNum.Balance.Equal(decimal.NewFromInt(1000)))
	byID, err := acctSvc.GetAccount(acct.ID)
	require.NoError(t, err)
	require.True(t, byID.Balance.Equal(decimal.NewFromInt(1000)))

	// Credit 200 via the UpdateBalance path (what CreditAccount RPC uses).
	require.NoError(t, acctSvc.UpdateBalanceWithOpts("111000100000999033", decimal.NewFromInt(200), true,
		repository.UpdateBalanceOpts{Memo: "OTC premium credit", IdempotencyKey: "otc-prem-1"}))

	// The by-ID read must be fresh (1200) — the key that was stale before the fix.
	freshID, err := acctSvc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.True(t, freshID.Balance.Equal(decimal.NewFromInt(1200)),
		"by-id read stale: %s (UpdateBalance left account:id:N cached)", freshID.Balance)
	freshNum, err := acctSvc.GetAccountByNumber("111000100000999033")
	require.NoError(t, err)
	assert.True(t, freshNum.Balance.Equal(decimal.NewFromInt(1200)), "by-number read stale: %s", freshNum.Balance)
}

// TestLedgerDebit_EvictsCache proves a direct ledger debit evicts the cache.
func TestLedgerDebit_EvictsCache(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db).WithCache(rc)

	seedAccount(t, db, "111000100000999022", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))
	primed, err := acctSvc.GetAccountByNumber("111000100000999022")
	require.NoError(t, err)
	require.True(t, primed.Balance.Equal(decimal.NewFromInt(1000)))

	require.NoError(t, ledgerSvc.Debit(context.Background(), "111000100000999022",
		decimal.NewFromInt(400), "fee", "f-1", "fee"))

	fresh, err := acctSvc.GetAccountByNumber("111000100000999022")
	require.NoError(t, err)
	assert.True(t, fresh.Balance.Equal(decimal.NewFromInt(600)),
		"post-debit read stale: %s", fresh.Balance)
}

// TestLedgerTransfer_EvictsBothAccounts proves Transfer evicts the debited AND
// credited accounts (both sides change).
func TestLedgerTransfer_EvictsBothAccounts(t *testing.T) {
	db := newTestDB(t)
	rc := newTestCacheRC(t)
	acctRepo := repository.NewAccountRepository(db)
	acctSvc := NewAccountService(acctRepo, db, rc)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db).WithCache(rc)

	seedAccount(t, db, "111000100000123011", decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))
	seedAccount(t, db, "111000100000456011", decimal.NewFromInt(500), decimal.NewFromInt(10_000_000))
	_, _ = acctSvc.GetAccountByNumber("111000100000123011") // prime
	_, _ = acctSvc.GetAccountByNumber("111000100000456011") // prime

	require.NoError(t, ledgerSvc.Transfer(context.Background(),
		"111000100000123011", "111000100000456011", decimal.NewFromInt(200), "xfer", "r1", "transfer"))

	from, err := acctSvc.GetAccountByNumber("111000100000123011")
	require.NoError(t, err)
	to, err := acctSvc.GetAccountByNumber("111000100000456011")
	require.NoError(t, err)
	assert.True(t, from.Balance.Equal(decimal.NewFromInt(800)), "from stale: %s", from.Balance)
	assert.True(t, to.Balance.Equal(decimal.NewFromInt(700)), "to stale: %s", to.Balance)
}
