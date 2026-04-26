package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type fakeBankAccountClient struct {
	created  int32
	nextID   uint64
	failNext bool
}

func (f *fakeBankAccountClient) CreateBankAccount(ctx context.Context, in *accountpb.CreateBankAccountRequest) (*accountpb.AccountResponse, error) {
	if f.failNext {
		return nil, errors.New("simulated failure")
	}
	atomic.AddInt32(&f.created, 1)
	id := atomic.AddUint64(&f.nextID, 1)
	return &accountpb.AccountResponse{Id: id, AccountNumber: "BANK"}, nil
}

type fundFixture struct {
	svc               *FundService
	bankAccountClient *fakeBankAccountClient
}

func newFundFixture(t *testing.T) *fundFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	bac := &fakeBankAccountClient{nextID: 1000}
	return &fundFixture{
		svc:               NewFundService(repo, bac, nil),
		bankAccountClient: bac,
	}
}

func TestFundService_Create_GeneratesRSDAccount(t *testing.T) {
	fx := newFundFixture(t)
	got, err := fx.svc.Create(context.Background(), CreateFundInput{
		ActorEmployeeID:        25,
		Name:                   "Alpha",
		Description:            "IT focus",
		MinimumContributionRSD: decimal.NewFromInt(1000),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ManagerEmployeeID != 25 {
		t.Errorf("manager %d", got.ManagerEmployeeID)
	}
	if got.RSDAccountID == 0 {
		t.Errorf("expected RSDAccountID != 0")
	}
	if atomic.LoadInt32(&fx.bankAccountClient.created) != 1 {
		t.Errorf("CreateBankAccount not called once")
	}
}

func TestFundService_Create_RejectsDuplicateName(t *testing.T) {
	fx := newFundFixture(t)
	if _, err := fx.svc.Create(context.Background(), CreateFundInput{
		ActorEmployeeID: 25, Name: "Beta",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := fx.svc.Create(context.Background(), CreateFundInput{
		ActorEmployeeID: 25, Name: "Beta",
	}); err == nil {
		t.Errorf("expected duplicate-name error")
	}
}

func TestFundService_Update_RejectsNonManager(t *testing.T) {
	fx := newFundFixture(t)
	f, err := fx.svc.Create(context.Background(), CreateFundInput{
		ActorEmployeeID: 25, Name: "Gamma",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newName := "GammaPrime"
	_, err = fx.svc.Update(context.Background(), UpdateFundInput{
		ActorEmployeeID: 99, // different from manager 25
		FundID:          f.ID,
		Name:            &newName,
	})
	if err == nil {
		t.Errorf("expected non-manager update to fail")
	}
}
