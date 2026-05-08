package service

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/exbanka/card-service/internal/cache"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

// TestVerifyPin_LocksAfterThreeFailures complements the existing
// TestVerifyPin_CorrectPinResetsAttempts by exercising the lockout branch:
// after 3 wrong PINs the card status flips to "blocked" and ErrCardLocked
// is returned on subsequent verify attempts.
func TestVerifyPin_LocksAfterThreeFailures(t *testing.T) {
	db := newCardTestDB(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("1234"), bcrypt.DefaultCost)
	card := seedCard(t, db, model.Card{
		Status: "active", PinHash: string(hash), PinAttempts: 0,
	})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	for i := 0; i < 3; i++ {
		ok, err := svc.VerifyPin(card.ID, "9999")
		require.NoError(t, err, "wrong PIN attempt %d returns false, not error", i)
		assert.False(t, ok)
	}

	var refreshed model.Card
	require.NoError(t, db.First(&refreshed, card.ID).Error)
	assert.Equal(t, "blocked", refreshed.Status, "card must be blocked after 3 failures")

	_, err := svc.VerifyPin(card.ID, "1234")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCardLocked)
}

// TestUseCard_NonVirtualIsNoOp covers the early-return for non-virtual cards.
func TestUseCard_NonVirtualIsNoOp(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active", IsVirtual: false})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	require.NoError(t, svc.UseCard(card.ID))

	var refreshed model.Card
	require.NoError(t, db.First(&refreshed, card.ID).Error)
	assert.Equal(t, "active", refreshed.Status)
}

// TestUseCard_UnlimitedIsNoOp covers the unlimited-usage early-return.
func TestUseCard_UnlimitedIsNoOp(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active", IsVirtual: true, UsageType: "unlimited"})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	require.NoError(t, svc.UseCard(card.ID))
}

// TestUseCard_DecrementsAndDeactivatesAtZero verifies the auto-deactivation
// branch when UsesRemaining hits 0.
func TestUseCard_DecrementsAndDeactivatesAtZero(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{
		Status: "active", IsVirtual: true, UsageType: "single_use",
		MaxUses: 1, UsesRemaining: 1,
	})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	require.NoError(t, svc.UseCard(card.ID))

	var refreshed model.Card
	require.NoError(t, db.First(&refreshed, card.ID).Error)
	assert.Equal(t, 0, refreshed.UsesRemaining)
	assert.Equal(t, "deactivated", refreshed.Status, "uses_remaining=0 must deactivate")
}

// TestUseCard_AlreadyUsedReturnsError covers the ErrSingleUseAlreadyUsed branch.
func TestUseCard_AlreadyUsedReturnsError(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{
		Status: "deactivated", IsVirtual: true, UsageType: "single_use",
		MaxUses: 1, UsesRemaining: 0,
	})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	err := svc.UseCard(card.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSingleUseAlreadyUsed)
}

// TestGetCard_WithCache exercises the cache hit/miss branches of GetCard.
func TestGetCard_WithCache(t *testing.T) {
	mr := miniredis.RunT(t)
	cc, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer cc.Close()

	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{Status: "active"})

	svc := &CardService{
		cardRepo: repository.NewCardRepository(db),
		cache:    cc,
		db:       db,
	}

	// Miss -> fetch from repo, populate cache.
	got1, err := svc.GetCard(card.ID)
	require.NoError(t, err)
	assert.Equal(t, card.ID, got1.ID)

	// Hit -> serve from cache. Wipe DB row to confirm.
	require.NoError(t, db.Delete(&model.Card{}, card.ID).Error)
	got2, err := svc.GetCard(card.ID)
	require.NoError(t, err)
	assert.Equal(t, card.ID, got2.ID, "second call must hit cache, not the deleted DB row")
}

// TestBlockCard_NotFound exercises the gorm.ErrRecordNotFound -> ErrCardNotFound
// translation branch in BlockCard.
func TestBlockCard_NotFound(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	_, err := svc.BlockCard(99999, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCardNotFound)
}

func TestUnblockCard_NotFound(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	_, err := svc.UnblockCard(99999, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCardNotFound)
}

func TestDeactivateCard_NotFound(t *testing.T) {
	db := newCardTestDB(t)
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}
	_, err := svc.DeactivateCard(99999, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCardNotFound)
}

// TestBlockCard_RecordsChangelog covers the changelogRepo non-nil branch on
// successful block.
func TestBlockCard_RecordsChangelog(t *testing.T) {
	db := newCardTestDB(t)
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

	card := seedCard(t, db, model.Card{Status: "active"})
	svc := &CardService{
		cardRepo:      repository.NewCardRepository(db),
		changelogRepo: repository.NewChangelogRepository(db),
		db:            db,
	}
	_, err := svc.BlockCard(card.ID, 7)
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Table("changelogs").Where("entity_type = ? AND entity_id = ?", "card", card.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "changelog row must be written on block")
}

// TestNewCardService_OptionalChangelogRepo covers both paths of the variadic
// changelogRepo argument: not supplied (default nil), and supplied.
func TestNewCardService_OptionalChangelogRepo(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)

	svc := NewCardService(cardRepo, blockRepo, authRepo, stubProducer(), nil, db)
	require.NotNil(t, svc)

	clRepo := repository.NewChangelogRepository(db)
	svc2 := NewCardService(cardRepo, blockRepo, authRepo, stubProducer(), nil, db, clRepo)
	require.NotNil(t, svc2)
}

func TestNewCardRequestService_Constructs(t *testing.T) {
	db := newCardTestDB(t)
	reqRepo := repository.NewCardRequestRepository(db)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)
	cardSvc := &CardService{cardRepo: cardRepo, blockRepo: blockRepo, authRepo: authRepo, db: db}
	svc := NewCardRequestService(reqRepo, cardSvc, stubProducer())
	require.NotNil(t, svc)
}

// TestUseCard_DecrementsButStaysActive covers the multi-use mid-lifecycle path.
func TestUseCard_DecrementsButStaysActive(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{
		Status: "active", IsVirtual: true, UsageType: "multi_use",
		MaxUses: 5, UsesRemaining: 3,
	})
	svc := &CardService{cardRepo: repository.NewCardRepository(db), db: db}

	require.NoError(t, svc.UseCard(card.ID))

	var refreshed model.Card
	require.NoError(t, db.First(&refreshed, card.ID).Error)
	assert.Equal(t, 2, refreshed.UsesRemaining)
	assert.Equal(t, "active", refreshed.Status, "uses_remaining>0 must keep card active")
}
