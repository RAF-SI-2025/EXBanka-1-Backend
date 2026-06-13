package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func newStaleScanner(t *testing.T) (*StaleReservationScanner, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.HoldingReservation{},
		&model.Order{},
		&model.OptionContract{},
		&model.Listing{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	reg := cronreg.NewRegistry("test", nil)
	s := NewStaleReservationScanner(
		db,
		repository.NewHoldingReservationRepository(db),
		repository.NewOrderRepository(db),
		repository.NewOptionContractRepository(db),
		0, 0, // defaults applied
		reg,
	).WithPeerContracts(repository.NewOptionContractRepository(db))
	return s, db
}

func u64(v uint64) *uint64 { return &v }

func TestStaleScanner_DefaultsApplied(t *testing.T) {
	s, _ := newStaleScanner(t)
	if s.interval != 24*time.Hour || s.threshold != 24*time.Hour {
		t.Errorf("defaults not applied: interval=%v threshold=%v", s.interval, s.threshold)
	}
}

func TestStaleScanner_ClassifyOrderBacked(t *testing.T) {
	s, db := newStaleScanner(t)

	// Missing order → orphan → stale++.
	stale := 0
	s.classifyOrderBacked(&model.HoldingReservation{ID: 1, OrderID: u64(999)}, &stale)
	if stale != 1 {
		t.Errorf("missing order: stale=%d want 1", stale)
	}

	// Filled order → terminal → stale++.
	filled := &model.Order{ID: 10, OwnerType: model.OwnerClient, OwnerID: u64(1), ListingID: 1, SecurityType: "stock", Ticker: "AAA", Direction: "sell", OrderType: "market", Quantity: 1, PricePerUnit: decimal.NewFromInt(1), ApproximatePrice: decimal.NewFromInt(1), RemainingPortions: 1, AccountID: 5, Status: "filled"}
	if err := db.Create(filled).Error; err != nil {
		t.Fatalf("create filled: %v", err)
	}
	s.classifyOrderBacked(&model.HoldingReservation{ID: 2, OrderID: u64(10)}, &stale)
	if stale != 2 {
		t.Errorf("filled order: stale=%d want 2", stale)
	}

	// Pending order → not terminal → no increment.
	pending := &model.Order{ID: 11, OwnerType: model.OwnerClient, OwnerID: u64(1), ListingID: 1, SecurityType: "stock", Ticker: "AAA", Direction: "sell", OrderType: "market", Quantity: 1, PricePerUnit: decimal.NewFromInt(1), ApproximatePrice: decimal.NewFromInt(1), RemainingPortions: 1, AccountID: 5, Status: "pending"}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending: %v", err)
	}
	s.classifyOrderBacked(&model.HoldingReservation{ID: 3, OrderID: u64(11)}, &stale)
	if stale != 2 {
		t.Errorf("pending order should not be stale: stale=%d want 2", stale)
	}
}

func TestStaleScanner_ClassifyOTCBacked(t *testing.T) {
	s, db := newStaleScanner(t)
	stale := 0

	// Missing contract → orphan.
	s.classifyOTCBacked(&model.HoldingReservation{ID: 1, OTCContractID: u64(888)}, &stale)
	if stale != 1 {
		t.Errorf("missing contract: stale=%d want 1", stale)
	}

	// Exercised contract → terminal.
	ex := buildLocalContract(20, model.OptionContractStatusExercised)
	if err := db.Create(ex).Error; err != nil {
		t.Fatalf("create exercised: %v", err)
	}
	s.classifyOTCBacked(&model.HoldingReservation{ID: 2, OTCContractID: u64(ex.ID)}, &stale)
	if stale != 2 {
		t.Errorf("exercised contract: stale=%d want 2", stale)
	}

	// Active contract → not terminal.
	act := buildLocalContract(21, model.OptionContractStatusActive)
	if err := db.Create(act).Error; err != nil {
		t.Fatalf("create active: %v", err)
	}
	s.classifyOTCBacked(&model.HoldingReservation{ID: 3, OTCContractID: u64(act.ID)}, &stale)
	if stale != 2 {
		t.Errorf("active contract should not be stale: stale=%d want 2", stale)
	}
}

