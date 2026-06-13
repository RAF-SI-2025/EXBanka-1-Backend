package service

import (
	"context"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"
	"time"

	gosqlite "github.com/glebarez/go-sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

// init registers SQLite stand-ins for the two PostgreSQL-only functions that
// CreateCard relies on for its serialize-on-account advisory lock:
//
//	SELECT pg_advisory_xact_lock(hashtext(?))
//
// Without these the statement errors with "no such function" on the in-memory
// SQLite test driver, which is why the original CreateCard limit test only
// exercised the repository-level count. With deterministic no-op shims the
// full CreateCard transaction (count check + INSERT) runs under SQLite so the
// service path can be tested directly. The shims are registered on the
// glebarez/go-sqlite driver that the GORM dialector uses; registration happens
// once at package-test init, before any connection is opened, and is global to
// the test binary. The names never collide with real columns/functions, so no
// other test is affected.
func init() {
	_ = gosqlite.RegisterDeterministicScalarFunction("hashtext", 1,
		func(_ *gosqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) == 0 {
				return int64(0), nil
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(fmt.Sprintf("%v", args[0])))
			return int64(h.Sum32()), nil
		})
	_ = gosqlite.RegisterDeterministicScalarFunction("pg_advisory_xact_lock", 1,
		func(_ *gosqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
			return int64(1), nil
		})
}

// ---------------------------------------------------------------------------
// CreateCard — happy path
// ---------------------------------------------------------------------------

func TestCreateCard_Success(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	card, cvv, err := svc.CreateCard(context.Background(), "265000000000000111", 7, "client", "visa")
	require.NoError(t, err)
	require.NotNil(t, card)

	assert.Equal(t, "265000000000000111", card.AccountNumber)
	assert.Equal(t, uint64(7), card.OwnerID)
	assert.Equal(t, "client", card.OwnerType)
	assert.Equal(t, "visa", card.CardBrand)
	assert.Equal(t, "debit", card.CardType)
	assert.Equal(t, "active", card.Status)
	assert.False(t, card.IsVirtual)
	assert.Len(t, cvv, 3, "CVV must be returned to the caller (3 digits)")
	// The persisted CardNumber must be masked; the full PAN is stored separately.
	assert.True(t, strings.Contains(card.CardNumber, "*"), "stored card number must be masked")
	assert.Len(t, card.CardNumberFull, 16, "visa PAN must be 16 digits")
	assert.True(t, LuhnCheck(card.CardNumberFull), "generated PAN must be Luhn-valid")
	// Expiry ~3 years out.
	assert.WithinDuration(t, time.Now().AddDate(3, 0, 0), card.ExpiresAt, 24*time.Hour)

	// Persisted in DB.
	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, "active", persisted.Status)
	assert.Equal(t, card.CardNumberFull, persisted.CardNumberFull)
}

// ---------------------------------------------------------------------------
// CreateCard — validation rejections
// ---------------------------------------------------------------------------

func TestCreateCard_InvalidBrand(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	_, _, err := svc.CreateCard(context.Background(), "265000000000000111", 1, "client", "discover")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCard)
	assert.Contains(t, err.Error(), "card brand must be one of")
}

func TestCreateCard_InvalidOwnerType(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	_, _, err := svc.CreateCard(context.Background(), "265000000000000111", 1, "robot", "visa")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCard)
	assert.Contains(t, err.Error(), "owner type must be one of")
}

// ---------------------------------------------------------------------------
// CreateCard — personal-account limit (max 2 active cards)
// ---------------------------------------------------------------------------

func TestCreateCard_PersonalAccount_ThirdRejected(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	acct := "265000000000000222"

	for i := 0; i < 2; i++ {
		_, _, err := svc.CreateCard(context.Background(), acct, 1, "client", "visa")
		require.NoError(t, err, "first two cards must succeed")
	}

	_, _, err := svc.CreateCard(context.Background(), acct, 1, "client", "visa")
	require.Error(t, err, "third card on a personal account must be rejected")
	assert.ErrorIs(t, err, ErrCardLimitReached)

	// Exactly two active cards persisted.
	cards, err := svc.ListCardsByAccount(acct)
	require.NoError(t, err)
	assert.Len(t, cards, 2)
}

