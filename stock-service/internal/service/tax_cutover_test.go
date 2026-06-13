package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestCleanupLegacyBuyerPremiumRows verifies the cutover deletes exactly the
// accept-time buyer-premium rows tied to still-ACTIVE contracts, and leaves
// everything else (collected rows, resolved-contract rows, new exercise rows).
func TestCleanupLegacyBuyerPremiumRows(t *testing.T) {
	db := newOTCExpiryDB(t) // migrates OptionContract + CapitalGain
	cid := uint64(3)

	// Active contract accepted under the old model (saga s-active).
	if err := db.Create(&model.OptionContract{
		Status: model.OptionContractStatusActive, OfferID: u64p(1), SagaID: "s-active",
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &cid, SellerOwnerType: model.OwnerBank,
		StockID: 1, Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(1),
		PremiumPaid: decimal.NewFromInt(1), PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(48 * time.Hour), PremiumPaidAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed active contract: %v", err)
	}
	// Already-exercised contract (saga s-done) — its premium row must survive.
	if err := db.Create(&model.OptionContract{
		Status: model.OptionContractStatusExercised, OfferID: u64p(2), SagaID: "s-done",
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &cid, SellerOwnerType: model.OwnerBank,
		StockID: 1, Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(1),
		PremiumPaid: decimal.NewFromInt(1), PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(48 * time.Hour), PremiumPaidAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed done contract: %v", err)
	}

	mk := func(key string, collected bool) *model.CapitalGain {
		k := key
		g := &model.CapitalGain{
			OwnerType: model.OwnerClient, OwnerID: &cid, OTC: true, SecurityType: "option",
			Ticker: "AAPL", Quantity: 50, TotalGain: decimal.NewFromInt(-1150), Currency: "USD",
			AccountID: 11, TaxYear: 2026, TaxMonth: 5, IdempotencyKey: &k,
		}
		if collected {
			tc := uint64(99)
			g.TaxCollectionID = &tc
		}
		return g
	}
	// Should be deleted: active contract's accept premium row, uncollected.
	db.Create(mk("s-active:buyer-premium-cg", false))
	// Should survive: same key but already collected.
	db.Create(mk("s-active:buyer-premium-cg-collected", true)) // distinct key, collected
	// Should survive: resolved contract's premium row.
	db.Create(mk("s-done:buyer-premium-cg", false))
	// Should survive: a new exercise-gain row for the active contract.
	db.Create(mk("s-active:buyer-exercise-cg", false))

	n, err := CleanupLegacyBuyerPremiumRows(db)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 row deleted, got %d", n)
	}
	var remaining int64
	db.Model(&model.CapitalGain{}).Count(&remaining)
	if remaining != 3 {
		t.Fatalf("expected 3 surviving rows, got %d", remaining)
	}
	// The exact deleted row is gone.
	var gone int64
	db.Model(&model.CapitalGain{}).Where("idempotency_key = ?", "s-active:buyer-premium-cg").Count(&gone)
	if gone != 0 {
		t.Fatalf("active-contract accept premium row should be deleted")
	}
}
