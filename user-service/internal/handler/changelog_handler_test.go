// changelog_handler_test.go — success-path coverage for the gRPC changelog
// read RPCs (ListChangelog row mapping + ListAllChangelogs), backed by a
// real ChangelogService over an in-memory SQLite DB. The model declares a
// PostgreSQL-only default:now() clause, so the changelogs table is created
// by hand here instead of via AutoMigrate.
package handler

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newChangelogHandlerDB(t *testing.T) *gorm.DB {
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

func seedChangelogHandler(t *testing.T) *UserGRPCHandler {
	t.Helper()
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	now := time.Now()
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "employee", EntityID: 1, Action: "update", FieldName: "last_name",
		OldValue: "Old", NewValue: "New", ChangedBy: 7, ChangedAt: now, Reason: "correction",
	}))
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "limit", EntityID: 2, Action: "create", ChangedBy: 9, ChangedAt: now.Add(time.Second),
	}))
	return &UserGRPCHandler{changelogService: service.NewChangelogService(repo)}
}

func TestListChangelog_HappyPathMapsFields(t *testing.T) {
	h := seedChangelogHandler(t)

	resp, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "employee", EntityId: 1, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)

	e := resp.Entries[0]
	assert.Equal(t, "employee", e.EntityType)
	assert.Equal(t, int64(1), e.EntityId)
	assert.Equal(t, "update", e.Action)
	assert.Equal(t, "last_name", e.FieldName)
	assert.Equal(t, "Old", e.OldValue)
	assert.Equal(t, "New", e.NewValue)
	assert.Equal(t, int64(7), e.ChangedBy)
	assert.Equal(t, "correction", e.Reason)
	assert.NotZero(t, e.ChangedAt)
}

func TestListAllChangelogs_HappyPath(t *testing.T) {
	h := seedChangelogHandler(t)

	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Len(t, resp.Entries, 2)
	assert.Equal(t, int32(1), resp.Page)
	assert.Equal(t, int32(10), resp.PageSize)
}

func TestListAllChangelogs_ActorFilter(t *testing.T) {
	h := seedChangelogHandler(t)

	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 10, ActorId: 9,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, int64(9), resp.Entries[0].ChangedBy)
}
