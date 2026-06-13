package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestTaxService_CollectTax_ForeignCurrencyGain exercises collectTaxInner's
// FX-conversion branches: a USD-denominated gain is taxed in USD, converted to
// RSD for the state credit, and (because the user's account is RSD) the debit
// amount is the RSD-converted figure.
func TestTaxService_CollectTax_ForeignCurrencyGain(t *testing.T) {
	svc, mocks := buildTaxService()
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-USD-ACCT")
	mocks.accountClient.accounts[1].CurrencyCode = "RSD" // account currency differs from the gain currency
	mocks.exchangeClient.convertRate["USD/RSD"] = decimal.NewFromInt(117)

	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		OwnerType: model.OwnerClient, OwnerID: ptrU64(42),
		SecurityType: "stock", Ticker: "AAPL", Quantity: 10,
		BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(1000), Currency: "USD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{OwnerType: "client", OwnerID: ptrU64(42), TotalDebtRSD: decimal.NewFromInt(100)},
	}

	collected, totalRSD, failed, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if collected != 1 || failed != 0 {
		t.Fatalf("collected=%d failed=%d, want 1/0", collected, failed)
	}
	// Tax = 1000 USD × 0.15 = 150 USD → ×117 = 17550 RSD.
	wantRSD := decimal.NewFromInt(17550)
	if !totalRSD.Equal(wantRSD) {
		t.Errorf("totalRSD = %s, want %s", totalRSD, wantRSD)
	}
	// The exchange client was consulted to convert the tax to RSD.
	if len(mocks.exchangeClient.convertCalls) == 0 {
		t.Errorf("expected an FX convert call for the USD gain")
	}
	// The state RSD credit equals the converted figure.
	if len(mocks.taxCollectionRepo.collections) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(mocks.taxCollectionRepo.collections))
	}
	if !mocks.taxCollectionRepo.collections[0].TaxAmountRSD.Equal(wantRSD) {
		t.Errorf("collection TaxAmountRSD = %s, want %s", mocks.taxCollectionRepo.collections[0].TaxAmountRSD, wantRSD)
	}
}
