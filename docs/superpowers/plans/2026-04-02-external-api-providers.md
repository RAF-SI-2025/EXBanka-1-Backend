# External API Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace static/hardcoded data sources in stock-service with live API calls to EODHD (exchanges), Alpaca (stock listings), and Finnhub (forex pairs), keeping AlphaVantage for stock details, with verified hardcoded fallbacks.

**Architecture:** Dedicated provider per API in `stock-service/internal/provider/`. The existing `security_sync.go` orchestrator calls each provider with fallback to CSV/hardcoded data. Each provider is nil-safe — when the API key is empty, the corresponding client is nil and sync skips directly to fallback.

**Tech Stack:** Go 1.25, net/http, encoding/json, httptest (testing), GORM/PostgreSQL

**Spec:** `docs/superpowers/specs/2026-04-02-external-api-providers-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `stock-service/internal/config/config.go` | Modify | Add 4 new API key env vars |
| `stock-service/internal/provider/eodhd.go` | Create | EODHD API client for exchanges |
| `stock-service/internal/provider/eodhd_test.go` | Create | Tests with httptest mock server |
| `stock-service/internal/provider/alpaca.go` | Create | Alpaca API client for stock listings |
| `stock-service/internal/provider/alpaca_test.go` | Create | Tests with httptest mock server |
| `stock-service/internal/provider/finnhub.go` | Create | Finnhub API client for forex pairs + rates |
| `stock-service/internal/provider/finnhub_test.go` | Create | Tests with httptest mock server |
| `stock-service/internal/service/exchange_hours.go` | Modify | Support IANA timezone strings |
| `stock-service/internal/service/exchange_hours_test.go` | Create | Tests for timezone parsing |
| `stock-service/internal/service/security_sync.go` | Modify | Use new providers with fallbacks |
| `stock-service/cmd/main.go` | Modify | Wire new provider clients |
| `docker-compose.yml` | Modify | Add new env vars to stock-service |

---

### Task 1: Add API key configuration

**Files:**
- Modify: `stock-service/internal/config/config.go`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add new config fields**

In `stock-service/internal/config/config.go`, add 4 fields to the `Config` struct after line 27 (`StateAccountNo`):

```go
// External API keys
EODHDAPIKey     string
AlpacaAPIKey    string
AlpacaAPISecret string
FinnhubAPIKey   string
```

In the `Load()` function, add these lines inside the return block after line 54 (`StateAccountNo`):

```go
EODHDAPIKey:     getEnv("EODHD_API_KEY", ""),
AlpacaAPIKey:    getEnv("ALPACA_API_KEY", ""),
AlpacaAPISecret: getEnv("ALPACA_API_SECRET", ""),
FinnhubAPIKey:   getEnv("FINNHUB_API_KEY", ""),
```

- [ ] **Step 2: Add env vars to docker-compose.yml**

In `docker-compose.yml`, add to the `stock-service` environment block (after the `SECURITY_SYNC_INTERVAL_MINUTES` line, around line 493):

```yaml
      EODHD_API_KEY: ${EODHD_API_KEY:-}
      ALPACA_API_KEY: ${ALPACA_API_KEY:-}
      ALPACA_API_SECRET: ${ALPACA_API_SECRET:-}
      FINNHUB_API_KEY: ${FINNHUB_API_KEY:-}
```

- [ ] **Step 3: Verify build**

Run: `cd stock-service && go build ./cmd`
Expected: clean build, no errors

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/config/config.go docker-compose.yml
git commit -m "feat(stock): add EODHD, Alpaca, Finnhub API key config"
```

---

### Task 2: EODHD provider for exchanges

**Files:**
- Create: `stock-service/internal/provider/eodhd.go`
- Create: `stock-service/internal/provider/eodhd_test.go`

- [ ] **Step 1: Write the test file**

Create `stock-service/internal/provider/eodhd_test.go`:

```go
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
		json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd stock-service && go test ./internal/provider/ -run TestFetch -v`
Expected: FAIL — `NewEODHDClient` not defined

- [ ] **Step 3: Write the implementation**

Create `stock-service/internal/provider/eodhd.go`:

