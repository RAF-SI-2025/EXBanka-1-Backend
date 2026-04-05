# Platform Hardening & Evolution Design Spec

**Date:** 2026-04-04
**Scope:** Concurrency fixes, audit trails, auth enhancement, Redis optimization, InfluxDB integration, API versioning, seeder update

This spec covers 7 functional areas that will each become a separate implementation plan. The `/api/` routes are frozen for backward compatibility with the existing frontend. All new functionality targets `/api/v1/`.

---

## 1. Concurrency & Data Integrity

### 1.1 ExchangeRate Missing BeforeUpdate Hook

**Bug:** `exchange-service/internal/model/exchange_rate.go` has a `Version` field but no `BeforeUpdate` hook. The repository manually increments version in a map update, bypassing GORM's optimistic lock enforcement.

**Fix:** Add the standard `BeforeUpdate` hook:

```go
func (e *ExchangeRate) BeforeUpdate(tx *gorm.DB) error {
    tx.Statement.Where("version = ?", e.Version)
    e.Version++
    return nil
}
```

Then refactor `exchange_rate_repository.go` `UpsertInTx` to load the struct and call `tx.Save()` instead of `tx.Model(&existing).Updates(map...)`, and check `RowsAffected == 0` for optimistic lock conflict.

### 1.2 Stock Service Concurrency Audit

Stock-service models (Stock, Listing, FuturesContract, Option, ForexPair, Holding, Order) all have Version fields with BeforeUpdate hooks. Verify:

- All `db.Save()` calls check `RowsAffected == 0`
- Order execution (buy/sell) uses transactions with `SELECT FOR UPDATE` on holdings
- Portfolio updates (holding quantity changes) are atomic
- OTC trades lock both buyer and seller holdings in a single transaction
- Option exercise locks the holding before modifying

### 1.3 Saga Startup Recovery

**Current:** `transaction-service` starts a background recovery goroutine (`StartCompensationRecovery`) that runs every 5 minutes but does NOT run an immediate check on startup.

**Fix:** Add an immediate recovery pass at startup before the ticker loop begins. Log the count of recovered/pending sagas at INFO level so operators can see if the service restarted with orphaned work.

### 1.4 Ledger Consistency Check

Account-service's ledger is append-only (immutable entries). Add a startup validation that:
- Sums all ledger entries per account and compares against current `Account.Balance`
- Logs WARNING for any mismatches (does not auto-correct — mismatches indicate a bug)
- Runs once on startup, not continuously

---

## 2. Audit Trails & Field-Level Changelog

### 2.1 Design

Each service that needs auditing gets an append-only `changelog` table:

```sql
CREATE TABLE changelogs (
    id            BIGSERIAL PRIMARY KEY,
    entity_type   VARCHAR(64)  NOT NULL,  -- e.g., "account", "employee", "client"
    entity_id     BIGINT       NOT NULL,
    action        VARCHAR(32)  NOT NULL,  -- "create", "update", "delete", "status_change"
    field_name    VARCHAR(128),           -- NULL for create/delete actions
    old_value     TEXT,                   -- JSON-encoded, NULL for creates
    new_value     TEXT,                   -- JSON-encoded, NULL for deletes
    changed_by    BIGINT       NOT NULL,  -- user_id from JWT (propagated via gRPC metadata)
    changed_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    reason        TEXT                    -- optional, for approval/rejection reasons
);

CREATE INDEX idx_changelog_entity ON changelogs(entity_type, entity_id);
CREATE INDEX idx_changelog_changed_by ON changelogs(changed_by);
CREATE INDEX idx_changelog_changed_at ON changelogs(changed_at);
```

### 2.2 Services That Need Changelogs

| Service | Entity Types | Key Operations Tracked |
|---------|-------------|----------------------|
| **account-service** | account | limit changes (daily/monthly/transfer), status changes (active/inactive), name changes |
| **user-service** | employee, employee_limit | role assignments, permission changes, limit changes, profile updates |
| **client-service** | client, client_limit | limit changes, profile updates |
| **credit-service** | loan_request, loan | approval/rejection (with approver + reason), status transitions |
| **card-service** | card | block/unblock/deactivate (with who authorized), PIN reset |
| **auth-service** | session | login events (already has LoginAttempt — enhance with device details), password changes, device activations/deactivations |

