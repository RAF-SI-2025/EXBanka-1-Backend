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

// newHoldingReservationFixture builds a HoldingReservationService backed by an
// in-memory SQLite DB. Seeds a single Holding with Quantity=100,
// ReservedQuantity=0 (userID=1, systemType="client", securityType="stock",
// securityID=10, accountID=1) and returns the service, the holding repo, and
// the seeded holding.
func newHoldingReservationFixture(t *testing.T) (*HoldingReservationService, *repository.HoldingRepository, *model.Holding) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
	))

	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	svc := NewHoldingReservationService(db, holdingRepo, resRepo)

	h := &model.Holding{
		UserID:           1,
		SystemType:       "client",
		UserFirstName:    "Test",
		UserLastName:     "User",
		SecurityType:     "stock",
		SecurityID:       10,
		ListingID:        100,
		Ticker:           "TEST",
		Name:             "Test Stock",
		Quantity:         100,
		AveragePrice:     decimal.NewFromInt(50),
		ReservedQuantity: 0,
		AccountID:        1,
		Version:          1,
	}
	require.NoError(t, db.Create(h).Error)
	return svc, holdingRepo, h
}

// orderKeys captures the standard "client=1, stock, securityID=10, acct=1" keys
// used across every test so we can pass them with less line noise.
func orderKeys() (userID uint64, systemType, securityType string, securityID, accountID uint64) {
	return 1, "client", "stock", 10, 1
}

func TestHoldingReservation_Reserve_HappyPath(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()

	out, err := svc.Reserve(context.Background(), userID, systemType, securityType, securityID, accountID, 500, 30)
	require.NoError(t, err)
	if out.ReservedQuantity != 30 {
		t.Errorf("ReservedQuantity: got %d want 30", out.ReservedQuantity)
	}
	if out.AvailableQuantity != 70 {
		t.Errorf("AvailableQuantity: got %d want 70", out.AvailableQuantity)
	}
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.Quantity != 100 {
		t.Errorf("Quantity must be unchanged by Reserve: got %d want 100", h.Quantity)
	}
	if h.ReservedQuantity != 30 {
		t.Errorf("holding.ReservedQuantity: got %d want 30", h.ReservedQuantity)
	}
}

func TestHoldingReservation_Reserve_Idempotent(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	r1, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 30)
	require.NoError(t, err)
	r2, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 30)
	require.NoError(t, err)
	if r1.ReservationID != r2.ReservationID {
		t.Errorf("retry produced different reservation id (%d vs %d)", r1.ReservationID, r2.ReservationID)
	}
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.ReservedQuantity != 30 {
		t.Errorf("retry double-counted ReservedQuantity: got %d want 30", h.ReservedQuantity)
	}
}

func TestHoldingReservation_Reserve_InsufficientAvailable(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()

	_, err := svc.Reserve(context.Background(), userID, systemType, securityType, securityID, accountID, 500, 200)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestHoldingReservation_Reserve_HoldingMissing(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)

	// Different user → no matching holding.
	_, err := svc.Reserve(context.Background(), 9999, "client", "stock", 10, 1, 500, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestHoldingReservation_PartialSettle_HappyPath(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 50)
	require.NoError(t, err)

	out, err := svc.PartialSettle(ctx, 500, 9001, 20)
	require.NoError(t, err)
	if out.SettledQuantity != 20 {
		t.Errorf("SettledQuantity: got %d want 20", out.SettledQuantity)
	}
	if out.QuantityAfter != 80 {
		t.Errorf("QuantityAfter: got %d want 80", out.QuantityAfter)
	}
	if out.RemainingReserved != 30 {
		t.Errorf("RemainingReserved: got %d want 30", out.RemainingReserved)
	}

	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.Quantity != 80 {
		t.Errorf("holding.Quantity: got %d want 80", h.Quantity)
	}
	if h.ReservedQuantity != 30 {
		t.Errorf("holding.ReservedQuantity: got %d want 30", h.ReservedQuantity)
	}
}

func TestHoldingReservation_PartialSettle_IdempotentOnTxnID(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 50)
	require.NoError(t, err)

	_, err = svc.PartialSettle(ctx, 500, 9001, 20)
	require.NoError(t, err)
	// Second call with same orderTransactionID must be a no-op.
	_, err = svc.PartialSettle(ctx, 500, 9001, 20)
	if err != nil {
		t.Fatalf("expected idempotent no-op, got error: %v", err)
	}
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.Quantity != 80 {
		t.Errorf("retry double-debited Quantity: got %d want 80", h.Quantity)
	}
	if h.ReservedQuantity != 30 {
		t.Errorf("retry double-debited ReservedQuantity: got %d want 30", h.ReservedQuantity)
	}
}

