package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTableNames(t *testing.T) {
	assert.Equal(t, "idempotency_records", IdempotencyRecord{}.TableName())
	assert.Equal(t, "changelogs", Changelog{}.TableName())
}

// openMemDB returns an in-memory SQLite GORM handle (callers AutoMigrate).
func openMemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

// TestInstallment_BeforeUpdate_BumpsVersion verifies the optimistic-lock hook
// on Installment fires and increments Version on every update.
func TestInstallment_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&Installment{}))
	inst := &Installment{Status: "unpaid"}
	require.NoError(t, db.Create(inst).Error)
	versionBefore := inst.Version

	inst.Status = "paid"
	require.NoError(t, db.Save(inst).Error)
	assert.Equal(t, versionBefore+1, inst.Version, "BeforeUpdate must increment Version")
}

func TestLoan_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&Loan{}))
	loan := &Loan{LoanNumber: "LN1", Status: "approved", CurrencyCode: "RSD"}
	require.NoError(t, db.Create(loan).Error)
	versionBefore := loan.Version

	loan.Status = "active"
	require.NoError(t, db.Save(loan).Error)
	assert.Equal(t, versionBefore+1, loan.Version)
}

func TestLoanRequest_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&LoanRequest{}))
	lr := &LoanRequest{Status: "pending", LoanType: "cash", CurrencyCode: "RSD"}
	require.NoError(t, db.Create(lr).Error)
	versionBefore := lr.Version

	lr.Status = "approved"
	require.NoError(t, db.Save(lr).Error)
	assert.Equal(t, versionBefore+1, lr.Version)
}
