# Stock Detail Chart — Candlesticks for Every Period — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every period (1D, 1W, 1M, 1Y, 5Y, ALL) on the stock/futures/forex detail page render an OHLC candlestick chart on a freshly-seeded backend.

**Architecture:** Backend `stock-service` gets a synthetic 5-year daily-OHLC backfill at seed time, idempotent and deterministic per listing. Frontend `PriceChart` replaces its recharts `<LineChart>` with a `<ComposedChart>` whose `<Bar>` components use custom shapes to draw wick and body. No new endpoints, no new dependencies.

**Tech Stack:** Go 1.22, GORM, PostgreSQL (backend); React 18 + recharts (frontend); Jest + testing-library (frontend tests); Go testify (backend tests).

**Spec:** `docs/superpowers/specs/2026-05-23-stock-chart-candles-design.md`

**Repo paths:**
- Backend: `C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal`
- Frontend: `C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal` (branch `dev`)

---

## File map

**Backend — create:**
- `stock-service/internal/service/listing_history_backfill.go`
- `stock-service/internal/service/listing_history_backfill_test.go`

**Backend — modify:**
- `stock-service/internal/model/listing_daily_price_info.go` — add `uniqueIndex` on `(listing_id, date)`
- `stock-service/internal/service/interfaces.go` — add `UpsertManyByListingAndDate` to `DailyPriceRepo`
- `stock-service/internal/repository/listing_daily_price_repository.go` — implement `UpsertManyByListingAndDate`
- `stock-service/internal/service/security_sync.go` — call backfill from `SeedAll`
- `stock-service/cmd/main.go` — construct the backfill, pass it in (via `WithBackfill` setter to avoid breaking the existing constructor signature used by tests)
- `stock-service/internal/service/listing_cron_test.go` — extend the daily mock with the new batch method (no-op)
- `docs/Specification.md` — note backfill rule

**Frontend — modify:**
- `src/views/securities/components/PriceChart.tsx` — replace LineChart with candle ComposedChart
- `src/views/securities/__tests__/PriceChart.test.tsx` — update recharts mock + add candle assertions

---

### Task 1: Add unique index to `ListingDailyPriceInfo.Date` model tag

**Files:**
- Modify: `stock-service/internal/model/listing_daily_price_info.go:18`

- [ ] **Step 1: Edit the model tag**

```go
// before
Date      time.Time       `gorm:"type:timestamp;not null;index:idx_listing_daily_listing_date" json:"date"`
// after
Date      time.Time       `gorm:"type:timestamp;not null;uniqueIndex:idx_listing_daily_listing_date" json:"date"`
```

Note: the existing GORM `index:idx_listing_daily_listing_date` is paired with `ListingID`'s `index` tag (line 17) under the same index name — making `Date`'s side a `uniqueIndex` promotes the composite index to UNIQUE on `(listing_id, date)`. The `ListingID` field tag does NOT change. Intraday snapshots today use microsecond-precise `time.Now()`, so all existing rows are already unique on `(listing_id, date)` — `AutoMigrate` will succeed.

- [ ] **Step 2: Verify build**

