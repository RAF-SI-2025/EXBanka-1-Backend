package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestTaxService_GetUserGainsAndTax exercises the rich portfolio summary
// endpoint over multiple gain windows + per-currency conversion paths.
func TestTaxService_GetUserGainsAndTax_HappyPath(t *testing.T) {
	svc, mocks := buildTaxService()
	now := time.Now()
	uid := uint64(42)

	// Record three gains, all in the current month → land in monthly,
	// yearly, and lifetime queries.
	for i, ccy := range []string{"RSD", "USD", "RSD"} {
		_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
			OwnerType: model.OwnerClient, OwnerID: &uid,
			SecurityType: "stock", Ticker: "AAA",
			Quantity:         1,
			BuyPricePerUnit:  decimal.NewFromInt(100),
			SellPricePerUnit: decimal.NewFromInt(150),
			TotalGain:        decimal.NewFromInt(50),
			Currency:         ccy,
			AccountID:        uint64(100 + i),
			TaxYear:          now.Year(),
			TaxMonth:         int(now.Month()),
		})
	}
	mocks.exchangeClient.setRate("USD", "RSD", decimal.NewFromInt(100))
	got, err := svc.GetUserGainsAndTax(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.RealizedGainThisMonthRSD.IsZero() {
		t.Errorf("expected non-zero monthly gain")
	}
	if got.RealizedGainThisYearRSD.IsZero() {
		t.Errorf("expected non-zero yearly gain")
	}
	if got.RealizedGainLifetimeRSD.IsZero() {
		t.Errorf("expected non-zero lifetime gain")
	}
	if got.ClosedTradesThisYear == 0 {
		t.Errorf("expected closed trade count > 0")
	}
}

// sumGainsInRSD: pure unit test of the conversion fallback.
func TestTaxService_SumGainsInRSD_NoConversion(t *testing.T) {
	svc, _ := buildTaxService()
	got := svc.sumGainsInRSD([]AccountGainSummary{
		{AccountID: 1, Currency: "RSD", TotalGain: decimal.NewFromInt(100)},
		{AccountID: 2, Currency: "RSD", TotalGain: decimal.NewFromInt(50)},
	})
	if !got.Equal(decimal.NewFromInt(150)) {
		t.Errorf("got %s want 150", got)
	}
}

func TestTaxService_SumGainsInRSD_WithConversion(t *testing.T) {
	svc, mocks := buildTaxService()
	mocks.exchangeClient.setRate("USD", "RSD", decimal.NewFromInt(100)) // 1 USD = 100 RSD
	got := svc.sumGainsInRSD([]AccountGainSummary{
		{AccountID: 1, Currency: "USD", TotalGain: decimal.NewFromInt(2)},
	})
	// 2 USD * 100 RSD/USD = 200 RSD
	if !got.Equal(decimal.NewFromInt(200)) {
		t.Errorf("got %s want 200", got)
	}
}

// computeUnpaidTax exercises a few branch scenarios: positive gain over
// already-collected, equal, and a multi-currency mix.
func TestTaxService_ComputeUnpaidTax_PositiveGainPartiallyCollected(t *testing.T) {
	svc, _ := buildTaxService()
	gains := []AccountGainSummary{
		{AccountID: 1, Currency: "RSD", TotalGain: decimal.NewFromInt(1000)},
	}
	// 15% of 1000 = 150 RSD, minus 50 already collected = 100 unpaid.
	got := svc.computeUnpaidTax(gains, decimal.NewFromInt(50))
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100", got)
	}
}

func TestTaxService_ComputeUnpaidTax_GainNegativeOrZero_Skipped(t *testing.T) {
	svc, _ := buildTaxService()
	gains := []AccountGainSummary{
		{AccountID: 1, Currency: "RSD", TotalGain: decimal.NewFromInt(-500)},
		{AccountID: 2, Currency: "RSD", TotalGain: decimal.Zero},
	}
	got := svc.computeUnpaidTax(gains, decimal.Zero)
	if !got.IsZero() {
		t.Errorf("got %s want 0 (negative/zero gains skipped)", got)
	}
}

func TestTaxService_ComputeUnpaidTax_OverCollectedFloorsAtZero(t *testing.T) {
	svc, _ := buildTaxService()
	gains := []AccountGainSummary{
		{AccountID: 1, Currency: "RSD", TotalGain: decimal.NewFromInt(100)},
	}
	// 15 RSD owed, 50 collected → unpaid floored at 0.
	got := svc.computeUnpaidTax(gains, decimal.NewFromInt(50))
	if !got.IsZero() {
		t.Errorf("got %s want 0", got)
	}
}

// ListUserTaxCollections smoke test.
func TestTaxService_ListUserTaxCollections_Empty(t *testing.T) {
	svc, _ := buildTaxService()
	uid := uint64(42)
	rows, err := svc.ListUserTaxCollections(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty, got %d", len(rows))
	}
}

// TestTaxService_WithDB exercises the gorm handle wiring helper.
func TestTaxService_WithDB(t *testing.T) {
	svc, _ := buildTaxService()
	out := svc.WithDB(nil)
	if out == nil {
		t.Fatal("expected non-nil")
	}
}

func TestTaxService_ListUserTaxCollections_HasRows(t *testing.T) {
	svc, mocks := buildTaxService()
	uid := uint64(42)
	_ = mocks.taxCollectionRepo.Create(&model.TaxCollection{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		Year: 2026, Month: 5, AccountID: 1, Currency: "RSD",
		TaxAmountRSD: decimal.NewFromInt(15),
	})
	rows, err := svc.ListUserTaxCollections(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d", len(rows))
	}
}
