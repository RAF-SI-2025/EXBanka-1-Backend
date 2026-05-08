package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
)

// newLoanTestDB opens a fresh in-memory SQLite database with the Loan
// table migrated. A unique DSN per test name keeps concurrent t.Parallel()
// runs from sharing state.
func newLoanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Loan{}))
	return db
}

func newSampleLoan(loanNumber string) *model.Loan {
	now := time.Now()
	return &model.Loan{
		LoanNumber:            loanNumber,
		LoanType:              "cash",
		AccountNumber:         "ACC-1",
		Amount:                decimal.NewFromInt(100000),
		RepaymentPeriod:       12,
		NominalInterestRate:   decimal.NewFromFloat(8.0),
		EffectiveInterestRate: decimal.NewFromFloat(8.3),
		ContractDate:          now,
		MaturityDate:          now.AddDate(1, 0, 0),
		NextInstallmentAmount: decimal.NewFromInt(8700),
		NextInstallmentDate:   now.AddDate(0, 1, 0),
		RemainingDebt:         decimal.NewFromInt(100000),
		CurrencyCode:          "RSD",
		Status:                "active",
		InterestType:          "fixed",
		BaseRate:              decimal.NewFromFloat(6.25),
		BankMargin:            decimal.NewFromFloat(1.75),
		CurrentRate:           decimal.NewFromFloat(8.0),
		ClientID:              1,
	}
}

func TestLoanRepo_GenerateLoanNumber_FormatAndLength(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	for i := 0; i < 5; i++ {
		ln := repo.GenerateLoanNumber()
		assert.Len(t, ln, 12, "loan number should be LN + 10 digits = 12 chars")
		assert.True(t, strings.HasPrefix(ln, "LN"), "loan number should start with LN")
		// Verify the digits part
		for _, c := range ln[2:] {
			assert.True(t, c >= '0' && c <= '9', "expected digit, got %q", c)
		}
	}
}

func TestLoanRepo_CreateAndGetByID(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	loan := newSampleLoan("LN-CRG-001")
	require.NoError(t, repo.Create(loan))
	assert.NotZero(t, loan.ID)

	got, err := repo.GetByID(loan.ID)
	require.NoError(t, err)
	assert.Equal(t, "LN-CRG-001", got.LoanNumber)
	assert.Equal(t, "cash", got.LoanType)
	assert.True(t, got.Amount.Equal(decimal.NewFromInt(100000)))
}

func TestLoanRepo_GetByID_NotFound(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	_, err := repo.GetByID(99999)
	require.Error(t, err)
}

func TestLoanRepo_List_Pagination(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	// Seed 5 loans for client 1, 2 for client 2
	for i := 0; i < 5; i++ {
		l := newSampleLoan(fmt.Sprintf("LN-LIST-1-%d", i))
		l.ClientID = 1
		require.NoError(t, repo.Create(l))
	}
	for i := 0; i < 2; i++ {
		l := newSampleLoan(fmt.Sprintf("LN-LIST-2-%d", i))
		l.ClientID = 2
		require.NoError(t, repo.Create(l))
	}

	// Page 1 of size 3 for client 1 should return 3 records, total=5
	got, total, err := repo.List(1, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, got, 3)

	// Page 2 of size 3 should return remaining 2
	got2, total2, err := repo.List(1, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total2)
	assert.Len(t, got2, 2)

	// Client 2 has just 2 loans
	got3, total3, err := repo.List(2, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total3)
	assert.Len(t, got3, 2)
}

func TestLoanRepo_List_NegativeOffsetSafe(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	require.NoError(t, repo.Create(newSampleLoan("LN-NO-001")))
	// Page 0 -> offset -3 -> code clamps to 0
	got, total, err := repo.List(1, 0, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)
}

func TestLoanRepo_ListAll_Filters(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	cash := newSampleLoan("LN-FILT-1")
	cash.LoanType = "cash"
	cash.Status = "active"
	cash.AccountNumber = "ACC-A"
	require.NoError(t, repo.Create(cash))

	housing := newSampleLoan("LN-FILT-2")
	housing.LoanType = "housing"
	housing.Status = "approved"
	housing.AccountNumber = "ACC-B"
	require.NoError(t, repo.Create(housing))

	auto := newSampleLoan("LN-FILT-3")
	auto.LoanType = "auto"
	auto.Status = "active"
	auto.AccountNumber = "ACC-A"
	require.NoError(t, repo.Create(auto))

	// Filter by type only
	got, total, err := repo.ListAll("cash", "", "", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)
	assert.Equal(t, "cash", got[0].LoanType)

	// Filter by account
	got, total, err = repo.ListAll("", "ACC-A", "", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, got, 2)

	// Filter by status
	got, total, err = repo.ListAll("", "", "active", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, got, 2)

	// All filters
	got, total, err = repo.ListAll("housing", "ACC-B", "approved", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)

	// Pagination with -ve offset clamping
	got, total, err = repo.ListAll("", "", "", 0, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 2)
}

func TestLoanRepo_Update_VersionedSuccess(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	loan := newSampleLoan("LN-UP-1")
	require.NoError(t, repo.Create(loan))

	// Re-fetch to get version
	fresh, err := repo.GetByID(loan.ID)
	require.NoError(t, err)
	fresh.Status = "completed"
	require.NoError(t, repo.Update(fresh))

	got, err := repo.GetByID(loan.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, fresh.Version, got.Version, "version should be incremented after save")
}

func TestLoanRepo_FindActiveVariableLoansInAmountRange(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	// Within range, active+variable: matches
	in := newSampleLoan("LN-VAR-IN")
	in.InterestType = "variable"
	in.Status = "active"
	in.Amount = decimal.NewFromInt(750_000)
	require.NoError(t, repo.Create(in))

	// Wrong interest type: skipped
	fixed := newSampleLoan("LN-VAR-FIX")
	fixed.InterestType = "fixed"
	fixed.Status = "active"
	fixed.Amount = decimal.NewFromInt(750_000)
	require.NoError(t, repo.Create(fixed))

	// Wrong status: skipped
	closed := newSampleLoan("LN-VAR-CL")
	closed.InterestType = "variable"
	closed.Status = "completed"
	closed.Amount = decimal.NewFromInt(750_000)
	require.NoError(t, repo.Create(closed))

	// Out of range (above): skipped
	above := newSampleLoan("LN-VAR-AB")
	above.InterestType = "variable"
	above.Status = "active"
	above.Amount = decimal.NewFromInt(2_000_000)
	require.NoError(t, repo.Create(above))

	got, err := repo.FindActiveVariableLoansInAmountRange(
		decimal.NewFromInt(500_000), decimal.NewFromInt(1_000_000))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "LN-VAR-IN", got[0].LoanNumber)

	// Unbounded upper: amountTo=0 — finds matches >= amountFrom
	gotUnbounded, err := repo.FindActiveVariableLoansInAmountRange(
		decimal.NewFromInt(500_000), decimal.Zero)
	require.NoError(t, err)
	// Should pick up "LN-VAR-IN" (750k) and "LN-VAR-AB" (2M)
	assert.Len(t, gotUnbounded, 2)
}
