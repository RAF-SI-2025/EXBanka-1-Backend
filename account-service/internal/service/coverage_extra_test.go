package service

// Additional service-layer tests added to push account-service unit coverage
// toward >= 80 %. These focus on the previously uncovered code paths:
//
//   * AccountService.ListAllAccounts / DeleteBankAccount / UpdateSpending /
//     SetBankRepo / DebitBankAccount / CreditBankAccount
//   * AccountService.UpdateAccountStatus / Limits / Name happy-path edge cases
//     (validation, missing-account, invalid limit string, etc.)
//   * CompanyService.* and CurrencyService.* — thin repo passthroughs
//   * ChangelogService.ListChangelog — validation + happy path
//   * IncomingReservationService.{ReserveIncoming, CommitIncoming, ReleaseIncoming}
//   * LedgerService.{Transfer, Credit, Debit, GetLedgerEntries}
//
// All tests use the in-memory SQLite helper newTestDB defined in
// concurrency_test.go.

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
	"github.com/exbanka/contract/changelog"
)

// ---------------------------------------------------------------------------
// AccountService.ListAllAccounts
// ---------------------------------------------------------------------------

func TestListAllAccounts_DefaultPaginationApplied(t *testing.T) {
	svc := newAccountService(t)
	// Empty DB — defaults applied without error.
	accounts, total, err := svc.ListAllAccounts("", "", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, accounts)
}

func TestListAllAccounts_PaginationHonoured(t *testing.T) {
	svc := newAccountService(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.CreateAccount(&model.Account{
			OwnerID: uint64(100 + i), CurrencyCode: "RSD", AccountKind: "current", AccountType: "standard",
		}))
	}
	accounts, total, err := svc.ListAllAccounts("", "", "", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, accounts, 2)
}

// ---------------------------------------------------------------------------
// AccountService.DeleteBankAccount
// ---------------------------------------------------------------------------

func TestDeleteBankAccount_LastRSDRefused(t *testing.T) {
	svc := newAccountService(t)
	rsd, err := svc.CreateBankAccount("RSD", "current", "RSD Sole", decimal.Zero)
	require.NoError(t, err)

	err = svc.DeleteBankAccount(rsd.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLastBankAccount)
}

func TestDeleteBankAccount_LastForeignRefused(t *testing.T) {
	svc := newAccountService(t)
	_, err := svc.CreateBankAccount("RSD", "current", "RSD Bank", decimal.Zero)
	require.NoError(t, err)
	eur, err := svc.CreateBankAccount("EUR", "foreign", "EUR Sole", decimal.Zero)
	require.NoError(t, err)

	err = svc.DeleteBankAccount(eur.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLastBankAccount)
}

