package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// TestConvertToRSD_Zero returns zero for zero amount without calling exchange.
func TestConvertToRSD_Zero(t *testing.T) {
	fx := newOrderServiceFixture()
	got, err := fx.svc.convertToRSD(context.Background(), decimal.Zero, "USD")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("got %s want 0", got)
	}
}

func TestConvertToRSD_RSDInput(t *testing.T) {
	fx := newOrderServiceFixture()
	got, err := fx.svc.convertToRSD(context.Background(), decimal.NewFromInt(100), "RSD")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100", got)
	}
}

func TestConvertToRSD_NoExchangeClient(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.exchangeClient = nil
	// Re-build service without exchange client.
	fx.svc = NewOrderService(
		fx.orderRepo, newMockOrderTxRepo(), fx.listingRepo, fx.settingRepo,
		fx.securityRepo, fx.producer,
		fx.sagaRepo, fx.accountClient, nil, fx.holdingSvc,
		fx.forexRepo, fx.settings,
	)
	got, err := fx.svc.convertToRSD(context.Background(), decimal.NewFromInt(100), "USD")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Falls back to native amount.
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s", got)
	}
}

func TestConvertToRSD_HappyPath(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.exchangeClient.setRate("USD", "RSD", decimal.NewFromInt(220), decimal.NewFromInt(110))
	got, err := fx.svc.convertToRSD(context.Background(), decimal.NewFromInt(2), "USD")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// fake returns the configured Amount verbatim → 220.
	if !got.Equal(decimal.NewFromInt(220)) {
		t.Errorf("got %s want 220", got)
	}
}

func TestConvertToRSD_ExchangeError(t *testing.T) {
	fx := newOrderServiceFixture()
	// no rate configured → exchangeClient returns an error
	_, err := fx.svc.convertToRSD(context.Background(), decimal.NewFromInt(2), "USD")
	if err == nil {
		t.Fatal("expected error")
	}
}
