package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Helper: build OTCService with mocks
// ---------------------------------------------------------------------------

type otcMocks struct {
	holdingRepo     *mockHoldingRepo
	capitalGainRepo *mockCapitalGainRepo
	listingRepo     *mockListingRepo
	accountClient   *mockAccountClient
}

func buildOTCService() (*OTCService, *otcMocks) {
	mocks := &otcMocks{
		holdingRepo:     newMockHoldingRepo(),
		capitalGainRepo: newMockCapitalGainRepo(),
		listingRepo:     newMockListingRepo(),
		accountClient:   newMockAccountClient(),
	}

	nameResolver := func(userID uint64, systemType string) (string, string, error) {
		return "Jane", "Buyer", nil
	}

	svc := NewOTCService(
		mocks.holdingRepo,
		mocks.capitalGainRepo,
		mocks.listingRepo,
		mocks.accountClient,
		nameResolver,
	)

	return svc, mocks
}

// otcStockListing creates a stock listing with USD currency at a given price.
func otcStockListing(id, securityID uint64, price float64) *model.Listing {
	return &model.Listing{
		ID:           id,
		SecurityID:   securityID,
		SecurityType: "stock",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:       1,
			Name:     "NYSE",
			Acronym:  "NYSE",
			Currency: "USD",
			TimeZone: "-5",
		},
		Price: decimal.NewFromFloat(price),
	}
}

// ---------------------------------------------------------------------------
// Tests: BuyOffer success — buyer gets holding, seller quantity decremented
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_Success(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	// Seller has 20 shares, 10 public, avg price $40
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})
	sellerHoldingID := uint64(1) // first holding gets ID 1

	// Set up accounts
	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	result, err := svc.BuyOffer(sellerHoldingID, 20, "client", 5, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify result
	if result.Quantity != 5 {
		t.Errorf("expected quantity 5, got %d", result.Quantity)
	}
	if !result.PricePerUnit.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected pricePerUnit 50, got %s", result.PricePerUnit)
	}
	if !result.TotalPrice.Equal(decimal.NewFromFloat(250.00)) {
		t.Errorf("expected totalPrice 250, got %s", result.TotalPrice)
	}

	// Verify seller's holding was decremented
	sellerHolding, err := mocks.holdingRepo.GetByID(sellerHoldingID)
	if err != nil {
		t.Fatalf("seller holding not found: %v", err)
	}
	if sellerHolding.Quantity != 15 {
		t.Errorf("expected seller quantity 15, got %d", sellerHolding.Quantity)
	}
	if sellerHolding.PublicQuantity != 5 {
		t.Errorf("expected seller public quantity 5, got %d", sellerHolding.PublicQuantity)
	}

	// Verify buyer got a holding
	buyerHolding, err := mocks.holdingRepo.GetByUserAndSecurity(20, "client", "stock", 100)
	if err != nil {
		t.Fatalf("buyer holding not found: %v", err)
	}
	if buyerHolding.Quantity != 5 {
		t.Errorf("expected buyer quantity 5, got %d", buyerHolding.Quantity)
	}
	if !buyerHolding.AveragePrice.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected buyer avg price 50, got %s", buyerHolding.AveragePrice)
	}
	if buyerHolding.UserFirstName != "Jane" || buyerHolding.UserLastName != "Buyer" {
		t.Errorf("expected buyer name Jane Buyer, got %s %s", buyerHolding.UserFirstName, buyerHolding.UserLastName)
	}
}

