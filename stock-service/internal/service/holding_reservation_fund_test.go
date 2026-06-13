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

// newFundReservationFixture builds a HoldingReservationService over an
// in-memory SQLite DB seeded with ONE fund_holdings row (fundID=7,
// securityType="stock", securityID=10, Quantity=100, Reserved=0) so the
// on-behalf-of-fund reserve/settle/release lifecycle can be exercised against
// fund_holdings — the mirror of the bank/user holdings fixture.
func newFundReservationFixture(t *testing.T) (*HoldingReservationService, *repository.FundHoldingRepository, *model.FundHolding) {
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
		&model.FundHolding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
	))

	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	fundHoldingRepo := repository.NewFundHoldingRepository(db)
	svc := NewHoldingReservationService(db, holdingRepo, resRepo)

	fh := &model.FundHolding{
		FundID:           7,
		SecurityType:     "stock",
		SecurityID:       10,
		Quantity:         100,
		ReservedQuantity: 0,
		AveragePriceRSD:  decimal.NewFromInt(50),
		Version:          1,
	}
	require.NoError(t, db.Create(fh).Error)
	return svc, fundHoldingRepo, fh
}

func TestReserveFund_HappyPath_LocksFundHolding(t *testing.T) {
	svc, fundRepo, seeded := newFundReservationFixture(t)

	out, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)
	require.Equal(t, int64(30), out.ReservedQuantity)
	require.Equal(t, int64(70), out.AvailableQuantity)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.Quantity, "Quantity unchanged by reserve")
	require.Equal(t, int64(30), got.ReservedQuantity, "reserved moved into ReservedQuantity")
	_ = seeded
}

func TestReserveFund_InsufficientAvailable(t *testing.T) {
	svc, _, _ := newFundReservationFixture(t)
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 101)
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestReserveFund_FundHoldingMissing(t *testing.T) {
	svc, _, _ := newFundReservationFixture(t)
	// Different fund id — no holding exists for it.
	_, err := svc.ReserveFund(context.Background(), 999, "stock", 10, 500, 1)
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestReserveFund_IdempotentOnOrderID(t *testing.T) {
	svc, fundRepo, _ := newFundReservationFixture(t)
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)
	// Replay with the same order id must not double-reserve.
	out, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)
	require.Equal(t, int64(30), out.ReservedQuantity)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(30), got.ReservedQuantity, "replay must not add a second 30")
}

func TestPartialSettleFund_DrawsDownFundHolding(t *testing.T) {
	svc, fundRepo, _ := newFundReservationFixture(t)
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)

	// Settle 20 of the 30 reserved.
	out, err := svc.PartialSettle(context.Background(), 500, 9001, 20)
	require.NoError(t, err)
	require.Equal(t, int64(20), out.SettledQuantity)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(80), got.Quantity, "shares physically left the fund holding")
	require.Equal(t, int64(10), got.ReservedQuantity, "reserved dropped by the settled amount")
}

func TestPartialSettleFund_IdempotentOnTxnID(t *testing.T) {
	svc, fundRepo, _ := newFundReservationFixture(t)
	// Reserve 50 (mirrors the bank idempotency test): the replay re-runs the
	// SumSettlements exceed-check with the prior settlement still counted, so
	// the reservation must leave headroom (40 <= 50) for the ON-CONFLICT replay
	// guard to be the thing that no-ops the second call.
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 50)
	require.NoError(t, err)

	_, err = svc.PartialSettle(context.Background(), 500, 9001, 20)
	require.NoError(t, err)
	// Replay of the same order-transaction id must move nothing.
	_, err = svc.PartialSettle(context.Background(), 500, 9001, 20)
	require.NoError(t, err)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(80), got.Quantity, "replay must not double-decrement")
	require.Equal(t, int64(30), got.ReservedQuantity, "50 reserved − 20 settled, replay no-op")
}

func TestReleaseFund_ReturnsRemainderToAvailable(t *testing.T) {
	svc, fundRepo, _ := newFundReservationFixture(t)
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)
	// Settle 20, then release the order — 10 reserved remain and must return.
	_, err = svc.PartialSettle(context.Background(), 500, 9001, 20)
	require.NoError(t, err)

	out, err := svc.Release(context.Background(), 500)
	require.NoError(t, err)
	require.Equal(t, int64(10), out.ReleasedQuantity)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(80), got.Quantity, "settled shares stayed gone")
	require.Equal(t, int64(0), got.ReservedQuantity, "unsettled remainder unlocked")
}

// A full reserve→settle (no leftover) then release is a clean no-op release.
func TestReleaseFund_AfterFullSettle_NoOp(t *testing.T) {
	svc, fundRepo, _ := newFundReservationFixture(t)
	_, err := svc.ReserveFund(context.Background(), 7, "stock", 10, 500, 30)
	require.NoError(t, err)
	_, err = svc.PartialSettle(context.Background(), 500, 9001, 30)
	require.NoError(t, err)

	out, err := svc.Release(context.Background(), 500)
	require.NoError(t, err)
	require.Equal(t, int64(0), out.ReleasedQuantity)

	got, err := fundRepo.GetByFundAndSecurity(7, "stock", 10)
	require.NoError(t, err)
	require.Equal(t, int64(70), got.Quantity)
	require.Equal(t, int64(0), got.ReservedQuantity)
}
