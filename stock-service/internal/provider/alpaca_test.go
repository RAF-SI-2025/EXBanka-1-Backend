package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlpacaFetchAssets(t *testing.T) {
	response := []map[string]interface{}{
		{"symbol": "AAPL", "name": "Apple Inc.", "exchange": "NASDAQ", "status": "active", "tradable": true},
		{"symbol": "MSFT", "name": "Microsoft Corporation", "exchange": "NASDAQ", "status": "active", "tradable": true},
		{"symbol": "DELISTED", "name": "Gone Corp", "exchange": "NYSE", "status": "inactive", "tradable": false},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/assets" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("APCA-API-KEY-ID") != "test-key" {
			t.Errorf("missing APCA-API-KEY-ID header")
		}
		if r.Header.Get("APCA-API-SECRET-KEY") != "test-secret" {
			t.Errorf("missing APCA-API-SECRET-KEY header")
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewAlpacaClient("test-key", "test-secret")
	client.baseURL = server.URL

	assets, err := client.FetchAssets()
	if err != nil {
		t.Fatalf("FetchAssets error: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected 2 tradable assets, got %d", len(assets))
	}
	if assets[0].Symbol != "AAPL" {
		t.Errorf("expected AAPL, got %s", assets[0].Symbol)
	}
	if assets[1].Exchange != "NASDAQ" {
		t.Errorf("expected NASDAQ, got %s", assets[1].Exchange)
	}
}

func TestAlpacaFetchAssets_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := NewAlpacaClient("bad-key", "bad-secret")
	client.baseURL = server.URL

	_, err := client.FetchAssets()
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}
