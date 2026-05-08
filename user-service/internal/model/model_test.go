// model_test.go — small tests for model helpers and hook side effects.
package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return db
}

// TestEmployee_BeforeUpdate_BumpsVersion exercises the hook through a real
// gorm.Save call against a sqlite database. We don't assert the WHERE clause
// directly — we observe the side effect (Version incremented).
func TestEmployee_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, db.AutoMigrate(&Employee{}))

	emp := &Employee{
		Email:       "v@x",
		FirstName:   "V",
		LastName:    "X",
		JMBG:        "1234567890123",
		Username:    "vx",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Version:     1,
	}
	require.NoError(t, db.Create(emp).Error)

	emp.LastName = "Updated"
	require.NoError(t, db.Save(emp).Error)
	assert.Equal(t, int64(2), emp.Version, "Save should bump Version via BeforeUpdate hook")
}

func TestEmployeeLimit_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, db.AutoMigrate(&EmployeeLimit{}))

	limit := &EmployeeLimit{EmployeeID: 1, Version: 1}
	require.NoError(t, db.Create(limit).Error)

	require.NoError(t, db.Save(limit).Error)
	assert.Equal(t, int64(2), limit.Version)
}

func TestActuaryLimit_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, db.AutoMigrate(&ActuaryLimit{}))

	a := &ActuaryLimit{EmployeeID: 1, Version: 1}
	require.NoError(t, db.Create(a).Error)

	require.NoError(t, db.Save(a).Error)
	assert.Equal(t, int64(2), a.Version)
}

func TestActuaryRow_ActuaryLimitID(t *testing.T) {
	row := ActuaryRow{}
	assert.Equal(t, uint64(0), row.ActuaryLimitID(), "nil pointer returns 0")

	id := int64(42)
	row2 := ActuaryRow{ActuaryLimitIDPtr: &id}
	assert.Equal(t, uint64(42), row2.ActuaryLimitID())
}

func TestChangelog_TableName(t *testing.T) {
	cl := Changelog{}
	assert.Equal(t, "changelogs", cl.TableName())
}