```go
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const eodhDefaultBaseURL = "https://eodhd.com"

// EODHDExchange represents one exchange from the /api/exchanges-list/ response.
type EODHDExchange struct {
	Name         string `json:"Name"`
	Code         string `json:"Code"`
	OperatingMIC string `json:"OperatingMIC"`
	Country      string `json:"Country"`
	Currency     string `json:"Currency"`
	CountryISO2  string `json:"CountryISO2"`
	CountryISO3  string `json:"CountryISO3"`
}

// EODHDExchangeDetails represents the /api/exchange-details/{CODE} response.
type EODHDExchangeDetails struct {
	Name         string              `json:"Name"`
	Code         string              `json:"Code"`
	Timezone     string              `json:"Timezone"`
	IsOpen       bool                `json:"isOpen"`
	TradingHours EODHDTradingHours   `json:"TradingHours"`
	Holidays     []EODHDHoliday      `json:"Holidays"`
}

type EODHDTradingHours struct {
	OpenHour  string `json:"OpenHour"`
	CloseHour string `json:"CloseHour"`
}

type EODHDHoliday struct {
	Holiday  string `json:"Holiday"`
	Date     string `json:"Date"`
}

// EODHDClient fetches exchange data from the EODHD API.
type EODHDClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewEODHDClient(apiKey string) *EODHDClient {
	return &EODHDClient{
		apiKey:  apiKey,
		baseURL: eodhDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchExchanges returns all exchanges from the EODHD exchanges-list endpoint.
func (c *EODHDClient) FetchExchanges() ([]EODHDExchange, error) {
	url := fmt.Sprintf("%s/api/exchanges-list/?api_token=%s&fmt=json", c.baseURL, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch exchanges: %w", err)
	}

	var exchanges []EODHDExchange
	if err := json.Unmarshal(body, &exchanges); err != nil {
		return nil, fmt.Errorf("parse exchanges: %w", err)
	}
	return exchanges, nil
}

// FetchExchangeDetails returns trading hours and timezone for a given exchange code.
func (c *EODHDClient) FetchExchangeDetails(code string) (*EODHDExchangeDetails, error) {
	url := fmt.Sprintf("%s/api/exchange-details/%s?api_token=%s&fmt=json", c.baseURL, code, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch exchange details for %s: %w", code, err)
	}

	var details EODHDExchangeDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("parse exchange details for %s: %w", code, err)
	}
	return &details, nil
}

func (c *EODHDClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("EODHD API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd stock-service && go test ./internal/provider/ -run TestFetch -v`
Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/provider/eodhd.go stock-service/internal/provider/eodhd_test.go
git commit -m "feat(stock): add EODHD provider for exchange data"
```

---

### Task 3: Alpaca provider for stock listings

**Files:**
- Create: `stock-service/internal/provider/alpaca.go`
- Create: `stock-service/internal/provider/alpaca_test.go`

- [ ] **Step 1: Write the test file**

Create `stock-service/internal/provider/alpaca_test.go`:

```go
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
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewAlpacaClient("test-key", "test-secret")
	client.baseURL = server.URL

	assets, err := client.FetchAssets()
	if err != nil {
		t.Fatalf("FetchAssets error: %v", err)
	}

	// Should only return tradable assets
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd stock-service && go test ./internal/provider/ -run TestAlpaca -v`
Expected: FAIL — `NewAlpacaClient` not defined

- [ ] **Step 3: Write the implementation**

Create `stock-service/internal/provider/alpaca.go`:

```go
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const alpacaDefaultBaseURL = "https://api.alpaca.markets"

// AlpacaAsset represents one asset from the /v2/assets response.
type AlpacaAsset struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"`
	Status   string `json:"status"`
	Tradable bool   `json:"tradable"`
}

// AlpacaClient fetches stock listings from the Alpaca API.
type AlpacaClient struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient *http.Client
}