func TestHoldingReservation_PartialSettle_OverReservation(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 30)
	require.NoError(t, err)

	_, err = svc.PartialSettle(ctx, 500, 9001, 50)
	if err == nil {
		t.Fatal("expected over-reservation error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
	// State should be unchanged.
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.Quantity != 100 {
		t.Errorf("Quantity must be unchanged on failure: got %d want 100", h.Quantity)
	}
	if h.ReservedQuantity != 30 {
		t.Errorf("ReservedQuantity must be unchanged on failure: got %d want 30", h.ReservedQuantity)
	}
}

func TestHoldingReservation_PartialSettle_FullyFills_TransitionsToSettled(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 30)
	require.NoError(t, err)
	_, err = svc.PartialSettle(ctx, 500, 9001, 30)
	require.NoError(t, err)

	// Reservation must now be settled — release should be a no-op (0).
	rel, err := svc.Release(ctx, 500)
	require.NoError(t, err)
	if rel.ReleasedQuantity != 0 {
		t.Errorf("expected 0 released after full settle, got %d", rel.ReleasedQuantity)
	}
}

func TestHoldingReservation_Release_ActiveReservation(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 50)
	require.NoError(t, err)

	rel, err := svc.Release(ctx, 500)
	require.NoError(t, err)
	if rel.ReleasedQuantity != 50 {
		t.Errorf("ReleasedQuantity: got %d want 50", rel.ReleasedQuantity)
	}
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.ReservedQuantity != 0 {
		t.Errorf("ReservedQuantity: got %d want 0", h.ReservedQuantity)
	}
	if h.Quantity != 100 {
		t.Errorf("Quantity must be unchanged by Release: got %d want 100", h.Quantity)
	}
}

func TestHoldingReservation_Release_Idempotent(t *testing.T) {
	svc, _, _ := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 50)
	require.NoError(t, err)
	if _, err := svc.Release(ctx, 500); err != nil {
		t.Fatal(err)
	}
	rel, err := svc.Release(ctx, 500)
	if err != nil {
		t.Fatal("second release should be no-op, got error:", err)
	}
	if rel.ReleasedQuantity != 0 {
		t.Errorf("second release should release 0, got %d", rel.ReleasedQuantity)
	}
}

func TestHoldingReservation_Release_PartialThenRelease(t *testing.T) {
	svc, holdingRepo, seeded := newHoldingReservationFixture(t)
	userID, systemType, securityType, securityID, accountID := orderKeys()
	ctx := context.Background()

	_, err := svc.Reserve(ctx, userID, systemType, securityType, securityID, accountID, 500, 50)
	require.NoError(t, err)
	_, err = svc.PartialSettle(ctx, 500, 9001, 20)
	require.NoError(t, err)

	rel, err := svc.Release(ctx, 500)
	require.NoError(t, err)
	if rel.ReleasedQuantity != 30 {
		t.Errorf("ReleasedQuantity: got %d want 30", rel.ReleasedQuantity)
	}
	h, err := holdingRepo.GetByID(seeded.ID)
	require.NoError(t, err)
	if h.ReservedQuantity != 0 {
		t.Errorf("ReservedQuantity: got %d want 0", h.ReservedQuantity)
	}
	if h.Quantity != 80 {
		t.Errorf("Quantity must reflect the 20-share fill: got %d want 80", h.Quantity)
	}
}
