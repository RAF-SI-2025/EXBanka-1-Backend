# Stock Service & InfluxDB Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add InfluxDB time-series storage for intraday stock price data and expose candle chart endpoints.

**Architecture:** Dual-write — PostgreSQL remains source of truth for daily snapshots, InfluxDB handles high-frequency intraday data. Reusable client in contract/influx/ for future service adoption.

**Tech Stack:** Go, InfluxDB 2.x, influxdb-client-go, gRPC, Protocol Buffers

---

### Task 1: Create reusable InfluxDB client package in `contract/influx/`

**Files:**
- Create: `contract/influx/client.go`
- Create: `contract/influx/client_test.go`
- Modify: `contract/go.mod` (add influxdb-client-go dependency)

- [ ] **Step 1: Write the test**

```go
// contract/influx/client_test.go
package influx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNilClientIsNoOp(t *testing.T) {
	var c *Client
	// All methods must be nil-safe — no panics
	assert.NotPanics(t, func() {
		c.WritePoint("test", map[string]string{"k": "v"}, map[string]interface{}{"f": 1.0}, time.Now())
	})
	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestNewClient_InvalidURL_ReturnsNil(t *testing.T) {
	// Empty URL should produce a nil client (graceful degradation)
	c := NewClient("", "token", "org", "bucket")
	assert.Nil(t, c)
}

func TestNewClient_ValidParams_ReturnsNonNil(t *testing.T) {
	// Valid params should produce a non-nil client even without a running InfluxDB
	c := NewClient("http://localhost:8086", "my-token", "exbanka", "stock_prices")
	assert.NotNil(t, c)
	if c != nil {
		c.Close()
	}
}

func TestQuery_NilClient_ReturnsEmptyResult(t *testing.T) {
	var c *Client
	result, err := c.Query(context.Background(), `from(bucket:"test") |> range(start: -1h)`)
	assert.NoError(t, err)
	assert.Nil(t, result)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./influx/ -v -run TestNilClient`
Expected: FAIL — package does not exist

- [ ] **Step 3: Add influxdb-client-go dependency**

Run: `cd contract && go get github.com/influxdata/influxdb-client-go/v2 && go mod tidy`

- [ ] **Step 4: Write implementation**

```go
// contract/influx/client.go
package influx

import (
	"context"
	"log"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

// Client wraps the InfluxDB v2 client with nil-safe methods.
// If the client is nil, all operations are no-ops — this allows services
// to run without InfluxDB (graceful degradation).
type Client struct {
	inner    influxdb2.Client
	writeAPI api.WriteAPIBlocking
	queryAPI api.QueryAPI
	org      string
	bucket   string
}

// NewClient creates a new InfluxDB client. Returns nil if url is empty
// (enabling graceful degradation when InfluxDB is not configured).
func NewClient(url, token, org, bucket string) *Client {
	if url == "" {
		log.Println("WARN: InfluxDB URL not configured — time-series writes disabled")
		return nil
	}

	inner := influxdb2.NewClient(url, token)
	return &Client{
		inner:    inner,
		writeAPI: inner.WriteAPIBlocking(org, bucket),
		queryAPI: inner.QueryAPI(org),
		org:      org,
		bucket:   bucket,
	}
}

// WritePoint writes a single data point to InfluxDB.
// Nil-safe: does nothing if the client is nil.
func (c *Client) WritePoint(measurement string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	if c == nil {
		return
	}

	p := influxdb2.NewPoint(measurement, tags, fields, ts)
	if err := c.writeAPI.WritePoint(context.Background(), p); err != nil {
		log.Printf("WARN: InfluxDB write failed for %s: %v", measurement, err)
	}
}

// WritePointAsync writes a point without blocking. Errors are logged.
// Nil-safe: does nothing if the client is nil.
func (c *Client) WritePointAsync(measurement string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	if c == nil {
		return
	}
	go c.WritePoint(measurement, tags, fields, ts)
}

// Query executes a Flux query and returns the result table.
// Nil-safe: returns nil, nil if the client is nil.
func (c *Client) Query(ctx context.Context, flux string) (*api.QueryTableResult, error) {
	if c == nil {
		return nil, nil
	}
	return c.queryAPI.Query(ctx, flux)
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

// Close releases all resources held by the client.
// Nil-safe: does nothing if the client is nil.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.inner.Close()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd contract && go test ./influx/ -v`
Expected: PASS (4 tests)

- [ ] **Step 6: Commit**

```
feat(contract): add reusable InfluxDB client package with nil-safe graceful degradation
```

---

