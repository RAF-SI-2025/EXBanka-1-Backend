# Option Offers as the Cross-Bank `/public-stock` Surface — Design

**Date:** 2026-06-10
**Status:** Approved (design); pending implementation plan
**Authorization:** User explicitly authorized the breaking changes (endpoint removal, route contract changes) — this is a deliberate clean break, not a compat-shimmed migration.

---

## 1. Goal

Consolidate cross-bank OTC option discovery onto the SI-TX-native `/public-stock` endpoint, and make our **option offers** (`/api/v3/me/otc/options`) the single backend surface that drives cross-bank `/public-stock` listings and negotiations. An option offer becomes **optionable inventory** — `(owner, ticker, quantity)` — with no preset strike/premium/settlement. Remove the proprietary `/public-option-offers` endpoint entirely. `/otc/stocks` (stock sale via `holding.public_quantity`) loses its cross-bank visibility but stays as a local feature (possible removal later).

## 2. Background / current state

Two independent discovery surfaces exist today:

- **`/public-stock`** (SI-TX §3.1 / §8.1) — peer-facing, served from `holdings WHERE public_quantity > 0` (`HoldingRepository.ListPublic`). Schema is `[{stock, sellers:[{seller: ForeignBankId, amount}]}]` — **no per-offer key**; the only identity is the seller. One entry per (seller, ticker).
- **`/public-option-offers`** — a **proprietary extension** (NOT in the SI-TX spec). Served from `OTCOffer` rows with preset terms; each carries a unique `OfferID`. Base-spec peers (e.g. Banka 4) 404 it.

This split caused the bug that triggered this work: a peer listing the same (seller, ticker) twice on `/public-stock` produced two cache rows colliding on one synthesized `ps:<rn>:<seller>:<ticker>` id, and our option offers (many per ticker, with ids) had no clean mapping onto `/public-stock` (one per seller+ticker, no id). The protocol's `POST /negotiations` body (`OtcOffer`) also carries **no** offer reference — only `sellerId + stock + terms` — so "multiple offers per (seller, ticker)" is not expressible or independently negotiable in the base protocol.

**Resolution:** stop modeling pre-termed option offers; make an option offer = optionable inventory keyed by (owner, ticker), which maps 1:1 onto a `/public-stock` seller entry. Negotiated terms already live on the negotiation chain (`OTCNegotiation.{Quantity,StrikePrice,Premium,SettlementDate}`), so dropping them from the listing is safe.

## 3. Conceptual model

| Concept | Before | After |
|---|---|---|
| Option offer (`OTCOffer`, `/otc/options`) | listing with preset strike/premium/settlement + quantity; many per (owner, ticker) | termless inventory `(owner, ticker, quantity, account)`; **exactly one open offer per (owner, ticker, direction)** |
| Cross-bank discovery of our options | `/public-option-offers` (proprietary, keyed) | `/public-stock` (SI-TX standard, seller-keyed) |
| Per-bid terms | on `OTCNegotiation` chain | unchanged — still on `OTCNegotiation` chain |
| `/otc/stocks` (holdings, `public_quantity`) | served on peer `/public-stock` | **removed from cross-bank**; local-only, untouched otherwise |

Inbound is already aligned: we synthesize option shells from a peer's `/public-stock` (`buildAndMirrorRemoteStockShells`). After this change, peers running our code serve their option offers on `/public-stock`, and base-spec peers serve their stock holdings on `/public-stock`; both arrive as termless shells and negotiate identically. The model is symmetric.

## 4. Requirements

- **R1.** Remove `/public-option-offers` end to end: the peer-facing gateway route + handler, the stock-service gRPC `GetPublicOptionOffers` and `PeerOTCForwarder` passthrough, the proto `GetPublicOptionOffers` RPC and `PeerPublicOptionOffer` message, and the **outbound ingestion** of peers' option-offers (`OptionRefresher.fetchPeer` + `buildAndMirrorRemoteOffers`). Keep `fetchPeerStocks` / `buildAndMirrorRemoteStockShells` — the `/public-stock` shells become the only cross-bank option discovery path.
- **R2.** Serve our **open, sell-initiated, public** option offers on the peer `GET /api/v3/cross-bank-protocol/public-stock` as `{stock, sellers:[{seller, amount}]}`. `GetPublicStocks` reads `OTCOffer` rows instead of `holdings.ListPublic()`.
- **R3.** Enforce **one open option offer per (owner, ticker, direction)** per bank, via a partial unique index. Creating a second open offer for the same key returns **409**.
- **R4.** Drop preset terms from the listing: remove `OTCOffer.{strike_price, premium, settlement_date, strike_currency, premium_currency, has_preset_terms}`. Terms are negotiated per-bid (already on the chain).
- **R5.** Add **`PUT /api/v3/me/otc/options/:id`** to set the offer's **total** quantity (edit up or down), with the validations in §6.
- **R6.** `/otc/stocks` stays functional and untouched except that it no longer drives the peer `/public-stock`.

## 5. Schema changes (`stock-service/internal/model/otc_offer.go`)

