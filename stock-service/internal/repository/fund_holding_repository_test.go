package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestFundHoldingRepository_Upsert_InsertThenWeightedAverage exercises the
// buy-side upsert: the first call inserts a row, the second (same
// fund/security) must add quantity and recompute average_price_rsd as a
// weighted average. This guards the ON CONFLICT expression that previously
// failed on Postgres with "operator is not unique: unknown * unknown" because
// the (? * ?) product multiplied two untyped bind parameters; the fix casts
// them to numeric. (SQLite can't reproduce the type error, but this still
// pins the upsert/weighted-average semantics.)
func TestFundHoldingRepository_Upsert_InsertThenWeightedAverage(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundHolding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundHoldingRepository(db)

	// First buy: 5 @ 38.
	if err := r.Upsert(&model.FundHolding{
		FundID: 1, SecurityType: "stock", SecurityID: 16,
		Quantity: 5, AveragePriceRSD: decimal.NewFromInt(38),
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	rows, err := r.ListByFundFIFO(1)
	if err != nil {
		t.Fatalf("list after first: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 5 {
		t.Fatalf("after first upsert want qty 5, got %+v", rows)
	}

	// Second buy: 5 @ 42. Weighted avg = (5*38 + 5*42) / 10 = 40.
	if err := r.Upsert(&model.FundHolding{
		FundID: 1, SecurityType: "stock", SecurityID: 16,
		Quantity: 5, AveragePriceRSD: decimal.NewFromInt(42),
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	rows, err = r.ListByFundFIFO(1)
	if err != nil {
		t.Fatalf("list after second: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want a single merged row, got %d", len(rows))
	}
	if rows[0].Quantity != 10 {
		t.Fatalf("want merged qty 10, got %d", rows[0].Quantity)
	}
	if !rows[0].AveragePriceRSD.Equal(decimal.NewFromInt(40)) {
		t.Fatalf("want weighted average 40, got %s", rows[0].AveragePriceRSD.String())
	}
}
