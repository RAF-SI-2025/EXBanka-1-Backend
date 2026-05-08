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

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
	clchangelog "github.com/exbanka/contract/changelog"
)

func newCardChangelogHandlerDB(t *testing.T) *gorm.DB {
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

func TestHandler_ListChangelog_ReturnsEntries(t *testing.T) {
	db := newCardChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "card", EntityID: 1, Action: "block",
		OldValue: "active", NewValue: "blocked", ChangedBy: 1,
		ChangedAt: time.Now().UTC(),
	}))

	h := &CardGRPCHandler{changelogService: svc}
	resp, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "card", EntityId: 1, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "blocked", resp.Entries[0].NewValue)
}

func TestHandler_ListChangelog_InvalidArgumentMapped(t *testing.T) {
	db := newCardChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &CardGRPCHandler{changelogService: svc}

	_, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "", EntityId: 1, Page: 1, PageSize: 50,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Constructor smoke tests — verify each NewXGRPCHandler returns non-nil.
func TestNewCardGRPCHandler_Constructs(t *testing.T) {
	h := NewCardGRPCHandler(nil, nil, nil, nil)
	require.NotNil(t, h)
}

func TestNewCardRequestGRPCHandler_Constructs(t *testing.T) {
	h := NewCardRequestGRPCHandler(nil)
	require.NotNil(t, h)
}

func TestNewVirtualCardGRPCHandler_Constructs(t *testing.T) {
	h := NewVirtualCardGRPCHandler(nil)
	require.NotNil(t, h)
}
