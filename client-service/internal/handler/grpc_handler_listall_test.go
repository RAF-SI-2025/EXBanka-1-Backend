package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/client-service/internal/service"
	clchangelog "github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/clientpb"
)

func TestHandler_ListAllChangelogs_ReturnsMappedEntries(t *testing.T) {
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "client", EntityID: 7, Action: "update",
		FieldName: "first_name", OldValue: "old", NewValue: "new",
		ChangedBy: 3, ChangedAt: now, Reason: "manual",
	}))
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "client_limit", EntityID: 9, Action: "update",
		ChangedBy: 4, ChangedAt: now.Add(-time.Hour),
	}))

	h := &ClientGRPCHandler{changelogService: svc}
	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Equal(t, int32(1), resp.Page)
	assert.Equal(t, int32(50), resp.PageSize)
	require.Len(t, resp.Entries, 2)

	// Newest (EntityID=7) first; verify field mapping is faithful.
	e := resp.Entries[0]
	assert.Equal(t, "client", e.EntityType)
	assert.Equal(t, int64(7), e.EntityId)
	assert.Equal(t, "update", e.Action)
	assert.Equal(t, "first_name", e.FieldName)
	assert.Equal(t, "old", e.OldValue)
	assert.Equal(t, "new", e.NewValue)
	assert.Equal(t, int64(3), e.ChangedBy)
	assert.Equal(t, now.Unix(), e.ChangedAt)
	assert.Equal(t, "manual", e.Reason)
}

func TestHandler_ListAllChangelogs_ActorFilterForwarded(t *testing.T) {
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "client", EntityID: 1, Action: "create", ChangedBy: 1, ChangedAt: now,
	}))
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "client", EntityID: 2, Action: "update", ChangedBy: 2, ChangedAt: now,
	}))

	h := &ClientGRPCHandler{changelogService: svc}
	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 50, ActorId: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, int64(2), resp.Entries[0].ChangedBy)
}

func TestHandler_ListAllChangelogs_RepoError_ReturnsInternal(t *testing.T) {
	// DB without the changelogs table → the underlying Count fails, so the
	// service surfaces an error that the handler maps to codes.Internal.
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &ClientGRPCHandler{changelogService: svc}

	_, err = h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 50,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
