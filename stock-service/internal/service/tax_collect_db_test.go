package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestTaxService_CollectTax_WithDB_TransactionWrapper exercises the
// db-wired branch of CollectTax (the advisory-lock transaction wrapper),
// which the no-db tests bypass.
func TestTaxService_CollectTax_WithDB_TransactionWrapper(t *testing.T) {
	svc, mocks := buildTaxService()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.TaxCollection{}, &model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc = svc.WithDB(db)

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-ACCT-001")
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		OwnerType: model.OwnerClient, OwnerID: ptrU64(42),
		SecurityType: "stock", Ticker: "AAPL", Quantity: 20,
		BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(2000), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{OwnerType: "client", OwnerID: ptrU64(42), TotalDebtRSD: decimal.NewFromInt(300)},
	}

	collected, totalRSD, failed, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if collected != 1 || failed != 0 {
		t.Fatalf("collected=%d failed=%d, want 1/0", collected, failed)
	}
	if !totalRSD.Equal(decimal.NewFromInt(300)) {
		t.Errorf("totalRSD = %s, want 300", totalRSD)
	}
	// The money side still moved (debit + credit) through the account client.
	if len(mocks.accountClient.updateBalCalls) < 2 {
		t.Errorf("expected debit+credit balance calls, got %d", len(mocks.accountClient.updateBalCalls))
	}
}