func NewAlpacaClient(apiKey, apiSecret string) *AlpacaClient {
	return &AlpacaClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   alpacaDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchAssets returns all active, tradable US equity assets.
func (c *AlpacaClient) FetchAssets() ([]AlpacaAsset, error) {
	url := fmt.Sprintf("%s/v2/assets?status=active&asset_class=us_equity", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", c.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch assets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Alpaca API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var all []AlpacaAsset
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("parse assets: %w", err)
	}

	// Filter to only tradable assets
	tradable := make([]AlpacaAsset, 0, len(all))
	for _, a := range all {
		if a.Tradable {
			tradable = append(tradable, a)
		}
	}
	return tradable, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd stock-service && go test ./internal/provider/ -run TestAlpaca -v`
Expected: all 2 tests PASS

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/provider/alpaca.go stock-service/internal/provider/alpaca_test.go
git commit -m "feat(stock): add Alpaca provider for stock listings"
```

---

### Task 4: Finnhub provider for forex pairs and rates

**Files:**
- Create: `stock-service/internal/provider/finnhub.go`
- Create: `stock-service/internal/provider/finnhub_test.go`

- [ ] **Step 1: Write the test file**

Create `stock-service/internal/provider/finnhub_test.go`:

```go
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
		json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd stock-service && go test ./internal/provider/ -run TestFinnhub -v`
Expected: FAIL — `NewFinnhubClient` not defined

- [ ] **Step 3: Write the implementation**

Create `stock-service/internal/provider/finnhub.go`:

```go
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const finnhubDefaultBaseURL = "https://finnhub.io"

// FinnhubForexSymbol represents one forex pair from the /api/v1/forex/symbol response.
type FinnhubForexSymbol struct {
	Symbol        string `json:"symbol"`
	DisplaySymbol string `json:"displaySymbol"`
	Description   string `json:"description"`
}

// FinnhubClient fetches forex data from the Finnhub API.
type FinnhubClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewFinnhubClient(apiKey string) *FinnhubClient {
	return &FinnhubClient{
		apiKey:  apiKey,
		baseURL: finnhubDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchForexSymbols returns all forex pairs from Finnhub (exchange=oanda).
func (c *FinnhubClient) FetchForexSymbols() ([]FinnhubForexSymbol, error) {
	url := fmt.Sprintf("%s/api/v1/forex/symbol?exchange=oanda&token=%s", c.baseURL, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch forex symbols: %w", err)
	}

	var symbols []FinnhubForexSymbol
	if err := json.Unmarshal(body, &symbols); err != nil {
		return nil, fmt.Errorf("parse forex symbols: %w", err)
	}
	return symbols, nil
}

// FetchForexRates returns current exchange rates for a base currency.
// Returns map of quote currency code → rate.
func (c *FinnhubClient) FetchForexRates(base string) (map[string]float64, error) {
	url := fmt.Sprintf("%s/api/v1/forex/rates?base=%s&token=%s", c.baseURL, base, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch forex rates for %s: %w", base, err)
	}

	var raw struct {
		Base  string             `json:"base"`
		Quote map[string]float64 `json:"quote"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse forex rates for %s: %w", base, err)
	}
	return raw.Quote, nil
}

