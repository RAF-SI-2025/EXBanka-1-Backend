package model

// Trivial model-layer coverage: TableName overrides + SeedCurrencies upsert
// + BeforeUpdate hooks (exercised indirectly via gorm.Save). Uses an
// in-memory SQLite DB.

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:model_"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	return db
}

func TestTableNames(t *testing.T) {
	assert.Equal(t, "account_reservations", AccountReservation{}.TableName())
	assert.Equal(t, "account_reservation_settlements", AccountReservationSettlement{}.TableName())
	assert.Equal(t, "changelogs", Changelog{}.TableName())
	assert.Equal(t, "incoming_reservations", IncomingReservation{}.TableName())
	assert.Equal(t, "idempotency_records", IdempotencyRecord{}.TableName())
}

func TestSeedCurrencies_UpsertsAll(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&Currency{}))

	require.NoError(t, SeedCurrencies(db))
	var count int64
	require.NoError(t, db.Model(&Currency{}).Count(&count).Error)
	assert.Equal(t, int64(8), count, "expect 8 default currencies seeded")

	// Re-running is a no-op (FirstOrCreate).
	require.NoError(t, SeedCurrencies(db))
	require.NoError(t, db.Model(&Currency{}).Count(&count).Error)
	assert.Equal(t, int64(8), count)
}

// Account.BeforeUpdate increments Version on Save.
func TestAccount_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&Account{}))

	a := &Account{
		AccountNumber:    "111000100000099011",
		OwnerID:          1,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          decimal.NewFromInt(100),
		AvailableBalance: decimal.NewFromInt(100),
		ExpiresAt:        time.Now().AddDate(1, 0, 0),
		Version:          1,
	}
	require.NoError(t, db.Create(a).Error)
	assert.Equal(t, int64(1), a.Version)

	a.AccountName = "Updated"
	require.NoError(t, db.Save(a).Error)
	assert.Equal(t, int64(2), a.Version, "BeforeUpdate increments Version")
}

// Company.BeforeUpdate increments Version on Save.
func TestCompany_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&Company{}))

	c := &Company{
		CompanyName:        "Acme",
		RegistrationNumber: "12345678",
		TaxNumber:          "123456789",
		ActivityCode:       "62.01",
		Address:            "Belgrade",
		OwnerID:            1,
		Version:            1,
	}
	require.NoError(t, db.Create(c).Error)
	assert.Equal(t, int64(1), c.Version)

	c.CompanyName = "Renamed"
	require.NoError(t, db.Save(c).Error)
	assert.Equal(t, int64(2), c.Version)
}

// AccountReservation.BeforeUpdate increments Version.
func TestAccountReservation_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&AccountReservation{}))

	r := &AccountReservation{
		AccountID: 1, OrderID: 100, Amount: decimal.NewFromInt(50),
		CurrencyCode: "RSD", Status: ReservationStatusActive,
	}
	require.NoError(t, db.Create(r).Error)
	startVersion := r.Version

	r.Status = ReservationStatusReleased
	require.NoError(t, db.Save(r).Error)
	assert.Equal(t, startVersion+1, r.Version)
}

// IncomingReservation.BeforeUpdate increments Version.
func TestIncomingReservation_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&IncomingReservation{}))

	r := &IncomingReservation{
		AccountNumber: "111000100000099022", Amount: decimal.NewFromInt(25),
		Currency: "RSD", ReservationKey: "rk-test", Status: IncomingReservationStatusPending,
	}
	require.NoError(t, db.Create(r).Error)
	startVersion := r.Version

	r.Status = IncomingReservationStatusCommitted
	require.NoError(t, db.Save(r).Error)
	assert.Equal(t, startVersion+1, r.Version)
}
