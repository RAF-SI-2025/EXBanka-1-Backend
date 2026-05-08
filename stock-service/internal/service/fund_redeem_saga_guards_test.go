package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// TestFundService_Redeem_NoSagaDepsWired covers the early-return when the
// saga dependencies aren't populated.
func TestFundService_Redeem_NoSagaDepsWired(t *testing.T) {
	fx := newFundFixture(t)
	_, err := fx.svc.Redeem(context.Background(), RedeemInput{
		FundID: 1, AmountRSD: decimal.NewFromInt(100),
	})
	if err == nil || err != errSagaDepsNotWired {
		t.Errorf("expected errSagaDepsNotWired, got %v", err)
	}
}