func (c *FinnhubClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Finnhub API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd stock-service && go test ./internal/provider/ -run TestFinnhub -v`
Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/provider/finnhub.go stock-service/internal/provider/finnhub_test.go
git commit -m "feat(stock): add Finnhub provider for forex pairs and rates"
```

---

### Task 5: Support IANA timezone strings in exchange hours

**Files:**
- Modify: `stock-service/internal/service/exchange_hours.go`
- Create: `stock-service/internal/service/exchange_hours_test.go`

- [ ] **Step 1: Write the test file**

Create `stock-service/internal/service/exchange_hours_test.go`:

```go
package service

import (
	"testing"
	"time"
)

func TestParseTimezoneLocation_IANAString(t *testing.T) {
	loc, err := parseTimezoneLocation("America/New_York")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify it's a valid location by getting the current time in it
	now := time.Now().In(loc)
	if now.Location().String() != "America/New_York" {
		t.Errorf("expected America/New_York, got %s", now.Location().String())
	}
}

func TestParseTimezoneLocation_OffsetString(t *testing.T) {
	loc, err := parseTimezoneLocation("-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FixedZone offset is -5 * 3600 = -18000
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != -5*3600 {
		t.Errorf("expected offset -18000, got %d", offset)
	}
}

func TestParseTimezoneLocation_PositiveOffset(t *testing.T) {
	loc, err := parseTimezoneLocation("+9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != 9*3600 {
		t.Errorf("expected offset 32400, got %d", offset)
	}
}

func TestParseTimezoneLocation_Empty(t *testing.T) {
	_, err := parseTimezoneLocation("")
	if err == nil {
		t.Fatal("expected error for empty timezone")
	}
}

func TestParseTimezoneLocation_InvalidIANA(t *testing.T) {
	_, err := parseTimezoneLocation("Not/A/Real/Zone")
	if err == nil {
		t.Fatal("expected error for invalid IANA timezone")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd stock-service && go test ./internal/service/ -run TestParseTimezoneLocation -v`
Expected: FAIL — `parseTimezoneLocation` not defined

- [ ] **Step 3: Rewrite exchange_hours.go**

Replace the full contents of `stock-service/internal/service/exchange_hours.go`:

```go
package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

// isWithinTradingHours checks if the current moment falls within the exchange's
// open and close times, accounting for its time zone.
// Supports both IANA timezone strings ("America/New_York") and UTC offset strings ("-5").
func isWithinTradingHours(ex *model.StockExchange) bool {
	loc, err := parseTimezoneLocation(ex.TimeZone)
	if err != nil {
		return false
	}
	now := time.Now().In(loc)

	openH, openM := parseTime(ex.OpenTime)
	closeH, closeM := parseTime(ex.CloseTime)

	nowMinutes := now.Hour()*60 + now.Minute()
	openMinutes := openH*60 + openM
	closeMinutes := closeH*60 + closeM

	return nowMinutes >= openMinutes && nowMinutes < closeMinutes
}

// parseTimezoneLocation parses a timezone string. It first tries IANA format
// (e.g. "America/New_York"), then falls back to numeric UTC offset (e.g. "-5", "+9").
func parseTimezoneLocation(tz string) (*time.Location, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return nil, fmt.Errorf("empty timezone")
	}

	// Try IANA format first (contains a slash like "America/New_York")
	if strings.Contains(tz, "/") {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("invalid IANA timezone %q: %w", tz, err)
		}
		return loc, nil
	}

	// Fall back to numeric offset ("-5", "+9", "0")
	offset, err := strconv.Atoi(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone offset %q: %w", tz, err)
	}
	return time.FixedZone(fmt.Sprintf("UTC%+d", offset), offset*3600), nil
}

// parseTime parses "09:30" or "09:30:00" to (9, 30).
func parseTime(t string) (int, int) {
	parts := strings.Split(t, ":")
	if len(parts) < 2 {
		return 0, 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h, m
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd stock-service && go test ./internal/service/ -run TestParseTimezoneLocation -v`
Expected: all 5 tests PASS

- [ ] **Step 5: Verify build**

Run: `cd stock-service && go build ./cmd`
Expected: clean build

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/exchange_hours.go stock-service/internal/service/exchange_hours_test.go
git commit -m "feat(stock): support IANA timezone strings in exchange hours"
```

---

### Task 6: Rewrite security_sync.go to use new providers

**Files:**
- Modify: `stock-service/internal/service/security_sync.go`

This is the largest task. We modify `SecuritySyncService` to accept the new provider clients and rewrite `syncExchanges`, `syncStocks`, and `seedForexPairs` with API-first + fallback logic.

- [ ] **Step 1: Update the SecuritySyncService struct and constructor**

In `stock-service/internal/service/security_sync.go`, replace the struct and constructor (lines 19-50):

```go
type SecuritySyncService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo ExchangeRepo
	settingRepo  SettingRepo
	avClient     *provider.AlphaVantageClient
	eodhClient   *provider.EODHDClient
	alpacaClient *provider.AlpacaClient
	finnhubClient *provider.FinnhubClient
	listingSvc   *ListingService
	csvPath      string
}

