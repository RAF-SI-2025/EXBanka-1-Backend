package repository

// Repository-level tests added to push account-service unit coverage toward
// >= 80%. These hit thin CRUD surfaces that previously had no direct
// coverage: AccountRepository, CompanyRepository, CurrencyRepository,
// ChangelogRepository, IncomingReservationRepository, LedgerRepository,
// and LedgerStore. SQLite is the only persistence — no Postgres-specific
// SQL is exercised (ILIKE filters skip the filter branches).

import (
	"context"
	"fmt"
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
// Account repository — direct CRUD coverage
// ---------------------------------------------------------------------------

func newAccount(num string, ownerID uint64, balance int64) *model.Account {
	bal := decimal.NewFromInt(balance)
	return &model.Account{
		AccountNumber:    num,
		OwnerID:          ownerID,
		AccountName:      "",
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          bal,
		AvailableBalance: bal,
		IsBankAccount:    false,
		ExpiresAt:        time.Now().AddDate(5, 0, 0),
		Version:          1,
	}
}

func TestAccountRepo_CreateGetAll(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000020011", 7, 1000)
	require.NoError(t, repo.Create(a))
	require.NotZero(t, a.ID)

	gotByID, err := repo.GetByID(a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.AccountNumber, gotByID.AccountNumber)

	gotByNum, err := repo.GetByNumber(a.AccountNumber)
	require.NoError(t, err)
	assert.Equal(t, a.ID, gotByNum.ID)

	_, err = repo.GetByID(99999)
	require.Error(t, err)

	_, err = repo.GetByNumber("missing")
	require.Error(t, err)
}

func TestAccountRepo_ListByClient(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(newAccount(fmt.Sprintf("11100010000010%04d", i), 42, 100)))
	}
	require.NoError(t, repo.Create(newAccount("111000100000111111", 99, 100)))

	got, total, err := repo.ListByClient(42, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)

	// Pagination
	got, _, err = repo.ListByClient(42, 1, 2)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestAccountRepo_ListAll_TypeFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a1 := newAccount("111000100000099021", 1, 0)
	a1.AccountType = "premium"
	require.NoError(t, repo.Create(a1))

	a2 := newAccount("111000100000099031", 1, 0)
	a2.AccountType = "standard"
	require.NoError(t, repo.Create(a2))

	got, total, err := repo.ListAll("", "", "premium", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)
	assert.Equal(t, "premium", got[0].AccountType)

	// No filter returns everything
	gotAll, totalAll, err := repo.ListAll("", "", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalAll)
	assert.Len(t, gotAll, 2)
}

