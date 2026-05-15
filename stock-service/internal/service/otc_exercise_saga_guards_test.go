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