- **Remove fields/columns:** `StrikePrice`, `Premium`, `SettlementDate`, `StrikeCurrency`, `PremiumCurrency`, `HasPresetTerms`. Remove `RemoteStockShellPrefix`-dependent `HasPresetTerms` branching in the repository/cache; the shell upsert no longer needs to force `has_preset_terms=false` (the column is gone).
- **Add a partial unique index:** on `(initiator_owner_id, ticker, direction)` `WHERE status = 'open' AND local = true`. (Postgres partial unique index; GORM raw DDL in an idempotent post-AutoMigrate step, mirroring the watchlist partial-unique-index precedent.)
- **Migration (idempotent, runs once on startup):** for each `(initiator_owner_id, ticker, direction)` group of **open local** offers, keep the oldest row, **sum** the others' `quantity` into it, and mark the rest `consumed`. This clears duplicates so the unique index can be created. Log the count merged.

> Note: `OTCNegotiation` is unchanged — it keeps `Quantity/StrikePrice/Premium/SettlementDate`; that's where negotiated terms live.

## 6. API changes (⚠️ frontend-facing — advisory in §9)

| Route | Change |
|---|---|
| `POST /api/v3/me/otc/options` | Request body drops `strike_price`/`premium`/`settlement_date`. New body: `{ stock_id \| ticker, quantity, account_id, direction }`. Returns **409 `conflict`** when an open offer for `(owner, ticker, direction)` already exists (message directs the caller to PUT the existing offer). Ownership + account checks unchanged. |
| `PUT /api/v3/me/otc/options/:id` *(new)* | Body `{ quantity }` sets the offer's **total** quantity. Validations: `quantity > 0`; `quantity >=` shares already committed to in-flight negotiation chains / formed contracts on this offer (cannot shrink below outstanding commitments); `quantity <=` the owner's holding for the ticker. Ownership verified gateway-side. Optimistic-lock safe (load → modify → `Save`, check `RowsAffected`). |
| `GET /api/v3/cross-bank-protocol/public-option-offers` | **Removed** → 404. |
| `GET /api/v3/cross-bank-protocol/public-stock` | Now lists our open sell-initiated public option offers as `{stock, sellers:[{seller, amount}]}`. Seller id format unchanged (`client-<n>` / `bank` / `employee-<n>`). |
| `GET /api/v3/otc/options` and `/api/v3/me/otc/options/*` reads | `has_preset_terms` removed. `strike_price`/`premium`/`settlement_date` (+ currencies) **remain on the DTO but are re-sourced from the relevant negotiation chain, contextual to the viewer** (see §6.5): bidder → their chain's current terms; owner → his most recent counter; none → empty. Negotiation-chain reads are unchanged. |

Validation in the gateway handler (`oneOf`, `positive`, ownership via `ResolveAndCheckAccount`) is added/updated for the new create and PUT bodies, before the gRPC call, per the gateway input-validation + ownership requirements.

## 6.5 Read-surface term projection (FE display)

The listing no longer holds preset terms, but the FE still needs `strike_price` / `premium` / `settlement_date` (+ their currencies) to render the marketplace. These fields stay on the unified offer DTO (`otccache.OptionOffer` / the `/api/v3/otc/options` read) but are **re-sourced from the relevant negotiation chain, contextual to the viewer**:

- **Bidder** (`me_owner = false`) with a chain on the listing → the fields show **that chain's current terms** (`my_negotiation`'s latest `StrikePrice/Premium/SettlementDate`) = "your current position in the chain."
- **Offer owner** (`me_owner = true`) → the fields show **the owner's most recent counter** — the terms from the latest `OTCNegotiationRevision` authored by the owner across any chain on the listing (ordered by time, newest first). Empty if the owner has not countered yet.
- **No relevant chain** → empty strings (pure inventory).

Constraints:
- The shared `otccache` snapshot stays viewer-agnostic — it holds the listing inventory (`ticker`, `amount`) plus the existing `best_bid` / `best_ask` / `active_chains_count` aggregate. The **per-viewer term projection is request-scoped**, applied where the viewer identity is already resolved and `me_owner` / `my_negotiation_id` are stamped (the service/handler enrichment path, not the cache).
- `best_bid` / `best_ask` / `active_chains_count` are unchanged and remain the owner's view of the spread across all bidder chains.
- **Shape unchanged:** one row per listing; no per-chain fan-out. Per-chain detail continues to come from the negotiations endpoints (`GET /api/v3/me/otc/options/negotiations` + `/:nid/revisions`).

New repository support: "latest revision authored by a given owner-principal for a listing" (for the owner projection). The bidder projection reuses the already-resolved `my_negotiation` terms.

## 7. Cross-bank internals

- **`GetPublicStocks` (stock-service)** reads OTCOffers: open, `direction = sell_initiated`, `public = true`, `private = false`, `local = true`, grouped to one seller entry per (owner, ticker) — the partial unique index guarantees one per (owner, ticker). Private/local-targeted offers are not broadcast (they stay intra-bank).
- **`OptionRefresher`** loses its `/public-option-offers` fetch branch; only `fetchPeerStocks` (→ `/public-stock` shells) remains. The earlier same-seller-duplicate **aggregation fix** in `buildAndMirrorRemoteStockShells` stays (base-spec peers may still emit duplicate `(seller, ticker)` rows).
- **proto** (`contract/proto/stock.proto`): delete the `GetPublicOptionOffers` RPC and `PeerPublicOptionOffer` / its request-response messages; regenerate with `make proto`.

