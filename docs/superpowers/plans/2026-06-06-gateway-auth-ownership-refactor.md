# Gateway / Auth / Ownership Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-scope the api-gateway to its proper responsibilities — rate limiting, local JWT verification (asymmetric ES256), gateway-owned token denylists, edge logging, REST↔gRPC mapping — and move resource-ownership enforcement out of the gateway into each owning service.

**Architecture:** Five workstreams across the gateway, auth-service, and every resource-owning service. auth-service signs access tokens with an **ES256 private key** and serves its **public keys over gRPC** (`GetSigningKeys`, JWKS-style, with rotation). The api-gateway verifies tokens **locally** with the cached public key — no per-request gRPC hop — then consults two **Redis denylists**: a hard-revocation set (`jti`/`sid`) and a per-principal `stale_after` timestamp (force-refresh on claims change). Ownership checks are deleted from the gateway; each service enforces ownership of its own resources using the **caller identity propagated over gRPC metadata**. The work is sequenced so there is never a window where no one checks ownership.

**Tech Stack:** Go 1.25, Gin, gRPC, `golang-jwt/jwt/v5` (ES256), `redis/go-redis/v9`, `crypto/ecdsa` (P-256), `log/slog`, miniredis + testify (tests).

---

## Phase map & safe ordering

| Phase | Scope | Services touched | Safe to do independently? |
|---|---|---|---|
| **A** | Rate limiting + edge logging | api-gateway only | ✅ Yes — self-contained. **IMPLEMENT NOW.** |
| **B** | Asymmetric ES256 JWT + key rotation + 2 denylists | auth-service + api-gateway | Coordinated cutover (gateway local-verify is dead until auth issues ES256). Do at the **auth-service pass** (the next service). |
| **C** | Ownership relocation (OWN-1) + identity propagation | api-gateway + every owning service | **Per-resource atomic** (service starts enforcing in the SAME change the gateway stops). Do as each owning service is reviewed. |
| **D** | Claims-invalidation `stale_after` writes (direct Redis) | user-service, client-service, auth-service | After B (depends on the `stale_after` schema). |
| **X** | Cross-cutting: rewrite CLAUDE.md ownership rule, docs, VERSION | repo-wide | Folded into B/C as they land. |

**Why A is the only "now" phase:** B/C/D are inherently multi-service. B is a coordinated token-format cutover; doing only the gateway half would break every login. C must move each ownership check atomically (gateway-stops ⇄ service-starts) or it opens the money holes the cross-bank hardening closed (`project_crossbank_adversarial_findings`, ROUTE-CHANGES.md §6/§8). So A ships now; B–D get task-level expansion (and exact per-file code) when we reach each service in the service-by-service review — their **design, contracts, and task lists are fully specified below** so there are no open design questions, only code to write against known service internals.

---

# PHASE A — Gateway rate limiting + edge logging (IMPLEMENT NOW)

**Files:**
- Create: `api-gateway/internal/middleware/ratelimit.go`
- Create: `api-gateway/internal/middleware/ratelimit_test.go`
- Create: `api-gateway/internal/middleware/request_logger.go`
- Create: `api-gateway/internal/middleware/request_logger_test.go`
- Modify: `api-gateway/internal/config/config.go` (rate-limit env knobs)
- Modify: `api-gateway/internal/router/router_v3.go` (build limiter store, wire middleware, swap `gin.Default()`→`gin.New()`)
- Modify: `api-gateway/cmd/main.go` (pass the shared `*redis.Client` into the router; it already constructs one at `main.go:275`)
- Modify: `docs/api/REST_API_v3.md` (document global 429 + auth-route throttling)
- Modify: `VERSION` (MINOR bump — additive failure mode)

### Task A1: Redis-backed rate-limit middleware

