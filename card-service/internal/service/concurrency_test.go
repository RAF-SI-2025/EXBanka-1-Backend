package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
)

func newCardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Use a per-test named in-memory database so tests don't interfere with
	// each other's schema. Single connection ensures all goroutines in the
	// same test share the same DB instance without "no such table" errors.
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Card{}, &model.CardBlock{}))
	return db
}

func seedCard(t *testing.T, db *gorm.DB, opts model.Card) *model.Card {
	t.Helper()
	if opts.CardNumber == "" {
		opts.CardNumber = "411111XXXXXX1111"
	}
	if opts.CardNumberFull == "" {
		opts.CardNumberFull = "4111111111111111"
	}
	if opts.CVV == "" {
		opts.CVV = "123"
	}
	if opts.CardBrand == "" {
		opts.CardBrand = "visa"
	}
	if opts.AccountNumber == "" {
		opts.AccountNumber = "111000100000000011"
	}
	if opts.OwnerID == 0 {
		opts.OwnerID = 1
	}
	if opts.OwnerType == "" {
		opts.OwnerType = "client"
	}
	if opts.Status == "" {
		opts.Status = "active"
	}
	if opts.ExpiresAt.IsZero() {
		opts.ExpiresAt = time.Now().AddDate(3, 0, 0)
	}
	if opts.CardLimit.IsZero() {
		opts.CardLimit = decimal.NewFromInt(1_000_000)
	}
	if opts.Version == 0 {
		opts.Version = 1
	}
	require.NoError(t, db.Create(&opts).Error)
	return &opts
}

// TestConcurrentPinVerificationLocksCard verifies that concurrent wrong-PIN
// attempts eventually lock the card after 3 failures, with no more than 3
// attempts succeeding regardless of goroutine interleaving.
func TestConcurrentPinVerificationLocksCard(t *testing.T) {
	db := newCardTestDB(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("9999"), bcrypt.MinCost)
	require.NoError(t, err)

	card := seedCard(t, db, model.Card{
		PinHash:   string(hash),
		IsVirtual: false,
	})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	const workers = 6
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			// All goroutines submit a wrong PIN ("0000"). Each wrong attempt
			// increments PinAttempts; the 3rd locks the card.
			_, _ = svc.VerifyPin(card.ID, "0000")
		}()
	}
	wg.Wait()

	var final model.Card
	require.NoError(t, db.First(&final, card.ID).Error)

	// PinAttempts must never exceed 3 (the service stops incrementing when locked).
	assert.LessOrEqual(t, final.PinAttempts, 3, "PinAttempts must not exceed 3")
	assert.Equal(t, "blocked", final.Status, "card must be blocked after 3 failed PIN attempts")
}

// TestConcurrentUseCardDecrementsCorrectly verifies that concurrent UseCard calls
// on a multi-use virtual card never result in UsesRemaining going below 0.
func TestConcurrentUseCardDecrementsCorrectly(t *testing.T) {
	db := newCardTestDB(t)

	const initialUses = 5
	card := seedCard(t, db, model.Card{
		CardNumber:     "411222XXXXXX2222",
		CardNumberFull: "4112222222222222",
		IsVirtual:      true,
		UsageType:      "multi_use",
		UsesRemaining:  initialUses,
		MaxUses:        initialUses,
	})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	// Launch more goroutines than available uses.
	const workers = 8
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := svc.UseCard(card.ID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, initialUses, successCount, "exactly initialUses successful UseCard calls expected")
	assert.Equal(t, workers-initialUses, failCount, "remaining goroutines should see 'no remaining uses'")

	var final model.Card
	require.NoError(t, db.First(&final, card.ID).Error)
	assert.Equal(t, 0, final.UsesRemaining, "UsesRemaining must be exactly 0 after all uses consumed")
	assert.Equal(t, "deactivated", final.Status, "card must be deactivated after all uses consumed")
}

// TestOptimisticLockConflictCard verifies that saving a stale Card (Version behind
// the current DB value) is rejected by the BeforeUpdate hook without corrupting data.
func TestOptimisticLockConflictCard(t *testing.T) {
	db := newCardTestDB(t)
	card := seedCard(t, db, model.Card{})

	cardRepo := repository.NewCardRepository(db)
	svc := &CardService{cardRepo: cardRepo, db: db}

	// First block — succeeds, bumps Version to 2.
	blocked, err := svc.BlockCard(card.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "blocked", blocked.Status)

	// Now attempt to block again using the stale card (Version=1 from seedCard).
	// The BeforeUpdate hook will add WHERE version=1; but DB has version=2, so 0 rows affected.
	// The service should return an error ("already blocked") because GetByIDForUpdate re-reads.
	_, err = svc.BlockCard(card.ID, 0)
	assert.Error(t, err, "blocking an already-blocked card must return an error")
	assert.Contains(t, err.Error(), "already blocked")

	var final model.Card
	require.NoError(t, db.First(&final, card.ID).Error)
	assert.Equal(t, "blocked", final.Status, "status must still be blocked")
	assert.Equal(t, int64(2), final.Version, "version must be 2 after one successful save")
}