### Task 2: Add InfluxDB to docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add InfluxDB service and volume**

Add the `influxdb` service in the "Third-party infrastructure" section (after the `redis` service), and add the `influxdb_data` volume.

In `docker-compose.yml`, after the `redis` service block (after line 37), add:

```yaml
  influxdb:
    image: influxdb:2.7-alpine
    environment:
      DOCKER_INFLUXDB_INIT_MODE: setup
      DOCKER_INFLUXDB_INIT_USERNAME: admin
      DOCKER_INFLUXDB_INIT_PASSWORD: adminpassword
      DOCKER_INFLUXDB_INIT_ORG: exbanka
      DOCKER_INFLUXDB_INIT_BUCKET: stock_prices
      DOCKER_INFLUXDB_INIT_ADMIN_TOKEN: exbanka-influx-dev-token
    ports:
      - "8086:8086"
    volumes:
      - influxdb_data:/var/lib/influxdb2
    healthcheck:
      test: ["CMD", "influx", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s
```

- [ ] **Step 2: Add InfluxDB environment variables to stock-service**

In `docker-compose.yml`, in the `stock-service` environment section (around line 478), add:

```yaml
      INFLUX_URL: http://influxdb:8086
      INFLUX_TOKEN: exbanka-influx-dev-token
      INFLUX_ORG: exbanka
      INFLUX_BUCKET: stock_prices
```

- [ ] **Step 3: Add InfluxDB to stock-service depends_on**

In the `stock-service` `depends_on:` block (around line 500), add:

```yaml
      influxdb:
        condition: service_healthy
```

- [ ] **Step 4: Add volume**

In the `volumes:` section at the bottom of `docker-compose.yml`, add:

```yaml
  influxdb_data:
```

- [ ] **Step 5: Verify the compose file parses**

Run: `docker compose config --quiet`
Expected: exits 0 with no errors

- [ ] **Step 6: Commit**

```
infra: add InfluxDB 2.7 to docker-compose for time-series stock data
```

---

### Task 3: Add InfluxDB config to stock-service

**Files:**
- Modify: `stock-service/internal/config/config.go`
- Modify: `stock-service/cmd/main.go`
- Modify: `stock-service/go.mod` (indirect dependency via contract)

- [ ] **Step 1: Add InfluxDB fields to Config struct**

In `stock-service/internal/config/config.go`, add four fields to the `Config` struct after the `FinnhubAPIKey` field (line 33):

```go
	// InfluxDB (time-series)
	InfluxURL    string
	InfluxToken  string
	InfluxOrg    string
	InfluxBucket string
```

- [ ] **Step 2: Load InfluxDB config from environment**

In `stock-service/internal/config/config.go`, in the `Load()` function, add these lines in the returned struct (after `FinnhubAPIKey` on line 63):

```go
		InfluxURL:    getEnv("INFLUX_URL", ""),
		InfluxToken:  getEnv("INFLUX_TOKEN", ""),
		InfluxOrg:    getEnv("INFLUX_ORG", "exbanka"),
		InfluxBucket: getEnv("INFLUX_BUCKET", "stock_prices"),
```

- [ ] **Step 3: Create InfluxDB client in main.go**

In `stock-service/cmd/main.go`, add the import:

```go
	"github.com/exbanka/contract/influx"
```

Then, after the Kafka producer setup (after line 81, after `EnsureTopics`), add:

```go
	// --- InfluxDB ---
	influxClient := influx.NewClient(cfg.InfluxURL, cfg.InfluxToken, cfg.InfluxOrg, cfg.InfluxBucket)
	if influxClient != nil {
		defer influxClient.Close()
		log.Println("InfluxDB client connected")
	}
```

- [ ] **Step 4: Pass influxClient to SecuritySyncService**

In `stock-service/cmd/main.go`, modify the `syncSvc` construction (around line 179) to pass `influxClient`:

Change:
```go
	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo, avClient,
		eodhClient, alpacaClient, finnhubClient,
		listingSvc, cfg.ExchangeCSVPath,
	)
```
To:
```go
	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo, avClient,
		eodhClient, alpacaClient, finnhubClient,
		listingSvc, cfg.ExchangeCSVPath, influxClient,
	)
```

- [ ] **Step 5: Pass influxClient to ListingCronService**

In `stock-service/cmd/main.go`, modify the `listingCron` construction (around line 222):

Change:
```go
	listingCron := service.NewListingCronService(listingRepo, dailyPriceRepo)
```
To:
```go
	listingCron := service.NewListingCronService(listingRepo, dailyPriceRepo, influxClient)
```