**Files:**
- Create: `api-gateway/internal/middleware/ratelimit.go`
- Test: `api-gateway/internal/middleware/ratelimit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// api-gateway/internal/middleware/ratelimit_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestRateLimit_AllowsUnderLimitThenBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedis(t)

	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "test", Limit: 3, Window: time.Minute},
		func(c *gin.Context) string { return "fixed-key" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, 200, do())
	require.Equal(t, 200, do())
	require.Equal(t, 200, do())
	require.Equal(t, 429, do()) // 4th in the window is blocked
}

func TestRateLimit_DisabledWhenLimitZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedis(t)
	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "off", Limit: 0, Window: time.Minute},
		func(c *gin.Context) string { return "k" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	for i := 0; i < 50; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, 200, w.Code)
	}
}

func TestRateLimit_FailOpenOnRedisError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close() // redis now unreachable

	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "t", Limit: 1, Window: time.Minute},
		func(c *gin.Context) string { return "k" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, 200, w.Code) // fail-open: a Redis outage must not lock everyone out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRateLimit -v`
Expected: FAIL — `RateLimit` / `RateLimitRule` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// api-gateway/internal/middleware/ratelimit.go
package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimitRule is one fixed-window bucket. Limit<=0 disables the rule.
type RateLimitRule struct {
	Name   string        // bucket namespace, keeps different rules from colliding
	Limit  int           // max requests per window per key
	Window time.Duration // window length
}

// fixedWindowScript atomically increments the window counter and sets the
// expiry only on first increment, so the window is fixed (not sliding).
var fixedWindowScript = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return c
`)

// RateLimit returns a gin middleware enforcing one fixed-window rule, keyed by
// keyFn(c). On exceed it writes 429 with the standard error envelope and a
// Retry-After header. It FAILS OPEN: any Redis error lets the request through
// (a limiter outage must never become a global outage).
func RateLimit(rdb *redis.Client, rule RateLimitRule, keyFn func(*gin.Context) string) gin.HandlerFunc {
	windowMillis := strconv.FormatInt(rule.Window.Milliseconds(), 10)
	return func(c *gin.Context) {
		if rule.Limit <= 0 {
			c.Next()
			return
		}
		key := "ratelimit:" + rule.Name + ":" + keyFn(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()
		n, err := fixedWindowScript.Run(ctx, rdb, []string{key}, windowMillis).Int64()
		if err != nil {
			c.Next() // fail open
			return
		}
		if n > int64(rule.Limit) {
			c.Header("Retry-After", strconv.Itoa(int(rule.Window.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "rate_limited", "message": "too many requests, slow down"},
			})
			return
		}
		c.Next()
	}
}

// ClientIPKey keys a bucket by client IP (per-IP limiting).
func ClientIPKey(c *gin.Context) string { return c.ClientIP() }

// RouteIPKey keys by client IP + matched route template, so a strict bucket on
// one route doesn't consume another route's budget.
func RouteIPKey(c *gin.Context) string { return c.ClientIP() + "|" + c.FullPath() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRateLimit -v`
Expected: PASS (3×200 then 429; disabled rule never blocks; fail-open returns 200).

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/middleware/ratelimit.go api-gateway/internal/middleware/ratelimit_test.go
git commit -m "feat(gateway): Redis fixed-window rate-limit middleware (fail-open)"
```

### Task A2: Rate-limit config knobs

**Files:**
- Modify: `api-gateway/internal/config/config.go`
- Test: `api-gateway/internal/config/config_test.go`

- [ ] **Step 1: Write the failing test** (append to `config_test.go`)

```go
func TestLoad_RateLimitDefaults(t *testing.T) {
	t.Setenv("RATE_LIMIT_GLOBAL_PER_MIN", "")
	t.Setenv("RATE_LIMIT_LOGIN_PER_5MIN", "")
	t.Setenv("RATE_LIMIT_RESET_PER_5MIN", "")
	cfg := Load()
	require.Equal(t, 3000, cfg.RateLimitGlobalPerMin) // generous: FE polls many routes/sec
	require.Equal(t, 20, cfg.RateLimitLoginPer5Min)   // strict
	require.Equal(t, 5, cfg.RateLimitResetPer5Min)    // strict
}

