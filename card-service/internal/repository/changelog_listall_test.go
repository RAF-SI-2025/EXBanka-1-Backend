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
)

// newChangelogListAllDB opens an in-memory SQLite DB with the changelogs table
// created via raw DDL (the table is not a GORM model here), mirroring the
// fixture used by TestChangelogRepository_CRUD.
func newChangelogListAllDB(t *testing.T) *gorm.DB {
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

// TestChangelogRepository_ListAll exercises every optional filter branch
// (ActorID, Action, Since, Until) plus the no-filter and pagination paths of
// ListAll, asserting both the returned rows and the total count.
func TestChangelogRepository_ListAll(t *testing.T) {
	db := newChangelogListAllDB(t)
	r := NewChangelogRepository(db)

	// Local time keeps the stored value and the time.Unix-derived filter bound
	// in the same timezone so SQLite's string comparison is consistent.
	base := time.Now().Truncate(time.Second)
	seed := func(entity string, id, actor int64, action string) {
		require.NoError(t, r.Create(changelog.Entry{
			EntityType: entity, EntityID: id, Action: action,
			ChangedBy: actor, ChangedAt: base,
		}))
	}
	seed("card", 1, 1, "create")        // A
	seed("card", 2, 2, "block")         // B
	seed("card_request", 3, 1, "block") // C

	// No filters -> all three, ordered DESC.
	rows, total, err := r.ListAll(ChangelogFilters{}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)

	// Filter by ActorID -> A and C.
	rows, total, err = r.ListAll(ChangelogFilters{ActorID: 1}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, int64(1), row.ChangedBy)
	}

	// Filter by Action -> B and C.
	rows, total, err = r.ListAll(ChangelogFilters{Action: "block"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, "block", row.Action)
	}

	// Combined ActorID + Action -> only C.
	rows, total, err = r.ListAll(ChangelogFilters{ActorID: 1, Action: "block"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "card_request", rows[0].EntityType)

	// Since + Until window spanning base -> all three (exercises both bounds).
	rows, total, err = r.ListAll(ChangelogFilters{
		Since: base.Add(-24 * time.Hour).Unix(),
		Until: base.Add(24 * time.Hour).Unix(),
	}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)

	// Pagination: pageSize 2 returns 2 rows but total stays 3.
	rows, total, err = r.ListAll(ChangelogFilters{}, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 2)
}