- [ ] **Step 6: Run go mod tidy**

Run: `cd stock-service && go mod tidy`

- [ ] **Step 7: Verify build compiles (will fail until Task 4 wires methods)**

Run: `cd stock-service && go build ./cmd/...` (expected to fail — constructor signatures not updated yet)

- [ ] **Step 8: Commit**

```
feat(stock): add InfluxDB config and client wiring to stock-service startup
```

---

### Task 4: Dual-write price data to InfluxDB

**Files:**
- Modify: `stock-service/internal/service/security_sync.go`
- Modify: `stock-service/internal/service/listing_cron.go`
- Create: `stock-service/internal/service/influx_writer.go`
- Create: `stock-service/internal/service/influx_writer_test.go`

- [ ] **Step 1: Write the InfluxDB writer helper**

```go
// stock-service/internal/service/influx_writer.go
package service

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/influx"
)

// writeSecurityPricePoint writes a single security price data point to InfluxDB.
// Non-blocking: if influxClient is nil, this is a no-op.
func writeSecurityPricePoint(
	influxClient *influx.Client,
	listingID uint64,
	securityType string,
	ticker string,
	exchangeAcronym string,
	price, high, low, change decimal.Decimal,
	volume int64,
	ts time.Time,
) {
	if influxClient == nil {
		return
	}

	tags := map[string]string{
		"listing_id":    fmt.Sprintf("%d", listingID),
		"security_type": securityType,
		"ticker":        ticker,
		"exchange":      exchangeAcronym,
	}

	fields := map[string]interface{}{
		"price":  priceToFloat(price),
		"high":   priceToFloat(high),
		"low":    priceToFloat(low),
		"change": priceToFloat(change),
		"volume": volume,
	}

	influxClient.WritePointAsync("security_price", tags, fields, ts)
}

// priceToFloat converts a decimal.Decimal to float64 for InfluxDB field storage.
func priceToFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}
```

- [ ] **Step 2: Write unit test for writeSecurityPricePoint**

```go
// stock-service/internal/service/influx_writer_test.go
package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestWriteSecurityPricePoint_NilClient_NoPanic(t *testing.T) {
	// Must not panic when influxClient is nil
	assert.NotPanics(t, func() {
		writeSecurityPricePoint(
			nil, // nil client
			1, "stock", "AAPL", "NASDAQ",
			decimal.NewFromFloat(165.0),
			decimal.NewFromFloat(167.5),
			decimal.NewFromFloat(163.2),
			decimal.NewFromFloat(-2.3),
			50000,
			time.Now(),
		)
	})
}

func TestPriceToFloat(t *testing.T) {
	tests := []struct {
		input    decimal.Decimal
		expected float64
	}{
		{decimal.NewFromFloat(165.1234), 165.1234},
		{decimal.Zero, 0},
		{decimal.NewFromFloat(-2.5), -2.5},
	}
	for _, tc := range tests {
		got := priceToFloat(tc.input)
		assert.InDelta(t, tc.expected, got, 0.0001)
	}
}
```

- [ ] **Step 3: Add influxClient to SecuritySyncService**

In `stock-service/internal/service/security_sync.go`, add the `influx` import:

```go
	"github.com/exbanka/contract/influx"
```

Add the field to the `SecuritySyncService` struct (after `csvPath string` on line 33):

```go
	influxClient *influx.Client
```

Update the `NewSecuritySyncService` constructor signature (line 35-63) to accept `influxClient *influx.Client` as the last parameter:

```go
func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo *repository.ExchangeRepository,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
	eodhClient *provider.EODHDClient,
	alpacaClient *provider.AlpacaClient,
	finnhubClient *provider.FinnhubClient,
	listingSvc *ListingService,
	csvPath string,
	influxClient *influx.Client,
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
		influxClient:  influxClient,
	}
}
```

- [ ] **Step 4: Write price data to InfluxDB during stock price sync**

In `stock-service/internal/service/security_sync.go`, in the `syncStockPrices` method (around line 270), after the successful `UpsertByTicker` call (after line 302), add the InfluxDB write.

After:
```go
		if err := s.stockRepo.UpsertByTicker(&stock); err != nil {
			log.Printf("WARN: failed to update stock %s price: %v", stock.Ticker, err)
		}
```
Add:
```go
		// Dual-write to InfluxDB for intraday candle data
		if listing, err := s.listingSvc.GetListingForSecurity(stock.ID, "stock"); err == nil {
			exchangeAcronym := ""
			if ex, err := s.exchangeRepo.GetByID(stock.ExchangeID); err == nil {
				exchangeAcronym = ex.Acronym
			}
			writeSecurityPricePoint(
				s.influxClient, listing.ID, "stock", stock.Ticker, exchangeAcronym,
				stock.Price, stock.High, stock.Low, stock.Change, stock.Volume,
				time.Now(),
			)
		}
```

