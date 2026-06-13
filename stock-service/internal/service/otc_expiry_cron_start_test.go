package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestOTCExpiryCron_Start_RunsStartupCatchup verifies the Start goroutine runs
// an immediate catch-up pass (expiring an already-past contract) and then
// returns cleanly on context cancel.
func TestOTCExpiryCron_Start_RunsStartupCatchup(t *testing.T) {
	db := newOTCExpiryDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	contractRepo := repository.NewOptionContractRepository(db)

	buyerUID := uint64(7)
	sellerUID := uint64(1)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2), // already past
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	// Far-future daily time so only the startup catch-up runs during the test.
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 10, "23:59", nilRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	cr.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := contractRepo.GetByID(c.ID)
		if got != nil && got.Status == model.OptionContractStatusExpired {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("startup catch-up did not expire the contract")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
}
