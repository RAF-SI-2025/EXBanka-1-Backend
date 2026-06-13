# Celina 3 — Securities / Exchange Trading — Test Cases

> Scope: **Trgovina na berzi** (secondary-market securities trading).
> Covers spec §6 "celina-3-securities": exchanges & working hours, listings
> (stocks / forex / futures / options) + market-data reads, all order types ×
> direction × execution-modifiers with the documented pricing & commission
> formulas, agent limits & supervisor approval, the order-review & my-orders
> portals, portfolio (holdings, P/L, sell, make-public, dividend history,
> option exercise), dividends, capital-gains tax, recurring orders (DCA),
> watchlist, and price alerts.
>
> Sources of truth: `docs/bank-requirements/Celina 3 2026.docx.md`;
> `docs/api/REST_API_v3.md` §25–33, §40, §42, §43, §45, §51;
> `docs/Specification.md` §17/§18/§20/§21; api-gateway handlers
> (`stock_order_handler.go`, `securities_handler.go`, `stock_exchange_handler.go`,
> `portfolio_handler.go`, `unified_portfolio_handler.go`, `tax_handler.go`,
> `actuary_handler.go`, `dividend_handler.go`, `recurring_order_handler.go`,
> `price_alert_handler.go`, `watchlist_handler.go`, `options_v2_handler.go`);
> stock-service `order_service.go` / `exchange_hours.go` / `listing_derived.go`;
> `Banka 2025 - E2E testovi` ("Trgovina hartijama sa berze") +
> `Banka 2025 - odbrana flow` ("3 - Trgovanje na berzi").

## Conventions used in this file

- Template per `docs/superpowers/specs/2026-06-07-comprehensive-test-plan-design.md` §4.
- ID scheme `TC-C3-<AREA>-<nnn>`; actor variants get `a/b/c` suffixes.
- Standard error codes: `validation_error`/400, `unauthorized`/401,
  `forbidden`/403, `not_found`/404, `conflict`/409, `business_rule_violation`/409,
  `rate_limited`/429, `internal_error`/500.
- `verification.skip` (supervisor/admin) bypasses TOTP; client self-service money
  moves take the full flow (→ `cross-cutting-verification.md`). Securities order
  placement in this codebase does **not** gate on the verification-challenge flow
  (no challenge is minted on `POST /me/orders`), so each order TC's Verification
  line is `n/a` unless noted.
- Base URL `http://localhost:8080`, all routes `/api/v3`.
- "Bank-funded fast path": flip `testing_mode` on (`POST /stock-exchanges/testing-mode {"enabled":true}`)
  so orders fill immediately and the after-hours/closed slow-fill paths are
  short-circuited; fund the RSD sentinel for employee/bank orders.

---

## Implementation-state callouts (read before executing)

These are the places where the requirement text and the shipped backend diverge;
each is tested against the **implemented** behavior and surfaced as a matrix row.

1. **Exchange-closed rejection.** The E2E doc + spec scope say a `Market` order
   placed while the exchange is closed must be **rejected** with `"Berza je
   zatvorena"`. The backend does **not** reject — it accepts the order and the
   fill engine simply does not fill (or fills slowly) until the exchange reopens.
   No `"Berza je zatvorena"` error string exists server-side. Covered as
   `NO-ENDPOINT` (TC-C3-EXC-030).
2. **Client visibility restriction.** Spec: clients may see **only stocks +
   futures**; forex/options must be hidden/forbidden. The backend serves every
   `/securities/*` route under `AnyAuthMiddleware` with **no** principal-type
   gate, so a client token gets 200 on `/securities/forex` and `/securities/options`.
   UI-only restriction → `NO-ENDPOINT` gap (TC-C3-VIS-002/003).
3. **Approval gate is narrower than the spec.** Spec lists 3 independent triggers
   (NeedApproval flag OR used-limit exhausted OR order exceeds limit). The code
   (`order_service.go` finalize step) requires the **conjunction**
   `NeedApproval==true AND Limit>0 AND used+orderRSD>Limit`. So an agent with
   `NeedApproval=false` is auto-approved even when over limit. Tested against the
   implemented conjunction (TC-C3-APV-001..006) and flagged.
4. **Margin prerequisites not enforced.** `margin=true` is stored on the order
   but the backend does **not** verify the actuary/employee permission, an
   approved credit > Initial Margin Cost, or cash > IMC. `Initial Margin Cost =
   Maintenance Margin × 1.1` is computed only for *display* (`listing_derived.go`).
   The "reject Margin order when neither credit nor cash ≥ IMC" rule is
   `NO-ENDPOINT` (TC-C3-MGN-001..004).
5. **Tax extras.** PDF export, report-by-year filter, and profit-discrepancy
   flagging (all in the E2E doc) have **no endpoint** (TC-C3-TAX-040..042).
6. **Dividend formula.** Spec formula is `qty × price × yield/4`, quarterly,
   auto on the last working day of Mar/Jun/Sep/Dec. The backend instead exposes
   an **admin-declared** `amount_per_share_rsd` + a manual `payout` fan-out
   (`POST /admin/dividends` → `POST /admin/dividends/:id/payout`); the 15% client
   / 0% bank withholding IS implemented. The automatic quarterly cron + the exact
   yield/4 formula are not endpoint-exposed (TC-C3-DIV-030).

---

## 1. Exchanges & working hours (AREA = EXC)

`GET /stock-exchanges`, `GET /stock-exchanges/:id` (AnyAuth);
`POST /stock-exchanges/testing-mode`, `GET /stock-exchanges/testing-mode`
(employee + `exchanges.manage`).

#### TC-C3-EXC-001 · List exchanges (POSITIVE)
- **Feature:** Pregled berzi (exchange list) · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_ListExchanges
- **Actor:** client (any JWT)
- **Preconditions:** seeder loaded ≥1 exchange.
- **Request:** `GET /api/v3/stock-exchanges?page=1&page_size=10`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · body `{ "exchanges":[…], "total_count":N }`; each item carries name/acronym/MIC/polity/currency/timezone/open_time/close_time.
- **Negative siblings:** unauthenticated → 401 (TC-C3-EXC-002).

#### TC-C3-EXC-002 · List exchanges unauthenticated (NEGATIVE)
- **Feature:** Pregled berzi · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_ListExchanges_Unauthenticated
- **Actor:** unauthenticated
- **Preconditions:** —
- **Request:** `GET /api/v3/stock-exchanges`
  - Auth: none
- **Verification:** n/a
- **Expected:** `401` · `error.code=unauthorized`.

#### TC-C3-EXC-003 · Search/filter exchanges (POSITIVE)
- **Feature:** Filtriranje berzi · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_ListExchanges_SearchFilter
- **Actor:** client
- **Preconditions:** ≥2 exchanges with distinct names.
- **Request:** `GET /api/v3/stock-exchanges?search=NYSE`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · only matching exchanges returned; `total_count` reflects the filtered set.

#### TC-C3-EXC-010 · Get exchange by id (POSITIVE)
- **Feature:** Detalj berze · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_GetExchange
- **Actor:** client
- **Preconditions:** known exchange id.
- **Request:** `GET /api/v3/stock-exchanges/:id`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · single exchange object with currency + working hours fields.
- **Negative siblings:** unknown id → 404 not_found (TC-C3-EXC-011); non-numeric id → 400 validation_error.

#### TC-C3-EXC-011 · Get exchange not found (NEGATIVE)
- **Feature:** Detalj berze · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_GetExchange_NotFound
- **Actor:** client
- **Preconditions:** —
- **Request:** `GET /api/v3/stock-exchanges/99999999`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `404` · `error.code=not_found`.

#### TC-C3-EXC-020 · Toggle testing-mode on then read (POSITIVE)
- **Feature:** Dugme uključi/isključi vreme berze · **Spec:** Celina 3 §Exchanges "dugme koje uključuje/isključuje vreme berze" · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_TestingMode_SetAndGet
- **Actor:** supervisor (or admin) with `exchanges.manage`
- **Preconditions:** supervisor token.
- **Request:** `POST /api/v3/stock-exchanges/testing-mode`
  - Auth: `Bearer <supervisor token>`
  - Body: `{ "enabled": true }`
- **Verification:** fast-path
- **Expected:** `200` · `{ "testing_mode": true }`; follow-up `GET /api/v3/stock-exchanges/testing-mode` → `200 {"testing_mode":true}`. Side-effect: orders placed afterwards skip after-hours/closed slow-fill (TC-C3-ORD fills become immediate).
- **Negative siblings:** missing `enabled` → 400; agent/client token → 403 (TC-C3-EXC-021).

#### TC-C3-EXC-021 · Toggle testing-mode requires exchanges.manage (NEGATIVE)
- **Feature:** Dugme uključi/isključi vreme berze · **Spec:** Celina 3 §Exchanges · **Existing test:** test-app/workflows/stock_exchange_test.go::TestStockExchange_TestingMode_RequiresSupervisor
- **Actor:** agent (lacks `exchanges.manage`) / client
- **Preconditions:** —
- **Request:** `POST /api/v3/stock-exchanges/testing-mode` Body `{ "enabled": true }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `403` · `error.code=forbidden`. Client token also 403.

#### TC-C3-EXC-030 · Order placed while exchange CLOSED is not server-rejected (NEGATIVE / NO-ENDPOINT)
- **Feature:** "Berza je zatvorena" rejection · **Spec:** E2E "Nalog odbijen van radnog vremena berze"; Celina 3 §Create Orders · **Existing test:** — (placement-only asserts in test-app/workflows/wf_full_day_test.go / wf_cross_currency_test.go)
- **Actor:** agent / client
- **Preconditions:** `testing_mode=false`; chosen listing's exchange currently outside trading hours (wall-clock).
- **Request:** `POST /api/v3/me/orders` Body market buy (see TC-C3-ORD-001)
  - Auth: `Bearer <token>`
- **Verification:** n/a
- **Expected (implemented):** `201` — order created `status=pending/approved`, **not** filled (no `OrderTransaction`, `is_done=false`) until the exchange reopens. No `"Berza je zatvorena"` error is returned. **Documented gap:** the spec/E2E rejection-with-message is not implemented (NO-ENDPOINT).

#### TC-C3-EXC-031 · After-hours order flagged + slow fill (POSITIVE)
- **Feature:** After-hours sporo izvršavanje (<4h to close) · **Spec:** Celina 3 §Create Orders "after-hours … sporije … 30 min" + Order.AfterHours · **Existing test:** —
- **Actor:** agent
- **Preconditions:** `testing_mode=false`; wall-clock within 240 min after the listing exchange's close time.
- **Request:** `POST /api/v3/me/orders` market buy.
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201` · order field `after_hours=true`; fill cadence is slower (≈+30 min per portion) so `is_done` flips later than for an in-hours order. Side-effect: `AfterHours` persisted on the Order row.
- **Negative siblings:** in-hours placement → `after_hours=false` (immediate-ish fill); with `testing_mode=true` after-hours is forced off (`after_hours=false`).

---

## 2. Listings & market data (AREA = LST)

All under `/api/v3/securities/*` (AnyAuth). Stocks, futures, forex pairs,
options chain, candles, daily-price history.

#### TC-C3-LST-001 · List stocks (POSITIVE)
- **Feature:** Listing akcija · **Spec:** Celina 3 §Stocks · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListStocks
- **Actor:** client
- **Preconditions:** seeded stocks.
- **Request:** `GET /api/v3/securities/stocks?page=1&page_size=10`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · `{ "stocks":[…], "total_count":N }`; items carry ticker, price, change, volume, ask/high, outstanding_shares, dividend_yield, maintenance_margin, initial_margin_cost (= MM×1.1).
- **Negative siblings:** unauthenticated → 401 (TC-C3-LST-002).

#### TC-C3-LST-002 · List stocks unauthenticated (NEGATIVE)
- **Feature:** Listing akcija · **Spec:** Celina 3 §Stocks · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListStocks_Unauthenticated
- **Actor:** unauthenticated
- **Request:** `GET /api/v3/securities/stocks` · Auth: none
- **Expected:** `401` · `error.code=unauthorized`.