- [ ] **Step 5: Write price data to InfluxDB during forex rate refresh**

In `stock-service/internal/service/security_sync.go`, in the `refreshForexRates` method (around line 492), after the successful `Update` call (after line 510), add:

After:
```go
			if err := s.forexRepo.Update(existing); err != nil {
				log.Printf("WARN: failed to update forex rate %s: %v", ticker, err)
			}
```
Add:
```go
			// Dual-write to InfluxDB
			if listing, err := s.listingSvc.GetListingForSecurity(existing.ID, "forex"); err == nil {
				writeSecurityPricePoint(
					s.influxClient, listing.ID, "forex", ticker, "FOREX",
					existing.ExchangeRate, existing.High, existing.Low, existing.Change, existing.Volume,
					time.Now(),
				)
			}
```

- [ ] **Step 6: Add influxClient to ListingCronService for snapshot writes**

In `stock-service/internal/service/listing_cron.go`, add the `influx` import:

```go
	"github.com/exbanka/contract/influx"
```

Add the field to the struct (after `dailyRepo DailyPriceRepo` on line 14):

```go
	influxClient *influx.Client
```

Update the constructor (line 16):

```go
func NewListingCronService(listingRepo ListingRepo, dailyRepo DailyPriceRepo, influxClient *influx.Client) *ListingCronService {
	return &ListingCronService{
		listingRepo:  listingRepo,
		dailyRepo:    dailyRepo,
		influxClient: influxClient,
	}
}
```

- [ ] **Step 7: Write to InfluxDB in SnapshotDailyPrices**

In `stock-service/internal/service/listing_cron.go`, in the `SnapshotDailyPrices` method, after the successful `UpsertByListingAndDate` call (after line 44), add:

After:
```go
		if err := c.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: listing cron: failed to snapshot listing %d: %v", l.ID, err)
			continue
		}
```
Add:
```go
		// Dual-write to InfluxDB
		writeSecurityPricePoint(
			c.influxClient, l.ID, l.SecurityType, "", l.Exchange.Acronym,
			l.Price, l.High, l.Low, l.Change, l.Volume,
			today,
		)
```

- [ ] **Step 8: Verify build compiles**

Run: `cd stock-service && go build ./cmd/...`
Expected: SUCCESS

- [ ] **Step 9: Run unit tests**

Run: `cd stock-service && go test ./internal/service/ -v -run TestWriteSecurityPricePoint`
Run: `cd stock-service && go test ./internal/service/ -v -run TestPriceToFloat`
Expected: PASS

- [ ] **Step 10: Commit**

```
feat(stock): dual-write price data to InfluxDB during sync and cron snapshot
```

---

### Task 5: Add candle query service

**Files:**
- Create: `stock-service/internal/service/candle_service.go`
- Create: `stock-service/internal/service/candle_service_test.go`

- [ ] **Step 1: Write the candle service tests**

```go
// stock-service/internal/service/candle_service_test.go
package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateInterval_ValidIntervals(t *testing.T) {
	valid := []string{"1m", "5m", "15m", "1h", "4h", "1d"}
	for _, interval := range valid {
		_, err := parseInterval(interval)
		assert.NoError(t, err, "interval %s should be valid", interval)
	}
}

func TestValidateInterval_InvalidIntervals(t *testing.T) {
	invalid := []string{"", "2m", "3h", "1w", "invalid", "10m"}
	for _, interval := range invalid {
		_, err := parseInterval(interval)
		assert.Error(t, err, "interval %s should be invalid", interval)
	}
}

func TestBuildCandleFluxQuery(t *testing.T) {
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)

	query := buildCandleFluxQuery("stock_prices", uint64(42), "1h", from, to)
	assert.Contains(t, query, `r["listing_id"] == "42"`)
	assert.Contains(t, query, "aggregateWindow(every: 1h")
	assert.Contains(t, query, `bucket: "stock_prices"`)
}

func TestCandlePoint_ZeroValues(t *testing.T) {
	cp := CandlePoint{}
	assert.True(t, cp.Timestamp.IsZero())
	assert.Equal(t, float64(0), cp.Open)
	assert.Equal(t, float64(0), cp.High)
	assert.Equal(t, float64(0), cp.Low)
	assert.Equal(t, float64(0), cp.Close)
	assert.Equal(t, int64(0), cp.Volume)
}
```

