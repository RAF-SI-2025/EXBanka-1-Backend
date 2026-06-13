package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type fakeFundLookup struct{ fund *model.InvestmentFund }

func (f fakeFundLookup) GetByID(id uint64) (*model.InvestmentFund, error) {
	if f.fund != nil && f.fund.ID == id {
		return f.fund, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func forexFixture(t *testing.T) *orderServiceFixture {
	t.Helper()
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(&model.Listing{
		ID: 2, SecurityID: 200, SecurityType: "forex", ExchangeID: 1,
		Exchange: model.StockExchange{ID: 1, Currency: "USD", TimeZone: "0"},
		Price:    decimal.NewFromFloat(1.10), High: decimal.NewFromFloat(1.10),
	})
	fx.forexRepo.add(&model.ForexPair{
		ID: 200, Ticker: "EURUSD", BaseCurrency: "EUR", QuoteCurrency: "USD",
		ExchangeRate: decimal.NewFromFloat(1.10),
	})
	return fx
}

func TestCreateOrder_Forex_QuoteCurrencyMismatch(t *testing.T) {
	fx := forexFixture(t)
	fx.accountClient.stub.accountCcy[77] = "EUR" // wrong: should be USD (the pair quote)
	fx.accountClient.stub.accountCcy[88] = "EUR"
	base := uint64(88)
	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 2, Direction: "buy",
		OrderType: "market", Quantity: 3, AccountID: 77, BaseAccountID: &base,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for quote mismatch, got %v", err)
	}
}

func TestCreateOrder_Forex_BaseCurrencyMismatch(t *testing.T) {
	fx := forexFixture(t)
	fx.accountClient.stub.accountCcy[77] = "USD" // correct quote
	fx.accountClient.stub.accountCcy[88] = "USD" // wrong base: should be EUR
	base := uint64(88)
	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 2, Direction: "buy",
		OrderType: "market", Quantity: 3, AccountID: 77, BaseAccountID: &base,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for base mismatch, got %v", err)
	}
}

func TestCreateOrder_OnBehalfOfFund_Branches(t *testing.T) {
	fund := &model.InvestmentFund{ID: 50, Name: "F", ManagerEmployeeID: 9, RSDAccountID: 7001, Active: true}

	// Fund support not configured (no fundRepo wired).
	plain := newOrderServiceFixture()
	plain.listingRepo.addListing(defaultListing(1))
	if _, err := plain.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 1, SystemType: "employee", ListingID: 1, Direction: "buy", OrderType: "market",
		Quantity: 1, OnBehalfOfFundID: 50, ActingEmployeeID: 9,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no fundRepo → FailedPrecondition, got %v", err)
	}

	build := func() *orderServiceFixture {
		fx := newOrderServiceFixture()
		fx.listingRepo.addListing(defaultListing(1))
		fx.svc = fx.svc.WithFundSupport(fakeFundLookup{fund: fund})
		fx.accountClient.stub.accountCcy[7001] = "USD"
		return fx
	}
	base := CreateOrderRequest{
		UserID: 1, SystemType: "employee", ListingID: 1, Direction: "buy", OrderType: "market", Quantity: 1,
	}

	// Fund not found.
	if _, err := build().svc.CreateOrder(context.Background(), withFund(base, 999, 9, 0)); status.Code(err) != codes.NotFound {
		t.Errorf("unknown fund → NotFound, got %v", err)
	}
	// Missing acting employee.
	if _, err := build().svc.CreateOrder(context.Background(), withFund(base, 50, 0, 0)); status.Code(err) != codes.PermissionDenied {
		t.Errorf("missing acting employee → PermissionDenied, got %v", err)
	}
	// Wrong manager.
	if _, err := build().svc.CreateOrder(context.Background(), withFund(base, 50, 8, 0)); status.Code(err) != codes.PermissionDenied {
		t.Errorf("non-manager → PermissionDenied, got %v", err)
	}
	// Account id disagrees with fund RSD account.
	if _, err := build().svc.CreateOrder(context.Background(), withFund(base, 50, 9, 12345)); status.Code(err) != codes.InvalidArgument {
		t.Errorf("account mismatch → InvalidArgument, got %v", err)
	}
	// Inactive fund.
	inactive := *fund
	inactive.Active = false
	fxIn := newOrderServiceFixture()
	fxIn.listingRepo.addListing(defaultListing(1))
	fxIn.svc = fxIn.svc.WithFundSupport(fakeFundLookup{fund: &inactive})
	if _, err := fxIn.svc.CreateOrder(context.Background(), withFund(base, 50, 9, 0)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("inactive fund → FailedPrecondition, got %v", err)
	}

	// Happy: account auto-resolves to the fund RSD account.
	if _, err := build().svc.CreateOrder(context.Background(), withFund(base, 50, 9, 0)); err != nil {
		t.Errorf("fund order happy path: %v", err)
	}
}

func withFund(r CreateOrderRequest, fundID, employeeID, accountID uint64) CreateOrderRequest {
	r.OnBehalfOfFundID = fundID
	r.ActingEmployeeID = employeeID
	r.AccountID = accountID
	return r
}
