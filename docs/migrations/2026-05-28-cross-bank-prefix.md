# Migration: Cross-Bank Protocol Canonical Prefix

> **WARNING: As of 2026-05-29, the legacy paths are REMOVED. Any cohort bank still registered with the old base_url will receive 404 from this bank and must update its peer-banks registration immediately.**

## Summary

All SI-TX wire-protocol routes are now served EXCLUSIVELY at the canonical prefix `/api/v3/cross-bank-protocol/...`. The legacy paths (`/api/v3/interbank`, `/api/v3/public-stock`, `/api/v3/negotiations/*`, `/api/v3/user/*`) were dual-mounted during Plan F (2026-05-28) and removed on 2026-05-29 per explicit user direction.

## Status: Completed (2026-05-29)

The legacy paths have been permanently removed. The canonical prefix is now the only path.

## What Changed

### Removed (2026-05-29)

- `POST /api/v3/interbank`
- `GET /api/v3/interbank/:transaction_id/status`
- `GET /api/v3/public-stock`
- `GET /api/v3/public-option-offers`
- `POST /api/v3/negotiations`
- `PUT /api/v3/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id`
- `DELETE /api/v3/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id/accept`
- `GET /api/v3/user/:rid/:id`

All of the above now return **404 Not Found**.

### Active (canonical paths)

- `POST /api/v3/cross-bank-protocol/interbank`
- `GET /api/v3/cross-bank-protocol/interbank/:transaction_id/status`
- `GET /api/v3/cross-bank-protocol/public-stock`
- `GET /api/v3/cross-bank-protocol/public-option-offers`
- `POST /api/v3/cross-bank-protocol/negotiations`
- `PUT /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `GET /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id`
- `GET /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept`
- `GET /api/v3/cross-bank-protocol/user/:rid/:id`

Same handlers, same PeerAuth middleware, identical request/response shapes and SI-TX protocol semantics. The protocol is unchanged — only the URL prefix changed.

## Action Required for Cohort Banks

**For cohort banks that previously registered with the legacy base_url:**

Update this bank's entry in your `peer_banks` table so `base_url` points at the new prefix:

```
PUT /api/v3/peer-banks/:id
Authorization: Bearer <employee-admin-token>
Content-Type: application/json

{ "base_url": "http://<this-bank-host>/api/v3/cross-bank-protocol" }
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

## Timeline

- **2026-05-28:** Dual-mount deployed (Plan F). Legacy paths added as aliases; canonical paths introduced.
- **2026-05-29:** Legacy paths removed. Canonical prefix is now the only path. Breaking change explicitly authorized by user.
