package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func TestReserveForCrossBankNewTx_InputValidation(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	uid := uint64(1)
	if _, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", "TEST", "tx-1", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("qty<=0 → InvalidArgument, got %v", err)
	}
	if _, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", "TEST", "", 5); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty crossbankTxID → InvalidArgument, got %v", err)
	}
}

func TestReleaseForCrossBankNewTx_NotFound_And_NotActive(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	// Missing tx id → no-op.
	out, err := svc.ReleaseForCrossBankNewTx(context.Background(), "no-such-tx")
	if err != nil || out.ReleasedQuantity != 0 {
		t.Fatalf("missing crossbank reservation should be no-op, got %+v err=%v", out, err)
	}
	// Reserve then release twice — second is a no-op.
	uid := uint64(1)
	if _, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", "TEST", "tx-9", 5); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := svc.ReleaseForCrossBankNewTx(context.Background(), "tx-9"); err != nil {
		t.Fatalf("first release: %v", err)
	}
	out, err = svc.ReleaseForCrossBankNewTx(context.Background(), "tx-9")
	if err != nil || out.ReleasedQuantity != 0 {
		t.Fatalf("second release should be no-op, got %+v err=%v", out, err)
	}
}

// peerExerciseSvc builds a HoldingReservationService whose DB also carries the
// unified option_contracts table so the ExerciseBuyerCreditForPeerOption guard
// (which reads the remote contract status) can run.
func peerExerciseSvc(t *testing.T) (*HoldingReservationService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Holding{}, &model.HoldingReservation{}, &model.HoldingReservationSettlement{}, &model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewHoldingReservationService(db, repository.NewHoldingRepository(db), repository.NewHoldingReservationRepository(db))
	return svc, db
}

func TestExerciseBuyerCreditForPeerOption_Branches(t *testing.T) {
	svc, db := peerExerciseSvc(t)
	buyer := uint64(7)

	// Bad qty.
	if err := svc.ExerciseBuyerCreditForPeerOption(context.Background(), 1, model.OwnerClient, &buyer, "AAA", 0, decimal.NewFromInt(10)); status.Code(err) != codes.InvalidArgument {
		t.Errorf("qty<=0 → InvalidArgument, got %v", err)
	}

	// No remote contract → FailedPrecondition.
	if err := svc.ExerciseBuyerCreditForPeerOption(context.Background(), 999, model.OwnerClient, &buyer, "AAA", 5, decimal.NewFromInt(10)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("missing remote contract → FailedPrecondition, got %v", err)
	}

	// Remote contract in a non-exercisable status (e.g. "expired") → FailedPrecondition.
	expired := buildRemoteContract(10, "expired")
	if err := db.Create(expired).Error; err != nil {
		t.Fatalf("create expired remote: %v", err)
	}
	if err := svc.ExerciseBuyerCreditForPeerOption(context.Background(), expired.ID, model.OwnerClient, &buyer, "AAPL", 5, decimal.NewFromInt(10)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expired contract → FailedPrecondition, got %v", err)
	}

	// Already exercised → no-op (nil).
	done := buildRemoteContract(11, "exercised")
	if err := db.Create(done).Error; err != nil {
		t.Fatalf("create exercised remote: %v", err)
	}
	if err := svc.ExerciseBuyerCreditForPeerOption(context.Background(), done.ID, model.OwnerClient, &buyer, "AAPL", 5, decimal.NewFromInt(10)); err != nil {
		t.Errorf("already-exercised should be a no-op, got %v", err)
	}

	// Active → credits the buyer and flips the contract to exercised.
	active := buildRemoteContract(12, "active")
	if err := db.Create(active).Error; err != nil {
		t.Fatalf("create active remote: %v", err)
	}
	if err := svc.ExerciseBuyerCreditForPeerOption(context.Background(), active.ID, model.OwnerClient, &buyer, "AAPL", 5, decimal.NewFromInt(100)); err != nil {
		t.Fatalf("exercise active: %v", err)
	}
	var reloaded model.OptionContract
	if err := db.First(&reloaded, active.ID).Error; err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	if reloaded.Status != "exercised" {
		t.Errorf("contract status = %s, want exercised", reloaded.Status)
	}
	// Buyer now holds 5 AAPL.
	var h model.Holding
	if err := db.Where("owner_id = ? AND ticker = ?", buyer, "AAPL").First(&h).Error; err != nil {
		t.Fatalf("buyer holding: %v", err)
	}
	if h.Quantity != 5 {
		t.Errorf("buyer holding qty = %d, want 5", h.Quantity)
	}
}
