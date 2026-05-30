package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestHoldingReservationService_PartialSettle_BadQty exercises the
// qty<=0 input-validation branch.
func TestHoldingReservationService_PartialSettle_BadQty(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.PartialSettle(context.Background(), 1, 1, 0)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestHoldingReservationService_PartialSettle_NoReservation exercises the
// not-found branch.
func TestHoldingReservationService_PartialSettle_NoReservation(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	_, err := svc.PartialSettle(context.Background(), 9999, 1, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

// ---------------- ReserveForPeerOptionContract ----------------

func TestHoldingReservationService_ReserveForPeerOptionContract_HappyPath(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	out, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 555, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.ReservedQuantity != 10 {
		t.Errorf("reserved=%d", out.ReservedQuantity)
	}
}

func TestHoldingReservationService_ReserveForPeerOptionContract_BadQty(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", "TEST", 1, 0)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestHoldingReservationService_ReserveForPeerOptionContract_HoldingMissing(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	uid := uint64(99)
	_, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", "OTHER", 1, 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

func TestHoldingReservationService_ReserveForPeerOptionContract_Insufficient(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 555, 9999)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition for insufficient, got %v", err)
	}
}

// Idempotent: second call with same contract id returns the same reservation
// without doubling the reserved quantity.
func TestHoldingReservationService_ReserveForPeerOptionContract_Idempotent(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	first, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 555, 5)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 555, 5)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ReservationID != second.ReservationID {
		t.Errorf("expected same reservation id, got %d vs %d", first.ReservationID, second.ReservationID)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 5 {
		t.Errorf("expected reserved=5, got %d", got.ReservedQuantity)
	}
}

// ---------------- ReleaseForPeerOptionContract ----------------

func TestHoldingReservationService_ReleaseForPeerOptionContract(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 100, 7)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	out, err := svc.ReleaseForPeerOptionContract(context.Background(), 100)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if out.ReleasedQuantity != 7 {
		t.Errorf("released=%d", out.ReleasedQuantity)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.ReservedQuantity != 0 {
		t.Errorf("expected reserved=0 after release, got %d", got.ReservedQuantity)
	}
}

// ---------------- ConsumeForPeerOptionContract ----------------

func TestHoldingReservationService_ConsumeForPeerOptionContract(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	uid := uint64(1)
	_, err := svc.ReserveForPeerOptionContract(context.Background(),
		model.OwnerClient, &uid, "stock", h.Ticker, 200, 5)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := svc.ConsumeForPeerOptionContract(context.Background(), 200, 5); err != nil {
		t.Fatalf("consume: %v", err)
	}
	got, _ := holdingRepo.GetByID(h.ID)
	if got.Quantity != 95 || got.ReservedQuantity != 0 {
		t.Errorf("post-consume holding: qty=%d reserved=%d", got.Quantity, got.ReservedQuantity)
	}
}

// ---------------- CreditBuyerHoldingForPeerOption ----------------

func TestHoldingReservationService_CreditBuyerHoldingForPeerOption_NewBuyer(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	buyerUID := uint64(99)
	strike := decimal.NewFromInt(150)
	if err := svc.CreditBuyerHoldingForPeerOption(context.Background(), model.OwnerClient, &buyerUID, h.Ticker, 5, strike); err != nil {
		t.Fatalf("credit: %v", err)
	}
	got, err := holdingRepo.GetByOwnerAndTicker(model.OwnerClient, &buyerUID, "stock", h.Ticker)
	if err != nil {
		t.Fatalf("lookup buyer holding: %v", err)
	}
	if got.Quantity != 5 {
		t.Errorf("buyer qty=%d want 5", got.Quantity)
	}
	if !got.AveragePrice.Equal(strike) {
		t.Errorf("buyer average_price=%s want 150 (strike)", got.AveragePrice)
	}
}

// TestHoldingReservationService_ExerciseBuyerCreditForPeerOption_Idempotent
// is the Bug D regression: a replayed cross-bank exercise (duplicate
// COMMIT_TX) must credit the buyer's shares exactly once. The contract
// status, flipped to "exercised" in the same transaction as the credit and
// read under a row lock, is the idempotency guard.
func TestHoldingReservationService_ExerciseBuyerCreditForPeerOption_Idempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
		&model.PeerOptionContract{},
	))
	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	svc := NewHoldingReservationService(db, holdingRepo, resRepo)

	strike := decimal.NewFromInt(150)
	contract := &model.PeerOptionContract{
		CrossbankTxID: "tx-idem", PostingIndex: 0,
		NegotiationRoutingNumber: 222, NegotiationID: "neg",
		BuyerRoutingNumber: 111, BuyerID: "client-99",
		SellerRoutingNumber: 222, SellerID: "client-1",
		Ticker: "AAPL", Quantity: 5, StrikePrice: strike,
		Currency: "USD", SettlementDate: "2026-12-31",
		Direction: "CREDIT", Status: "active",
	}
	require.NoError(t, db.Create(contract).Error)

	buyer := uint64(99)
	// First exercise: credits 5 shares and flips status to exercised.
	require.NoError(t, svc.ExerciseBuyerCreditForPeerOption(context.Background(), contract.ID, model.OwnerClient, &buyer, "AAPL", 5, strike))
	// Replay: must be a no-op (contract already exercised), NOT a second credit.
	require.NoError(t, svc.ExerciseBuyerCreditForPeerOption(context.Background(), contract.ID, model.OwnerClient, &buyer, "AAPL", 5, strike))

	got, err := holdingRepo.GetByOwnerAndTicker(model.OwnerClient, &buyer, "stock", "AAPL")
	require.NoError(t, err)
	if got.Quantity != 5 {
		t.Errorf("buyer qty=%d want 5 — replayed exercise double-credited", got.Quantity)
	}
	var reloaded model.PeerOptionContract
	require.NoError(t, db.First(&reloaded, contract.ID).Error)
	if reloaded.Status != "exercised" {
		t.Errorf("contract status=%q want exercised", reloaded.Status)
	}
}

