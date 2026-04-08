package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFinnhubFetchForexSymbols(t *testing.T) {
	response := []map[string]string{
		{"symbol": "OANDA:EUR_USD", "displaySymbol": "EUR/USD", "description": "Euro/US Dollar"},
		{"symbol": "OANDA:GBP_JPY", "displaySymbol": "GBP/JPY", "description": "British Pound/Japanese Yen"},
		{"symbol": "OANDA:BTC_USD", "displaySymbol": "BTC/USD", "description": "Bitcoin/US Dollar"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/forex/symbol" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("token") != "test-key" {
			t.Errorf("missing token")
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewFinnhubClient("test-key")
	client.baseURL = server.URL

	symbols, err := client.FetchForexSymbols()
	if err != nil {
		t.Fatalf("FetchForexSymbols error: %v", err)
	}
	if len(symbols) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(symbols))
	}
	if symbols[0].DisplaySymbol != "EUR/USD" {
		t.Errorf("expected EUR/USD, got %s", symbols[0].DisplaySymbol)
	}
}

func TestFinnhubFetchForexRates(t *testing.T) {
	response := map[string]interface{}{
		"base": "USD",
		"quote": map[string]float64{
			"EUR": 0.85,
			"GBP": 0.73,
			"JPY": 110.5,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/forex/rates" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("base") != "USD" {
			t.Errorf("expected base=USD, got %s", r.URL.Query().Get("base"))
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewFinnhubClient("test-key")
	client.baseURL = server.URL

	rates, err := client.FetchForexRates("USD")
	if err != nil {
		t.Fatalf("FetchForexRates error: %v", err)
	}
	if rates["EUR"] != 0.85 {
		t.Errorf("expected EUR=0.85, got %f", rates["EUR"])
	}
	if rates["JPY"] != 110.5 {
		t.Errorf("expected JPY=110.5, got %f", rates["JPY"])
	}
}

func TestFinnhubFetchForexSymbols_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewFinnhubClient("bad-key")
	client.baseURL = server.URL

	_, err := client.FetchForexSymbols()
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
