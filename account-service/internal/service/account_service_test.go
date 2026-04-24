package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

// newAccountService creates an AccountService backed by an in-memory SQLite DB.
func newAccountService(t *testing.T) *AccountService {
	t.Helper()
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	return NewAccountService(repo, db, nil)
}

// ---------------------------------------------------------------------------
// CreateAccount
// ---------------------------------------------------------------------------

func TestCreateAccount_CurrentRSD(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	err := svc.CreateAccount(acct)
	require.NoError(t, err)

	assert.Len(t, acct.AccountNumber, 18, "account number must be 18 digits")
	assert.Equal(t, "11", acct.AccountNumber[16:18], "type code for current = 11")
	assert.Equal(t, "active", acct.Status)
	assert.False(t, acct.ExpiresAt.IsZero(), "expires_at must be set")
}

func TestCreateAccount_ForeignEUR(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "EUR",
		AccountKind:  "foreign",
		AccountType:  "standard",
	}
	err := svc.CreateAccount(acct)
	require.NoError(t, err)

	assert.Len(t, acct.AccountNumber, 18)
	assert.Equal(t, "21", acct.AccountNumber[16:18], "type code for foreign = 21")
	assert.Equal(t, "active", acct.Status)
}

func TestCreateAccount_WithInitialBalance(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:          42,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Balance:          decimal.NewFromInt(5000),
		AvailableBalance: decimal.NewFromInt(5000),
	}
	err := svc.CreateAccount(acct)
	require.NoError(t, err)

	// Retrieve from DB to confirm persistence.
	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(5000)), "balance persisted")
	assert.True(t, got.AvailableBalance.Equal(decimal.NewFromInt(5000)), "available balance persisted")
}

