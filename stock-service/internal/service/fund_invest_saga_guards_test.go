package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// TestFundService_Invest_NoSagaDepsWired covers the early-return when the
// saga dependencies aren't populated.
func TestFundService_Invest_NoSagaDepsWired(t *testing.T) {
	fx := newFundFixture(t)
	_, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: 1, Amount: decimal.NewFromInt(100), Currency: "RSD",
	})
	if err == nil || err != errSagaDepsNotWired {
		t.Errorf("expected errSagaDepsNotWired, got %v", err)
	}
}
