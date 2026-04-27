# Design: Route Consolidation to v3 (Spec E)

**Status:** Approved
**Date:** 2026-04-27
**Scope:** Clean-break deletion of v1 and v2 routers. **API versioning capability preserved** — v4 (and beyond) can be added as separate explicit router files when needed.

## Problem

Three live router versions: v1, v2, v3. CLAUDE.md falsely claims "v2 router transparently falls back to v1" — actually `router_v2.go:63` calls `RegisterCoreRoutes(v2, ...)` directly, so v2 duplicates the entire v1 surface. v3 is a thin file with only 3 unique surfaces: investment funds, inter-bank transfers, OTC options.

Surface area today:
- `router_v1.go` — 834 lines, defines `RegisterCoreRoutes()` (line 109) and `SetupV1Routes()` (line 795).
- `router_v2.go` — 92 lines, calls `RegisterCoreRoutes()` and adds 2 unique routes (options orders, options exercise).
- `router_v3.go` — 100 lines, defines new surfaces only, does NOT call `RegisterCoreRoutes()` (so v3 lacks all the v1 routes — clients must use v1 prefix for those).

Three REST docs (`REST_API.md`, `REST_API_v1.md`, `REST_API_v2.md`, `REST_API_v3.md`) duplicate content.

Permission gates: 71 distinct `RequirePermission(...)` strings live inside `RegisterCoreRoutes()`, called by both v1 and v2.

## Goal

One canonical version (v3) at the time of this refactor. **The router architecture supports cleanly adding v4 in the future without re-introducing the duplication or fallback hacks.** No implicit fallback between versions — each version is a distinct file with explicit registrations.

## Approach

### Deletion (clean break)

- Delete `router_v1.go` (834 lines) entirely.
- Delete `router_v2.go` (92 lines) entirely.
- Delete `RegisterCoreRoutes()` — its body inlines into the v3 router.
- Delete `docs/api/REST_API.md`, `docs/api/REST_API_v1.md`, `docs/api/REST_API_v2.md` (only `REST_API_v3.md` survives, renamed to `REST_API.md`).
- Delete `api-gateway/internal/router/v2_fallback_test.go` — it tests a behavior that no longer exists (and arguably never did, given the audit).
- Delete the version selector logic in `api-gateway/cmd/main.go` if any.

### Migration of unique routes to v3

The 2 v2-only routes (`POST /api/v2/options/:option_id/orders`, `POST /api/v2/options/:option_id/exercise`) move to `/api/v3/options/...`.

All v1 routes (which are also v2 routes via `RegisterCoreRoutes`) move to `/api/v3/...` paths. v3 becomes the only live surface.

### Per-version router pattern (preserved for v4 and beyond)

Even though only v3 exists today, the structure is set up to support a future v4 cleanly:

```
api-gateway/internal/router/
    handlers.go         # Shared handler-instantiation. Returns a struct of all handlers.
    middleware.go       # Shared middleware factories (identity, permission, auth).
    router_v3.go        # Defines SetupV3(r *gin.Engine, h *Handlers).
    # router_v4.go      # When v4 is needed: defines SetupV4. Re-registers everything,
    #                   # explicitly. May reuse handler functions; may diverge per route.
```

The pattern when v4 ships:
- `router_v4.go` is a new file. It registers routes under `/api/v4/`.
- Routes that are unchanged from v3: v4 handler binding calls the same handler function.
- Routes that change shape in v4: v4 binds a new handler variant (e.g., `handler.PlaceOrderV4`).
- v4 does NOT inherit from v3. Each version is explicit. This avoids the hidden-coupling bug we just fixed.
- Both routers run side-by-side. `cmd/main.go` calls both `SetupV3(r, h)` and `SetupV4(r, h)`.
- Sunset policy for v3 is documented but not enforced by code.

### Single router file (today)

`router_v3.go` (rename to `router.go` if desired — leaving as `router_v3.go` to make the version-explicit pattern obvious):

```go
package router

func SetupV3(r *gin.Engine, h *Handlers) {
    public := r.Group("/api/v3")
    {
        public.POST("/auth/login",        h.Auth.Login)
        public.POST("/auth/client-login", h.Auth.ClientLogin)
        public.POST("/auth/refresh",      h.Auth.Refresh)
        public.GET("/exchange-rates",     h.Exchange.ListRates)
    }

    me := r.Group("/api/v3/me",
        middleware.AnyAuthMiddleware,
        middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
    )
    {
        me.POST("/orders",        middleware.RequirePermission(perms.Orders.Place.Own), h.Stock.PlaceMyOrder)
        me.GET("/orders",         middleware.RequirePermission(perms.Orders.Read.Own),  h.Stock.ListMyOrders)
        me.GET("/portfolios",     middleware.RequirePermission(perms.Securities.Read.HoldingsOwn), h.Portfolio.GetMyPortfolio)
        // ... all /me/* trading routes
    }

    meProfile := r.Group("/api/v3/me",
        middleware.AnyAuthMiddleware,
        middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
    )
    {
        meProfile.GET("/profile",  h.Client.GetMyProfile)
        meProfile.GET("/cards",    h.Card.ListMyCards)
        // ... all /me/* non-trading routes
    }

    employee := r.Group("/api/v3",
        middleware.AuthMiddleware,
        middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
    )
    {
        employee.GET("/clients",                  middleware.RequirePermission(perms.Clients.Read.All), h.Client.ListClients)
        employee.POST("/clients/:client_id/orders", middleware.RequirePermission(perms.Orders.Place.OnBehalfClient), h.Stock.PlaceOrderForClient)
        // ... admin/employee routes
    }

    bank := r.Group("/api/v3",
        middleware.AuthMiddleware,
    )
    {
        bank.POST("/bank/orders", middleware.RequirePermission(perms.Orders.Place.OnBehalfBank), h.Stock.PlaceOrderForBank)
        // ... bank-specific routes (if not handled via /me/* + OwnerIsBankIfEmployee)
    }
}
```