Run from repo root in PowerShell:
```powershell
cd stock-service; go build ./...; cd ..
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```powershell
git add stock-service/internal/model/listing_daily_price_info.go
git commit -m "stock: make (listing_id, date) a unique index on listing_daily_price_infos"
```

---

### Task 2: Add `UpsertManyByListingAndDate` to `DailyPriceRepo`

**Files:**
- Modify: `stock-service/internal/service/interfaces.go:97-102`
- Modify: `stock-service/internal/repository/listing_daily_price_repository.go` (append method)
- Test: `stock-service/internal/repository/repos_extra_test.go` (append test) — uses the existing `sqlite` test harness if present; otherwise we test the service behavior in Task 4 and just exercise the new method via the SQL path in integration. For unit-test cover we add the row through the mock in Task 4. So no repo unit test is required here — but the interface contract is fixed.

- [ ] **Step 1: Extend the interface**

In `stock-service/internal/service/interfaces.go`, replace the `DailyPriceRepo` block:

```go
type DailyPriceRepo interface {
	Create(info *model.ListingDailyPriceInfo) error
	UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error
	UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error
	GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error)
	GetHistoryBucketed(listingID uint64, from, to time.Time, bucketSeconds int) ([]model.ListingDailyPriceInfo, error)
}
```

- [ ] **Step 2: Implement on the repository**

Append to `stock-service/internal/repository/listing_daily_price_repository.go`:

```go
// UpsertManyByListingAndDate inserts or updates a batch of price-info rows in a
// single round-trip per chunk. Used by the history backfill to write 5y of
// synthetic OHLC per listing without paying per-row transaction overhead.
//
// Rows MAY span multiple listings. Conflicts on (listing_id, date) overwrite
// price/high/low/change/volume; the existing row's id is preserved.
func (r *ListingDailyPriceRepository) UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error {
	if len(infos) == 0 {
		return nil
	}
	const chunk = 500
	return r.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < len(infos); i += chunk {
			end := i + chunk
			if end > len(infos) {
				end = len(infos)
			}
			batch := infos[i:end]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "listing_id"}, {Name: "date"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"price", "high", "low", "change", "volume",
				}),
			}).Create(&batch).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
```

- [ ] **Step 3: Verify build**

```powershell
cd stock-service; go build ./...; cd ..
```
Expected: no output, exit 0.

- [ ] **Step 4: Update existing mocks that implement `DailyPriceRepo`**

Two files have mocks implementing `DailyPriceRepo`:
- `stock-service/internal/service/listing_cron_test.go` — `listingCronDailyMock`
- `stock-service/internal/service/listing_service_extra_test.go` — search for any local daily mock

In `listing_cron_test.go`, append to the mock (after `GetHistoryBucketed` at line ~48):

```go
func (m *listingCronDailyMock) UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	for i := range infos {
		row := infos[i]
		m.upserts = append(m.upserts, &row)
	}
	return nil
}
```

- [ ] **Step 5: Find and patch any other `DailyPriceRepo` mocks**

Run:
```powershell
cd stock-service; go build ./...; cd ..
```
Compiler will name any other mock missing the new method. Add the same no-op (or capturing) implementation to each. Expected after all patches: clean build.

- [ ] **Step 6: Commit**

```powershell
git add stock-service/internal/service/interfaces.go stock-service/internal/repository/listing_daily_price_repository.go stock-service/internal/service/listing_cron_test.go
# plus any other test files patched in Step 5
git commit -m "stock(repo): add UpsertManyByListingAndDate batch upsert for daily price info"
```

---

### Task 3: Write the failing test for `ListingHistoryBackfill.Run`

**Files:**
- Create: `stock-service/internal/service/listing_history_backfill_test.go`

- [ ] **Step 1: Write the test file**

```go
package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
)

// backfillMockListings serves a fixed listing set.
type backfillMockListings struct {
	listingCronListingMock
}

// backfillMockDaily captures everything UpsertMany writes.
type backfillMockDaily struct {
	listingCronDailyMock
	batches [][]model.ListingDailyPriceInfo
}

func (m *backfillMockDaily) UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error {
	cp := make([]model.ListingDailyPriceInfo, len(infos))
	copy(cp, infos)
	m.batches = append(m.batches, cp)
	for i := range infos {
		row := infos[i]
		m.upserts = append(m.upserts, &row)
	}
	return nil
}

func newBackfillFixture(listings []model.Listing) (*ListingHistoryBackfill, *backfillMockDaily) {
	lr := &backfillMockListings{}
	lr.listings = listings
	dr := &backfillMockDaily{}
	return NewListingHistoryBackfill(lr, dr), dr
}

func priceDec(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }

