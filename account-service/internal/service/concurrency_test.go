package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Per-test named in-memory database with shared cache: all goroutines in
	// the same test share the same DB, tests don't pollute each other.
	// SetMaxOpenConns(1) serializes all access — SQLite does not support
	// truly concurrent writes anyway, so this is always safe.
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Account{},
		&model.LedgerEntry{},
		&model.AccountReservation{},
		&model.AccountReservationSettlement{},
	))
	return db
}

func seedAccount(t *testing.T, db *gorm.DB, accountNumber string, balance decimal.Decimal, dailyLimit decimal.Decimal) *model.Account {
	t.Helper()
	acct := &model.Account{
		AccountNumber:    accountNumber,
		OwnerID:          1,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          balance,
		AvailableBalance: balance,
		DailyLimit:       dailyLimit,
		MonthlyLimit:     decimal.NewFromInt(10_000_000),
		IsBankAccount:    false,
		ExpiresAt:        time.Now().AddDate(5, 0, 0),
		Version:          1,
	}
	require.NoError(t, db.Create(acct).Error)
	return acct
}

// TestConcurrentDebitDoesNotOverdraft verifies that concurrent debits never result in
// a negative balance. SQLite serializes transactions at the DB level, so this test
// confirms the logic (sufficient-funds check → update) is correct when serialized.
func TestConcurrentDebitDoesNotOverdraft(t *testing.T) {
	db := newTestDB(t)
	acctNumber := "111000100000000011"
	seedAccount(t, db, acctNumber, decimal.NewFromInt(1000), decimal.NewFromInt(10_000_000))

	repo := repository.NewAccountRepository(db)

	// 12 goroutines each try to debit 100; only 10 should succeed (balance = 1000).
	const workers = 12
	const debitEach = 100
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, err := repo.UpdateBalance(acctNumber, decimal.NewFromInt(-debitEach), true, repository.UpdateBalanceOpts{})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 10, successCount, "exactly 10 debits of 100 should succeed against a balance of 1000")
	assert.Equal(t, 2, failCount, "2 debits should fail due to insufficient funds")

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", acctNumber).First(&acct).Error)
	assert.True(t, acct.Balance.Equal(decimal.Zero), "final balance must be exactly 0")
	assert.True(t, acct.AvailableBalance.Equal(decimal.Zero), "available balance must be exactly 0")
}

// TestConcurrentSpendingTracking verifies that spending counters are updated
// correctly under concurrent debits.
func TestConcurrentSpendingTracking(t *testing.T) {
	db := newTestDB(t)
	acctNumber := "111000100000000022"
	seedAccount(t, db, acctNumber, decimal.NewFromInt(5000), decimal.NewFromInt(10_000_000))

	repo := repository.NewAccountRepository(db)

	const workers = 5
	const debitEach = 200
	var wg sync.WaitGroup

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _ = repo.UpdateBalance(acctNumber, decimal.NewFromInt(-debitEach), true, repository.UpdateBalanceOpts{})
		}()
	}
	wg.Wait()

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", acctNumber).First(&acct).Error)

	// 5 debits × 200 = 1000 spent; balance should be 4000.
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(4000)), "balance should be 4000 after 5 × 200 debits")
	assert.True(t, acct.DailySpending.Equal(decimal.NewFromInt(1000)), "daily spending should equal total debited")
	assert.True(t, acct.MonthlySpending.Equal(decimal.NewFromInt(1000)), "monthly spending should equal total debited")
}

// TestSpendingLimitEnforcedConcurrently verifies that the daily spending limit
// rejects debits that would exceed it, even under concurrent load.
func TestSpendingLimitEnforcedConcurrently(t *testing.T) {
	db := newTestDB(t)
	acctNumber := "111000100000000033"
	// Balance 10000 but daily limit only 500 — at most 5 debits of 100 should succeed.
	seedAccount(t, db, acctNumber, decimal.NewFromInt(10_000), decimal.NewFromInt(500))

	repo := repository.NewAccountRepository(db)

	const workers = 10
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, err := repo.UpdateBalance(acctNumber, decimal.NewFromInt(-100), true, repository.UpdateBalanceOpts{})
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 5, successCount, "daily limit of 500 allows exactly 5 debits of 100")

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", acctNumber).First(&acct).Error)
	assert.True(t, acct.DailySpending.LessThanOrEqual(decimal.NewFromInt(500)),
		"daily spending must never exceed the limit")
}

// TestOptimisticLockConflict verifies the version-based WHERE clause pattern used
// by BeforeUpdate hooks: updating a row with a stale version affects 0 rows.
// This test exercises the SQL constraint directly (not via the GORM hook) to
// ensure the underlying database enforces the version guard.
func TestOptimisticLockConflict(t *testing.T) {
	db := newTestDB(t)
	acctNumber := "111000100000000044"
	acct := seedAccount(t, db, acctNumber, decimal.NewFromInt(5000), decimal.NewFromInt(10_000_000))

	// Simulate a successful update: bump version from 1 → 2.
	result := db.Exec(
		"UPDATE accounts SET account_name=?, version=? WHERE id=? AND version=?",
		"Updated Name", 2, acct.ID, 1,
	)
	require.NoError(t, result.Error)
	assert.Equal(t, int64(1), result.RowsAffected, "update with correct version must affect 1 row")

	// Simulate a concurrent stale update: use old version=1 against DB that now has version=2.
	staleResult := db.Exec(
		"UPDATE accounts SET account_name=?, version=? WHERE id=? AND version=?",
		"Stale Name", 2, acct.ID, 1,
	)
	require.NoError(t, staleResult.Error)
	assert.Equal(t, int64(0), staleResult.RowsAffected,
		"stale version update must affect 0 rows — the optimistic lock check works at DB level")

	// DB must reflect the first successful update only.
	var final model.Account
	require.NoError(t, db.First(&final, acct.ID).Error)
	assert.Equal(t, "Updated Name", final.AccountName)
	assert.Equal(t, int64(2), final.Version)
}
