package model

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newCoverageTestDB returns a real in-memory sqlite session so BeforeUpdate
// hooks that dereference tx.Statement.Where can be exercised without panicking.
func newCoverageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// We only need a Statement-bearing session to drive the BeforeUpdate
	// hooks; we don't actually run SQL. Wrap in a fresh Session so each
	// caller starts with an empty Statement.
	return db.Session(&gorm.Session{NewDB: true})
}