#### TC-C3-LST-003 · Search stocks by ticker (POSITIVE)
- **Feature:** Pretraga po tickeru/nazivu · **Spec:** Celina 3 §Portal Hartije "Pretraga" · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListStocks_SearchByTicker
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks?search=AAPL` · Auth: `Bearer <client token>`
- **Expected:** `200` · only matching ticker/name rows.

#### TC-C3-LST-004 · Sort stocks by price (POSITIVE) + invalid sort (NEGATIVE)
- **Feature:** Sortiranje (price/volume/change/margin) · **Spec:** Celina 3 §Portal Hartije "Sortiranje" · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListStocks_SortByPrice / TestSecurities_ListStocks_InvalidSortBy
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks?sort_by=price&sort_order=desc` · Auth: `Bearer <client token>`
- **Expected:** `200` · rows ordered by price desc.
- **Negative siblings:** `sort_by=bogus` → 400 validation_error; `sort_order=sideways` → 400.

#### TC-C3-LST-005 · Filter stocks by price/volume range + exchange (POSITIVE)
- **Feature:** Filtriranje (Exchange prefix, Price/Ask/Bid/Volume range) · **Spec:** Celina 3 §Portal Hartije "Filtriranje" · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks?exchange_acronym=NYSE&min_price=10&max_price=500&min_volume=1000` · Auth: `Bearer <client token>`
- **Expected:** `200` · only rows inside the price/volume window on the named exchange.
- **Negative siblings:** `min_price` > `max_price` → empty set (200, total_count=0).

#### TC-C3-LST-010 · Get stock detail (POSITIVE)
- **Feature:** Detaljan prikaz akcije · **Spec:** Celina 3 §Prikaz akcije · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetStock
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks/:id` · Auth: `Bearer <client token>`
- **Expected:** `200` · stock object incl. derived market_cap, maintenance_margin, initial_margin_cost.
- **Negative siblings:** unknown id → 404; non-numeric → 400.

#### TC-C3-LST-011 · Stock price history per period (POSITIVE) + invalid period (NEGATIVE)
- **Feature:** Grafik/istorija cene (dan/nedelja/mesec/godina/5y/all) · **Spec:** Celina 3 §Detaljan prikaz "dan, nedelja, mesec, godina, 5 godina, od početka" · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetStockHistory / TestSecurities_GetStockHistory_InvalidPeriod
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks/:id/history?period=year` (also test `day`,`week`,`month`,`5y`,`all`) · Auth: `Bearer <client token>`
- **Expected:** `200` · `{ "history":[…], "total_count":N }`; each row has date, price, ask/high, bid/low, change, volume.
- **Negative siblings:** `period=decade` → 400 validation_error.

#### TC-C3-LST-020 · List futures + settlement-date filter (POSITIVE)
- **Feature:** Listing futures (month codes / settlement) · **Spec:** Celina 3 §Futures · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListFutures / TestSecurities_ListFutures_SettlementDateFilter
- **Actor:** client
- **Request:** `GET /api/v3/securities/futures?settlement_date_from=2026-01-01&settlement_date_to=2026-12-31` · Auth: `Bearer <client token>`
- **Expected:** `200` · `{ "futures":[…] }`; items carry contract_size, contract_unit, settlement_date, ticker with month-code (e.g. `CLJ22`), maintenance_margin = ContractSize×Price×10%.
- **Negative siblings:** unknown future id → 404 (TC-C3-LST-021 / TestSecurities_GetFutures_NotFound).

#### TC-C3-LST-021 · Get futures detail + history (POSITIVE / NEGATIVE)
- **Feature:** Futures detalj + istorija · **Spec:** Celina 3 §Futures · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetFutures / TestSecurities_GetFutures_NotFound / TestSecurities_GetFuturesHistory
- **Actor:** client
- **Request:** `GET /api/v3/securities/futures/:id` then `…/:id/history?period=month` · Auth: `Bearer <client token>`
- **Expected:** `200` futures object + `200` history page.
- **Negative siblings:** unknown id → 404.

#### TC-C3-LST-030 · List forex pairs + liquidity filter (POSITIVE) + invalid liquidity (NEGATIVE)
- **Feature:** Listing forex parova (base/quote/rate/liquidity) · **Spec:** Celina 3 §Forex pairs · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListForexPairs / TestSecurities_ListForexPairs_LiquidityFilter / TestSecurities_ListForexPairs_InvalidLiquidity
- **Actor:** agent (actuary)
- **Request:** `GET /api/v3/securities/forex?liquidity=high&base_currency=EUR&quote_currency=USD` · Auth: `Bearer <agent token>`
- **Expected:** `200` · `{ "forex_pairs":[…] }`; items carry base_currency, quote_currency, price (rate), liquidity, contract_size=1000, maintenance_margin = CS×Price×10%, nominal_value.
- **Negative siblings:** `liquidity=ultra` → 400 validation_error.

#### TC-C3-LST-031 · Get forex pair detail + history (POSITIVE / NEGATIVE)
- **Feature:** Forex detalj/istorija · **Spec:** Celina 3 §Forex pairs · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetForexPair / TestSecurities_GetForexPair_NotFound / TestSecurities_GetForexPairHistory
- **Actor:** agent
- **Request:** `GET /api/v3/securities/forex/:id` then `…/history?period=week` · Auth: `Bearer <agent token>`
- **Expected:** `200` pair object + history. Unknown id → 404.

#### TC-C3-LST-040 · Options chain requires stock_id (POSITIVE / NEGATIVE)
- **Feature:** Options chain (CALLS/PUTS by strike) · **Spec:** Celina 3 §Options + §Prikaz akcije · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListOptions_RequiresStockID / TestSecurities_ListOptions_WithStockID / TestSecurities_ListOptions_FilterByType
- **Actor:** agent
- **Request:** `GET /api/v3/securities/options?stock_id=42&option_type=call&min_strike=100&max_strike=200` · Auth: `Bearer <agent token>`
- **Expected:** `200` · `{ "options":[…] }`; items carry stock_listing, option_type (call/put), strike_price, implied_volatility, open_interest, settlement_date, contract_size=100, maintenance_margin=CS×50%×stockPrice.
- **Negative siblings:** missing `stock_id` → 400 validation_error ("stock_id query parameter is required"); `option_type=straddle` → 400.

#### TC-C3-LST-041 · Get single option (POSITIVE / NEGATIVE)
- **Feature:** Option detalj · **Spec:** Celina 3 §Options · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetOption / TestSecurities_GetOption_NotFound
- **Actor:** agent
- **Request:** `GET /api/v3/securities/options/:id` · Auth: `Bearer <agent token>`
- **Expected:** `200` option object incl. ticker format `MSFT220404C00180000`. Unknown id → 404.

#### TC-C3-LST-050 · Candle OHLCV chart (POSITIVE / NEGATIVE)
- **Feature:** Grafik (candles) iz time-series · **Spec:** Celina 3 §Detaljan prikaz "grafik" · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/securities/candles?listing_id=42&interval=1h&from=2026-04-01T00:00:00Z&to=2026-04-02T00:00:00Z` · Auth: `Bearer <client token>`
- **Expected:** `200` · `{ "candles":[{time,open,high,low,close,volume}], "count":N }`.
- **Negative siblings:** missing `listing_id`/`from`/`to` → 400 validation_error; bad `interval` → 400; unauthenticated → 401.

---

## 3. Client visibility restriction (AREA = VIS)

#### TC-C3-VIS-001 · Client can view stocks + futures (POSITIVE)
- **Feature:** Klijent vidi akcije + futures · **Spec:** Celina 3 §Portal Hartije "Klijenti mogu videti samo akcije i futures-e" · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ClientCanViewStocksAndFutures
- **Actor:** client
- **Preconditions:** activated trading client.
- **Request:** `GET /api/v3/securities/stocks` then `GET /api/v3/securities/futures` · Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** both `200`.

#### TC-C3-VIS-002 · Client viewing forex is NOT blocked (NEGATIVE / NO-ENDPOINT)
- **Feature:** Sakrij forex od klijenta · **Spec:** Celina 3 §Portal Hartije (forex hidden from clients) · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/securities/forex` · Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected (implemented):** `200` — the backend does **not** restrict forex visibility by principal type. **Documented gap:** spec wants 403/hidden for clients; not enforced server-side (UI-only). NO-ENDPOINT.

#### TC-C3-VIS-003 · Client viewing options is NOT blocked (NEGATIVE / NO-ENDPOINT)
- **Feature:** Sakrij opcije od klijenta · **Spec:** Celina 3 §Portal Hartije (options hidden from clients) · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/securities/options?stock_id=42` · Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected (implemented):** `200`. **Documented gap:** spec wants forbidden/hidden for clients. NO-ENDPOINT.

---

## 4. Orders — types × direction × pricing/commission (AREA = ORD)

`POST /api/v3/me/orders` (AnyAuth, owner from JWT). Pricing/commission per
`order_service.go`:
- **Market buy** fill price = `ContractSize × Ask`; **market sell** = `ContractSize × Bid`.
- **Limit buy** = `ContractSize × min(limit, Ask)` (fills only when Ask ≤ limit);
  **limit sell** = `ContractSize × max(limit, Bid)` (fills only when Bid ≥ limit).
- **Stop** → becomes Market on trigger (buy: Ask ≥ stop; sell: Bid ≤ stop).
- **Stop-limit** → becomes Limit(limit) on trigger.
- **Commission** = Market/Stop `min(14% × approxPrice, $7)`; Limit/Stop-limit
  `min(24% × approxPrice, $12)`; `approxPrice = ContractSize × PricePerUnit × Quantity`;
  credited to the bank account in the order's currency (best-effort post-saga —
  a commission-credit failure does **not** roll back the trade).

#### TC-C3-ORD-001 · Market BUY (default type) executes at Ask (POSITIVE)
- **Feature:** Market Order kupovina · **Spec:** Celina 3 §Market Order; E2E "Klijent kupuje akcije po tržišnoj ceni" · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateMarketBuyOrder ; test-app/workflows/wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle
- **Actor:** client (trading-enabled)
- **Preconditions:** funded investment account (≥ ask×qty×contract + commission); `testing_mode=true` for fast fill.
- **Request:** `POST /api/v3/me/orders`
  - Auth: `Bearer <client token>`
  - Body: `{ "listing_id":42, "direction":"buy", "order_type":"market", "quantity":5, "account_id":1 }`
- **Verification:** n/a
- **Expected:** `201` order `status=approved` (client auto-approve, `approved_by="no need for approval"`); on fill: holding +5, account debited `5×ContractSize×Ask + commission`, commission `min(14%×approx,$7)` credited to bank, `is_done=true`, ≥1 `order_transaction`. Reservation released as it settles.
- **Negative siblings:** zero qty → 400 (TC-C3-ORD-005); missing account_id → 400 (TC-C3-ORD-006); insufficient funds → 409 business_rule_violation.

#### TC-C3-ORD-002 · Market SELL from portfolio at Bid (POSITIVE)
- **Feature:** Market Order prodaja · **Spec:** Celina 3 §Market Order + §Moj portfolio "prodaj" · **Existing test:** test-app/workflows/wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle ; test-app/workflows/wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding
- **Actor:** client
- **Preconditions:** holding of the security with quantity ≥ sell qty.
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"sell", "order_type":"market", "quantity":3, "account_id":1 }`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `201`; on fill: holding −3, account credited `3×ContractSize×Bid − commission` (proceeds destination = account_id), commission to bank, realized P/L recorded for tax. Holding reservation locks the 3 units until fill.
- **Negative siblings:** sell qty > held → 409 business_rule_violation (insufficient holding); sell with no account_id → 400.

#### TC-C3-ORD-003 · Limit BUY fills only at favorable Ask (POSITIVE)
- **Feature:** Limit Order kupovina (povoljnija cena) · **Spec:** Celina 3 §Limit Order; E2E "Limit BUY se izvršava samo po povoljnoj ceni" · **Existing test:** test-app/workflows/wf_order_types_test.go::TestWF_MultiAssetOrderTypes
- **Actor:** agent
- **Preconditions:** bank RSD account funded; listing Ask currently > limit.
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"buy", "order_type":"limit", "quantity":10, "limit_value":"98.00", "account_id":<bank> }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201` order pending fill; does NOT fill while Ask > 98; fills at `ContractSize × min(98, Ask)` once Ask ≤ 98; commission `min(24%×approx,$12)`. Limit orders are visible/listable.
- **Negative siblings:** missing `limit_value` → 400 (TC-C3-ORD-007).