### 2.3 Services That Do NOT Need Changelogs

| Service | Reason |
|---------|--------|
| **notification-service** | Delivers messages, no user-facing state to audit |
| **exchange-service** | Rates are external data synced automatically, not user-modified |
| **verification-service** | Challenges are ephemeral (expire in minutes) |
| **stock-service** | Has its own order/transaction/capital-gain history — sufficient audit trail |
| **transaction-service** | Has saga logs + account ledger entries — sufficient audit trail |

### 2.4 changed_by Propagation

The API gateway extracts `user_id` from JWT context and sets it as gRPC metadata on every outbound call:

```go
md := metadata.Pairs("x-changed-by", strconv.FormatInt(userID, 10))
ctx = metadata.NewOutgoingContext(ctx, md)
```

Each service's handler layer reads this metadata and passes it to the service layer. The service layer passes it to the changelog repository when recording entries.

For system-initiated changes (cron jobs, background goroutines), use `changed_by = 0` as a sentinel value meaning "system". This avoids nullable columns while clearly distinguishing automated from human-initiated changes.

### 2.5 Kafka Events

Each changelog entry publishes a Kafka event to `<service>.changelog` (e.g., `account.changelog`, `user.changelog`). Payload matches the changelog table row. This allows future consumers (analytics, compliance dashboards) to react to changes.

---

## 3. Auth Enhancement & Per-Device Sessions

### 3.1 Refresh Token Device Binding

Add fields to `RefreshToken` model:

```go
type RefreshToken struct {
    // ... existing fields ...
    SessionID  *int64  // FK to ActiveSession
    IPAddress  string  // IP at token issuance
    UserAgent  string  // User-Agent header at issuance
}
```

On login and refresh, the gateway forwards `X-Forwarded-For` (or remote IP) and `User-Agent` as gRPC metadata. Auth-service stores them with the refresh token.

### 3.2 ActiveSession Integration

The `ActiveSession` model already exists but is unused. Integrate it:

- **On login:** Create an `ActiveSession` record with IP, user agent, system type. Link the new refresh token to this session via `SessionID`.
- **On refresh:** Update `ActiveSession.LastActiveAt` and IP/user-agent (in case they changed). Create new refresh token linked to the same session.
- **On logout:** Revoke the refresh token AND set `ActiveSession.RevokedAt`.
- **On explicit session revocation:** Revoke all refresh tokens with that `SessionID`, set `ActiveSession.RevokedAt`.

### 3.3 Per-Device Revocation

Current behavior: `DeactivateDevice()` revokes ALL refresh tokens for the account. 

New behavior:
- `RevokeSession(sessionID)` revokes only refresh tokens linked to that session
- `RevokeAllSessions(accountID)` revokes all (backward compatible with current behavior)
- Mobile device deactivation: look up sessions where `device_id` matches (from JWT claims stored in ActiveSession), revoke only those sessions
- Add `DeviceID` field to `ActiveSession` model so mobile sessions can be linked to their device
- Old `/api/auth/logout` endpoint continues to revoke only the current token (unchanged)

### 3.4 New V1 Endpoints

```
GET    /api/v1/me/sessions          — List active sessions (IP, user agent, last active, device type)
DELETE /api/v1/me/sessions/:id      — Revoke a specific session
DELETE /api/v1/me/sessions          — Revoke all sessions except current
```

### 3.5 Login History Enhancement

The existing `LoginAttempt` model tracks email + IP + success. Enhance for v1:

- Add `user_agent` field to `LoginAttempt`
- Add `device_type` field (parsed from user agent: "browser", "mobile", "api")
- New v1 endpoint: `GET /api/v1/me/login-history?page=1&page_size=20`

### 3.6 Mobile /api/me Compatibility

Current mobile auth uses `MobileAuthMiddleware` for `/api/mobile/*` routes. The `/api/me/*` routes use `AnyAuthMiddleware` which accepts mobile tokens (they have valid JWT with `system_type`). Mobile clients CAN use `/api/me/*` routes today — no changes needed. Verify this works in integration tests.

