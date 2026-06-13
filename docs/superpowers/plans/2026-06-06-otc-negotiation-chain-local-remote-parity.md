# OTC Negotiation Chain — Local/Remote Parity

**Date:** 2026-06-06
**Goal:** Make negotiation-chain *visibility* behave identically for local and remote (cross-bank) OTC option listings. Four reported problems, all read-path visibility (no money movement).

## Reported problems

1. **Timeline shows only local chains, not remote.** `GET /api/v3/otc/options/{id}/timeline` must show *both* the local chains and the remote (peer) chains on a listing.
2. **Bidder can't see his own chain when bidding on a remote-owned option.** Our backend persists the mirror, but the per-listing / timeline views of that remote listing don't surface it.
3. **Owner can't see chains from remote bidders** on a listing they posted.
4. **Counter supersession:** when a user counters multiple times, only the newest counter must be acceptable; previous ones stay visible but not acceptable.

## Root causes (evidence-based)

### RC-A — `authorizeListingAudience` resolves REMOTE mirror offers (→ P1 remote-listing facet, P2)
`OTCNegotiationService.authorizeListingAudience` loads the parent via `offerRepo.GetByIDTx → getByID`, which **does not filter `local`** (unlike `LockByIDTx`/`GetRemoteByID`, which do). Remote mirror offers live in the **same `otc_offers` table** with `local=false` (stamped by `OTCOffer.BeforeCreate`; written by `otccache.UpsertRemote`).

So for a REMOTE listing id:
- **client caller** → not the (bank/nil) initiator → `ErrOTCListingAudienceForbidden` (403).
- **bank caller** → returns the remote offer; local chains are empty; the bank seller-merge finds nothing (we're the buyer on a remote listing) → empty.

Either way the handler's remote fallback (`remoteListingOwnChains` / `remoteOfferTimeline`) is gated on `isOTCOfferNotFound(err)` and **never fires**. The bidder gets 403 or an empty list/timeline instead of his chain.

*Why tests miss it:* the unified-views tests store remote mirror offers in a **separate `fakeRemoteOfferGetter`**, not in the shared sqlite `otc_offers` table, so `authorizeListingAudience` returns NotFound in tests and the fallback fires — false confidence.

### RC-B — owner-side views of a LOCAL listing don't merge remote bidder chains (→ P1 owner facet, P3)
A peer bidding on our LOCAL listing creates a remote mirror row where we host the seller (`RemoteSellerRouting=own`, `RemoteSellerID=client-N|employee-N`, `RemoteParent*`=our listing lot key). But:
- `ListNegotiationsByListing` merges remote chains **only for `ot == OwnerBank`** (handler line ~759), via `ListRemoteNegByBankParty(seller)`. **Client-owned listings get no remote merge.**
- `GetOfferTimeline` (local-listing branch) merges **no** remote chains at all.

### RC-C — counter supersession (→ P4): NO BUG (verify-only)
A chain is one row holding the *latest-terms snapshot* + append-only revisions (local) or `RemoteOfferJSON` (remote). Counter overwrites the snapshot; revisions/history keep prior terms but are **not independently acceptable** (no accept-by-revision API). Accept uses the current snapshot and forbids accepting the terms you last proposed (local `AcceptNegotiation` last-action guard; remote `acceptRemoteNegotiation` anti-self-accept guard). Inbound remote counters are turn-guarded. ⇒ only the newest counter is acceptable, on both paths. Add regression tests; no code change.

## Fixes

**Fix A** (`otc_negotiation_service.go`, `authorizeListingAudience`): after loading the parent, if `!parent.Local` return `ErrOTCOfferNotFound`. Makes the audience check local-only (consistent with `LockByIDTx`), so the handler remote fallback fires for both `ListByParentOffer` and `OfferTimeline`. Fixes P1(remote)+P2.

**Fix B1** (`otc_negotiation_handler.go`, `ListNegotiationsByListing`): replace the `ot == OwnerBank`-gated merge with a shared helper `remoteBidderChainsOnLocalListing(parentOffer)` that derives the seller principal from the **parent offer's initiator** (bank → `ListRemoteNegByBankParty(seller)`; client → `ListRemoteNegByClient("client-<initiatorID>", "seller")`), filtered by the listing lot key. Fixes P3 (and lets an `otc.read.all` employee see remote bids on a client-owned listing).

**Fix B2** (`otc_negotiation_handler.go`, `GetOfferTimeline` local-listing branch): after building local revision entries, append one entry per remote bidder chain (terms from `RemoteOfferJSON`, like `remoteOfferTimeline`) using the same helper, then sort the merged stream by `created_at`. Fixes P1(owner facet) — "show both".

**Fix C**: regression tests proving stale counters can't be accepted (local + remote). No code change.

## Testing

- **New failing tests (TDD), `stock-service/internal/handler`:**
  - Per-listing + timeline of a REMOTE listing where the mirror offer is **inserted into the sqlite `otc_offers` table** (`local=false`) — reproduces production. Bidder (client and bank) must see his own chain. (RC-A)
  - Client-owned LOCAL listing with a remote bidder chain → poster sees it via per-listing + timeline. (RC-B)
  - Bank-owned LOCAL listing remote bid still appears in timeline (B2 doesn't regress B's bank path).
- **Service test:** `authorizeListingAudience`/`OfferTimeline`/`ListByParentOffer` return NotFound for a remote offer row in the DB.
- **Regression (P4):** local chain — counter 3×, accepting yields the latest terms; the proposer can't accept own last counter. Remote — same via snapshot.
- Keep all existing unified-views tests green.
- `make test` + `make lint` on stock-service.

## Non-code deliverables
- `docs/api/REST_API_v3.md`: note that the per-listing, timeline endpoints now include remote chains.
- `Specification.md`: update the OTC negotiation read-surface behavior if described.
- Bump `VERSION` (PATCH — bug fix, no contract change).

## Out of scope (flagged, not done unless asked)
- Unifying the remote status vocabulary (`ongoing` vs `open`/`countered`) in responses / `ListMyNegotiations` status filtering.
- Adding a turn rule to the LOCAL `CounterNegotiation` (remote enforces turns; local does not).
