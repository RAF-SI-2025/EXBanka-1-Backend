# Stock Data Source Abstraction + v2 Options API — Design

**Date:** 2026-04-13
**Scope:** stock-service, api-gateway, user-service (permission seed), CLAUDE.md
**Status:** approved by user; pending implementation plan

---

## 1. Goals

1. Abstract how stock-service acquires securities data (stocks, futures, forex, options) behind a single interface with three interchangeable implementations: existing external providers, locally generated deterministic data, and the Market-Simulator HTTP API.
2. Provide an admin-only endpoint to switch the active source at runtime. A switch wipes stock-service data *and* trading state (orders, holdings, capital gains, tax collections) and reseeds from the new source.
3. Introduce a `/api/v2` router that adds two new routes — create an option order and exercise an option — while preserving every existing `/api/v1` route verbatim. `/api/v2` falls back to `/api/v1` for any path not explicitly redefined at v2.
4. Codify in `CLAUDE.md` that newer API versions must not break older ones without explicit user authorization.
5. Audit (read-only) whether buying a stock or futures contract correctly surfaces the holding in the user's portfolio.

## 2. Non-goals

- Changing any existing `/api/v1` request or response shape in a breaking way. The only v1 change is adding an **optional** `listing_id` field to `OptionItem` / `OptionDetail`, which existing clients that ignore unknown fields will tolerate.
- Making per-instrument-type source selection possible (stocks from X, options from Y). One global source only.
- Persisting per-user state across source switches. A switch is explicitly destructive.
- Rewriting the existing Alpaca / EODHD / AlphaVantage / Finnhub integrations — they are wrapped, not replaced.
- Adding Market-Simulator as a Docker Compose service or `depends_on` entry. Market-Simulator runs independently; stock-service reaches a hard-coded URL at runtime.

## 3. Architecture

### 3.1 New package: `stock-service/internal/source/`

Defines a single interface and three implementations. The sync service holds exactly one `Source` at a time; switching sources replaces the reference under a mutex.

```go
package source

type Source interface {
    Name() string // "external" | "generated" | "simulator"

    FetchExchanges(ctx context.Context) ([]model.StockExchange, error)
    FetchStocks(ctx context.Context) ([]StockWithListing, error)
    FetchFutures(ctx context.Context) ([]FuturesWithListing, error)
    FetchForex(ctx context.Context) ([]ForexWithListing, error)
    FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error)

    // Called by the refresh loop; only meaningful for `simulator` (3s cadence)
    // and `external` (existing slower cadence). `generated` implements a cheap
    // in-process random walk so demos look alive.
    RefreshPrices(ctx context.Context) error
}

// Helper DTOs so the caller can insert a security + its listing in one pass.
type StockWithListing struct {
    Stock      model.Stock
    ExchangeID uint64
    Price      decimal.Decimal
    High       decimal.Decimal
    Low        decimal.Decimal
}
type FuturesWithListing struct { /* symmetric */ }
type ForexWithListing struct  { /* symmetric */ }
```

Implementations:

- **`external_source.go`** — wraps the existing `AlpacaClient`, `EODHDClient`, `AlphaVantageClient`, `FinnhubClient`. The current logic in `SecuritySyncService.syncStocks`, `syncStockPrices`, `seedForexPairs`, `seedFutures`, `generateAllOptions` is lifted here almost verbatim. This is the default on fresh installs and matches present-day behavior.

- **`generated_source.go`** — deterministic, entirely local. See §4 for details.

- **`simulator_source.go`** — HTTP client against Market-Simulator. See §5 for details.

### 3.2 `SecuritySyncService` changes

Stops holding the four concrete provider clients. Holds a single `source.Source` behind a `sync.RWMutex`. All `syncX` / `seedX` / `generateAllOptions` methods become thin wrappers: call `source.FetchX()` then upsert. Repository and handler layers are unchanged.

### 3.3 Option ↔ listing unification

**The only v1-visible change in this entire project.**

Today `listings.security_type` is enumerated as `"stock" | "futures" | "forex"` with an explicit code comment that options have no listings. We extend the enum with `"option"`. For every option persisted by any source, the sync layer inserts a matching `listings` row:

- `security_id = option.id`
- `security_type = "option"`
- `exchange_id` = the exchange of the option's underlying stock (derived via `stocks[option.StockID] → listings WHERE security_type='stock' → exchange_id`). Since the schema enforces one stock per exchange (unique ticker), this lookup is always deterministic.
- `price = option.Premium` (denormalized, as with other security types).

