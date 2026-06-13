package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestDividend_Declare_Validation(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	ctx := context.Background()
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	if _, err := svc.Declare(ctx, 0, "AAPL", decimal.NewFromInt(10), date, 1); err == nil {
		t.Error("security_id 0 should error")
	}
	if _, err := svc.Declare(ctx, 42, "", decimal.NewFromInt(10), date, 1); err == nil {
		t.Error("empty ticker should error")
	}
	if _, err := svc.Declare(ctx, 42, "AAPL", decimal.Zero, date, 1); err == nil {
		t.Error("non-positive amount should error")
	}
}

func TestDividend_Payout_StateGuards(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	ctx := context.Background()

	// Missing payment → error.
	if _, err := svc.Payout(ctx, 99999); err == nil {
		t.Error("missing payment should error")
	}

	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	payment, err := svc.Declare(ctx, 42, "AAPL", decimal.NewFromInt(10), date, 1)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	// First payout (no holdings → empty summary) marks the payment paid_out.
	if _, err := svc.Payout(ctx, payment.ID); err != nil {
		t.Fatalf("first payout: %v", err)
	}
	// Second payout → already paid out.
	if _, err := svc.Payout(ctx, payment.ID); err == nil {
		t.Error("second payout should error (already paid out)")
	}
}