#### TC-C3-ORD-004 · Limit SELL fills only when Bid ≥ limit (POSITIVE)
- **Feature:** Sell Limit Order · **Spec:** Celina 3 §Limit Order (sell min price) · **Existing test:** test-app/workflows/wf_order_types_test.go::TestWF_MultiAssetOrderTypes
- **Actor:** agent
- **Preconditions:** holding present; Bid currently < limit.
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"sell", "order_type":"limit", "quantity":5, "limit_value":"150.00", "account_id":<bank> }`
  - Auth: `Bearer <agent token>`
- **Expected:** `201`; no fill while Bid < 150; fills at `ContractSize × max(150, Bid)` once Bid ≥ 150.
- **Negative siblings:** unfilled while price never reached → stays not-done; cancellable (TC-C3-MGMT-030).

#### TC-C3-ORD-005 · Zero quantity rejected (NEGATIVE)
- **Feature:** Validacija količine · **Spec:** Celina 3 §Order entitet · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateOrder_ZeroQuantity
- **Actor:** client
- **Request:** `POST /api/v3/me/orders` Body `{…"quantity":0…}` · Auth: `Bearer <client token>`
- **Expected:** `400` · `validation_error` "quantity must be positive".

#### TC-C3-ORD-006 · Buy without account_id rejected (NEGATIVE)
- **Feature:** Izbor računa za kupovinu · **Spec:** Celina 3 §Create Orders "navodi sa kog računa" · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateBuyOrder_RequiresAccountID
- **Actor:** client
- **Request:** `POST /api/v3/me/orders` Body buy without `account_id` · Auth: `Bearer <client token>`
- **Expected:** `400` · `validation_error` "account_id is required for buy orders".

#### TC-C3-ORD-007 · Limit order without limit_value rejected (NEGATIVE)
- **Feature:** Validacija limit naloga · **Spec:** Celina 3 §Limit Order · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateLimitOrder_RequiresLimitValue
- **Actor:** client
- **Request:** `POST /api/v3/me/orders` Body `{…"order_type":"limit"…}` (no `limit_value`) · Auth: `Bearer <client token>`
- **Expected:** `400` · `validation_error` "limit_value is required for limit/stop_limit orders".

#### TC-C3-ORD-008 · Invalid direction / order_type rejected (NEGATIVE)
- **Feature:** Enum validacija · **Spec:** Celina 3 §Order entitet · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateOrder_InvalidDirection / TestOrder_CreateOrder_InvalidOrderType
- **Actor:** client
- **Request:** `POST /api/v3/me/orders` Body `{…"direction":"hold"…}` then `{…"order_type":"trailing"…}` · Auth: `Bearer <client token>`
- **Expected:** `400` · `validation_error` (oneOf) on each.

#### TC-C3-ORD-009 · Unauthenticated order rejected (NEGATIVE)
- **Feature:** Auth na nalogu · **Spec:** Celina 3 §Order · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CreateOrder_Unauthenticated
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/me/orders` Body market buy · Auth: none
- **Expected:** `401` · `unauthorized`.

#### TC-C3-ORD-010 · Stop order → market on trigger (POSITIVE)
- **Feature:** Stop (Stop-Loss) Order · **Spec:** Celina 3 §Stop Order · **Existing test:** test-app/workflows/wf_order_types_test.go::TestWF_MultiAssetOrderTypes
- **Actor:** agent
- **Preconditions:** stop value not yet hit.
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"buy", "order_type":"stop", "quantity":4, "stop_value":"120.00", "account_id":<bank> }`
  - Auth: `Bearer <agent token>`
- **Expected:** `201`; remains inactive until Ask ≥ 120 (buy) / Bid ≤ 120 (sell), then executes as a Market order at current Ask/Bid; commission uses Market schedule `min(14%,$7)`. approxPrice uses `stop_value` as PricePerUnit.
- **Negative siblings:** missing `stop_value` → 400 "stop_value is required for stop/stop_limit orders".

#### TC-C3-ORD-011 · Stop-Limit two-stage activation (POSITIVE)
- **Feature:** Stop-Limit Order · **Spec:** Celina 3 §Stop-Limit Order; E2E "Stop-Limit kreira Limit kada se dostigne stop" · **Existing test:** test-app/workflows/wf_stop_limit_refund_test.go::TestWF_StopLimit_ExpiryReleasesReservation
- **Actor:** agent
- **Preconditions:** stop not yet hit; bank account funded; reservation taken at placement.
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"buy", "order_type":"stop_limit", "quantity":6, "stop_value":"100.00", "limit_value":"98.00", "account_id":<bank> }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201`; when Ask reaches stop (100) the order converts to a Buy Limit @98 and fills only at `ContractSize × min(98,Ask)`; commission uses Limit schedule `min(24%,$12)`. If it expires unfilled, the funds reservation is released (assert reserved balance restored).
- **Negative siblings:** missing either `stop_value` or `limit_value` → 400.

#### TC-C3-ORD-012 · Forex buy converts + credits base account (POSITIVE)
- **Feature:** Forex kupovina (konverzija, bez provizije menjačnice) · **Spec:** Celina 3 §Forex pairs; E2E "Kupovina forex-a uz konverziju" + defense Provera 2 · **Existing test:** test-app/workflows/wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit
- **Actor:** agent
- **Preconditions:** quote-currency account (debited) + base-currency account (credited), both bank-owned.
- **Request:** `POST /api/v3/me/orders` Body `{ "security_type":"forex", "listing_id":501, "direction":"buy", "order_type":"market", "quantity":1000, "account_id":<quote>, "base_account_id":<base> }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201`; on fill quote account debited (in quote ccy, FX-converted if account ≠ quote ccy), base account credited the base-currency amount, security commission to bank (no menjačnica commission on the FX leg). Defense: money leaves *from* ccy, arrives in *to* ccy.
- **Negative siblings:** TC-C3-ORD-013..016.

#### TC-C3-ORD-013 · Forex must be buy (NEGATIVE)
- **Feature:** Forex smer · **Spec:** Celina 3 §Forex; REST §27 "forex orders MUST be buy" · **Existing test:** —
- **Actor:** agent
- **Request:** `POST /api/v3/me/orders` Body `{ "security_type":"forex","direction":"sell",…,"base_account_id":7 }` · Auth: `Bearer <agent token>`
- **Expected:** `400` · `validation_error` "forex orders must be direction=buy".

#### TC-C3-ORD-014 · Forex requires base_account_id (NEGATIVE)
- **Feature:** Forex base account · **Spec:** REST §27 · **Existing test:** —
- **Actor:** agent
- **Request:** `POST /api/v3/me/orders` Body `{ "security_type":"forex","direction":"buy",… }` (no `base_account_id`) · Auth: `Bearer <agent token>`
- **Expected:** `400` · `validation_error` "forex orders require base_account_id".

#### TC-C3-ORD-015 · base_account_id must differ from account_id (NEGATIVE)
- **Feature:** Forex računi različiti · **Spec:** REST §27 · **Existing test:** —
- **Actor:** agent
- **Request:** `POST /api/v3/me/orders` Body forex buy with `base_account_id == account_id` · Auth: `Bearer <agent token>`
- **Expected:** `400` · `validation_error` "base_account_id must differ from account_id".

#### TC-C3-ORD-016 · Order on account not owned by caller (NEGATIVE / ownership)
- **Feature:** Vlasništvo nad računom · **Spec:** Resource-Ownership requirement · **Existing test:** —
- **Actor:** client A
- **Preconditions:** `account_id` belongs to client B.
- **Request:** `POST /api/v3/me/orders` Body buy with B's `account_id` · Auth: `Bearer <client A token>`
- **Expected:** `403` · `forbidden` "account does not belong to you". Same for a non-owned `base_account_id`.

#### TC-C3-ORD-017 · Client order is auto-approved (POSITIVE)
- **Feature:** Klijentovi orderi automatski Approved · **Spec:** Celina 3 §Order "klijent … automatski Approved" · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_ClientOrderAutoApproved
- **Actor:** client
- **Request:** `POST /api/v3/me/orders` market buy · Auth: `Bearer <client token>`
- **Expected:** `201` · `status=approved`, `approved_by="no need for approval"`.

#### TC-C3-ORD-020 · Order on behalf of a client (POSITIVE)
- **Feature:** Agent postavlja order u ime klijenta · **Spec:** Celina 3 §Create Orders; REST §27 `POST /orders` · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow
- **Actor:** employee-on-behalf (`orders.place.on_behalf_client`)
- **Preconditions:** `account_id` owned by `client_id`.
- **Request:** `POST /api/v3/orders` Body `{ "client_id":5, "account_id":12, "listing_id":42, "direction":"buy", "order_type":"market", "quantity":10 }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201`; order owner = client 5, `acting_employee_id` = caller; holding lands on client 5; ApprovedBy blank until human approve (employee-on-behalf path).
- **Negative siblings:** account not owned by client_id → 403 "account does not belong to client"; neither/both of client_id & on_behalf_of_fund_id → 400; missing `orders.place.*` permission → 403.

#### TC-C3-ORD-021 · Order on behalf of an investment fund (POSITIVE)
- **Feature:** Order u ime fonda · **Spec:** REST §27 `POST /orders` (fund branch) · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** employee fund-manager
- **Preconditions:** caller manages fund 9; `account_id` = fund's RSD account.
- **Request:** `POST /api/v3/orders` Body `{ "on_behalf_of_fund_id":9, "account_id":100, "listing_id":42, "direction":"buy", "order_type":"market", "quantity":10 }`
  - Auth: `Bearer <fund-manager token>`
- **Expected:** `201`; fill lands in `fund_holdings`; bank-owned order.
- **Negative siblings:** acting employee not the fund manager → 403 `fund_not_managed_by_actor`; account ≠ fund RSD account → 400.

#### TC-C3-ORD-030 · Concurrent orders respect available balance (NEGATIVE / concurrency)
- **Feature:** Rezervacija sredstava · **Spec:** Concurrency/reservation safety · **Existing test:** test-app/workflows/wf_stock_concurrent_orders_test.go::TestWF_StockConcurrentOrders_RespectsAvailableBalance ; test-app/workflows/wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation
- **Actor:** agent
- **Preconditions:** account funds cover only ONE of two simultaneous buys.
- **Request:** two parallel `POST /api/v3/me/orders` buys, each needing > 50% of available.
- **Expected:** exactly one succeeds; the other → 409 business_rule_violation (insufficient available). Cancel of an unfilled buy releases its reservation (available restored).

#### TC-C3-ORD-031 · Partial multi-trader fill aggregation (POSITIVE)
- **Feature:** Delimično prikupljanje od više trgovaca · **Spec:** Celina 3 §Create Orders "Sadržaj Order-a … od različitih trgovca" · **Existing test:** test-app/workflows/wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle (multi-portion) ; helpers waitForOrderFill
- **Actor:** agent
- **Request:** market buy qty=10 (filled in portions 1..n).
- **Expected:** order accrues multiple `order_transactions` summing to 10; `remaining_portions` decreases to 0; `is_done=true` only after the full quantity fills.

#### TC-C3-ORD-040 · Commission-credit failure does not abort trade (NEGATIVE / resilience)
- **Feature:** Provizija best-effort · **Spec:** Celina 3 §Market/Limit "Provizija se prebacuje na bankin račun" + Kafka-after-commit · **Existing test:** test-app/workflows/wf_stock_commission_failure_test.go::TestWF_StockFill_CommissionFailure_TradeStillCompletes
- **Actor:** agent
- **Preconditions:** bank commission account unreachable / errors.
- **Request:** market buy that fills.
- **Expected:** trade still completes (holding credited, account debited, `is_done=true`); commission credit is skipped (logged), not rolled back.

