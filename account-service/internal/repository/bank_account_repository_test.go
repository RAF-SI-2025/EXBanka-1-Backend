package repository

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/account-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedBankSentinelAccount(t *testing.T, db *gorm.DB, currency, balance string) *model.Account {
	t.Helper()
	bal := decimal.RequireFromString(balance)
	acct := &model.Account{
		AccountNumber:    "BANK-" + currency + "-0001",
		OwnerID:          1_000_000_000,
		CurrencyCode:     currency,
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          bal,
		AvailableBalance: bal,
		IsBankAccount:    true,
		ExpiresAt:        time.Now().AddDate(10, 0, 0),
		Version:          1,
	}
	require.NoError(t, db.Create(acct).Error)
	return acct
}

func TestDebitBankAccount_Success(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankSentinelAccount(t, db, "RSD", "1000000")

	resp, err := repo.DebitBank(context.Background(), "RSD", decimal.RequireFromString("50000"), "loan-disbursement:1", "loan 1 disbursement")
	require.NoError(t, err)
	require.False(t, resp.Replayed)
	require.Equal(t, "950000.0000", resp.NewBalance)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "950000.0000", acct.Balance.StringFixed(4))
}

func TestDebitBankAccount_InsufficientBalance(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankSentinelAccount(t, db, "RSD", "100")

	_, err := repo.DebitBank(context.Background(), "RSD", decimal.RequireFromString("5000"), "loan-disbursement:2", "loan 2")
	require.ErrorIs(t, err, ErrInsufficientBankLiquidity)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "100.0000", acct.Balance.StringFixed(4), "balance must be unchanged on failed debit")
}

func TestDebitBankAccount_IdempotentReplay(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankSentinelAccount(t, db, "RSD", "1000000")

	first, err := repo.DebitBank(context.Background(), "RSD", decimal.RequireFromString("50000"), "loan-disbursement:3", "loan 3")
	require.NoError(t, err)
	require.False(t, first.Replayed)

	second, err := repo.DebitBank(context.Background(), "RSD", decimal.RequireFromString("50000"), "loan-disbursement:3", "loan 3")
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, first.NewBalance, second.NewBalance)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "950000.0000", acct.Balance.StringFixed(4), "replay must not double-debit")
}

func TestCreditBankAccount_Success(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankSentinelAccount(t, db, "RSD", "1000000")

	resp, err := repo.CreditBank(context.Background(), "RSD", decimal.RequireFromString("250"), "fee:9", "fee collection")
	require.NoError(t, err)
	require.False(t, resp.Replayed)
	require.Equal(t, "1000250.0000", resp.NewBalance)
}

func TestCreditBankAccount_IdempotentReplay(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	seedBankSentinelAccount(t, db, "RSD", "1000000")

	_, err := repo.CreditBank(context.Background(), "RSD", decimal.RequireFromString("250"), "fee:10", "fee collection")
	require.NoError(t, err)

	replay, err := repo.CreditBank(context.Background(), "RSD", decimal.RequireFromString("250"), "fee:10", "fee collection")
	require.NoError(t, err)
	require.True(t, replay.Replayed)

	var acct model.Account
	require.NoError(t, db.Where("account_number = ?", "BANK-RSD-0001").First(&acct).Error)
	require.Equal(t, "1000250.0000", acct.Balance.StringFixed(4), "replay must not double-credit")
}

func TestDebitBankAccount_CurrencyNotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewBankAccountRepository(db)
	// No seed — no bank account for EUR.

	_, err := repo.DebitBank(context.Background(), "EUR", decimal.RequireFromString("100"), "loan-disbursement:99", "loan 99")
	require.ErrorIs(t, err, ErrBankAccountNotFound)
}
