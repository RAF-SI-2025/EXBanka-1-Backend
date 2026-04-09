package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newInboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.MobileInboxItem{}))
	return db
}

func seedInboxItem(t *testing.T, db *gorm.DB, opts model.MobileInboxItem) *model.MobileInboxItem {
	t.Helper()
	if opts.Method == "" {
		opts.Method = "code_pull"
	}
	if opts.Status == "" {
		opts.Status = "pending"
	}
	if opts.ExpiresAt.IsZero() {
		opts.ExpiresAt = time.Now().Add(5 * time.Minute)
	}
	require.NoError(t, db.Create(&opts).Error)
	return &opts
}

func TestGetPendingByUser_ReturnsPendingItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 1, Method: "code_pull"})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 2, Method: "qr_scan"})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestGetPendingByUser_ExcludesOtherUsers(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 100})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 99, ChallengeID: 101})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, uint64(100), items[0].ChallengeID)
}

func TestGetPendingByUser_ExcludesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetPendingByUser_ExcludesDeliveredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, Status: "delivered"})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestMarkDelivered_MarksItemAsDelivered(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{UserID: 10})

	err := repo.MarkDelivered(item.ID)
	require.NoError(t, err)

	var updated model.MobileInboxItem
	require.NoError(t, db.First(&updated, item.ID).Error)
	assert.Equal(t, "delivered", updated.Status)
	assert.NotNil(t, updated.DeliveredAt)
}

func TestMarkDelivered_ReturnsErrorForUnknownItem(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	err := repo.MarkDelivered(9999)
	assert.Error(t, err)
}

func TestMarkDelivered_ReturnsErrorForAlreadyDelivered(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, Status: "delivered"})

	err := repo.MarkDelivered(item.ID)
	assert.Error(t, err)
}

func TestDeleteExpired_RemovesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(-2 * time.Minute)})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(-1 * time.Minute)})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(10 * time.Minute)})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []model.MobileInboxItem
	require.NoError(t, db.Find(&remaining).Error)
	assert.Len(t, remaining, 1)
}

func TestDeleteExpired_NothingToDelete(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(10 * time.Minute)})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)
}