func TestLoad_RateLimitOverride(t *testing.T) {
	t.Setenv("RATE_LIMIT_GLOBAL_PER_MIN", "100")
	require.Equal(t, 100, Load().RateLimitGlobalPerMin)
}
```

(Add `import "github.com/stretchr/testify/require"` if not present.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api-gateway && go test ./internal/config/ -run TestLoad_RateLimit -v`
Expected: FAIL — fields undefined.

- [ ] **Step 3: Write minimal implementation**

Add fields to the `Config` struct:

```go
	// Rate limiting (Phase A). A value of 0 disables that bucket.
	RateLimitGlobalPerMin int // generous per-IP safety ceiling across ALL routes
	RateLimitLoginPer5Min int // strict per-IP bucket on POST /auth/login
	RateLimitResetPer5Min int // strict per-IP bucket on POST /auth/password/reset-request
```

Add to the `Load()` return literal:

```go
		RateLimitGlobalPerMin: getEnvInt("RATE_LIMIT_GLOBAL_PER_MIN", 3000),
		RateLimitLoginPer5Min: getEnvInt("RATE_LIMIT_LOGIN_PER_5MIN", 20),
		RateLimitResetPer5Min: getEnvInt("RATE_LIMIT_RESET_PER_5MIN", 5),
```

Add the helper:

```go
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
```