func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
	eodhClient *provider.EODHDClient,
	alpacaClient *provider.AlpacaClient,
	finnhubClient *provider.FinnhubClient,
	listingSvc *ListingService,
	csvPath string,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:     stockRepo,
		futuresRepo:   futuresRepo,
		forexRepo:     forexRepo,
		optionRepo:    optionRepo,
		exchangeRepo:  exchangeRepo,
		settingRepo:   settingRepo,
		avClient:      avClient,
		eodhClient:    eodhClient,
		alpacaClient:  alpacaClient,
		finnhubClient: finnhubClient,
		listingSvc:    listingSvc,
		csvPath:       csvPath,
	}
}
```

- [ ] **Step 2: Rewrite SeedAll to call syncExchanges first**

Replace the `SeedAll` method:

```go
// SeedAll runs the full initial data seed.
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.syncExchanges()
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.seedFutures(futuresSeedPath)
	s.generateAllOptions()
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
}
```

- [ ] **Step 3: Write syncExchanges using EODHD with CSV fallback**

Add a new `syncExchanges` method and a helper `upsertExchangeFromEODHD`. Place these after the `isTestingMode` method:

```go
// syncExchanges fetches exchanges from EODHD API, falling back to CSV.
func (s *SecuritySyncService) syncExchanges() {
	if s.eodhClient != nil {
		exchanges, err := s.eodhClient.FetchExchanges()
		if err != nil {
			log.Printf("WARN: EODHD FetchExchanges failed: %v — falling back to CSV", err)
		} else {
			synced := 0
			for _, ex := range exchanges {
				details, err := s.eodhClient.FetchExchangeDetails(ex.Code)
				if err != nil {
					log.Printf("WARN: EODHD FetchExchangeDetails(%s) failed: %v", ex.Code, err)
					continue
				}
				s.upsertExchangeFromEODHD(ex, details)
				synced++
			}
			if synced > 0 {
				log.Printf("synced %d exchanges from EODHD API", synced)
				return
			}
			log.Println("WARN: no exchanges synced from EODHD — falling back to CSV")
		}
	} else {
		log.Println("WARN: no EODHD API key — falling back to CSV for exchanges")
	}

	// Fallback: load from CSV
	csvExchanges, err := provider.LoadExchangesFromCSVFile(s.csvPath)
	if err != nil {
		log.Printf("WARN: failed to load exchanges from CSV: %v", err)
		return
	}
	for _, ex := range csvExchanges {
		ex := ex
		if err := s.exchangeRepo.UpsertByMICCode(&ex); err != nil {
			log.Printf("WARN: failed to upsert exchange %s: %v", ex.MICCode, err)
		}
	}
	log.Printf("seeded %d exchanges from CSV fallback", len(csvExchanges))
}

// upsertExchangeFromEODHD converts EODHD data into StockExchange records.
// One EODHD entry may have multiple MIC codes (comma-separated), so we create
// one record per MIC code with the same trading hours.
func (s *SecuritySyncService) upsertExchangeFromEODHD(ex provider.EODHDExchange, details *provider.EODHDExchangeDetails) {
	mics := strings.Split(ex.OperatingMIC, ",")
	openTime := strings.TrimSuffix(details.TradingHours.OpenHour, ":00")
	closeTime := strings.TrimSuffix(details.TradingHours.CloseHour, ":00")

	for _, mic := range mics {
		mic = strings.TrimSpace(mic)
		if mic == "" {
			continue
		}
		exchange := &model.StockExchange{
			Name:     ex.Name,
			Acronym:  ex.Code,
			MICCode:  mic,
			Polity:   ex.Country,
			Currency: ex.Currency,
			TimeZone: details.Timezone,
			OpenTime: openTime,
			CloseTime: closeTime,
		}
		if err := s.exchangeRepo.UpsertByMICCode(exchange); err != nil {
			log.Printf("WARN: failed to upsert exchange MIC %s: %v", mic, err)
		}
	}
}
```

- [ ] **Step 4: Rewrite syncStocks to use Alpaca for ticker list**

Replace the existing `syncStocks` method:

```go
// syncStocks fetches the stock ticker list from Alpaca, then fetches details
// from AlphaVantage per ticker. Falls back to hardcoded tickers + seed data.
func (s *SecuritySyncService) syncStocks(ctx context.Context) {
	tickers := s.getStockTickers()

	if s.avClient == nil {
		log.Println("WARN: no AlphaVantage API key — seeding default stocks")
		s.seedDefaultStocks()
		return
	}

	for _, ticker := range tickers {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stockData, err := s.avClient.FetchStockData(ticker)
		if err != nil {
			log.Printf("WARN: failed to fetch stock %s: %v", ticker, err)
			continue
		}

		// Resolve exchange from the overview data
		exchange, err := s.resolveExchange(ticker)
		if err != nil {
			log.Printf("WARN: could not resolve exchange for %s: %v, skipping", ticker, err)
			continue
		}
		stockData.ExchangeID = exchange.ID

		if err := s.stockRepo.UpsertByTicker(stockData); err != nil {
			log.Printf("WARN: failed to upsert stock %s: %v", ticker, err)
		}

		// Rate limit: AlphaVantage free tier allows 5 calls/min
		time.Sleep(12 * time.Second)
	}
	log.Printf("synced %d stock tickers", len(tickers))
}

