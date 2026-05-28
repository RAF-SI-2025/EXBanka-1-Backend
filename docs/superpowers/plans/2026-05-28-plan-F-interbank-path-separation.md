# Plan F — Interbank Protocol Path Separation

> **For agentic workers:** Use superpowers:subagent-driven-development. **Extra care required:** this touches the SI-TX-Proto wire protocol; existing cohort-bank registrations must keep working.

**Goal:** Mount all interbank wire-protocol routes under a dedicated `/api/v3/cross-bank-protocol/...` prefix so they're visually and structurally separated from our own user/employee routes.

**Constraint:** Do NOT break existing cohort-bank interop. The frozen-routes memory says paths must match what cohort banks expect; cohort banks currently call us at the original prefix. Therefore: we will **dual-mount** (serve BOTH the new and old prefixes) for a deprecation window. Outbound calls continue to read the prefix from each peer's `base_url` registration, so they're unaffected.

---

## Routes in scope

Currently registered (peer-authenticated, under `peer.Use(h.PeerAuthMW)`):

```
POST   /api/v3/interbank
GET    /api/v3/interbank/:transaction_id/status
GET    /api/v3/public-stock
GET    /api/v3/public-option-offers
POST   /api/v3/negotiations
PUT    /api/v3/negotiations/:rid/:id
GET    /api/v3/negotiations/:rid/:id
DELETE /api/v3/negotiations/:rid/:id
GET    /api/v3/negotiations/:rid/:id/accept
GET    /api/v3/user/:rid/:id
```

After Plan F these all get a sibling at `/api/v3/cross-bank-protocol/<rest>`:

```
POST   /api/v3/cross-bank-protocol/interbank
GET    /api/v3/cross-bank-protocol/interbank/:transaction_id/status
GET    /api/v3/cross-bank-protocol/public-stock
GET    /api/v3/cross-bank-protocol/public-option-offers
POST   /api/v3/cross-bank-protocol/negotiations
PUT    /api/v3/cross-bank-protocol/negotiations/:rid/:id
GET    /api/v3/cross-bank-protocol/negotiations/:rid/:id
DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id
GET    /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept
GET    /api/v3/cross-bank-protocol/user/:rid/:id
```

Both old and new paths route to the **same** handlers under the **same** middleware.

---

## What stays untouched

- `/api/v3/peer-banks` admin routes — these are local admin only (registration management); they're not on the wire. Leave as-is.
- `/api/v3/me/peer-otc/*` initiate routes — these are OUR side calling out to peers. They use `base_url` from peer registration; not affected.
- Outbound HTTP calls — unchanged. Each peer's `base_url` is configured per-peer when the cohort bank registers. If a cohort bank registers a new base_url that includes `/cross-bank-protocol`, our outbound will hit their new prefix. Until then we keep using their old prefix as registered.

---

## Tasks

### F1: Add dual route registration

`api-gateway/internal/router/router_v3.go` — in the existing `peer := v3.Group(""); peer.Use(h.PeerAuthMW)` block, also register the same handlers under `/cross-bank-protocol/...`:

```go
peer := v3.Group("")
peer.Use(h.PeerAuthMW)
{
    // Legacy paths — kept for cohort-bank backwards compat
    peer.POST("/interbank", h.PeerTx.PostInterbank)
    peer.GET("/interbank/:transaction_id/status", h.PeerTxStatus.GetTxStatus)
    peer.GET("/public-stock", h.PeerOTC.GetPublicStocks)
    peer.GET("/public-option-offers", h.PeerOTC.GetPublicOptionOffers)
    peer.POST("/negotiations", h.PeerOTC.CreateNegotiation)
    peer.PUT("/negotiations/:rid/:id", h.PeerOTC.UpdateNegotiation)
    peer.GET("/negotiations/:rid/:id", h.PeerOTC.GetNegotiation)
    peer.DELETE("/negotiations/:rid/:id", h.PeerOTC.DeleteNegotiation)
    peer.GET("/negotiations/:rid/:id/accept", h.PeerOTC.AcceptNegotiation)
    peer.GET("/user/:rid/:id", h.PeerUser.GetUser)
}

// New canonical paths — same handlers under a dedicated prefix
crossBank := v3.Group("/cross-bank-protocol")
crossBank.Use(h.PeerAuthMW)
{
    crossBank.POST("/interbank", h.PeerTx.PostInterbank)
    crossBank.GET("/interbank/:transaction_id/status", h.PeerTxStatus.GetTxStatus)
    crossBank.GET("/public-stock", h.PeerOTC.GetPublicStocks)
    crossBank.GET("/public-option-offers", h.PeerOTC.GetPublicOptionOffers)
    crossBank.POST("/negotiations", h.PeerOTC.CreateNegotiation)
    crossBank.PUT("/negotiations/:rid/:id", h.PeerOTC.UpdateNegotiation)
    crossBank.GET("/negotiations/:rid/:id", h.PeerOTC.GetNegotiation)
    crossBank.DELETE("/negotiations/:rid/:id", h.PeerOTC.DeleteNegotiation)
    crossBank.GET("/negotiations/:rid/:id/accept", h.PeerOTC.AcceptNegotiation)
    crossBank.GET("/user/:rid/:id", h.PeerUser.GetUser)
}
```