func TestDeleteBankAccount_AllowedWhenSecondaryExists(t *testing.T) {
	svc := newAccountService(t)
	_, err := svc.CreateBankAccount("RSD", "current", "RSD Primary", decimal.Zero)
	require.NoError(t, err)
	rsd2, err := svc.CreateBankAccount("RSD", "current", "RSD Secondary", decimal.Zero)
	require.NoError(t, err)
	_, err = svc.CreateBankAccount("EUR", "foreign", "EUR Primary", decimal.Zero)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteBankAccount(rsd2.ID))

	got, err := svc.ListBankAccounts()
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestDeleteBankAccount_NotFound(t *testing.T) {
	svc := newAccountService(t)
	// At least one RSD + one foreign so the precondition checks succeed
	// before NotFound surfaces.
	_, err := svc.CreateBankAccount("RSD", "current", "RSD Primary", decimal.Zero)
	require.NoError(t, err)
	_, err = svc.CreateBankAccount("EUR", "foreign", "EUR Primary", decimal.Zero)
	require.NoError(t, err)

	err = svc.DeleteBankAccount(99999)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// ---------------------------------------------------------------------------
// AccountService.UpdateSpending — direct passthrough for cron callers
// ---------------------------------------------------------------------------

func TestUpdateSpending_IncrementsClientAccount(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)
	acct := seedAccount(t, db, "111000100000900011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.UpdateSpending(acct.AccountNumber, decimal.NewFromInt(75)))

	got, err := svc.GetAccountByNumber(acct.AccountNumber)
	require.NoError(t, err)
	assert.True(t, got.DailySpending.Equal(decimal.NewFromInt(75)), "daily spending: got %s", got.DailySpending)
	assert.True(t, got.MonthlySpending.Equal(decimal.NewFromInt(75)), "monthly spending: got %s", got.MonthlySpending)
}

// ---------------------------------------------------------------------------
// AccountService.SetBankRepo / DebitBankAccount / CreditBankAccount
// ---------------------------------------------------------------------------

func TestSetBankRepo_ThenDebitCreditBankAccount(t *testing.T) {
	db := newTestDB(t)
	accountRepo := repository.NewAccountRepository(db)
	bankRepo := repository.NewBankAccountRepository(db)
	svc := NewAccountService(accountRepo, db, nil)
	svc.SetBankRepo(bankRepo)

	// Seed a bank sentinel account directly.
	bank := &model.Account{
		AccountNumber:    "BANK-EUR-0001",
		OwnerID:          BankOwnerID,
		CurrencyCode:     "EUR",
		AccountKind:      "foreign",
		AccountType:      "bank",
		Status:           "active",
		Balance:          decimal.NewFromInt(10000),
		AvailableBalance: decimal.NewFromInt(10000),
		IsBankAccount:    true,
		ExpiresAt:        time.Now().AddDate(10, 0, 0),
		Version:          1,
	}
	require.NoError(t, db.Create(bank).Error)

	ctx := context.Background()
	debit, err := svc.DebitBankAccount(ctx, "EUR", "1500", "ref-debit-1", "test debit")
	require.NoError(t, err)
	assert.Equal(t, "BANK-EUR-0001", debit.AccountNumber)
	assert.Equal(t, "8500.0000", debit.NewBalance)
	assert.False(t, debit.Replayed)

	// Replay returns Replayed=true.
	debit2, err := svc.DebitBankAccount(ctx, "EUR", "1500", "ref-debit-1", "test debit")
	require.NoError(t, err)
	assert.True(t, debit2.Replayed)

	credit, err := svc.CreditBankAccount(ctx, "EUR", "500", "ref-credit-1", "test credit")
	require.NoError(t, err)
	assert.Equal(t, "9000.0000", credit.NewBalance)
}

func TestDebitBankAccount_InvalidAmountStringFails(t *testing.T) {
	db := newTestDB(t)
	bankRepo := repository.NewBankAccountRepository(db)
	svc := NewAccountService(repository.NewAccountRepository(db), db, nil)
	svc.SetBankRepo(bankRepo)

	_, err := svc.DebitBankAccount(context.Background(), "EUR", "not-a-number", "ref", "reason")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid amount")
}

func TestCreditBankAccount_InvalidAmountStringFails(t *testing.T) {
	db := newTestDB(t)
	bankRepo := repository.NewBankAccountRepository(db)
	svc := NewAccountService(repository.NewAccountRepository(db), db, nil)
	svc.SetBankRepo(bankRepo)

	_, err := svc.CreditBankAccount(context.Background(), "EUR", "not-a-number", "ref", "reason")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid amount")
}

// ---------------------------------------------------------------------------
// AccountService — error edge cases for already-covered methods
// ---------------------------------------------------------------------------

func TestUpdateAccountName_AccountMissing(t *testing.T) {
	svc := newAccountService(t)
	err := svc.UpdateAccountName(99999, 1, "NewName", 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestUpdateAccountLimits_AccountMissing(t *testing.T) {
	svc := newAccountService(t)
	val := "100"
	err := svc.UpdateAccountLimits(99999, &val, nil, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

func TestUpdateAccountLimits_InvalidMonthlyValue(t *testing.T) {
	svc := newAccountService(t)
	acct := &model.Account{
		OwnerID: 7, CurrencyCode: "RSD", AccountKind: "current", AccountType: "standard",
	}
	require.NoError(t, svc.CreateAccount(acct))
	bad := "totally-bogus"
	err := svc.UpdateAccountLimits(acct.ID, nil, &bad, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid monthly_limit")
}

func TestCreateAccount_InvalidPersistenceError(t *testing.T) {
	// Trigger a duplicate account_number — covered by the unique index.
	svc := newAccountService(t)
	first := &model.Account{OwnerID: 7, CurrencyCode: "RSD", AccountKind: "current", AccountType: "standard"}
	require.NoError(t, svc.CreateAccount(first))
	// Creating an account with the same generated number is astronomically
	// unlikely, but force a duplicate via ExistsByNameAndOwner failure path
	// by reusing the same name on the same owner.
	dup := &model.Account{OwnerID: 7, CurrencyCode: "RSD", AccountKind: "current", AccountType: "standard", AccountName: "n1"}
	require.NoError(t, svc.CreateAccount(dup))
	again := &model.Account{OwnerID: 7, CurrencyCode: "RSD", AccountKind: "current", AccountType: "standard", AccountName: "n1"}
	require.Error(t, svc.CreateAccount(again))
}

// ---------------------------------------------------------------------------
// CompanyService — CRUD passthroughs
// ---------------------------------------------------------------------------

func newCompanyService(t *testing.T) *CompanyService {
	t.Helper()
	db := newTestDB(t)
	repo := repository.NewCompanyRepository(db)
	return NewCompanyService(repo)
}

func TestCompanyService_CreateGetUpdate(t *testing.T) {
	svc := newCompanyService(t)

	c := &model.Company{
		CompanyName: "Acme", RegistrationNumber: "12345678", TaxNumber: "123456789",
		ActivityCode: "62.01", Address: "Belgrade", OwnerID: 5,
	}
	require.NoError(t, svc.Create(c))
	require.NotZero(t, c.ID)

	got, err := svc.Get(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme", got.CompanyName)

	got.CompanyName = "Acme Renamed"
	require.NoError(t, svc.Update(got))
	again, err := svc.Get(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme Renamed", again.CompanyName)
}

func TestCompanyService_GetByOwnerID(t *testing.T) {
	svc := newCompanyService(t)
	require.NoError(t, svc.Create(&model.Company{
		CompanyName: "Beta", RegistrationNumber: "11111111", TaxNumber: "222222222",
		OwnerID: 99,
	}))

	got, err := svc.GetByOwnerID(99)
	require.NoError(t, err)
	assert.Equal(t, "Beta", got.CompanyName)
}

func TestCompanyService_GetMissing(t *testing.T) {
	svc := newCompanyService(t)
	_, err := svc.Get(999999)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CurrencyService
// ---------------------------------------------------------------------------

func TestCurrencyService_ListAndGet(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewCurrencyRepository(db)
	svc := NewCurrencyService(repo)
	require.NoError(t, db.Create(&model.Currency{Code: "RSD", Name: "Serbian Dinar", Symbol: "din", Active: true}).Error)
	require.NoError(t, db.Create(&model.Currency{Code: "EUR", Name: "Euro", Symbol: "€", Active: true}).Error)
	// Insert an inactive row via raw SQL — gorm's default:true clause would
	// otherwise rewrite Active=false back to true on insert.
	require.NoError(t, db.Exec(
		`INSERT INTO currencies (id, name, code, symbol, country, description, active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		3, "Hidden", "ZZZ", "?", "", "", false,
	).Error)

	listed, err := svc.List()
	require.NoError(t, err)
	assert.Len(t, listed, 2, "inactive currencies must be filtered out")

	got, err := svc.GetByCode("EUR")
	require.NoError(t, err)
	assert.Equal(t, "Euro", got.Name)

	_, err = svc.GetByCode("XXX")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ChangelogService
// ---------------------------------------------------------------------------

// changelogTestDB returns a DB with a sqlite-friendly Changelog table. The
// generated schema from gorm includes `DEFAULT now()` (Postgres-specific) on
// the changed_at column which sqlite refuses to parse, so we issue our own
// CREATE TABLE.
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

func TestChangelogService_RequiresEntityType(t *testing.T) {
	db := changelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("", 1, 1, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_type")
}

func TestChangelogService_RequiresPositiveEntityID(t *testing.T) {
	db := changelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("account", 0, 1, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_id")
}

func TestChangelogService_DefaultsAndCap(t *testing.T) {
	db := changelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	// Seed a few entries so pagination has something to page.
	entries := []changelog.Entry{
		{EntityType: "account", EntityID: 7, Action: "update", FieldName: "name", OldValue: "a", NewValue: "b", ChangedAt: time.Now()},
		{EntityType: "account", EntityID: 7, Action: "update", FieldName: "name", OldValue: "b", NewValue: "c", ChangedAt: time.Now()},
		{EntityType: "account", EntityID: 7, Action: "update", FieldName: "name", OldValue: "c", NewValue: "d", ChangedAt: time.Now()},
	}
	require.NoError(t, repo.CreateBatch(entries))

	// page <= 0 → defaults to 1; pageSize <= 0 → defaults to 20; capped at 200
	got, total, err := svc.ListChangelog("account", 7, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)

	// Cap-at-200 path
	got2, _, err := svc.ListChangelog("account", 7, 1, 5000)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got2), 200)
}

// ---------------------------------------------------------------------------
// LedgerService — Transfer, Credit, Debit, GetLedgerEntries
// ---------------------------------------------------------------------------

func newLedgerSvcWithTwoAccounts(t *testing.T) (*LedgerService, *model.Account, *model.Account) {
	t.Helper()
	db := newTestDB(t)
	ledgerRepo := repository.NewLedgerRepository(db)
	svc := NewLedgerService(ledgerRepo, db)
	from := seedAccount(t, db, "111000100000200011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))
	to := seedAccount(t, db, "111000100000201011", decimal.NewFromInt(0), decimal.NewFromInt(1_000_000))
	return svc, from, to
}

func TestLedgerService_Transfer_Happy(t *testing.T) {
	svc, from, to := newLedgerSvcWithTwoAccounts(t)

	err := svc.Transfer(context.Background(), from.AccountNumber, to.AccountNumber, decimal.NewFromInt(250),
		"transfer", "ref-1", "test")
	require.NoError(t, err)

	entries, total, err := svc.GetLedgerEntries(from.AccountNumber, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "debit", entries[0].EntryType)

	entries2, _, err := svc.GetLedgerEntries(to.AccountNumber, 1, 10)
	require.NoError(t, err)
	require.Len(t, entries2, 1)
	assert.Equal(t, "credit", entries2[0].EntryType)
}

func TestLedgerService_Transfer_InsufficientFunds(t *testing.T) {
	svc, from, to := newLedgerSvcWithTwoAccounts(t)

	err := svc.Transfer(context.Background(), from.AccountNumber, to.AccountNumber, decimal.NewFromInt(100_000),
		"too big", "ref-x", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient funds")
}

func TestLedgerService_CreditAndDebit(t *testing.T) {
	db := newTestDB(t)
	ledgerRepo := repository.NewLedgerRepository(db)
	svc := NewLedgerService(ledgerRepo, db)
	acct := seedAccount(t, db, "111000100000300011", decimal.NewFromInt(500), decimal.NewFromInt(1_000_000))

	require.NoError(t, svc.Credit(context.Background(), acct.AccountNumber, decimal.NewFromInt(150),
		"credit op", "r-c", "test"))
	require.NoError(t, svc.Debit(context.Background(), acct.AccountNumber, decimal.NewFromInt(50),
		"debit op", "r-d", "test"))

	entries, total, err := svc.GetLedgerEntries(acct.AccountNumber, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, entries, 2)
}

func TestLedgerService_ReconcileBalance_OK(t *testing.T) {
	svc, from, to := newLedgerSvcWithTwoAccounts(t)

	require.NoError(t, svc.Transfer(context.Background(), from.AccountNumber, to.AccountNumber, decimal.NewFromInt(100), "ok", "r1", "t"))
	require.NoError(t, svc.ReconcileBalance(context.Background(), to.AccountNumber))
}

// ---------------------------------------------------------------------------
// IncomingReservationService — Reserve / Commit / Release
// ---------------------------------------------------------------------------

func newIncomingFixture(t *testing.T) (*IncomingReservationService, *model.Account) {
	t.Helper()
	db := newTestDB(t)
	accountRepo := repository.NewAccountRepository(db)
	resRepo := repository.NewIncomingReservationRepository(db)
	svc := NewIncomingReservationService(db, accountRepo, resRepo)
	acct := seedAccount(t, db, "111000100000400011", decimal.NewFromInt(0), decimal.NewFromInt(1_000_000))
	return svc, acct
}

func TestIncomingReservationService_Reserve_RejectsNonPositive(t *testing.T) {
	svc, acct := newIncomingFixture(t)
	_, err := svc.ReserveIncoming(context.Background(), acct.AccountNumber, decimal.Zero, "RSD", "k0")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestIncomingReservationService_Reserve_NotFound(t *testing.T) {
	svc, _ := newIncomingFixture(t)
	_, err := svc.ReserveIncoming(context.Background(), "no-such-account", decimal.NewFromInt(10), "RSD", "k0a")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestIncomingReservationService_Reserve_CurrencyMismatch(t *testing.T) {
	svc, acct := newIncomingFixture(t)
	_, err := svc.ReserveIncoming(context.Background(), acct.AccountNumber, decimal.NewFromInt(10), "EUR", "k0b")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestIncomingReservationService_ReserveAndCommit(t *testing.T) {
	svc, acct := newIncomingFixture(t)
	ctx := context.Background()

	res, err := svc.ReserveIncoming(ctx, acct.AccountNumber, decimal.NewFromInt(750), "RSD", "k1")
	require.NoError(t, err)
	require.NotNil(t, res)

	// Idempotent reserve: same key returns same row, no balance impact.
	res2, err := svc.ReserveIncoming(ctx, acct.AccountNumber, decimal.NewFromInt(750), "RSD", "k1")
	require.NoError(t, err)
	assert.Equal(t, res.ID, res2.ID)

	// Commit credits the account.
	updated, err := svc.CommitIncoming(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, updated.Balance.Equal(decimal.NewFromInt(750)))

	// Commit again is idempotent — returns current account state.
	updated2, err := svc.CommitIncoming(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, updated2.Balance.Equal(decimal.NewFromInt(750)))
}

func TestIncomingReservationService_Commit_NotFound(t *testing.T) {
	svc, _ := newIncomingFixture(t)
	_, err := svc.CommitIncoming(context.Background(), "missing-key")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestIncomingReservationService_Release(t *testing.T) {
	svc, acct := newIncomingFixture(t)
	ctx := context.Background()

	_, err := svc.ReserveIncoming(ctx, acct.AccountNumber, decimal.NewFromInt(150), "RSD", "k2")
	require.NoError(t, err)

	require.NoError(t, svc.ReleaseIncoming(ctx, "k2"))
	// Releasing again is a no-op (status is no longer pending).
	require.NoError(t, svc.ReleaseIncoming(ctx, "k2"))
}

func TestIncomingReservationService_Release_NotFound(t *testing.T) {
	svc, _ := newIncomingFixture(t)
	err := svc.ReleaseIncoming(context.Background(), "no-such-key")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------------------------------------------------------------------
// Cron services — exercise constructor + Start with cancelled ctx so the
// scheduling goroutines exit immediately via the <-ctx.Done() branch.
// ---------------------------------------------------------------------------

func TestSpendingCronService_StartAndCancel(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	svc := NewSpendingCronService(repo)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	cancel()
	// Give goroutines a tick to honour cancellation.
	time.Sleep(50 * time.Millisecond)
}

func TestMaintenanceCronService_StartAndCancel(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := NewLedgerService(ledgerRepo, db)
	svc := NewMaintenanceCronService(repo, ledgerSvc)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// chargeMaintenanceFees runs synchronously inside the cron loop. Exercise
// it directly so its branches (no bank account, ineligible balance, success)
// get coverage without waiting for the cron timer.
func TestMaintenanceCronService_ChargeMaintenanceFees_NoBankAccount(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db)
	svc := NewMaintenanceCronService(repo, ledgerSvc)

	// Seed a client account that is eligible for maintenance fees but no bank
	// RSD account exists — should warn-and-skip without erroring.
	acct := seedAccount(t, db, "111000100001000011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", acct.ID).
		Update("maintenance_fee", decimal.NewFromInt(220)).Error)

	svc.chargeMaintenanceFees(context.Background())
}

func TestMaintenanceCronService_ChargeMaintenanceFees_InsufficientBalance(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db)
	svc := NewMaintenanceCronService(repo, ledgerSvc)

	// Bank account exists.
	require.NoError(t, db.Create(&model.Account{
		AccountNumber: "111999999999999991", OwnerID: BankOwnerID, OwnerName: "Bank",
		AccountKind: "current", AccountType: "bank", CurrencyCode: "RSD",
		Status: "active", Balance: decimal.Zero, AvailableBalance: decimal.Zero,
		IsBankAccount: true, ExpiresAt: time.Now().AddDate(50, 0, 0), Version: 1,
	}).Error)

	// Client account with insufficient balance for the fee.
	acct := seedAccount(t, db, "111000100001100011", decimal.NewFromInt(10), decimal.NewFromInt(1_000_000))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", acct.ID).
		Update("maintenance_fee", decimal.NewFromInt(220)).Error)

	svc.chargeMaintenanceFees(context.Background())

	// Balance unchanged — fee was skipped.
	got, err := repo.GetByID(acct.ID)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(10)), "balance should remain 10")
}

func TestMaintenanceCronService_ChargeMaintenanceFees_Happy(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	ledgerSvc := NewLedgerService(repository.NewLedgerRepository(db), db)
	svc := NewMaintenanceCronService(repo, ledgerSvc)

	// Bank RSD account.
	require.NoError(t, db.Create(&model.Account{
		AccountNumber: "111999999999999992", OwnerID: BankOwnerID, OwnerName: "Bank",
		AccountKind: "current", AccountType: "bank", CurrencyCode: "RSD",
		Status: "active", Balance: decimal.Zero, AvailableBalance: decimal.Zero,
		IsBankAccount: true, ExpiresAt: time.Now().AddDate(50, 0, 0), Version: 1,
	}).Error)

	// Client account with enough balance.
	acct := seedAccount(t, db, "111000100001200011", decimal.NewFromInt(1000), decimal.NewFromInt(1_000_000))
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", acct.ID).
		Update("maintenance_fee", decimal.NewFromInt(220)).Error)

	svc.chargeMaintenanceFees(context.Background())

	got, err := repo.GetByID(acct.ID)
	require.NoError(t, err)
	assert.True(t, got.Balance.Equal(decimal.NewFromInt(780)), "balance should be 780 after 220 fee charge: %s", got.Balance)
}

func TestIncomingReservationService_CommitAfterRelease_FailsPrecondition(t *testing.T) {
	svc, acct := newIncomingFixture(t)
	ctx := context.Background()

	_, err := svc.ReserveIncoming(ctx, acct.AccountNumber, decimal.NewFromInt(40), "RSD", "k3")
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseIncoming(ctx, "k3"))

	_, err = svc.CommitIncoming(ctx, "k3")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}