- [ ] **Step 2: Write the candle service implementation**

```go
// stock-service/internal/service/candle_service.go
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/exbanka/contract/influx"
)

// CandlePoint represents a single OHLCV candle bar.
type CandlePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
}

// CandleService queries InfluxDB for aggregated candle data.
type CandleService struct {
	influxClient *influx.Client
}

// NewCandleService creates a new CandleService.
func NewCandleService(influxClient *influx.Client) *CandleService {
	return &CandleService{influxClient: influxClient}
}

// GetCandles retrieves aggregated candle data for a listing.
// Returns empty slice (not error) if InfluxDB is not configured.
func (s *CandleService) GetCandles(ctx context.Context, listingID uint64, interval string, from, to time.Time) ([]CandlePoint, error) {
	if s.influxClient == nil {
		return []CandlePoint{}, nil
	}

	dur, err := parseInterval(interval)
	if err != nil {
		return nil, err
	}
	_ = dur // validated; we use the string form in Flux

	bucket := s.influxClient.Bucket()
	query := buildCandleFluxQuery(bucket, listingID, interval, from, to)

	result, err := s.influxClient.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("influx query failed: %w", err)
	}
	if result == nil {
		return []CandlePoint{}, nil
	}

	return parseCandleResult(result)
}

// parseInterval validates and converts interval strings to time.Duration.
var validIntervals = map[string]time.Duration{
	"1m":  1 * time.Minute,
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"1h":  1 * time.Hour,
	"4h":  4 * time.Hour,
	"1d":  24 * time.Hour,
}

func parseInterval(interval string) (time.Duration, error) {
	dur, ok := validIntervals[interval]
	if !ok {
		return 0, fmt.Errorf("invalid interval %q: must be one of 1m, 5m, 15m, 1h, 4h, 1d", interval)
	}
	return dur, nil
}

// buildCandleFluxQuery constructs a Flux query that aggregates security_price
// data into OHLCV candles. InfluxDB's aggregateWindow computes the aggregation.
//
// The query performs:
//  1. Filter by bucket, time range, measurement, and listing_id tag
//  2. Pivot fields (price, high, low, volume) into columns
//  3. Use aggregateWindow to bucket into the requested interval
//
// Since InfluxDB stores each write as a snapshot, we use:
//   - first(price) as Open
//   - max(high)    as High
//   - min(low)     as Low
//   - last(price)  as Close
//   - sum(volume)  as Volume
func buildCandleFluxQuery(bucket string, listingID uint64, interval string, from, to time.Time) string {
	return fmt.Sprintf(`
open = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "price")
  |> aggregateWindow(every: %s, fn: first, createEmpty: false)
  |> set(key: "_field", value: "open")

high = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "high")
  |> aggregateWindow(every: %s, fn: max, createEmpty: false)
  |> set(key: "_field", value: "high")

low = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "low")
  |> aggregateWindow(every: %s, fn: min, createEmpty: false)
  |> set(key: "_field", value: "low")

close = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "price")
  |> aggregateWindow(every: %s, fn: last, createEmpty: false)
  |> set(key: "_field", value: "close")

vol = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "volume")
  |> aggregateWindow(every: %s, fn: sum, createEmpty: false)
  |> set(key: "_field", value: "volume")

union(tables: [open, high, low, close, vol])
  |> pivot(rowKey: ["_time"], columnKey: ["_field"], valueColumn: "_value")
  |> sort(columns: ["_time"])
`,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
	)
}

// parseCandleResult reads the InfluxDB query result and builds CandlePoint slices.
func parseCandleResult(result interface{ Next() bool; Record() interface{ Time() time.Time; ValueByKey(string) interface{} }; Err() error }) ([]CandlePoint, error) {
	// This function is typed against the api.QueryTableResult interface for testability.
	// In practice, it receives *api.QueryTableResult from InfluxDB.
	var candles []CandlePoint

	for result.Next() {
		record := result.Record()
		cp := CandlePoint{
			Timestamp: record.Time(),
		}

		if v := record.ValueByKey("open"); v != nil {
			cp.Open = toFloat64(v)
		}
		if v := record.ValueByKey("high"); v != nil {
			cp.High = toFloat64(v)
		}
		if v := record.ValueByKey("low"); v != nil {
			cp.Low = toFloat64(v)
		}
		if v := record.ValueByKey("close"); v != nil {
			cp.Close = toFloat64(v)
		}
		if v := record.ValueByKey("volume"); v != nil {
			cp.Volume = toInt64(v)
		}

		candles = append(candles, cp)
	}

	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("influx result error: %w", err)
	}

	return candles, nil
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0
	}
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}
```

