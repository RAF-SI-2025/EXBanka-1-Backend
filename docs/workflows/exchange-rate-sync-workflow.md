# Exchange Rate Sync Workflow

## Summary

Exchange rates are fetched from an external API every 6 hours and stored in the database. All rate pairs are upserted in a single DB transaction: if any upsert fails, the entire sync is rolled back and the previous rates remain intact.

**Coordination pattern:** Atomic DB transaction (all-or-nothing).

**Services involved:** exchange-service (internal cron → external API → PostgreSQL)

## Trigger

`exchange-service/cmd/main.go` starts a ticker with `EXCHANGE_SYNC_INTERVAL_HOURS` (default: 6). On each tick:
```
SyncRates(ctx, provider)
```

## Sync Flow

```
SyncRates(ctx, provider)
    │
    ├─ FetchRatesFromRSD() — HTTP GET open.er-api.com
    │   If fails: log WARN, return error, keep existing rates intact
    │
    └─ db.Transaction:
        For each (code, midRate) in response:
            UpsertInTx(tx, "RSD", code, buy, sell)    ──► If fails: TX ROLLBACK (all pairs)
            UpsertInTx(tx, code, "RSD", buy, sell)    ──► If fails: TX ROLLBACK (all pairs)
        If all succeed: TX COMMIT
```

## Upsert Logic (`UpsertInTx`)

Each currency pair is upserted with SELECT FOR UPDATE to prevent concurrent writes:

```
SELECT ... FOR UPDATE WHERE from_currency=? AND to_currency=?
    ├─ Not found: INSERT new row (version=1)
    └─ Found: UPDATE buy_rate, sell_rate, version++ (manual increment, not GORM hook)
```

Version is manually incremented (not via GORM BeforeUpdate hook) because the `Updates(map[...])` approach is used to bypass the hook — the FOR UPDATE lock already provides concurrency safety.

## Rate Pair Structure

For each currency code (e.g. EUR), two pairs are stored:

| Pair | Direction | BuyRate | SellRate |
|------|-----------|---------|---------|
| `RSD/EUR` | RSD → EUR | mid × (1 - spread) | mid × (1 + spread) |
| `EUR/RSD` | EUR → RSD | (1/mid) × (1 - spread) | (1/mid) × (1 + spread) |

The bank always uses the **sell rate** when executing conversions.

## Conversion Logic (transaction-service)

When transaction-service needs to convert currencies, it calls `exchange-service.Convert(from, to, amount)`:

| Path | Method | Description |
|------|--------|-------------|
| Same currency | Return amount unchanged | No conversion needed |
| RSD → foreign | Single-leg: `repo.GetByPair("RSD", to)` | Apply sell rate |
| Foreign → RSD | Single-leg: `repo.GetByPair(from, "RSD")` | Apply sell rate |
| Foreign → foreign | Two-leg: from→RSD→to | Apply sell rate twice |

## On Failure

| Failure Point | Effect | Recovery |
|--------------|--------|---------|
| External API unreachable | No update; log WARN | Next sync in 6h |
| Partial DB failure (single upsert) | TX rollback; no pairs updated | Next sync in 6h |
| All DB writes fail | TX rollback; no change | Next sync in 6h |

**There is no manual retry API.** If the 6-hour sync window is unacceptable in production, reduce `EXCHANGE_SYNC_INTERVAL_HOURS` or add a manual trigger endpoint.

## Seeded Defaults

On first startup, hardcoded default rates are seeded if the table is empty. This ensures the service is functional before the first external sync completes. External API sync failure is non-fatal (service continues with seed rates).
