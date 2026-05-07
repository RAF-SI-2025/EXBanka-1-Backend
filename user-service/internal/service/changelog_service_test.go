// changelog_service_test.go
//
// ChangelogService is a thin wrapper around the concrete
// *repository.ChangelogRepository, so the simplest way to test it without
// changing production code is to back the repository with an in-memory
// SQLite DB. The Changelog model declares `default:now()`, which SQLite
// can't compile, so we hand-create the table here instead of relying on
// AutoMigrate.
package service_test

import (
	"testing"
	"time"

	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newChangelogTestDB returns a sqlite-backed gorm DB with a manually-created
// `changelogs` table that works around the model's PostgreSQL-only
// `default:now()` clause.
func newChangelogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id BIGINT NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by BIGINT NOT NULL,
		changed_at DATETIME NOT NULL,
		reason TEXT
	)`).Error)
	return db
}

func TestChangelogService_RejectsBadInput(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	_, _, err := svc.ListChangelog("", 1, 1, 10)
	assert.Error(t, err, "empty entity_type must error")

	_, _, err = svc.ListChangelog("employee", 0, 1, 10)
	assert.Error(t, err, "non-positive entity_id must error")

	_, _, err = svc.ListChangelog("employee", -1, 1, 10)
	assert.Error(t, err, "negative entity_id must error")
}

func TestChangelogService_PaginationDefaultsAndCap(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	now := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "employee",
			EntityID:   42,
			Action:     "update",
			FieldName:  "field",
			ChangedAt:  now.Add(time.Duration(i) * time.Second),
		}))
	}

	// page <1 normalizes to 1, pageSize <=0 normalizes to default 20.
	rows, total, err := svc.ListChangelog("employee", 42, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 5)

	// pageSize > 200 caps at 200 — exercising the cap branch.
	rows, total, err = svc.ListChangelog("employee", 42, 1, 999)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 5)

	// Second page returns no rows but still succeeds.
	rows, _, err = svc.ListChangelog("employee", 42, 2, 5)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestChangelogService_FiltersByEntity(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	now := time.Now()
	require.NoError(t, repo.Create(changelog.Entry{EntityType: "employee", EntityID: 1, Action: "create", ChangedAt: now}))
	require.NoError(t, repo.Create(changelog.Entry{EntityType: "employee", EntityID: 2, Action: "create", ChangedAt: now}))
	require.NoError(t, repo.Create(changelog.Entry{EntityType: "limit", EntityID: 1, Action: "create", ChangedAt: now}))

	rows, total, err := svc.ListChangelog("employee", 1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "employee", rows[0].EntityType)
	assert.Equal(t, int64(1), rows[0].EntityID)
}
