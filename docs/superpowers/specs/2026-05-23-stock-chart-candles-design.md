# Stock Detail Chart — Candlesticks for Every Period

**Date:** 2026-05-23
**Status:** Approved
**Scope:** `stock-service` (backend backfill) + frontend `PriceChart.tsx` (candle render). Applies to stocks, futures, and forex detail pages because they share `PriceChart`.

## Goal

The stock/futures/forex detail page shows a price chart with period selectors **1D, 1W, 1M, 1Y, 5Y, All**. Today the chart is a line chart and is frequently blank because a freshly-seeded DB has no historical data. After this change:

1. The chart renders **OHLC candlesticks**, not a line.
2. Every period button renders a populated chart immediately after `make docker-up` — no waiting for wallclock time to accumulate.
3. Live oscillation continues to land on top of the synthetic history (the current minute's candle visibly updates each tick).

## Non-goals

- No InfluxDB work. The `/securities/candles` endpoint stays as-is.
- No new HTTP/gRPC endpoints. `/securities/stocks/:id/history` contract is unchanged.
- No new charting dependency. Reuse the already-installed `recharts` package.

## Architecture

```
[stock-service SeedAll]
  ├─ syncStocks                          (existing)
  ├─ SyncListingsFromSecurities          (existing)
  └─ NEW: ListingHistoryBackfill.Run()
          writes 5y of synthetic daily OHLC into listing_daily_price_infos,
          idempotent (upsert on (listing_id, date))

[stock-service refresh loop]
  └─ SnapshotIntradayPrices              (existing; today's row keeps updating
                                          on top of backfilled history)

[GET /securities/stocks/:id/history]     (unchanged response contract)
  └─ now returns ≥ 2 buckets for every period on a fresh DB

[frontend PriceChart.tsx]
  ├─ ComposedChart + custom-shape candles replace LineChart
  └─ "<2 rows" gate dropped to "<1" so a single candle still renders
```

## Backend — synthetic history backfill

**New file:** `stock-service/internal/service/listing_history_backfill.go`

**Type:**

```go
type ListingHistoryBackfill struct {
    listingRepo ListingRepo
    dailyRepo   DailyPriceRepo
}

func NewListingHistoryBackfill(l ListingRepo, d DailyPriceRepo) *ListingHistoryBackfill
func (b *ListingHistoryBackfill) Run() error
```

**Algorithm per listing** (deterministic, seeded by `listing.ID`; runs newest→oldest then writes the resulting slice in any order because each row is keyed by date):

```
backfillDays = 1825   // 5 years
seed         = int64(listing.ID*1_000_003)
price        = listing.Price                // anchor: today equals current seed
baseVolume   = max(listing.Volume, 100_000) // fallback when listing.Volume == 0
for d := 0; d < backfillDays; d++ {
    rng    = rand.New(rand.NewSource(seed + int64(d)))
    drift  = (rng.Float64()*2 - 1) * 0.01    // ±1%
    spread = rng.Float64() * 0.015            // 0–1.5%
    date   = truncateToUTCMidnight(today.AddDate(0, 0, -d))
    close  = price
    open   = close * (1 + drift)
    high   = max(open, close) * (1 + spread)
    low    = min(open, close) * (1 - spread)
    vol    = int64(baseVolume * (0.5 + rng.Float64()*1.5))
    change = close - open
    rows = append(rows, { listing_id, date, price:close, high, low, change, volume:vol })
    price = open                              // step backwards
}
```

Listings with `Price == 0` are skipped (avoids zero-anchored garbage history during a seed race).

**New repository batch method:** `DailyPriceRepo.UpsertManyByListingAndDate(rows []model.ListingDailyPriceInfo) error` — single `INSERT … ON CONFLICT (listing_id, date) DO UPDATE SET price=excluded.price, …`. Required because 50 listings × 1825 rows = 90k upserts; per-row transactions are too slow.

**Model change:** `model.ListingDailyPriceInfo.Date` GORM tag changes from `index:idx_listing_daily_listing_date` to `uniqueIndex:idx_listing_daily_listing_date` so `ON CONFLICT (listing_id, date)` resolves. The composite index already pairs the two columns; adding `unique` is an `ALTER TABLE` that GORM's `AutoMigrate` handles on next startup.

**Wiring** (`stock-service/internal/service/security_sync.go`):

- `SeedAll` calls `ListingHistoryBackfill.Run()` immediately after `SyncListingsFromSecurities`.
- `reseedAll` inherits the call via `SeedAll`, so source switches refresh history too.
- `ListingCronService.SeedInitialSnapshot` stays — it's a no-op when backfill has already written today's row (upsert).
- `cmd/main.go` constructs the new service and passes it to `NewSecuritySyncService` (one extra constructor argument).

## Frontend — candle render

**Modified file:** `src/views/securities/components/PriceChart.tsx`. Props (`data`, `selectedPeriod`, `onPeriodChange`, `isLoading`) unchanged so the three consumer detail views need no changes.

**Data mapping:**

```ts
const chartData = data.map(e => {
  const close = Number(e.price)
  const open  = close - Number(e.change)
  const high  = Number(e.high)
  const low   = Number(e.low)
  return {
    date: e.date,
    open, close, high, low,
    wick: [low, high],
    body: [Math.min(open, close), Math.max(open, close)],
    bullish: close >= open,
  }
})
```

**Render:**

```tsx
<ResponsiveContainer width="100%" height={320}>
  <ComposedChart data={chartData}>
    <CartesianGrid strokeDasharray="3 3" />
    <XAxis dataKey="date" tickFormatter={fmtTick(selectedPeriod)} />
    <YAxis domain={['auto', 'auto']} />
    <Tooltip content={<CandleTooltip />} />
    <Bar dataKey="wick" shape={<Wick />} />
    <Bar dataKey="body" shape={<CandleBody />} />
  </ComposedChart>
</ResponsiveContainer>
```

**Custom shapes** (inline in the same file, ~40 LOC):

- `<Wick>` — vertical 1px line at `x + width/2` from `y(low)` to `y(high)`, stroke color from `bullish`.
- `<CandleBody>` — filled rect, width ≈ 60% of slot, height = `|openY − closeY|`, fill color from `bullish`.

Recharts passes `x, y, width, height, payload` to shape components, so the scale is handled automatically.

**Tooltip** shows date, O/H/L/C, volume.

**Tick formatter:**

| Period | Format |
|--------|--------|
| `day`  | `HH:mm` |
| `week`, `month` | `MMM dd` |
| `year`, `5y`, `all` | `MMM yyyy` |

**Colors:** bullish uses the existing `--success` (or `--chart-1`) token, bearish uses `--destructive`. Picked from the theme variables already in the codebase.

**Empty-state gate:** `chartData.length < 1` (was `< 2`). A single candle is a valid render.

## Testing

### Backend (`stock-service`)

`stock-service/internal/service/listing_history_backfill_test.go` — new file. Test cases:

1. **Determinism** — `Run()` twice on the same listing produces identical row contents.
2. **Anchor** — newest row's `Price` equals the listing's current `Price`.
3. **Idempotency** — running `Run()` twice leaves row count at exactly `backfillDays` per listing (upsert, not insert).
4. **Zero-price skip** — a listing with `Price == 0` produces no rows.
5. **Date contiguity** — all 1825 dates are distinct and consecutive (one per day).
6. **OHLC invariants** — for every row, `High ≥ max(Open, Close)` and `Low ≤ min(Open, Close)`.

Update `listing_service_extra_test.go`: after running the backfill, `GetPriceHistoryForSecurity` returns ≥ 2 buckets for `day`, `week`, `month`, `year`, `5y`, `all`.

### Frontend (`PriceChart.test.tsx`)

Replace the `recharts` mock — `LineChart` → `ComposedChart`, drop `Line`, add `Bar`. Tests:

1. Renders one bar per candle row (assert by count of `data-testid="bar"`).
2. Bullish row (close > open) marked bullish; bearish row (close < open) marked bearish. Use a `data-bullish` attribute on the mocked `<Bar>` for assertion.
3. Single-candle data renders (was previously blocked by the `<2` gate).
4. Empty data shows the "No historical data" message.
5. Period button clicks still fire `onPeriodChange` with the correct value.

### Manual

`make docker-up`, log in as `admin+testadmin@admin.com / Admin1234!`, open a stock detail page, click through every period button — each renders candles. Wait 60s — today's candle visibly updates.

## Spec / lint / build

- `Specification.md` Section 17 (REST API): note that `/securities/stocks/:id/history` returns synthetic OHLC history on a freshly-seeded DB.
- `Specification.md` Section 21 (business rules): note the 5-year deterministic synthetic backfill at seed time.
- `make lint` clean for `stock-service`. No api-gateway changes.
- No env var changes, no docker-compose changes, no proto changes.
