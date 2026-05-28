# Migration: Cross-Bank Protocol Canonical Prefix (Plan F, 2026-05-28)

## Summary

All SI-TX wire-protocol routes are now dual-mounted: served at both the legacy prefix (`/api/v3/<route>`) and the new canonical prefix (`/api/v3/cross-bank-protocol/<route>`). This change is fully backwards-compatible — existing cohort-bank registrations continue to work without any changes.

## What Changed

- `POST /api/v3/interbank` → also at `POST /api/v3/cross-bank-protocol/interbank`
- `GET /api/v3/interbank/:transaction_id/status` → also at `GET /api/v3/cross-bank-protocol/interbank/:transaction_id/status`
- `GET /api/v3/public-stock` → also at `GET /api/v3/cross-bank-protocol/public-stock`
- `GET /api/v3/public-option-offers` → also at `GET /api/v3/cross-bank-protocol/public-option-offers`
- `POST /api/v3/negotiations` → also at `POST /api/v3/cross-bank-protocol/negotiations`
- `PUT /api/v3/negotiations/:rid/:id` → also at `PUT /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id` → also at `GET /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `DELETE /api/v3/negotiations/:rid/:id` → also at `DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id/accept` → also at `GET /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept`
- `GET /api/v3/user/:rid/:id` → also at `GET /api/v3/cross-bank-protocol/user/:rid/:id`

Same handlers, same PeerAuth middleware, identical request/response shapes. The protocol semantics are unchanged.

## Cohort-Bank Coordination Required

> This change is fully backwards-compatible: existing peer registrations continue to work. However, peer banks SHOULD update their registration of this bank in their own `peer_banks` table to point at the new `base_url` ending in `/api/v3/cross-bank-protocol`. This allows the deprecated paths to be removed in a future release.

**For peer banks that want to migrate inbound calls to this bank's canonical prefix:**

Update this bank's entry in your `peer_banks` table so `base_url` points at the new prefix:

```
http://<this-bank-host>/api/v3/cross-bank-protocol
```

The outbound HTTP client appends only the leaf names (`/interbank`, `/public-stock`, `/negotiations`, `/user`) to `base_url`, so a single row update migrates all outbound calls. No code changes are needed.

## Migrating This Bank's Outbound Calls to a Peer's New Prefix

If a cohort peer has also deployed their own canonical prefix and you want to update our outbound calls to use it:

```
PUT /api/v3/peer-banks/:id
Authorization: Bearer <employee-admin-token>
Content-Type: application/json

{ "base_url": "http://peer-222/api/v3/cross-bank-protocol" }
```

## Deprecation Timeline

- **2026-05-28:** Dual-mount deployed. Legacy paths remain live.
- **Future release (TBD):** Legacy paths removed once all cohort banks confirm re-registration at the canonical prefix. Requires explicit cohort-wide coordination before removal.

## Rollback

No rollback needed — the canonical paths are additive. The legacy paths are unchanged. If the canonical paths need to be removed, delete the `crossBank := v3.Group("/cross-bank-protocol")` block in `api-gateway/internal/router/router_v3.go` and regenerate swagger.
