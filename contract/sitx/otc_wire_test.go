package sitx

import (
	"encoding/json"
	"testing"
)

func TestPublicStocksResponse_BareArrayWithSellers(t *testing.T) {
	resp := PublicStocksResponse{
		{
			Stock: StockDescription{Ticker: "AAPL"},
			Sellers: []PublicSeller{
				{Seller: ForeignBankId{RoutingNumber: 111, ID: "client-3"}, Amount: 50},
			},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"stock":{"ticker":"AAPL"},"sellers":[{"seller":{"routingNumber":111,"id":"client-3"},"amount":50}]}]`
	if string(b) != want {
		t.Fatalf("public-stock shape:\n got: %s\nwant: %s", string(b), want)
	}
}