func TestStaleScanner_ClassifyPeerOTCBacked(t *testing.T) {
	s, db := newStaleScanner(t)
	stale := 0

	// Remote (routing 222) exercised → terminal.
	rem := buildRemoteContract(30, "exercised")
	if err := db.Create(rem).Error; err != nil {
		t.Fatalf("create remote: %v", err)
	}
	s.classifyPeerOTCBacked(&model.HoldingReservation{ID: 1, PeerOptionContractID: u64(rem.ID)}, &stale)
	if stale != 1 {
		t.Errorf("remote exercised: stale=%d want 1", stale)
	}

	// Remote active → not terminal.
	rem2 := buildRemoteContract(31, "active")
	if err := db.Create(rem2).Error; err != nil {
		t.Fatalf("create remote2: %v", err)
	}
	s.classifyPeerOTCBacked(&model.HoldingReservation{ID: 2, PeerOptionContractID: u64(rem2.ID)}, &stale)
	if stale != 1 {
		t.Errorf("remote active should not be stale: stale=%d want 1", stale)
	}

	// A LOCAL contract id passed to peer classifier → GetRemoteContractByID
	// returns not-found → counted as missing.
	loc := buildLocalContract(32, model.OptionContractStatusActive)
	if err := db.Create(loc).Error; err != nil {
		t.Fatalf("create local: %v", err)
	}
	s.classifyPeerOTCBacked(&model.HoldingReservation{ID: 3, PeerOptionContractID: u64(loc.ID)}, &stale)
	if stale != 2 {
		t.Errorf("local-as-peer missing: stale=%d want 2", stale)
	}
}

func TestStaleScanner_ScanOnce_MixedRows(t *testing.T) {
	s, db := newStaleScanner(t)
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now()

	// Stale active reservation backed by missing order (orphan).
	r1 := &model.HoldingReservation{ID: 1, OrderID: u64(777), Quantity: 5, Status: model.HoldingReservationStatusActive, CreatedAt: old}
	// Stale active reservation backed by a missing OTC contract.
	r2 := &model.HoldingReservation{ID: 2, OTCContractID: u64(666), Quantity: 5, Status: model.HoldingReservationStatusActive, CreatedAt: old}
	// Recent active row (within threshold) → excluded from the scan.
	r3 := &model.HoldingReservation{ID: 3, OrderID: u64(555), Quantity: 5, Status: model.HoldingReservationStatusActive, CreatedAt: recent}
	for _, r := range []*model.HoldingReservation{r1, r2, r3} {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("create reservation %d: %v", r.ID, err)
		}
	}
	// created_at is auto-stamped on create; force the old timestamps.
	if err := db.Model(&model.HoldingReservation{}).Where("id IN ?", []uint64{1, 2}).Update("created_at", old).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Should run without error; exercises the scan loop + classifier dispatch.
	s.scanOnce(context.Background())
}

func TestStaleScanner_Run_StopsOnContextCancel(t *testing.T) {
	s, _ := newStaleScanner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run; first scan executes, loop exits immediately.
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func baseContract(id uint64, status string) *model.OptionContract {
	now := time.Now().UTC()
	return &model.OptionContract{
		ID:              id,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    u64(99),
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   u64(1),
		StockID:         1,
		Ticker:          "AAA",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromInt(10),
		PremiumPaid:     decimal.NewFromInt(1),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  now.AddDate(0, 1, 0),
		Status:          status,
		SagaID:          "saga",
		PremiumPaidAt:   now,
	}
}

func buildLocalContract(id uint64, status string) *model.OptionContract {
	c := baseContract(id, status)
	// RoutingNumber 0 → BeforeCreate stamps OwnRouting and Local=true.
	return c
}

func buildRemoteContract(id uint64, status string) *model.OptionContract {
	c := baseContract(id, status)
	c.RoutingNumber = 222 // != OwnRouting → Local=false (remote mirror)
	native := "native-" + kafkaUint(id)
	c.NativeID = &native
	bbc, sbc := "111", "222"
	c.BuyerBankCode = &bbc
	c.SellerBankCode = &sbc
	c.BuyerOwnerType = model.OwnerBank
	c.BuyerOwnerID = nil
	c.SellerOwnerType = model.OwnerBank
	c.SellerOwnerID = nil
	return c
}
