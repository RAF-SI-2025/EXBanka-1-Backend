package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestOTCExerciseContract_NoSagaDepsWired returns errOTCSagaDepsNotWired when
// the saga deps aren't populated.
func TestOTCExerciseContract_NoSagaDepsWired(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{ContractID: 1})
	if err == nil || err != errOTCSagaDepsNotWired {
		t.Errorf("expected errOTCSagaDepsNotWired, got %v", err)
	}
}

// TestOTCExerciseContract_ContractNotFound surfaces the GetByID error.
func TestOTCExerciseContract_ContractNotFound(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: 9999, ActorUserID: 7, ActorSystemType: "client"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestOTCExerciseContract_InactiveContract rejects exercise of a non-active
// contract.
func TestOTCExerciseContract_InactiveContract(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		Status:         model.OptionContractStatusExpired,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: c.ID, ActorUserID: 7, ActorSystemType: "client"})
	if err == nil {
		t.Fatal("expected error for inactive contract")
	}
}

// TestOTCExerciseContract_ExpiredSettlement rejects when settlement_date is
// not in the future.
func TestOTCExerciseContract_ExpiredSettlement(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(-24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: c.ID, ActorUserID: 7, ActorSystemType: "client"})
	if err == nil {
		t.Fatal("expected error for expired settlement")
	}
}

// TestOTCExerciseContract_EmitsExercisedNotifications: a successful exercise
// emits OTC_CONTRACT_EXERCISED to both client parties (buyer + seller).
func TestOTCExerciseContract_EmitsExercisedNotifications(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Accept first to create a real contract with the seller's holding reserved.
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Drop the notifications recorded during accept so the assertions below
	// only see exercise-time notifications.
	fx.notifier.notifs = nil

	exercised, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: contract.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if exercised.Status != model.OptionContractStatusExercised {
		t.Fatalf("contract status = %s, want EXERCISED", exercised.Status)
	}
	if got := countNotifs(fx.notifier.notifs, "OTC_CONTRACT_EXERCISED"); got != 2 {
		t.Fatalf("OTC_CONTRACT_EXERCISED count = %d, want 2 (buyer + seller)", got)
	}
	buyerUID := uint64(fx.buyerID)
	sellerUID := uint64(fx.sellerID)
	sawBuyer, sawSeller := false, false
	for _, m := range fx.notifier.notifs {
		if m.Type != "OTC_CONTRACT_EXERCISED" {
			continue
		}
		if m.RefType != "otc_contract" || m.RefID != contract.ID {
			t.Errorf("notif ref = %s/%d, want otc_contract/%d", m.RefType, m.RefID, contract.ID)
		}
		if m.Data["ticker"] != contract.Ticker {
			t.Errorf("notif ticker = %q, want %q", m.Data["ticker"], contract.Ticker)
		}
		if m.UserID == buyerUID {
			sawBuyer = true
		}
		if m.UserID == sellerUID {
			sawSeller = true
		}
	}
	if !sawBuyer || !sawSeller {
		t.Errorf("expected notifications to both buyer (%d) and seller (%d); sawBuyer=%v sawSeller=%v", buyerUID, sellerUID, sawBuyer, sawSeller)
	}
}

