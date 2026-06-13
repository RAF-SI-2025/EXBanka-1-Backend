package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func seedTaxGain(t *testing.T, mocks *taxMocks, currency string, year, month int) {
	t.Helper()
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		OwnerType: model.OwnerClient, OwnerID: ptrU64(42),
		SecurityType: "stock", Ticker: "AAPL", Quantity: 10,
		BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(1000), Currency: currency, AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{OwnerType: "client", OwnerID: ptrU64(42), TotalDebtRSD: decimal.NewFromInt(100)},
	}
}

// Account currency differs from BOTH the gain currency and RSD → the debit is
// FX-converted to the account currency (the else branch of the debit FX).
func TestTaxService_CollectTax_DebitConvertedToForeignAccount(t *testing.T) {
	svc, mocks := buildTaxService()
	now := time.Now()
	year, month := now.Year(), int(now.Month())

	mocks.accountClient.addAccount(1, "USER-EUR")
	mocks.accountClient.accounts[1].CurrencyCode = "EUR"
	mocks.exchangeClient.convertRate["USD/RSD"] = decimal.NewFromInt(117)
	mocks.exchangeClient.convertRate["USD/EUR"] = decimal.NewFromFloat(0.9)
	seedTaxGain(t, mocks, "USD", year, month)

	collected, _, failed, err := svc.CollectTax(year, month)
	if err != nil || collected != 1 || failed != 0 {
		t.Fatalf("collect=%d failed=%d err=%v", collected, failed, err)
	}
	// The user debit ran in EUR (the account currency), credit in RSD.
	debit := mocks.accountClient.updateBalCalls[0]
	debitAmt, _ := decimal.NewFromString(debit.Amount)
	// 150 USD × 0.9 = -135 EUR.
	if !debitAmt.Equal(decimal.NewFromFloat(-135).Round(4)) {
		t.Errorf("debit = %s, want -135 EUR", debitAmt)
	}
}

// A failed FX conversion of the tax to RSD marks the user as failed (skipped).
func TestTaxService_CollectTax_ConvertError_Skips(t *testing.T) {
	svc, mocks := buildTaxService()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	mocks.accountClient.addAccount(1, "USER")
	mocks.exchangeClient.convertErr = errMsg("fx down")
	seedTaxGain(t, mocks, "USD", year, month)

	collected, _, failed, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if collected != 0 || failed != 1 {
		t.Errorf("collect=%d failed=%d, want 0/1", collected, failed)
	}
}

// A failed state-account credit leaves the collection unrecorded (the user is
// marked failed so a retry can re-issue the credit under the same key).
func TestTaxService_CollectTax_StateCreditFails_NotRecorded(t *testing.T) {
	svc, mocks := buildTaxService()
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	mocks.accountClient.addAccount(1, "USER-RSD")
	mocks.accountClient.failOnAccountNumber = "STATE-RSD-001" // the state credit leg fails
	seedTaxGain(t, mocks, "RSD", year, month)

	collected, _, failed, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if collected != 0 || failed != 1 {
		t.Errorf("collect=%d failed=%d, want 0/1", collected, failed)
	}
	if len(mocks.taxCollectionRepo.collections) != 0 {
		t.Errorf("no collection should be recorded when the state credit fails")
	}
}

type errMsgType string

func (e errMsgType) Error() string { return string(e) }
func errMsg(s string) error        { return errMsgType(s) }
