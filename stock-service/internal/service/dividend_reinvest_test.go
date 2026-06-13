package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func reinvestFund() *model.InvestmentFund {
	return &model.InvestmentFund{ID: 1, Name: "F", ManagerEmployeeID: 5, RSDAccountID: 99}
}

func TestReinvest_NoDepsWired_NoOp(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient()) // no WithReinvest
	// Should not panic and place nothing.
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(1000))
}

func TestReinvest_NoUsableListing_KeepsCash(t *testing.T) {
	db := openDividendTestDB(t)
	placer := &fakeReinvestOrderPlacer{}
	// nil listing → no usable listing → no order.
	svc := newDividendService(db, newFakeDividendAccountClient()).
		WithReinvest(placer, &fakeListingLookup{listing: nil}, nil)
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(1000))
	if len(placer.calls) != 0 {
		t.Errorf("no usable listing → no order, got %d", len(placer.calls))
	}
}

func TestReinvest_ZeroPriceListing_KeepsCash(t *testing.T) {
	db := openDividendTestDB(t)
	placer := &fakeReinvestOrderPlacer{}
	svc := newDividendService(db, newFakeDividendAccountClient()).
		WithReinvest(placer, &fakeListingLookup{listing: &model.Listing{ID: 1, Price: decimal.Zero}}, nil)
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(1000))
	if len(placer.calls) != 0 {
		t.Errorf("zero price → no order, got %d", len(placer.calls))
	}
}

func TestReinvest_DividendTooSmall_KeepsCash(t *testing.T) {
	db := openDividendTestDB(t)
	placer := &fakeReinvestOrderPlacer{}
	// price 200, gross 100 → floor(100/200)=0 shares → keep cash.
	svc := newDividendService(db, newFakeDividendAccountClient()).
		WithReinvest(placer, &fakeListingLookup{listing: &model.Listing{ID: 1, Price: decimal.NewFromInt(200)}}, nil)
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(100))
	if len(placer.calls) != 0 {
		t.Errorf("sub-share dividend → no order, got %d", len(placer.calls))
	}
}

func TestReinvest_FXConvertedPrice_PlacesOrder(t *testing.T) {
	db := openDividendTestDB(t)
	placer := &fakeReinvestOrderPlacer{}
	// USD-priced listing; exchange converts 2 USD → 200 RSD.
	listing := &model.Listing{ID: 7, Price: decimal.NewFromInt(2), Exchange: model.StockExchange{Currency: "USD"}}
	exch := &fakeFundExchangeClient{convert: "200"}
	svc := newDividendService(db, newFakeDividendAccountClient()).
		WithReinvest(placer, &fakeListingLookup{listing: listing}, exch)
	// gross 1000 RSD, priceRSD 200 → 5 shares.
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(1000))
	if len(placer.calls) != 1 || placer.calls[0].Quantity != 5 {
		t.Fatalf("expected 1 order of 5 shares, got %+v", placer.calls)
	}
}

func TestReinvest_FXConvertError_KeepsCash(t *testing.T) {
	db := openDividendTestDB(t)
	placer := &fakeReinvestOrderPlacer{}
	listing := &model.Listing{ID: 7, Price: decimal.NewFromInt(2), Exchange: model.StockExchange{Currency: "USD"}}
	exch := &fakeFundExchangeClient{failNext: context.DeadlineExceeded}
	svc := newDividendService(db, newFakeDividendAccountClient()).
		WithReinvest(placer, &fakeListingLookup{listing: listing}, exch)
	svc.reinvestFundDividend(context.Background(), reinvestFund(), 10, decimal.NewFromInt(1000))
	if len(placer.calls) != 0 {
		t.Errorf("FX convert error → keep cash, got %d", len(placer.calls))
	}
}