Note: The `parseCandleResult` function above uses a typed interface parameter for documentation purposes. The actual implementation needs to use the concrete `*api.QueryTableResult` type. Replace the function signature with:

```go
func parseCandleResult(result *api.QueryTableResult) ([]CandlePoint, error) {
```

And add the import:
```go
	"github.com/influxdata/influxdb-client-go/v2/api"
```

The full import block for `candle_service.go`:
```go
import (
	"context"
	"fmt"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api"

	"github.com/exbanka/contract/influx"
)
```

- [ ] **Step 3: Run unit tests**

Run: `cd stock-service && go test ./internal/service/ -v -run "TestValidateInterval|TestBuildCandleFluxQuery|TestCandlePoint"`
Expected: PASS

- [ ] **Step 4: Commit**

```
feat(stock): add CandleService for InfluxDB OHLCV candle queries
```

---

### Task 6: Add gRPC service for candles

**Files:**
- Modify: `contract/proto/stock/stock.proto`
- Modify: `stock-service/internal/handler/security_handler.go`
- Modify: `stock-service/cmd/main.go`
- Modify: `api-gateway/internal/handler/securities_handler.go`
- Modify: `api-gateway/internal/router/router.go`

- [ ] **Step 1: Add candle messages and RPC to stock.proto**

In `contract/proto/stock/stock.proto`, add the `GetCandles` RPC to the `SecurityGRPCService` (after the `GetOption` RPC on line 122):

```protobuf
  // Candles (InfluxDB time-series)
  rpc GetCandles(GetCandlesRequest) returns (GetCandlesResponse);
```

Add the message definitions after the `GetOptionRequest` message (after line 314):

```protobuf
// ==================== Candles ====================

message GetCandlesRequest {
  uint64 listing_id = 1;
  string interval = 2;    // 1m, 5m, 15m, 1h, 4h, 1d
  string from = 3;        // RFC3339 timestamp
  string to = 4;          // RFC3339 timestamp
}

message CandlePoint {
  string timestamp = 1;   // RFC3339
  string open = 2;
  string high = 3;
  string low = 4;
  string close = 5;
  int64 volume = 6;
}

message GetCandlesResponse {
  repeated CandlePoint candles = 1;
  int64 count = 2;
}
```

- [ ] **Step 2: Regenerate protobuf Go files**

Run: `make proto`

- [ ] **Step 3: Add CandleService to security handler**

In `stock-service/internal/handler/security_handler.go`, add the `candleSvc` field and update the constructor.

Change the struct (lines 18-22):
```go
type SecurityHandler struct {
	pb.UnimplementedSecurityGRPCServiceServer
	secSvc     *service.SecurityService
	listingSvc *service.ListingService
	candleSvc  *service.CandleService
}

func NewSecurityHandler(secSvc *service.SecurityService, listingSvc *service.ListingService, candleSvc *service.CandleService) *SecurityHandler {
	return &SecurityHandler{secSvc: secSvc, listingSvc: listingSvc, candleSvc: candleSvc}
}
```

- [ ] **Step 4: Implement GetCandles handler**

In `stock-service/internal/handler/security_handler.go`, add the handler method (after the `GetForexPairHistory` method):

```go
func (h *SecurityHandler) GetCandles(ctx context.Context, req *pb.GetCandlesRequest) (*pb.GetCandlesResponse, error) {
	if req.ListingId == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id is required")
	}
	if req.Interval == "" {
		return nil, status.Error(codes.InvalidArgument, "interval is required")
	}

	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid from timestamp: use RFC3339 format")
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid to timestamp: use RFC3339 format")
	}

	candles, err := h.candleSvc.GetCandles(ctx, req.ListingId, req.Interval, from, to)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	pbCandles := make([]*pb.CandlePoint, len(candles))
	for i, cp := range candles {
		pbCandles[i] = &pb.CandlePoint{
			Timestamp: cp.Timestamp.Format(time.RFC3339),
			Open:      fmt.Sprintf("%.4f", cp.Open),
			High:      fmt.Sprintf("%.4f", cp.High),
			Low:       fmt.Sprintf("%.4f", cp.Low),
			Close:     fmt.Sprintf("%.4f", cp.Close),
			Volume:    cp.Volume,
		}
	}

	return &pb.GetCandlesResponse{
		Candles: pbCandles,
		Count:   int64(len(pbCandles)),
	}, nil
}
```