func TestAccountRepo_ExistsByNameAndOwner(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099041", 1, 0)
	a.AccountName = "MyAccount"
	require.NoError(t, repo.Create(a))

	exists, err := repo.ExistsByNameAndOwner("MyAccount", 1, 0)
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = repo.ExistsByNameAndOwner("MyAccount", 1, a.ID) // exclude self
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = repo.ExistsByNameAndOwner("Other", 1, 0)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestAccountRepo_UpdateName(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099051", 1, 0)
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.UpdateName(a.ID, 1, "Renamed"))

	got, _ := repo.GetByID(a.ID)
	assert.Equal(t, "Renamed", got.AccountName)

	// Wrong owner — no rows affected → ErrRecordNotFound.
	err := repo.UpdateName(a.ID, 999, "X")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAccountRepo_UpdateLimits(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099061", 1, 0)
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.UpdateLimits(a.ID, map[string]interface{}{
		"daily_limit":   decimal.NewFromInt(50000),
		"monthly_limit": decimal.NewFromInt(200000),
	}))

	got, _ := repo.GetByID(a.ID)
	assert.True(t, got.DailyLimit.Equal(decimal.NewFromInt(50000)))
	assert.True(t, got.MonthlyLimit.Equal(decimal.NewFromInt(200000)))

	err := repo.UpdateLimits(99999, map[string]interface{}{"daily_limit": decimal.Zero})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAccountRepo_UpdateStatus(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099071", 1, 0)
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.UpdateStatus(a.ID, "inactive"))
	got, _ := repo.GetByID(a.ID)
	assert.Equal(t, "inactive", got.Status)

	err := repo.UpdateStatus(99999, "active")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAccountRepo_ListBankAccounts(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	bank := newAccount("111000100000099081", 1_000_000_000, 0)
	bank.IsBankAccount = true
	require.NoError(t, repo.Create(bank))

	bank2 := newAccount("111000100000099091", 1_000_000_000, 0)
	bank2.IsBankAccount = true
	bank2.CurrencyCode = "EUR"
	bank2.AccountKind = "foreign"
	require.NoError(t, repo.Create(bank2))

	regular := newAccount("111000100000099101", 1, 0)
	require.NoError(t, repo.Create(regular))

	all, err := repo.ListBankAccounts()
	require.NoError(t, err)
	assert.Len(t, all, 2)

	rsd, err := repo.ListBankAccountsByCurrency("RSD")
	require.NoError(t, err)
	assert.Len(t, rsd, 1)
	assert.Equal(t, "RSD", rsd[0].CurrencyCode)

	none, err := repo.ListBankAccountsByCurrency("JPY")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestAccountRepo_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099111", 1, 0)
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.SoftDelete(a.ID))

	_, err := repo.GetByID(a.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// SoftDelete on missing returns ErrRecordNotFound.
	err = repo.SoftDelete(99999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAccountRepo_ResetSpending(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099121", 1, 1000)
	require.NoError(t, repo.Create(a))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", a.ID).Updates(map[string]interface{}{
		"daily_spending":   decimal.NewFromInt(75),
		"monthly_spending": decimal.NewFromInt(200),
	}).Error)

	require.NoError(t, repo.ResetDailySpending())
	got, _ := repo.GetByID(a.ID)
	assert.True(t, got.DailySpending.IsZero())

	require.NoError(t, repo.ResetMonthlySpending())
	got, _ = repo.GetByID(a.ID)
	assert.True(t, got.MonthlySpending.IsZero())
}

func TestAccountRepo_ListActiveAccountsWithMaintenanceFee(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099131", 1, 0)
	require.NoError(t, repo.Create(a))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).
		Where("id = ?", a.ID).Update("maintenance_fee", decimal.NewFromInt(220)).Error)

	// inactive — should not appear
	b := newAccount("111000100000099141", 2, 0)
	b.Status = "inactive"
	require.NoError(t, repo.Create(b))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).
		Where("id = ?", b.ID).Update("maintenance_fee", decimal.NewFromInt(220)).Error)

	// bank account — should not appear
	bank := newAccount("111000100000099151", 1_000_000_000, 0)
	bank.IsBankAccount = true
	require.NoError(t, repo.Create(bank))

	got, err := repo.ListActiveAccountsWithMaintenanceFee()
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, a.ID, got[0].ID)
}

func TestAccountRepo_UpdateSpending(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099161", 1, 1000)
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.UpdateSpending(a.AccountNumber, decimal.NewFromInt(50)))
	got, _ := repo.GetByID(a.ID)
	assert.True(t, got.DailySpending.Equal(decimal.NewFromInt(50)))
	assert.True(t, got.MonthlySpending.Equal(decimal.NewFromInt(50)))

	// Bank accounts skip spending updates.
	bank := newAccount("111000100000099171", 1_000_000_000, 1000)
	bank.IsBankAccount = true
	require.NoError(t, repo.Create(bank))
	require.NoError(t, repo.UpdateSpending(bank.AccountNumber, decimal.NewFromInt(50)))
	gotBank, _ := repo.GetByID(bank.ID)
	assert.True(t, gotBank.DailySpending.IsZero(), "bank spending must remain 0")
}

