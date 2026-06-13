# Spec — Negotiate an option off a peer's `/public-stock` listing

**Date:** 2026-06-10
**Status:** Approved (design)
**Area:** stock-service cross-bank OTC (buyer-side discovery); no frontend code changes

## 1. Problem

Our cross-bank OTC **buyer** flow can only bid on a peer's **option offers**, discovered
through our own `/public-option-offers` *extension* endpoint. A base-spec peer (e.g. Banka 4)
exposes only the protocol-standard `/public-stock`, so our buyer has nothing to bid on and
"our bank **buys** a peer's option" cannot start (finding A-5 in
`docs/protocol/bank-4-interop-otc-results.md`).

The SI-TX spec's discovery model (§8) is: a seller publishes **stocks** via `/public-stock`;
a buyer discovers a stock and **opens an option negotiation** on it. We must support this
**in addition to** the existing `/public-option-offers` path (keep both).

## 2. Goal / non-goals

**Goal:** let our buyer initiate (and counter) an option negotiation against any seller a
peer publishes on `/public-stock`, reliably (persisted + auto-refreshed + revalidated live),
reusing the existing list/bid/dispatch machinery.

**Non-goals:**
- No change to frontend API **route shapes** beyond one **additive** response field.
- No change to the `/cross-bank-protocol` inbound route *handlers* (only verified, §7).
- No new buyer-facing route. No change to the existing `/public-option-offers` path.
- We do **not** dedupe in the backend — the frontend dedupes/filters (decision below).

## 3. Approach (chosen: reuse the `otc_offers` mirror)

A peer `/public-stock` listing is materialized as a **synthesized `sell_initiated` remote
`OTCOffer` "shell"** in the *same* `otc_offers` table the option mirror already uses. The
shell carries **no preset terms** (the buyer proposes strike/premium/settlement on bid, as
today); a new flag distinguishes it from a real option-offer.

### 3.1 Data model — `model.OTCOffer`

New column:

| Column | Type | Meaning |
|---|---|---|
| `HasPresetTerms` | `bool` (`gorm:"not null;default:true"`) | `true` ⇒ row came from a `/public-option-offers` listing (owner set strike/premium up front — a "start position"). `false` ⇒ synthesized from `/public-stock` (no preset terms; fully buyer-negotiated). |

Synthesized shell row values:

| Field | Value |
|---|---|
| `RoutingNumber` | peer routing |
| `InitiatorBankCode` | peer bank code |
| `Local` | `false` (remote mirror) |
| `Direction` | `sell_initiated` |
| `Ticker` | the public stock's ticker |
| `RemoteSellerID` | the seller's SI-TX id (`client-<N>` / `bank`) from `/public-stock` |
| `StrikePrice`, `Premium` | `0` (placeholder; columns are NOT NULL) |
| `StrikeCurrency`, `PremiumCurrency` | `NULL` (no preset currency; buyer chooses on bid) |
| `HasPresetTerms` | `false` |
| `NativeID` | **`ps:<sellerRn>:<sellerId>:<ticker>`** — stable per (seller, ticker) so the reconciler upserts (never duplicates) across cycles and can expire it. |