Add `"fmt"` to the import block if not already present.

- [ ] **Step 5: Wire CandleService in main.go**

In `stock-service/cmd/main.go`, after creating the `listingSvc` (around line 154), add:

```go
	candleSvc := service.NewCandleService(influxClient)
```

Update the security handler construction (around line 251):

Change:
```go
	securityHandler := handler.NewSecurityHandler(secSvc, listingSvc)
```
To:
```go
	securityHandler := handler.NewSecurityHandler(secSvc, listingSvc, candleSvc)
```

- [ ] **Step 6: Add gateway candle endpoint**

In `api-gateway/internal/handler/securities_handler.go`, add the `GetCandles` method:

```go
func (h *SecuritiesHandler) GetCandles(c *gin.Context) {
	listingIDStr := c.Query("listing_id")
	if listingIDStr == "" {
		apiError(c, 400, ErrValidation, "listing_id query parameter is required")
		return
	}
	listingID, err := strconv.ParseUint(listingIDStr, 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid listing_id")
		return
	}

	interval := c.DefaultQuery("interval", "1h")
	if _, err := oneOf("interval", interval, "1m", "5m", "15m", "1h", "4h", "1d"); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	fromStr := c.Query("from")
	if fromStr == "" {
		apiError(c, 400, ErrValidation, "from query parameter is required (RFC3339)")
		return
	}
	toStr := c.Query("to")
	if toStr == "" {
		apiError(c, 400, ErrValidation, "to query parameter is required (RFC3339)")
		return
	}

	resp, err := h.client.GetCandles(c.Request.Context(), &stockpb.GetCandlesRequest{
		ListingId: listingID,
		Interval:  interval,
		From:      fromStr,
		To:        toStr,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"candles": emptyIfNil(resp.Candles), "count": resp.Count})
}
```

- [ ] **Step 7: Add route in router.go**

In `api-gateway/internal/router/router.go`, in the securities group (after line 179, after the options routes), add:

```go
		// Candles (InfluxDB)
		securities.GET("/candles", securitiesHandler.GetCandles)
```

- [ ] **Step 8: Verify everything builds**

Run: `cd stock-service && go build ./cmd/...`
Run: `cd api-gateway && go build ./cmd/...`
Expected: SUCCESS for both

- [ ] **Step 9: Commit**

```
feat(stock): add GetCandles gRPC RPC and REST endpoint for InfluxDB candle data
```

---

### Task 7: Add tests for candle gRPC handler

**Files:**
- Create: `stock-service/internal/handler/candle_handler_test.go`

- [ ] **Step 1: Write handler unit tests**

```go
// stock-service/internal/handler/candle_handler_test.go
package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/exbanka/contract/stockpb"
)

func TestGetCandles_MissingListingID(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		Interval: "1h",
		From:     "2026-04-01T00:00:00Z",
		To:       "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing_id is required")
}

func TestGetCandles_MissingInterval(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		From:      "2026-04-01T00:00:00Z",
		To:        "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval is required")
}

func TestGetCandles_InvalidFromTimestamp(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "not-a-timestamp",
		To:        "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid from timestamp")
}

func TestGetCandles_InvalidToTimestamp(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "2026-04-01T00:00:00Z",
		To:        "not-a-timestamp",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid to timestamp")
}

func TestGetCandles_NilCandleService_ReturnsEmpty(t *testing.T) {
	// CandleService with nil influx client returns empty candles
	svc := &service.CandleService{} // zero value — influxClient is nil
	// This tests graceful degradation
	h := NewSecurityHandler(nil, nil, service.NewCandleService(nil))
	resp, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "2026-04-01T00:00:00Z",
		To:        "2026-04-04T00:00:00Z",
	})
	_ = svc
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Candles)
	assert.Equal(t, int64(0), resp.Count)
}
```

Add the import for the service package:
```go
import (
	"github.com/exbanka/stock-service/internal/service"
)
```

- [ ] **Step 2: Run tests**

Run: `cd stock-service && go test ./internal/handler/ -v -run TestGetCandles`
Expected: PASS

- [ ] **Step 3: Commit**

```
test(stock): add unit tests for GetCandles gRPC handler validation
```

---

### Task 8: Update CLAUDE.md and Specification.md

**Files:**
- Modify: `CLAUDE.md`
- Modify: `Specification.md`

- [ ] **Step 1: Add InfluxDB environment variables to CLAUDE.md**

