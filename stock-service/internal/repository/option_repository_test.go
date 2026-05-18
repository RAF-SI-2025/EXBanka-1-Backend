package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

func newOptionRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Stock{}, &model.Option{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestOptionRepository_UpsertByTicker_PopulatesIDOnUpdate guards against the
// bug where UpsertByTicker's update path silently left the caller's struct
// with ID=0, breaking downstream SetListingID calls in generateAllOptions.
func TestOptionRepository_UpsertByTicker_PopulatesIDOnUpdate(t *testing.T) {
	db := newOptionRepoTestDB(t)
	stock := model.Stock{
		Ticker: "AAPL", Name: "Apple Inc.", ExchangeID: 1,
		Price: decimal.NewFromInt(150),
	}
	if err := db.Create(&stock).Error; err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	repo := NewOptionRepository(db)

	// First upsert: creates new row, o.ID populated by Create().
	first := &model.Option{
		Ticker:         "AAPL260430C00001500",
		Name:           "AAPL 2026-04-30 Call 150",
		StockID:        stock.ID,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(5),
		SettlementDate: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	}
	if err := repo.UpsertByTicker(first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("first upsert must populate ID on insert")
	}
	createdID := first.ID

	// Second upsert with the same ticker but a fresh struct (simulating the
	// way generateAllOptions builds options each pass). Before the fix this
	// would leave second.ID == 0.
	second := &model.Option{
		Ticker:         "AAPL260430C00001500",
		Name:           "AAPL 2026-04-30 Call 150 (updated)",
		StockID:        stock.ID,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(7), // changed
		SettlementDate: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	}
	if err := repo.UpsertByTicker(second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID == 0 {
		t.Fatalf("second upsert must populate ID on update path (got 0)")
	}
	if second.ID != createdID {
		t.Errorf("second upsert should retain the original ID %d, got %d",
			createdID, second.ID)
	}
}

// TestOptionRepository_SetListingID_ActuallyPersists guards against the bug
// where `db.Model(&model.Option{}).Updates(...)` on a versioned model adds a
// WHERE version = 0 clause via the BeforeUpdate hook, silently updating zero
// rows. The fix skips hooks. See CLAUDE.md "NEVER use db.Model(&MyModel{}).
// Updates(...) on a versioned model".
func TestOptionRepository_SetListingID_ActuallyPersists(t *testing.T) {
	db := newOptionRepoTestDB(t)

	// Seed one stock so the FK references pass the not-null stock_id check.
	stock := model.Stock{
		Ticker:     "AAPL",
		Name:       "Apple Inc.",
		ExchangeID: 1,
		Price:      decimal.NewFromInt(150),
	}
	if err := db.Create(&stock).Error; err != nil {
		t.Fatalf("seed stock: %v", err)
	}

	// Seed one option with the default Version=1.
	opt := &model.Option{
		Ticker:         "AAPL260430C00001500",
		Name:           "AAPL 2026-04-30 Call 150",
		StockID:        stock.ID,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(150),
		Premium:        decimal.NewFromInt(5),
		SettlementDate: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	}
	repo := NewOptionRepository(db)
	if err := repo.Create(opt); err != nil {
		t.Fatalf("create option: %v", err)
	}

	// The default version after Create is 1.
	if opt.Version != 1 {
		t.Errorf("expected version=1 after create, got %d", opt.Version)
	}

	// Act: attach a listing ID.
	const listingID uint64 = 4242
	if err := repo.SetListingID(opt.ID, listingID); err != nil {
		t.Fatalf("SetListingID: %v", err)
	}

	// Assert: the row must actually carry the new listing_id. Prior to the
	// fix, the BeforeUpdate hook would add WHERE version = 0 and the UPDATE
	// would match zero rows, leaving listing_id NULL.
	var got model.Option
	if err := db.First(&got, opt.ID).Error; err != nil {
		t.Fatalf("reload option: %v", err)
	}
	if got.ListingID == nil {
		t.Fatalf("listing_id is still NULL after SetListingID — the hook-induced WHERE version=0 bug has regressed")
	}
	if *got.ListingID != listingID {
		t.Errorf("expected listing_id=%d, got %d", listingID, *got.ListingID)
	}
}