The `Option` model gains a nullable `ListingID *uint64` field so the sync path can write it back. The protobuf `OptionItem` and `OptionDetail` messages each gain an optional `uint64 listing_id` field. Existing v1 clients that do not read this field see identical responses. New v2 clients use it to create orders.

No join table is introduced. There is no new "mapping" — we are simply removing the historical "options are different" carve-out.

## 4. Generated source

Hard-coded, deterministic, stable across restarts. No external I/O.

### 4.1 Exchanges (20)

Real-sounding global exchanges, identified by acronym and MIC code, seeded with stable IDs 1..20:

```
NYSE, NASDAQ, LSE, TSE (Tokyo), HKEX, SSE (Shanghai), EURONEXT, TSX (Toronto),
BSE (Bombay), ASX (Australia), JSE (Johannesburg), BMV (Mexico), BVMF (Brazil),
KRX (Korea), BME (Madrid), SIX (Swiss), OMX (Nordic), WSE (Warsaw),
BVC (Colombia), MOEX (Moscow)
```

### 4.2 Stocks (20)

```
AAPL, MSFT, GOOGL, AMZN, META, NVDA, TSLA, JPM, V, JNJ,
WMT, PG, MA, HD, DIS, BAC, XOM, KO, PEP, CSCO
```

Each pinned to one exchange via a deterministic hash (`fnv32(ticker) % 20 + 1`). Initial prices come from a fixed in-code table of roughly realistic snapshots (e.g., AAPL ≈ $180, MSFT ≈ $420, NVDA ≈ $900).

### 4.3 Options (20 per stock → 400)

The existing `GenerateOptionsForStock` loop continues to run, but now it is guaranteed non-empty because the generated source populates non-zero stock prices. Each stock gets 10 calls + 10 puts across a strike grid centered on spot, expiring 30–180 days out.

### 4.4 Forex (56 pairs)

Every ordered combination of the 8 supported currencies (`RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD`) — `8 × 7 = 56` pairs. Prices drawn from a fixed realistic table (e.g., `EUR/USD = 1.08`).

### 4.5 Futures (20)

Real-sounding commodity, energy, and index futures tickers with realistic prices and near-term settlement dates:

```
CL (crude oil), GC (gold), SI (silver), NG (nat gas), HG (copper),
ZC (corn), ZS (soy), ZW (wheat), CC (cocoa), KC (coffee),
SB (sugar), CT (cotton), ES (S&P500), NQ (Nasdaq100), YM (Dow),
RTY (Russell2k), ZB (30Y T-bond), ZN (10Y note), 6E (euro FX), 6J (yen FX)
```

### 4.6 Refresh

`RefreshPrices` applies a ±0.5% random walk per tick using a seeded PRNG. Premiums on options are re-derived from the new spot via the existing `estimatePremium` function. Nothing talks to the network.

### 4.7 Determinism guarantees

- Same seed → same tickers, same initial prices, same option strikes, same settlement dates — every startup.
- `RefreshPrices` uses a PRNG keyed by a rolling counter so walks are reproducible within a single process but evolve over time.

## 5. Simulator source

### 5.1 Hard-coded config

No environment variables introduced. Constants in `internal/source/simulator_source.go`:

```go
const (
    simulatorBaseURL         = "http://market-simulator:8080"
    simulatorBankName        = "ExBanka"
    simulatorRefreshInterval = 3 * time.Second
)
```

### 5.2 Self-registration flow

Runs when the simulator source is activated, and on stock-service boot if `system_settings.active_stock_source = simulator`:

1. Read `system_settings.market_simulator_api_key`. If present, validate with `GET /api/banks/me` (sends `X-API-Key: <key>`).
2. If missing, or validation returns 401, call `POST /api/banks/register` with body `{"name": "ExBanka"}`. Persist the returned `api_key` to `system_settings`.
3. If registration returns 409 (name taken — e.g., stale Market-Simulator DB with an orphaned bank), log an explicit fatal error with operator guidance. Do not auto-append suffixes.
4. If Market-Simulator is unreachable at switch time, the admin switch endpoint responds 503 with a clear message; the DB is not wiped.

### 5.3 Stock selection

Market-Simulator supports one stock on multiple exchanges; our schema does not. Deterministic selection:

1. `GET /api/market/stocks?per_page=200` — pull up to 200 stocks.
2. For each, call `GET /api/market/stocks/:ticker/listings`.
3. Pick the listing with the **lowest exchange ID** and ingest that pair.
4. Persist one `stocks` row + one `listings` row per chosen stock.

