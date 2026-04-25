package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

// ---------------------------------------------------------------------------
// CreateVirtualCard — validation paths
// ---------------------------------------------------------------------------

func TestCreateVirtualCard_InvalidUsageType(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	_, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "bogus", 1, 1, "1000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage_type must be one of: single_use, multi_use")
}

func TestCreateVirtualCard_InvalidExpiry(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	_, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "single_use", 1, 5, "1000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expiry_months must be between 1 and 3")
}

func TestCreateVirtualCard_MultiUseLowMax(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	_, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "multi_use", 1, 1, "1000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multi_use cards must have max_uses >= 2")
}

func TestCreateVirtualCard_InvalidLimit(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	_, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "single_use", 1, 1, "-100")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit must be a positive number")
}

func TestCreateVirtualCard_InvalidBrand(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	_, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "discover", "single_use", 1, 1, "1000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "card brand must be one of: visa, mastercard, dinacard, amex")
}

func TestCreateVirtualCard_SingleUse_Success(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	card, cvv, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "single_use", 99, 1, "5000")
	require.NoError(t, err)
	assert.True(t, card.IsVirtual)
	assert.Equal(t, "single_use", card.UsageType)
	// single_use forces MaxUses to 1 regardless of input.
	assert.Equal(t, 1, card.MaxUses)
	assert.Equal(t, 1, card.UsesRemaining)
	assert.NotEmpty(t, cvv)
}

func TestCreateVirtualCard_MultiUse_Success(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, producer: stubProducer(), db: db}

	card, _, err := svc.CreateVirtualCard(context.Background(), "265000000000000001", 1, "visa", "multi_use", 5, 2, "1000")
	require.NoError(t, err)
	assert.Equal(t, 5, card.MaxUses)
	assert.Equal(t, 5, card.UsesRemaining)
}

// ---------------------------------------------------------------------------
// SetPin — validation and happy path
// ---------------------------------------------------------------------------

func TestSetPin_InvalidFormat(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{})
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	err := svc.SetPin(card.ID, "abc")
	require.Error(t, err)
	assert.Equal(t, "PIN must be exactly 4 digits", err.Error())
}

func TestSetPin_NotFound(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	err := svc.SetPin(9999, "1234")
	require.Error(t, err)
}

func TestSetPin_Success(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{PinAttempts: 2})
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	require.NoError(t, svc.SetPin(card.ID, "1234"))

	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.NotEmpty(t, persisted.PinHash)
	assert.Equal(t, 0, persisted.PinAttempts, "PinAttempts must reset on SetPin")
	// Verify the stored hash matches.
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(persisted.PinHash), []byte("1234")))
}

// ---------------------------------------------------------------------------
// VerifyPin — wrong pin, then correct pin resets attempts
// ---------------------------------------------------------------------------

func TestVerifyPin_CorrectPinResetsAttempts(t *testing.T) {
	db := newCardTestDB(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("1234"), bcrypt.MinCost)
	require.NoError(t, err)
	card := seedCard(t, db, model.Card{PinHash: string(hash), PinAttempts: 1})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	ok, err := svc.VerifyPin(card.ID, "1234")
	require.NoError(t, err)
	assert.True(t, ok)

	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, 0, persisted.PinAttempts, "successful verify must reset attempts")
}

// ---------------------------------------------------------------------------
// TemporaryBlockCard — validation and happy path
// ---------------------------------------------------------------------------

func TestTemporaryBlockCard_InvalidDuration(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	svc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, producer: stubProducer(), db: db}

	_, err := svc.TemporaryBlockCard(context.Background(), 1, 0, "lost")
	require.Error(t, err)
	assert.Equal(t, "duration_hours must be between 1 and 720", err.Error())
}

func TestTemporaryBlockCard_TooLong(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	svc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, producer: stubProducer(), db: db}

	_, err := svc.TemporaryBlockCard(context.Background(), 1, 1000, "lost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration_hours must be between 1 and 720")
}

func TestTemporaryBlockCard_Success(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	svc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, producer: stubProducer(), db: db}

	got, err := svc.TemporaryBlockCard(context.Background(), card.ID, 24, "lost wallet")
	require.NoError(t, err)
	assert.Equal(t, "blocked", got.Status)

	// Verify a block row was created.
	var block model.CardBlock
	require.NoError(t, db.Where("card_id = ?", card.ID).First(&block).Error)
	assert.Equal(t, "lost wallet", block.Reason)
	assert.True(t, block.Active)
	require.NotNil(t, block.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), *block.ExpiresAt, 5*time.Second)
}