// TestCreateCard_DeactivatedDoesNotCountTowardLimit verifies a deactivated card
// frees a slot: with one active + one deactivated card a new card is allowed.
func TestCreateCard_DeactivatedDoesNotCountTowardLimit(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	acct := "265000000000000333"

	seedCard(t, db, model.Card{
		CardNumber: "4111XXXXXXXX5001", CardNumberFull: "4111111111115001",
		AccountNumber: acct, OwnerID: 1, OwnerType: "client", Status: "active",
	})
	seedCard(t, db, model.Card{
		CardNumber: "4111XXXXXXXX5002", CardNumberFull: "4111111111115002",
		AccountNumber: acct, OwnerID: 1, OwnerType: "client", Status: "deactivated",
	})

	// Only one active card exists, so a new one is permitted.
	_, _, err := svc.CreateCard(context.Background(), acct, 1, "client", "mastercard")
	require.NoError(t, err, "deactivated cards must not consume the per-account slot")
}

// ---------------------------------------------------------------------------
// CreateCard — business-account (authorized_person) limit (max 1 per person)
// ---------------------------------------------------------------------------

func TestCreateCard_AuthorizedPerson_SecondRejected(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	acct := "265000000000000444"

	_, _, err := svc.CreateCard(context.Background(), acct, 50, "authorized_person", "visa")
	require.NoError(t, err, "first card for the person must succeed")

	_, _, err = svc.CreateCard(context.Background(), acct, 50, "authorized_person", "visa")
	require.Error(t, err, "second card for the same person on a business account must be rejected")
	assert.ErrorIs(t, err, ErrCardLimitReached)

	// A different person on the same account is allowed their own card.
	_, _, err = svc.CreateCard(context.Background(), acct, 51, "authorized_person", "visa")
	require.NoError(t, err, "a different authorized person may hold their own card")
}

// ---------------------------------------------------------------------------
// ApproveRequest — success path (creates a card, flips request to approved)
// ---------------------------------------------------------------------------

func TestApproveRequest_Success(t *testing.T) {
	db := newRequestTestDB(t)
	reqRepo := repository.NewCardRequestRepository(db)
	cardRepo := repository.NewCardRepository(db)
	cardSvc := &CardService{cardRepo: cardRepo, db: db}
	svc := &CardRequestService{repo: reqRepo, cardSvc: cardSvc, producer: stubProducer()}

	req := seedRequest(t, db, model.CardRequest{
		ClientID: 9, AccountNumber: "265000000000000555", CardBrand: "mastercard", Status: "pending",
	})

	card, err := svc.ApproveRequest(context.Background(), req.ID, 1234)
	require.NoError(t, err)
	require.NotNil(t, card)
	assert.Equal(t, "265000000000000555", card.AccountNumber)
	assert.Equal(t, uint64(9), card.OwnerID)
	assert.Equal(t, "client", card.OwnerType)
	assert.Equal(t, "mastercard", card.CardBrand)
	assert.Equal(t, "active", card.Status)

	// Request flipped to approved and stamped with the approving employee.
	updated, err := reqRepo.GetByID(req.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status)
	assert.Equal(t, uint64(1234), updated.ApprovedBy)

	// The created card is persisted and discoverable by account.
	cards, err := cardRepo.ListByAccount("265000000000000555")
	require.NoError(t, err)
	assert.Len(t, cards, 1)
}

// TestApproveRequest_NotFound covers the GetByID error -> ErrCardRequestNotFound
// translation branch.
func TestApproveRequest_NotFound(t *testing.T) {
	db := newRequestTestDB(t)
	reqRepo := repository.NewCardRequestRepository(db)
	cardSvc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	svc := &CardRequestService{repo: reqRepo, cardSvc: cardSvc, producer: stubProducer()}

	_, err := svc.ApproveRequest(context.Background(), 99999, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCardRequestNotFound)
}
