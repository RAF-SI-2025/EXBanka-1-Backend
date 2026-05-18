package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
)

// TestHoldingReservation_Reserve_BankOwnedHolding exercises the Reserve path
// where ownerID is nil (bank-owned holding). Mirrors the regular client path
// but with the OwnerType=bank branch.
func TestHoldingReservation_Reserve_BankOwnedHolding(t *testing.T) {
	svc, holdingRepo, _ := newHoldingReservationFixture(t)
	// Seed a bank-owned holding for the same security id used by the main
	// fixture's client holding (10).
	bankHolding := &model.Holding{
		OwnerType: model.OwnerBank, OwnerID: nil,
		SecurityType: "stock", SecurityID: 11, Ticker: "BANK",
		Quantity: 50, AveragePrice: decimal.NewFromInt(40),
	}
	require.NoError(t, holdingRepo.Upsert(context.Background(), bankHolding))

	out, err := svc.Reserve(context.Background(), model.OwnerBank, nil, "stock", 11, 999, 7)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.ReservedQuantity != 7 {
		t.Errorf("got reserved=%d", out.ReservedQuantity)
	}
}

// TestHoldingReservation_Release_BankOwnedHolding mirrors Release for bank.
func TestHoldingReservation_Release_BankOwnedHolding(t *testing.T) {
	svc, holdingRepo, _ := newHoldingReservationFixture(t)
	bankHolding := &model.Holding{
		OwnerType: model.OwnerBank, OwnerID: nil,
		SecurityType: "stock", SecurityID: 12, Ticker: "BANKB",
		Quantity: 30, AveragePrice: decimal.NewFromInt(40),
	}
	require.NoError(t, holdingRepo.Upsert(context.Background(), bankHolding))
	_, err := svc.Reserve(context.Background(), model.OwnerBank, nil, "stock", 12, 1001, 10)
	require.NoError(t, err)
	out, err := svc.Release(context.Background(), 1001)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.ReleasedQuantity != 10 {
		t.Errorf("released=%d want 10", out.ReleasedQuantity)
	}
}