func TestCreateAccount_MissingOwnerID(t *testing.T) {
	svc := newAccountService(t)
	err := svc.CreateAccount(&model.Account{
		CurrencyCode: "RSD",
		AccountKind:  "current",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "owner_id")
}

func TestCreateAccount_MissingCurrencyCode(t *testing.T) {
	svc := newAccountService(t)
	err := svc.CreateAccount(&model.Account{
		OwnerID:     42,
		AccountKind: "current",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "currency_code")
}

func TestCreateAccount_InvalidKind(t *testing.T) {
	svc := newAccountService(t)
	err := svc.CreateAccount(&model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "savings",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "account kind")
}

func TestCreateAccount_CurrentWithNonRSD(t *testing.T) {
	svc := newAccountService(t)
	err := svc.CreateAccount(&model.Account{
		OwnerID:      42,
		CurrencyCode: "EUR",
		AccountKind:  "current",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "current accounts can only use RSD")
}

func TestCreateAccount_ForeignWithRSD(t *testing.T) {
	svc := newAccountService(t)
	err := svc.CreateAccount(&model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "foreign",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "foreign accounts cannot use RSD")
}

func TestCreateAccount_DuplicateNameForSameOwner(t *testing.T) {
	svc := newAccountService(t)

	first := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountName:  "Main",
	}
	require.NoError(t, svc.CreateAccount(first))

	second := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountName:  "Main",
	}
	err := svc.CreateAccount(second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCreateAccount_MaintenanceFeeByType(t *testing.T) {
	tests := []struct {
		accountType string
		expectedFee int64
	}{
		{"premium", 500},
		{"student", 0},
		{"youth", 0},
		{"pension", 100},
		{"standard", 220},
	}
	for _, tt := range tests {
		t.Run(tt.accountType, func(t *testing.T) {
			svc := newAccountService(t)
			acct := &model.Account{
				OwnerID:      42,
				CurrencyCode: "RSD",
				AccountKind:  "current",
				AccountType:  tt.accountType,
			}
			require.NoError(t, svc.CreateAccount(acct))
			assert.True(t, acct.MaintenanceFee.Equal(decimal.NewFromInt(tt.expectedFee)),
				"expected fee %d for type %s", tt.expectedFee, tt.accountType)
		})
	}
}

// ---------------------------------------------------------------------------
// GetAccount / GetAccountByNumber
// ---------------------------------------------------------------------------

func TestGetAccount_Found(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      1,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.Equal(t, acct.ID, got.ID)
	assert.Equal(t, acct.AccountNumber, got.AccountNumber)
}

func TestGetAccount_NotFound(t *testing.T) {
	svc := newAccountService(t)
	_, err := svc.GetAccount(99999)
	assert.Error(t, err)
}

func TestGetAccountByNumber(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      1,
		CurrencyCode: "EUR",
		AccountKind:  "foreign",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.Equal(t, acct.ID, got.ID)
}

// ---------------------------------------------------------------------------
// ListAccountsByClient (pagination)
// ---------------------------------------------------------------------------

func TestListAccountsByClient_Pagination(t *testing.T) {
	svc := newAccountService(t)

	// Create 5 accounts for client 10.
	for i := 0; i < 5; i++ {
		require.NoError(t, svc.CreateAccount(&model.Account{
			OwnerID:      10,
			CurrencyCode: "RSD",
			AccountKind:  "current",
			AccountType:  "standard",
		}))
	}

	// Page 1, size 2 → 2 results, total 5.
	accounts, total, err := svc.ListAccountsByClient(10, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, accounts, 2)

	// Page 3, size 2 → 1 result.
	accounts, total, err = svc.ListAccountsByClient(10, 3, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, accounts, 1)
}

func TestListAccountsByClient_DefaultPagination(t *testing.T) {
	svc := newAccountService(t)
	// page <= 0 and pageSize <= 0 should default to 1 and 10 respectively.
	accounts, total, err := svc.ListAccountsByClient(999, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, accounts)
}

// ---------------------------------------------------------------------------
// UpdateAccountName
// ---------------------------------------------------------------------------

func TestUpdateAccountName_Success(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
		AccountName:  "Old Name",
	}
	require.NoError(t, svc.CreateAccount(acct))

	err := svc.UpdateAccountName(acct.ID, 42, "New Name", 0)
	require.NoError(t, err)

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.Equal(t, "New Name", got.AccountName)
}

func TestUpdateAccountName_DuplicateForSameClient(t *testing.T) {
	svc := newAccountService(t)

	first := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
		AccountName:  "Alpha",
	}
	require.NoError(t, svc.CreateAccount(first))

	second := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
		AccountName:  "Beta",
	}
	require.NoError(t, svc.CreateAccount(second))

	// Try to rename second to same name as first.
	err := svc.UpdateAccountName(second.ID, 42, "Alpha", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// ---------------------------------------------------------------------------
// UpdateAccountLimits
// ---------------------------------------------------------------------------

func TestUpdateAccountLimits_Success(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	daily := "50000"
	monthly := "200000"
	err := svc.UpdateAccountLimits(acct.ID, &daily, &monthly, 0)
	require.NoError(t, err)

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.True(t, got.DailyLimit.Equal(decimal.NewFromInt(50000)))
	assert.True(t, got.MonthlyLimit.Equal(decimal.NewFromInt(200000)))
}

func TestUpdateAccountLimits_InvalidValue(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	bad := "not-a-number"
	err := svc.UpdateAccountLimits(acct.ID, &bad, nil, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid daily_limit")
}

func TestUpdateAccountLimits_NegativeValue(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	negative := "-100"
	err := svc.UpdateAccountLimits(acct.ID, &negative, nil, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daily_limit must be greater than 0")
}

func TestUpdateAccountLimits_ZeroValue(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	zero := "0"
	err := svc.UpdateAccountLimits(acct.ID, nil, &zero, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "monthly_limit must be greater than 0")
}

func TestUpdateAccountLimits_NoUpdates(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	// Both nil → no-op, no error.
	err := svc.UpdateAccountLimits(acct.ID, nil, nil, 0)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// UpdateAccountStatus
// ---------------------------------------------------------------------------

func TestUpdateAccountStatus_ActiveToInactive(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))
	assert.Equal(t, "active", acct.Status)

	err := svc.UpdateAccountStatus(acct.ID, "inactive", 0)
	require.NoError(t, err)

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.Equal(t, "inactive", got.Status)
}

func TestUpdateAccountStatus_InactiveToActive(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	// First deactivate, then re-activate.
	require.NoError(t, svc.UpdateAccountStatus(acct.ID, "inactive", 0))
	err := svc.UpdateAccountStatus(acct.ID, "active", 0)
	require.NoError(t, err)

	got, err := svc.GetAccount(acct.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
}

func TestUpdateAccountStatus_InvalidStatus(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	err := svc.UpdateAccountStatus(acct.ID, "frozen", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "account status must be")
}

func TestUpdateAccountStatus_AlreadySameStatus(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID:      42,
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountType:  "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))

	err := svc.UpdateAccountStatus(acct.ID, "active", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already active")
}

func TestUpdateAccountStatus_NotFound(t *testing.T) {
	svc := newAccountService(t)
	err := svc.UpdateAccountStatus(99999, "inactive", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// UpdateBalance
// ---------------------------------------------------------------------------

func TestUpdateBalance_Credit(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000001011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	err := svc.UpdateBalance(acct.AccountNumber, decimal.NewFromInt(500), true)
	require.NoError(t, err)

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(1500)), "balance should be 1500 after +500 credit")
	assert.True(t, got.AvailableBalance.Equal(decimal.NewFromInt(1500)), "available balance should match")
}

func TestUpdateBalance_Debit(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000002011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	err := svc.UpdateBalance(acct.AccountNumber, decimal.NewFromInt(-300), true)
	require.NoError(t, err)

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(700)), "balance should be 700 after -300 debit")
	assert.True(t, got.DailySpending.Equal(decimal.NewFromInt(300)), "daily spending tracks debit")
	assert.True(t, got.MonthlySpending.Equal(decimal.NewFromInt(300)), "monthly spending tracks debit")
}

func TestUpdateBalance_InsufficientFunds(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	seedAccount(t, db, "111000100000003011", decimal.NewFromInt(100), decimal.NewFromInt(1_000_000))

	err := svc.UpdateBalance("111000100000003011", decimal.NewFromInt(-200), true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient funds")
}

func TestUpdateBalance_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	err := svc.UpdateBalance("000000000000000000", decimal.NewFromInt(100), true)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// UpdateBalance — memo + idempotency_key (bank-safety plumbing)
// ---------------------------------------------------------------------------

// A retry with the same idempotency_key must not mutate the account a second
// time — crash-retries of a credit/debit step are safe no-ops.
func TestUpdateBalance_WithIdempotencyKey_RetrySafe(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000010011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	opts := repository.UpdateBalanceOpts{Memo: "sell proceeds", IdempotencyKey: "sell-credit-42"}
	require.NoError(t, svc.UpdateBalanceWithOpts(acct.AccountNumber, decimal.NewFromInt(100), true, opts))

	// Replay with same key — even with different amount, the first call wins
	// and no further mutation happens.
	require.NoError(t, svc.UpdateBalanceWithOpts(acct.AccountNumber, decimal.NewFromInt(500), true, opts))

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(1100)), "retry must not double-credit: got %s want 1100", got.Balance)

	// Exactly one ledger entry with the key.
	var count int64
	require.NoError(t, db.Table("ledger_entries").Where("idempotency_key = ?", "sell-credit-42").Count(&count).Error)
	assert.Equal(t, int64(1), count, "exactly one ledger entry should exist for the key")
}

// Distinct idempotency_keys are treated as distinct operations — both land.
func TestUpdateBalance_DifferentKeys_NotDeduped(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000011011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.UpdateBalanceWithOpts(acct.AccountNumber, decimal.NewFromInt(100), true,
		repository.UpdateBalanceOpts{Memo: "first", IdempotencyKey: "key-a"}))
	require.NoError(t, svc.UpdateBalanceWithOpts(acct.AccountNumber, decimal.NewFromInt(100), true,
		repository.UpdateBalanceOpts{Memo: "second", IdempotencyKey: "key-b"}))

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(1200)), "different keys must credit cumulatively: got %s want 1200", got.Balance)
}

