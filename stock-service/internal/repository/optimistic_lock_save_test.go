package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// These tests are F15 regressions: they assert that the four repositories
// whose Save/Update path was widened with Select("*") correctly surface
// ErrOptimisticLock on a concurrent-modification scenario.
//
// Without Select("*"), GORM v1.31.1's Save falls back to
// INSERT ... ON CONFLICT(id) DO UPDATE when the BeforeUpdate WHERE version=?
// clause matches no row, returning RowsAffected=1 and silently clobbering
// the winner of the race. With Select("*"), the fallback is disabled and
// RowsAffected==0 surfaces correctly to the caller.

// newOptLockTestDB opens a fresh in-memory SQLite DB and migrates the four
// models exercised by these regression tests.
func newOptLockTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.StockExchange{},
		&model.Stock{},
		&model.Listing{},
		&model.OptionContract{},
		&model.Holding{},
		&model.HoldingReservation{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestStockRepository_Update_SurfacesOptimisticLock(t *testing.T) {
	db := newOptLockTestDB(t)
	repo := NewStockRepository(db)

	exchange := &model.StockExchange{Name: "Test Exchange", Acronym: "TST"}
	if err := db.Create(exchange).Error; err != nil {
		t.Fatalf("seed exchange: %v", err)
	}

	stock := &model.Stock{
		Ticker: "F15S", Name: "F15 Stock", ExchangeID: exchange.ID,
		Price: decimal.NewFromInt(100),
	}
	if err := repo.Create(stock); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Two snapshots loaded at the same version.
	stale, err := repo.GetByID(stock.ID)
	if err != nil {
		t.Fatalf("load stale: %v", err)
	}
	winner, err := repo.GetByID(stock.ID)
	if err != nil {
		t.Fatalf("load winner: %v", err)
	}

	// Winner commits first.
	winner.Name = "winner"
	if err := repo.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	// Loser tries to commit on the now-stale Version.
	stale.Name = "loser"
	err = repo.Update(stale)
	if !errors.Is(err, ErrOptimisticLock) {
		t.Fatalf("expected ErrOptimisticLock, got: %v", err)
	}

	// Verify the winner's value persisted (stale did not clobber).
	final, err := repo.GetByID(stock.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if final.Name != "winner" {
		t.Errorf("stale write clobbered winner: name=%q want winner", final.Name)
	}
}

func TestListingRepository_Update_SurfacesOptimisticLock(t *testing.T) {
	db := newOptLockTestDB(t)
	repo := NewListingRepository(db)

	exchange := &model.StockExchange{Name: "Test Exchange", Acronym: "TST"}
	if err := db.Create(exchange).Error; err != nil {
		t.Fatalf("seed exchange: %v", err)
	}

	listing := &model.Listing{
		SecurityID: 42, SecurityType: "stock", ExchangeID: exchange.ID,
		Price: decimal.NewFromInt(100),
	}
	if err := repo.Create(listing); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale, err := repo.GetByID(listing.ID)
	if err != nil {
		t.Fatalf("load stale: %v", err)
	}
	winner, err := repo.GetByID(listing.ID)
	if err != nil {
		t.Fatalf("load winner: %v", err)
	}

	winner.Price = decimal.NewFromInt(150)
	if err := repo.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Price = decimal.NewFromInt(999)
	err = repo.Update(stale)
	if !errors.Is(err, ErrOptimisticLock) {
		t.Fatalf("expected ErrOptimisticLock, got: %v", err)
	}

	final, err := repo.GetByID(listing.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !final.Price.Equal(decimal.NewFromInt(150)) {
		t.Errorf("stale write clobbered winner: price=%s want 150", final.Price)
	}
}

func TestOptionContractRepository_Save_SurfacesOptimisticLock(t *testing.T) {
	db := newOptLockTestDB(t)
	repo := NewOptionContractRepository(db)

	uid := uint64(7)
	settlement, _ := time.Parse("2006-01-02", "2026-12-31")
	c := &model.OptionContract{
		OfferID:         100,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &uid,
		SellerOwnerType: model.OwnerBank,
		SellerOwnerID:   nil,
		StockID:         1,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromInt(50),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  settlement,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-f15",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := repo.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale, err := repo.GetByID(c.ID)
	if err != nil {
		t.Fatalf("load stale: %v", err)
	}
	winner, err := repo.GetByID(c.ID)
	if err != nil {
		t.Fatalf("load winner: %v", err)
	}

	winner.Status = model.OptionContractStatusExercised
	if err := repo.Save(winner); err != nil {
		t.Fatalf("winner save: %v", err)
	}

	stale.Status = model.OptionContractStatusFailed
	err = repo.Save(stale)
	if !errors.Is(err, ErrOptimisticLock) {
		t.Fatalf("expected ErrOptimisticLock, got: %v", err)
	}

	final, err := repo.GetByID(c.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if final.Status != model.OptionContractStatusExercised {
		t.Errorf("stale write clobbered winner: status=%s want %s",
			final.Status, model.OptionContractStatusExercised)
	}
}

func TestHoldingReservationRepository_UpdateStatus_SurfacesOptimisticLock(t *testing.T) {
	db := newOptLockTestDB(t)
	repo := NewHoldingReservationRepository(db)

	uid := uint64(1)
	holding := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		UserFirstName: "Test", UserLastName: "User",
		SecurityType: "stock", SecurityID: 1, ListingID: 1,
		Ticker: "F15", Name: "F15 Stock",
		Quantity: 1000, AveragePrice: decimal.NewFromInt(100), AccountID: 1,
	}
	if err := db.Create(holding).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}

	orderID := uint64(9001)
	res := &model.HoldingReservation{
		HoldingID: holding.ID, OrderID: &orderID,
		Quantity: 500, Status: model.HoldingReservationStatusActive,
	}
	if err := repo.Create(res); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale, err := repo.GetByOrderID(orderID)
	if err != nil {
		t.Fatalf("load stale: %v", err)
	}
	winner, err := repo.GetByOrderID(orderID)
	if err != nil {
		t.Fatalf("load winner: %v", err)
	}

	winner.Status = model.HoldingReservationStatusSettled
	if err := repo.UpdateStatus(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Status = model.HoldingReservationStatusReleased
	err = repo.UpdateStatus(stale)
	if !errors.Is(err, ErrOptimisticLock) {
		t.Fatalf("expected ErrOptimisticLock, got: %v", err)
	}

	final, err := repo.GetByOrderID(orderID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if final.Status != model.HoldingReservationStatusSettled {
		t.Errorf("stale write clobbered winner: status=%s want %s",
			final.Status, model.HoldingReservationStatusSettled)
	}
}