Identical strategy applies to options: for each ingested stock, call `GET /api/market/options?stock_id=<simulator_stock_id>&per_page=100` and persist the result. The option's listing gets inserted on the same exchange as its underlying stock (by construction).

Futures and forex are pulled straight through — one row each, no selection needed.

### 5.4 Background refresh

When the simulator source is active, a dedicated goroutine runs every `simulatorRefreshInterval`:

- Re-fetch prices for stocks, futures, forex pairs, and option premiums.
- Update only `listings.price`, `listings.high`, `listings.low`, `stocks.price`, `options.premium`. Metadata rows are untouched.
- The goroutine is bound to a `context.Context` owned by the sync service. Switching away from `simulator` cancels the context and the goroutine exits on the next loop iteration.

## 6. Source-switching API

### 6.1 Routes

**`POST /api/v1/admin/stock-source`**

- Middleware: `AuthMiddleware` + `RequirePermission("securities.manage")`.
- Request: `{ "source": "external" | "generated" | "simulator" }`.
- Validation: `source` value enforced via `oneOf` helper.
- Response 202 (async sources) / 200 (synchronous `generated`):
  ```json
  {
    "source": "simulator",
    "status": "reseeding" | "idle" | "failed",
    "started_at": "2026-04-13T12:34:56Z"
  }
  ```

**`GET /api/v1/admin/stock-source`**

- Same middleware.
- Returns the currently active source, last successful switch timestamp, current reseed status, and last error message if status is `failed`.

### 6.2 Switch behavior

1. **Acquire** the in-memory switch mutex in the sync service. Concurrent switch requests return 409 `conflict`.
2. **Validate** the target source. For `simulator`, ping `GET /api/banks/me` (or register fresh) first — fail fast with 503 if unreachable.
3. **Persist** the new active source to `system_settings.active_stock_source`.
4. **Wipe** (inside a single `db.Transaction`, honoring FK order):
   - `tax_collections`
   - `capital_gains`
   - `order_transactions`
   - `orders`
   - `holdings`
   - `options`
   - `listings`
   - `forex_pairs`
   - `futures_contracts`
   - `stocks`
   - `stock_exchanges`
5. **Reseed** via the new source. `generated` is synchronous (<100 ms). `external` and `simulator` run in a background goroutine; the endpoint returns 202 with `status: "reseeding"`. The goroutine writes its progress (`idle` / `reseeding` / `failed`) and last error back to `system_settings` so `GET /admin/stock-source` can report it.
6. **For simulator:** after reseed, start the 3s refresh goroutine.
7. **On cancellation / failure:** leave DB in the wiped-then-partially-seeded state but set status to `failed` with the error. Admin can retry the switch to recover.

### 6.3 Startup behavior

Stock-service boot reads `system_settings.active_stock_source`:
- Unset → default to `external`, run the current legacy boot path (no change).
- `generated` → use the generated source.
- `simulator` → run self-registration, use the simulator source, start the refresh goroutine.

### 6.4 Permission wiring

- Add `"securities.manage"` permission constant to `user-service/internal/service/role_service.go`.
- Append it to the seed permission list for `EmployeeAdmin` only.
- `EmployeeSupervisor`, `EmployeeAgent`, `EmployeeBasic` do NOT receive it.
- Add to Section 6 of `Specification.md`.

## 7. v2 router

### 7.1 New file: `api-gateway/internal/router/router_v2.go`

Exports `SetupV2Routes(r *gin.Engine, …same deps as v1…)`. Called from `cmd/main.go` immediately after `SetupV1Routes`.

### 7.2 Fallback to v1

A single `r.NoRoute` handler installed by `SetupV2Routes` (because the engine allows only one):

```go
r.NoRoute(func(c *gin.Context) {
    if strings.HasPrefix(c.Request.URL.Path, "/api/v2/") {
        c.Request.URL.Path = "/api/v1/" + strings.TrimPrefix(c.Request.URL.Path, "/api/v2/")
        r.HandleContext(c)
        return
    }
    apiError(c, http.StatusNotFound, "not_found", "route not found")
})
```

Behavior:
- `POST /api/v2/options/:id/orders` → v2 handler (explicit route).
- `GET /api/v2/securities/stocks` → no v2 route → fallback rewrites to `/api/v1/securities/stocks` → v1 handler.
- `GET /api/v1/securities/stocks` → matches v1 directly; v2 never consulted.
- `POST /api/v2/garbage` → no v2 route → rewrite to `/api/v1/garbage` → no v1 match → real 404.

Method mismatches at v1 are also surfaced as 404 (Gin returns 404 for unmatched method+path pairs). This is acceptable — the alternative would be a 405 layer and it is out of scope.

