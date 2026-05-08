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

func openMemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestCard_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&Card{}))
	c := &Card{
		CardNumber: "411111XXXXXX1111", CardNumberFull: "4111111111111111",
		CVV: "123", CardBrand: "visa", AccountNumber: "ACC1",
		OwnerID: 1, OwnerType: "client", Status: "active",
	}
	require.NoError(t, db.Create(c).Error)
	v := c.Version

	c.Status = "blocked"
	require.NoError(t, db.Save(c).Error)
	assert.Equal(t, v+1, c.Version, "BeforeUpdate must increment Version")
}