In the environment table in `CLAUDE.md`, add:

| Variable | Default | Notes |
|---|---|---|
| `INFLUX_URL` | *(empty)* | InfluxDB URL; empty = disabled (graceful degradation) |
| `INFLUX_TOKEN` | *(empty)* | InfluxDB admin token |
| `INFLUX_ORG` | exbanka | InfluxDB organization |
| `INFLUX_BUCKET` | stock_prices | InfluxDB bucket for time-series data |

- [ ] **Step 2: Add candle endpoint to Specification.md**

Add the new REST endpoint under the securities section:
- `GET /api/securities/candles?listing_id=X&interval=1h&from=...&to=...`
- Query parameters: `listing_id` (required), `interval` (1m/5m/15m/1h/4h/1d), `from` (RFC3339), `to` (RFC3339)
- Protected by `AnyAuthMiddleware`
- Returns: `{ "candles": [...], "count": N }`

- [ ] **Step 3: Add contract/influx to architecture section**

Note in the architecture section that `contract/influx/` provides a reusable InfluxDB client wrapper used by stock-service.

- [ ] **Step 4: Commit**

```
docs: update CLAUDE.md and Specification.md with InfluxDB and candle endpoint
```

---

### Task 9: Verify end-to-end in testing mode

**Files:**
- No new files; verification task

- [ ] **Step 1: Start services**

Run: `make docker-up`
Wait for all services to report healthy.

- [ ] **Step 2: Verify InfluxDB is running**

Run: `curl -s http://localhost:8086/health | jq .`
Expected: `{ "status": "pass" }`

- [ ] **Step 3: Enable testing mode**

```bash
# Login as admin
TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@admin.com","password":"AdminAdmin2026!."}' | jq -r '.access_token')

# Enable testing mode
curl -s -X POST http://localhost:8080/api/stock-exchanges/testing-mode \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

Expected: `{"testing_mode": true}`

- [ ] **Step 4: Verify listings exist**

```bash
curl -s http://localhost:8080/api/securities/stocks \
  -H "Authorization: Bearer $TOKEN" | jq '.total_count'
```

Expected: > 0

- [ ] **Step 5: Query candles endpoint (expect empty — no intraday data yet in testing mode)**

```bash
curl -s "http://localhost:8080/api/securities/candles?listing_id=1&interval=1h&from=2026-04-01T00:00:00Z&to=2026-04-05T00:00:00Z" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: `{ "candles": [], "count": 0 }` (no intraday data written yet since testing mode skips price refresh)

- [ ] **Step 6: Write a test point manually via InfluxDB API**

```bash
curl -s -X POST 'http://localhost:8086/api/v2/write?org=exbanka&bucket=stock_prices&precision=s' \
  -H 'Authorization: Token exbanka-influx-dev-token' \
  -H 'Content-Type: text/plain' \
  --data-binary 'security_price,listing_id=1,security_type=stock,ticker=AAPL,exchange=NASDAQ price=165.0,high=167.5,low=163.2,change=-2.3,volume=50000i 1743465600'
```

- [ ] **Step 7: Query candles again and verify data**

```bash
curl -s "http://localhost:8080/api/securities/candles?listing_id=1&interval=1d&from=2025-03-31T00:00:00Z&to=2025-04-02T00:00:00Z" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: 1 candle point returned

- [ ] **Step 8: Tear down**

Run: `make docker-down`

- [ ] **Step 9: Commit (no code changes — verification only)**

No commit needed. Add a note to the PR description confirming end-to-end verification passed.

---

## Summary of all changes

| # | Scope | Files | Description |
|---|-------|-------|-------------|
| 1 | `contract/influx/` | 2 new | Reusable InfluxDB client with nil-safe graceful degradation |
| 2 | `docker-compose.yml` | 1 modified | Add InfluxDB 2.7 service, volume, stock-service env vars |
| 3 | `stock-service/config` + `cmd` | 2 modified | InfluxDB config fields, client creation, wiring |
| 4 | `stock-service/service` | 3 modified, 2 new | Dual-write helper, sync + cron InfluxDB writes |
| 5 | `stock-service/service` | 2 new | CandleService with Flux query builder |
| 6 | Proto + handlers + gateway | 5 modified | GetCandles RPC, handler, REST endpoint, route |
| 7 | `stock-service/handler` | 1 new | Handler unit tests for GetCandles |
| 8 | Docs | 2 modified | CLAUDE.md + Specification.md updates |
| 9 | Verification | 0 | End-to-end manual verification |

**Total: 7 new files, 10 modified files, 9 commits**