---

## 4. Redis Optimization

### 4.1 Exchange-Service: Cache Exchange Rates

Exchange rates are queried on every cross-currency transaction. Currently hits PostgreSQL each time.

- Add Redis cache with key pattern `rate:<from_currency>:<to_currency>`
- TTL: 5 minutes (rates sync every 6 hours, so 5 min is conservative)
- Invalidate on rate sync (delete pattern `rate:*`)
- Graceful degradation: fall back to DB on cache miss or Redis unavailable

### 4.2 Stock-Service: Cache Security Data

Listings and security details are browsed frequently.

- Cache individual securities: `security:<type>:<id>` with 2-minute TTL
- Cache listing lists: `listings:<exchange_id>:page:<n>` with 1-minute TTL
- Invalidate on price refresh
- Graceful degradation: fall back to DB

### 4.3 Card-Service: Activate Existing Redis

Redis is already initialized but unused. Add caching for:
- `card:id:<id>` — individual card lookups (3-minute TTL)
- Invalidate on any card status change (block, unblock, deactivate)

### 4.4 Account-Service: Activate Existing Redis

Redis is already initialized but unused. Add caching for:
- `account:id:<id>` — individual account lookups (2-minute TTL)
- `account:number:<num>` — by account number (2-minute TTL)
- Invalidate on balance change, status change, limit change
- **Do NOT cache balances for authoritative checks** — balance reads for transactions must always hit DB with `SELECT FOR UPDATE`

### 4.5 Remove Unused Redis

- **transaction-service**: Remove Redis initialization (transactions are write-heavy, caching adds complexity without benefit — reads go through account-service)
- **credit-service**: Remove Redis initialization (loan data is not frequently read in hot paths)

---

## 5. Stock Service & InfluxDB

### 5.1 InfluxDB Setup

- Add InfluxDB 2.x to `docker-compose.yml` (port 8086)
- Create organization `exbanka`, bucket `stock_prices` with 365-day retention
- Environment variables: `INFLUX_URL`, `INFLUX_TOKEN`, `INFLUX_ORG`, `INFLUX_BUCKET`

### 5.2 Dual-Write Architecture

PostgreSQL remains the source of truth for daily snapshots (`ListingDailyPriceInfo`). InfluxDB handles high-frequency data:

- **Write path:** On each price refresh (every N minutes), write a point to InfluxDB:
  ```
  measurement: security_price
  tags: listing_id, security_type, ticker, exchange
  fields: price, high, low, volume, change
  timestamp: time of refresh
  ```
- **Read path:** New candle endpoints query InfluxDB. Existing daily history endpoints continue to query PostgreSQL.
- **Fallback:** If InfluxDB is unavailable, log warning and skip write (non-fatal, like Redis).

### 5.3 Reusable Client Package

Create `contract/influx/` with:
- `Client` struct wrapping the InfluxDB Go client
- `NewClient(url, token, org, bucket)` constructor
- `WritePoint(measurement, tags, fields, timestamp)` method
- `Query(flux string)` method
- Graceful degradation (nil client = no-op)

This allows other services to import and use InfluxDB later without duplicating setup code.

### 5.4 New V1 Endpoints

```
GET /api/v1/securities/stocks/:id/candles?interval=1m&from=...&to=...
GET /api/v1/securities/futures/:id/candles?interval=5m&from=...&to=...
GET /api/v1/securities/forex/:id/candles?interval=1h&from=...&to=...
```

Supported intervals: `1m`, `5m`, `15m`, `1h`, `4h`, `1d`. Default: `1h`. Max range: 30 days for minute candles, 365 days for hourly/daily.

### 5.5 Test Mode Verification

Current test mode already:
- Bypasses exchange hours (all exchanges treated as open)
- Skips external API price refresh (prices stay at seed values)
- Order execution engine processes orders normally (debits/credits real accounts)

Verify end-to-end in integration tests: enable testing mode, place order, confirm execution, check account balance changes and holding updates. No new code needed — just test coverage.

---

## 6. API Versioning & Router Restructuring

### 6.1 Router File Structure

