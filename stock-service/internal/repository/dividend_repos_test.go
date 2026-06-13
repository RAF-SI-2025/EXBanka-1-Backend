// Tests for DividendPaymentRepository and DividendPayoutRepository.
package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// DividendPaymentRepository
// ---------------------------------------------------------------------------

func newDividendPaymentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.DividendPayment{}); err != nil {
		t.Fatalf("migrate dividend_payments: %v", err)
	}
	return db
}

func TestDividendPaymentRepository_Create_And_GetByID(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	dp := &model.DividendPayment{
		SecurityID:           1,
		Ticker:               "AAPL",
		AmountPerShareRSD:    decimal.NewFromFloat(5.5),
		PaymentDate:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Status:               "declared",
		DeclaredByEmployeeID: 42,
	}
	if err := r.Create(dp); err != nil {
		t.Fatalf("create: %v", err)
	}
	if dp.ID == 0 {
		t.Fatal("expected non-zero id after create")
	}

	got, err := r.GetByID(dp.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Ticker != "AAPL" {
		t.Errorf("ticker mismatch: got %q", got.Ticker)
	}
}

func TestDividendPaymentRepository_Create_Idempotent(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	dp := &model.DividendPayment{
		SecurityID:           1,
		Ticker:               "AAPL",
		AmountPerShareRSD:    decimal.NewFromFloat(5.5),
		PaymentDate:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		DeclaredByEmployeeID: 42,
	}
	if err := r.Create(dp); err != nil {
		t.Fatalf("first create: %v", err)
	}
	origID := dp.ID

	// Second create with same (security_id, payment_date) should be idempotent
	dp2 := &model.DividendPayment{
		SecurityID:           1,
		Ticker:               "AAPL",
		AmountPerShareRSD:    decimal.NewFromFloat(6.0),
		PaymentDate:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		DeclaredByEmployeeID: 42,
	}
	if err := r.Create(dp2); err != nil {
		t.Fatalf("second create: %v", err)
	}
	// ID==0 signals "already exists, use GetBySecurityAndDate to re-fetch"
	if dp2.ID != 0 {
		t.Errorf("expected id=0 (already exists), got %d", dp2.ID)
	}
	_ = origID
}

func TestDividendPaymentRepository_GetByID_NotFound(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	if _, err := r.GetByID(9999); err == nil {
		t.Error("expected error for non-existent id")
	}
}

func TestDividendPaymentRepository_GetBySecurityAndDate(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	payDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	dp := &model.DividendPayment{
		SecurityID:           2,
		Ticker:               "MSFT",
		AmountPerShareRSD:    decimal.NewFromFloat(3.0),
		PaymentDate:          payDate,
		DeclaredByEmployeeID: 1,
	}
	if err := r.Create(dp); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.GetBySecurityAndDate(2, payDate)
	if err != nil {
		t.Fatalf("get by security and date: %v", err)
	}
	if got.Ticker != "MSFT" {
		t.Errorf("ticker mismatch: got %q", got.Ticker)
	}

	// not found
	if _, err := r.GetBySecurityAndDate(2, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("expected error for missing date")
	}
}

func TestDividendPaymentRepository_MarkPaidOut(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	dp := &model.DividendPayment{
		SecurityID:           3,
		Ticker:               "GOOG",
		AmountPerShareRSD:    decimal.NewFromFloat(10.0),
		PaymentDate:          time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Status:               "declared",
		DeclaredByEmployeeID: 1,
	}
	if err := r.Create(dp); err != nil {
		t.Fatalf("create: %v", err)
	}

	paidAt := time.Now().UTC()
	if err := r.MarkPaidOut(dp.ID, paidAt); err != nil {
		t.Fatalf("mark paid out: %v", err)
	}

	got, _ := r.GetByID(dp.ID)
	if got.Status != "paid_out" {
		t.Errorf("expected status=paid_out, got %q", got.Status)
	}
}

func TestDividendPaymentRepository_ListBySecurityID(t *testing.T) {
	db := newDividendPaymentTestDB(t)
	r := NewDividendPaymentRepository(db)

	for i := 1; i <= 3; i++ {
		if err := r.Create(&model.DividendPayment{
			SecurityID:           4,
			Ticker:               "IBM",
			AmountPerShareRSD:    decimal.NewFromFloat(float64(i)),
			PaymentDate:          time.Date(2026, time.Month(i), 1, 0, 0, 0, 0, time.UTC),
			DeclaredByEmployeeID: 1,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	rows, total, err := r.ListBySecurityID(4, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total=3, got %d", total)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}

	// page 2 with pageSize 2 should return 1 row
	rows2, total2, err := r.ListBySecurityID(4, 2, 2)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total2 != 3 {
		t.Errorf("expected total=3, got %d", total2)
	}
	if len(rows2) != 1 {
		t.Errorf("expected 1 row on page 2, got %d", len(rows2))
	}
}

// ---------------------------------------------------------------------------
// DividendPayoutRepository
// ---------------------------------------------------------------------------

func newDividendPayoutTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.DividendPayout{}); err != nil {
		t.Fatalf("migrate dividend_payouts: %v", err)
	}
	return db
}

func TestDividendPayoutRepository_Create_And_GetByIdempotencyKey(t *testing.T) {
	db := newDividendPayoutTestDB(t)
	r := NewDividendPayoutRepository(db)

	uid := uint64(7)
	payout := &model.DividendPayout{
		DividendPaymentID: 1,
		HoldingOwnerType:  "client",
		HoldingOwnerID:    &uid,
		HoldingID:         10,
		Shares:            100,
		GrossAmountRSD:    decimal.NewFromFloat(550),
		TaxAmountRSD:      decimal.NewFromFloat(82.5),
		NetAmountRSD:      decimal.NewFromFloat(467.5),
		CreditedAccountID: 5,
		IdempotencyKey:    "dividend-1-10",
	}

	if err := r.Create(db, payout); err != nil {
		t.Fatalf("create: %v", err)
	}
	if payout.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := r.GetByIdempotencyKey("dividend-1-10")
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if got.Shares != 100 {
		t.Errorf("shares mismatch: got %d", got.Shares)
	}
}

func TestDividendPayoutRepository_GetByIdempotencyKey_NotFound(t *testing.T) {
	db := newDividendPayoutTestDB(t)
	r := NewDividendPayoutRepository(db)

	if _, err := r.GetByIdempotencyKey("no-such-key"); err == nil {
		t.Error("expected error for missing key")
	}
}

func TestDividendPayoutRepository_ListByOwner(t *testing.T) {
	db := newDividendPayoutTestDB(t)
	r := NewDividendPayoutRepository(db)

	uid := uint64(42)
	for i := 1; i <= 3; i++ {
		if err := r.Create(db, &model.DividendPayout{
			DividendPaymentID: uint64(i),
			HoldingOwnerType:  "client",
			HoldingOwnerID:    &uid,
			HoldingID:         uint64(i),
			Shares:            int64(i * 10),
			GrossAmountRSD:    decimal.NewFromFloat(float64(i * 100)),
			NetAmountRSD:      decimal.NewFromFloat(float64(i * 85)),
			CreditedAccountID: 1,
			IdempotencyKey:    "k-" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	rows, total, err := r.ListByOwner("client", &uid, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d want 3", total)
	}
	if len(rows) != 3 {
		t.Errorf("rows=%d want 3", len(rows))
	}

	// nil owner_id (bank owner)
	if err := r.Create(db, &model.DividendPayout{
		DividendPaymentID: 99,
		HoldingOwnerType:  "bank",
		HoldingOwnerID:    nil,
		HoldingID:         99,
		Shares:            1,
		GrossAmountRSD:    decimal.NewFromFloat(10),
		NetAmountRSD:      decimal.NewFromFloat(10),
		CreditedAccountID: 1,
		IdempotencyKey:    "k-bank",
	}); err != nil {
		t.Fatalf("create bank: %v", err)
	}
	bankRows, bankTotal, err := r.ListByOwner("bank", nil, 1, 10)
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if bankTotal != 1 || len(bankRows) != 1 {
		t.Errorf("bank: total=%d rows=%d", bankTotal, len(bankRows))
	}
}

func TestDividendPayoutRepository_SumNetByOwner(t *testing.T) {
	db := newDividendPayoutTestDB(t)
	r := NewDividendPayoutRepository(db)

	uid := uint64(5)
	for i := 1; i <= 2; i++ {
		if err := r.Create(db, &model.DividendPayout{
			DividendPaymentID: uint64(i),
			HoldingOwnerType:  "client",
			HoldingOwnerID:    &uid,
			HoldingID:         uint64(i),
			Shares:            10,
			GrossAmountRSD:    decimal.NewFromFloat(100),
			NetAmountRSD:      decimal.NewFromFloat(85),
			CreditedAccountID: 1,
			IdempotencyKey:    "sum-" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	sum, err := r.SumNetByOwner("client", &uid)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if !sum.Equal(decimal.NewFromFloat(170)) {
		t.Errorf("sum=%s want 170", sum)
	}

	// sum for unknown owner should be zero
	other := uint64(999)
	sum2, err := r.SumNetByOwner("client", &other)
	if err != nil {
		t.Fatalf("sum unknown: %v", err)
	}
	if !sum2.IsZero() {
		t.Errorf("expected zero, got %s", sum2)
	}
}

func TestDividendPayoutRepository_ListByPaymentID(t *testing.T) {
	db := newDividendPayoutTestDB(t)
	r := NewDividendPayoutRepository(db)

	uid := uint64(10)
	paymentID := uint64(77)
	for i := 1; i <= 2; i++ {
		if err := r.Create(db, &model.DividendPayout{
			DividendPaymentID: paymentID,
			HoldingOwnerType:  "client",
			HoldingOwnerID:    &uid,
			HoldingID:         uint64(i),
			Shares:            5,
			GrossAmountRSD:    decimal.NewFromFloat(50),
			NetAmountRSD:      decimal.NewFromFloat(42.5),
			CreditedAccountID: 1,
			IdempotencyKey:    "pay-" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	rows, err := r.ListByPaymentID(paymentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}

	// empty for unrelated payment id
	empty, err := r.ListByPaymentID(9999)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty, got %d", len(empty))
	}
}
