package repository

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/shared/saga"
)

// seedAcctForLedger inserts a non-bank account with the given balance, ready
// to be debited or credited by the ledger repo. Returns the account number.
func seedAcctForLedger(t *testing.T, db *gorm.DB, balance string) string {
	t.Helper()
	bal := decimal.RequireFromString(balance)
	acct := &model.Account{
		AccountNumber:    "265-9999-99",
		OwnerID:          42,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          bal,
		AvailableBalance: bal,
		IsBankAccount:    false,
		ExpiresAt:        time.Now().AddDate(1, 0, 0),
		Version:          1,
	}
	require.NoError(t, db.Create(acct).Error)
	return acct.AccountNumber
}

// TestDebitWithLock_StampsSagaContext verifies that a saga-driven debit
// (ctx carries saga_id + saga_step) writes those values onto the new
// ledger row so cross-service audit queries can find every row a saga
// touched.
func TestDebitWithLock_StampsSagaContext(t *testing.T) {
	db := newTestDB(t)
	repo := NewLedgerRepository(db)
	acctNum := seedAcctForLedger(t, db, "1000")

	ctx := saga.WithSagaID(context.Background(), "saga-stamp-debit")
	ctx = saga.WithSagaStep(ctx, saga.StepDebitBuyer)

	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := repo.DebitWithLock(ctx, tx, acctNum, decimal.RequireFromString("100"), "memo", "ref-1", "trade")
		return e
	})
	require.NoError(t, err)

	var entry model.LedgerEntry
	require.NoError(t, db.First(&entry).Error)
	require.NotNil(t, entry.SagaID, "saga_id should be stamped")
	require.Equal(t, "saga-stamp-debit", *entry.SagaID)
	require.NotNil(t, entry.SagaStep, "saga_step should be stamped")
	require.Equal(t, string(saga.StepDebitBuyer), *entry.SagaStep)
}

// TestCreditWithLock_StampsSagaContext mirrors the debit test for credit.
func TestCreditWithLock_StampsSagaContext(t *testing.T) {
	db := newTestDB(t)
	repo := NewLedgerRepository(db)
	acctNum := seedAcctForLedger(t, db, "1000")

	ctx := saga.WithSagaID(context.Background(), "saga-stamp-credit")
	ctx = saga.WithSagaStep(ctx, saga.StepCreditSeller)

	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := repo.CreditWithLock(ctx, tx, acctNum, decimal.RequireFromString("50"), "memo", "ref-2", "trade")
		return e
	})
	require.NoError(t, err)

	var entry model.LedgerEntry
	require.NoError(t, db.First(&entry).Error)
	require.NotNil(t, entry.SagaID)
	require.Equal(t, "saga-stamp-credit", *entry.SagaID)
	require.NotNil(t, entry.SagaStep)
	require.Equal(t, string(saga.StepCreditSeller), *entry.SagaStep)
}

// TestDebitWithLock_NoSagaContext_LeavesNull verifies that non-saga callers
// (REST handlers, crons) leave saga_id / saga_step as NULL — the columns
// must not default to "" for non-saga rows so cross-service audit reports
// don't false-positive on every row.
func TestDebitWithLock_NoSagaContext_LeavesNull(t *testing.T) {
	db := newTestDB(t)
	repo := NewLedgerRepository(db)
	acctNum := seedAcctForLedger(t, db, "1000")

	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := repo.DebitWithLock(context.Background(), tx, acctNum, decimal.RequireFromString("10"), "no-saga", "ref-3", "trade")
		return e
	})
	require.NoError(t, err)

	var entry model.LedgerEntry
	require.NoError(t, db.First(&entry).Error)
	require.Nil(t, entry.SagaID, "expected saga_id to be NULL for non-saga write")
	require.Nil(t, entry.SagaStep, "expected saga_step to be NULL for non-saga write")
}
