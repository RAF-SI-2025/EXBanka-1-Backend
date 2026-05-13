package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	pb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/credit-service/internal/model"
)

// TestLoanRepo_Update_DBClosedReturnsError exercises the error branch in
// LoanRepository.Update when Save fails (DB closed). The repo wraps the gorm
// error and returns it.
func TestLoanRepo_Update_DBClosedReturnsError(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	loan := newSampleLoan("LN-DBC-1")
	require.NoError(t, repo.Create(loan))

	// Close the underlying DB so subsequent writes fail
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	loan.Status = "closed"
	err = repo.Update(loan)
	require.Error(t, err)
}

// TestLoanRequestRepo_Update_DBClosedReturnsError exercises the error branch in
// LoanRequestRepository.Update when Save fails (DB closed).
func TestLoanRequestRepo_Update_DBClosedReturnsError(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	req := newSampleLoanRequest("ACC-DBC-2", "pending")
	require.NoError(t, repo.Create(req))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	req.Status = "approved"
	err = repo.Update(req)
	require.Error(t, err)
}

// TestLoanRepo_List_CountErrorBranch exercises the count-error path in List.
// Closing the DB before calling List makes the Count() query fail.
func TestLoanRepo_List_CountErrorBranch(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	require.NoError(t, repo.Create(newSampleLoan("LN-LCE-1")))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, _, err = repo.List(1, 1, 10)
	require.Error(t, err)
}

// TestLoanRepo_ListAll_CountErrorBranch covers the count error in ListAll.
func TestLoanRepo_ListAll_CountErrorBranch(t *testing.T) {
	db := newLoanTestDB(t)
	repo := NewLoanRepository(db)

	require.NoError(t, repo.Create(newSampleLoan("LN-LAE-1")))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, _, err = repo.ListAll("", "", "", 1, 10)
	require.Error(t, err)
}

// TestLoanRequestRepo_List_CountErrorBranch covers the count error in
// LoanRequestRepository.List.
func TestLoanRequestRepo_List_CountErrorBranch(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	require.NoError(t, repo.Create(newSampleLoanRequest("ACC-LRE-1", "pending")))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, _, err = repo.List("", "", "", 0, 1, 10)
	require.Error(t, err)
}

// TestInstallmentRepo_ListByLoan_DBError exercises the error branch.
func TestInstallmentRepo_ListByLoan_DBError(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.ListByLoan(1)
	require.Error(t, err)
}

// TestInstallmentRepo_GetDueInstallments_DBError covers the error branch.
func TestInstallmentRepo_GetDueInstallments_DBError(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.GetDueInstallments()
	require.Error(t, err)
}

// TestInstallmentRepo_MarkPaid_DBError covers the error branch in MarkPaid
// where the UPDATE itself errors.
func TestInstallmentRepo_MarkPaid_DBError(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	inst := &model.Installment{
		LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now, Status: "unpaid",
	}
	require.NoError(t, db.Create(inst).Error)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.MarkPaid(inst.ID)
	require.Error(t, err)
}

// TestBankMarginRepo_ListAll_DBError covers the error branch in ListAll.
func TestBankMarginRepo_ListAll_DBError(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.ListAll()
	require.Error(t, err)
}

// TestBankMarginRepo_Count_DBError covers the count error branch.
func TestBankMarginRepo_Count_DBError(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.Count()
	require.Error(t, err)
}

// TestTierRepo_ListAll_DBError covers the error branch in ListAll.
func TestTierRepo_ListAll_DBError(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.ListAll()
	require.Error(t, err)
}

// TestTierRepo_Count_DBError covers the count error branch.
func TestTierRepo_Count_DBError(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = repo.Count()
	require.Error(t, err)
}

// TestChangelogRepo_ListByEntity_CountError covers the count-error branch.
func TestChangelogRepo_ListByEntity_CountError(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, _, err = repo.ListByEntity("loan", 1, 1, 10)
	require.Error(t, err)
}

// TestIdempotency_DBClosedReturnsError exercises the error branch when the
// initial Create fails (e.g., DB closed).
func TestIdempotency_DBClosedReturnsError(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = Run(repo, db, "key-closed", func() *pb.LoanResponse { return &pb.LoanResponse{} },
		func() (*pb.LoanResponse, error) { return &pb.LoanResponse{}, nil })
	require.Error(t, err)
}
