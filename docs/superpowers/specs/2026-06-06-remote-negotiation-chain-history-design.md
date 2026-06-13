# Remote Negotiation Chain History — Full Parity with Local

**Date:** 2026-06-06
**Status:** Approved (design)
**Version impact:** MINOR (2.12.1 → 2.13.0) — additive optional response fields only.

## Problem

Local and remote (cross-bank) OTC negotiation chains already live in the **same**
`otc_negotiations` table (the `Local` flag differentiates them). But their *history*
diverges: local chains append every move to the shared `otc_negotiation_revisions`
table, while remote mirrors only keep the current-terms snapshot in `RemoteOfferJSON`
and never append revisions. As a result the timeline expands a local chain into its
full bid→counter→…→accept sequence, but a remote chain shows a single current-terms
entry. Our users must see the full back-and-forth for remote chains too.

## Constraint (why "we may not have full info")

SI-TX does not expose a peer's internal revision log — `GET /negotiations/{id}`
returns only current terms. We therefore record history **forward-only**: every move
as it crosses our boundary (each inbound webhook + each outbound action). From the
bid onward that is the complete exchange. Chains created before this change keep
showing current terms until their next move (no backfill).

## Goal

Remote chains accumulate full per-move history in the **same** `otc_negotiation_revisions`
table, with **maximum fidelity**: each move records terms, action, the role that moved
(`buyer`/`seller`/`system`), and the **exact opaque wire id** of the mover
(`client-N` / `employee-N` / `bank`). Both the cross-chain **timeline** and the
per-chain **/revisions** endpoint surface this history for remote chains, at parity
with local.

## Design

### Schema (additive)
`OTCNegotiationRevision` gains a nullable column:
```go
// RemoteActorWireID is the opaque SI-TX wire id of the party who made this move
// on a REMOTE chain ("client-<N>" / "employee-<N>" / "bank"). Nil on LOCAL
// revisions (which identify the mover via ModifiedByPrincipalType/ID).
RemoteActorWireID *string `gorm:"size:128" json:"remote_actor_wire_id,omitempty"`
```
For remote revisions: `ModifiedByPrincipalType` carries the **role** (`buyer`/`seller`),
`ModifiedByPrincipalID = 0`, `RemoteActorWireID` carries the exact wire id.
AutoMigrate adds the column (nullable → safe).

### Proto (additive, backward-compatible)
Add `string action_by_wire_id` to:
- `OTCTimelineEntry` (field 13)
- `OTCNegotiationRevisionResponse` (field 12)

`make proto`; gateway passes the field through. New optional fields ⇒ not breaking.

### Revision logging — atomic, in the repository
Add `…WithRevision` repo methods that perform the mutation **and** append a revision
in **one transaction** (concurrency-safe per CLAUDE.md), then switch the remote call
sites to them:

| Mutation today | New method | Appends |
|---|---|---|
| `UpsertRemoteNeg` (create) | `UpsertRemoteNegWithRevision` | BID (rev 1) only on a true insert |
| `UpdateRemoteNegOffer` (counter) | `UpdateRemoteNegOfferWithRevision` | COUNTER, unless a retry (see idempotency) |
| `UpdateRemoteNegStatus` (terminal/cascade) | `SetRemoteNegStatusWithRevision` | REJECT/CANCEL, only on a real non-terminal→terminal transition |
| `CompareAndSetRemoteNegStatus` (accept) | `CompareAndSetRemoteNegStatusWithRevision` | ACCEPT, only when `RowsAffected==1` |

The caller passes a revision template (terms + action + role + wire id); the repo fills
`NegotiationID` + `RevisionNumber` (`NextRevisionNumber`).

**Recorded actions: BID, COUNTER, ACCEPT, REJECT** — at parity with local, which records
exactly these (local does NOT record bidder cancels or cascade-cancels as revisions).

**8 party-move call sites:**
- BID — inbound `CreateNegotiation`, outbound `openRemoteNegotiation`
- COUNTER — inbound `UpdateNegotiation`, outbound `counterRemoteNegotiation`
- ACCEPT — inbound accept (CAS `ongoing→accepted`), outbound `acceptRemoteNegotiation`
- REJECT — inbound `DeleteNegotiation`, outbound `cancelRemoteNegotiation`

**NOT recorded** (automated, not party moves — parity with local): `cascadeCancelRemoteSiblings`,
`cascadeCancelRemoteChildrenOfListing`, the reconciler cancel (they keep plain
`UpdateRemoteNegStatus`), and the accept-rollback compensation (`accepted→ongoing`).

### Mover identity per site
The mover is read from `offer.LastModifiedBy` (already authenticated/derived) and the
chain's buyer/seller columns: if `lastModifiedBy.routing/id` == buyer → role `buyer`,
wire id = buyer id; if == seller → role `seller`. Inbound bid → buyer. Cascade/reconciler
cancels → role `system`, wire id empty.

### Idempotency (webhooks retry)
- **Counter:** skip if the chain's most recent revision already has the same
  `(action, terms, wire id)` (a retry). A legitimate same-terms re-counter by the
  *other* party differs by wire id, so it still records.
- **Terminal:** only append on a real status transition (CAS `RowsAffected==1` /
  non-terminal→terminal guard) — double-delivery is a no-op.
- **Bid:** appended only when the chain has **zero** revisions yet (so an upsert
  retry, which finds the BID already present, is a no-op).

### Read paths
- `GetOfferTimeline` (local-listing remote merge) and `remoteOfferTimeline` (remote
  listing): for each remote chain, emit **one entry per revision** via
  `ListRevisions(row.ID)`; **fall back** to the single current-terms entry only when a
  chain has no revisions yet (legacy). Merged stream stays sorted by `created_at`.
- Per-chain `/revisions` endpoint (`ListNegotiationRevisions`): today its auth matches
  the local bidder columns, which remote rows lack, so it 403s for remote chains. Add a
  **remote-aware authorization** path (caller matches the hosted party in the Remote*
  columns, mirroring `resolveRemoteNegAction`) so the endpoint returns the full remote
  history. Each revision response carries `action_by_wire_id`.
- Per-chain *list* and `negotiations` views are unchanged (they show current terms,
  same as local).

## Testing
- **Repo:** `…WithRevision` methods are atomic + idempotent (retry skips a dup COUNTER;
  alternating same-terms different-mover records; terminal double-delivery is a no-op;
  BID only on insert). Revision numbering is gap-free under the unique index.
- **Handler round-trip (production-faithful, mirror in shared table):** bid → seller
  counters → buyer counters → accept ⇒ timeline shows **4 ordered entries** with correct
  actions, roles, and wire ids — exercised for both inbound and outbound moves.
- **/revisions parity:** the hosted party (client and bank) can read a remote chain's
  full revision list; a non-party gets `NotFound`.
- **Legacy fallback:** a remote chain with no revisions still yields one current-terms
  timeline entry.
- All existing tests stay green; `make test` + `make lint` on stock-service.

## Non-code deliverables
- `docs/api/REST_API_v3.md`: timeline + `/revisions` now return full remote history;
  document the new `action_by_wire_id` field.
- `VERSION` 2.12.1 → 2.13.0 + `version.go`.

## Out of scope
- Backfilling existing remote chains (forward-only by decision).
- Reconstructing the peer's pre-boundary history (SI-TX doesn't expose it).
