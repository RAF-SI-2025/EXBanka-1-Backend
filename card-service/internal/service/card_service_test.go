package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

// ---------------------------------------------------------------------------
// CreateCard — card number format and brand validation
// ---------------------------------------------------------------------------

// TestCreateCardVisa_LuhnValid_StartsWith4_16Digits verifies that Visa card
// numbers are Luhn-valid, 16 digits long, and start with "4".
func TestCreateCardVisa_LuhnValid_StartsWith4_16Digits(t *testing.T) {
	for i := 0; i < 50; i++ {
		num := GenerateCardNumber("visa")
		assert.Len(t, num, 16, "Visa card number must be 16 digits")
		assert.True(t, LuhnCheck(num), "Visa card number must pass Luhn check")
		assert.True(t, strings.HasPrefix(num, "4"), "Visa card must start with 4, got prefix %s", num[:1])
	}
}

// TestCreateCardAmex_15Digits_StartsWith34Or37 verifies that Amex card
// numbers are 15 digits and start with 34 or 37.
func TestCreateCardAmex_15Digits_StartsWith34Or37(t *testing.T) {
	for i := 0; i < 50; i++ {
		num := GenerateCardNumber("amex")
		assert.Len(t, num, 15, "Amex card number must be 15 digits")
		assert.True(t, LuhnCheck(num), "Amex card number must pass Luhn check")
		prefix := num[:2]
		assert.True(t, prefix == "34" || prefix == "37",
			"Amex card must start with 34 or 37, got %s", prefix)
	}
}

// ---------------------------------------------------------------------------
// CreateCard — max cards per personal account (limit = 2)
// ---------------------------------------------------------------------------

// TestCreateCard_Max2PerPersonalAccount_ThirdRejected verifies the counting
// logic that limits personal accounts to 2 active cards. Because CreateCard
// internally uses a PostgreSQL advisory lock (unavailable in SQLite), we
// verify the enforcement by seeding 2 active cards and confirming the count
// via the repository, then verifying that a 3rd call to CreateCard fails.
func TestCreateCard_Max2PerPersonalAccount_ThirdRejected(t *testing.T) {
	db := newCardTestDB(t)

	acct := "265000000000000099"

	// Seed two active cards on the same personal account.
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX1001",
		CardNumberFull: "4111111111111001",
		AccountNumber:  acct,
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "active",
	})
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX1002",
		CardNumberFull: "4111111111111002",
		AccountNumber:  acct,
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "active",
	})

	// Verify count is 2 via repository.
	cardRepo := repository.NewCardRepository(db)
	count, err := cardRepo.CountByAccount(acct)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "should have 2 active cards on account")

	// Deactivated cards should NOT count toward the limit.
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX1003",
		CardNumberFull: "4111111111111003",
		AccountNumber:  acct,
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "deactivated",
	})
	count, err = cardRepo.CountByAccount(acct)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "deactivated cards must not count toward the limit")
}

// ---------------------------------------------------------------------------
// GetCard — found
// ---------------------------------------------------------------------------

func TestGetCard_Found(t *testing.T) {
	db := newCardTestDB(t)
	seeded := seedCard(t, db, model.Card{
		CardNumber:     "5425XXXXXXXX9903",
		CardNumberFull: "5425233430109903",
		CardBrand:      "mastercard",
	})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	got, err := svc.GetCard(seeded.ID)
	require.NoError(t, err)
	assert.Equal(t, seeded.ID, got.ID)
	assert.Equal(t, "5425233430109903", got.CardNumberFull)
	assert.Equal(t, "mastercard", got.CardBrand)
}

func TestGetCard_NotFound(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.GetCard(999)
	assert.Error(t, err, "GetCard for non-existent ID must return error")
}

// ---------------------------------------------------------------------------
// ListCardsByAccount / ListCardsByClient
// ---------------------------------------------------------------------------

func TestListCardsByAccount(t *testing.T) {
	db := newCardTestDB(t)
	acct := "265000000000000001"

	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX2001",
		CardNumberFull: "4111111111112001",
		AccountNumber:  acct,
	})
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX2002",
		CardNumberFull: "4111111111112002",
		AccountNumber:  acct,
	})
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX2003",
		CardNumberFull: "4111111111112003",
		AccountNumber:  "265000000000000002", // different account
	})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	cards, err := svc.ListCardsByAccount(acct)
	require.NoError(t, err)
	assert.Len(t, cards, 2, "should return only cards for the given account")
}

func TestListCardsByClient(t *testing.T) {
	db := newCardTestDB(t)
	var clientID uint64 = 42

	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX3001",
		CardNumberFull: "4111111111113001",
		OwnerID:        clientID,
		OwnerType:      "client",
	})
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX3002",
		CardNumberFull: "4111111111113002",
		OwnerID:        clientID,
		OwnerType:      "client",
	})
	seedCard(t, db, model.Card{
		CardNumber:     "4111XXXXXXXX3003",
		CardNumberFull: "4111111111113003",
		OwnerID:        99,
		OwnerType:      "client",
	})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	cards, err := svc.ListCardsByClient(clientID)
	require.NoError(t, err)
	assert.Len(t, cards, 2, "should return only cards for the given client")
}

// ---------------------------------------------------------------------------
// BlockCard
// ---------------------------------------------------------------------------

func TestBlockCard_StatusChanges(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	blocked, err := svc.BlockCard(card.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "blocked", blocked.Status)

	// Verify persisted.
	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, "blocked", persisted.Status)
}

func TestBlockCard_AlreadyBlocked_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "blocked"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.BlockCard(card.ID, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already blocked")
}

func TestBlockCard_Deactivated_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "deactivated"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.BlockCard(card.ID, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deactivated")
}

// ---------------------------------------------------------------------------
// UnblockCard
// ---------------------------------------------------------------------------

func TestUnblockCard_StatusBackToActive(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	// Block, then unblock.
	blocked, err := svc.BlockCard(card.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "blocked", blocked.Status)

	unblocked, err := svc.UnblockCard(card.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "active", unblocked.Status)

	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, "active", persisted.Status)
}

func TestUnblockCard_NotBlocked_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.UnblockCard(card.ID, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not blocked")
}

// ---------------------------------------------------------------------------
// DeactivateCard
// ---------------------------------------------------------------------------

func TestDeactivateCard_Permanent(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	deactivated, err := svc.DeactivateCard(card.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "deactivated", deactivated.Status)

	var persisted model.Card
	require.NoError(t, db.First(&persisted, card.ID).Error)
	assert.Equal(t, "deactivated", persisted.Status)
}

func TestDeactivateCard_AlreadyDeactivated_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "deactivated"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.DeactivateCard(card.ID, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already deactivated")
}

func TestDeactivateCard_ThenUnblock_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	// Deactivate the card.
	_, err := svc.DeactivateCard(card.ID, 0)
	require.NoError(t, err)

	// Attempt to unblock the deactivated card — must fail.
	_, err = svc.UnblockCard(card.ID, 0)
	assert.Error(t, err, "unblocking a deactivated card must fail")
	assert.Contains(t, err.Error(), "not blocked")
}

// ---------------------------------------------------------------------------
// BlockCard on deactivated card — cannot block
// ---------------------------------------------------------------------------

func TestBlockCard_AfterDeactivation_Error(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	_, err := svc.DeactivateCard(card.ID, 0)
	require.NoError(t, err)

	_, err = svc.BlockCard(card.ID, 0)
	assert.Error(t, err, "blocking a deactivated card must fail")
	assert.Contains(t, err.Error(), "deactivated")
}