### 7.3 Routes

**`POST /api/v2/options/:option_id/orders`**

- Middleware: `AnyAuthMiddleware` + `RequirePermission("securities.trade")`.
- Path: `option_id` — validated `uint > 0`.
- Body:
  ```json
  {
    "direction": "buy" | "sell",
    "order_type": "market" | "limit" | "stop" | "stop_limit",
    "quantity": 1,
    "limit_value": "5.75",
    "stop_value": "6.00",
    "all_or_none": false,
    "margin": true,
    "account_id": 42,
    "holding_id": 0
  }
  ```
- Gateway validation:
  - `direction` via `oneOf("direction", val, "buy", "sell")`.
  - `order_type` via `oneOf` on the 4 allowed values.
  - `quantity` > 0 via `positive`.
  - `limit_value` required when `order_type ∈ {limit, stop_limit}`; parseable decimal.
  - `stop_value` required when `order_type ∈ {stop, stop_limit}`; parseable decimal.
  - `account_id` > 0.
- Handler flow:
  1. Call `securityClient.GetOption(option_id)` → reads `OptionDetail.listing_id`.
  2. If `listing_id` is unset (e.g., stale data from before the migration), return 409 `business_rule_violation` with message "option not tradeable".
  3. Call existing `orderClient.CreateOrder` with the resolved `listing_id` and body fields. No changes to `CreateOrder` RPC contract.
- Response: 201 with the existing `Order` JSON shape.

**`POST /api/v2/options/:option_id/exercise`**

- Same middleware.
- Path: `option_id`.
- Body:
  ```json
  {
    "holding_id": 0
  }
  ```
- Handler flow:
  1. Call a **new** gRPC RPC `ExerciseOptionByOptionID(user_id, option_id, holding_id)` added to the portfolio service.
  2. Return the existing `ExerciseResult` shape as 200.

**New RPC (additive to `contract/proto/stock/stock.proto`):**

```protobuf
rpc ExerciseOptionByOptionID(ExerciseOptionByOptionIDRequest) returns (ExerciseResult);

message ExerciseOptionByOptionIDRequest {
  uint64 option_id = 1;
  uint64 user_id = 2;
  uint64 holding_id = 3; // optional; 0 means auto-resolve
}
```

**Stock-service implementation:**
- If `holding_id > 0`, load that holding and delegate to the existing `ExerciseOption` logic.
- If `holding_id == 0`, query `holdings WHERE user_id = $1 AND security_type = 'option' AND security_id = $2 AND quantity > 0 ORDER BY created_at ASC`. Pick the first. Return `NotFound` if none. (The `Holding` model already supports `security_type = "option"` per `stock-service/internal/model/holding.go:18` — no schema change needed.)
- Reject (FailedPrecondition) if the holding is not for the requested option or is short.

The existing `ExerciseOption(holding_id)` RPC and v1 `/api/me/portfolio/:id/exercise` route stay untouched.

### 7.4 Swagger & docs

- Both v2 handlers carry full `@Summary / @Tags / @Param / @Success / @Failure / @Router` annotations.
- `make swagger` regenerates `api-gateway/docs/` as usual. Only explicit v2 routes appear; the v2→v1 fallback is not in swagger.
- New doc file `docs/api/REST_API_v2.md` documents the two new routes and opens with: *"Any path not listed here transparently falls back to /api/v1. See REST_API_v1.md for the full v1 surface."*
- `docs/api/REST_API_v1.md` is untouched except for the additive `listing_id` field on option responses (noted inline in the options section).

## 8. Portfolio audit (task 5)

Read-only trace — no code changes unless the audit reveals a bug and the user authorizes a fix.

Trace scope:
1. `stock-service/internal/service/order_execution.go` — follow the buy path for `security_type = stock` and `security_type = futures` to confirm the execution step writes to `holdings`.
2. `stock-service/internal/repository/holding_repository.go` — confirm there is an `UpsertByUserAndListing` (or equivalent) call invoked from the execution path.
3. `stock-service/internal/service/portfolio_service.go` — confirm `ListMyPortfolio` reads `holdings` filtered by `user_id`, and that the response DTO includes the newly created holding.
4. Gateway path: `GET /api/v1/me/portfolio` → `portfolioClient.ListMyHoldings` → `PortfolioHandler.ListMyHoldings` → `portfolio_service.go`.

Findings are captured in the implementation plan (`docs/superpowers/plans/2026-04-13-*.md`) under a dedicated "Portfolio Audit" section. A bug, if found, is flagged to the user before any fix is attempted.

## 9. Testing