// Empty idempotency_key opts out of dedup — existing (pre-plumbing) callers
// that didn't pass a key keep the original "apply each call" behaviour.
func TestUpdateBalance_EmptyKey_NotDeduped(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000012011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.UpdateBalance(acct.AccountNumber, decimal.NewFromInt(100), true))
	require.NoError(t, svc.UpdateBalance(acct.AccountNumber, decimal.NewFromInt(100), true))

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(1200)), "empty-key calls must credit cumulatively: got %s want 1200", got.Balance)
}

// Memo persists to the ledger entry's description so transaction history
// shows what the balance change was for.
func TestUpdateBalance_Memo_PersistsToLedger(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000013011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.UpdateBalanceWithOpts(acct.AccountNumber, decimal.NewFromInt(250), true,
		repository.UpdateBalanceOpts{Memo: "Commission for order #17"}))

	ledgerRepo := repository.NewLedgerRepository(db)
	entries, _, err := ledgerRepo.GetEntriesByAccount(acct.AccountNumber, 1, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected 1 ledger entry")
	assert.Equal(t, "Commission for order #17", entries[0].Description)
	assert.Equal(t, "credit", entries[0].EntryType)
	assert.True(t, entries[0].Amount.Equal(decimal.NewFromInt(250)))
}

// Without memo/key neither a ledger entry is written (the historical no-
// ledger UpdateBalance behaviour) — this is what keeps transaction-service
// / payment-service, which write their own ledger entries, unaffected.
func TestUpdateBalance_NoOpts_NoLedgerEntry(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	acct := seedAccount(t, db, "111000100000014011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.UpdateBalance(acct.AccountNumber, decimal.NewFromInt(100), true))

	ledgerRepo := repository.NewLedgerRepository(db)
	entries, _, err := ledgerRepo.GetEntriesByAccount(acct.AccountNumber, 1, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 0, "no ledger entry should be written when memo and key are empty")
}

// ---------------------------------------------------------------------------
// CreateBankAccount / ListBankAccounts / DeleteBankAccount
// ---------------------------------------------------------------------------

func TestCreateBankAccount(t *testing.T) {
	svc := newAccountService(t)

	acct, err := svc.CreateBankAccount("RSD", "current", "Bank RSD Main", decimal.NewFromInt(1_000_000))
	require.NoError(t, err)
	assert.True(t, acct.IsBankAccount)
	assert.Equal(t, BankOwnerID, acct.OwnerID)
	assert.Equal(t, "EX Banka", acct.OwnerName)
	assert.True(t, acct.Balance.Equal(decimal.NewFromInt(1_000_000)))
	assert.Len(t, acct.AccountNumber, 18)
}

func TestCreateBankAccount_DuplicateName(t *testing.T) {
	svc := newAccountService(t)

	_, err := svc.CreateBankAccount("RSD", "current", "Bank Main", decimal.Zero)
	require.NoError(t, err)

	_, err = svc.CreateBankAccount("EUR", "foreign", "Bank Main", decimal.Zero)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestListBankAccounts(t *testing.T) {
	svc := newAccountService(t)

	_, err := svc.CreateBankAccount("RSD", "current", "RSD Account", decimal.Zero)
	require.NoError(t, err)
	_, err = svc.CreateBankAccount("EUR", "foreign", "EUR Account", decimal.Zero)
	require.NoError(t, err)

	accounts, err := svc.ListBankAccounts()
	require.NoError(t, err)
	assert.Len(t, accounts, 2)
}

func TestGetBankRSDAccount(t *testing.T) {
	svc := newAccountService(t)

	_, err := svc.CreateBankAccount("RSD", "current", "RSD Primary", decimal.NewFromInt(500))
	require.NoError(t, err)

	rsd, err := svc.GetBankRSDAccount()
	require.NoError(t, err)
	assert.Equal(t, "RSD", rsd.CurrencyCode)
	assert.True(t, rsd.IsBankAccount)
}

func TestGetBankRSDAccount_NoneExists(t *testing.T) {
	svc := newAccountService(t)
	_, err := svc.GetBankRSDAccount()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no bank RSD account")
}
