package service

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
	"github.com/exbanka/credit-service/internal/repository"
)

func newChangelogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	// model.Changelog uses Postgres-only `default:now()`; build the table
	// manually so SQLite can parse it.
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
          changed_at DATETIME NOT NULL,
          reason TEXT
        )`).Error)
	return db
}

func TestChangelogService_ListChangelog_ReturnsRowsForEntity(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		entry := changelog.Entry{
			EntityType: "loan_request",
			EntityID:   42,
			Action:     "status_change",
			FieldName:  "status",
			OldValue:   "pending",
			NewValue:   "approved",
			ChangedBy:  7,
			ChangedAt:  now.Add(time.Duration(i) * time.Second),
			Reason:     fmt.Sprintf("attempt %d", i),
		}
		require.NoError(t, repo.Create(entry))
	}

	// Add a row for a different entity that should NOT be returned
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan_request",
		EntityID:   99,
		Action:     "status_change",
		ChangedBy:  1,
		ChangedAt:  now,
	}))

	rows, total, err := svc.ListChangelog("loan_request", 42, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
	for _, r := range rows {
		assert.Equal(t, int64(42), r.EntityID)
		assert.Equal(t, "loan_request", r.EntityType)
	}
}

func TestChangelogService_ListChangelog_AppliesPaginationDefaults(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "loan",
			EntityID:   10,
			Action:     "create",
			ChangedBy:  1,
			ChangedAt:  time.Now().UTC().Add(time.Duration(i) * time.Second),
		}))
	}

	// page=0 and pageSize=0 should default to 1/20
	rows, total, err := svc.ListChangelog("loan", 10, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 5)
}

func TestChangelogService_ListChangelog_PageSizeCappedAt200(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan",
		EntityID:   1,
		Action:     "create",
		ChangedBy:  1,
		ChangedAt:  time.Now().UTC(),
	}))

	// pageSize > 200 must not error; cap should silently apply.
	rows, total, err := svc.ListChangelog("loan", 1, 1, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
}

func TestChangelogService_ListChangelog_InvalidEntityType(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	_, _, err := svc.ListChangelog("", 1, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_type is required")
}

func TestChangelogService_ListChangelog_InvalidEntityID(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	_, _, err := svc.ListChangelog("loan_request", 0, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_id must be positive")
}

func TestChangelogService_ListChangelog_NegativeEntityID(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	_, _, err := svc.ListChangelog("loan_request", -1, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_id must be positive")
}

func TestChangelogService_ListChangelog_OrdersByChangedAtDesc(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	now := time.Now().UTC()
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan", EntityID: 5, Action: "a",
		NewValue: "old", ChangedBy: 1, ChangedAt: now.Add(-2 * time.Hour),
	}))
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan", EntityID: 5, Action: "a",
		NewValue: "newer", ChangedBy: 1, ChangedAt: now.Add(-1 * time.Hour),
	}))
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "loan", EntityID: 5, Action: "a",
		NewValue: "newest", ChangedBy: 1, ChangedAt: now,
	}))

	rows, _, err := svc.ListChangelog("loan", 5, 1, 50)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, "newest", rows[0].NewValue)
	assert.Equal(t, "newer", rows[1].NewValue)
	assert.Equal(t, "old", rows[2].NewValue)
}
