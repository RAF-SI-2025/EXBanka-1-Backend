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

	clchangelog "github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/exbanka/credit-service/internal/service"
)

// newChangelogTestDB builds an in-memory SQLite DB with a manually created
// changelogs table (model.Changelog uses a Postgres-only `default:now()`
// clause that AutoMigrate cannot translate to SQLite).
func newChangelogHandlerDB(t *testing.T) *gorm.DB {
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
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)

	require.NoError(t, repo.Create(clchangelog.Entry{
		EntityType: "loan_request",
		EntityID:   42,
		Action:     "status_change",
		OldValue:   "pending",
		NewValue:   "approved",
		ChangedBy:  7,
		ChangedAt:  time.Now().UTC(),
		Reason:     "manual",
	}))

	h := &CreditGRPCHandler{changelogService: svc}
	resp, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "loan_request", EntityId: 42, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "loan_request", resp.Entries[0].EntityType)
	assert.Equal(t, int64(42), resp.Entries[0].EntityId)
	assert.Equal(t, "approved", resp.Entries[0].NewValue)
	assert.Equal(t, "manual", resp.Entries[0].Reason)
}

func TestHandler_ListChangelog_InvalidArgsMappedToInvalidArgument(t *testing.T) {
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &CreditGRPCHandler{changelogService: svc}

	_, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "", EntityId: 1, Page: 1, PageSize: 50,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestNewCreditGRPCHandler_Constructs verifies the constructor assigns every
// dependency without panicking.
func TestNewCreditGRPCHandler_Constructs(t *testing.T) {
	h := NewCreditGRPCHandler(nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, h)
}
