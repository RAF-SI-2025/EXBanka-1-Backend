package model

// Extra model-layer coverage: OutgoingReservation TableName + BeforeUpdate, and
// the Account currency-immutable invariant (Fix R9) including the sentinel
// error's Error() string.

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutgoingReservation_TableName(t *testing.T) {
	assert.Equal(t, "outgoing_reservations", OutgoingReservation{}.TableName())
}

// OutgoingReservation.BeforeUpdate increments Version on Save.
func TestOutgoingReservation_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&OutgoingReservation{}))

	r := &OutgoingReservation{
		AccountNumber:  "111000100000099044",
		Amount:         decimal.NewFromInt(300),
		Currency:       "RSD",
		ReservationKey: "ork-test",
		Status:         OutgoingReservationStatusPending,
	}
	require.NoError(t, db.Create(r).Error)
	startVersion := r.Version

	r.Status = OutgoingReservationStatusReleased
	require.NoError(t, db.Save(r).Error)
	assert.Equal(t, startVersion+1, r.Version)
}

// Account.BeforeUpdate forbids changing CurrencyCode post-creation (Fix R9). The
// update must fail with errAccountCurrencyImmutable and leave the stored
// currency unchanged.
func TestAccount_BeforeUpdate_CurrencyImmutable(t *testing.T) {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&Account{}))

	a := &Account{
		AccountNumber:    "111000100000099055",
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

	err := db.Model(a).Updates(map[string]interface{}{"currency_code": "EUR"}).Error
	require.Error(t, err)
	assert.ErrorIs(t, err, errAccountCurrencyImmutable)
	assert.Contains(t, err.Error(), "immutable")

	// The stored currency is unchanged.
	var fresh Account
	require.NoError(t, db.First(&fresh, a.ID).Error)
	assert.Equal(t, "RSD", fresh.CurrencyCode)
}
