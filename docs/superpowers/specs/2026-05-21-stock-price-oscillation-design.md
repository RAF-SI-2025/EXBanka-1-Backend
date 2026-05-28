# Deterministic Stock Price Oscillation (Demo / Test Driver)

**Date:** 2026-05-21
**Status:** Approved
**Scope:** `stock-service` only

## Goal

Replace the current ±0.5% random walk on `GeneratedSource` with a deterministic,
visible price oscillation that demonstrates the system is alive and lets us
verify order triggers (limit, stop, stop-limit, price alerts).

## Behavior

Stocks and futures oscillate around their seed price on a fixed 4-minute cycle
keyed to wallclock (UTC) minutes. Forex stays at its seed value because
cross-currency conversion (`exchange-service.Convert`) depends on these rates
for buy/sell fills and fee math — swinging it ±10% would distort balances.

Phase index for a given instant is `floor(unixSeconds / 60) mod 4`:

| Phase | Multiplier | Meaning           |
|-------|------------|-------------------|
| 0     | 0.90       | -10% (trough)     |
| 1     | 1.00       | back to base      |
| 2     | 1.10       | +10% (peak)       |
| 3     | 1.00       | back to base      |

`RefreshPrices` recomputes `stockPx[t] = baseStockPx[t] * multiplier(phase)` and
the same for futures. Base seeds are captured once at construction and never
mutated, so the cycle never drifts.

## Visibility

The security price refresh interval default drops from 15 min → 1 min
(`SECURITY_SYNC_INTERVAL_MINUTES=1`). Each tick the DB is updated to the
current-minute phase, so listings, holdings, and portfolio reads visibly step
every minute.

## Non-goals

- No oscillation on forex (would break conversion).
- No oscillation on the `external` source (real provider data) or `simulator`
  source (delegates to market-simulator).
- No new HTTP/gRPC endpoints.

## Code touchpoints

- `stock-service/internal/source/generated_source.go`
  - Add `baseStockPx`, `baseFuturesPx` maps captured at construction.
  - Replace `RefreshPrices` random walk with deterministic phase computation.
  - Inject a clock function for deterministic tests.
- `stock-service/internal/source/generated_source_test.go`
  - Update existing random-walk tests to assert oscillation behavior.
  - Add coverage: each phase multiplier, full cycle returns to base, no drift
    across many cycles, forex unchanged.
- `stock-service/internal/config/config.go`
  - Default `SecuritySyncIntervalMins` from `15` → `1`.
- `stock-service/internal/config/config_test.go`
  - Update default-value assertion.
- `Specification.md`
  - Note new default refresh interval and the oscillation business rule.

## Verification

- Unit tests pass (`go test ./stock-service/...`).
- `make lint` clean for the service.
- Manual: start docker stack with `generated` source, observe a stock's price
  in `/api/v1/stocks/{ticker}` change at minute boundaries between 0.9×, 1.0×,
  1.1×, 1.0× of seed.
