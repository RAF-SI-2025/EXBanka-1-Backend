package sitx_test

import (
	"bytes"
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

func TestOptionDescriptionSpecShape(t *testing.T) {
	od := sitx.OptionDescription{
		NegotiationID:  sitx.ForeignBankId{RoutingNumber: 111, ID: "neg-1"},
		Stock:          sitx.StockDescription{Ticker: "WMT"},
		PricePerUnit:   sitx.MonetaryValue{Amount: sitx.DecimalNumber{Decimal: decimal.RequireFromString("50")}, Currency: "RSD"},
		SettlementDate: "2026-12-31T00:00:00+02:00",
		Amount:         10,
	}
	got, err := json.Marshal(od)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"negotiationId":{"routingNumber":111,"id":"neg-1"},"stock":{"ticker":"WMT"},"pricePerUnit":{"amount":50,"currency":"RSD"},"settlementDate":"2026-12-31T00:00:00+02:00","amount":10}`
	var g, w bytes.Buffer
	_ = json.Compact(&g, got)
	_ = json.Compact(&w, []byte(want))
	if g.String() != w.String() {
		t.Errorf("shape mismatch:\n got: %s\nwant: %s", g.String(), w.String())
	}
	// Verify removed flat fields do not appear anywhere in the output.
	// Note: "ticker" and "currency" are NOT checked here because they legitimately
	// appear nested inside "stock":{"ticker":...} and "pricePerUnit":{"currency":...}
	// respectively — a bytes.Contains check cannot distinguish top-level from nested
	// occurrences. The shape equality check above (got vs want) is the authoritative
	// assertion that no unexpected top-level flat fields are present.
	for _, bad := range []string{`"strikePrice"`, `"intent"`} {
		if bytes.Contains(got, []byte(bad)) {
			t.Errorf("unexpected legacy field %s in %s", bad, got)
		}
	}
}

func TestOptionDescription_RoundTrip(t *testing.T) {
	in := sitx.OptionDescription{
		NegotiationID:  sitx.ForeignBankId{RoutingNumber: 222, ID: "neg-7"},
		Stock:          sitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   sitx.MonetaryValue{Amount: sitx.DecimalNumber{Decimal: decimal.RequireFromString("200")}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         50,
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

func TestUserInformation_SpecShape(t *testing.T) {
	in := sitx.UserInformation{
		BankDisplayName: "EXBanka",
		DisplayName:     "Marko Marković",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"bankDisplayName":"EXBanka","displayName":"Marko Marković"}` {
		t.Fatalf("spec shape mismatch: %s", raw)
	}
	var out sitx.UserInformation
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.BankDisplayName != "EXBanka" || out.DisplayName != "Marko Marković" {
		t.Errorf("got %+v", out)
	}
}

func TestPublicStocksResponse_RoundTrip(t *testing.T) {
	in := sitx.PublicStocksResponse{
		{
			Stock: sitx.StockDescription{Ticker: "MSFT"},
			Sellers: []sitx.PublicSeller{
				{Seller: sitx.ForeignBankId{RoutingNumber: 111, ID: "client-7"}, Amount: 25},
			},
		},
	}
	raw, _ := json.Marshal(in)
	var out sitx.PublicStocksResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].Stock.Ticker != "MSFT" {
		t.Errorf("got %+v", out)
	}
	if len(out[0].Sellers) != 1 || out[0].Sellers[0].Seller.ID != "client-7" {
		t.Errorf("sellers: %+v", out[0].Sellers)
	}
}
