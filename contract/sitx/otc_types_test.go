package sitx_test

import (
	"encoding/json"
	"testing"

	"github.com/exbanka/contract/sitx"
	"github.com/shopspring/decimal"
)

func TestOtcOffer_RoundTrip(t *testing.T) {
	in := sitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          100,
		PricePerStock:   decimal.NewFromFloat(180.50),
		Currency:        "USD",
		Premium:         decimal.NewFromFloat(700),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  sitx.ForeignBankId{RoutingNumber: 222, ID: "user-1"},
	}
	raw, _ := json.Marshal(in)
	var out sitx.OtcOffer
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Ticker != "AAPL" || out.Amount != 100 || !out.PricePerStock.Equal(decimal.NewFromFloat(180.50)) {
		t.Errorf("got %+v", out)
	}
	if out.LastModifiedBy.RoutingNumber != 222 {
		t.Errorf("foreignBankId routing: %d", out.LastModifiedBy.RoutingNumber)
	}
}

func TestOptionDescription_RoundTrip(t *testing.T) {
	in := sitx.OptionDescription{
		Ticker:         "AAPL",
		Amount:         50,
		StrikePrice:    decimal.NewFromFloat(200),
		Currency:       "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  sitx.ForeignBankId{RoutingNumber: 222, ID: "neg-7"},
	}
	raw, _ := json.Marshal(in)
	var out sitx.OptionDescription
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.NegotiationID.ID != "neg-7" {
		t.Errorf("got %+v", out)
	}
}

func TestUserInformation_RoundTrip(t *testing.T) {
	in := sitx.UserInformation{
		ID:        sitx.ForeignBankId{RoutingNumber: 222, ID: "u1"},
		FirstName: "Marko",
		LastName:  "Marković",
	}
	raw, _ := json.Marshal(in)
	var out sitx.UserInformation
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.FirstName != "Marko" || out.ID.ID != "u1" {
		t.Errorf("got %+v", out)
	}
}

func TestPublicStocksResponse_RoundTrip(t *testing.T) {
	in := sitx.PublicStocksResponse{
		Stocks: []sitx.PublicStock{
			{
				OwnerID:       sitx.ForeignBankId{RoutingNumber: 111, ID: "client-7"},
				Ticker:        "MSFT",
				Amount:        25,
				PricePerStock: decimal.NewFromFloat(420.10),
				Currency:      "USD",
			},
		},
	}
	raw, _ := json.Marshal(in)
	var out sitx.PublicStocksResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Stocks) != 1 || out.Stocks[0].Ticker != "MSFT" {
		t.Errorf("got %+v", out)
	}
}
