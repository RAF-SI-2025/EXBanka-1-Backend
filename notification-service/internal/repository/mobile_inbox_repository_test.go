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

// newInboxTestDB opens a fresh in-memory SQLite database per test and
// auto-migrates the MobileInboxItem schema.
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

// seedInboxItem inserts a MobileInboxItem with sensible defaults.
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

// TestGetPendingByUserAndDevice_ReturnsPendingItems verifies that pending,
// non-expired items for the correct user+device are returned.
func TestGetPendingByUserAndDevice_ReturnsPendingItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:      10,
		DeviceID:    "device-abc",
		ChallengeID: 1,
		Method:      "code_pull",
	})
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:      10,
		DeviceID:    "device-abc",
		ChallengeID: 2,
		Method:      "qr_scan",
	})

	items, err := repo.GetPendingByUserAndDevice(10, "device-abc")
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

// TestGetPendingByUserAndDevice_EmptyForUnknownDevice verifies that no items
// are returned when querying with a device ID that has no associated items.
func TestGetPendingByUserAndDevice_EmptyForUnknownDevice(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:   10,
		DeviceID: "device-abc",
	})

	items, err := repo.GetPendingByUserAndDevice(10, "device-UNKNOWN")
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestGetPendingByUserAndDevice_ExcludesExpiredItems verifies that items whose
// ExpiresAt is in the past are not returned as pending.
func TestGetPendingByUserAndDevice_ExcludesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	// Insert an already-expired item.
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		DeviceID:  "device-abc",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // expired
	})

	items, err := repo.GetPendingByUserAndDevice(10, "device-abc")
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestGetPendingByUserAndDevice_ExcludesDeliveredItems verifies that items
// with status "delivered" are not included in the pending list.
func TestGetPendingByUserAndDevice_ExcludesDeliveredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	delivered := seedInboxItem(t, db, model.MobileInboxItem{
		UserID:   10,
		DeviceID: "device-abc",
		Status:   "delivered",
	})
	_ = delivered

	items, err := repo.GetPendingByUserAndDevice(10, "device-abc")
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestMarkDelivered_MarksItemAsDelivered verifies that MarkDelivered sets the
// status to "delivered" and records a DeliveredAt timestamp.
func TestMarkDelivered_MarksItemAsDelivered(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{
		UserID:   10,
		DeviceID: "device-abc",
	})

	err := repo.MarkDelivered(item.ID, "device-abc")
	require.NoError(t, err)

	var updated model.MobileInboxItem
	require.NoError(t, db.First(&updated, item.ID).Error)
	assert.Equal(t, "delivered", updated.Status)
	assert.NotNil(t, updated.DeliveredAt)
}

// TestMarkDelivered_ReturnsErrorForUnknownItem verifies that MarkDelivered
// returns an error when no matching pending item exists.
func TestMarkDelivered_ReturnsErrorForUnknownItem(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	err := repo.MarkDelivered(9999, "device-abc")
	assert.Error(t, err)
}

// TestMarkDelivered_ReturnsErrorForWrongDevice verifies that MarkDelivered
// does not affect an item belonging to a different device.
func TestMarkDelivered_ReturnsErrorForWrongDevice(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{
		UserID:   10,
		DeviceID: "device-abc",
	})

	err := repo.MarkDelivered(item.ID, "device-WRONG")
	assert.Error(t, err)

	// Verify the item is still pending.
	var unchanged model.MobileInboxItem
	require.NoError(t, db.First(&unchanged, item.ID).Error)
	assert.Equal(t, "pending", unchanged.Status)
}

// TestDeleteExpired_RemovesExpiredItems verifies that DeleteExpired removes
// items whose ExpiresAt is in the past and returns the count of deleted rows.
func TestDeleteExpired_RemovesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	// Two expired items.
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		DeviceID:  "device-abc",
		ExpiresAt: time.Now().Add(-2 * time.Minute),
	})
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		DeviceID:  "device-abc",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})
	// One still-valid item.
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		DeviceID:  "device-abc",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	// Confirm only the valid item remains.
	var remaining []model.MobileInboxItem
	require.NoError(t, db.Find(&remaining).Error)
	assert.Len(t, remaining, 1)
}

// TestDeleteExpired_NothingToDelete verifies that DeleteExpired returns 0 when
// there are no expired items.
func TestDeleteExpired_NothingToDelete(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	// Only a fresh item that has not expired.
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		DeviceID:  "device-abc",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)
}