### 9.1 Unit tests

- `stock-service/internal/source/generated_source_test.go` — asserts counts (20 / 20 / 400 / 56 / 20), determinism across two `Seed()` runs with the same seed, non-zero prices, stable ticker-to-exchange assignment.
- `stock-service/internal/source/simulator_source_test.go` — uses `httptest.Server` to mock Market-Simulator. Covers: successful self-registration, key reuse from `system_settings`, 401 → re-registration, 409 → fatal, stock selection picks lowest exchange ID, refresh loop updates prices.
- `stock-service/internal/source/external_source_test.go` — refactor parity test against the existing provider mocks.
- `stock-service/internal/service/security_sync_test.go` — updated to inject a `source.Source` stub; verifies wipe order, reseed, and refresh-goroutine lifecycle.
- `api-gateway/internal/handler/securities_handler_test.go` — add cases for the new `listing_id` field being passed through (optional).
- `api-gateway/internal/handler/options_v2_handler_test.go` — new file: create-order and exercise-option happy paths, validation failures, option-without-listing error.
- `api-gateway/internal/router/router_v2_test.go` — asserts fallback: `/api/v2/securities/stocks` reaches the v1 handler; `/api/v2/options/:id/orders` reaches the v2 handler; truly unknown paths return 404.
- `user-service/internal/service/role_service_test.go` — updated seed assertion with `securities.manage` on `EmployeeAdmin`.

### 9.2 Integration tests (`test-app/workflows/`)

- `stock_source_switch_test.go` — admin logs in, switches to `generated`, `GET /securities/stocks` returns the 20 seeded tickers, `GET /securities/options?stock_id=X` returns options, switches back to `external`, holdings are empty (confirming wipe).
- `options_order_v2_test.go` — create option order via `POST /api/v2/options/:id/orders`, employee approves, client hits `GET /api/me/portfolio`, holding appears.
- `options_exercise_v2_test.go` — seeded option holding, exercise via `POST /api/v2/options/:id/exercise`, verify cash and stock movements.
- `v2_fallback_test.go` — `/api/v2/securities/stocks` returns the same payload as `/api/v1/securities/stocks`.

### 9.3 Linting & docs

- `make lint` on `stock-service`, `api-gateway`, `user-service`.
- `make swagger` to regenerate `api-gateway/docs/`.
- `make test` must pass.
- `docs/api/REST_API_v1.md` updated only for the optional `listing_id` field note.
- New `docs/api/REST_API_v2.md`.
- `Specification.md` updated per CLAUDE.md Section 1–2 instructions (new route, new permission, new model field, new v2 router).

## 10. Docker Compose

No changes. Market-Simulator runs independently. Stock-service reaches the hard-coded `http://market-simulator:8080` at runtime when the simulator source is active. If unreachable, the switch endpoint returns 503 without mutating state.

## 11. CLAUDE.md update

New section appended (placement: immediately before "Implementation Plans"):

```
## API Versioning Compatibility Requirement

**Newer versions of the API must never break older versions unless the user has explicitly permitted it.** This is a hard requirement — not optional.

- When introducing `/api/v{N+1}/...` routes, existing `/api/v{N}/...` routes must remain functional with their exact current request and response shapes.
- Adding new *optional* fields to v{N} response bodies is allowed and does not count as a breaking change, provided existing clients that ignore unknown fields continue to work.
- Removing fields, renaming fields, changing field types, changing HTTP methods, changing status codes, changing authentication requirements, or tightening validation on existing v{N} routes are breaking changes and require explicit user authorization before merging.
- The v2 router in api-gateway transparently falls back to v1 for any route not explicitly redefined at v2, so v2 clients can reach any v1 endpoint using a v2 URL.
- When in doubt, ask the user before changing any existing route.
```

## 12. Open risks & mitigations

- **Market-Simulator downtime mid-refresh.** The 3s goroutine logs errors and continues; prices go stale but reads do not fail. Status endpoint surfaces the last refresh error.
- **Long external reseed blocks subsequent switches.** The switch mutex is held for the duration. We accept this — this is an admin-only operation and concurrent switches are nonsensical. Returns 409 to concurrent callers.
- **FK order on wipe.** Unit test the wipe helper specifically against a populated DB so any regression in FK ordering fails loudly.
- **Existing holdings referencing stocks that get wiped.** Not a risk because holdings are wiped too — documented in the switch endpoint's swagger `@Description`.
- **Stale `listing_id` on options from before the migration.** v2 create-order handler returns 409 with a clear message rather than a null-pointer panic. Startup code backfills `listing_id` for any existing options.
