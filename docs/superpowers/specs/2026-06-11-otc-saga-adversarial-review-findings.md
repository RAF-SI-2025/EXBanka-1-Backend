# OTC + Saga Adversarial Review — Findings (2026-06-11)

Two parallel adversarial reviews of (a) the OTC option **sagas** + recovery and
(b) the cross-bank OTC **negotiation actions** + termless-offer lifecycle. Each
finding was verified against the code before action. This doc records what was
**fixed this session** and what remains **open** (with precise repro) so the
remaining items are tracked, not lost.

Context: option offers are now **termless inventory** (one open `OTCOffer` per
`(owner, ticker, direction)`; terms live on the `OTCNegotiation` chain). The
`/public-stock` wire carries **no offer id**, so a peer bid creates a remote
mirror with `parent_offer_id = 0` and a non-numeric / absent `RemoteParentNativeID`.

---

## FIXED this session

| # | Sev | Finding | Fix (commit) |
|---|-----|---------|--------------|
| F1 | HIGH | **Cross-bank accept never lowered the local listing.** Direction-2 accept formed a contract but left the `/public-stock` listing `open`/full (the user's "lowers the available stock" bug). | Consume the listing by its `(owner,ticker,sell)` key on the OUTBOUND `acceptRemoteNegotiation` (`e6d2b335`). |
| F2 | HIGH | **No over-commit guard on the termless accept.** A second still-`ongoing` sibling bid could be accepted into a second contract (one listing → N contracts). | Pre-accept `LocalSellOfferOpenForSeller` gate on the outbound path (`a7a8ab0d`). |
| F3 | CRITICAL | **Inbound accept asymmetry.** The INBOUND `PeerOTCGRPCHandler.AcceptNegotiation` (peer buyer accepts our seller's counter) skipped its orphan guard for termless bids (non-numeric parent id) AND never consumed the listing → over-commit + contract/premium on a CANCELLED listing. | Mirror the guard + consume on the inbound path when `sellerRouting == ownRouting` (`2ff505ce`). |
| F4 | CRITICAL | **Saga recovery hid stuck compensations.** `saga.Compensate` returned `nil` even when a `Backward` was left `compensating`; the reconciler force-marked the row terminal → money in limbo permanently hidden, never dead-lettered, compensating rows spawned each tick. | `Compensate` returns `ErrCompensationStuck`; recoverers propagate → dead-letter. `RecordCompensation` made idempotent (reuse the row) to bound the spawn (`0d45aff3`). |
| F5 | HIGH | **Seller couldn't see cross-bank bids on their own listing** (was O5; live-reproduced). The per-listing view + timeline correlated remote bids by the local offer's surrogate id, but a cross-bank bid carries no parentOfferId (`nil`, e.g. Banka-4) or our `"ps:"` shell id — never the surrogate — so EVERY remote bid was dropped. | `remoteBidderChainsOnLocalListing` now correlates by **(seller, ticker)** (the query already scopes to the seller; one open offer per owner+ticker ⇒ ticker identifies the listing). Covers both `ListNegotiationsByListing` + `GetOfferTimeline`; dead `localOfferCrossBankNativeID` removed. |

Also verified WORKING from live cross-bank data (no fix needed): **premium** is
credited to the seller (ledger "Peer OTC option premium and contract acceptance"
+8 EUR) and **capital-gains tax on profit** is recorded on the cross-bank
exercise (`capital_gains` row: strike − avg-cost × qty, e.g. `+51.905`;
`peer_otc_grpc_handler.go:1900-1927`). Reservation/idempotency keys, anti-self-accept,
existence non-leak, `UpdateQuantity` floor — all reviewed and SAFE.

---

## OPEN (tracked for a follow-up; money risk already mitigated)

### O1 — Cancel-listing cross-bank cascade is dead for termless bids — **MEDIUM** (was filed HIGH; money risk now closed)
`cascadeCancelRemoteChildrenOfListing` (`otc_negotiation_handler.go:~459`) looks
up children by the **numeric** offer id, but cross-bank bids store a non-numeric
`RemoteParentNativeID` (`"ps:…"`) or nil (foreign banks send an empty
`parentOfferId`). So cancelling a listing does **not** flip in-flight cross-bank
bid mirrors to `cancelled` — they linger `ongoing`.
- **Money risk: CLOSED** by F2/F3 — an accept against a cancelled/consumed listing
  is now rejected (`FailedPrecondition`) on both accept paths, so a lingering bid
  cannot form a contract.
- **Residual:** stale `ongoing` mirror rows (UI/state cleanliness only).
- **Fix sketch:** correlate children by `(seller wire id, ticker)` (or the `"ps:"`
  shell key) instead of the numeric offer id.

### O2 — Outbound accept guard is advisory (COUNT), not a row lock — **MEDIUM/HIGH** (narrow race)
`LocalSellOfferOpenForSeller` is a `SELECT COUNT` and the consume runs AFTER the
`GET /accept` dispatch. Two **concurrent** accepts of two different bids on the
**same** listing can both pass the count and both dispatch before either
consumes (race window = the outbound HTTP round-trip). The local path serializes
via `SELECT FOR UPDATE` on the parent; the cross-bank path has no equivalent.
- **Bound:** over-commit is still capped by the seller's holding (reservations
  vote NO once short), so it cannot create shares — it can only form more
  contracts than the listing advertised.
- **Fix sketch:** `SELECT FOR UPDATE` the resolved listing row and re-check open
  **inside the tx that flips it consumed**, claiming it BEFORE dispatch (treat
  dispatch failure as a release).

### O3 — Local `AcceptNegotiation` crash-gap strands an "accepted" negotiation — **HIGH** (pre-existing, no money moved)
`otc_negotiation_service.go:445-565`: the status flip (negotiation→`accepted`,
parent→`consumed`, siblings→`cancelled`) commits BEFORE the formation saga's first
`saga_logs` row is written (two `GetAccount` RPCs sit in the gap). A crash in the
gap leaves an `accepted` negotiation + `consumed` listing with **no contract and
no saga row**, so the `saga_logs`-driven recovery never sees it.
- **Fix sketch:** add a reconciler for `accepted` negotiations with no
  `minted_contract_id`, or persist the saga's first `pending` row inside the same
  state TX.

### O4 — One-open-offer DB unique index excludes BANK-owned offers — **MEDIUM** (edge)
The partial index predicate includes `initiator_owner_id IS NOT NULL`; bank
offers carry `NULL` owner id (and Postgres treats NULLs as distinct anyway), so
two concurrent bank `Create`s for the same `(ticker, sell)` both pass the
non-transactional pre-check and insert. Bank-as-OTC-principal is exposed
cross-bank as `"employee-<N>"`, so the "exactly one open" assumption the termless
guards rely on is unenforced for bank sellers.
- **Fix sketch:** index `COALESCE(initiator_owner_id, 0)` + `initiator_owner_type`
  (drop the `IS NOT NULL`); map its violation in `Create`.

### O5 — Seller can't see cross-bank bids on their own listing — **FIXED** (see F5)
Was: `remoteBidderChainsOnLocalListing` correlated remote bids by the numeric
surrogate id, but inbound bids stored `"ps:…"` / nil, so the seller saw zero
cross-bank bids. Now correlates by **(seller, ticker)** — covers both the
per-listing view and the timeline. (O1's cancel-cascade still keys on the numeric
id, but its money risk is closed by the accept guards; only stale-state cleanup
remains.)

### O6 — Exercise saga recovery path can forward-resume the buyer-share credit — **MEDIUM** (speculative)
The pivot was removed with the justification "compensation runs synchronously
inside Execute." On the **async recovery** path that doesn't hold: after a crash
past the buyer-holding credit, recovery forward-resumes and the buyer's shares are
tradeable until a later forward step fails and rolls back. `DecrementForOwner` has
no insufficient-quantity guard.
- **Fix sketch:** pivot at/after the share transfer on the recovery path, or
  credit the buyer's shares as a reservation until `mark_contract_exercised`.

---

These are recorded for a dedicated follow-up. O1/O5 share one root cause (numeric
vs `"ps:"` correlation key) and could be fixed together. O2/O3/O6 are
saga/concurrency hardening. The money-creating/hiding paths were the priority and
are fixed above.
