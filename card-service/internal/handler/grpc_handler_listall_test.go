package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
	clchangelog "github.com/exbanka/contract/changelog"
)

// TestHandler_ListAllChangelogs_ReturnsEntries verifies the global changelog
// handler maps repository rows to proto entries and echoes pagination back.
func TestHandler_ListAllChangelogs_ReturnsEntries(t *testing.T) {
	db := newCardChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "card", EntityID: 1, Action: "block",
		OldValue: "active", NewValue: "blocked", ChangedBy: 7,
		ChangedAt: now,
	}))
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "card_request", EntityID: 2, Action: "approve",
		ChangedBy: 8, ChangedAt: now,
	}))

	h := &CardGRPCHandler{changelogService: svc}
	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Equal(t, int32(1), resp.Page)
	assert.Equal(t, int32(50), resp.PageSize)
	require.Len(t, resp.Entries, 2)
}

// TestHandler_ListAllChangelogs_FilterByAction exercises the filter wiring
// (Action/ActorId) from request into the repository query.
func TestHandler_ListAllChangelogs_FilterByAction(t *testing.T) {
	db := newCardChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "card", EntityID: 1, Action: "block", ChangedBy: 7, ChangedAt: now,
	}))
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "card", EntityID: 2, Action: "unblock", ChangedBy: 7, ChangedAt: now,
	}))

	h := &CardGRPCHandler{changelogService: svc}
	resp, err := h.ListAllChangelogs(context.Background(), &pb.ListAllChangelogsRequest{
		Page: 1, PageSize: 50, Action: "unblock",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "unblock", resp.Entries[0].Action)
	assert.Equal(t, "card", resp.Entries[0].EntityType)
}
