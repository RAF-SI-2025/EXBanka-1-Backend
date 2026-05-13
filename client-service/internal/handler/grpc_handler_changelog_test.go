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

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/client-service/internal/service"
	clchangelog "github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/clientpb"
)

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
		EntityType: "client", EntityID: 7, Action: "update",
		OldValue: "old", NewValue: "new",
		ChangedBy: 1, ChangedAt: time.Now().UTC(),
	}))

	h := &ClientGRPCHandler{changelogService: svc}
	resp, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "client", EntityId: 7, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "client", resp.Entries[0].EntityType)
	assert.Equal(t, int64(7), resp.Entries[0].EntityId)
}

func TestHandler_ListChangelog_InvalidArgument(t *testing.T) {
	db := newChangelogHandlerDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &ClientGRPCHandler{changelogService: svc}

	_, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "", EntityId: 1, Page: 1, PageSize: 50,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNewClientGRPCHandler_Constructs(t *testing.T) {
	h := NewClientGRPCHandler(nil, nil)
	require.NotNil(t, h)
}

func TestNewClientLimitGRPCHandler_Constructs(t *testing.T) {
	h := NewClientLimitGRPCHandler(nil)
	require.NotNil(t, h)
}

// TestUpdateClient_AllOptionalFieldsSupplied exercises every nil-check
// branch in UpdateClient that maps protobuf optional pointers to the
// service-layer updates map.
func TestUpdateClient_AllOptionalFieldsSupplied(t *testing.T) {
	h, stub := newTestClientHandler()
	captured := map[string]interface{}{}
	stub.updateFn = func(id uint64, updates map[string]interface{}, _ int64) (*model.Client, error) {
		for k, v := range updates {
			captured[k] = v
		}
		return sampleClient(id), nil
	}

	firstName := "F"
	lastName := "L"
	dob := int64(123)
	gender := "male"
	email := "x@y.z"
	phone := "+38166"
	address := "addr"

	_, err := h.UpdateClient(context.Background(), &pb.UpdateClientRequest{
		Id:          10,
		FirstName:   &firstName,
		LastName:    &lastName,
		DateOfBirth: &dob,
		Gender:      &gender,
		Email:       &email,
		Phone:       &phone,
		Address:     &address,
	})
	require.NoError(t, err)

	for _, k := range []string{"first_name", "last_name", "date_of_birth", "gender", "email", "phone", "address"} {
		assert.Contains(t, captured, k, "every supplied optional field must be forwarded")
	}
}