(Add `"strconv"` to imports.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api-gateway && go test ./internal/config/ -run TestLoad_RateLimit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/config/config.go api-gateway/internal/config/config_test.go
git commit -m "feat(gateway): rate-limit config knobs (generous global + strict auth)"
```

### Task A3: Structured edge logging + request-id middleware

**Files:**
- Create: `api-gateway/internal/middleware/request_logger.go`
- Test: `api-gateway/internal/middleware/request_logger_test.go`

- [ ] **Step 1: Write the failing test**

```go
// api-gateway/internal/middleware/request_logger_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestLogger_SetsRequestIDHeaderAndContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	var seen string
	r.GET("/x", func(c *gin.Context) {
		seen = RequestID(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	require.NotEmpty(t, seen)
	require.Equal(t, seen, w.Header().Get("X-Request-Id"))
}

func TestRequestLogger_HonorsInboundRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	r.ServeHTTP(w, req)

	require.Equal(t, "abc-123", w.Header().Get("X-Request-Id"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRequestLogger -v`
Expected: FAIL — `RequestLogger` / `RequestID` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// api-gateway/internal/middleware/request_logger.go
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const requestIDKey = "request_id"

// RequestLogger assigns a request id (honoring an inbound X-Request-Id, else a
// fresh UUID), exposes it on the gin context + response header, and emits one
// structured slog line per request after it completes. Pair with gin.Recovery().
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set(requestIDKey, rid)
		c.Header("X-Request-Id", rid)

		start := time.Now()
		c.Next()

		slog.Info("http_request",
			"request_id", rid,
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"principal_id", c.GetInt64("principal_id"),
		)
	}
}

// RequestID returns the per-request id set by RequestLogger ("" if absent).
func RequestID(c *gin.Context) string { return c.GetString(requestIDKey) }
```

Then promote `github.com/google/uuid` from indirect to a direct dependency:

Run: `cd api-gateway && go get github.com/google/uuid && go mod tidy`

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api-gateway && go test ./internal/middleware/ -run TestRequestLogger -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/middleware/request_logger.go api-gateway/internal/middleware/request_logger_test.go api-gateway/go.mod api-gateway/go.sum
git commit -m "feat(gateway): structured request logging + X-Request-Id middleware"
```

### Task A4: Wire middleware into the router

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`
- Modify: `api-gateway/cmd/main.go`
- Test: `api-gateway/internal/router/router_v3_test.go`

- [ ] **Step 1: Write the failing test** (append to `router_v3_test.go`)

```go
func TestNewRouter_GlobalLimiterAnd429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedis(t) // reuse helper; if not in package, inline a miniredis client
	r := NewRouter(RouterOpts{Redis: rdb, GlobalPerMin: 2})
	r.GET("/api/v3/version", func(c *gin.Context) { c.Status(200) })

	do := func() int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v3/version", nil))
		return w.Code
	}
	require.Equal(t, 200, do())
	require.Equal(t, 200, do())
	require.Equal(t, 429, do())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api-gateway && go test ./internal/router/ -run TestNewRouter_GlobalLimiter -v`
Expected: FAIL — `NewRouter` signature mismatch / `RouterOpts` undefined.

- [ ] **Step 3: Write minimal implementation**

Change `NewRouter` in `router_v3.go` to accept options and wire the middleware. Replace `gin.Default()` with `gin.New()` + `Recovery` + our logger so we don't double-log:

```go
// RouterOpts carries cross-cutting wiring for the engine.
type RouterOpts struct {
	Redis        *redis.Client
	GlobalPerMin int
	LoginPer5Min int
	ResetPer5Min int
}

func NewRouter(opts RouterOpts) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogger())
	r.Use(apimetrics.GinMiddleware())
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-Id", "X-Device-Id", "X-Device-Signature"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-Id"},
		AllowCredentials: false,
	}))
	// Generous per-IP ceiling across every route (does NOT throttle normal FE
	// polling; only catches runaway/abusive clients).
	if opts.Redis != nil {
		r.Use(middleware.RateLimit(opts.Redis,
			middleware.RateLimitRule{Name: "global", Limit: opts.GlobalPerMin, Window: time.Minute},
			middleware.ClientIPKey))
	}
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	return r
}
```

In `SetupV3`, attach the strict per-IP buckets to the two sensitive auth routes (store the opts on the engine or pass them through; simplest is to add the buckets in `SetupV3` via a small `RouterOpts` field threaded on `*Handlers`, OR register them inside `NewRouter` before returning by giving `SetupV3` access to opts). Concretely, add the strict middleware where the auth group is built:

```go
	auth := v3.Group("/auth")
	{
		login := []gin.HandlerFunc{h.Auth.Login}
		if h.RL.Redis != nil {
			login = append([]gin.HandlerFunc{middleware.RateLimit(h.RL.Redis,
				middleware.RateLimitRule{Name: "login", Limit: h.RL.LoginPer5Min, Window: 5 * time.Minute},
				middleware.RouteIPKey)}, login...)
		}
		auth.POST("/login", login...)
		// ... reset-request gets the "reset" bucket the same way ...
	}
```

(Thread an `RL RouterOpts` field on the `Handlers` struct in `handlers.go`, set from `main.go`. Add `"time"` and the redis import to `router_v3.go`.)

In `cmd/main.go`, pass the already-constructed `redisClient` (`main.go:275`) and config knobs:

```go
	r := router.NewRouter(router.RouterOpts{
		Redis:        redisClient,
		GlobalPerMin: cfg.RateLimitGlobalPerMin,
		LoginPer5Min: cfg.RateLimitLoginPer5Min,
		ResetPer5Min: cfg.RateLimitResetPer5Min,
	})
	// ... deps ...
	deps.RL = router.RouterOpts{Redis: redisClient, LoginPer5Min: cfg.RateLimitLoginPer5Min, ResetPer5Min: cfg.RateLimitResetPer5Min}
	h := router.NewHandlers(deps)
	router.SetupV3(r, h)
```

(Update every other caller of `NewRouter()` — there are calls in `router_v3_test.go`, `handlers_test.go`; switch them to `NewRouter(router.RouterOpts{})` which yields no limiter, preserving existing behavior.)

- [ ] **Step 4: Run tests**

Run: `cd api-gateway && go build ./... && go test ./internal/router/ -v`
Expected: build OK; new limiter test passes; existing router tests still pass (they pass an empty `RouterOpts`, so no limiter).

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/router/ api-gateway/cmd/main.go
git commit -m "feat(gateway): wire global + strict-auth rate limits and request logger"
```

### Task A5: Docs, lint, version, full test run

- [ ] **Step 1:** Document in `docs/api/REST_API_v3.md` — add a short "Rate limiting" subsection: a generous per-IP global ceiling (`RATE_LIMIT_GLOBAL_PER_MIN`, default 3000/min) applies to all routes; `POST /auth/login` (20/5min) and `POST /auth/password/reset-request` (5/5min) are stricter; exceeding any returns **429 `rate_limited`** with `Retry-After`. Note it is an additive failure mode (no success contract changed). Also note the new `X-Request-Id` response header.
- [ ] **Step 2:** Add the three `RATE_LIMIT_*` env vars to `docker-compose.yml` + `docker-compose-remote.yml` api-gateway `environment:` blocks (with the documented defaults), per the Docker Compose Requirement.
- [ ] **Step 3:** Bump `VERSION` (MINOR) and sync `api-gateway/internal/version/version.go`.
- [ ] **Step 4:** Run `cd api-gateway && golangci-lint run ./...` — fix any new warnings in touched files.
- [ ] **Step 5:** Run `cd api-gateway && go test ./...` — all green.
- [ ] **Step 6:** Commit.

```bash
git add docs/api/REST_API_v3.md docker-compose.yml docker-compose-remote.yml VERSION api-gateway/internal/version/version.go
git commit -m "docs(gateway): rate limiting + request-id; bump VERSION"
```

---

# PHASE B — Asymmetric ES256 JWT + key rotation + denylists (auth-service pass)

> Executed during the auth-service review. Design + contracts are frozen here; task-level steps get exact code once we read `auth-service/internal/handler`, the auth proto, and the mobile/websocket verify sites.

**Shared contracts (define first, in `contract/`):**
- `contract/proto/auth.proto` — add to `AuthService`:
  ```proto
  rpc GetSigningKeys(GetSigningKeysRequest) returns (GetSigningKeysResponse);
  message GetSigningKeysRequest {}
  message JWK { string kid = 1; string alg = 2; string pem_public_key = 3; bool primary = 4; }
  message GetSigningKeysResponse { repeated JWK keys = 1; } // current first, previous during overlap
  ```
- `contract/authredis/keys.go` (NEW shared pkg): the Redis key schema used by BOTH auth (writer) and gateway (reader):
  - `RevokedJTIKey(jti string) string` → `"revoked:jti:"+jti`
  - `RevokedSessionKey(sid string) string` → `"revoked:sid:"+sid`
  - `StaleAfterKey(principalType string, principalID int64) string` → `"stale_after:"+principalType+":"+id`
  - helpers: `IsStale(tokenIATUnix, staleAfterUnix int64) bool { return tokenIATUnix < staleAfterUnix }`

**B1 — auth-service signs ES256 (`auth-service/internal/service/jwt_service.go`):**
- Replace `secret []byte` with an `*ecdsa.PrivateKey` (P-256) + a `kid` string + a ring of retired public keys.
- `GenerateAccessToken` / `GenerateMobileAccessToken`: `jwt.NewWithClaims(jwt.SigningMethodES256, claims)`; set `token.Header["kid"] = s.kid`; add a `Sid` claim (new `sid` field on `Claims`, a stable per-session id = the refresh-token session id, so RevokeSession can target it). `jti` (`RegisteredClaims.ID`) and `iat` already exist — keep.
- Key material: load PEM from env (`JWT_EC_PRIVATE_KEY` / `JWT_EC_KID`); if unset in dev, generate one at startup and log the public PEM. Keep the previous public key+kid for the rotation overlap window.
- `ValidateToken` stays (used by `GetMe` employee/client branches and any internal caller) but parses ES256 with the public key.

**B2 — `GetSigningKeys` gRPC (`auth-service/internal/handler`):** return `{kid, alg:"ES256", pem_public_key, primary}` for the current key (+ previous while overlapping). No auth required at the gRPC layer (public keys are public); it's only reachable in-cluster.

**B3 — auth writes the revocation denylist:** in the Logout / RevokeSession / RevokeAllSessions / password-change service paths, write `revoked:jti:<jti>` and/or `revoked:sid:<sid>` to Redis with TTL = access-token lifetime. (RevokeAllSessions for a principal can instead bump `stale_after` to "now" AND revoke refresh tokens — pick per semantics: logout-everywhere should hard-revoke.)

**B4 — gateway verifies locally (`api-gateway/internal/middleware/auth.go` + new `internal/jwks/`):**
- Add `github.com/golang-jwt/jwt/v5` to the gateway.
- New `internal/jwks/cache.go`: fetches `GetSigningKeys` at startup, caches `kid→*ecdsa.PublicKey`, refreshes on a ticker (e.g. 10 min) and on a `kid` cache-miss; honors `ctx` cancellation.
- Rewrite `AuthMiddleware` / `AnyAuthMiddleware` / `MobileAuthMiddleware` / `RequireDeviceSignature` (mobile_auth.go) / websocket auth (`websocket_handler.go`) to:
  1. parse+verify the token locally (ES256, key by `kid`, check `exp`/`nbf`),
  2. if `jti`/`sid` ∈ revoked set → **401 `unauthorized`** (logout),
  3. else if `IsStale(iat, stale_after[principal])` → **401 `token_expired`** (FE refreshes — see 1.3b),
  4. else set the same context keys `setTokenContext` sets today (map claims → context).
- Keep a fallback to gRPC `ValidateToken` ONLY for device-signature HMAC (which needs auth-service state), not for plain token validation.

**B5 — config + compose:** auth-service gets `JWT_EC_PRIVATE_KEY`, `JWT_EC_KID`, `JWT_KEY_OVERLAP` ; gateway gets nothing new for keys (fetched via gRPC) but needs `AUTH_GRPC_ADDR` (already present). Update both compose files.

**Tests:** auth jwt_service unit (sign→parse round-trip, kid header, ES256 rejects HS256); gateway jwks cache (fetch, refresh, kid-miss refetch); gateway auth middleware (valid passes; revoked→401 unauthorized; stale→401 token_expired; bad-sig→401; expired→401). Integration: login→call protected route with no per-request auth gRPC hop; revoke→next call 401; permission change bumps stale_after→next call 401 token_expired→refresh yields new perms.

**Docs/Version:** ROUTE-CHANGES.md note (token format internal; revoked vs stale 401 semantics), CLAUDE.md "Token types" section updated to ES256 + denylists, VERSION MINOR.

---

# PHASE C — Ownership relocation OWN-1 (per owning service)

> Executed atomically per resource as each owning service is reviewed. NEVER remove a gateway check before its service enforces.

**Shared plumbing (define once, first service that needs it):**
- `contract/identity/metadata.go` (NEW): gRPC metadata propagation of the caller identity.
  - Keys: `x-principal-type`, `x-principal-id`, `x-on-behalf-client-id`, `x-acting-employee-id`.
  - `Inject(ctx, ResolvedIdentity) context.Context` (gateway side) and `FromContext(ctx) (Identity, bool)` (service side). Mirrors the existing `contract/changelog` x-changed-by pattern (`changed_by.go`).
- Gateway: a single helper (extend `GRPCContextWithChangedBy`) that injects identity metadata on EVERY gRPC call (not just mutations), built from `c.MustGet("identity")`.

**Per-service pattern (repeat for account, card, credit, stock, transaction):**
1. Service `handler`/`service` layer reads `identity.FromContext(ctx)`.
2. Before acting on a resource it owns, enforce:
   - client principal → `resource.owner_id == principal_id` and not bank-owned, else `codes.NotFound` (existence hiding → gateway 404).
   - employee, no on-behalf → resource must be bank-owned, else `codes.PermissionDenied` (→ 403).
   - employee + on-behalf → `resource.owner_id == on_behalf_client_id` (and the gateway already gated the `*.on_behalf_client` permission), else `codes.PermissionDenied`.
3. In the gateway, DELETE the matching pre-fetch + check (`enforceOwnership` / `ResolveAndCheckAccount*` / inline `OwnerId !=` / the ownership-only `GetAccount`/`GetHolding`). Collapse the now-single call.
4. Preserve 404(client)/403(employee) — log any deviation in ROUTE-CHANGES.md.

**Gateway deletions inventory** (from SERVICE_REVIEW.md §1.4): `validation.go` `enforceOwnership`/`ResolveAndCheckAccount`/`ResolveAndCheckAccountByNumber`/`checkAccountOwnership`; call sites in `transaction_handler.go` (692,719,772,802,1076), `account_handler.go` (671,716,775), `card_handler.go` (403,761,897,1019), `credit_handler.go` (831,886), `stock_order_handler.go` (106,123,158,413,425), `portfolio_handler.go` (478,528), `otc_options_handler.go` (~475), `peer_tx_dispatcher_handler.go` (109).

**Critical money-path note:** the cross-bank/SI-TX checks (peer_tx_dispatcher, otc exercise strike account) plug real money bugs. When relocating, the destination service (transaction-service outbound init; stock-service OTC) must enforce equivalently — the inbound peer path already enforces inside stock-service, use it as the model. Do these LAST and verify two-stack.

**Tests:** per service, unit tests for all three ownership branches (client-owns, client-foreign→NotFound, employee-bank, employee-on-behalf-match/mismatch). Gateway: assert the old 403/404 still returned (now sourced from the gRPC error mapping). Integration: a client cannot read/act on another client's account/card/loan/holding (404); an employee can only use bank resources unless on-behalf.

**CLAUDE.md:** rewrite the "Resource Ownership Verification Requirement" to the new principle (services enforce; gateway propagates identity) as part of the FIRST service in this phase.

---

# PHASE D — Claims-invalidation `stale_after` writes (direct Redis)

> After Phase B (needs the `contract/authredis` schema). Done as user-service / client-service are reviewed.

For every mutation that changes data baked into a JWT, write `stale_after:<ptype>:<id> = now` (TTL = access-token lifetime) directly to Redis:
- **user-service:** set/replace employee roles, set/replace permissions, set additional permissions, change account-active/lock → bump `stale_after:employee:<id>`.
- **client-service:** activate/deactivate client → bump `stale_after:client:<id>`.
- **auth-service:** password change / account lock it owns → bump (also hard-revoke on logout-everywhere).

Each such service gets a tiny Redis dependency (most already have `redis/go-redis/v9`). Add `REDIS_ADDR` to their config + compose if missing. Publish the existing Kafka event too (unchanged) — the Redis write is the new side-effect, not a replacement.

**Tests:** unit — mutating a permission writes the `stale_after` key with the right value+TTL (miniredis). Integration — change an employee's permission, the employee's next gateway call returns 401 `token_expired`, refresh returns a token with the new permission set.

---

## Self-Review

- **Spec coverage:** SERVICE_REVIEW.md §1.1 (rate limit)→Phase A; §1.2 (asymmetric ES256 + rotation gRPC)→Phase B/B1-B2-B4; §1.3a/b (two denylists, token_expired vs unauthorized)→Phase B3-B4 + the `authredis` schema; §1.4 OWN-1→Phase C; §1.7 (logging)→Phase A3; claims-invalidation direct Redis→Phase D; CLAUDE.md rewrite→Phase C; AUTH-A..D pre-seed→Phase B/D. Covered.
- **Placeholder scan:** Phase A is concrete code. Phases B–D specify exact contracts (proto, Redis keys, function signatures, file paths, error codes) and defer only per-line code to the service pass where the service internals are read — by design for a multi-subsystem effort, not vague TODOs.
- **Type consistency:** `RateLimitRule{Name,Limit,Window}`, `RateLimit(rdb, rule, keyFn)`, `RequestID(c)`, `RouterOpts{Redis,GlobalPerMin,LoginPer5Min,ResetPer5Min}`, `authredis.StaleAfterKey/IsStale`, `identity.Inject/FromContext` — names used consistently across tasks.

## Execution Handoff

Phase A executes now in this session (inline). Phases B–D execute at their service passes in the ongoing service-by-service review.
