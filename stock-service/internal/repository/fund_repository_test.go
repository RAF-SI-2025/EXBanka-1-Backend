package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestFundRepository_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)

	f := &model.InvestmentFund{
		Name:                   "Alpha",
		ManagerEmployeeID:      25,
		MinimumContributionRSD: decimal.NewFromInt(1000),
		RSDAccountID:           4812,
		Active:                 true,
	}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("expected autoincrement id")
	}
	got, err := r.GetByID(f.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Alpha" {
		t.Errorf("got %q want Alpha", got.Name)
	}
}

func TestFundRepository_NameUnique_AmongActive(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)

	a := &model.InvestmentFund{Name: "Beta", ManagerEmployeeID: 1, RSDAccountID: 100, Active: true}
	if err := r.Create(a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := &model.InvestmentFund{Name: "Beta", ManagerEmployeeID: 1, RSDAccountID: 101, Active: true}
	if err := r.Create(b); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestClientFundPositionRepository_UpsertIncrements(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)

	delta := decimal.NewFromInt(500)
	if err := r.IncrementContribution(1, 99, "client", delta); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := r.IncrementContribution(1, 99, "client", delta); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, err := r.GetByOwner(1, 99, "client")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.TotalContributedRSD.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("got %s want 1000", got.TotalContributedRSD)
	}
}

func TestFundHoldingRepository_FifoOrder(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundHolding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundHoldingRepository(db)
	for i := uint64(1); i <= 3; i++ {
		if err := r.Upsert(&model.FundHolding{
			FundID: 1, SecurityType: "stock", SecurityID: i,
			Quantity: 10, AveragePriceRSD: decimal.NewFromInt(100),
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, err := r.ListByFundFIFO(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 || got[0].SecurityID != 1 || got[2].SecurityID != 3 {
		t.Errorf("FIFO order broken: got %+v", got)
	}
}
