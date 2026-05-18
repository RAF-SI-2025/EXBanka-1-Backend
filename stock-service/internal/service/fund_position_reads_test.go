package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// fundPositionDB returns a sqlite-backed FundService with the position-reads
// repos wired so the rich DTO methods exercise their main code path.
func fundPositionDB(t *testing.T) (*FundService, *gorm.DB, *repository.ClientFundPositionRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{},
		&model.ClientFundPosition{},
		&model.FundPositionSettlement{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	posRepo := repository.NewClientFundPositionRepository(db)
	bac := &fakeBankAccountClient{nextID: 1000}
	svc := NewFundService(repo, bac, nil)
	// Wire only the positions repo so the early-exit "deps not wired" branch
	// is partially activated. fundValueRSD will return cash only.
	svc.positions = posRepo
	return svc, db, posRepo
}

func TestFundService_ListMyPositionsDTO_NoPositionsRepoWired(t *testing.T) {
	fx := newFundFixture(t)
	uid := uint64(7)
	rows, err := fx.svc.ListMyPositionsDTO(context.Background(), model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil when positions repo not wired")
	}
}

func TestFundService_ListMyPositionsDTO_HappyPath(t *testing.T) {
	svc, _, posRepo := fundPositionDB(t)
	// Seed a fund.
	created, err := svc.Create(context.Background(), CreateFundInput{Name: "Foo"})
	if err != nil {
		t.Fatalf("create fund: %v", err)
	}
	uid := uint64(7)
	if err := posRepo.IncrementContribution(created.ID, model.OwnerClient, &uid, decimal.NewFromInt(1000), uint64(1)); err != nil {
		t.Fatalf("increment: %v", err)
	}
	rows, err := svc.ListMyPositionsDTO(context.Background(), model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d", len(rows))
	}
}

func TestFundService_ListBankPositionsDTO(t *testing.T) {
	fx := newFundFixture(t)
	rows, err := fx.svc.ListBankPositionsDTO(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil when positions repo not wired")
	}
}

func TestFundService_FundValueRSD_NilDepsReturnsCashOnly(t *testing.T) {
	fx := newFundFixture(t)
	created, err := fx.svc.Create(context.Background(), CreateFundInput{Name: "X"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := fx.svc.fundValueRSD(context.Background(), created)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Cash defaults to zero (no accounts wired); function returns cash only.
	if !got.IsZero() {
		t.Errorf("got %s want 0 when accounts deps not wired", got)
	}
}