`(RoutingNumber, NativeID)` is the existing unique key; the `ps:` namespace keeps shells
distinct from option-offer rows (whose `NativeID` is the peer's offer id), so a peer that
publishes **both** for the same (seller, ticker) yields **two** distinct rows — by design
(§3.4).

The seller's published `amount` (shares offered) from `/public-stock` is **capacity
information only**; v1 does not store it on the shell (the negotiation quantity is
buyer-proposed and the seller's bank enforces available capacity at reserve/accept time). It
can be surfaced later if the frontend wants to show the cap — out of scope here.

### 3.2 Ingestion (reliable, persisted — mirrors `/public-option-offers`)

The existing `OptionRefresher` (`stock-service/internal/otccache/option_cache.go`) already, each
cycle, fetches every peer's `/public-option-offers`, builds rows, `UpsertRemote`s them, and
`ReconcileRemoteNotSeen` expires the not-seen ones. We extend it so each cycle ALSO:

1. fetches that peer's **`/public-stock`** (egress already exists — `cache.go`'s `fetchPeer`
   uses `interbank-service` `PeerEgressService` → `GET /public-stock`; reuse the same client),
2. builds a `sell_initiated` **shell** per `(seller, ticker)` (§3.1),
3. `UpsertRemote`s each shell, and
4. **reconciles shells separately from option-offers** so reconciling one source never expires
   the other. Implementation: scope `ReconcileRemoteNotSeen` by the `ps:` `NativeID` namespace
   (e.g. a variant that only touches rows whose `NativeID` has the shell prefix for that peer),
   passing the set of shell `NativeID`s seen this cycle.

This keeps shells on the same persisted-mirror + reconciler guarantees as option-offers — not
a fragile in-memory cache. If a peer's `/public-stock` fetch fails in a cycle, its shells are
left **as-is** (not expired) — exactly how the option path treats a failed peer fetch (avoid
flapping rows out on a transient error).

### 3.3 Bid path (unchanged + a freshness guard)

The existing bid path is unchanged in shape:
`POST /api/v3/otc/options/:id/bid → openRemoteNegotiation → GetRemoteByID → POST /negotiations`.
The buyer already supplies `quantity/strike/premium/settlement` + their settlement account, and
the seller + ticker come from the resolved row — so a shell bids exactly like an option-offer.

**Added freshness guard (the "check live data if DB is stale" requirement):** when the resolved
row has `HasPresetTerms == false`, before dispatching the negotiation, **re-fetch the peer's
live `/public-stock`** and confirm the `(seller, ticker)` is still published. If it's gone,
return `FailedPrecondition` "peer no longer offers this stock" instead of dispatching a doomed
negotiation. (Option-offer rows keep today's behavior — the seller's bank validates on
`POST /negotiations`.)

### 3.4 No dedup — frontend dedupes/filters (decision)

The backend surfaces **all** rows (shells AND option-offers). A cohort peer running our code
exposes both endpoints, so for the same (seller, ticker) the buyer sees a preset option-offer
**and** a public-stock shell; a base-spec peer yields only the shell. The frontend dedupes and
filters using the `HasPresetTerms` flag plus the existing per-offer bank identity
(`routing_number` / `initiator_bank_code`) — e.g. "for our own instances prefer preset offers;
for foreign banks use the shell." No backend suppression.

### 3.5 Any-price bids/counters (verify; expected no change)

Requirement: because peers can't see our preset terms, they must be able to bid/counter with
**any** strike/premium/date on our listings. Direction-1 testing already showed the inbound
`/cross-bank-protocol` negotiation/counter handlers accept arbitrary terms (marko set strike
44 then 45; both accepted) — they validate currency-in-enum, ids, and `amount > 0`, with **no
minimum-term floor**. Action: **verify** there is no preset-minimum enforcement on
`peer_otc_grpc_handler.CreateNegotiation`/`UpdateNegotiation`; document it. No code change
expected. (If a floor is found, remove it for the cross-bank path — a separate, flagged item.)

### 3.6 Response field (additive)

Add `has_preset_terms` to the remote-offer projection returned by `GET /api/v3/otc/options`
(and any single-offer GET). Additive — existing clients ignore unknown fields; no shape break.

## 4. Frontend advice (NOT implemented — advisory only)

To render the new rows cleanly, the frontend should, for `has_preset_terms == false`:
- show "Negotiable — no preset terms" instead of the `0 / 0` strike/premium,
- present an "open negotiation" CTA (the user types strike/premium/settlement) rather than an
  "accept these terms" CTA,
- use `has_preset_terms` + bank identity to dedupe/filter (own-instances vs foreign).
`has_preset_terms == true` rows render exactly as today. I will not touch frontend code unless
you ask.

## 5. Components & boundaries

| Unit | Responsibility | Touched |
|---|---|---|
| `otccache.OptionRefresher` | per-cycle: also fetch `/public-stock`, build shells, upsert, source-scoped reconcile | yes |
| `otccache` public-stock→shell builder | pure `PublicStock → []OTCOffer` shell conversion (unit-testable) | new helper |
| `OTCOfferRepository` | `UpsertRemote` (unchanged); `ReconcileRemoteNotSeen` gains a shell-namespace-scoped variant; `GetRemoteByID` (unchanged) | yes |
| `model.OTCOffer` | new `HasPresetTerms` column | yes |
| `OTCOptionsHandler` (`openRemoteNegotiation`) | shell freshness re-validation before dispatch | yes |
| remote-offer list projection | expose `has_preset_terms` | yes |
| `peer_otc_grpc_handler` inbound | **verify** no min-term floor (no change expected) | verify only |

## 6. Error handling

- Peer `/public-stock` fetch fails in a cycle → keep that peer's existing shells (no expiry); log.
- Bid on a shell whose stock the peer no longer publishes → `FailedPrecondition` (clear message), no dispatch.
- Synthesized row with a malformed seller id → skipped at build (mirror the option path's skip).

## 7. Testing

**Unit (stock-service):**
- `PublicStock → shell` builder: fields set per §3.1; malformed seller skipped.
- Source-scoped reconcile: expiring shells doesn't expire option-offers and vice-versa.
- Bid freshness guard: dispatch when still published; `FailedPrecondition` when gone.
- `has_preset_terms` set correctly on both row kinds; present in the projection.

**Integration (`test-app/workflows/`):**
- A peer publishes a public stock (no option-offer) → it surfaces with `has_preset_terms=false`
  → buyer bids with chosen terms → negotiation opens on the peer.

**Manual (live, two-stack):** EXBanka-1 buys an option off Banka 4's `/public-stock` end-to-end
(the reverse of the already-verified Direction 1).

## 8. Versioning

MINOR bump (new backward-compatible field + capability). Update `VERSION` + `version.go`.

## 9. Out of scope

`/public-option-offers` path, `/cross-bank-protocol` handler changes, frontend code, and the
exercise key-length item (Banka-4-side, tracked separately).