// UpdateBalance edge cases not covered by service tests.
func TestAccountRepo_UpdateBalance_DailyLimitExceeded(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099181", 1, 10_000)
	a.DailyLimit = decimal.NewFromInt(100)
	require.NoError(t, repo.Create(a))

	_, err := repo.UpdateBalance(a.AccountNumber, decimal.NewFromInt(-200), true, UpdateBalanceOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daily spending limit exceeded")
}

func TestAccountRepo_UpdateBalance_MonthlyLimitExceeded(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099191", 1, 10_000)
	a.DailyLimit = decimal.NewFromInt(1_000_000)
	a.MonthlyLimit = decimal.NewFromInt(50)
	require.NoError(t, repo.Create(a))

	_, err := repo.UpdateBalance(a.AccountNumber, decimal.NewFromInt(-100), true, UpdateBalanceOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monthly spending limit exceeded")
}

func TestAccountRepo_UpdateBalance_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	_, err := repo.UpdateBalance("missing-account", decimal.NewFromInt(100), true, UpdateBalanceOpts{})
	require.Error(t, err)
}

func TestAccountRepo_UpdateBalance_IdempotencyReplay(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountRepository(db)

	a := newAccount("111000100000099201", 1, 1000)
	require.NoError(t, repo.Create(a))

	// First call writes ledger entry.
	entry1, err := repo.UpdateBalance(a.AccountNumber, decimal.NewFromInt(100), true,
		UpdateBalanceOpts{Memo: "first", IdempotencyKey: "ub-replay-1"})
	require.NoError(t, err)
	require.NotNil(t, entry1)

	// Second call returns cached entry without mutating account.
	entry2, err := repo.UpdateBalance(a.AccountNumber, decimal.NewFromInt(500), true,
		UpdateBalanceOpts{Memo: "second", IdempotencyKey: "ub-replay-1"})
	require.NoError(t, err)
	assert.Equal(t, entry1.ID, entry2.ID)

	got, _ := repo.GetByID(a.ID)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(1100)), "balance must reflect only first call")
}

// ---------------------------------------------------------------------------
// Company repository
// ---------------------------------------------------------------------------

func TestCompanyRepo_CreateGetUpdate(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Company{}))
	repo := NewCompanyRepository(db)

	c := &model.Company{
		CompanyName: "Acme", RegistrationNumber: "12345678", TaxNumber: "123456789",
		ActivityCode: "62.01", Address: "Belgrade", OwnerID: 5,
	}
	require.NoError(t, repo.Create(c))
	require.NotZero(t, c.ID)

	got, err := repo.GetByID(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme", got.CompanyName)

	got2, err := repo.GetByOwnerID(5)
	require.NoError(t, err)
	assert.Equal(t, c.ID, got2.ID)

	got.CompanyName = "Acme Renamed"
	require.NoError(t, repo.Update(got))

	again, err := repo.GetByID(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme Renamed", again.CompanyName)
}

func TestCompanyRepo_GetMissing(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Company{}))
	repo := NewCompanyRepository(db)

	_, err := repo.GetByID(9999)
	require.Error(t, err)

	_, err = repo.GetByOwnerID(9999)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Currency repository
// ---------------------------------------------------------------------------

func TestCurrencyRepo_ListAndGetByCode(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Currency{}))
	repo := NewCurrencyRepository(db)

	require.NoError(t, db.Create(&model.Currency{
		Code: "RSD", Name: "Serbian Dinar", Symbol: "din", Active: true,
	}).Error)
	require.NoError(t, db.Create(&model.Currency{
		Code: "EUR", Name: "Euro", Symbol: "€", Active: true,
	}).Error)
	// Inactive — must be filtered from List.
	require.NoError(t, db.Exec(
		`INSERT INTO currencies (id, name, code, symbol, country, description, active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		3, "Hidden", "ZZZ", "?", "", "", false,
	).Error)

	listed, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, listed, 2)

	got, err := repo.GetByCode("EUR")
	require.NoError(t, err)
	assert.Equal(t, "Euro", got.Name)

	_, err = repo.GetByCode("XXX")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Changelog repository
// ---------------------------------------------------------------------------

// changelogTestDB returns a sqlite-friendly schema for changelogs.
func changelogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by INTEGER NOT NULL,
		changed_at DATETIME NOT NULL,
		reason TEXT
	)`).Error)
	return db
}

