package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

// stubProducer returns a Producer that writes to a non-existent broker.
// kafka-go's Writer lazily dials, and errors from WriteMessages are discarded
// by all Publish* callers (via `_ =`), so this is safe and fast for tests.
func stubProducer() *kafkaprod.Producer {
	return kafkaprod.NewProducer("localhost:1")
}

// newRequestTestDB creates a per-test in-memory SQLite database with both
// the CardRequest and Card tables migrated.
func newRequestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CardRequest{}, &model.Card{}, &model.CardBlock{}, &model.AuthorizedPerson{}))
	return db
}

// seedRequest inserts a CardRequest with sensible defaults.
func seedRequest(t *testing.T, db *gorm.DB, overrides model.CardRequest) *model.CardRequest {
	t.Helper()
	req := overrides
	if req.ClientID == 0 {
		req.ClientID = 1
	}
	if req.AccountNumber == "" {
		req.AccountNumber = "265000000000000001"
	}
	if req.CardBrand == "" {
		req.CardBrand = "visa"
	}
	if req.CardType == "" {
		req.CardType = "debit"
	}
	if req.Status == "" {
		req.Status = "pending"
	}
	require.NoError(t, db.Create(&req).Error)
	return &req
}

// ---------------------------------------------------------------------------
// CreateRequest — status must be set to "pending"
// ---------------------------------------------------------------------------

func TestCreateRequest_StatusPending(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	req := &model.CardRequest{
		ClientID:      10,
		AccountNumber: "265000000000000010",
		CardBrand:     "visa",
	}

	err := svc.CreateRequest(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "pending", req.Status, "newly created request must be pending")
	assert.NotZero(t, req.ID, "ID must be assigned after creation")
	assert.Equal(t, "debit", req.CardType, "default card type must be debit")
}

func TestCreateRequest_InvalidBrand_Error(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	req := &model.CardRequest{
		ClientID:      10,
		AccountNumber: "265000000000000010",
		CardBrand:     "discover",
	}

	err := svc.CreateRequest(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid card brand")
}

// ---------------------------------------------------------------------------
// RejectRequest — status changes to "rejected"
// ---------------------------------------------------------------------------

func TestRejectRequest_StatusRejected(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{Status: "pending"})

	err := svc.RejectRequest(context.Background(), req.ID, 100, "insufficient documents")
	require.NoError(t, err)

	// Verify persisted status.
	updated, err := repo.GetByID(req.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status)
	assert.Equal(t, "insufficient documents", updated.Reason)
	assert.Equal(t, uint64(100), updated.ApprovedBy)
}

// ---------------------------------------------------------------------------
// ApproveRequest on already-rejected request — must error
// ---------------------------------------------------------------------------

func TestApproveRequest_AlreadyRejected_Error(t *testing.T) {
	db := newRequestTestDB(t)
	reqRepo := repository.NewCardRequestRepository(db)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)

	cardSvc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, authRepo: authRepo, db: db}
	svc := &CardRequestService{repo: reqRepo, cardSvc: cardSvc, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{Status: "rejected"})

	_, err := svc.ApproveRequest(context.Background(), req.ID, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already rejected")
}

// ---------------------------------------------------------------------------
// ApproveRequest on already-approved request — must error
// ---------------------------------------------------------------------------

func TestApproveRequest_AlreadyApproved_Error(t *testing.T) {
	db := newRequestTestDB(t)
	reqRepo := repository.NewCardRequestRepository(db)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)

	cardSvc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, authRepo: authRepo, db: db}
	svc := &CardRequestService{repo: reqRepo, cardSvc: cardSvc, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{Status: "approved"})

	_, err := svc.ApproveRequest(context.Background(), req.ID, 200)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already approved")
}

// ---------------------------------------------------------------------------
// RejectRequest on already-approved request — must error
// ---------------------------------------------------------------------------

func TestRejectRequest_AlreadyApproved_Error(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{Status: "approved"})

	err := svc.RejectRequest(context.Background(), req.ID, 200, "changed mind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already approved")
}

// ---------------------------------------------------------------------------
// GetRequest
// ---------------------------------------------------------------------------

func TestGetRequest_Found(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{
		ClientID:      42,
		AccountNumber: "265000000000000042",
		CardBrand:     "mastercard",
	})

	got, err := svc.GetRequest(req.ID)
	require.NoError(t, err)
	assert.Equal(t, req.ID, got.ID)
	assert.Equal(t, uint64(42), got.ClientID)
	assert.Equal(t, "mastercard", got.CardBrand)
}

func TestGetRequest_NotFound(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	_, err := svc.GetRequest(999)
	assert.Error(t, err, "GetRequest for non-existent ID must return error")
}
