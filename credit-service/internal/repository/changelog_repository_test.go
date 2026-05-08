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

	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/credit-service/internal/model"
)

func newChangelogRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	// SQLite does not understand the postgres-flavored `default:now()` clause used
	// by the production model. Create the table with an SQLite-compatible DDL
	// that matches the live schema column-for-column.
	require.NoError(t, db.Exec(`
		CREATE TABLE changelogs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			field_name TEXT,
			old_value TEXT,
			new_value TEXT,
			changed_by INTEGER NOT NULL,
			changed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			reason TEXT
		)
	`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX idx_changelog_entity ON changelogs (entity_type, entity_id)`).Error)
	_ = model.Changelog{} // ensure model package is referenced
	return db
}

func TestChangelogRepo_Create(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	entry := changelog.Entry{
		EntityType: "loan_request",
		EntityID:   42,
		Action:     "status_change",
		FieldName:  "status",
		OldValue:   "pending",
		NewValue:   "approved",
		ChangedBy:  7,
		ChangedAt:  time.Now(),
		Reason:     "approved by supervisor",
	}
	require.NoError(t, repo.Create(entry))

	var got model.Changelog
	require.NoError(t, db.First(&got, "entity_type = ? AND entity_id = ?", "loan_request", 42).Error)
	assert.Equal(t, "approved", got.NewValue)
	assert.Equal(t, "pending", got.OldValue)
	assert.Equal(t, int64(7), got.ChangedBy)
}

func TestChangelogRepo_CreateBatch_Empty(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	// Empty slice is a no-op (no error)
	require.NoError(t, repo.CreateBatch([]changelog.Entry{}))

	var count int64
	require.NoError(t, db.Model(&model.Changelog{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestChangelogRepo_CreateBatch(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	now := time.Now()
	entries := []changelog.Entry{
		{EntityType: "loan", EntityID: 1, Action: "create", FieldName: "amount", OldValue: "", NewValue: "100000", ChangedBy: 7, ChangedAt: now},
		{EntityType: "loan", EntityID: 1, Action: "update", FieldName: "status", OldValue: "approved", NewValue: "active", ChangedBy: 7, ChangedAt: now},
		{EntityType: "loan", EntityID: 2, Action: "create", FieldName: "amount", OldValue: "", NewValue: "200000", ChangedBy: 8, ChangedAt: now},
	}
	require.NoError(t, repo.CreateBatch(entries))

	var count int64
	require.NoError(t, db.Model(&model.Changelog{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}

func TestChangelogRepo_ListByEntity_Pagination(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	now := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "loan",
			EntityID:   1,
			Action:     "update",
			FieldName:  "status",
			OldValue:   "old",
			NewValue:   fmt.Sprintf("new-%d", i),
			ChangedBy:  7,
			ChangedAt:  now.Add(time.Duration(i) * time.Second),
		}))
	}
	// Other entity (should not appear in our filtered listing)
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan", EntityID: 2, Action: "update", FieldName: "x",
		OldValue: "a", NewValue: "b", ChangedBy: 9, ChangedAt: now,
	}))

	// Page 1 of size 3 — should return 3, total=5, ordered DESC by ChangedAt
	got, total, err := repo.ListByEntity("loan", 1, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, got, 3)
	// Newest first (i=4)
	assert.Equal(t, "new-4", got[0].NewValue)

	// Page 2 of size 3 — should return remaining 2
	got, total, err = repo.ListByEntity("loan", 1, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, got, 2)
}

func TestChangelogRepo_ListByEntity_NoMatch(t *testing.T) {
	db := newChangelogRepoDB(t)
	repo := NewChangelogRepository(db)

	got, total, err := repo.ListByEntity("loan", 999, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, got)
}