// CreditBuyerHoldingForPeerOption with an existing holding produces a
// weighted-average cost basis across the pre-existing shares and the
// strike-priced shares. Without this, two cross-bank exercises into the
// same ticker would silently overwrite cost basis.
func TestHoldingReservationService_CreditBuyerHoldingForPeerOption_WeightedAverage(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t)
	buyerUID := uint64(99)
	// Seed an existing position: 10 shares at avg=100.
	pre := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &buyerUID,
		SecurityType: "stock", Ticker: h.Ticker, Name: h.Ticker,
		Quantity: 10, AveragePrice: decimal.NewFromInt(100),
	}
	if err := holdingRepo.Upsert(context.Background(), pre); err != nil {
		t.Fatalf("seed: %v", err)
	}
	strike := decimal.NewFromInt(200)
	if err := svc.CreditBuyerHoldingForPeerOption(context.Background(), model.OwnerClient, &buyerUID, h.Ticker, 10, strike); err != nil {
		t.Fatalf("credit: %v", err)
	}
	got, err := holdingRepo.GetByOwnerAndTicker(model.OwnerClient, &buyerUID, "stock", h.Ticker)
	if err != nil {
		t.Fatalf("lookup buyer holding: %v", err)
	}
	if got.Quantity != 20 {
		t.Errorf("buyer qty=%d want 20", got.Quantity)
	}
	// (10 × 100 + 10 × 200) / 20 = 150
	want := decimal.NewFromInt(150)
	if !got.AveragePrice.Equal(want) {
		t.Errorf("buyer average_price=%s want %s (weighted avg)", got.AveragePrice, want)
	}
}

// TestReserveForCrossBankNewTx_NoOverReservation is the regression for the
// concurrent over-reservation bug: distinct crossbank tx ids must not reserve
// more shares in aggregate than the holding owns. The atomic guarded UPDATE
// re-checks availability in its WHERE, so a reserve that would exceed the
// holding fails even with a fresh (insertable) tx id.
func TestReserveForCrossBankNewTx_NoOverReservation(t *testing.T) {
	svc, holdingRepo, h := newHoldingReservationFixture(t) // holding qty=100, ticker TEST, owner 1
	uid := uint64(1)
	// Force a non-zero version so the reserve must succeed against a real version
	// — guards against the BeforeUpdate-hook regression where a zero-value model
	// injected a stale `version = 0` predicate into the increment and matched 0 rows.
	require.NoError(t, holdingRepo.DB().Exec("UPDATE holdings SET version = 7 WHERE id = ?", h.ID).Error)

	r1, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", h.Ticker, "222:tx-a", 60)
	require.NoError(t, err)
	require.Equal(t, int64(60), r1.ReservedQuantity)

	// Second reserve of 60 (distinct tx) must FAIL — only 40 free — and not inflate reserved.
	_, err = svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", h.Ticker, "222:tx-b", 60)
	require.Error(t, err, "second reserve must fail: only 40 of 100 free")

	got, err := holdingRepo.GetByOwnerAndTicker(model.OwnerClient, &uid, "stock", h.Ticker)
	require.NoError(t, err)
	require.Equal(t, int64(60), got.ReservedQuantity, "reserved must stay 60, never exceed the 100 owned")

	// A reserve that exactly fits the remaining 40 succeeds.
	r3, err := svc.ReserveForCrossBankNewTx(context.Background(), model.OwnerClient, &uid, "stock", h.Ticker, "222:tx-c", 40)
	require.NoError(t, err)
	require.Equal(t, int64(100), r3.ReservedQuantity)
}
