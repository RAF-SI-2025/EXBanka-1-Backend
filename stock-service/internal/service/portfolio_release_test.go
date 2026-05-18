package service

import (
	"context"
	"testing"
)

// TestPortfolioService_ReleaseResidualReservation_NoFillClient is the no-op
// branch when no fill client is wired.
func TestPortfolioService_ReleaseResidualReservation_NoFillClient(t *testing.T) {
	svc := buildPortfolioServiceWithEmptyMocks() // fillClient=nil
	if err := svc.ReleaseResidualReservation(context.Background(), 42); err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
}

// TestPortfolioService_CommissionRecipientAccount_FallbackToStateAccount
// covers the fallback branch where no dynamic recipient is wired.
func TestPortfolioService_CommissionRecipientAccount_FallbackToStateAccount(t *testing.T) {
	svc := NewPortfolioService(nil, nil, nil, nil, nil, nil, nil, "STATE-ACCT-1")
	got, err := svc.commissionRecipientAccount(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "STATE-ACCT-1" {
		t.Errorf("got %q want STATE-ACCT-1", got)
	}
}

// TestPortfolioService_AccountCurrency_NilFillClient returns empty when
// fillClient isn't wired.
func TestPortfolioService_AccountCurrency_NilFillClient(t *testing.T) {
	svc := NewPortfolioService(nil, nil, nil, nil, nil, nil, nil, "S")
	got, err := svc.accountCurrency(context.Background(), 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "" {
		t.Errorf("got %q want empty", got)
	}
}
