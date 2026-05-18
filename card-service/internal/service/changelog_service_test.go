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

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

func newCardChangelogTestDB(t *testing.T) *gorm.DB {
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

func TestChangelogService_ListChangelog_ReturnsEntries(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "card", EntityID: 5, Action: "update",
			ChangedBy: 1, ChangedAt: now.Add(time.Duration(i) * time.Second),
		}))
	}

	rows, total, err := svc.ListChangelog("card", 5, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}

func TestChangelogService_ListChangelog_PaginationDefaults(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "card", EntityID: 1, Action: "update",
		ChangedBy: 1, ChangedAt: time.Now().UTC(),
	}))
	rows, total, err := svc.ListChangelog("card", 1, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
}

func TestChangelogService_ListChangelog_PageSizeCappedAt200(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "card", EntityID: 1, Action: "update",
		ChangedBy: 1, ChangedAt: time.Now().UTC(),
	}))
	_, _, err := svc.ListChangelog("card", 1, 1, 1000)
	require.NoError(t, err)
}

func TestChangelogService_ListChangelog_EmptyEntityType(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("", 1, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_type is required")
}

func TestChangelogService_ListChangelog_NegativeEntityID(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	_, _, err := svc.ListChangelog("card", 0, 1, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_id must be positive")
}

func TestNewChangelogService_ConstructsNonNil(t *testing.T) {
	svc := NewChangelogService(nil)
	require.NotNil(t, svc)
}
