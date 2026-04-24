package handler

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestToListingInfo_PopulatesID(t *testing.T) {
	info := toListingInfo(
		42,                              // listingID
		7,                               // exchangeID
		"NYSE",                          // exchangeAcronym
		"USD",                           // exchangeCurrency
		decimal.NewFromInt(100),         // price
		decimal.NewFromInt(101),         // high
		decimal.NewFromInt(99),          // low
		decimal.NewFromInt(1),           // change
		1_234_567,                       // volume
		decimal.NewFromInt(50),          // initialMarginCost
		time.Unix(1_700_000_000, 0).UTC(),
	)
	if info.Currency != "USD" {
		t.Errorf("expected Currency=USD, got %q", info.Currency)
	}
	if info.Id != 42 {
		t.Fatalf("expected Id=42, got %d", info.Id)
	}
	if info.Volume != 1_234_567 {
		t.Fatalf("expected Volume=1234567, got %d", info.Volume)
	}
}

func TestToStockItem_PopulatesListingID(t *testing.T) {
	s := &model.Stock{ID: 10, Ticker: "AAPL", Name: "Apple"}
	s.Exchange.Acronym = "NASDAQ"
	item := toStockItem(s, 99)
	if item.Listing.Id != 99 {
		t.Fatalf("expected Listing.Id=99, got %d", item.Listing.Id)
	}
	if item.Id != 10 {
		t.Fatalf("expected Id (security)=10, got %d", item.Id)
	}
}

func TestToForexPairItem_PopulatesListingID(t *testing.T) {
	fp := &model.ForexPair{ID: 5, Ticker: "EUR/USD", BaseCurrency: "EUR", QuoteCurrency: "USD"}
	item := toForexPairItem(fp, 77)
	if item.Listing.Id != 77 {
		t.Fatalf("expected Listing.Id=77, got %d", item.Listing.Id)
	}
}

func TestToFuturesItem_PopulatesListingID(t *testing.T) {
	fc := &model.FuturesContract{ID: 7, Ticker: "CL"}
	item := toFuturesItem(fc, 33)
	if item.Listing.Id != 33 {
		t.Fatalf("expected Listing.Id=33, got %d", item.Listing.Id)
	}
}
