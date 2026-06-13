package repository

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newHoldingCreditTestDB opens an in-memory SQLite DB with the tables the
// marker-guarded credit paths touch: Holding, FundHolding, HoldingCreditMarker.
func newHoldingCreditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.FundHolding{},
		&model.HoldingCreditMarker{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func buyerHolding(qty int64) *model.Holding {
	uid := uint64(7)
	return &model.Holding{
		OwnerType:    model.OwnerClient,
		OwnerID:      &uid,
		SecurityType: "stock",
		SecurityID:   42,
		Ticker:       "ACME",
		Name:         "Acme",
		Quantity:     qty,
		AveragePrice: decimal.NewFromInt(100),
		AccountID:    1,
	}
}

// TestUpsertIdempotent_CreditsExactlyOnce proves the OTC exercise buyer-credit
// step is safe to replay: calling UpsertIdempotent N times with the same key
// credits the shares exactly once (the crash-recovery forward-resume guarantee).
func TestUpsertIdempotent_CreditsExactlyOnce(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	repo := NewHoldingRepository(db)
	ctx := context.Background()
	const key = "otc-exercise-buyer-credit-99"
	uid := uint64(7)

	for i := 0; i < 3; i++ {
		if err := repo.UpsertIdempotent(ctx, buyerHolding(10), key); err != nil {
			t.Fatalf("UpsertIdempotent #%d: %v", i, err)
		}
	}

	h, err := repo.GetByOwnerAndSecurity(model.OwnerClient, &uid, "stock", 42)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if h.Quantity != 10 {
		t.Fatalf("replay double-credited: quantity=%d want 10", h.Quantity)
	}
}

// TestDecrementForOwnerIdempotent_ReversesOnceThenReCredits proves the paired
// Backward: a single decrement reverses the credit (no-op on replay), and after
// reversal the SAME key can credit again (so a compensated exercise can be
// retried).
func TestDecrementForOwnerIdempotent_ReversesOnceThenReCredits(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	repo := NewHoldingRepository(db)
	ctx := context.Background()
	const key = "otc-exercise-buyer-credit-99"
	uid := uint64(7)

	if err := repo.UpsertIdempotent(ctx, buyerHolding(10), key); err != nil {
		t.Fatalf("credit: %v", err)
	}
	// Decrement twice — second is a no-op (marker already gone).
	for i := 0; i < 2; i++ {
		if err := repo.DecrementForOwnerIdempotent(ctx, model.OwnerClient, &uid, "stock", 42, 10, key); err != nil {
			t.Fatalf("decrement #%d: %v", i, err)
		}
	}
	if _, err := repo.GetByOwnerAndSecurity(model.OwnerClient, &uid, "stock", 42); err != gorm.ErrRecordNotFound {
		t.Fatalf("holding should be gone after reversal, got err=%v", err)
	}

	// Marker was deleted with the reversal, so the same key credits again.
	if err := repo.UpsertIdempotent(ctx, buyerHolding(10), key); err != nil {
		t.Fatalf("re-credit: %v", err)
	}
	h, err := repo.GetByOwnerAndSecurity(model.OwnerClient, &uid, "stock", 42)
	if err != nil {
		t.Fatalf("lookup after re-credit: %v", err)
	}
	if h.Quantity != 10 {
		t.Fatalf("re-credit quantity=%d want 10", h.Quantity)
	}
}

// TestFundUpsertIdempotent_CreditsExactlyOnce mirrors the personal-holding test
// for the on-behalf-of-fund branch of the exercise buyer-credit step.
func TestFundUpsertIdempotent_CreditsExactlyOnce(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	repo := NewFundHoldingRepository(db)
	const key = "otc-exercise-buyer-credit-fund-99"

	for i := 0; i < 3; i++ {
		fh := &model.FundHolding{
			FundID:          5,
			SecurityType:    "stock",
			SecurityID:      42,
			Quantity:        10,
			AveragePriceRSD: decimal.NewFromInt(100),
		}
		if err := repo.UpsertIdempotent(fh, key); err != nil {
			t.Fatalf("UpsertIdempotent #%d: %v", i, err)
		}
	}
	h, err := repo.GetByFundAndSecurity(5, "stock", 42)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if h.Quantity != 10 {
		t.Fatalf("replay double-credited fund: quantity=%d want 10", h.Quantity)
	}

	// Reverse once + replay, then re-credit under same key.
	for i := 0; i < 2; i++ {
		if err := repo.DecrementForFundSecurityIdempotent(5, "stock", 42, 10, key); err != nil {
			t.Fatalf("fund decrement #%d: %v", i, err)
		}
	}
	h, _ = repo.GetByFundAndSecurity(5, "stock", 42)
	if h.Quantity != 0 {
		t.Fatalf("fund reversal quantity=%d want 0", h.Quantity)
	}
}