Commit.

### F2: Mark the legacy paths as deprecated in swagger

Each legacy route's Swagger annotation gets `@Deprecated true`. Add to each legacy handler's godoc:
```go
// @Deprecated true
// @Description Legacy path. Use /cross-bank-protocol/<rest> instead.
```

(The new canonical path's docstring is the live one.)

Regenerate `make swagger`.

Commit.

### F3: Tests

- Add an end-to-end test in `api-gateway/internal/router/router_v3_test.go` that hits BOTH paths and confirms both return the same response for an authenticated peer (use the existing PeerAuth test fixtures).
- Test cases:
  - `POST /interbank` and `POST /cross-bank-protocol/interbank` — both succeed for a valid NEW_TX envelope.
  - Same for negotiations.

Commit.

### F4: REST_API + Specification docs

`docs/api/REST_API_v3.md`:
- Move the existing peer-protocol section under a "Cross-Bank Protocol (`/cross-bank-protocol`)" heading.
- Note at the top: "Legacy aliases without the prefix remain functional but are deprecated. New cohort registrations should use `base_url` pointing at `/api/v3/cross-bank-protocol`."

`docs/Specification.md`:
- §17: document both old and new paths, mark old as deprecated.
- Add a §21 business rule: "Cross-bank protocol routes are served at both the legacy prefix (`/api/v3/<route>`) and the canonical prefix (`/api/v3/cross-bank-protocol/<route>`). Cohort banks may use either; new registrations should prefer the canonical prefix."

Commit.

### F5: Migration note for cohort banks

Add a section to `Specification.md` (or a new `docs/migrations/2026-05-28-cross-bank-prefix.md`):

> **Cohort-bank coordination required:** This change is fully backwards-compatible: existing peer registrations continue to work. However, peer banks SHOULD update their registration of this bank in their own peer-banks table to point at the new `base_url` ending in `/api/v3/cross-bank-protocol`. This allows the deprecated paths to be removed in a future release.
>
> To migrate this bank's outbound calls to a cohort peer's new prefix, update the peer's row in `peer_banks` table via `PUT /api/v3/peer-banks/:id` and set `base_url` to the new prefix.

Commit.

### F6: Update memory

Edit `feedback_interbank_protocol_frozen.md` to reflect:
- The frozen list now includes BOTH the legacy AND `/cross-bank-protocol/...` paths.
- New canonical prefix is `/cross-bank-protocol` for documentation purposes.
- Legacy paths must remain functional until the cohort-wide migration completes.

Commit.

---

## What we are NOT doing in this plan

- **Not removing the legacy paths.** They stay as live aliases. A future plan can remove them after every cohort bank confirms re-registration.
- **Not changing outbound HTTP client.** Outbound paths come from each peer's `base_url`. Peers will re-register us at the new prefix in their own time.
- **Not changing the protocol semantics.** Same envelopes, same auth, same idempotency keys, same status codes.

## Verification before submitting

- `make build` clean.
- `cd api-gateway && go test ./... -count=1` clean.
- Manual check: `curl http://localhost:8080/api/v3/interbank/...` and `curl http://localhost:8080/api/v3/cross-bank-protocol/interbank/...` both return identical responses.
- Swagger UI shows both paths, with the legacy ones flagged deprecated.
