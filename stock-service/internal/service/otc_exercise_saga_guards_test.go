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
		ContractID: 9999, ActorUserID: 7, ActorSystemType: "client",
		BuyerAccountID: 1, SellerAccountID: 2,
	})
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
		ContractID: c.ID, ActorUserID: 7, ActorSystemType: "client",
		BuyerAccountID: 1, SellerAccountID: 2,
	})
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
		ContractID: c.ID, ActorUserID: 7, ActorSystemType: "client",
		BuyerAccountID: 1, SellerAccountID: 2,
	})
	if err == nil {
		t.Fatal("expected error for expired settlement")
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
		ContractID: c.ID, ActorUserID: 99, ActorSystemType: "client",
		BuyerAccountID: 1, SellerAccountID: 2,
	})
	if err == nil {
		t.Fatal("expected error: not buyer")
	}
}