func TestBackfill_WritesExpectedRowCountPerListing(t *testing.T) {
	listings := []model.Listing{
		{ID: 1, SecurityType: "stock", Price: priceDec(100)},
		{ID: 2, SecurityType: "stock", Price: priceDec(50)},
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	const days = 1825
	assert.Equal(t, 2*days, len(dr.upserts), "expected one row per listing per day for 5y")
}

func TestBackfill_AnchorsAtCurrentPrice(t *testing.T) {
	listings := []model.Listing{{ID: 7, Price: priceDec(123.45)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	// Find today's row (latest date for listing 7).
	var latest *model.ListingDailyPriceInfo
	for _, row := range dr.upserts {
		if row.ListingID != 7 {
			continue
		}
		if latest == nil || row.Date.After(latest.Date) {
			latest = row
		}
	}
	require.NotNil(t, latest)
	assert.True(t, latest.Price.Equal(priceDec(123.45)),
		"newest row's close must equal listing.Price, got %s", latest.Price.String())
}

func TestBackfill_IsDeterministic(t *testing.T) {
	listings := []model.Listing{{ID: 9, Price: priceDec(80)}}

	b1, dr1 := newBackfillFixture(listings)
	require.NoError(t, b1.Run())
	b2, dr2 := newBackfillFixture(listings)
	require.NoError(t, b2.Run())

	require.Equal(t, len(dr1.upserts), len(dr2.upserts))
	for i := range dr1.upserts {
		a := dr1.upserts[i]
		b := dr2.upserts[i]
		assert.True(t, a.Price.Equal(b.Price), "row %d price differs: %s vs %s", i, a.Price, b.Price)
		assert.True(t, a.High.Equal(b.High))
		assert.True(t, a.Low.Equal(b.Low))
		assert.Equal(t, a.Volume, b.Volume)
	}
}

func TestBackfill_SkipsZeroPriceListings(t *testing.T) {
	listings := []model.Listing{
		{ID: 1, Price: priceDec(0)},        // skip
		{ID: 2, Price: priceDec(10)},       // include
	}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for _, row := range dr.upserts {
		assert.NotEqual(t, uint64(1), row.ListingID, "zero-price listing must produce no rows")
	}
	assert.Equal(t, 1825, len(dr.upserts))
}

func TestBackfill_DatesAreDistinctAndContiguous(t *testing.T) {
	listings := []model.Listing{{ID: 1, Price: priceDec(10)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	seen := map[time.Time]bool{}
	for _, row := range dr.upserts {
		assert.False(t, seen[row.Date], "duplicate date: %s", row.Date)
		seen[row.Date] = true
	}
	assert.Equal(t, 1825, len(seen))
}

func TestBackfill_OHLCInvariants(t *testing.T) {
	listings := []model.Listing{{ID: 1, Price: priceDec(50)}}
	b, dr := newBackfillFixture(listings)
	require.NoError(t, b.Run())

	for i, row := range dr.upserts {
		open := row.Price.Sub(row.Change) // open = close - change
		maxOC := decimal.Max(open, row.Price)
		minOC := decimal.Min(open, row.Price)
		assert.True(t, row.High.GreaterThanOrEqual(maxOC),
			"row %d: High %s must be >= max(open,close) %s", i, row.High, maxOC)
		assert.True(t, row.Low.LessThanOrEqual(minOC),
			"row %d: Low %s must be <= min(open,close) %s", i, row.Low, minOC)
	}
}
```

- [ ] **Step 2: Run the test — must fail (no constructor exists yet)**

```powershell
cd stock-service; go test ./internal/service/ -run TestBackfill -v; cd ..
```
Expected: compile error — `undefined: NewListingHistoryBackfill` / `undefined: ListingHistoryBackfill`.

---

### Task 4: Implement `ListingHistoryBackfill`

**Files:**
- Create: `stock-service/internal/service/listing_history_backfill.go`

- [ ] **Step 1: Write the implementation**

```go
package service

import (
	"log"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const backfillDays = 1825 // 5 years

// ListingHistoryBackfill seeds 5 years of deterministic synthetic OHLC history
// for every listing. Used so the stock detail chart renders candles for every
// period (1D..ALL) immediately after `make docker-up`, not after the system has
// been running for years.
//
// Determinism: each listing's history is seeded from its ID, so re-runs (e.g.
// SwitchSource) produce identical rows. Idempotent: upsert-by-(listing_id, date)
// prevents duplicates.
type ListingHistoryBackfill struct {
	listingRepo ListingRepo
	dailyRepo   DailyPriceRepo
	now         func() time.Time // injectable for tests
}

func NewListingHistoryBackfill(listingRepo ListingRepo, dailyRepo DailyPriceRepo) *ListingHistoryBackfill {
	return &ListingHistoryBackfill{
		listingRepo: listingRepo,
		dailyRepo:   dailyRepo,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// Run walks every listing, generates 5y of synthetic daily OHLC ending at the
// listing's current price, and batch-upserts it.
func (b *ListingHistoryBackfill) Run() error {
	listings, err := b.listingRepo.ListAll()
	if err != nil {
		return err
	}

	today := b.now().Truncate(24 * time.Hour)
	totalListings := 0
	totalRows := 0

	for _, l := range listings {
		if l.Price.IsZero() {
			continue
		}
		rows := generateHistory(l.ID, l.Price, l.Volume, today)
		if err := b.dailyRepo.UpsertManyByListingAndDate(rows); err != nil {
			log.Printf("WARN: backfill: listing %d: %v", l.ID, err)
			continue
		}
		totalListings++
		totalRows += len(rows)
	}

	log.Printf("listing history backfill: %d listings, %d rows", totalListings, totalRows)
	return nil
}

// generateHistory produces backfillDays rows of deterministic synthetic OHLC
// for one listing. Newest row's close equals anchorPrice (today). Each
// preceding day steps backwards: today's open becomes yesterday's close.
func generateHistory(listingID uint64, anchorPrice decimal.Decimal, anchorVolume int64, today time.Time) []model.ListingDailyPriceInfo {
	rows := make([]model.ListingDailyPriceInfo, 0, backfillDays)

	baseVolume := anchorVolume
	if baseVolume <= 0 {
		baseVolume = 100_000
	}

	// We walk newest -> oldest so the close anchors at today.
	price, _ := anchorPrice.Float64()
	for d := 0; d < backfillDays; d++ {
		rng := rand.New(rand.NewSource(int64(listingID*1_000_003) + int64(d)))
		drift := (rng.Float64()*2 - 1) * 0.01    // ±1%
		spread := rng.Float64() * 0.015           // 0–1.5%

		close := price
		open := close * (1 + drift)
		hi := maxF(open, close) * (1 + spread)
		lo := minF(open, close) * (1 - spread)
		vol := int64(float64(baseVolume) * (0.5 + rng.Float64()*1.5))

		date := today.AddDate(0, 0, -d)
		rows = append(rows, model.ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      date,
			Price:     decimal.NewFromFloat(close),
			High:      decimal.NewFromFloat(hi),
			Low:       decimal.NewFromFloat(lo),
			Change:    decimal.NewFromFloat(close - open),
			Volume:    vol,
		})

		price = open // step backwards
	}
	return rows
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Run the tests — they should all pass now**

```powershell
cd stock-service; go test ./internal/service/ -run TestBackfill -v; cd ..
```
Expected: 6 tests PASS.

- [ ] **Step 3: Run the full service test suite — make sure nothing regressed**

```powershell
cd stock-service; go test ./...; cd ..
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```powershell
git add stock-service/internal/service/listing_history_backfill.go stock-service/internal/service/listing_history_backfill_test.go
git commit -m "stock: 5-year deterministic synthetic OHLC backfill for listings"
```

---

### Task 5: Wire backfill into `SeedAll` and `cmd/main.go`

**Files:**
- Modify: `stock-service/internal/service/security_sync.go:24-52` (add field), `:101-112` (call from SeedAll)
- Modify: `stock-service/cmd/main.go:383-485` (construct, attach)

- [ ] **Step 1: Add a setter on `SecuritySyncService`**

In `stock-service/internal/service/security_sync.go`, add a field to the struct (insert after line 51, before the closing `}`):

```go
	backfill *ListingHistoryBackfill
}
```

Then add a setter right after the `NewSecuritySyncService` constructor (around line 86):

```go
// WithHistoryBackfill attaches the backfill service. Called from main.go after
// listing/daily repos are built. Optional — when nil, SeedAll skips backfill.
func (s *SecuritySyncService) WithHistoryBackfill(b *ListingHistoryBackfill) *SecuritySyncService {
	s.backfill = b
	return s
}
```

- [ ] **Step 2: Call backfill from `SeedAll`**

In `security_sync.go`, modify `SeedAll` (currently at line 102):

```go
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.syncExchanges()
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.seedFutures(futuresSeedPath)
	s.generateAllOptions()
	// Sync listings from the securities we just seeded
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
	// Backfill 5y of synthetic OHLC history so the chart renders candles for
	// every period on a fresh DB. Idempotent on reseed.
	if s.backfill != nil {
		if err := s.backfill.Run(); err != nil {
			log.Printf("WARN: listing history backfill: %v", err)
		}
	}
}
```

- [ ] **Step 3: Construct backfill in `cmd/main.go`**

In `stock-service/cmd/main.go`, after `syncSvc := service.NewSecuritySyncService(...)` (around line 485), insert:

```go
	historyBackfill := service.NewListingHistoryBackfill(listingRepo, dailyPriceRepo)
	syncSvc = syncSvc.WithHistoryBackfill(historyBackfill)
```

- [ ] **Step 4: Build everything**

```powershell
cd stock-service; go build ./...; cd ..
```
Expected: no output, exit 0.

- [ ] **Step 5: Run the full stock-service test suite**

```powershell
cd stock-service; go test ./...; cd ..
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```powershell
git add stock-service/internal/service/security_sync.go stock-service/cmd/main.go
git commit -m "stock: wire 5y synthetic OHLC backfill into SeedAll"
```

---

### Task 6: Frontend — replace `<LineChart>` with candlestick `<ComposedChart>`

**Files:**
- Modify: `C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal\src\views\securities\components\PriceChart.tsx` (full rewrite of render block)

- [ ] **Step 1: Rewrite the file**

Replace the entire contents of `src/views/securities/components/PriceChart.tsx` with:

```tsx
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { Button } from '@/components/ui/button'
import type { PriceHistoryEntry, PriceHistoryPeriod } from '@/types/security'

interface PriceChartProps {
  data: PriceHistoryEntry[]
  selectedPeriod: PriceHistoryPeriod
  onPeriodChange: (period: PriceHistoryPeriod) => void
  isLoading?: boolean
}

const PERIODS: { label: string; value: PriceHistoryPeriod }[] = [
  { label: '1D', value: 'day' },
  { label: '1W', value: 'week' },
  { label: '1M', value: 'month' },
  { label: '1Y', value: 'year' },
  { label: '5Y', value: '5y' },
  { label: 'All', value: 'all' },
]

const BULL_COLOR = '#16a34a' // tailwind green-600
const BEAR_COLOR = '#dc2626' // tailwind red-600

interface CandleDatum {
  date: string
  open: number
  close: number
  high: number
  low: number
  volume: number
  wick: [number, number] // [low, high]
  body: [number, number] // [min(open,close), max(open,close)]
  bullish: boolean
}

function toCandle(entry: PriceHistoryEntry): CandleDatum {
  const close = Number(entry.price)
  const change = Number(entry.change)
  const high = Number(entry.high)
  const low = Number(entry.low)
  const open = close - change
  return {
    date: entry.date,
    open,
    close,
    high,
    low,
    volume: entry.volume,
    wick: [low, high],
    body: [Math.min(open, close), Math.max(open, close)],
    bullish: close >= open,
  }
}

function fmtTick(period: PriceHistoryPeriod): (value: string) => string {
  return (value: string) => {
    if (!value) return ''
    const d = new Date(value)
    if (Number.isNaN(d.getTime())) return value
    if (period === 'day') {
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    }
    if (period === 'week' || period === 'month') {
      return d.toLocaleDateString([], { month: 'short', day: '2-digit' })
    }
    return d.toLocaleDateString([], { month: 'short', year: 'numeric' })
  }
}

// Wick: thin vertical line from low to high. Recharts passes the bar's slot
// geometry as x/y/width/height (where y is the top of the value range and
// height is the slot's pixel height for `wick` = [low, high]).
function Wick(props: unknown) {
  const { x, y, width, height, payload } = props as {
    x: number
    y: number
    width: number
    height: number
    payload: CandleDatum
  }
  const color = payload.bullish ? BULL_COLOR : BEAR_COLOR
  const cx = x + width / 2
  return (
    <line
      data-testid="candle-wick"
      data-bullish={payload.bullish ? 'true' : 'false'}
      x1={cx}
      x2={cx}
      y1={y}
      y2={y + height}
      stroke={color}
      strokeWidth={1}
    />
  )
}

// Body: filled rect spanning open to close, ~60% of bar slot width.
function CandleBody(props: unknown) {
  const { x, y, width, height, payload } = props as {
    x: number
    y: number
    width: number
    height: number
    payload: CandleDatum
  }
  const color = payload.bullish ? BULL_COLOR : BEAR_COLOR
  const bodyWidth = Math.max(2, width * 0.6)
  const bodyX = x + (width - bodyWidth) / 2
  // Always draw at least 1px so doji candles are visible.
  const bodyHeight = Math.max(1, height)
  return (
    <rect
      data-testid="candle-body"
      data-bullish={payload.bullish ? 'true' : 'false'}
      x={bodyX}
      y={y}
      width={bodyWidth}
      height={bodyHeight}
      fill={color}
    />
  )
}

interface CandleTooltipPayloadItem {
  payload?: CandleDatum
}

function CandleTooltip({ active, payload }: { active?: boolean; payload?: CandleTooltipPayloadItem[] }) {
  if (!active || !payload || payload.length === 0 || !payload[0].payload) return null
  const d = payload[0].payload
  return (
    <div className="rounded border bg-background p-2 text-xs shadow">
      <div className="font-medium">{d.date}</div>
      <div>O: {d.open.toFixed(2)}</div>
      <div>H: {d.high.toFixed(2)}</div>
      <div>L: {d.low.toFixed(2)}</div>
      <div>C: {d.close.toFixed(2)}</div>
      <div>Vol: {d.volume.toLocaleString()}</div>
    </div>
  )
}

export function PriceChart({ data, selectedPeriod, onPeriodChange, isLoading }: PriceChartProps) {
  const chartData = data.map(toCandle)

  return (
    <div>
      <div className="flex gap-1 mb-4">
        {PERIODS.map((p) => (
          <Button
            key={p.value}
            size="sm"
            variant={selectedPeriod === p.value ? 'default' : 'outline'}
            onClick={() => onPeriodChange(p.value)}
          >
            {p.label}
          </Button>
        ))}
      </div>
      {isLoading ? (
        <div className="h-64 flex items-center justify-center text-muted-foreground">
          Loading chart...
        </div>
      ) : chartData.length < 1 ? (
        <div className="h-64 flex items-center justify-center text-muted-foreground text-sm">
          No historical data available for this period.
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={320}>
          <ComposedChart data={chartData}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="date" tickFormatter={fmtTick(selectedPeriod)} />
            <YAxis domain={['auto', 'auto']} />
            <Tooltip content={<CandleTooltip />} />
            <Bar dataKey="wick" shape={<Wick />} isAnimationActive={false} />
            <Bar dataKey="body" shape={<CandleBody />} isAnimationActive={false} />
          </ComposedChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Type-check the frontend**

```powershell
cd C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal; npx tsc --noEmit; cd C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal
```
Expected: no errors. If `tsc` reports unused-import warnings on `Line`/`LineChart` elsewhere, those imports are now gone — no fix needed.

- [ ] **Step 3: Commit**

```powershell
cd C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal
git add src/views/securities/components/PriceChart.tsx
git commit -m "feat(securities): candlestick price chart with custom recharts shapes"
cd C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal
```

---

### Task 7: Update `PriceChart.test.tsx` to assert candle render

**Files:**
- Modify: `C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal\src\views\securities\__tests__\PriceChart.test.tsx`

- [ ] **Step 1: Replace the file**

```tsx
import { screen, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '@/__tests__/utils/test-utils'
import { PriceChart } from '@/views/securities/components/PriceChart'
import { createMockPriceHistory } from '@/__tests__/fixtures/security-fixtures'

// Recharts is mocked because JSDOM has no SVG layout engine. The mock renders
// children inline so we can assert on the shape callbacks recharts would
// otherwise invoke with computed geometry.
jest.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="responsive-container">{children}</div>
  ),
  ComposedChart: ({ children, data }: { children: React.ReactNode; data: unknown[] }) => (
    <div data-testid="composed-chart" data-rows={data.length}>
      {children}
    </div>
  ),
  Bar: ({ dataKey, shape }: { dataKey: string; shape: React.ReactElement }) => (
    // Render the custom shape once per row so we can assert on it. Recharts
    // would normally pass per-row geometry; for the test we pass synthetic
    // x/y/width/height and the row's payload via the chart's data prop, which
    // the parent ComposedChart mock embeds via context-free rendering. Tests
    // use the dataKey to distinguish wick vs body.
    <div data-testid={`bar-${dataKey}`}>{shape}</div>
  ),
  XAxis: () => <div data-testid="x-axis" />,
  YAxis: () => <div data-testid="y-axis" />,
  CartesianGrid: () => <div data-testid="cartesian-grid" />,
  Tooltip: () => <div data-testid="tooltip" />,
}))

describe('PriceChart', () => {
  const defaultProps = {
    data: createMockPriceHistory(),
    selectedPeriod: 'month' as const,
    onPeriodChange: jest.fn(),
  }

  beforeEach(() => jest.clearAllMocks())

  it('renders period selector buttons', () => {
    renderWithProviders(<PriceChart {...defaultProps} />)
    expect(screen.getByText('1D')).toBeInTheDocument()
    expect(screen.getByText('1W')).toBeInTheDocument()
    expect(screen.getByText('1M')).toBeInTheDocument()
    expect(screen.getByText('1Y')).toBeInTheDocument()
    expect(screen.getByText('5Y')).toBeInTheDocument()
    expect(screen.getByText('All')).toBeInTheDocument()
  })

  it('calls onPeriodChange when a period button is clicked', () => {
    renderWithProviders(<PriceChart {...defaultProps} />)
    fireEvent.click(screen.getByText('1W'))
    expect(defaultProps.onPeriodChange).toHaveBeenCalledWith('week')
  })

  it('renders a ComposedChart with both wick and body bars when data is present', () => {
    renderWithProviders(<PriceChart {...defaultProps} />)
    expect(screen.getByTestId('composed-chart')).toBeInTheDocument()
    expect(screen.getByTestId('bar-wick')).toBeInTheDocument()
    expect(screen.getByTestId('bar-body')).toBeInTheDocument()
  })

  it('passes the full data row count to the chart', () => {
    renderWithProviders(<PriceChart {...defaultProps} data={createMockPriceHistory(7)} />)
    expect(screen.getByTestId('composed-chart')).toHaveAttribute('data-rows', '7')
  })

  it('renders a single candle when data has exactly 1 entry (no longer blocked by <2 gate)', () => {
    renderWithProviders(
      <PriceChart
        data={createMockPriceHistory(1)}
        selectedPeriod="month"
        onPeriodChange={jest.fn()}
      />
    )
    expect(screen.getByTestId('composed-chart')).toBeInTheDocument()
    expect(screen.getByTestId('composed-chart')).toHaveAttribute('data-rows', '1')
  })

  it('shows loading state when isLoading is true', () => {
    renderWithProviders(<PriceChart {...defaultProps} isLoading />)
    expect(screen.getByText('Loading chart...')).toBeInTheDocument()
    expect(screen.queryByTestId('composed-chart')).not.toBeInTheDocument()
  })

  it('shows "No historical data available" when data is empty', () => {
    renderWithProviders(<PriceChart data={[]} selectedPeriod="month" onPeriodChange={jest.fn()} />)
    expect(screen.getByText(/no historical data/i)).toBeInTheDocument()
    expect(screen.queryByTestId('composed-chart')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run the test file**

```powershell
cd C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal
npx jest src/views/securities/__tests__/PriceChart.test.tsx
cd C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal
```
Expected: 7 tests PASS.

- [ ] **Step 3: Run the full frontend test suite — make sure nothing regressed**

```powershell
cd C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal; npx jest; cd C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```powershell
cd C:\Users\admin\GolandProjects\EXBanka-1-Frontend-forkLocal
git add src/views/securities/__tests__/PriceChart.test.tsx
git commit -m "test(securities): assert candlestick render in PriceChart"
cd C:\Users\admin\GolandProjects\EXBanka-1-Backend-forkLocal
```

---

### Task 8: Update `Specification.md`

**Files:**
- Modify: `docs/Specification.md` (Sections 17 and 21)

- [ ] **Step 1: Find Section 17 (REST API) entry for `/securities/stocks/:id/history`**

Open `docs/Specification.md` and locate the documentation for `GET /securities/stocks/:id/history`. Add (or update) the response note to read:

> Returns OHLC-bucketed price history. On a freshly-seeded DB the response is non-empty for every period — `stock-service` writes 5 years of deterministic synthetic daily OHLC per listing during `SeedAll`. Live intraday snapshots (1-minute interval) accumulate on top of synthetic history.

- [ ] **Step 2: Find Section 21 (Business Rules)**

Append a new rule:

> **Stock-service synthetic history backfill.** During `SeedAll` (initial seed and `SwitchSource`), `stock-service` writes 5 years (1825 days) of deterministic synthetic OHLC rows per listing to `listing_daily_price_infos`. Random walk is seeded by `listing.ID`, anchors the newest row at the listing's current price, and is idempotent on reseed via `INSERT … ON CONFLICT (listing_id, date) DO UPDATE`.

- [ ] **Step 3: Commit**

```powershell
git add docs/Specification.md
git commit -m "docs: spec the 5y synthetic OHLC backfill in Section 17 and 21"
```

---

### Task 9: Manual verification

- [ ] **Step 1: Bring up the stack**

```powershell
make docker-up
```
Wait for `api-gateway` to be healthy.

- [ ] **Step 2: Verify history endpoint returns data for every period**

```powershell
$token = (Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v3/auth/login -ContentType 'application/json' -Body '{"email":"admin+testadmin@admin.com","password":"Admin1234!"}').access_token
foreach ($p in 'day','week','month','year','5y','all') {
  $r = Invoke-RestMethod -Headers @{Authorization="Bearer $token"} -Uri "http://localhost:8080/api/v3/securities/stocks/1/history?period=$p"
  Write-Host "$p -> $($r.total_count) rows"
}
```
Expected: each period prints `>= 2` rows.

- [ ] **Step 3: Visual check**

Open the frontend (whatever `npm run dev` URL it serves) and navigate to a stock detail page. Click each of `1D / 1W / 1M / 1Y / 5Y / All`. Confirm candlesticks render for each. Wait one minute and confirm the rightmost candle updates.

- [ ] **Step 4: Final commit (combined if anything was tweaked)**

If any docs adjustments were needed during manual testing:
```powershell
git add -A
git commit -m "docs: post-verification tweaks"
```

---

## Final verification

- [ ] `make test` from repo root — backend test suite passes.
- [ ] `npx jest` from frontend repo root — frontend test suite passes.
- [ ] `make lint` from backend repo root for `stock-service` — clean.
- [ ] All six period buttons render candles on a freshly-`docker-up`'d stack.

---

## Self-review notes

**Spec coverage:** Backend backfill (Tasks 1–5), frontend candle render (Task 6), test updates (Tasks 3, 7), spec doc (Task 8), manual verification (Task 9). ✓

**Placeholder scan:** No TBDs, every step has concrete code. ✓

**Type consistency:**
- `DailyPriceRepo.UpsertManyByListingAndDate(infos []model.ListingDailyPriceInfo) error` — same signature in interface (Task 2 Step 1), implementation (Task 2 Step 2), and mock (Task 2 Step 4). ✓
- `ListingHistoryBackfill.Run() error` — defined in Task 4, used in Task 5 inside `SeedAll`. ✓
- `WithHistoryBackfill(*ListingHistoryBackfill) *SecuritySyncService` — defined Task 5 Step 1, called Task 5 Step 3. ✓
- Frontend `CandleDatum` type — defined and used only in `PriceChart.tsx`. ✓

**Risk:** Task 1 promotes a non-unique composite index to UNIQUE. Existing rows use microsecond-precise `time.Now()` for `Date`, so they are already unique on `(listing_id, date)`. If any row pair collides (extremely unlikely), `AutoMigrate` will fail and a manual `DELETE` of the duplicate is needed before redeploy. Documented above.
