package testutil

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var dbCounter uint64

// SetupTestDB creates an isolated in-memory SQLite database with the given
// models auto-migrated. Each call produces a separate database keyed by test name
// and a monotonically increasing counter to ensure isolation across multiple calls
// within the same test.
func SetupTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	id := atomic.AddUint64(&dbCounter, 1)
	dbName := fmt.Sprintf("%s_%d", strings.ReplaceAll(t.Name(), "/", "_"), id)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("SetupTestDB: open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("SetupTestDB: raw db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("SetupTestDB: migrate: %v", err)
	}
	return db
}