#### TC-C3-ORD-041 · Account-service fill failure: no divergence (NEGATIVE / saga)
- **Feature:** Saga konzistentnost na fill grešci · **Spec:** §22 Saga · **Existing test:** test-app/workflows/wf_stock_fill_failure_test.go::TestWF_StockFill_AccountServiceFailure_NoDivergence
- **Actor:** agent
- **Request:** order whose settle step fails at account-service.
- **Expected:** no money/holding divergence — either both ledger+holding move or neither; saga compensation keeps invariants.

---

## 5. Execution modifier — All-or-None (AREA = AON)

#### TC-C3-AON-001 · AON blocks partial fill (POSITIVE)
- **Feature:** All-or-None nalog · **Spec:** Celina 3 §AON; E2E "AON zastavica blokira delimičnu realizaciju" · **Existing test:** test-app/workflows/wf_order_types_test.go::TestWF_MultiAssetOrderTypes (all_or_none branch)
- **Actor:** agent
- **Preconditions:** market liquidity < requested quantity (only partial available).
- **Request:** `POST /api/v3/me/orders` Body `{ "listing_id":42, "direction":"sell", "order_type":"market", "quantity":1000, "all_or_none":true, "account_id":<bank> }`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201`; order does **not** execute partially — stays pending/not-done with no `order_transaction` until the full quantity can fill in one shot. (E2E: status "Na čekanju".)
- **Negative siblings:** same order with `all_or_none=false` fills partially (multiple transactions) — contrast case.

#### TC-C3-AON-002 · AON fully fills when full quantity available (POSITIVE)
- **Feature:** AON kompletno izvršenje · **Spec:** Celina 3 §AON · **Existing test:** —
- **Actor:** agent
- **Preconditions:** liquidity ≥ requested quantity; `testing_mode=true`.
- **Request:** AON market buy qty=5.
- **Expected:** fills in a single complete fill (or completes only once whole), holding +5, `is_done=true`.

---

## 6. Execution modifier — Margin (AREA = MGN)

#### TC-C3-MGN-001 · Margin flag persisted on order (POSITIVE)
- **Feature:** Margin nalog · **Spec:** Celina 3 §Margin · **Existing test:** test-app/workflows/wf_order_types_test.go::TestWF_MultiAssetOrderTypes (margin branch)
- **Actor:** agent
- **Request:** `POST /api/v3/me/orders` Body `{ …, "margin":true, "account_id":<bank> }` · Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201` · order field `margin=true` recorded.

#### TC-C3-MGN-002 · Margin permission prerequisite (NEGATIVE / NO-ENDPOINT)
- **Feature:** Margin permisija · **Spec:** Celina 3 §Margin "Zaposleni mora imati permisiju; Klijent sa odobrenim kreditom dobija je" · **Existing test:** —
- **Actor:** client/agent without margin permission
- **Request:** margin order.
- **Expected (implemented):** order accepted regardless of any margin permission — the backend does **not** gate on a margin permission. **Documented gap:** NO-ENDPOINT.

#### TC-C3-MGN-003 · Margin credit/cash ≥ Initial Margin Cost prerequisite (NEGATIVE / NO-ENDPOINT)
- **Feature:** Margin uslov (kredit ili sredstva > IMC) · **Spec:** Celina 3 §Margin "Ako jedan od dva uslova … nije zadovoljen, Margin Order neće biti prihvaćen"; `IMC = MaintenanceMargin × 1.1` · **Existing test:** —
- **Actor:** client/agent with neither approved credit > IMC nor cash > IMC
- **Request:** margin order.
- **Expected (implemented):** order accepted (no IMC check). **Documented gap:** the "reject if neither credit nor cash ≥ IMC" rule is not enforced server-side; IMC is computed only for display (`listing_derived.go`). NO-ENDPOINT.

#### TC-C3-MGN-004 · Initial Margin Cost display value (POSITIVE)
- **Feature:** Initial Margin Cost prikaz · **Spec:** Celina 3 §Izvedeni podaci "IMC = MM × 1.1" · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_GetStock (initial_margin_cost field)
- **Actor:** client
- **Request:** `GET /api/v3/securities/stocks/:id`
- **Expected:** `200`; `initial_margin_cost == maintenance_margin × 1.1` (stock MM = 50% × price; futures/forex MM = ContractSize × price × 10%; option MM = 100 × 50% × stockPrice).

---

## 7. Agent limits & supervisor approval (AREA = APV)

Approval gate (implemented): an **employee/agent buy** needs approval only when
`NeedApproval==true AND Limit>0 AND used_limit + orderRSD > Limit`. RSD comparison
uses exchange Convert with **no commission**. `used_limit` bumps on auto-approve
(at placement) or on supervisor approve, and is prorata-refunded on cancel.

#### TC-C3-APV-001 · Over-limit + NeedApproval → pending (POSITIVE)
- **Feature:** Order zahteva odobrenje (prekoračen limit) · **Spec:** Celina 3 §Order status; E2E "Nalog agenta zahteva odobrenje zbog prekoračenja dnevnog limita"; defense Provera 3 · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow
- **Actor:** agent with `need_approval=true`, `limit=100000`, `used_limit=95000`
- **Preconditions:** supervisor previously set limit + require-approval (TC-C3-ACT-*).
- **Request:** `POST /api/v3/orders` (on behalf) or agent buy whose RSD value = 10000 (used+order=105000 > 100000)
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `201` · `status=pending` (E2E "Na čekanju"); used_limit not yet bumped; appears in supervisor order-review portal.

#### TC-C3-APV-002 · Under-limit with NeedApproval → auto-approved (POSITIVE / boundary)
- **Feature:** Auto-approve kad used+order ≤ limit · **Spec:** Celina 3 §Aktuari/§Order status · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow ; test-app/workflows/wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType
- **Actor:** agent `need_approval=true`, `limit=100000`, `used_limit=95000`
- **Request:** buy whose RSD value = 5000 (used+order = 100000, not > limit)
- **Expected:** `201` · `status=approved`; used_limit incremented to 100000.

#### TC-C3-APV-003 · Agent with NeedApproval=false auto-approves even over limit (POSITIVE / implemented conjunction)
- **Feature:** Approval gate konjunkcija · **Spec:** Celina 3 §Order status (implementation diverges) · **Existing test:** test-app/workflows/wf_limit_enforcement_test.go::TestWF_LimitEnforcementAcrossDomains
- **Actor:** agent `need_approval=false`, low limit
- **Request:** buy whose RSD value far exceeds limit.
- **Expected (implemented):** `201` · `status=approved` (auto). **Documented gap:** spec says "used-limit exhausted OR order exceeds limit" alone should force approval; the code requires `NeedApproval` too. Flagged.

#### TC-C3-APV-004 · Multi-currency limit check via no-commission conversion (POSITIVE)
- **Feature:** Limit u jednoj valuti, konverzija bez provizije · **Spec:** Celina 3 §Aktuari "konverzija … bez … provizije" · **Existing test:** test-app/workflows/wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit (RSD-equivalent path)
- **Actor:** agent trading a USD-denominated listing, limit in RSD
- **Request:** USD buy whose RSD-converted value crosses the limit.
- **Expected:** `201`; the used_limit comparison converts the order's native amount to RSD with the exchange rate and **no** commission, then applies the gate.

#### TC-C3-APV-005 · Supervisor approves a pending order (POSITIVE)
- **Feature:** Supervizor Approve · **Spec:** Celina 3 §Portal Pregled ordera; defense Provera 3 · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow ; test-app/workflows/stock_order_test.go::TestOrder_ApproveOrder_RequiresSupervisor
- **Actor:** supervisor (`orders.cancel.all`)
- **Preconditions:** pending order from TC-C3-APV-001.
- **Request:** `POST /api/v3/orders/:id/approve`
  - Auth: `Bearer <supervisor token>`
- **Verification:** fast-path
- **Expected:** `200` · `status=approved`, `approved_by`=supervisor name; used_limit bumped now; audit log `order.approve` entry; `stock.order-approved` Kafka + `ORDER_APPROVED` push. Order proceeds to fill.
- **Negative siblings:** agent token → 403 (TestOrder_ApproveOrder_RequiresSupervisor).

#### TC-C3-APV-006 · Supervisor declines a pending order (POSITIVE)
- **Feature:** Supervizor Decline · **Spec:** Celina 3 §Portal Pregled ordera · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow ; test-app/workflows/stock_order_test.go::TestOrder_RejectOrder_RequiresSupervisor
- **Actor:** supervisor
- **Request:** `POST /api/v3/orders/:id/reject`
  - Auth: `Bearer <supervisor token>`
- **Verification:** fast-path
- **Expected:** `200` · `status=declined`; audit `order.decline`; `stock.order-declined` Kafka + `ORDER_DECLINED` push; reservation released; used_limit not charged.
- **Negative siblings:** agent token → 403.

#### TC-C3-APV-007 · Approve/Decline is once-only; illegal transition rejected (NEGATIVE)
- **Feature:** Status se menja samo jednom · **Spec:** Celina 3 §Order status "supervizor može samo jednom da promeni status" · **Existing test:** —
- **Actor:** supervisor
- **Preconditions:** order already approved (or declined) via TC-C3-APV-005/006.
- **Request:** `POST /api/v3/orders/:id/approve` again (or `/reject` on an approved order)
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `409` · `business_rule_violation` "order is not pending" (FailedPrecondition). A declined order cannot be approved and vice-versa.

#### TC-C3-APV-008 · Settlement-date-passed order: decline-only (NEGATIVE)
- **Feature:** Istekao settlement → samo Decline · **Spec:** Celina 3 §Order status; E2E "Automatsko odbijanje fjučersa sa isteklim datumom" · **Existing test:** —
- **Actor:** supervisor
- **Preconditions:** pending futures order whose settlement date has passed.
- **Request:** `POST /api/v3/orders/:id/approve` then `POST /api/v3/orders/:id/reject`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** approve → `409` `business_rule_violation` "cannot approve: settlement date has passed"; reject → `200` `status=declined` (decline still allowed = decline-only).

#### TC-C3-APV-009 · Approve/Reject not found / non-numeric id (NEGATIVE)
- **Feature:** Validacija order id · **Spec:** Celina 3 §Portal Pregled ordera · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_GetMyOrder_NotFound
- **Actor:** supervisor
- **Request:** `POST /api/v3/orders/99999/approve` ; `POST /api/v3/orders/abc/approve`
- **Expected:** unknown id → 404 not_found; non-numeric → 400 validation_error.

---

## 8. Order-review portal, my-orders, cancellation, audit (AREA = MGMT)

#### TC-C3-MGMT-001 · Supervisor lists all orders for review (POSITIVE)
- **Feature:** Portal: Pregled ordera (filteri All/Pending/Approved/Declined/Done) · **Spec:** Celina 3 §Portal Pregled ordera · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_ListOrders_Supervisor / TestOrder_ListOrders_RequiresSupervisor
- **Actor:** supervisor (`orders.read.all`)
- **Request:** `GET /api/v3/orders?status=pending&agent_email=a@b.rs&direction=buy&order_type=limit`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200` · `{ "orders":[…], "total_count":N }` with agent, order_type, asset, quantity, contract_size, price_per_unit, direction, remaining_portions, status.
- **Negative siblings:** agent/client token → 403 forbidden (lacks `orders.read.all`).

#### TC-C3-MGMT-010 · Agent/client lists own orders (My Orders) (POSITIVE)
- **Feature:** "Moji orderi" stranica · **Spec:** Celina 3 §Portal Pregled ordera "Moji orderi" · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_ListMyOrders
- **Actor:** client (also agent)
- **Request:** `GET /api/v3/me/orders?status=approved&direction=buy&order_type=market`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200`; only caller's own orders; filterable by status/direction/order_type; rows carry ticker, quantity, execution price, status, dates, commission.

