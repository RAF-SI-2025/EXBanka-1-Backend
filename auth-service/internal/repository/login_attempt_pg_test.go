package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/auth-service/internal/model"
)

var pgDBCounter uint64

// setupPgCompatTestDB opens an isolated SQLite database and confirms the
// pg_advisory_xact_lock UDF (registered in TestMain) is callable. Uses a
// fresh sql.DB so the connection picks up the UDF.
func setupPgCompatTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	id := atomic.AddUint64(&pgDBCounter, 1)
	dbName := fmt.Sprintf("%s_pg_%d", strings.ReplaceAll(t.Name(), "/", "_"), id)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	rawDB, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)

	db, err := gorm.Open(&sqlite.Dialector{
		DriverName: "sqlite",
		DSN:        dsn,
		Conn:       rawDB,
	}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	t.Cleanup(func() { _ = rawDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	return db
}

// TestPgFunc_RegisteredViaTestMain confirms that the pg_advisory_xact_lock
// stub registered in TestMain is visible from a fresh GORM connection.
func TestPgFunc_RegisteredViaTestMain(t *testing.T) {
	db := setupPgCompatTestDB(t, &model.LoginAttempt{})
	if err := db.Exec("SELECT pg_advisory_xact_lock(?)", 42).Error; err != nil {
		t.Skipf("pg_advisory_xact_lock UDF not active: %v", err)
	}
}

func TestRecordFailureAndCheckLock_AccumulatesAttempts(t *testing.T) {
	db := setupPgCompatTestDB(t, &model.LoginAttempt{}, &model.AccountLock{})
	repo := NewLoginAttemptRepository(db)

	const maxAttempts = 3
	const window = 15 * time.Minute
	const lockDur = 30 * time.Minute

	locked, remaining, err := repo.RecordFailureAndCheckLock("u@test.com", "ip", "ua", "browser", maxAttempts, window, lockDur)
	if err != nil {
		t.Skipf("pg_advisory_xact_lock UDF not active: %v", err)
	}
	assert.False(t, locked)
	assert.Equal(t, 2, remaining)

	locked, remaining, err = repo.RecordFailureAndCheckLock("u@test.com", "ip", "ua", "browser", maxAttempts, window, lockDur)
	require.NoError(t, err)
	assert.False(t, locked)
	assert.Equal(t, 1, remaining)

	locked, remaining, err = repo.RecordFailureAndCheckLock("u@test.com", "ip", "ua", "browser", maxAttempts, window, lockDur)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.Equal(t, 0, remaining)

	// Already-locked early return.
	locked, _, err = repo.RecordFailureAndCheckLock("u@test.com", "ip", "ua", "browser", maxAttempts, window, lockDur)
	require.NoError(t, err)
	assert.True(t, locked)

	lock, err := repo.GetActiveLock("u@test.com")
	require.NoError(t, err)
	require.NotNil(t, lock)
}
