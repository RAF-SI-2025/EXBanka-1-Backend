// Tests for FundDividendPaymentRepository.
package repository

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newFundDividendPaymentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundDividendPayment{}); err != nil {
		t.Fatalf("migrate fund_dividend_payments: %v", err)
	}
	return db
}

func TestFundDividendPaymentRepository_Create_And_SumByFundID(t *testing.T) {
	db := newFundDividendPaymentTestDB(t)
	r := NewFundDividendPaymentRepository(db)

	for i := 1; i <= 3; i++ {
		fdp := &model.FundDividendPayment{
			DividendPaymentID:   uint64(i),
			FundID:              1,
			AmountRSD:           decimal.NewFromFloat(float64(i * 100)),
			PerInvestorSnapshot: "[]",
		}
		if err := r.Create(db, fdp); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if fdp.ID == 0 {
			t.Errorf("expected non-zero id for row %d", i)
		}
	}

	sum, err := r.SumByFundID(1)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	expected := decimal.NewFromFloat(600) // 100+200+300
	if !sum.Equal(expected) {
		t.Errorf("sum=%s want 600", sum)
	}

	// sum for fund with no payments should be zero
	sum2, err := r.SumByFundID(9999)
	if err != nil {
		t.Fatalf("sum empty: %v", err)
	}
	if !sum2.IsZero() {
		t.Errorf("expected zero sum, got %s", sum2)
	}
}

func TestFundDividendPaymentRepository_ListByFundID(t *testing.T) {
	db := newFundDividendPaymentTestDB(t)
	r := NewFundDividendPaymentRepository(db)

	fundID := uint64(2)
	for i := 1; i <= 5; i++ {
		fdp := &model.FundDividendPayment{
			DividendPaymentID:   uint64(i),
			FundID:              fundID,
			AmountRSD:           decimal.NewFromFloat(float64(i * 50)),
			PerInvestorSnapshot: "[]",
		}
		if err := r.Create(db, fdp); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	rows, total, err := r.ListByFundID(fundID, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Errorf("total=%d want 5", total)
	}
	if len(rows) != 5 {
		t.Errorf("rows=%d want 5", len(rows))
	}

	// page 2 with pageSize 3 → 2 remaining
	rows2, total2, err := r.ListByFundID(fundID, 2, 3)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total2 != 5 {
		t.Errorf("total=%d want 5", total2)
	}
	if len(rows2) != 2 {
		t.Errorf("rows=%d want 2", len(rows2))
	}
}

func TestFundDividendPaymentRepository_ListByDividendPaymentID(t *testing.T) {
	db := newFundDividendPaymentTestDB(t)
	r := NewFundDividendPaymentRepository(db)

	paymentID := uint64(77)
	for _, fundID := range []uint64{10, 20, 30} {
		fdp := &model.FundDividendPayment{
			DividendPaymentID:   paymentID,
			FundID:              fundID,
			AmountRSD:           decimal.NewFromFloat(200),
			PerInvestorSnapshot: "[]",
		}
		if err := r.Create(db, fdp); err != nil {
			t.Fatalf("create fund %d: %v", fundID, err)
		}
	}

	rows, err := r.ListByDividendPaymentID(paymentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3, got %d", len(rows))
	}

	// empty for unknown payment id
	empty, err := r.ListByDividendPaymentID(9999)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty, got %d", len(empty))
	}
}
