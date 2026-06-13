package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestExerciseContract_OnBehalfOfFund_CreditsFundHoldings covers
// buildExerciseSaga's fund branch: an OnBehalfOfFund contract credits
// fund_holdings (not the buyer's personal holdings) on exercise.
func TestExerciseContract_OnBehalfOfFund_CreditsFundHoldings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.Holding{}, &model.HoldingReservation{}, &model.HoldingReservationSettlement{},
		&model.HoldingCreditMarker{}, &model.OptionContract{}, &model.FundHolding{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	contractRepo := repository.NewOptionContractRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	fundHoldingRepo := repository.NewFundHoldingRepository(db)
	holdingResSvc := NewHoldingReservationService(db, holdingRepo, resRepo)

	accounts := &fakeOTCAccountClient{fakeFundAccountClient: newFakeFundAccountClient()}
	accounts.addAccount(5001, "BUYER-RSD", "1000000")
	accounts.accounts[5001].CurrencyCode = "RSD"
	accounts.addAccount(6001, "SELLER-RSD", "0")
	accounts.accounts[6001].CurrencyCode = "RSD"

	svc := NewOTCOfferService(
		repository.NewOTCOfferRepository(db), repository.NewOTCOfferRevisionRepository(db),
		contractRepo, holdingRepo, repository.NewOTCReadReceiptRepository(db), nil,
	).WithSaga(newFakeSagaRepo(), accounts, &fakeFundExchangeClient{}, holdingResSvc, holdingRepo).
		WithFundHolding(fundHoldingRepo)

	stockID := uint64(42)
	sellerUID := uint64(87)
	buyerUID := uint64(55)
	_ = holdingRepo.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &sellerUID,
		SecurityType: "stock", SecurityID: stockID, Quantity: 100, AveragePrice: decimal.NewFromInt(100),
	})

	fundID := uint64(9)
	contract := &model.OptionContract{
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		StockID: stockID, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(5000), PremiumPaid: decimal.NewFromInt(50000),
		PremiumCurrency: "RSD", StrikeCurrency: "RSD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, 7), Status: model.OptionContractStatusActive,
		SagaID: uuid.NewString(), PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID: 5001, SellerAccountID: 6001,
		OnBehalfOfFundID: &fundID,
	}
	if err := contractRepo.Create(contract); err != nil {
		t.Fatalf("create contract: %v", err)
	}
	if _, err := holdingResSvc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &sellerUID, "stock", stockID, contract.ID, 10); err != nil {
		t.Fatalf("reserve seller: %v", err)
	}

	if _, err := svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: contract.ID, ActorUserID: int64(buyerUID), ActorSystemType: "client",
	}); err != nil {
		t.Fatalf("exercise: %v", err)
	}

	// The acquired shares landed in fund_holdings (fund 9), not the buyer's personal holdings.
	var fh model.FundHolding
	if err := db.Where("fund_id = ? AND security_id = ?", fundID, stockID).First(&fh).Error; err != nil {
		t.Fatalf("fund holding not credited: %v", err)
	}
	if fh.Quantity != 10 {
		t.Errorf("fund holding qty = %d, want 10", fh.Quantity)
	}
}
