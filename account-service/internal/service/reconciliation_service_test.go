package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

// newReconciliationService wires a ReconciliationService backed by an
// in-memory SQLite test database that already has the Account and
// LedgerEntry tables migrated.
func newReconciliationService(t *testing.T) (*ReconciliationService, *LedgerService) {
	t.Helper()
	db := newTestDB(t)
	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := NewLedgerService(ledgerRepo, db)
	reconcileSvc := NewReconciliationService(db, ledgerSvc)
	return reconcileSvc, ledgerSvc
}

// seedAccountForReconcile inserts a minimal Account row with a known balance
// and account number. Unlike seedAccount (in concurrency_test.go), this
// helper accepts balance as a decimal to allow arbitrary test values.
func seedAccountForReconcile(t *testing.T, svc *ReconciliationService, accountNumber string, balance decimal.Decimal) {
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
		DailyLimit:       decimal.NewFromInt(1_000_000),
		MonthlyLimit:     decimal.NewFromInt(10_000_000),
		IsBankAccount:    false,
		ExpiresAt:        time.Now().AddDate(5, 0, 0),
		Version:          1,
	}
	require.NoError(t, svc.db.Create(acct).Error)
}

// seedLedgerEntry inserts a raw LedgerEntry row for testing reconciliation.
func seedLedgerEntry(t *testing.T, svc *ReconciliationService, accountNumber, entryType string, amount decimal.Decimal) {
	t.Helper()
	entry := &model.LedgerEntry{
		AccountNumber: accountNumber,
		EntryType:     entryType,
		Amount:        amount,
		BalanceBefore: decimal.Zero,
		BalanceAfter:  decimal.Zero,
		Description:   "test entry",
		ReferenceID:   "test-ref",
		ReferenceType: "test",
	}
	require.NoError(t, svc.db.Create(entry).Error)
}

// ---------------------------------------------------------------------------
// TestCheckAllBalances_Consistent
// ---------------------------------------------------------------------------

// TestCheckAllBalances_Consistent verifies that an account whose stored balance
// matches the ledger net (credits − debits) produces zero mismatches.
func TestCheckAllBalances_Consistent(t *testing.T) {
	svc, _ := newReconciliationService(t)
	ctx := context.Background()

	// balance=500, ledger: 700 credit − 200 debit = net 500 ✓
	seedAccountForReconcile(t, svc, "111000100000001011", decimal.NewFromInt(500))
	seedLedgerEntry(t, svc, "111000100000001011", "credit", decimal.NewFromInt(700))
	seedLedgerEntry(t, svc, "111000100000001011", "debit", decimal.NewFromInt(200))

	mismatches := svc.CheckAllBalances(ctx)
	assert.Equal(t, 0, mismatches)
}

// ---------------------------------------------------------------------------
// TestCheckAllBalances_Mismatch
// ---------------------------------------------------------------------------

// TestCheckAllBalances_Mismatch verifies that an account whose stored balance
// does not match the ledger net produces exactly 1 mismatch.
func TestCheckAllBalances_Mismatch(t *testing.T) {
	svc, _ := newReconciliationService(t)
	ctx := context.Background()

	// balance=999 (incorrect), ledger: 500 credit, 0 debit → net 500 ≠ 999
	seedAccountForReconcile(t, svc, "111000100000002011", decimal.NewFromInt(999))
	seedLedgerEntry(t, svc, "111000100000002011", "credit", decimal.NewFromInt(500))

	mismatches := svc.CheckAllBalances(ctx)
	assert.Equal(t, 1, mismatches)
}

// ---------------------------------------------------------------------------
// TestCheckAllBalances_NoAccounts
// ---------------------------------------------------------------------------

// TestCheckAllBalances_NoAccounts verifies that an empty database produces
// zero mismatches and does not panic.
func TestCheckAllBalances_NoAccounts(t *testing.T) {
	svc, _ := newReconciliationService(t)
	ctx := context.Background()

	mismatches := svc.CheckAllBalances(ctx)
	assert.Equal(t, 0, mismatches)
}

// ---------------------------------------------------------------------------
// TestCheckAllBalances_MultipleAccounts_MixedResults
// ---------------------------------------------------------------------------

// TestCheckAllBalances_MultipleAccounts_MixedResults verifies that when there
// are 3 accounts (1 consistent, 1 mismatched, 1 with no ledger entries and
// zero balance), exactly 1 mismatch is reported.
func TestCheckAllBalances_MultipleAccounts_MixedResults(t *testing.T) {
	svc, _ := newReconciliationService(t)
	ctx := context.Background()

	// Account A: balance=200, ledger net=200 → consistent
	seedAccountForReconcile(t, svc, "111000100000003011", decimal.NewFromInt(200))
	seedLedgerEntry(t, svc, "111000100000003011", "credit", decimal.NewFromInt(200))

	// Account B: balance=999, ledger net=500 → mismatch
	seedAccountForReconcile(t, svc, "111000100000004011", decimal.NewFromInt(999))
	seedLedgerEntry(t, svc, "111000100000004011", "credit", decimal.NewFromInt(500))

	// Account C: balance=0, no ledger entries → consistent (ledger net=0 == stored 0)
	seedAccountForReconcile(t, svc, "111000100000005011", decimal.Zero)

	mismatches := svc.CheckAllBalances(ctx)
	assert.Equal(t, 1, mismatches)
}
