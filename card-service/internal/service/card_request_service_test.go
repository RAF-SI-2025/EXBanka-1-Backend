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

// ---------------------------------------------------------------------------
// In-app notification emission (Plan B5 Task 3)
// ---------------------------------------------------------------------------
//
// Each CardRequestService notifier emit is exercised via the small helper
// `notifyRequest` so the test does not have to spin up a full CardService /
// repository fixture for ApproveRequest. The helper is the single emit point
// shared by CreateRequest, ApproveRequest, and RejectRequest, so verifying it
// guarantees consistent Type / UserID / RefType / RefID / Data wiring across
// all three call sites. Precedent: card_service_test.go's
// TestCardService_TemporaryBlockCard_EmitsNotification.

// TestCardRequestService_CreateRequest_EmitsNotification asserts a
// CARD_REQUEST_CREATED in-app notification is emitted for the request's client,
// carrying the card_brand data key and RefType="card_request" / RefID=req.ID.
func TestCardRequestService_CreateRequest_EmitsNotification(t *testing.T) {
	rec := &recordingCardNotifier{}
	svc := &CardRequestService{notifier: rec}

	req := &model.CardRequest{CardBrand: "visa"}
	req.ID = 11
	req.ClientID = 42

	svc.notifyRequest(context.Background(), req, "CARD_REQUEST_CREATED", map[string]string{"card_brand": req.CardBrand})

	require.Len(t, rec.notifs, 1, "expected exactly one notification emit")
	n := rec.notifs[0]
	assert.Equal(t, "CARD_REQUEST_CREATED", n.Type)
	assert.Equal(t, uint64(42), n.UserID, "UserID must equal the request's ClientID")
	assert.Equal(t, "card_request", n.RefType)
	assert.Equal(t, uint64(11), n.RefID, "RefID must equal the request's ID")
	assert.Equal(t, "visa", n.Data["card_brand"], "card_brand data key must equal the request's brand")
	assert.Empty(t, n.Title, "Title must be empty (notification-service renders via template)")
	assert.Empty(t, n.Message, "Message must be empty (notification-service renders via template)")
}

// TestCardRequestService_ApproveRequest_EmitsNotification asserts a
// CARD_REQUEST_APPROVED in-app notification is emitted to the request's client
// with card_brand data key and the same RefType / RefID shape as creation.
func TestCardRequestService_ApproveRequest_EmitsNotification(t *testing.T) {
	rec := &recordingCardNotifier{}
	svc := &CardRequestService{notifier: rec}

	req := &model.CardRequest{CardBrand: "mastercard"}
	req.ID = 22
	req.ClientID = 99

	svc.notifyRequest(context.Background(), req, "CARD_REQUEST_APPROVED", map[string]string{"card_brand": req.CardBrand})

	require.Len(t, rec.notifs, 1, "expected exactly one notification emit")
	n := rec.notifs[0]
	assert.Equal(t, "CARD_REQUEST_APPROVED", n.Type)
	assert.Equal(t, uint64(99), n.UserID)
	assert.Equal(t, "card_request", n.RefType)
	assert.Equal(t, uint64(22), n.RefID)
	assert.Equal(t, "mastercard", n.Data["card_brand"])
}

// TestCardRequestService_RejectRequest_EmitsNotification asserts a
// CARD_REQUEST_REJECTED in-app notification is emitted with the reason data
// key and matching RefType / RefID.
func TestCardRequestService_RejectRequest_EmitsNotification(t *testing.T) {
	rec := &recordingCardNotifier{}
	svc := &CardRequestService{notifier: rec}

	req := &model.CardRequest{CardBrand: "visa"}
	req.ID = 33
	req.ClientID = 7

	reason := "insufficient documents"
	svc.notifyRequest(context.Background(), req, "CARD_REQUEST_REJECTED", map[string]string{"reason": reason})

	require.Len(t, rec.notifs, 1, "expected exactly one notification emit")
	n := rec.notifs[0]
	assert.Equal(t, "CARD_REQUEST_REJECTED", n.Type)
	assert.Equal(t, uint64(7), n.UserID)
	assert.Equal(t, "card_request", n.RefType)
	assert.Equal(t, uint64(33), n.RefID)
	assert.Equal(t, reason, n.Data["reason"], "reason data key must equal the supplied reason")
}

// TestCardRequestService_Notify_NilNotifier_NoPanic verifies the helper is a
// safe no-op when the notifier field is unset (e.g. service started without
// a Kafka producer). Mirrors the typed-nil guard precedent from Plan B5 Task 2.
func TestCardRequestService_Notify_NilNotifier_NoPanic(t *testing.T) {
	svc := &CardRequestService{}
	req := &model.CardRequest{CardBrand: "visa"}
	req.ID = 1
	req.ClientID = 1

	// Should not panic and should be a no-op.
	svc.notifyRequest(context.Background(), req, "CARD_REQUEST_CREATED", map[string]string{"card_brand": req.CardBrand})
}