```
api-gateway/internal/router/
├── router.go          # Existing /api/ routes (FROZEN — no modifications)
├── router_v1.go       # /api/v1/ routes (mirrors + new endpoints)
└── router_latest.go   # /api/latest → redirect/proxy to /api/v1/
```

### 6.2 V1 Router Design

`router_v1.go` exports a `RegisterV1Routes(r *gin.Engine, deps Dependencies)` function. Initially, it registers the same handler functions as `router.go` for all mirrored routes. Over time, v1 handlers can diverge.

```go
func RegisterV1Routes(r *gin.Engine, deps Dependencies) {
    v1 := r.Group("/api/v1")
    
    // --- Public ---
    auth := v1.Group("/auth")
    auth.POST("/login", deps.AuthHandler.Login)
    // ... mirror all /api/auth/* routes ...
    
    // --- /api/v1/me/* (can diverge from /api/me/*) ---
    me := v1.Group("/me", deps.AnyAuthMiddleware)
    me.GET("/sessions", deps.AuthHandler.ListSessions)         // NEW
    me.DELETE("/sessions/:id", deps.AuthHandler.RevokeSession)  // NEW
    me.DELETE("/sessions", deps.AuthHandler.RevokeAllSessions)  // NEW
    me.GET("/login-history", deps.AuthHandler.LoginHistory)     // NEW
    // ... mirror all existing /api/me/* routes ...
    
    // --- New v1-only endpoints ---
    // Audit history
    v1.GET("/accounts/:id/changelog", deps.AccountHandler.GetChangelog)
    v1.GET("/employees/:id/changelog", deps.UserHandler.GetChangelog)
    v1.GET("/clients/:id/changelog", deps.ClientHandler.GetChangelog)
    v1.GET("/cards/:id/changelog", deps.CardHandler.GetChangelog)
    v1.GET("/loans/:id/changelog", deps.CreditHandler.GetChangelog)
    
    // Intraday candles
    v1.GET("/securities/stocks/:id/candles", deps.StockHandler.GetCandles)
    v1.GET("/securities/futures/:id/candles", deps.StockHandler.GetFuturesCandles)
    v1.GET("/securities/forex/:id/candles", deps.StockHandler.GetForexCandles)
}
```

### 6.3 /api/latest Alias

```go
func RegisterLatestRoutes(r *gin.Engine) {
    r.Any("/api/latest/*path", func(c *gin.Context) {
        c.Request.URL.Path = "/api/v1" + c.Param("path")
        r.HandleContext(c)
    })
}
```

### 6.4 Per-Version REST API Docs

- `docs/api/REST_API.md` — frozen, documents `/api/` routes only
- `docs/api/REST_API_v1.md` — documents `/api/v1/` routes, starts as a copy of REST_API.md with new endpoints added and marked `(optional)` or `(new in v1)`

### 6.5 Test Workflow Adjustments

- Existing integration tests in `test-app/workflows/` continue to test `/api/` routes (regression)
- New test files suffixed `_v1_test.go` test `/api/v1/` routes
- Shared helpers work with configurable base path

---

## 7. Seeder & Bootstrapping

### 7.1 Default Test Client

After creating the admin employee (existing flow), the seeder creates a default client:

```
Email:    admin+testclient@admin.com
Password: Admin1234! (same as admin)
```

Flow:
1. Check if client already exists (login attempt or lookup by email)
2. If not, call `client-service.CreateClient()` via gRPC with test client details
3. Auth-service creates account + activation token, publishes to Kafka
4. Seeder reads Kafka for activation email matching the client email
5. Call `auth-service.ActivateAccount(token, password)` to activate
6. Log success, exit

### 7.2 Idempotency

Seeder checks existence before creating. If admin or test client already exists and can log in, skip creation. Same pattern as the existing admin flow.

---

## Non-Goals

- No changes to existing `/api/` route behavior (frozen for frontend team)
- No InfluxDB in services other than stock-service (designed for future adoption)
- No blanket Redis across all services (only where read patterns justify it)
- No event sourcing or CQRS — this is append-only changelog, not full event store
- No UI for audit trails in this phase — only API endpoints