## 8. Local negotiation (unchanged)

Opening a chain (`POST /api/v3/otc/options/:id/bid`), countering, and accepting still carry `quantity/strike_price/premium/settlement_date` in the request and store them on `OTCNegotiation`. Because the offer never held authoritative terms for the negotiation (the bidder always proposed them), removing offer-level terms does not change negotiation mechanics. The marketplace list view simply shows `(ticker, available quantity, seller)` per listing instead of preset terms.

## 9. Frontend advisory (no frontend code changes without sign-off)

The frontend currently:
1. **Creates option offers** with `strike_price`/`premium`/`settlement_date` → must drop those fields; send `{ ticker/stock_id, quantity, account_id, direction }`. Handle 409 by routing the user to "edit existing offer."
2. **Edits quantity** → call the new `PUT /api/v3/me/otc/options/:id`.
3. **Reads `/public-option-offers`** (if it ever did for cross-bank discovery) → switch to the unified `/api/v3/otc/options` read surface (which already merges local + remote).
4. **Renders offer terms** (strike/premium/settlement on the listing) → keep the fields, but their meaning changes (§6.5): they now show the **viewer's current chain position** (bidder) or the **owner's most recent counter** (owner), not static preset terms. Empty means "no active chain / inventory only." The FE labels should reflect "your position" / "your latest offer" rather than "asking terms."

Recommended cleaner UX: present an option offer as a single editable "optionable inventory" row per ticker (quantity editable), with strike/premium/settlement shown as the live negotiation position (per §6.5) and the full term history inside the negotiation drawer. This will be written up for the frontend team; **no frontend code is changed as part of this work.**

## 10. Decisions

- **`buy_initiated` offers:** same treatment — lose preset terms, subject to the one-open-offer-per-(owner, ticker, direction) uniqueness, remain local-only (never on `/public-stock`).
- **`/otc/stocks`:** route untouched; only its cross-bank `/public-stock` exposure is removed. May be deleted in a later, separate change.
- **Privacy:** `public`/`private`/`private_to_bank_code` columns are kept; `/public-stock` serves only `public && !private` offers, so private offers are intra-bank only (the old per-bank `/public-option-offers` targeting is gone with that endpoint).

## 11. Out of scope

- Deleting `/otc/stocks` (future).
- Any frontend code changes (advisory only).
- Changing the SI-TX wire protocol or the inbound negotiation/accept/exercise machinery beyond removing the `/public-option-offers` discovery path.

## 12. Testing

- **Unit (stock-service):**
  - `OTCOffer` model: partial-unique-index migration merges duplicates (sum quantity, oldest kept, rest consumed).
  - `OTCOfferService.CreateOffer`: rejects a second open offer for `(owner, ticker, direction)` with AlreadyExists; succeeds for a different ticker/direction or after the prior is consumed/cancelled.
  - New `UpdateQuantity` service method: sets total; rejects `<= 0`, below outstanding commitments, above holding; optimistic-lock conflict path.
  - `GetPublicStocks`: returns OTCOffer-sourced sellers (one per owner+ticker), excludes private/non-open/buy_initiated/remote rows.
  - `OptionRefresher`: no `/public-option-offers` fetch; `/public-stock` shells still ingested; same-seller duplicate aggregation preserved.
  - Read-surface term projection (§6.5): bidder viewer → DTO terms equal their chain's current terms; owner viewer → terms equal the owner's most recent counter (and empty when the owner hasn't countered); non-participant viewer → empty; `best_bid`/`best_ask`/`active_chains_count` unchanged.
- **Unit (api-gateway):** create handler (new body + 409), new PUT handler (validation + ownership), removed `/public-option-offers` route returns 404.
- **Integration (`test-app/workflows/`):**
  - Create an option offer → it appears on a peer's `/public-stock` shell ingestion (two-stack) and is biddable cross-bank end to end (open → accept → contract), proving options-as-stocks works over the standard protocol.
  - Second create for the same ticker → 409; `PUT` quantity → reflected on `/public-stock`.
  - `GET /public-option-offers` → 404.
- All tests assert spec behavior (bodies, side effects, `/public-stock` shape), not just status codes. Use shared helpers.

## 13. Versioning

Breaking changes to existing `/api/v3` routes (removed endpoint, changed `POST /otc/options` contract) — **MAJOR** bump to `3.0.0` (authorized). Keep `VERSION` and `api-gateway/internal/version/version.go` in sync. Update `docs/api/REST_API_v3.md` (remove `/public-option-offers`, update `/otc/options` create, add the PUT, note `/public-stock` now serves option offers), regenerate Swagger, and refresh `docs/protocol/bank-4-interop-otc-results.md` with the unified model. Full `make ci` green before done.