func TestChangelogRepo_CreateAndList(t *testing.T) {
	db := changelogTestDB(t)
	repo := NewChangelogRepository(db)

	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "account", EntityID: 1, Action: "create",
		FieldName: "", OldValue: "", NewValue: "{}",
		ChangedBy: 5, ChangedAt: time.Now(), Reason: "init",
	}))

	require.NoError(t, repo.CreateBatch([]changelog.Entry{
		{EntityType: "account", EntityID: 1, Action: "update", FieldName: "name", OldValue: "a", NewValue: "b", ChangedAt: time.Now()},
		{EntityType: "account", EntityID: 1, Action: "update", FieldName: "name", OldValue: "b", NewValue: "c", ChangedAt: time.Now()},
	}))

	got, total, err := repo.ListByEntity("account", 1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)

	// CreateBatch with empty list → no-op.
	require.NoError(t, repo.CreateBatch(nil))
	require.NoError(t, repo.CreateBatch([]changelog.Entry{}))
}

// ---------------------------------------------------------------------------
// IncomingReservation repository
// ---------------------------------------------------------------------------

func TestIncomingReservationRepo_CreateGetMark(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.IncomingReservation{}))
	repo := NewIncomingReservationRepository(db)

	res := &model.IncomingReservation{
		AccountNumber:  "111000100000099111",
		Amount:         decimal.NewFromInt(100),
		Currency:       "RSD",
		ReservationKey: "irk-1",
		Status:         model.IncomingReservationStatusPending,
	}
	require.NoError(t, repo.Create(res))

	got, err := repo.GetByKey("irk-1")
	require.NoError(t, err)
	assert.Equal(t, res.ID, got.ID)

	require.NoError(t, repo.MarkCommitted("irk-1"))
	got2, _ := repo.GetByKey("irk-1")
	assert.Equal(t, model.IncomingReservationStatusCommitted, got2.Status)

	// MarkReleased on committed row is a no-op (RowsAffected=0, no error).
	require.NoError(t, repo.MarkReleased("irk-1"))
	got3, _ := repo.GetByKey("irk-1")
	assert.Equal(t, model.IncomingReservationStatusCommitted, got3.Status)

	// Missing key → ErrRecordNotFound.
	_, err = repo.GetByKey("missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestIncomingReservationRepo_MarkReleased_PendingRow(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.IncomingReservation{}))
	repo := NewIncomingReservationRepository(db)

	require.NoError(t, repo.Create(&model.IncomingReservation{
		AccountNumber: "111000100000099122", Amount: decimal.NewFromInt(50),
		Currency: "RSD", ReservationKey: "irk-rel", Status: model.IncomingReservationStatusPending,
	}))

	require.NoError(t, repo.MarkReleased("irk-rel"))
	got, _ := repo.GetByKey("irk-rel")
	assert.Equal(t, model.IncomingReservationStatusReleased, got.Status)
}

func TestIncomingReservationRepo_WithTx(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.IncomingReservation{}))
	repo := NewIncomingReservationRepository(db)

	err := db.Transaction(func(tx *gorm.DB) error {
		txRepo := repo.WithTx(tx)
		return txRepo.Create(&model.IncomingReservation{
			AccountNumber: "111000100000099133", Amount: decimal.NewFromInt(20),
			Currency: "RSD", ReservationKey: "irk-tx", Status: model.IncomingReservationStatusPending,
		})
	})
	require.NoError(t, err)
	got, _ := repo.GetByKey("irk-tx")
	require.NotNil(t, got)
}

func TestIncomingReservationRepo_ListStaleByCreatedBefore(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.IncomingReservation{}))
	repo := NewIncomingReservationRepository(db)

	require.NoError(t, repo.Create(&model.IncomingReservation{
		AccountNumber: "111000100000099144", Amount: decimal.NewFromInt(10),
		Currency: "RSD", ReservationKey: "irk-stale", Status: model.IncomingReservationStatusPending,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}))
	// Recent — must be excluded.
	require.NoError(t, repo.Create(&model.IncomingReservation{
		AccountNumber: "111000100000099155", Amount: decimal.NewFromInt(10),
		Currency: "RSD", ReservationKey: "irk-fresh", Status: model.IncomingReservationStatusPending,
		CreatedAt: time.Now(),
	}))

	got, err := repo.ListStaleByCreatedBefore(time.Now().Add(-1*time.Hour), 10)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "irk-stale", got[0].ReservationKey)
}

