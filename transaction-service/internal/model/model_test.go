package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Payment{}, &Transfer{}))
	return db
}

// TestPayment_BeforeUpdate_Direct exercises the hook in isolation: it bumps the
// Version on the receiver and registers the optimistic-lock WHERE clause.
func TestPayment_BeforeUpdate_Direct(t *testing.T) {
	db := newModelTestDB(t)
	p := &Payment{Version: 7}
	require.NoError(t, p.BeforeUpdate(db.Session(&gorm.Session{DryRun: true})))
	assert.Equal(t, int64(8), p.Version, "BeforeUpdate must increment Version")
}

func TestTransfer_BeforeUpdate_Direct(t *testing.T) {
	db := newModelTestDB(t)
	tr := &Transfer{Version: 3}
	require.NoError(t, tr.BeforeUpdate(db.Session(&gorm.Session{DryRun: true})))
	assert.Equal(t, int64(4), tr.Version, "BeforeUpdate must increment Version")
}

// TestPayment_Version_IncrementsOnPersistedSave proves the hook fires on a real
// GORM Save: a load-modify-save round-trip leaves the persisted row one version
// higher than it started.
func TestPayment_Version_IncrementsOnPersistedSave(t *testing.T) {
	db := newModelTestDB(t)
	p := &Payment{
		IdempotencyKey:    "opt-pay-1",
		FromAccountNumber: "A",
		ToAccountNumber:   "B",
		InitialAmount:     decimal.NewFromInt(100),
		FinalAmount:       decimal.NewFromInt(100),
		Status:            "pending",
		Version:           1,
	}
	require.NoError(t, db.Create(p).Error)

	var loaded Payment
	require.NoError(t, db.First(&loaded, p.ID).Error)
	loaded.Status = "completed"
	res := db.Save(&loaded)
	require.NoError(t, res.Error)
	assert.Equal(t, int64(1), res.RowsAffected, "current-version save must update exactly one row")

	var reloaded Payment
	require.NoError(t, db.First(&reloaded, p.ID).Error)
	assert.Equal(t, int64(2), reloaded.Version, "persisted Version must be incremented by BeforeUpdate")
	assert.Equal(t, "completed", reloaded.Status)
}

func TestTransfer_Version_IncrementsOnPersistedSave(t *testing.T) {
	db := newModelTestDB(t)
	tr := &Transfer{
		IdempotencyKey:    "opt-tr-1",
		FromAccountNumber: "A",
		ToAccountNumber:   "B",
		InitialAmount:     decimal.NewFromInt(50),
		FinalAmount:       decimal.NewFromInt(50),
		ExchangeRate:      decimal.NewFromInt(1),
		Status:            "pending",
		Version:           1,
	}
	require.NoError(t, db.Create(tr).Error)

	var loaded Transfer
	require.NoError(t, db.First(&loaded, tr.ID).Error)
	loaded.Status = "completed"
	res := db.Save(&loaded)
	require.NoError(t, res.Error)
	assert.Equal(t, int64(1), res.RowsAffected)

	var reloaded Transfer
	require.NoError(t, db.First(&reloaded, tr.ID).Error)
	assert.Equal(t, int64(2), reloaded.Version, "persisted Version must be incremented by BeforeUpdate")
	assert.Equal(t, "completed", reloaded.Status)
}