#### TC-C3-MGMT-011 · Get a single order with audit/reservation fields (POSITIVE / NEGATIVE)
- **Feature:** Detalj ordera · **Spec:** REST §27 GET /me/orders/:id · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_GetMyOrder / TestOrder_GetMyOrder_NotFound
- **Actor:** client
- **Request:** `GET /api/v3/me/orders/:id` · Auth: `Bearer <client token>`
- **Expected:** `200`; includes reservation_amount/currency/account_id, placement_rate, saga_id, last_modification, nested `order_transactions` (with native/converted amount, fx_rate, commission). Unknown id → 404.

#### TC-C3-MGMT-030 · Cancel an unfilled order releases reservation (POSITIVE)
- **Feature:** Otkazivanje neispunjenog ordera · **Spec:** Celina 3 §Portal Pregled ordera "otkazivanje celog ili dela" · **Existing test:** test-app/workflows/stock_order_test.go::TestOrder_CancelOrder ; test-app/workflows/wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation
- **Actor:** client (owner)
- **Preconditions:** an unfilled (or partially filled) buy order with an active funds reservation.
- **Request:** `POST /api/v3/me/orders/:id/cancel`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · `status=cancelled`, `is_done=true`; funds reservation released (available balance restored by the unfilled portion); for an agent order, prorated used_limit refunded.
- **Negative siblings:** cancel a fully-done order → 409 "order is already completed"; cancel an already-declined/cancelled order → 409; cancel someone else's order → 404 not_found (no existence leak) (TC-C3-MGMT-031); unknown id → 404 (TestOrder_CancelOrder_NotFound).