Identity middleware (from Spec C) is wired per route group. Permissions (from Spec D) are typed constants — `RequirePermission` takes `permissions.Permission`, not string. The `RegisterCoreRoutes` god-function is gone — each route group is self-contained.

### `cmd/main.go`

```go
r := gin.New()
h := router.NewHandlers(/* deps */)
router.SetupV3(r, h)
// When v4 ships: router.SetupV4(r, h)
log.Fatal(r.Run(cfg.GatewayHTTPAddr))
```

### Path prefix

All routes under `/api/v3/`. No `/api/`, no `/api/v1/`, no `/api/v2/`. Requests to old prefixes get 404 (Gin default — no special handling).

### Files DELETED

- `api-gateway/internal/router/router_v1.go` (834 lines)
- `api-gateway/internal/router/router_v2.go` (92 lines)
- `api-gateway/internal/router/v2_fallback_test.go`
- `docs/api/REST_API.md`
- `docs/api/REST_API_v1.md`
- `docs/api/REST_API_v2.md`
- The `RegisterCoreRoutes()` function (its body is inlined and reorganized into the v3 setup).

### Files ADDED

- `api-gateway/internal/router/handlers.go` — `Handlers` struct that bundles every handler. Constructor takes all gRPC clients as parameters. Used by all current and future router versions.
- `api-gateway/internal/router/router_versioning.md` — documentation of the per-version router pattern. Explains how to add v4 when needed.

### Files MODIFIED

- `api-gateway/internal/router/router_v3.go` — expanded to contain every route, organized by route group with identity rule.
- `api-gateway/cmd/main.go` — calls `SetupV3` only.
- `docs/api/REST_API_v3.md` → renamed to `docs/api/REST_API.md`. Contains every v3 endpoint.
- `test-app/workflows/*.go` — every URL `/api/v1/...` and `/api/v2/...` rewritten to `/api/v3/...`. Approximately 40+ files.
- `test-app/internal/client/*.go` — base path constant changes from configurable to `/api/v3`.
- Swagger annotations on every handler — version-tag updated.

### Swagger regeneration

`make swagger` produces a fresh `api-gateway/docs/swagger.json` reflecting v3-only. The `swag init` invocation in `Makefile` runs as part of `make build`.

### Dependency on Specs C and D

This spec **must merge after C and D** because:
- It uses `ResolvedIdentity` from Spec C.
- It uses `permissions.Permission` typed constants from Spec D.

If for some reason this lands first, the temporary state is `RequirePermission(permissions.Permission("orders.place.own"))` — explicit cast. But the order of merge is fixed: C → D → E.

## Test Plan

### Unit tests

- `router_v3_test.go`: every route is registered exactly once. Table test enumerates expected (method, path, middleware-stack, handler) tuples.
- Identity rule wiring: each route group has the correct rule (table test mapping route prefix → expected `IdentityRule`).
- `handlers.go`: `NewHandlers` constructor accepts all required deps; missing deps fail fast.

### Integration tests

- All existing `test-app/workflows/` tests pass after URL rewrite.
- Smoke: requesting any `/api/v1/*` or `/api/v2/*` URL returns 404 (proves the old paths are dead).
- Smoke: every endpoint in the new `REST_API.md` returns a non-404 response (or 401/403 if auth-protected).
- Identity isolation: cross-test from Spec C's test plan continues to pass under v3-only.

### Forward-compat smoke

- Add a stub `router_v4.go` test fixture in the test suite (not the production code) that calls `SetupV4`. Confirms the per-version pattern compiles. Documented as the template for the future.

## Out of Scope

- Defining v4 itself. v4 is added when there's a real breaking change to make.
- Sunset policy enforcement (auto-deprecate v3 after a date). Not needed yet.
- gRPC service versioning. Separate concern.
- Frontend client updates to call `/api/v3/...`. That's a frontend repo change, tracked but not in this spec.
- API versioning negotiation via `Accept` header. We use URL prefix versioning, which is simpler and visible in logs.
