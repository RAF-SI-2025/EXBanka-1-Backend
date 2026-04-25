package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlphaVantage_FetchQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("function") != "GLOBAL_QUOTE" {
			t.Errorf("expected function=GLOBAL_QUOTE, got %s", r.URL.Query().Get("function"))
		}
		if r.URL.Query().Get("symbol") != "AAPL" {
			t.Errorf("expected symbol=AAPL, got %s", r.URL.Query().Get("symbol"))
		}
		if r.URL.Query().Get("apikey") != "test-key" {
			t.Errorf("expected apikey=test-key")
		}
		_, _ = w.Write([]byte(`{
			"Global Quote": {
				"05. price": "180.50",
				"03. high": "182.00",
				"04. low": "179.00",
				"09. change": "1.25",
				"06. volume": "12345678"
			}
		}`))
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	q, err := c.FetchQuote("AAPL")
	if err != nil {
		t.Fatalf("FetchQuote error: %v", err)
	}
	if q.Price.String() != "180.5" {
		t.Errorf("expected price 180.5, got %s", q.Price.String())
	}
	if q.Volume != 12345678 {
		t.Errorf("expected volume 12345678, got %d", q.Volume)
	}
}

func TestAlphaVantage_FetchQuote_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	_, err := c.FetchQuote("AAPL")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "fetch quote") {
		t.Errorf("expected wrapped error mentioning 'fetch quote', got %q", err.Error())
	}
}

func TestAlphaVantage_FetchQuote_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	_, err := c.FetchQuote("AAPL")
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
	if !strings.Contains(err.Error(), "parse quote") {
		t.Errorf("expected error mentioning 'parse quote', got %q", err.Error())
	}
}

func TestAlphaVantage_FetchOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("function") != "OVERVIEW" {
			t.Errorf("expected function=OVERVIEW, got %s", r.URL.Query().Get("function"))
		}
		_, _ = w.Write([]byte(`{
			"Name": "Apple Inc",
			"Exchange": "NASDAQ",
			"SharesOutstanding": "16000000000",
			"DividendYield": "0.005"
		}`))
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	o, err := c.FetchOverview("AAPL")
	if err != nil {
		t.Fatalf("FetchOverview error: %v", err)
	}
	if o.Name != "Apple Inc" {
		t.Errorf("expected name Apple Inc, got %q", o.Name)
	}
	if o.OutstandingShares != 16000000000 {
		t.Errorf("expected shares 16000000000, got %d", o.OutstandingShares)
	}
	if o.Exchange != "NASDAQ" {
		t.Errorf("expected exchange NASDAQ, got %q", o.Exchange)
	}
}

func TestAlphaVantage_FetchOverview_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	_, err := c.FetchOverview("AAPL")
	if err == nil {
		t.Fatal("expected error on 502")
	}
}

func TestAlphaVantage_FetchStockData_MergesQuoteAndOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("function") {
		case "GLOBAL_QUOTE":
			_, _ = w.Write([]byte(`{
				"Global Quote": {
					"05. price": "180.50",
					"03. high": "182.00",
					"04. low": "179.00",
					"09. change": "1.25",
					"06. volume": "12345"
				}
			}`))
		case "OVERVIEW":
			_, _ = w.Write([]byte(`{
				"Name": "Apple",
				"Exchange": "NASDAQ",
				"SharesOutstanding": "16000000000",
				"DividendYield": "0.005"
			}`))
		default:
			http.Error(w, "unexpected function", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	stock, err := c.FetchStockData("AAPL")
	if err != nil {
		t.Fatalf("FetchStockData error: %v", err)
	}
	if stock.Ticker != "AAPL" {
		t.Errorf("expected ticker AAPL, got %q", stock.Ticker)
	}
	if stock.Name != "Apple" {
		t.Errorf("expected name Apple, got %q", stock.Name)
	}
	if stock.OutstandingShares != 16000000000 {
		t.Errorf("expected outstanding 16B, got %d", stock.OutstandingShares)
	}
	if stock.Volume != 12345 {
		t.Errorf("expected volume 12345, got %d", stock.Volume)
	}
}

func TestAlphaVantage_FetchStockData_QuoteFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	_, err := c.FetchStockData("AAPL")
	if err == nil {
		t.Fatal("expected error when quote endpoint fails")
	}
}

func TestAlphaVantage_FetchStockData_OverviewFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("function") == "GLOBAL_QUOTE" {
			_, _ = w.Write([]byte(`{
				"Global Quote": {"05. price": "1", "03. high": "1", "04. low": "1", "09. change": "0", "06. volume": "1"}
			}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewAlphaVantageClient("test-key")
	c.baseURL = server.URL

	_, err := c.FetchStockData("AAPL")
	if err == nil {
		t.Fatal("expected error when overview endpoint fails")
	}
	if !strings.Contains(err.Error(), "fetch overview") {
		t.Errorf("expected error mentioning 'fetch overview', got %q", err.Error())
	}
}

func TestNewAlphaVantageClient_PopulatesDefaults(t *testing.T) {
	c := NewAlphaVantageClient("k")
	if c.apiKey != "k" {
		t.Errorf("apiKey not set")
	}
	if c.baseURL != alphaVantageBaseURL {
		t.Errorf("baseURL default mismatch: %q", c.baseURL)
	}
	if c.httpClient == nil {
		t.Fatal("httpClient nil")
	}
}