// getStockTickers tries Alpaca first, falls back to hardcoded list.
func (s *SecuritySyncService) getStockTickers() []string {
	if s.alpacaClient != nil {
		assets, err := s.alpacaClient.FetchAssets()
		if err != nil {
			log.Printf("WARN: Alpaca FetchAssets failed: %v — using default tickers", err)
		} else if len(assets) > 0 {
			tickers := make([]string, len(assets))
			for i, a := range assets {
				tickers[i] = a.Symbol
			}
			log.Printf("fetched %d stock tickers from Alpaca", len(tickers))
			return tickers
		}
	} else {
		log.Println("WARN: no Alpaca API key — using default stock tickers")
	}
	return provider.DefaultStockTickers
}

// resolveExchange tries to find the exchange for a stock by fetching overview
// from AlphaVantage and looking up the exchange acronym in our DB.
func (s *SecuritySyncService) resolveExchange(ticker string) (*model.StockExchange, error) {
	exchangeAcronym := "NYSE" // default
	if s.avClient != nil {
		overview, err := s.avClient.FetchOverview(ticker)
		if err == nil && overview.Exchange != "" {
			exchangeAcronym = overview.Exchange
		}
	}
	return s.exchangeRepo.GetByAcronym(exchangeAcronym)
}
```

- [ ] **Step 5: Rewrite seedForexPairs to use Finnhub with fallback**

Replace the existing `seedForexPairs` method:

```go
// seedForexPairs fetches forex pairs from Finnhub, falling back to hardcoded pairs.
func (s *SecuritySyncService) seedForexPairs() {
	forexExchange, err := s.exchangeRepo.GetByAcronym("FOREX")
	if err != nil {
		log.Println("WARN: no FOREX exchange found — forex pairs will not have exchange association")
		return
	}

	if s.finnhubClient != nil {
		if s.seedForexFromFinnhub(forexExchange.ID) {
			return
		}
	} else {
		log.Println("WARN: no Finnhub API key — seeding hardcoded forex pairs")
	}

	// Fallback: hardcoded pairs
	s.seedHardcodedForexPairs(forexExchange.ID)
}

// seedForexFromFinnhub fetches symbols and rates from Finnhub.
// Returns true if successful, false if fallback should be used.
func (s *SecuritySyncService) seedForexFromFinnhub(forexExchangeID uint64) bool {
	symbols, err := s.finnhubClient.FetchForexSymbols()
	if err != nil {
		log.Printf("WARN: Finnhub FetchForexSymbols failed: %v — falling back to hardcoded", err)
		return false
	}

	// Build rates map: base → (quote → rate)
	rates := make(map[string]map[string]float64)
	for _, base := range SupportedCurrencies {
		r, err := s.finnhubClient.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: Finnhub FetchForexRates(%s) failed: %v", base, err)
			continue
		}
		rates[base] = r
	}

	count := 0
	supported := make(map[string]bool)
	for _, c := range SupportedCurrencies {
		supported[c] = true
	}

	for _, sym := range symbols {
		// displaySymbol is like "EUR/USD"
		parts := strings.SplitN(sym.DisplaySymbol, "/", 2)
		if len(parts) != 2 {
			continue
		}
		base, quote := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if !supported[base] || !supported[quote] {
			continue
		}

		rate := float64(0)
		if baseRates, ok := rates[base]; ok {
			if r, ok := baseRates[quote]; ok {
				rate = r
			}
		}

		liquidity := "medium"
		if isMajorPair(base, quote) {
			liquidity = "high"
		} else if isExoticPair(base, quote) {
			liquidity = "low"
		}

		fp := &model.ForexPair{
			Ticker:        fmt.Sprintf("%s/%s", base, quote),
			Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
			BaseCurrency:  base,
			QuoteCurrency: quote,
			ExchangeRate:  decimal.NewFromFloat(rate),
			Liquidity:     liquidity,
			ExchangeID:    forexExchangeID,
			LastRefresh:   time.Now(),
		}
		if err := s.forexRepo.UpsertByTicker(fp); err != nil {
			log.Printf("WARN: failed to upsert forex pair %s: %v", fp.Ticker, err)
		}
		count++
	}

	if count == 0 {
		log.Println("WARN: no supported forex pairs found from Finnhub — falling back")
		return false
	}
	log.Printf("seeded %d forex pairs from Finnhub", count)
	return true
}

