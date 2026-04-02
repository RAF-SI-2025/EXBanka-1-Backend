# External API Providers for Stock Service

**Date:** 2026-04-02
**Status:** Approved

## Problem

The stock-service currently relies on a static CSV file for exchange data (which was broken in Docker — Dockerfile didn't copy `data/` directory), AlphaVantage for stock quotes (only if API key is set, otherwise 8 hardcoded stocks), and hardcoded forex pairs. The EODIEX API originally specified in the project spec was shut down in August 2024. The system needs real, comprehensive data from external APIs with working hardcoded fallbacks.

## Decision

Use **Approach 1: Dedicated provider per API**. Each external API gets its own provider file in `stock-service/internal/provider/`. The `security_sync.go` orchestrator calls each provider with a fallback to existing static/hardcoded data on failure.

## API Assignments

| Data | Primary API | Endpoint | Auth | Fallback |
|------|------------|----------|------|----------|
| Exchange list | EODHD | `GET https://eodhd.com/api/exchanges-list/?api_token={KEY}&fmt=json` | `api_token` query param | `data/exchanges.csv` |
| Exchange hours/timezone | EODHD | `GET https://eodhd.com/api/exchange-details/{CODE}?api_token={KEY}&fmt=json` | `api_token` query param | `data/exchanges.csv` |
| Stock ticker list | Alpaca | `GET https://api.alpaca.markets/v2/assets` | `APCA-API-KEY-ID` + `APCA-API-SECRET-KEY` headers | Hardcoded `DefaultStockTickers` (20 tickers) |
| Stock price data | AlphaVantage | `GLOBAL_QUOTE` | `apikey` query param | Hardcoded `seedDefaultStocks()` (8 stocks) |
| Stock company info | AlphaVantage | `OVERVIEW` | `apikey` query param | Hardcoded `seedDefaultStocks()` |
| Stock history | AlphaVantage | `TIME_SERIES_DAILY` | `apikey` query param | No fallback needed |
| Forex pair list | Finnhub | `GET https://finnhub.io/api/v1/forex/symbol?exchange=oanda&token={KEY}` | `token` query param | Hardcoded 56 pairs (8 currencies) |
| Forex rates | Finnhub | `GET https://finnhub.io/api/v1/forex/rates?base=USD&token={KEY}` | `token` query param | Exchange-service rates |
| Futures | N/A | N/A | N/A | `data/futures_seed.json` (per spec recommendation) |
| Options | N/A | N/A | N/A | Generated via Black-Scholes (already implemented) |

## API Response Formats

### EODHD Exchanges List

```json
[
  {
    "Name": "USA Stocks",
    "Code": "US",
    "OperatingMIC": "XNAS,XNYS",
    "Country": "USA",
    "Currency": "USD",
    "CountryISO2": "US",
    "CountryISO3": "USA"
  }
]
```

**Note:** `OperatingMIC` is comma-separated. A single EODHD "exchange" (e.g. `US`) may map to multiple MIC codes (XNAS, XNYS). We need to expand these into separate `StockExchange` records.

### EODHD Exchange Details

```
GET https://eodhd.com/api/exchange-details/{CODE}?api_token={KEY}&fmt=json
```

Returns: timezone (IANA format, e.g. `"America/New_York"`), trading hours with open/close times, `isOpen` boolean, holidays list. Costs 5 API calls per request.

### Alpaca Assets

```
GET https://api.alpaca.markets/v2/assets?status=active&asset_class=us_equity
```

Headers: `APCA-API-KEY-ID: {KEY}`, `APCA-API-SECRET-KEY: {SECRET}`

Returns array of assets with `symbol`, `name`, `exchange`, `status`, `tradable`.

### Finnhub Forex

```
GET https://finnhub.io/api/v1/forex/symbol?exchange=oanda&token={KEY}
```

Returns: `[{"symbol": "OANDA:EUR_USD", "displaySymbol": "EUR/USD", "description": "Euro/US Dollar"}]`

```
GET https://finnhub.io/api/v1/forex/rates?base=USD&token={KEY}
```

Returns: `{"base": "USD", "quote": {"EUR": 0.85, "GBP": 0.73, ...}}`

## New Files

### `provider/eodhd.go`

```go
type EODHDClient struct {
    apiKey     string
    httpClient *http.Client
}

// FetchExchanges returns all exchanges from EODHD API.
func (c *EODHDClient) FetchExchanges() ([]EODHDExchange, error)

// FetchExchangeDetails returns trading hours and timezone for a given exchange code.
func (c *EODHDClient) FetchExchangeDetails(code string) (*EODHDExchangeDetails, error)
```

`FetchExchanges` calls `/api/exchanges-list/`. Each result's `OperatingMIC` field is comma-separated; we split it and create one `StockExchange` per MIC code.

`FetchExchangeDetails` calls `/api/exchange-details/{CODE}` to get timezone (IANA string), trading hours (open/close in exchange local time), and holiday dates.

### `provider/alpaca.go`

```go
type AlpacaClient struct {
    apiKey     string
    apiSecret  string
    httpClient *http.Client
}

// FetchAssets returns all active US equity assets.
func (c *AlpacaClient) FetchAssets() ([]AlpacaAsset, error)
```

Calls `GET /v2/assets?status=active&asset_class=us_equity`. Filters to tradable assets. Returns ticker symbols, names, and exchange identifiers that can be resolved against our exchange table.

### `provider/finnhub.go`

```go
type FinnhubClient struct {
    apiKey     string
    httpClient *http.Client
}

// FetchForexSymbols returns available forex pairs from Finnhub.
func (c *FinnhubClient) FetchForexSymbols() ([]FinnhubForexSymbol, error)

// FetchForexRates returns current exchange rates for a base currency.
func (c *FinnhubClient) FetchForexRates(base string) (map[string]float64, error)
```

`FetchForexSymbols` calls `/api/v1/forex/symbol?exchange=oanda`. We filter to our 8 supported currencies.

`FetchForexRates` calls `/api/v1/forex/rates?base={base}` for each of our 8 base currencies to build the full rate matrix.

## Modified Files

### `config/config.go`

New fields:
```go
EODHDAPIKey    string  // env: EODHD_API_KEY
AlpacaAPIKey   string  // env: ALPACA_API_KEY
AlpacaAPISecret string // env: ALPACA_API_SECRET
FinnhubAPIKey  string  // env: FINNHUB_API_KEY
```

### `security_sync.go`

**`SeedAll()` rewrite:**

1. `syncExchanges()` — EODHD `FetchExchanges()` + `FetchExchangeDetails()` per exchange → upsert. On failure: `LoadExchangesFromCSVFile()`.
2. `syncStocks()` — Alpaca `FetchAssets()` for ticker list → AlphaVantage `FetchStockData()` per ticker → upsert. On failure: use `DefaultStockTickers` then `seedDefaultStocks()`.
3. `seedForexPairs()` — Finnhub `FetchForexSymbols()` + `FetchForexRates()` → upsert. On failure: hardcoded 56 pairs with zero rates.
4. `seedFutures()` — unchanged (JSON seed file).
5. `generateAllOptions()` — unchanged (Black-Scholes generation).

**`RefreshPrices()` changes:**

1. Stock prices: AlphaVantage `FetchQuote()` per stock (unchanged).
2. Forex rates: Finnhub `FetchForexRates()` per base currency (new — replaces static zero rates).
3. Exchange hours: no periodic refresh needed (hours don't change frequently; daily at most).

### `docker-compose.yml`

Add to `stock-service` environment:
```yaml
EODHD_API_KEY: ${EODHD_API_KEY:-}
ALPACA_API_KEY: ${ALPACA_API_KEY:-}
ALPACA_API_SECRET: ${ALPACA_API_SECRET:-}
FINNHUB_API_KEY: ${FINNHUB_API_KEY:-}
```

All default to empty string. When empty, the corresponding provider is nil and fallback is used (same pattern as existing `ALPHAVANTAGE_API_KEY`).

### `Dockerfile` (already fixed)

The `data/` directory is now copied into the Docker image for fallback CSV/JSON files.

## Exchange Timezone Handling

EODHD returns timezone as IANA string (e.g. `"America/New_York"`). The `StockExchange` model `TimeZone` field currently stores UTC offset strings (e.g. `"-5"`). 

**Change:** Store the IANA timezone string instead (e.g. `"America/New_York"`). Update `isWithinTradingHours()` in `exchange_hours.go` to use `time.LoadLocation()` for proper DST handling instead of manual offset parsing. The CSV fallback already uses offset strings, so `isWithinTradingHours()` should handle both formats (try IANA first, fall back to offset parsing).

## Rate Limiting

| API | Rate Limit | Strategy |
|-----|-----------|----------|
| EODHD | Depends on plan; free = 20 calls/day | Only called on startup (exchanges don't change often) |
| Alpaca | 200 calls/min | Single call for all assets — well within limits |
| AlphaVantage | Free: 25 calls/day, 5/min | 12s sleep between calls (already implemented) |
| Finnhub | 30 calls/sec, free: 60 calls/min | Minimal calls (1 for symbols, 8 for rates) |

## Fallback Verification

Each fallback path must be verified to work:
- CSV file is now correctly copied in Dockerfile (already fixed)
- `seedDefaultStocks()` hardcoded data is verified (8 stocks with realistic prices)
- Hardcoded forex pairs generation (56 pairs from 8 currencies) is verified
- `futures_seed.json` is correctly copied in Dockerfile (already fixed)

## Scope Exclusions

- No new API gateway routes needed — existing endpoints serve the data regardless of source.
- No changes to the proto definitions — the gRPC responses remain the same.
- No changes to the order execution, portfolio, OTC, or tax subsystems.
- Futures and options data sources remain unchanged.
