package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestDividend_ListMyDividends(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	uid := uint64(7)
	for i := 0; i < 3; i++ {
		dp := &model.DividendPayout{
			DividendPaymentID: uint64(i + 1), HoldingOwnerType: "client", HoldingOwnerID: &uid,
			HoldingID: 1, Shares: 10, GrossAmountRSD: decimal.NewFromInt(100),
			TaxAmountRSD: decimal.NewFromInt(15), NetAmountRSD: decimal.NewFromInt(85),
			CreditedAccountID: 1, IdempotencyKey: "k" + kafkaUint(uint64(i)),
		}
		if err := db.Create(dp).Error; err != nil {
			t.Fatalf("seed payout: %v", err)
		}
	}
	rows, total, err := svc.ListMyDividends("client", &uid, 1, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(rows) != 2 {
		t.Errorf("page size 2 → %d rows, want 2", len(rows))
	}
}

func TestDividend_SumDividendsReceivedByOwner(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	uid := uint64(7)
	for i := 0; i < 2; i++ {
		dp := &model.DividendPayout{
			DividendPaymentID: uint64(i + 1), HoldingOwnerType: "client", HoldingOwnerID: &uid,
			HoldingID: 1, Shares: 10, GrossAmountRSD: decimal.NewFromInt(100),
			NetAmountRSD: decimal.NewFromInt(85), CreditedAccountID: 1,
			IdempotencyKey: "s" + kafkaUint(uint64(i)),
		}
		if err := db.Create(dp).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	sum, err := svc.SumDividendsReceivedByOwner("client", &uid)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if !sum.Equal(decimal.NewFromInt(170)) {
		t.Errorf("sum = %s, want 170", sum)
	}
}

func TestDividend_ListFundDividendsAndInvestorShare(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	investor := uint64(42)

	// FundDividendPayment with a per-investor snapshot.
	snapshot := `[{"investor_owner_type":"client","investor_owner_id":42,"gross_share_rsd":"60"},` +
		`{"investor_owner_type":"client","investor_owner_id":99,"gross_share_rsd":"40"}]`
	fdp := &model.FundDividendPayment{
		DividendPaymentID: 1, FundID: 5, AmountRSD: decimal.NewFromInt(100),
		PerInvestorSnapshot: snapshot, CreatedAt: time.Now(),
	}
	if err := db.Create(fdp).Error; err != nil {
		t.Fatalf("seed fdp: %v", err)
	}

	// ListFundDividends.
	rows, total, err := svc.ListFundDividends(5, 1, 10)
	if err != nil {
		t.Fatalf("list fund dividends: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("want 1 fund dividend, got total=%d len=%d", total, len(rows))
	}

	// Investor 42 → gross share 60.
	got, err := svc.DividendsReceivedByFundInvestor(5, "client", &investor)
	if err != nil {
		t.Fatalf("investor share: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(60)) {
		t.Errorf("investor 42 share = %s, want 60", got)
	}

	// An investor not in the snapshot → 0.
	missing := uint64(123)
	got, err = svc.DividendsReceivedByFundInvestor(5, "client", &missing)
	if err != nil {
		t.Fatalf("missing investor share: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("missing investor share = %s, want 0", got)
	}
}

func TestOwnerIDsMatch(t *testing.T) {
	a, b := uint64(1), uint64(1)
	c := uint64(2)
	if !ownerIDsMatch(nil, nil) {
		t.Error("nil,nil should match")
	}
	if ownerIDsMatch(&a, nil) || ownerIDsMatch(nil, &a) {
		t.Error("one-nil should not match")
	}
	if !ownerIDsMatch(&a, &b) {
		t.Error("1,1 should match")
	}
	if ownerIDsMatch(&a, &c) {
		t.Error("1,2 should not match")
	}
}
