package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/exbanka/account-service/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Account{},
		&model.LedgerEntry{},
		&model.BankOperation{},
		&model.AccountReservation{},
		&model.AccountReservationSettlement{},
		&model.IdempotencyRecord{},
	))
	return db
}