// ---------------------------------------------------------------------------
// Ledger repository — RecordEntry + saga-step paths covered separately.
// ---------------------------------------------------------------------------

func TestLedgerRepo_RecordEntry(t *testing.T) {
	db := newTestDB(t)
	repo := NewLedgerRepository(db)

	entry := &model.LedgerEntry{
		AccountNumber: "111000100000099166",
		EntryType:     "credit",
		Amount:        decimal.NewFromInt(123),
		BalanceBefore: decimal.NewFromInt(0),
		BalanceAfter:  decimal.NewFromInt(123),
		Description:   "test",
	}
	require.NoError(t, repo.RecordEntry(entry))
	require.NotZero(t, entry.ID)
}

// ---------------------------------------------------------------------------
// Reservation repository — UpdateStatus + GetByOrderIDForUpdate
// ---------------------------------------------------------------------------

func TestReservationRepo_UpdateStatus(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 8001, Amount: decimal.NewFromInt(100),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	require.NoError(t, repo.Create(r))

	r.Status = model.ReservationStatusReleased
	require.NoError(t, repo.UpdateStatus(r))

	got, _ := repo.GetByOrderID(8001)
	assert.Equal(t, model.ReservationStatusReleased, got.Status)
}

func TestReservationRepo_GetByOrderIDForUpdate(t *testing.T) {
	db := newTestDB(t)
	repo := NewAccountReservationRepository(db)

	r := &model.AccountReservation{
		AccountID: 1, OrderID: 8101, Amount: decimal.NewFromInt(100),
		CurrencyCode: "RSD", Status: model.ReservationStatusActive,
	}
	require.NoError(t, repo.Create(r))

	err := db.Transaction(func(tx *gorm.DB) error {
		got, err := repo.WithTx(tx).GetByOrderIDForUpdate(8101)
		if err != nil {
			return err
		}
		assert.Equal(t, r.ID, got.ID)
		return nil
	})
	require.NoError(t, err)

	// Missing returns ErrRecordNotFound.
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := repo.WithTx(tx).GetByOrderIDForUpdate(99999)
		return err
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// ---------------------------------------------------------------------------
// Ledger store — adapter to shared.Store interface
// ---------------------------------------------------------------------------

func TestLedgerStore_RecordEntryAndGetByKey(t *testing.T) {
	db := newTestDB(t)
	store := NewLedgerStore(db)
	ctx := context.Background()

	entry := &shared.Entry{
		Subject:        "111000100000099177",
		Type:           shared.EntryCredit,
		Amount:         decimal.NewFromInt(100),
		BalanceBefore:  decimal.NewFromInt(0),
		BalanceAfter:   decimal.NewFromInt(100),
		Description:    "test",
		IdempotencyKey: "ls-rec-1",
	}
	require.NoError(t, store.RecordEntry(ctx, entry))
	require.NotZero(t, entry.ID)

	got, err := store.GetByIdempotencyKey(ctx, "ls-rec-1")
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
	assert.Equal(t, "111000100000099177", got.Subject)

	// Empty key returns ErrNotFound without DB hit.
	_, err = store.GetByIdempotencyKey(ctx, "")
	require.ErrorIs(t, err, shared.ErrNotFound)

	// Missing key returns ErrNotFound.
	_, err = store.GetByIdempotencyKey(ctx, "no-such-key")
	require.ErrorIs(t, err, shared.ErrNotFound)
}

func TestLedgerStore_RecordEntry_DuplicateKey(t *testing.T) {
	db := newTestDB(t)
	store := NewLedgerStore(db)
	ctx := context.Background()

	first := &shared.Entry{
		Subject:        "111000100000099188",
		Type:           shared.EntryCredit,
		Amount:         decimal.NewFromInt(100),
		BalanceBefore:  decimal.NewFromInt(0),
		BalanceAfter:   decimal.NewFromInt(100),
		IdempotencyKey: "dup-key",
	}
	require.NoError(t, store.RecordEntry(ctx, first))

	// Replay with same idempotency key returns ErrDuplicateEntry.
	dup := &shared.Entry{
		Subject:        "111000100000099188",
		Type:           shared.EntryCredit,
		Amount:         decimal.NewFromInt(100),
		BalanceBefore:  decimal.NewFromInt(0),
		BalanceAfter:   decimal.NewFromInt(100),
		IdempotencyKey: "dup-key",
	}
	err := store.RecordEntry(ctx, dup)
	require.ErrorIs(t, err, shared.ErrDuplicateEntry)
}

func TestLedgerStore_RecordMovement(t *testing.T) {
	db := newTestDB(t)
	store := NewLedgerStore(db)
	ctx := context.Background()

	mv := &shared.Movement{
		Debit: shared.Entry{
			Subject:        "111000100000099199",
			Type:           shared.EntryDebit,
			Amount:         decimal.NewFromInt(50),
			BalanceBefore:  decimal.NewFromInt(100),
			BalanceAfter:   decimal.NewFromInt(50),
			IdempotencyKey: "mv-debit-1",
		},
		Credit: shared.Entry{
			Subject:        "111000100000099210",
			Type:           shared.EntryCredit,
			Amount:         decimal.NewFromInt(50),
			BalanceBefore:  decimal.NewFromInt(0),
			BalanceAfter:   decimal.NewFromInt(50),
			IdempotencyKey: "mv-credit-1",
		},
	}
	require.NoError(t, store.RecordMovement(ctx, mv))
	require.NotZero(t, mv.Debit.ID)
	require.NotZero(t, mv.Credit.ID)
}

func TestLedgerStore_ListBySubject(t *testing.T) {
	db := newTestDB(t)
	store := NewLedgerStore(db)
	ctx := context.Background()

	subject := "111000100000099221"
	for i := 0; i < 3; i++ {
		require.NoError(t, store.RecordEntry(ctx, &shared.Entry{
			Subject: subject, Type: shared.EntryCredit,
			Amount: decimal.NewFromInt(int64(i + 1)), BalanceBefore: decimal.Zero, BalanceAfter: decimal.NewFromInt(int64(i + 1)),
			IdempotencyKey: fmt.Sprintf("ls-list-%d", i),
		}))
	}

	got, total, err := store.ListBySubject(ctx, subject, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)

	// Default pagination
	gotDef, _, err := store.ListBySubject(ctx, subject, 0, 0)
	require.NoError(t, err)
	assert.Len(t, gotDef, 3)
}

func TestLedgerStore_ListByReference(t *testing.T) {
	db := newTestDB(t)
	store := NewLedgerStore(db)
	ctx := context.Background()

	ref := "ref-X"
	for i := 0; i < 2; i++ {
		require.NoError(t, store.RecordEntry(ctx, &shared.Entry{
			Subject: "111000100000099232", Type: shared.EntryCredit,
			Amount: decimal.NewFromInt(int64(i + 1)), BalanceBefore: decimal.Zero, BalanceAfter: decimal.NewFromInt(int64(i + 1)),
			ReferenceID: ref, ReferenceType: "trade",
			IdempotencyKey: fmt.Sprintf("ls-ref-%d", i),
		}))
	}

	got, err := store.ListByReference(ctx, "trade", ref)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// ---------------------------------------------------------------------------
// LedgerRepository.GetEntriesByAccount
// ---------------------------------------------------------------------------

func TestLedgerRepo_GetEntriesByAccount(t *testing.T) {
	db := newTestDB(t)
	repo := NewLedgerRepository(db)

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.RecordEntry(&model.LedgerEntry{
			AccountNumber: "111000100000099243",
			EntryType:     "credit",
			Amount:        decimal.NewFromInt(int64(i + 1)),
			BalanceBefore: decimal.Zero,
			BalanceAfter:  decimal.NewFromInt(int64(i + 1)),
			Description:   fmt.Sprintf("entry-%d", i),
		}))
	}

	got, total, err := repo.GetEntriesByAccount("111000100000099243", 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, got, 3)
}