#### TC-C3-MGMT-031 · Cancel another user's order hidden (NEGATIVE / ownership)
- **Feature:** Vlasništvo ordera · **Spec:** Resource-Ownership · **Existing test:** —
- **Actor:** client A
- **Request:** `POST /api/v3/me/orders/<B's order id>/cancel` · Auth: `Bearer <client A token>`
- **Expected:** `404` · not_found (cross-owner lookups don't leak existence).

#### TC-C3-MGMT-040 · Audit-log entries for approve/decline & limit changes (POSITIVE)
- **Feature:** Audit log (order odobravanje/odbijanje, limit, reset, porez) · **Spec:** Celina 3 §Portal Pregled ordera "Audit log" · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow (side-effect) ; api-gateway/internal/handler/business_audit_handler_test.go
- **Actor:** supervisor reading `GET /api/v3/admin/changelog?action=order.approve` (and `order.decline`, `limit.set`, `limit.used_reset`, `tax.collect`)
- **Verification:** n/a
- **Expected:** `200`; business-audit rows record who/when approved/declined an order, changed a limit, reset usedLimit, ran manual tax. Visible only to admins/supervisors; filterable by action/target/actor/date.

---

## 9. Portfolio (AREA = PRT)

`GET /me/portfolio` (unified), `GET /me/portfolio/summary`,
`GET /me/holdings/:id/transactions`, `POST /me/portfolio/:id/exercise`,
`POST /options/:option_id/exercise`. Make-public now routes via OTC stocks
(`POST /me/otc/stocks`, Celina 4).

#### TC-C3-PRT-001 · List holdings with P/L (POSITIVE)
- **Feature:** Moj portfolio — spisak hartija + profit · **Spec:** Celina 3 §Moj Portfolio; defense Provera 3 · **Existing test:** test-app/workflows/portfolio_test.go::TestPortfolio_ListHoldings / TestPortfolio_ListHoldings_FilterByType ; test-app/workflows/wf_client_stock_banking_test.go::TestWF_ClientTradesStockAfterBanking
- **Actor:** client / agent
- **Request:** `GET /api/v3/me/portfolio` (and `?...` filter via summary)
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200`; grouped `securities` + `funds`; per-position symbol/quantity/avg_cost_rsd/current_price_rsd/current_value_rsd/p_l_rsd/p_l_pct (unrealized), `dividends_received_rsd`, option fields (strike, premium, intrinsic), `fund_status`. Realized vs unrealized totals present.
- **Negative siblings:** unauthenticated → 401 (TestPortfolio_ListHoldings_Unauthenticated); invalid security_type filter → 400 (TestPortfolio_ListHoldings_InvalidSecurityType).

#### TC-C3-PRT-002 · Portfolio summary (POSITIVE)
- **Feature:** Profit prikaz · **Spec:** Celina 3 §Moj Portfolio "Profit" · **Existing test:** test-app/workflows/portfolio_test.go::TestPortfolio_GetSummary
- **Actor:** client
- **Request:** `GET /api/v3/me/portfolio/summary` · Auth: `Bearer <client token>`
- **Expected:** `200`; total value, gains/losses, allocation.

#### TC-C3-PRT-003 · Holding transaction breakdown (POSITIVE / NEGATIVE)
- **Feature:** Po-transakcijski prikaz pozicije · **Spec:** REST §28 /me/holdings/:id/transactions · **Existing test:** —
- **Actor:** client (owner)
- **Request:** `GET /api/v3/me/holdings/:id/transactions?direction=buy`
- **Expected:** `200` per-purchase price/native/converted/fx_rate/commission/account; `direction` not in {buy,sell} → 400; holding not owned → 404.

#### TC-C3-PRT-010 · Sell quantity ≤ held enforced (NEGATIVE)
- **Feature:** Prodaja ≤ broj koji se poseduje · **Spec:** Celina 3 §Moj Portfolio + §Create Orders SELL; defense Celina 4 Provera 1 "ne nudi više od available" · **Existing test:** test-app/workflows/wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding (boundary = sell-all)
- **Actor:** client
- **Preconditions:** holds 5 units.
- **Request:** `POST /api/v3/me/orders` sell qty=6.
- **Expected:** `409` · `business_rule_violation` (cannot reserve more than held). Boundary: qty=5 (sell all) → 201.

#### TC-C3-PRT-020 · Make-public to OTC marketplace (POSITIVE / NEGATIVE)
- **Feature:** "Javni režim" akcija → OTC · **Spec:** Celina 3 §Moj Portfolio "javnim … OTC trading (4. celina)"; (Phase 8 moved to OTC stocks) · **Existing test:** test-app/workflows/portfolio_test.go::TestPortfolio_MakePublic_InvalidQuantity
- **Actor:** client / agent
- **Request:** `POST /api/v3/me/otc/stocks` Body `{ "direction":"sell", "quantity":10, "holding_id":<id> }`
  - Auth: `Bearer <token>`
- **Verification:** n/a
- **Expected:** `201` standing OTC sell offer created from the holding (full flow lives in celina-4-otc-and-funds.md).
- **Negative siblings:** `quantity:0` → 400 validation_error; quantity > held → 409.

#### TC-C3-PRT-030 · Exercise option from portfolio (POSITIVE / NEGATIVE)
- **Feature:** Iskorišćavanje opcije (in-the-money) · **Spec:** Celina 3 §Moj Portfolio "iskorišćavanje opcije … in the money"; defense Provera 4 · **Existing test:** test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle ; test-app/workflows/portfolio_test.go::TestPortfolio_ExerciseOption_NotFound / TestPortfolio_ExerciseOption_Unauthenticated
- **Actor:** agent (only actuaries hold exchange-bought options)
- **Preconditions:** option holding, settlement date not passed, in-the-money.
- **Request:** `POST /api/v3/me/portfolio/:id/exercise` (holding id) — or `POST /api/v3/options/:option_id/exercise`
  - Auth: `Bearer <agent token>`
- **Verification:** n/a
- **Expected:** `200`; underlying shares delivered per strike, account debited the strike cost, option holding consumed (can no longer be exercised — defense Provera 4 "opcija više ne može da se iskoristi"); realized gain feeds tax.
- **Negative siblings:** holding not found / not owned → 404; unauthenticated → 401; past settlement / out-of-money → 409 business_rule_violation.

#### TC-C3-PRT-040 · Dividend history per position (POSITIVE)
- **Feature:** Istorija primljenih dividendi · **Spec:** Celina 3 §Moj Portfolio "Istorija primljenih dividendi" · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode (dividends_received side-effect)
- **Actor:** client
- **Request:** `GET /api/v3/me/portfolio` (read `dividends_received_rsd` per position) + `GET /api/v3/me/dividends`
- **Expected:** `200`; per-position `dividends_received_rsd` reflects net dividends; `/me/dividends` lists payout history.

---

## 10. Dividends (AREA = DIV)

#### TC-C3-DIV-001 · Declare a dividend (POSITIVE / NEGATIVE)
- **Feature:** Deklarisanje dividende · **Spec:** Celina 3 §Isplata dividendi; REST §51 · **Existing test:** —
- **Actor:** employee with `securities.manage.catalog`
- **Request:** `POST /api/v3/admin/dividends` Body `{ "security_id":12, "ticker":"AAPL", "amount_per_share_rsd":"50.00", "payment_date":"2026-06-15" }`
  - Auth: `Bearer <supervisor/admin token>`
- **Verification:** fast-path
- **Expected:** `201`; `status=declared`; idempotent on `(security_id, payment_date)`.
- **Negative siblings:** missing/invalid fields → 400; non-catalog permission → 403.

#### TC-C3-DIV-002 · Payout fan-out: 15% client, 0% bank (POSITIVE)
- **Feature:** Isplata dividendi proporcionalno + porez 15% (osim banke) · **Spec:** Celina 3 §Isplata dividendi "15% porez osim … banke" · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** employee with `securities.manage.catalog`
- **Preconditions:** declared dividend; holdings exist (client + bank + fund).
- **Request:** `POST /api/v3/admin/dividends/:id/payout`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200` `{ payouts_created, fund_payouts, total_amount_rsd }`; client holdings credited net = gross − 15% tax; bank holdings credited full gross (no tax → Profit Banke); fund holdings full gross with per-investor snapshot.
- **Negative siblings:** payout already paid/cancelled → 500 internal_error; invalid id → 400.

#### TC-C3-DIV-010 · Account routing fallback to RSD (POSITIVE)
- **Feature:** Ruta isplate: isti račun → default u valuti → RSD konverzija · **Spec:** Celina 3 §Isplata dividendi "ako taj račun ne postoji … konvertuje u RSD" · **Existing test:** —
- **Actor:** employee
- **Preconditions:** holder lacks the listing-currency account.
- **Request:** payout as TC-C3-DIV-002.
- **Expected:** payout credited to a resolvable account; when no listing-currency account exists, the amount is converted to RSD and credited. `credited_account_id` reflects the chosen account.

#### TC-C3-DIV-020 · Fund dividends listing (POSITIVE)
- **Feature:** Dividende fonda · **Spec:** REST §51 /investment-funds/:id/dividends · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** any JWT
- **Request:** `GET /api/v3/investment-funds/:id/dividends`
- **Expected:** `200` `{ payments:[…] }` each with `per_investor_snapshot`. Invalid id → 400.

#### TC-C3-DIV-030 · Quarterly auto-payout + qty×price×yield/4 formula (NO-ENDPOINT)
- **Feature:** Kvartalna automatska isplata + formula · **Spec:** Celina 3 §Isplata dividendi (last working day Mar/Jun/Sep/Dec; `Dividend = Qty × Price × DividendYield/4`) · **Existing test:** —
- **Actor:** system cron
- **Expected (implemented):** dividends are admin-declared (`amount_per_share_rsd`) + manually paid out; there is no endpoint exercising the exact `yield/4` quarterly auto-cron. **Documented gap:** NO-ENDPOINT (the 15% client / 0% bank withholding IS implemented, TC-C3-DIV-002).

---

## 11. Capital-gains tax (AREA = TAX)

15% on realized profit (stock sale, futures settlement, option exercise,
dividends); none on loss/unrealized. Monthly auto-deduct; supervisor manual
trigger; RSD conversion with no commission (state has only an RSD account).

#### TC-C3-TAX-001 · Self tax balance (paid this year, unpaid this month) (POSITIVE)
- **Feature:** Tax info za korisnika · **Spec:** Celina 3 §Moj Portfolio "Porez" + §Porez; defense Provera 5 "Korisnik vidi svoje dugovanje" · **Existing test:** test-app/workflows/tax_test.go::TestTax_ListMyTaxRecords / TestTax_ListMyTaxRecords_EmployeeToken
- **Actor:** client (or employee/actuary)
- **Request:** `GET /api/v3/me/tax?page=1&page_size=10`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `200` · `{ records:[…], total_count, tax_paid_this_year, tax_unpaid_this_month }`; each record carries security_type, ticker, qty, buy/sell price, total_gain, currency, tax_year, tax_month.
- **Negative siblings:** unauthenticated → 401 (TestTax_ListMyTaxRecords_Unauthenticated).

#### TC-C3-TAX-010 · Supervisor lists all tax debts (POSITIVE / NEGATIVE)
- **Feature:** Portal Porez tracking (svačije dugovanje) · **Spec:** Celina 3 §Izgled stranice; defense Provera 5 "Supervizor vidi svačije" · **Existing test:** test-app/workflows/tax_test.go::TestTax_ListTaxRecords / TestTax_ListTaxRecords_FilterByUserType / TestTax_ListTaxRecords_InvalidUserType
- **Actor:** supervisor (`securities.read.holdings.all`)
- **Request:** `GET /api/v3/tax?user_type=client&search=Marko`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200` · `{ tax_records:[…], total_count }`; filter by user_type (client/actuary).
- **Negative siblings:** `user_type=robot` → 400 validation_error; client/agent token → 403 forbidden.

#### TC-C3-TAX-020 · Manual tax collection (POSITIVE / side-effects)
- **Feature:** Pokretanje obračuna poreza (supervizor) · **Spec:** Celina 3 §Porez "Supervizori mogu ručno pokrenuti"; defense Provera 5 (state credited, user debited, history shows paid) · **Existing test:** test-app/workflows/tax_test.go::TestTax_CollectTax / TestTax_CollectTax_AgentCannot ; test-app/workflows/wf_tax_collection_test.go::TestWF_TaxCollectionCycle
- **Actor:** supervisor (`securities.manage.catalog`)
- **Preconditions:** users with realized gains this month.
- **Request:** `POST /api/v3/tax/collect` (empty body)
  - Auth: `Bearer <supervisor token>`
- **Verification:** fast-path
- **Expected:** `200` · `{ collected_count, total_collected_rsd, failed_count }`; per user: tax debited from the account where the gain landed, converted to RSD with **no** commission, credited to the State (Firma) RSD account; `tax_paid_this_year` increases, `tax_unpaid_this_month` drops to 0; business-audit `tax.collect` entry.
- **Negative siblings:** agent/client token → 403 (TestTax_CollectTax_AgentCannot).

#### TC-C3-TAX-030 · No tax on loss / unrealized (NEGATIVE / boundary)
- **Feature:** Nema poreza na gubitak/nerealizovano · **Spec:** Celina 3 §Porez "15% od dobiti" (only positive realized) · **Existing test:** test-app/workflows/wf_tax_collection_test.go::TestWF_TaxCollectionCycle (loss branch) ; test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Actor:** supervisor / owner
- **Preconditions:** a position sold at a loss + an open (unrealized) winning position.
- **Request:** `POST /api/v3/tax/collect`; then `GET /api/v3/me/tax`.
- **Expected:** the loss sale produces **no** tax record/charge; the unrealized position is not taxed. Only realized gains are taxed at 15%.

#### TC-C3-TAX-031 · Tax across multiple asset types (POSITIVE)
- **Feature:** Porez po tipu sredstva (akcije/opcije/fjučersi) · **Spec:** E2E "Izračunavanje poreza za više tipova sredstava" · **Existing test:** test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle ; test-app/workflows/wf_tax_collection_test.go::TestWF_TaxCollectionCycle
- **Actor:** owner with realized gains on stock + option exercise + futures settlement
- **Request:** `GET /api/v3/me/tax`.
- **Expected:** `200`; tax records for each realized event at 15%; aggregate `tax_unpaid_this_month` sums them.

#### TC-C3-TAX-040 · PDF export of tax report (NO-ENDPOINT)
- **Feature:** Izvoz u PDF · **Spec:** E2E "omogući izvoz u PDF" · **Existing test:** —
- **Expected:** no PDF endpoint exists. NO-ENDPOINT.

#### TC-C3-TAX-041 · Tax report filtered by year (NO-ENDPOINT)
- **Feature:** Poreski izveštaj po godini · **Spec:** E2E "filtriram po godini 2024" · **Existing test:** —
- **Expected:** `GET /api/v3/me/tax` supports only `page`/`page_size` — no `year` filter param. NO-ENDPOINT.

#### TC-C3-TAX-042 · Profit-discrepancy flagging (NO-ENDPOINT)
- **Feature:** Označavanje neslaganja profita za ručno usklađivanje · **Spec:** E2E "Otkrivanje neslaganja u profitu i označavanje transakcije" · **Existing test:** —
- **Expected:** no endpoint flags a transaction when reported vs computed profit diverge. NO-ENDPOINT.

---

## 12. Recurring orders / DCA (AREA = DCA)

`/api/v3/me/recurring-orders*`. Weekly/monthly Market-order templates;
pause/resume/cancel; cron materialises real orders; insufficient funds → skip +
notify. (Note: the hourly cron's placement step is wired but currently no-ops
until the order-placer integration lands — CRUD + transitions operate.)

#### TC-C3-DCA-001 · Create monthly recurring order (POSITIVE)
- **Feature:** Trajni nalog (DCA) kreiranje · **Spec:** Celina 3 §DCA · **Existing test:** —
- **Actor:** client (also agent)
- **Request:** `POST /api/v3/me/recurring-orders` Body `{ "listing_id":7, "side":"buy", "quantity":10, "account_id":42, "interval":"monthly", "day_of_month":15, "start_date_unix":1731699200, "end_date_unix":0 }`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** `201` · `{ recurring_order:{ status:"active", next_run … } }`.
- **Negative siblings:** TC-C3-DCA-002/003.

#### TC-C3-DCA-002 · Validation: side/interval/day ranges (NEGATIVE)
- **Feature:** DCA validacija · **Spec:** REST §45 · **Existing test:** —
- **Actor:** client
- **Request:** variants — `side:"hold"`; `interval:"daily"`; `interval:"weekly"` with `day_of_week:9`; `interval:"monthly"` with `day_of_month:0` or `31`.
- **Expected:** each → `400` validation_error ("side" oneOf; "interval" oneOf; "day_of_week must be 0..6"; "day_of_month must be 1..28").

#### TC-C3-DCA-003 · Pause → resume → cancel lifecycle (POSITIVE / NEGATIVE)
- **Feature:** DCA pauziranje/nastavak/otkazivanje · **Spec:** Celina 3 §DCA "pauzirati ili otkazati" · **Existing test:** —
- **Actor:** client (owner)
- **Request:** `POST /me/recurring-orders/:id/pause` → `…/resume` → `…/cancel`
- **Expected:** pause → `status=paused`; resume → `status=active`; cancel → terminal (no further ticks). Cross-owner id → 404 not_found.

#### TC-C3-DCA-004 · List + get own recurring orders (POSITIVE)
- **Feature:** DCA pregled · **Spec:** REST §45 · **Existing test:** —
- **Actor:** client
- **Request:** `GET /me/recurring-orders` ; `GET /me/recurring-orders/:id`
- **Expected:** `200`; caller-scoped; unknown/other-owner id → 404.

#### TC-C3-DCA-010 · Cron fires → materialises Market order (POSITIVE) / insufficient funds → skip+notify (NEGATIVE)
- **Feature:** Cron izvršenje + preskakanje na nedostatak sredstava · **Spec:** Celina 3 §DCA "Cron … Market Order … Ako nema dovoljno sredstava, preskače + notifikacija; aktuaru se uračunava u limit" · **Existing test:** —
- **Actor:** system cron (admin-trigger via `POST /api/v3/admin/crons/stock-service/<name>/trigger`)
- **Expected:** on a due active template the cron creates a Market order and advances `next_run`; on insufficient funds it skips, advances `next_run` (never stuck), and emits `RECURRING_ORDER_SKIPPED`; success emits `RECURRING_ORDER_EXECUTED`. For an actuary, spent funds count against the daily limit. **Note:** placement step currently no-ops until the order-placer integration lands — partial coverage.

---

## 13. Watchlist (AREA = WL)

Default `/me/watchlist*` + named `/me/watchlists*`. Track stocks/options/
futures/forex without buying; filter by type; multiple lists.

#### TC-C3-WL-001 · Add → list (enriched) → remove from default watchlist (POSITIVE)
- **Feature:** Watchlist CRUD · **Spec:** Celina 3 §Watchlist · **Existing test:** test-app/workflows/wf_watchlist_named_test.go::TestWF_WatchlistNamedLists
- **Actor:** client (also agent)
- **Request:** `POST /api/v3/me/watchlist {"listing_id":7}` → `GET /api/v3/me/watchlist?listing_type=stock` → `DELETE /api/v3/me/watchlist/7`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** add → `201 {item}` (idempotent re-add still 201 with existing row); list → `200 {items:[…]}` each enriched with current_price, daily_change, daily_change_percent; delete → `204`.
- **Negative siblings:** add unknown listing → 404; `listing_type=crypto` → 400; delete a non-tracked listing → 404.

#### TC-C3-WL-010 · Named watchlists (multiple lists) (POSITIVE / NEGATIVE)
- **Feature:** Više watchlisti ("tech", "forex parovi") · **Spec:** Celina 3 §Watchlist "može imati više watchlisti" · **Existing test:** test-app/workflows/wf_watchlist_named_test.go::TestWF_WatchlistNamedLists
- **Actor:** client (owner)
- **Request:** `POST /me/watchlists {"name":"tech"}` → `POST /me/watchlists/:id/items {"listing_id":1}` → `GET /me/watchlists/:id/items?listing_type=stock` → `DELETE /me/watchlists/:id/items/1` → `DELETE /me/watchlists/:id`
- **Expected:** create `201`; default list always present in `GET /me/watchlists`; add `201`; list `200`; same listing may live in multiple lists; deletes `204`.
- **Negative siblings:** name >64 chars / empty → 400; touching another caller's list → 404.

---

## 14. Price alerts (AREA = ALERT)

`/api/v3/me/price-alerts*`. Conditions `gte`/`lte`/`daily_change_pct_gte`/
`daily_change_pct_lte`; single-shot or recurring with cooldown.

#### TC-C3-ALERT-001 · Create → list → get → update → delete (POSITIVE)
- **Feature:** Price alert CRUD · **Spec:** Celina 3 §Watchlist (alerting UX); REST §43 · **Existing test:** api-gateway/internal/handler/price_alert_handler.go (handler-level) ; — (no workflow test)
- **Actor:** client (also agent)
- **Request:** `POST /me/price-alerts {"listing_id":7,"condition":"gte","threshold":"200.00","is_recurring":false,"cooldown_seconds":3600,"email_too":false}` → `GET /me/price-alerts` → `GET /me/price-alerts/:id` → `PUT /me/price-alerts/:id {"active":false,…}` → `DELETE /me/price-alerts/:id`
  - Auth: `Bearer <client token>`
- **Verification:** n/a
- **Expected:** create `201 {alert}`; list `200 {alerts:[…]}`; get `200`; update `200` (active toggled); delete `204`. Side-effect: a matching tick publishes a `PRICE_ALERT_TRIGGERED` notification; single-shot deactivates on first match.
- **Negative siblings:** TC-C3-ALERT-002.

#### TC-C3-ALERT-002 · Alert validation + ownership (NEGATIVE)
- **Feature:** Price alert validacija · **Spec:** REST §43 · **Existing test:** —
- **Actor:** client
- **Request:** variants — `condition:"crosses"`; missing `listing_id`/`threshold`; `is_recurring:true` with `cooldown_seconds:30` (out of 60..86400); `listing_id` not found; `GET/PUT/DELETE` another caller's alert id.
- **Expected:** invalid condition / missing field / bad cooldown → 400 validation_error; unknown listing → 404; another caller's alert → 404 not_found.

---

## 15. Actuary management (AREA = ACT)

`/api/v3/actuaries*`. Supervisor sets limit, resets usedLimit, toggles
require/skip approval; performance feed for Profit Banke.

#### TC-C3-ACT-001 · List actuaries with filters (POSITIVE / NEGATIVE)
- **Feature:** Portal za upravljanje aktuarima · **Spec:** Celina 3 §Portal za upravljanje aktuarima · **Existing test:** test-app/workflows/actuary_test.go::TestActuary_ListActuaries / TestActuary_ListActuaries_AgentCannot / TestActuary_Unauthenticated
- **Actor:** supervisor (`actuaries.read.all`)
- **Request:** `GET /api/v3/actuaries?search=Milos&position=agent&page=1`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200` · `{ actuaries:[…], total_count }` with limit, used_limit, need_approval, position; filter by email/name/position.
- **Negative siblings:** agent token → 403 forbidden; unauthenticated → 401.

#### TC-C3-ACT-002 · Set actuary limit (POSITIVE / NEGATIVE)
- **Feature:** Supervizor menja limit agenta · **Spec:** Celina 3 §Aktuari "Limit … menja supervizor" · **Existing test:** test-app/workflows/actuary_test.go::TestActuary_SetLimit / TestActuary_SetLimit_EmptyValue ; test-app/workflows/employee_limits_test.go
- **Actor:** supervisor (`actuaries.manage.any`)
- **Request:** `PUT /api/v3/actuaries/:id/limit` Body `{ "limit":"100000.00" }`
  - Auth: `Bearer <supervisor token>`
- **Verification:** fast-path
- **Expected:** `200` updated actuary; business-audit `limit.set` entry.
- **Negative siblings:** empty/invalid limit → 400; agent token → 403; unknown actuary → 404.

#### TC-C3-ACT-003 · Reset used-limit to zero (POSITIVE)
- **Feature:** Supervizor resetuje usedLimit · **Spec:** Celina 3 §Aktuari "resetuje limit i usedLimit … bilo kada" (+ daily 23:59 auto-reset) · **Existing test:** test-app/workflows/actuary_test.go::TestActuary_ResetLimit
- **Actor:** supervisor (`actuaries.manage.any`)
- **Request:** `POST /api/v3/actuaries/:id/reset-limit`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200`; `used_limit=0`; business-audit `limit.used_reset` entry. (Daily auto-reset at 23:59 is the cron counterpart.)

#### TC-C3-ACT-004 · Require / skip approval toggle (POSITIVE / NEGATIVE)
- **Feature:** NeedApproval flag · **Spec:** Celina 3 §Aktuari "Need Approval" · **Existing test:** test-app/workflows/actuary_test.go::TestActuary_RequireApproval
- **Actor:** supervisor/admin (`actuaries.manage.any`)
- **Request:** `POST /api/v3/actuaries/:id/require-approval` then `POST /api/v3/actuaries/:id/skip-approval`
  - Auth: `Bearer <supervisor token>`
- **Verification:** n/a
- **Expected:** `200`; `need_approval` flips true then false (feeds TC-C3-APV-001/003). Unknown actuary → 404; non-manage token → 403.

#### TC-C3-ACT-010 · Actuary performance feed (POSITIVE / NEGATIVE)
- **Feature:** Actuary Performances (Profit Banke) · **Spec:** REST §32 /actuaries/performance · **Existing test:** test-app/workflows/wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType (owner-type basis)
- **Actor:** supervisor (`actuaries.read.all`)
- **Request:** `GET /api/v3/actuaries/performance`
  - Auth: `Bearer <supervisor token>`
- **Expected:** `200` `{ actuaries:[{employee_id, realised_profit_rsd, trade_count, …}] }` (realised P&L only, on-behalf-of-bank trades).
- **Negative siblings:** missing `actuaries.read.all` → 403.

---

## 16. Defense-flow end-to-end scenarios (AREA = E2E)

Each chains the relevant TCs into one must-pass grading flow (defense
"3 - Trgovanje na berzi").

#### TC-C3-E2E-001 · Provera 1 — securities portal layout (POSITIVE)
- **Feature:** Pregled po tabovima (forex/stock/futures) + detalj akcije · **Spec:** defense Provera 1 · **Existing test:** test-app/workflows/securities_test.go::TestSecurities_ListStocks / TestSecurities_ListFutures / TestSecurities_ListForexPairs / TestSecurities_GetStock
- **Steps:** TC-C3-LST-001 → TC-C3-LST-020 → TC-C3-LST-030 → TC-C3-LST-010 (detail + options chain).
- **Expected:** all 200; stock detail exposes the options section.

#### TC-C3-E2E-002 · Provera 2 — buy a ForexPair (POSITIVE)
- **Feature:** Kupovina forex-a; money out in *from*, in in *to* · **Spec:** defense Provera 2 · **Existing test:** test-app/workflows/wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit
- **Steps:** TC-C3-EXC-020 (testing_mode on) → TC-C3-ORD-012 (forex buy) → verify quote account debited + base account credited.
- **Expected:** balances move per the FX legs; no menjačnica commission.

#### TC-C3-E2E-003 · Provera 3 — buy stock/futures (client auto vs agent-needs-approval) + portfolio (POSITIVE)
- **Feature:** Kreiranje Ordera (klijent auto / aktuar approval) + Moj portfolio · **Spec:** defense Provera 3 · **Existing test:** test-app/workflows/wf_order_approval_test.go::TestWF_OrderApprovalWorkflow ; test-app/workflows/wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle
- **Steps:** client path TC-C3-ORD-017 (auto-approved fill) → TC-C3-PRT-001 (holding appears); agent path TC-C3-ACT-002/004 → TC-C3-APV-001 (pending) → TC-C3-APV-005 (supervisor approve) → fill → TC-C3-PRT-001.
- **Expected:** client fills immediately; agent over-limit order waits for supervisor approval then fills; both holdings show in portfolio.

#### TC-C3-E2E-004 · Provera 4 — buy & exercise an option (POSITIVE)
- **Feature:** Kupovina + iskorišćavanje opcije; dospeće u portfoliju · **Spec:** defense Provera 4 · **Existing test:** test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Steps:** agent buys an option (TC-C3-LST-040 → order) → TC-C3-PRT-030 (exercise) → verify underlying delivered, account debited, option consumed (re-exercise → 404/409).
- **Expected:** post-exercise the option is gone and shares appear; money left the account.

#### TC-C3-E2E-005 · Provera 5 — tax (user debt, supervisor portal, collect, state credited) (POSITIVE)
- **Feature:** Porez end-to-end · **Spec:** defense Provera 5 · **Existing test:** test-app/workflows/wf_tax_collection_test.go::TestWF_TaxCollectionCycle ; test-app/workflows/tax_test.go::TestTax_CollectTax
- **Steps:** realize a gain → TC-C3-TAX-001 (user sees debt) → TC-C3-TAX-010 (supervisor sees all) → TC-C3-TAX-020 (collect) → verify State RSD account credited + user account debited + TC-C3-TAX-001 shows paid in history.
- **Expected:** money moves user→State at 15% (RSD, no commission); history reflects payment.

---

## 17. Field-validation matrices

### 17.1 Order (`POST /api/v3/me/orders`, `POST /api/v3/orders`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `direction` | `"buy"` / `"sell"` | `"hold"` → 400 validation_error (oneOf); forex+`"sell"` → 400 "forex orders must be direction=buy" |
| `order_type` | `"market"`/`"limit"`/`"stop"`/`"stop_limit"` | `"trailing"` → 400 (oneOf) |
| `quantity` | `5` | `0`/negative → 400 "quantity must be positive" |
| `listing_id` | `42` | `0`/missing → 400 "listing_id is required" |
| `account_id` | owned account | missing on buy → 400; missing on sell → 400 "proceeds destination"; not owned → 403 forbidden |
| `limit_value` | `"98.00"` (limit/stop_limit) | missing for limit/stop_limit → 400 |
| `stop_value` | `"100.00"` (stop/stop_limit) | missing for stop/stop_limit → 400 |
| `security_type` | `"stock"`/`"futures"`/`"forex"`/`"option"` | unknown → 400 (oneOf) |
| `base_account_id` | owned, ≠ account_id (forex) | missing for forex → 400; == account_id → 400 "must differ"; not owned → 403 |
| `all_or_none` | `true`/`false` | non-bool → 400 (bind error) |
| `margin` | `true`/`false` | (no prerequisite check — see TC-C3-MGN-002/003) |
| `client_id` vs `on_behalf_of_fund_id` (POST /orders) | exactly one set | neither → 400; both → 400; account not owned by client → 403 |

### 17.2 Listing / Option read filters

| Field | Valid example | Invalid form → expected |
|---|---|---|
| stocks `sort_by` | `price`/`volume`/`change`/`margin` | other → 400 |
| stocks `sort_order` | `asc`/`desc` | other → 400 |
| history `period` | `day`/`week`/`month`/`year`/`5y`/`all` | other → 400 |
| forex `liquidity` | `high`/`medium`/`low` | other → 400 |
| options `stock_id` | present uint | missing → 400 "stock_id … required"; non-numeric → 400 |
| options `option_type` | `call`/`put` | other → 400 |
| candles `listing_id`/`from`/`to` | present | missing → 400 |
| candles `interval` | `1m`/`5m`/`15m`/`1h`/`4h`/`1d` | other → 400 |

### 17.3 Exchange (testing-mode)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `enabled` | `true`/`false` | missing/non-bool → 400; caller lacks `exchanges.manage` → 403 |
| `:id` (GET /:id) | existing | unknown → 404; non-numeric → 400 |

### 17.4 RecurringOrder (`POST /api/v3/me/recurring-orders`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `side` | `buy`/`sell` | other → 400 (oneOf) |
| `interval` | `weekly`/`monthly` | other → 400 (oneOf) |
| `day_of_week` | `0..6` (weekly) | <0 / >6 → 400 |
| `day_of_month` | `1..28` (monthly) | <1 / >28 → 400 |
| `listing_id`/`quantity`/`account_id` | positive | missing/0 → 400 |
| `end_date_unix` | `0` (no end) or future | — |

### 17.5 Watchlist & Price Alert

| Field | Valid example | Invalid form → expected |
|---|---|---|
| watchlist `listing_id` | existing | unknown → 404 |
| watchlist `listing_type` (filter) | `stock`/`option`/`futures`/`forex` | other → 400 |
| named list `name` | 1–64 chars | empty / >64 → 400 |
| alert `condition` | `gte`/`lte`/`daily_change_pct_gte`/`daily_change_pct_lte` | other → 400 |
| alert `threshold` | `"200.00"` | missing → 400 |
| alert `cooldown_seconds` (recurring) | `60..86400` | out of range → 400 |
| alert `:id` | owned | not owned → 404 |

### 17.6 TaxObligation / Dividend

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `GET /tax` `user_type` | `client`/`actuary` | other → 400; caller lacks `securities.read.holdings.all` → 403 |
| `POST /tax/collect` | empty body | caller lacks `securities.manage.catalog` → 403 |
| dividend `security_id`/`amount_per_share_rsd`/`payment_date` | present, valid | missing/invalid → 400; non-catalog → 403 |
| dividend payout `:id` | declared, unpaid | already paid/cancelled → 500; invalid id → 400 |

### 17.7 Actuary management

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `PUT /actuaries/:id/limit` `limit` | `"100000.00"` | empty/invalid → 400; not `actuaries.manage.any` → 403; unknown → 404 |
| `:id` (all actuary routes) | existing actuary | unknown → 404; non-numeric → 400 |
| list `position` filter | `agent`/`supervisor` | — (free text; empty ⇒ all) |

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| exchanges: list/search/detail | TC-C3-EXC-001..011 | stock_exchange_test.go::TestStockExchange_ListExchanges/_SearchFilter/_GetExchange/_GetExchange_NotFound/_ListExchanges_Unauthenticated | covered |
| exchanges: testing-mode toggle (open/close for testing) | TC-C3-EXC-020,021 | stock_exchange_test.go::TestStockExchange_TestingMode_SetAndGet/_RequiresSupervisor | covered |
| order rejected when exchange closed ("Berza je zatvorena") | TC-C3-EXC-030 | — | NO-ENDPOINT |
| after-hours (<4h to close) slow fill + after_hours flag | TC-C3-EXC-031 | — | partial |
| listings: stocks list/search/sort/filter | TC-C3-LST-001..005 | securities_test.go::TestSecurities_ListStocks/_SearchByTicker/_SortByPrice/_InvalidSortBy | covered |
| listings: stock detail + price history periods | TC-C3-LST-010,011 | securities_test.go::TestSecurities_GetStock/_GetStockHistory/_GetStockHistory_InvalidPeriod | covered |
| listings: futures (month codes/settlement) + filter/detail/history | TC-C3-LST-020,021 | securities_test.go::TestSecurities_ListFutures/_SettlementDateFilter/_GetFutures/_GetFutures_NotFound/_GetFuturesHistory | covered |
| listings: forex pairs list/filter/detail/history | TC-C3-LST-030,031 | securities_test.go::TestSecurities_ListForexPairs/_LiquidityFilter/_InvalidLiquidity/_GetForexPair/_GetForexPair_NotFound/_GetForexPairHistory | covered |
| listings: options chain + detail | TC-C3-LST-040,041 | securities_test.go::TestSecurities_ListOptions_RequiresStockID/_WithStockID/_FilterByType/_GetOption/_GetOption_NotFound | covered |
| market-data: candles | TC-C3-LST-050 | — | covered |
| client visibility: stocks+futures allowed | TC-C3-VIS-001 | securities_test.go::TestSecurities_ClientCanViewStocksAndFutures | covered |
| client visibility: forex/options hidden from clients | TC-C3-VIS-002,003 | — | NO-ENDPOINT |
| order: market buy/sell pricing (ask/bid) + commission min(14%,$7) | TC-C3-ORD-001,002 | stock_order_test.go::TestOrder_CreateMarketBuyOrder; wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle; wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding | covered |
| order: limit buy/sell favorable-price + commission min(24%,$12) | TC-C3-ORD-003,004 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| order: stop → market on trigger | TC-C3-ORD-010 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| order: stop-limit two-stage activation | TC-C3-ORD-011 | wf_stop_limit_refund_test.go::TestWF_StopLimit_ExpiryReleasesReservation | covered |
| order: input validation (qty/account/limit/stop/direction/type/auth) | TC-C3-ORD-005..009 | stock_order_test.go::TestOrder_CreateOrder_ZeroQuantity/_InvalidDirection/_InvalidOrderType/_CreateLimitOrder_RequiresLimitValue/_CreateBuyOrder_RequiresAccountID/_CreateOrder_Unauthenticated | covered |
| order: forex buy convert+base-credit + forex constraints | TC-C3-ORD-012..015 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | covered |
| order: account ownership enforcement | TC-C3-ORD-016 | — | covered |
| order: client auto-approved | TC-C3-ORD-017 | stock_order_test.go::TestOrder_ClientOrderAutoApproved | covered |
| order: on-behalf-of-client | TC-C3-ORD-020 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow | covered |
| order: on-behalf-of-fund (fund_holdings) | TC-C3-ORD-021 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| order: concurrency / reservation release | TC-C3-ORD-030 | wf_stock_concurrent_orders_test.go::TestWF_StockConcurrentOrders_RespectsAvailableBalance; wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation | covered |
| order: partial multi-trader fill aggregation | TC-C3-ORD-031 | wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle | covered |
| order: commission-failure resilience | TC-C3-ORD-040 | wf_stock_commission_failure_test.go::TestWF_StockFill_CommissionFailure_TradeStillCompletes | covered |
| order: fill saga no-divergence | TC-C3-ORD-041 | wf_stock_fill_failure_test.go::TestWF_StockFill_AccountServiceFailure_NoDivergence | covered |
| AON: blocks partial fill / full fill | TC-C3-AON-001,002 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | partial |
| margin: flag persisted | TC-C3-MGN-001 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| margin: permission prerequisite | TC-C3-MGN-002 | — | NO-ENDPOINT |
| margin: credit/cash ≥ IMC prerequisite | TC-C3-MGN-003 | — | NO-ENDPOINT |
| margin: IMC = MM×1.1 display | TC-C3-MGN-004 | securities_test.go::TestSecurities_GetStock | covered |
| agent approval: over-limit+needApproval → pending | TC-C3-APV-001 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow | covered |
| agent approval: under-limit auto-approve (boundary) | TC-C3-APV-002 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType | covered |
| agent approval: needApproval=false auto-approves over limit (impl conjunction) | TC-C3-APV-003 | wf_limit_enforcement_test.go::TestWF_LimitEnforcementAcrossDomains | partial |
| agent approval: multi-currency limit via no-commission conversion | TC-C3-APV-004 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | partial |
| supervisor approve/decline | TC-C3-APV-005,006 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; stock_order_test.go::TestOrder_ApproveOrder_RequiresSupervisor/_RejectOrder_RequiresSupervisor | covered |
| approve/decline once-only + illegal transition | TC-C3-APV-007 | — | covered |
| settlement-passed → decline-only | TC-C3-APV-008 | — | covered |
| approve/reject id validation | TC-C3-APV-009 | stock_order_test.go::TestOrder_GetMyOrder_NotFound | covered |
| order-review portal (supervisor list+filters) | TC-C3-MGMT-001 | stock_order_test.go::TestOrder_ListOrders_Supervisor/_ListOrders_RequiresSupervisor | covered |
| my-orders (agent/client list+filters) | TC-C3-MGMT-010 | stock_order_test.go::TestOrder_ListMyOrders | covered |
| order detail (audit/reservation fields) | TC-C3-MGMT-011 | stock_order_test.go::TestOrder_GetMyOrder/_GetMyOrder_NotFound | covered |
| cancel unfilled portion + release reservation + ownership | TC-C3-MGMT-030,031 | stock_order_test.go::TestOrder_CancelOrder/_CancelOrder_NotFound; wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation | covered |
| audit-log entries (approve/decline/limit/reset/tax) | TC-C3-MGMT-040 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; business_audit_handler_test.go | covered |
| portfolio: holdings + realized/unrealized P/L | TC-C3-PRT-001,002 | portfolio_test.go::TestPortfolio_ListHoldings/_FilterByType/_GetSummary/_ListHoldings_Unauthenticated/_ListHoldings_InvalidSecurityType; wf_client_stock_banking_test.go::TestWF_ClientTradesStockAfterBanking | covered |
| portfolio: holding transaction breakdown | TC-C3-PRT-003 | — | covered |
| portfolio: sell qty ≤ held | TC-C3-PRT-010 | wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding | covered |
| portfolio: make-public → OTC | TC-C3-PRT-020 | portfolio_test.go::TestPortfolio_MakePublic_InvalidQuantity | covered |
| portfolio: option exercise | TC-C3-PRT-030 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle; portfolio_test.go::TestPortfolio_ExerciseOption_NotFound/_ExerciseOption_Unauthenticated | covered |
| portfolio: dividend history | TC-C3-PRT-040 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: declare | TC-C3-DIV-001 | — | covered |
| dividends: payout 15% client / 0% bank / fund snapshot | TC-C3-DIV-002 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: account routing fallback → RSD | TC-C3-DIV-010 | — | partial |
| dividends: fund dividends listing | TC-C3-DIV-020 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: quarterly auto cron + qty×price×yield/4 | TC-C3-DIV-030 | — | NO-ENDPOINT |
| tax: self balance (paid-year/unpaid-month) | TC-C3-TAX-001 | tax_test.go::TestTax_ListMyTaxRecords/_EmployeeToken/_Unauthenticated | covered |
| tax: supervisor portal list + filters | TC-C3-TAX-010 | tax_test.go::TestTax_ListTaxRecords/_FilterByUserType/_InvalidUserType | covered |
| tax: manual collect (15%, RSD no-commission, state credited) | TC-C3-TAX-020 | tax_test.go::TestTax_CollectTax/_AgentCannot; wf_tax_collection_test.go::TestWF_TaxCollectionCycle | covered |
| tax: none on loss/unrealized | TC-C3-TAX-030 | wf_tax_collection_test.go::TestWF_TaxCollectionCycle; wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| tax: multiple asset types | TC-C3-TAX-031 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle; wf_tax_collection_test.go::TestWF_TaxCollectionCycle | covered |
| tax: PDF export | TC-C3-TAX-040 | — | NO-ENDPOINT |
| tax: report by year filter | TC-C3-TAX-041 | — | NO-ENDPOINT |
| tax: profit-discrepancy flagging | TC-C3-TAX-042 | — | NO-ENDPOINT |
| DCA: create monthly/weekly | TC-C3-DCA-001 | — | covered |
| DCA: validation (side/interval/day) | TC-C3-DCA-002 | — | covered |
| DCA: pause/resume/cancel + ownership | TC-C3-DCA-003 | — | covered |
| DCA: list/get own | TC-C3-DCA-004 | — | covered |
| DCA: cron fire + insufficient-funds skip+notify | TC-C3-DCA-010 | — | partial |
| watchlist: default add/list/remove + filter | TC-C3-WL-001 | wf_watchlist_named_test.go::TestWF_WatchlistNamedLists | covered |
| watchlist: multiple named lists | TC-C3-WL-010 | wf_watchlist_named_test.go::TestWF_WatchlistNamedLists | covered |
| price alerts: CRUD | TC-C3-ALERT-001 | — | partial |
| price alerts: validation + ownership | TC-C3-ALERT-002 | — | covered |
| actuaries: list + filters + RBAC | TC-C3-ACT-001 | actuary_test.go::TestActuary_ListActuaries/_ListActuaries_AgentCannot/_Unauthenticated | covered |
| actuaries: set limit | TC-C3-ACT-002 | actuary_test.go::TestActuary_SetLimit/_SetLimit_EmptyValue; employee_limits_test.go | covered |
| actuaries: reset used-limit | TC-C3-ACT-003 | actuary_test.go::TestActuary_ResetLimit | covered |
| actuaries: require/skip approval toggle | TC-C3-ACT-004 | actuary_test.go::TestActuary_RequireApproval | covered |
| actuaries: performance feed | TC-C3-ACT-010 | wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType | covered |
| defense Provera 1 — portal layout | TC-C3-E2E-001 | securities_test.go::TestSecurities_ListStocks/_ListFutures/_ListForexPairs/_GetStock | covered |
| defense Provera 2 — buy ForexPair | TC-C3-E2E-002 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | covered |
| defense Provera 3 — buy stock/futures + approval + portfolio | TC-C3-E2E-003 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle | covered |
| defense Provera 4 — buy & exercise option | TC-C3-E2E-004 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| defense Provera 5 — tax end-to-end | TC-C3-E2E-005 | wf_tax_collection_test.go::TestWF_TaxCollectionCycle; tax_test.go::TestTax_CollectTax | covered |
```

Summary: 92 test cases (TC-C3-*) across exchanges/hours, listings+market-data, client-visibility, all order types × direction × AON × margin with pricing/commission formulas, agent limits & supervisor approval (incl. once-only/illegal-transition/settlement-decline-only), order-review & my-orders portals + cancel + audit, portfolio (P/L, sell, make-public, exercise, dividend history), dividends, capital-gains tax, DCA, watchlist, price alerts, actuary management, and the 5 defense provere.

Notable gaps (NO-ENDPOINT / partial): exchange-closed "Berza je zatvorena" rejection is not server-enforced (orders accepted, fill deferred); client forex/option visibility restriction is UI-only; margin prerequisites (permission + credit/cash ≥ IMC) are not enforced; the approval gate is a conjunction (needApproval AND over-limit) rather than the spec's disjunction; dividends are admin-declared+manual-payout (no quarterly yield/4 auto-cron); and tax PDF export, year-filter report, and profit-discrepancy flagging have no endpoints.
