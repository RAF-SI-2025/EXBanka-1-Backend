package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func TestOTCExpiryCron_RunOnce_PeerWarnAndCapitalGain(t *testing.T) {
	db := newOTCExpiryDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	contractRepo := repository.NewOptionContractRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	holdingRes := NewHoldingReservationService(db, holdingRepo, resRepo)
	cgRepo := repository.NewCapitalGainRepository(db)

	sellerUID := uint64(1)
	buyerUID := uint64(7)
	// Seller holds the underlying for both the local and the remote contract.
	if err := db.Create(&model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &sellerUID,
		UserFirstName: "S", UserLastName: "Eller", SecurityType: "stock", SecurityID: 42,
		ListingID: 100, Ticker: "AAPL", Name: "Apple", Quantity: 100, AveragePrice: decimal.NewFromInt(50),
	}).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}

	// 1) Local active contract past settlement → expired + buyer premium loss booked.
	local := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		BuyerAccountID: 5001, PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(local); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	if _, err := holdingRes.ReserveForOTCContract(context.Background(), model.OwnerClient, &sellerUID, "stock", 42, local.ID, 10); err != nil {
		t.Fatalf("reserve local: %v", err)
	}

	// 2) A contract settling exactly 7 days out → expiring-soon warning.
	soon := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(1),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(5),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, 7).Truncate(24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(soon); err != nil {
		t.Fatalf("seed soon: %v", err)
	}

	// 3) Remote (cross-bank) contract past settlement, direction DEBIT (seller side).
	native := "rem-1"
	dir := "DEBIT"
	remote := &model.OptionContract{
		RoutingNumber: 222, NativeID: &native,
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(5),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(10),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -1),
		Status:         "active",
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		RemoteDirection: &dir, SagaID: "saga-r", PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(remote); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	if remote.Local {
		t.Fatalf("remote contract should not be local")
	}
	if _, err := holdingRes.ReserveForPeerOptionContract(context.Background(), model.OwnerClient, &sellerUID, "stock", "AAPL", remote.ID, 5); err != nil {
		t.Fatalf("reserve peer: %v", err)
	}

	cron := NewOTCExpiryCron(contractRepo, holdingRes, nil, 10, "02:00", nilRegistry()).
		WithCapitalGains(cgRepo).
		WithExpiryWarning(7).
		WithPeerContracts(contractRepo)

	if err := cron.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	// Local contract expired.
	gotLocal, _ := contractRepo.GetByID(local.ID)
	if gotLocal.Status != model.OptionContractStatusExpired {
		t.Errorf("local contract status = %s, want expired", gotLocal.Status)
	}
	// Buyer premium loss booked.
	var lossCount int64
	db.Model(&model.CapitalGain{}).Where("owner_id = ? AND security_type = ?", buyerUID, "option").Count(&lossCount)
	if lossCount == 0 {
		t.Errorf("expected a buyer premium-loss capital gain row")
	}
	// Remote contract expired.
	var gotRemote model.OptionContract
	if err := db.First(&gotRemote, remote.ID).Error; err != nil {
		t.Fatalf("reload remote: %v", err)
	}
	if gotRemote.Status != "expired" {
		t.Errorf("remote contract status = %s, want expired", gotRemote.Status)
	}
}