// ---------------------------------------------------------------------------
// CreateAuthorizedPerson + GetAuthorizedPerson
// ---------------------------------------------------------------------------

func TestCreateAndGetAuthorizedPerson(t *testing.T) {
	db := newCardTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthorizedPerson{}))
	authRepo := repository.NewAuthorizedPersonRepository(db)
	svc := &CardService{authRepo: authRepo, db: db}

	ap := &model.AuthorizedPerson{
		FirstName: "Alice", LastName: "Smith",
		DateOfBirth: time.Now().Add(-30 * 365 * 24 * time.Hour).Unix(),
		Email:       "a@b.c", AccountID: 1,
	}
	require.NoError(t, svc.CreateAuthorizedPerson(context.Background(), ap))
	assert.NotZero(t, ap.ID)

	got, err := svc.GetAuthorizedPerson(ap.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.FirstName)
}

// ---------------------------------------------------------------------------
// CreateRequest — Kafka publish path; ListRequests + ListByClient
// ---------------------------------------------------------------------------

func TestListRequests_FiltersByStatus(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	seedRequest(t, db, model.CardRequest{Status: "pending"})
	seedRequest(t, db, model.CardRequest{Status: "pending"})
	seedRequest(t, db, model.CardRequest{Status: "approved"})

	pending, total, err := svc.ListRequests("pending", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, pending, 2)

	all, total, err := svc.ListRequests("", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, all, 3)
}

func TestListByClient_FiltersByClientID(t *testing.T) {
	db := newRequestTestDB(t)
	repo := repository.NewCardRequestRepository(db)
	svc := &CardRequestService{repo: repo, producer: stubProducer()}

	seedRequest(t, db, model.CardRequest{ClientID: 1})
	seedRequest(t, db, model.CardRequest{ClientID: 1})
	seedRequest(t, db, model.CardRequest{ClientID: 2})

	got, total, err := svc.ListByClient(1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, got, 2)
}

// ---------------------------------------------------------------------------
// MaskCardNumber edge cases
// ---------------------------------------------------------------------------

func TestMaskCardNumber_ShortString(t *testing.T) {
	// len < 8 returns the input unchanged.
	assert.Equal(t, "1234", MaskCardNumber("1234"))
	assert.Equal(t, "1234567", MaskCardNumber("1234567"))
}

func TestMaskCardNumber_Long(t *testing.T) {
	assert.Equal(t, "4111********1111", MaskCardNumber("4111111111111111"))
}

// ---------------------------------------------------------------------------
// runCardCronTick — unblock expired blocks and deactivate expired virtual cards
// ---------------------------------------------------------------------------

func TestRunCardCronTick_UnblocksExpiredBlocks(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "blocked"})

	past := time.Now().Add(-1 * time.Hour)
	require.NoError(t, db.Create(&model.CardBlock{
		CardID:    card.ID,
		Reason:    "lost",
		BlockedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: &past,
		Active:    true,
	}).Error)

	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)

	runCardCronTick(context.Background(), cardRepo, blockRepo, db)

	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, "active", persisted.Status, "expired block must unblock the card")

	var block model.CardBlock
	require.NoError(t, db.Where("card_id = ?", card.ID).First(&block).Error)
	assert.False(t, block.Active, "block must be marked inactive")
}

func TestRunCardCronTick_DeactivatesExpiredVirtualCards(t *testing.T) {
	db := newCardTestDB(t)
	expired := seedCard(t, db, model.Card{
		IsVirtual: true,
		Status:    "active",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	notExpired := seedCard(t, db, model.Card{
		CardNumber:     "411222XXXXXX9999",
		CardNumberFull: "4112222222229999",
		IsVirtual:      true,
		Status:         "active",
		ExpiresAt:      time.Now().Add(48 * time.Hour),
	})

	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)

	runCardCronTick(context.Background(), cardRepo, blockRepo, db)

	var p1, p2 model.Card
	require.NoError(t, db.First(&p1, expired.ID).Error)
	require.NoError(t, db.First(&p2, notExpired.ID).Error)
	assert.Equal(t, "deactivated", p1.Status, "expired virtual card must be deactivated")
	assert.Equal(t, "active", p2.Status, "non-expired virtual card must stay active")
}
