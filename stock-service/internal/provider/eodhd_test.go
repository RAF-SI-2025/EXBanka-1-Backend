package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchExchanges(t *testing.T) {
	response := []map[string]string{
		{
			"Name":         "USA Stocks",
			"Code":         "US",
			"OperatingMIC": "XNAS,XNYS",
			"Country":      "USA",
			"Currency":     "USD",
			"CountryISO2":  "US",
			"CountryISO3":  "USA",
		},
		{
			"Name":         "London Exchange",
			"Code":         "LSE",
			"OperatingMIC": "XLON",
			"Country":      "UK",
			"Currency":     "GBP",
			"CountryISO2":  "GB",
			"CountryISO3":  "GBR",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/exchanges-list/" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("api_token") != "test-key" {
			t.Errorf("missing api_token")
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewEODHDClient("test-key")
	client.baseURL = server.URL

	exchanges, err := client.FetchExchanges()
	if err != nil {
		t.Fatalf("FetchExchanges error: %v", err)
	}
	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}
	if exchanges[0].Code != "US" {
		t.Errorf("expected code US, got %s", exchanges[0].Code)
	}
	if exchanges[0].OperatingMIC != "XNAS,XNYS" {
		t.Errorf("expected OperatingMIC XNAS,XNYS, got %s", exchanges[0].OperatingMIC)
	}
}

func TestFetchExchangeDetails(t *testing.T) {
	response := map[string]interface{}{
		"Name":     "USA Stocks",
		"Code":     "US",
		"Timezone": "America/New_York",
		"isOpen":   false,
		"TradingHours": map[string]interface{}{
			"OpenHour":  "09:30:00",
			"CloseHour": "16:00:00",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/exchange-details/US" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewEODHDClient("test-key")
	client.baseURL = server.URL

	details, err := client.FetchExchangeDetails("US")
	if err != nil {
		t.Fatalf("FetchExchangeDetails error: %v", err)
	}
	if details.Timezone != "America/New_York" {
		t.Errorf("expected timezone America/New_York, got %s", details.Timezone)
	}
	if details.TradingHours.OpenHour != "09:30:00" {
		t.Errorf("expected open 09:30:00, got %s", details.TradingHours.OpenHour)
	}
}

func TestFetchExchanges_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewEODHDClient("test-key")
	client.baseURL = server.URL

	_, err := client.FetchExchanges()
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