// TestOTCExerciseContract_RecordsSellerCapitalGain: a successful exercise of
// a sell_initiated contract realises the seller's P/L from cost basis up to
// the strike price. The fixture seeds the seller with AveragePrice=100,
// Quantity=100 and a sell_initiated offer at StrikePrice=5000, Qty=10 — so
// exercising the resulting contract should produce one CapitalGain row with
// BuyPricePerUnit=100, SellPricePerUnit=5000, Quantity=10, TotalGain=49000.
// This is the bug the user surfaced: "buy at one price, write a contract,
// get exercised at higher price → profit must show up".
func TestOTCExerciseContract_RecordsSellerCapitalGain(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	cgRepo := newMockCapitalGainRepo()
	fx.svc = fx.svc.WithCapitalGain(cgRepo)

	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: contract.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
	}); err != nil {
		t.Fatalf("exercise: %v", err)
	}

	// Filter to stock CG: Accept also wrote 2 SecurityType="option"
	// premium rows (writer + buyer), which we ignore here. This test
	// only asserts the EXERCISE-time stock realisation.
	var stockGains []model.CapitalGain
	for _, g := range cgRepo.gains {
		if g.SecurityType == "stock" {
			stockGains = append(stockGains, g)
		}
	}
	if len(stockGains) != 1 {
		t.Fatalf("expected 1 stock capital gain row from exercise, got %d (all rows: %d)", len(stockGains), len(cgRepo.gains))
	}
	cg := stockGains[0]
	sellerUID := uint64(fx.sellerID)
	if cg.OwnerType != model.OwnerClient || cg.OwnerID == nil || *cg.OwnerID != sellerUID {
		t.Errorf("owner = %s/%v, want client/%d", cg.OwnerType, cg.OwnerID, sellerUID)
	}
	if !cg.OTC {
		t.Error("OTC flag should be true for option-exercise gain")
	}
	if cg.Quantity != 10 {
		t.Errorf("Quantity = %d, want 10", cg.Quantity)
	}
	if !cg.BuyPricePerUnit.Equal(decimal.NewFromInt(100)) {
		t.Errorf("BuyPricePerUnit = %s, want 100 (seller's AveragePrice pre-exercise)", cg.BuyPricePerUnit)
	}
	if !cg.SellPricePerUnit.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("SellPricePerUnit = %s, want 5000 (StrikePrice)", cg.SellPricePerUnit)
	}
	wantGain := decimal.NewFromInt(49000) // (5000 - 100) * 10
	if !cg.TotalGain.Equal(wantGain) {
		t.Errorf("TotalGain = %s, want %s", cg.TotalGain, wantGain)
	}
	if cg.AccountID != contract.SellerAccountID {
		t.Errorf("AccountID = %d, want %d (seller account)", cg.AccountID, contract.SellerAccountID)
	}
}

// TestAcceptSaga_RecordsOptionPremiumCapitalGains: a successful acceptance
// realises the option premium as two SecurityType="option" capital gain
// rows — +premium for the writer (seller), −premium for the buyer.
// This is the accounting that makes "total P/L" work for any combination
// of stock + option events: regardless of whether the option later
// exercises or expires, the premium is already booked.
func TestAcceptSaga_RecordsOptionPremiumCapitalGains(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	cgRepo := newMockCapitalGainRepo()
	fx.svc = fx.svc.WithCapitalGain(cgRepo)

	_, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	if len(cgRepo.gains) != 2 {
		t.Fatalf("expected 2 capital gain rows (writer + buyer), got %d", len(cgRepo.gains))
	}
	sellerUID := uint64(fx.sellerID)
	buyerUID := uint64(fx.buyerID)
	var writerCG, buyerCG *model.CapitalGain
	for i := range cgRepo.gains {
		g := &cgRepo.gains[i]
		if g.OwnerID != nil && *g.OwnerID == sellerUID {
			writerCG = g
		} else if g.OwnerID != nil && *g.OwnerID == buyerUID {
			buyerCG = g
		}
	}
	if writerCG == nil {
		t.Fatalf("missing writer CG row")
	}
	if buyerCG == nil {
		t.Fatalf("missing buyer CG row")
	}
	// Premium in fixture = 50000.
	wantPremium := decimal.NewFromInt(50000)
	if writerCG.SecurityType != "option" {
		t.Errorf("writer SecurityType = %q, want option", writerCG.SecurityType)
	}
	if !writerCG.TotalGain.Equal(wantPremium) {
		t.Errorf("writer TotalGain = %s, want +%s", writerCG.TotalGain, wantPremium)
	}
	if buyerCG.SecurityType != "option" {
		t.Errorf("buyer SecurityType = %q, want option", buyerCG.SecurityType)
	}
	if !buyerCG.TotalGain.Equal(wantPremium.Neg()) {
		t.Errorf("buyer TotalGain = %s, want -%s", buyerCG.TotalGain, wantPremium)
	}
	if !writerCG.OTC || !buyerCG.OTC {
		t.Error("both option CG rows must have OTC=true")
	}
}

// TestOTCExerciseContract_NoCapitalGainRepoWired: without WithCapitalGain the
// exercise still succeeds (shares + money move). Legacy callers continue to
// work; the missing gain is a known degraded mode, not an error.
func TestOTCExerciseContract_NoCapitalGainRepoWired(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	exercised, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: contract.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if exercised.Status != model.OptionContractStatusExercised {
		t.Errorf("status = %s, want EXERCISED", exercised.Status)
	}
}

// TestOTCExerciseContract_NotBuyer rejects when the actor isn't the contract buyer.
func TestOTCExerciseContract_NotBuyer(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: c.ID, ActorUserID: 99, ActorSystemType: "client"})
	if err == nil {
		t.Fatal("expected error: not buyer")
	}
}