// seedHardcodedForexPairs generates all 56 pairs from supported currencies.
func (s *SecuritySyncService) seedHardcodedForexPairs(forexExchangeID uint64) {
	count := 0
	for _, base := range SupportedCurrencies {
		for _, quote := range SupportedCurrencies {
			if base == quote {
				continue
			}
			ticker := fmt.Sprintf("%s/%s", base, quote)
			name := fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote))

			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}

			fp := &model.ForexPair{
				Ticker:        ticker,
				Name:          name,
				BaseCurrency:  base,
				QuoteCurrency: quote,
				ExchangeRate:  decimal.Zero,
				Liquidity:     liquidity,
				ExchangeID:    forexExchangeID,
				LastRefresh:   time.Now(),
			}

			if err := s.forexRepo.UpsertByTicker(fp); err != nil {
				log.Printf("WARN: failed to seed forex pair %s: %v", ticker, err)
			}
			count++
		}
	}
	log.Printf("seeded %d hardcoded forex pairs", count)
}
```

- [ ] **Step 6: Update RefreshPrices to also refresh forex rates**

Replace the existing `RefreshPrices` method:

```go
// RefreshPrices updates price data for all securities.
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	s.refreshForexRates()
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
	log.Println("price refresh complete")
}

// refreshForexRates updates forex pair exchange rates from Finnhub.
func (s *SecuritySyncService) refreshForexRates() {
	if s.finnhubClient == nil {
		return
	}

	for _, base := range SupportedCurrencies {
		rates, err := s.finnhubClient.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: failed to refresh forex rates for %s: %v", base, err)
			continue
		}
		for quote, rate := range rates {
			ticker := fmt.Sprintf("%s/%s", base, quote)
			existing, err := s.forexRepo.GetByTicker(ticker)
			if err != nil {
				continue // pair doesn't exist in our DB
			}
			existing.ExchangeRate = decimal.NewFromFloat(rate)
			existing.LastRefresh = time.Now()
			if err := s.forexRepo.Update(existing); err != nil {
				log.Printf("WARN: failed to update forex rate %s: %v", ticker, err)
			}
		}
	}
}
```

- [ ] **Step 7: Remove old seedForexPairs and dead code**

Delete the old standalone `seedForexPairs` method (which was the one that used to be called — now replaced). Also remove the old `syncStocks` method that's been replaced. Ensure the `syncStockPrices`, `seedDefaultStocks`, `seedFutures`, `generateAllOptions`, `currencyName`, `isMajorPair`, `isExoticPair` helper methods remain unchanged.

- [ ] **Step 8: Add missing import for `strings`**

The file already imports `fmt`, `log`, `time`, `provider`, `model`, `repository`, `decimal`. Ensure `strings` is in the import block (used by `upsertExchangeFromEODHD` for `strings.Split`).

- [ ] **Step 9: Verify build**

Run: `cd stock-service && go build ./cmd`
Expected: FAIL — `main.go` passes wrong arguments to `NewSecuritySyncService` (will fix in Task 7)

- [ ] **Step 10: Commit sync changes**

```bash
git add stock-service/internal/service/security_sync.go
git commit -m "feat(stock): rewrite security_sync to use EODHD, Alpaca, Finnhub providers"
```

---

### Task 7: Wire new providers in cmd/main.go

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 1: Create provider clients after config load**

In `stock-service/cmd/main.go`, replace the AlphaVantage client block (lines 161-165) with all 4 provider clients:

```go
	// --- External API Clients ---
	// Each is nil when its API key is not set (triggers fallback to static data).

	var avClient *provider.AlphaVantageClient
	if cfg.AlphaVantageAPIKey != "" {
		avClient = provider.NewAlphaVantageClient(cfg.AlphaVantageAPIKey)
	}

	var eodhClient *provider.EODHDClient
	if cfg.EODHDAPIKey != "" {
		eodhClient = provider.NewEODHDClient(cfg.EODHDAPIKey)
	}

	var alpacaClient *provider.AlpacaClient
	if cfg.AlpacaAPIKey != "" && cfg.AlpacaAPISecret != "" {
		alpacaClient = provider.NewAlpacaClient(cfg.AlpacaAPIKey, cfg.AlpacaAPISecret)
	}

	var finnhubClient *provider.FinnhubClient
	if cfg.FinnhubAPIKey != "" {
		finnhubClient = provider.NewFinnhubClient(cfg.FinnhubAPIKey)
	}
