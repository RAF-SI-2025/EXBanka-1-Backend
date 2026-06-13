package service

import (
	"context"
	"testing"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestDividend_Payout_FullBody walks the direct-holding and fund-holding payout
// loops end to end: account resolution, cross-service credit, and payout-row
// persistence for both a client holder and a fund holder.
func TestDividend_Payout_FullBody(t *testing.T) {
	db := openDividendTestDB(t)
	accts := newFakeDividendAccountClient()
	accts.accounts[5] = &accountpb.AccountResponse{Id: 5, AccountNumber: "CLIENT-ACCT"}
	accts.accounts[99] = &accountpb.AccountResponse{Id: 99, AccountNumber: "FUND-ACCT"}
	svc := newDividendService(db, accts)
	ctx := context.Background()

	uid := uint64(7)
	// A client holding of security 42 (qty 10) with a usable account.
	if err := db.Create(&model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		UserFirstName: "A", UserLastName: "B",
		SecurityType: "stock", SecurityID: 42, ListingID: 1, Ticker: "AAPL", Name: "Apple",
		Quantity: 10, AveragePrice: decimal.NewFromInt(100), AccountID: 5,
	}).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}
	// A fund holding the same security.
	fund := &model.InvestmentFund{ID: 1, Name: "F", ManagerEmployeeID: 1, RSDAccountID: 99}
	if err := db.Create(fund).Error; err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	if err := db.Create(&model.FundHolding{
		FundID: 1, SecurityType: "stock", SecurityID: 42, Quantity: 20, AveragePriceRSD: decimal.NewFromInt(80),
	}).Error; err != nil {
		t.Fatalf("seed fund holding: %v", err)
	}

	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	payment, err := svc.Declare(ctx, 42, "AAPL", decimal.NewFromInt(10), date, 1)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	summary, err := svc.Payout(ctx, payment.ID)
	if err != nil {
		t.Fatalf("payout: %v", err)
	}
	if summary.PayoutsCreated != 1 {
		t.Errorf("client payouts = %d, want 1", summary.PayoutsCreated)
	}
	if summary.FundPayouts != 1 {
		t.Errorf("fund payouts = %d, want 1", summary.FundPayouts)
	}
	// Two credits issued (client net + fund gross).
	if len(accts.credits) != 2 {
		t.Fatalf("expected 2 credits, got %d", len(accts.credits))
	}
	// Re-running is idempotent (no new payout rows / credits).
	summary2, err := svc.Payout(ctx, payment.ID)
	// Second run errors because the payment is already paid_out — exercises the guard.
	if err == nil {
		_ = summary2
		t.Errorf("second payout should error (already paid out)")
	}
}
