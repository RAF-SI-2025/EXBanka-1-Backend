package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChangelog_TableName(t *testing.T) {
	assert.Equal(t, "changelogs", Changelog{}.TableName())
}

func openMemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestClient_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&Client{}))
	c := &Client{FirstName: "A", LastName: "B", Email: "a@b.c", JMBG: "0101990710024"}
	require.NoError(t, db.Create(c).Error)
	v := c.Version
	c.FirstName = "Updated"
	require.NoError(t, db.Save(c).Error)
	assert.Equal(t, v+1, c.Version)
}

func TestClientLimit_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.AutoMigrate(&ClientLimit{}))
	cl := &ClientLimit{ClientID: 1, SetByEmployee: 1}
	require.NoError(t, db.Create(cl).Error)
	v := cl.Version
	cl.SetByEmployee = 2
	require.NoError(t, db.Save(cl).Error)
	assert.Equal(t, v+1, cl.Version)
}
