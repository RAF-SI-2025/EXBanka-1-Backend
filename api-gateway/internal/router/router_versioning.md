# API Versioning Pattern

This gateway supports multiple coexisting API versions via per-version
router files. There is **no transparent fallback** between versions —
each version explicitly registers its own routes.

## Today

`router_v3.go` defines `SetupV3(r *gin.Engine, h *Handlers)` and is wired
in `cmd/main.go`. v3 is the only live version. It contains the full
surface previously split across the deleted v1 and v2 routers, plus
the v3-only investment-funds, OTC-options, and inter-bank-transfer
features.

## Adding a new version (when you need v4)

When a breaking change is required (e.g., a route changes its request
or response shape, its identity rule, or its permission requirements):

1. Create `router_v4.go` with `func SetupV4(r *gin.Engine, h *Handlers)`.
2. Register every v4 route explicitly. Routes that are unchanged from
   v3 call the same `h.X.Y` handler. Routes that change shape bind to
   a new handler variant (e.g., `h.StockOrder.PlaceOrderV4`).
3. Wire it in `cmd/main.go`:
   ```go
   router.SetupV3(r, h)
   router.SetupV4(r, h)
   ```
4. Both versions run side-by-side. Clients on v3 keep working.
5. Add `docs/api/REST_API_v4.md` for the new version's surface; keep
   `REST_API_v1.md` (the canonical REST doc — see the project memory
   rule about v1 being the doc home) updated for v3 routes that didn't
   change.
6. Run `swag init -g cmd/main.go --output docs` to regenerate
   `api-gateway/docs/swagger.{json,yaml}` and commit them.

## Sunset policy

When a version is to be retired:
1. Delete its `router_vN.go` file (and any tests scoped to it) in a
   single PR.
2. Add a 404 smoke test in `test-app/workflows/wf_old_paths_404_test.go`
   asserting the retired prefix returns 404.
3. Update `docs/api/REST_API_v1.md`'s "Version Notes" section to add
   the sunset prefix to the list of retired versions.

The change is intentionally explicit and visible — no quiet
deprecation path.

## Why no fallback?

The previous v1 -> v2 setup transparently delegated unknown v2 routes
to v1. This led to the per-handler identity bug where a v2 route was
"in" v2 but used v1's handler with v1's identity assumptions, which
broke when v2 added new identity rules (the actuary-limit regression
fixed in spec C). Explicit per-version registration prevents this
class of bug at the cost of some boilerplate when introducing a v4 —
that boilerplate is the feature, not a bug.

## Identity middleware

Each route group declares an identity rule via
`middleware.ResolveIdentity`. The three rules currently in use are:

- `OwnerIsPrincipal` — owner == JWT principal. Used for /me/profile,
  /me/cards, etc.
- `OwnerIsBankIfEmployee` — if the JWT principal is an employee, owner
  is the bank (`OwnerType="bank"`, `OwnerID=nil`) and the JWT id is
  carried as `ActingEmployeeID` for per-actuary limits; otherwise
  owner == principal. Used for trading routes and /me/orders.
- `OwnerFromURLParam` — owner is the client identified by a URL path
  parameter (`:client_id`). Used for employee-on-behalf-of-client
  endpoints.

Identity is read by handlers via the bound `ResolvedIdentity` context
key (see `middleware/identity.go`). When you add a new route, pick the
matching rule from this set; do not invent ad-hoc per-handler identity
logic.
