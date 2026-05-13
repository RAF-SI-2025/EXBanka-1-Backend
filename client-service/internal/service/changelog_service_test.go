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

	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

func newClientChangelogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
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
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "client",
			EntityID:   42,
			Action:     "update",
			ChangedBy:  7,
			ChangedAt:  now.Add(time.Duration(i) * time.Second),
		}))
	}
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "client", EntityID: 99,
		Action: "update", ChangedBy: 1, ChangedAt: now,
	}))

	rows, total, err := svc.ListChangelog("client", 42, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}

func TestChangelogService_ListChangelog_PaginationDefaults(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	for i := 0; i < 4; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "client", EntityID: 1, Action: "update",
			ChangedBy: 1, ChangedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}))
	}
	rows, total, err := svc.ListChangelog("client", 1, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Len(t, rows, 4)
}

func TestChangelogService_ListChangelog_PageSizeCappedAt200(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "client", EntityID: 1, Action: "update",
		ChangedBy: 1, ChangedAt: time.Now().UTC(),
	}))
	rows, total, err := svc.ListChangelog("client", 1, 1, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
}

func TestChangelogService_ListChangelog_InvalidEntityType(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("", 1, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_type is required")
}

func TestChangelogService_ListChangelog_InvalidEntityID(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("client", 0, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_id must be positive")
}

func TestChangelogService_CreateBatch_NoOpForEmptySlice(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	require.NoError(t, repo.CreateBatch(nil))
	require.NoError(t, repo.CreateBatch([]changelog.Entry{}))
}

func TestChangelogService_CreateBatch_PersistsAll(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateBatch([]changelog.Entry{
		{EntityType: "client", EntityID: 1, Action: "update", ChangedBy: 1, ChangedAt: now},
		{EntityType: "client", EntityID: 1, Action: "update", ChangedBy: 1, ChangedAt: now.Add(1 * time.Second)},
	}))
	rows, total, err := svc.ListChangelog("client", 1, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)
}