// ---------------------------------------------------------------------------
// Tests: Commission = min(14% of total, $7) — below cap
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_CommissionBelowCap(t *testing.T) {
	svc, mocks := buildOTCService()

	// Price $10, buy 3 shares => total $30 => commission = 14% * 30 = $4.20 (below $7 cap)
	listing := otcStockListing(1, 100, 10.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       100,
		PublicQuantity: 50,
		AveragePrice:   decimal.NewFromFloat(8.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	result, err := svc.BuyOffer(1, 20, "client", 3, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCommission := decimal.NewFromFloat(4.20)
	if !result.Commission.Equal(expectedCommission) {
		t.Errorf("expected commission %s, got %s", expectedCommission, result.Commission)
	}

	// Buyer debited total + commission = 30 + 4.20 = 34.20
	if len(mocks.accountClient.updateBalCalls) < 1 {
		t.Fatal("expected at least 1 UpdateBalance call")
	}
	debitCall := mocks.accountClient.updateBalCalls[0]
	debitAmount, _ := decimal.NewFromString(debitCall.Amount)
	expectedDebit := decimal.NewFromFloat(-34.20).Round(4)
	if !debitAmount.Equal(expectedDebit) {
		t.Errorf("expected debit %s, got %s", expectedDebit, debitAmount)
	}
}

// ---------------------------------------------------------------------------
// Tests: Commission = min(14% of total, $7) — at cap
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_CommissionAtCap(t *testing.T) {
	svc, mocks := buildOTCService()

	// Price $100, buy 5 shares => total $500 => 14% * 500 = $70 => capped at $7
	listing := otcStockListing(1, 100, 100.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       100,
		PublicQuantity: 50,
		AveragePrice:   decimal.NewFromFloat(80.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	result, err := svc.BuyOffer(1, 20, "client", 5, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCommission := decimal.NewFromFloat(7.00)
	if !result.Commission.Equal(expectedCommission) {
		t.Errorf("expected commission %s (cap), got %s", expectedCommission, result.Commission)
	}
}

// ---------------------------------------------------------------------------
// Tests: Capital gain recorded for seller
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_CapitalGainRecorded(t *testing.T) {
	svc, mocks := buildOTCService()

	// Seller avg price $40, market price $50 => gain per share = $10
	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	_, err := svc.BuyOffer(1, 20, "client", 5, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify capital gain was recorded
	if len(mocks.capitalGainRepo.gains) != 1 {
		t.Fatalf("expected 1 capital gain, got %d", len(mocks.capitalGainRepo.gains))
	}
	cg := mocks.capitalGainRepo.gains[0]
	if cg.UserID != 10 {
		t.Errorf("expected capital gain for seller (user 10), got user %d", cg.UserID)
	}
	if !cg.OTC {
		t.Error("expected OTC flag to be true")
	}
	// Gain = (50 - 40) * 5 = 50
	expectedGain := decimal.NewFromInt(50)
	if !cg.TotalGain.Equal(expectedGain) {
		t.Errorf("expected totalGain %s, got %s", expectedGain, cg.TotalGain)
	}
	if !cg.BuyPricePerUnit.Equal(decimal.NewFromFloat(40.00)) {
		t.Errorf("expected buyPricePerUnit 40, got %s", cg.BuyPricePerUnit)
	}
	if !cg.SellPricePerUnit.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected sellPricePerUnit 50, got %s", cg.SellPricePerUnit)
	}
	if cg.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", cg.Currency)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuyOffer for more than public quantity => error
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_InsufficientPublicQuantity(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 5, // only 5 public
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")

	// Try to buy 10 when only 5 are public
	_, err := svc.BuyOffer(1, 20, "client", 10, 5)
	if err == nil {
		t.Fatal("expected error for insufficient public quantity")
	}
	if !strings.Contains(err.Error(), "insufficient public quantity for OTC purchase") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: ListOffers returns only public holdings (public_quantity > 0)
// ---------------------------------------------------------------------------

func TestOTC_ListOffers_OnlyPublicHoldings(t *testing.T) {
	svc, mocks := buildOTCService()

	// Holding with public quantity (should appear)
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(50.00),
		AccountID:      1,
	})

	// Holding without public quantity (should NOT appear)
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         20,
		SystemType:     "client",
		SecurityType:   "stock",
		SecurityID:     200,
		ListingID:      2,
		Ticker:         "GOOG",
		Name:           "Alphabet Inc.",
		Quantity:       50,
		PublicQuantity: 0,
		AveragePrice:   decimal.NewFromFloat(100.00),
		AccountID:      2,
	})

	offers, total, err := svc.ListOffers(repository.OTCFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
	if offers[0].Ticker != "AAPL" {
		t.Errorf("expected AAPL, got %s", offers[0].Ticker)
	}
	if offers[0].PublicQuantity != 10 {
		t.Errorf("expected public quantity 10, got %d", offers[0].PublicQuantity)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuyOffer compensating reversal — seller credit fails after buyer debit
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_CompensatesBuyerOnSellerCreditFailure(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	// Fail the seller's UpdateBalance (credit) while allowing buyer debit to succeed
	mocks.accountClient.failUpdateForAccount("SELLER-ACCT", errors.New("seller account frozen"))

	_, err := svc.BuyOffer(1, 20, "client", 5, 5)
	if err == nil {
		t.Fatal("expected error when seller credit fails")
	}

	// Verify compensation happened: buyer debit + buyer re-credit
	// The debit call goes through (recorded), the seller credit fails (not recorded),
	// then compensation re-credits buyer (recorded).
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls (buyer debit + buyer compensation), got %d", len(mocks.accountClient.updateBalCalls))
	}

	// First call: buyer debit (negative)
	debitCall := mocks.accountClient.updateBalCalls[0]
	if debitCall.AccountNumber != "BUYER-ACCT" {
		t.Errorf("expected first call to BUYER-ACCT, got %s", debitCall.AccountNumber)
	}
	debitAmt, _ := decimal.NewFromString(debitCall.Amount)
	if debitAmt.IsPositive() {
		t.Errorf("expected negative debit amount, got %s", debitAmt)
	}

	// Second call: buyer compensation (positive, same magnitude)
	compCall := mocks.accountClient.updateBalCalls[1]
	if compCall.AccountNumber != "BUYER-ACCT" {
		t.Errorf("expected compensation to BUYER-ACCT, got %s", compCall.AccountNumber)
	}
	compAmt, _ := decimal.NewFromString(compCall.Amount)
	if !compAmt.Equal(debitAmt.Neg()) {
		t.Errorf("expected compensation amount %s, got %s", debitAmt.Neg(), compAmt)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuyOffer compensating reversal — capital gain fails after both balance ops
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_CompensatesBothOnCapitalGainFailure(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	// Capital gain creation will fail
	mocks.capitalGainRepo.failNextCreate = errors.New("capital gain db error")

	_, err := svc.BuyOffer(1, 20, "client", 5, 5)
	if err == nil {
		t.Fatal("expected error when capital gain creation fails")
	}

	// Verify: buyer debit, seller credit, then compensation (buyer re-credit, seller re-debit)
	// Total = 4 recorded calls (debit, credit, compensation credit, compensation debit)
	if len(mocks.accountClient.updateBalCalls) != 4 {
		t.Fatalf("expected 4 UpdateBalance calls, got %d", len(mocks.accountClient.updateBalCalls))
	}

	// Call 0: buyer debit (negative)
	buyerDebit := mocks.accountClient.updateBalCalls[0]
	if buyerDebit.AccountNumber != "BUYER-ACCT" {
		t.Errorf("call 0: expected BUYER-ACCT, got %s", buyerDebit.AccountNumber)
	}
	buyerDebitAmt, _ := decimal.NewFromString(buyerDebit.Amount)

	// Call 1: seller credit (positive)
	sellerCredit := mocks.accountClient.updateBalCalls[1]
	if sellerCredit.AccountNumber != "SELLER-ACCT" {
		t.Errorf("call 1: expected SELLER-ACCT, got %s", sellerCredit.AccountNumber)
	}
	sellerCreditAmt, _ := decimal.NewFromString(sellerCredit.Amount)

	// Call 2: buyer re-credit (positive, reverse of debit)
	buyerComp := mocks.accountClient.updateBalCalls[2]
	if buyerComp.AccountNumber != "BUYER-ACCT" {
		t.Errorf("call 2: expected BUYER-ACCT, got %s", buyerComp.AccountNumber)
	}
	buyerCompAmt, _ := decimal.NewFromString(buyerComp.Amount)
	if !buyerCompAmt.Equal(buyerDebitAmt.Neg()) {
		t.Errorf("expected buyer compensation %s, got %s", buyerDebitAmt.Neg(), buyerCompAmt)
	}

	// Call 3: seller re-debit (negative, reverse of credit)
	sellerComp := mocks.accountClient.updateBalCalls[3]
	if sellerComp.AccountNumber != "SELLER-ACCT" {
		t.Errorf("call 3: expected SELLER-ACCT, got %s", sellerComp.AccountNumber)
	}
	sellerCompAmt, _ := decimal.NewFromString(sellerComp.Amount)
	if !sellerCompAmt.Equal(sellerCreditAmt.Neg()) {
		t.Errorf("expected seller compensation %s, got %s", sellerCreditAmt.Neg(), sellerCompAmt)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuyOffer — buyer holding upsert fails (no balance compensation needed
// per task spec, but error should be returned)
// ---------------------------------------------------------------------------

func TestOTC_BuyOffer_HoldingUpsertFailure_ReturnsError(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple Inc.",
		Quantity:       20,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      2,
	})

	mocks.accountClient.addAccount(5, "BUYER-ACCT")
	mocks.accountClient.addAccount(2, "SELLER-ACCT")

	// Make holding upsert fail (buyer holding creation step)
	mocks.holdingRepo.failNextUpsert = errors.New("upsert failed")

	_, err := svc.BuyOffer(1, 20, "client", 5, 5)
	if err == nil {
		t.Fatal("expected error when buyer holding upsert fails")
	}
}