```

- [ ] **Step 2: Update NewSecuritySyncService call**

Replace the `NewSecuritySyncService` call (lines 167-170) to pass all new clients:

```go
	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo, avClient,
		eodhClient, alpacaClient, finnhubClient,
		listingSvc, cfg.ExchangeCSVPath,
	)
```

- [ ] **Step 3: Remove the separate exchange seed call**

Delete the `exchangeSvc.SeedExchanges(cfg.ExchangeCSVPath)` call (lines 154-156) since exchange seeding is now handled inside `syncSvc.SeedAll`. The exchange service is still needed for its other methods (`GetExchange`, `ListExchanges`, `SetTestingMode`, etc.) so keep the `exchangeSvc` variable.

```go
	// Remove these lines:
	// if err := exchangeSvc.SeedExchanges(cfg.ExchangeCSVPath); err != nil {
	// 	log.Printf("WARN: failed to seed exchanges from CSV: %v", err)
	// }
```

- [ ] **Step 4: Verify build**

Run: `cd stock-service && go build ./cmd`
Expected: clean build, no errors

- [ ] **Step 5: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock): wire EODHD, Alpaca, Finnhub providers in main.go"
```

---

### Task 8: Verify all fallback paths work

**Files:** (no new files — verification only)

- [ ] **Step 1: Run all provider tests**

Run: `cd stock-service && go test ./internal/provider/ -v`
Expected: all tests PASS (EODHD, Alpaca, Finnhub)

- [ ] **Step 2: Run exchange hours tests**

Run: `cd stock-service && go test ./internal/service/ -run TestParseTimezoneLocation -v`
Expected: all 5 tests PASS

- [ ] **Step 3: Build the full service**

Run: `cd stock-service && go build -o bin/stock-service ./cmd`
Expected: clean build, binary created at `bin/stock-service`

- [ ] **Step 4: Build entire project**

Run: `make build`
Expected: all services build successfully

- [ ] **Step 5: Verify Docker build**

Run: `docker compose build stock-service`
Expected: build succeeds (Dockerfile copies `data/` directory correctly)

- [ ] **Step 6: Commit the Dockerfile fix from earlier (if not yet committed)**

The Dockerfile fix was made earlier in this conversation. Verify it's committed:

Run: `git log --oneline -5`

If the Dockerfile fix isn't committed:
```bash
git add stock-service/Dockerfile
git commit -m "fix(stock): copy data/ directory into Docker image for CSV/JSON fallbacks"
```

- [ ] **Step 7: Final commit with all null-fix changes**

If the `emptyIfNil` changes from earlier aren't committed:
```bash
git add api-gateway/internal/handler/validation.go \
        api-gateway/internal/handler/stock_exchange_handler.go \
        api-gateway/internal/handler/stock_order_handler.go \
        api-gateway/internal/handler/portfolio_handler.go \
        api-gateway/internal/handler/securities_handler.go \
        api-gateway/internal/handler/tax_handler.go
git commit -m "fix(gateway): prevent null JSON arrays in stock API list responses"
```
