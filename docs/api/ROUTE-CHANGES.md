# API Route Changes — canonical migration log

This file is the **canonical old→new diff** for the EXBanka REST API. It records
routes that were **removed, renamed, or re-shaped** plus the **request/response
body changes** that came with them. The current, working API surface is documented
in [`REST_API_v3.md`](./REST_API_v3.md) — that file is **current-state only** and
no longer carries migration prose; this file holds the history.

All routes are served under `/api/v3/`. v1 and v2 were retired (plan E, 2026-04-27):
any `/api/v1/*` or `/api/v2/*` request returns **404**.

---

## 1. Unified OTC marketplace + bank-as-cross-bank-principal effort

The OTC (over-the-counter) surface was consolidated so that **one route serves
both intra-bank (LOCAL) and cross-bank (REMOTE) flows**. The frontend no longer
chooses a "peer" route — dispatch happens inside stock-service based on the
listing/contract routing. The separate `/me/peer-otc/*` client surface and the
peer-exercise route were deleted in a clean cut.

### 1.1 Removed routes → unified replacements

| OLD route (removed) | NEW route (unified, local + cross-bank) |
|---|---|
| `POST /api/v3/me/peer-otc/negotiations` | `POST /api/v3/otc/options/:id/bid` (open a chain; `:id` = the discovery row's `local_id`, local or remote) |
| `GET /api/v3/me/peer-otc/negotiations` | `GET /api/v3/me/otc/options/negotiations` (caller's chains, LOCAL + REMOTE merged; remote rows carry `kind="remote"`) |
| `PUT /api/v3/me/peer-otc/negotiations/:rid/:id` | `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter` |
| `POST /api/v3/me/peer-otc/negotiations/:rid/:id/accept` | `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` |
| `DELETE /api/v3/me/peer-otc/negotiations/:rid/:id` | `DELETE /api/v3/me/otc/options/:id/negotiations/:nid` (bidder withdraws) |
| `POST /api/v3/me/otc/contracts/peer/:id/exercise` | `POST /api/v3/otc/contracts/:id/exercise` (unified; cross-bank path gained an optional `buyer_account_number` body field) |

These removals landed in the SP-2b clean cut (commit `eebdcdc`); 4 dead
`PeerOTCService` RPCs and the gateway `PeerOTCInitiateHandler` were retired with
them (`8313fa1`). The cross-bank behaviour they used to carry is now dispatched by
stock-service behind the unified routes above.

> The peer-facing **wire-protocol** routes under `/api/v3/cross-bank-protocol/*`
> (`/interbank`, `/public-stock`, `/public-option-offers`, `/negotiations/*`,
> `/user/*`) are UNCHANGED — they are the frozen SI-TX protocol that cohort banks
> call. Only the *client-facing* `/me/peer-otc/*` convenience surface was removed.

### 1.2 OTC offer namespace rename (Phase 8)

The earlier single-chain `/otc/offers/...` surface was replaced by two separated
marketplaces: `/otc/stocks/...` (no negotiation) and `/otc/options/...` (parallel
per-bidder negotiation chains). Mapping:

| OLD route (removed) | NEW route |
|---|---|
| `GET /api/v3/otc/offers` | `GET /api/v3/otc/stocks` (and `GET /api/v3/otc/options` for options) |
| `POST /api/v3/otc/offers/:id/buy` | `POST /api/v3/otc/stocks/:id/buy` |
| `POST /api/v3/otc/offers/:id/buy-on-behalf` | `POST /api/v3/otc/stocks/:id/buy-on-behalf` |
| `POST /api/v3/otc/offers` (create listing) | `POST /api/v3/me/otc/options` |
| `POST /api/v3/otc/offers/:id/counter` | `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter` |
| `POST /api/v3/otc/offers/:id/accept` | `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` |
| `POST /api/v3/otc/offers/:id/reject` | `POST /api/v3/me/otc/options/:id/negotiations/:nid/reject` |
| `GET /api/v3/otc/offers/:id` | `GET /api/v3/otc/options/:id` |
| `GET /api/v3/me/otc/offers` | `GET /api/v3/me/otc/options` |
| `POST /api/v3/me/portfolio/:id/make-public` | `POST /api/v3/me/otc/stocks` with `direction=sell` |

New routes introduced by the negotiation-chain model (no OLD equivalent):

| NEW route | Purpose |
|---|---|
| `POST /api/v3/otc/options/:id/bid` | Open a negotiation chain (one per bidder per listing) |
| `DELETE /api/v3/me/otc/options/:id/negotiations/:nid` | Bidder withdraws their chain |
| `DELETE /api/v3/me/otc/options/:id` | Initiator cancels the listing (cascade-cancels child chains) |
| `GET /api/v3/otc/options/:id/negotiations` | Every chain on a listing (poster / `otc.read.all` only) |
| `GET /api/v3/otc/options/:id/timeline` | Cross-chain interaction timeline (poster / `otc.read.all` only) |
| `GET /api/v3/me/otc/options/negotiations` | Caller's chains (LOCAL + REMOTE merged) |
| `GET /api/v3/me/otc/options/negotiations/:nid/revisions` | One chain's full revision history |
| `GET /api/v3/me/otc/options/posted` | History of every listing the caller posted (any status) |
| `GET /api/v3/otc/options` | Unified local + cross-bank discovery of open listings |
| `POST /api/v3/me/otc/stocks`, `GET /api/v3/me/otc/stocks`, `DELETE /api/v3/me/otc/stocks/:id` | Caller's stock offers (sell + buy direction) |
| `POST /api/v3/otc/stocks/:id/sell` | Fill a buy-direction offer with the caller's shares |

### 1.3 Removed (no replacement)

| OLD route (removed) | Note |
|---|---|
| `POST /api/v3/me/portfolio/:id/make-public` | Folded into `POST /api/v3/me/otc/stocks` (`direction=sell`) — see 1.2 |

---

## 2. OTC request/response body changes

### 2.1 Offer read bodies (`OTCOfferResponse` — surfaced by `GET /api/v3/otc/options`, `GET /api/v3/otc/options/:id`, `GET /api/v3/me/otc/options`, create/counter/cancel responses)

**Fields gained** (all additive):

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `"local"` (this bank hosts the listing) or `"remote"` (peer-bank mirror). Derived from the new `local` discriminator (see below). |
| `me_owner` | bool | `true` only when the acting caller owns the listing (its poster/seller); always `false` for remote rows. |
| `routing_number` | int64 | Hosting bank's routing number. |
| `bank_code` | string | Hosting bank's 3-digit code. |
| `seller_id` | string | The listing initiator's LOCAL SI-TX seller identity: `"bank"` for a bank-owned listing, `"client-<N>"` for a client-owned one. Now stamped uniformly on every single-offer response (create / detail / counter / cancel). Distinct from the cross-bank wire id `"employee-<N>"` composed only on the SI-TX publish path. |
| `best_bid` | string (decimal) | MAX premium across active (`open`/`countered`) chains on a `sell_initiated` listing. Omitted when no active chains. |
| `best_ask` | string (decimal) | MIN premium across active chains on a `buy_initiated` listing. Omitted when no active chains. |
| `active_chains_count` | int | Count of `open`/`countered` chains. Omitted when zero. |
| `local_id` | uint64 | Stable local surrogate id (on discovery-list rows). Use as `:id` in `GET /api/v3/otc/options/:id`. |
| `my_negotiation_id` | uint64 | The authenticated caller's own (bidder) chain id against this offer, so the FE can jump to it. Omitted/0 when the caller has no chain (a poster who never bid is `me_owner=true` with no `my_negotiation_id` — the two are independent). |
| `my_negotiation_status` | string | That chain's status. Omitted when `my_negotiation_id` absent. |

**Access change:** `GET /api/v3/otc/options/:id` (offer detail) is now **public to any
authenticated caller** (was participant-gated). A non-participant receives the offer with
`me_owner=false` and an **empty `revisions[]`** (the counter history stays gated to
participants). Previously a non-participant got a `not_found` masked as 500.

**Internal discriminator:** an explicit `local bool` column was added to the three
unified OTC tables (`OTCOffer`, `OTCNegotiation`, `OptionContract`) as the
authoritative local-vs-remote flag (replacing the implicit `routing_number == own`
check). `kind` is derived from it. The column is internal; the JSON discriminator
clients read is `kind`.

### 2.2 Negotiation read bodies (`OTCNegotiationResponse`)

**Fields gained:** `kind`, `routing_number`, `bank_code`, `me_owner`
(`true` only when the caller is the parent listing's poster — someone bidding on
*my* listing; a chain the caller opened as the bidder is `false`), and
`minted_contract_id` (uint64, 0 when absent; set on accepted rows that minted a
contract).

### 2.3 Contract read bodies (`OptionContractResponse` — `GET /api/v3/me/otc/contracts`, `GET /api/v3/otc/contracts/:id`)

**Fields gained:** `kind`, `routing_number`, `bank_code`, `me_owner`
(`true` only when the caller is the contract's **buyer/holder** — the formed option
is the buyer's owned asset, so the seller/writer is `false`; note this is the
opposite owner-rule from offers/negotiations).

**Removed:** `peer_contracts[]` and `peer_total` are **no longer returned** by
`GET /api/v3/me/otc/contracts` (proto fields 3 and 4 reserved in `ListContractsResponse`).
Remote contracts now appear exclusively in the unified `contracts[]` with `kind="remote"`.

### 2.4 Bid / counter request validation

`POST /api/v3/otc/options/:id/bid` and `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter`
now **reject non-positive `quantity` / `strike_price`** with HTTP 400. `premium`
must be `>= 0` (zero allowed, negative rejected). The gateway validates these as
strict decimals before forwarding (commit `51afd79`).

### 2.5 Accept response

`POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` response gained
**`cross_bank_transaction_id`** (string, optional). Populated **only** when the
accepted chain resolves to a folded-in cross-bank (REMOTE) chain — it carries the
peer bank's SI-TX `transactionId` so the FE can poll
`GET /api/v3/me/otc/transactions/:txid/status`. Empty string for a LOCAL accept
(commit `92f96ff`).

### 2.6 Exercise request

`POST /api/v3/otc/contracts/:id/exercise` (the unified exercise route) gained an
optional **`buyer_account_number`** body field for the **cross-bank (REMOTE)** path
— the buyer's currency account that pays the strike. Required for a remote
contract, ignored for a local one. The gateway gates it before forwarding (`403`
on mismatch) authoritatively for all principals: a client must own it, a
bank-acting employee must bind a BANK account, an on-behalf employee must bind that
client's account.

---

## 3. Bank as a first-class cross-bank OTC principal (SP-3)

No route changes — behavioural only. An employee acting **as the bank** can now
bid / counter / accept / reject / cancel / exercise in the cross-bank OTC
marketplace exactly like a client, settling against **BANK** accounts/holdings.
On the SI-TX wire, bank-owned offers/bids publish the stable identity
`employee-<ActingEmployeeID>` (never the legacy literal `"bank"`); inbound peers
that still send `"bank"` are parsed identically. The bank now sees its own remote
chains in every read view (`GET /api/v3/me/otc/options/negotiations`, the
per-listing `negotiations`/`timeline` views, history, and the `my_negotiation_id`
stamp), matched by the `employee-<N>` prefix. Client and bank principal scopes
never cross.

---

## 4. Earlier additive release — TODO_final + options-tax (SP1–SP6, VERSION 1.0.0 → 1.6.0)

This release was **fully backward-compatible**: it added routes, optional query
params, and additive response fields only. No existing route, method, status code,
auth requirement, request field, or response field was removed, renamed, or
re-typed.

### 4.1 New routes

| Method | Path | Auth | Sub-project |
|---|---|---|---|
| `GET` | `/api/v3/admin/audit/business-actions` | `admin.audit.view` | SP2 — business audit log |
| `GET` | `/api/v3/me/watchlists` | AnyAuth | SP6 — named watchlists |
| `POST` | `/api/v3/me/watchlists` | AnyAuth | SP6 |
| `DELETE` | `/api/v3/me/watchlists/:watchlist_id` | AnyAuth | SP6 |
| `GET` | `/api/v3/me/watchlists/:watchlist_id/items` | AnyAuth | SP6 |
| `POST` | `/api/v3/me/watchlists/:watchlist_id/items` | AnyAuth | SP6 |
| `DELETE` | `/api/v3/me/watchlists/:watchlist_id/items/:listing_id` | AnyAuth | SP6 |

**SP2 — `GET /api/v3/admin/audit/business-actions`** — who changed an employee limit,
reset a usedLimit, approved/rejected an order, changed permissions, or triggered
manual tax collection. Query: `action` (`limit.set`\|`limit.used_reset`\|`order.approve`\|`order.decline`\|`permissions.set`\|`tax.collect`),
`target_type` (`employee`\|`order`\|`role`\|`tax`), `actor_id`, `since`/`until`
(`YYYY-MM-DD`), `page`/`page_size`. Returns `{entries:[{id, action, actor_id,
target_type, target_id, detail, timestamp}], total, page, page_size}`.

**SP6 — named watchlists** — a user may keep several named lists. `POST /me/watchlists {name}`
(1–64 chars, idempotent on name). `GET /me/watchlists` → `{watchlists:[{id, name,
item_count, created_at}]}` (always includes the lazily-created default). Per-list
item ops mirror the legacy single-list ops but are scoped to `:watchlist_id`. A
list is owner-scoped (403 if not yours); the same listing may live in multiple lists.

### 4.2 Changed routes (additive only — existing clients unaffected)

| Method | Path | What was added |
|---|---|---|
| `GET` | `/api/v3/investment-funds` | New query params `sort_by` (`name`\|`value`\|`profit`\|`annualized_return`\|`volatility`\|`reward_to_variability`\|`max_drawdown`) + `sort_order` (`asc`\|`desc`). New per-fund fields: `annualized_return_pct, volatility_pct, reward_to_variability, max_drawdown_pct, metrics_available, dividend_mode`. (SP3, SP4) |
| `GET` | `/api/v3/investment-funds/:id` | New fields: the four metrics above + `metrics_available`, `dividend_mode`, plus `history` (daily NAV series `[{date, total_value_rsd}]`) and `average_history` (system-average series, indexed to 100). (SP3, SP4) |
| `POST` | `/api/v3/investment-funds` | New optional body field `dividend_mode` (`payout` default \| `reinvest`). (SP4) |
| `PUT` | `/api/v3/investment-funds/:id` | New optional body field `dividend_mode`. (SP4) |
| `GET/POST/DELETE` | `/api/v3/me/watchlist[/:listing_id]` | Behaviour clarified (not changed): the legacy single-list routes operate on the owner's lazily-created default "My Watchlist". Same request/response shapes. (SP6) |

### 4.3 Behaviour-only (no REST changes)

- **SP1 (options/premium tax)** — internal tax recording on the existing OTC accept/exercise sagas and the monthly `POST /api/v3/tax/collect`. Seller premium taxed at accept; buyer taxed at exercise on `(market−strike)×qty − premium`; buyer `−premium` loss at expiry; bank-owned (actuary-on-behalf) gains exempt.
- **SP5 (notifications)** — new in-app/email notification *types* (`LIMIT_CHANGED`, `OTC_CONTRACT_EXPIRING_SOON`) surfaced through the existing `GET /api/v3/me/notifications`.

### 4.4 New config / env vars

| Var | Default | Service |
|---|---|---|
| `FUND_SNAPSHOT_CRON_UTC` | `23:50` | stock-service (SP3) |
| `FUND_METRICS_MIN_MONTHLY_RETURNS` | `2` | stock-service (SP3) |
| `OTC_EXPIRY_WARNING_DAYS` | `3` | stock-service (SP5-E) |

### 4.5 Serialization note

`GET /investment-funds` and `GET /me/watchlists` hand-shape their items so every
field is always present — `metrics_available` (`false`) and `item_count` (`0`) no
longer drop out when they hold default values (raw proto-JSON omits `false`/`0`).
Field names and types are unchanged (numeric ids stay JSON numbers); only the
previously-omitted default fields are now always included.

---

## 5. SI-TX cross-bank OTC: standard seller id + opaque participant ids (2.7.0)

Fixes a partner-reported interop bug where two cross-bank endpoints were
inconsistent and one violated SI-TX §2.3 (`ForeignBankId.id` is opaque; banks
other than the issuing bank **MUST NOT interpret** it; max 64 bytes). The wire
shapes are unchanged — `seller` in `/public-stock` and `buyerId`/`sellerId`/
`lastModifiedBy` in the `OtcOffer` are still spec-mandated `ForeignBankId`
objects — only the **published id value** and the **inbound validation** changed.

### 5.1 `GET /api/v3/cross-bank-protocol/public-stock` — seller id value

| Field | Before (≤ 2.6.6) | After (2.7.0) |
|---|---|---|
| `sellers[].seller.id` | **bare numeric** owner id (e.g. `"7"`; bank-held → `"0"`) | **standard opaque participant id**: `client-<N>` (client-held) or `bank` (bank-held) |

The old bare-numeric form could not be addressed back by a peer — echoing it as
`sellerId` in `POST /negotiations` failed the local seller resolver. The new
value is the SAME form `parseSellerOwner` accepts, so a discovering bank can
return our catalog's seller id verbatim and have it resolve. This is the same
composer (`sellerIDForOwner`) already used by `/public-option-offers`,
negotiation reads, and the local OTC views — one standard value everywhere.

### 5.2 `POST` / `PUT /api/v3/cross-bank-protocol/negotiations[/:rid/:id]` — participant-id validation relaxed to spec

The inbound validator previously format-checked **both** `buyerId.id` and
`sellerId.id` against `^(client|employee)-\d+$`. That was a §2.3 violation: it
rejected spec-conformant peers whose opaque ids use a different scheme (UUID,
`acc-42`, …). New rules:

- `buyerId.id` (the PEER's, `routingNumber` = peer): validated ONLY as non-empty
  and ≤ 64 bytes; stored verbatim, never interpreted.
- `sellerId.id` (OURS, `routingNumber` MUST equal this bank): validated non-empty
  + ≤ 64 bytes at the gateway; the real check is downstream resolution to a local
  seller (`client-<N>` / `employee-<N>` / `bank`). A non-resolvable seller → clean
  4xx, not a gateway format reject.
- Currency / amount>0 / routing checks are unchanged (real spec/business invariants).

**Compatibility:** strictly **widening** for inbound peers (more offers accepted,
none newly rejected within the §2.3 bound). For OUR published catalog the seller
id string changes value — any cohort bank that *parsed* our old bare-numeric id
must accept the standard `ForeignBankId.id` opaque form (which the spec already
requires them to treat as opaque). Version bumped MINOR (2.6.6 → 2.7.0).

## 6. Inbound cross-bank OTC money/authz hardening (2.8.0)

Closes three residual money/authorization holes on the INBOUND (peer-facing)
`/api/v3/cross-bank-protocol/*` path found by adversarial review. **Wire shapes
are unchanged** — these are tightenings of inbound validation on already-spec-
legitimate fields (authenticating the sender + resolving OUR OWN side). The
peer's **buyer** opaque id is still accepted verbatim ≤ 64 bytes per §2.3.

| Hole | Endpoint | New rule (2.8.0) | Reject |
|---|---|---|---|
| 1 — forged `lastModifiedBy` self-accept | `POST` + `PUT /negotiations[/:rid/:id]` | `lastModifiedBy.routingNumber` MUST be the authenticated peer's (zero/absent tolerated). A peer may only mark **itself** as the last actor. *(Refined in 2.8.1 — see §6.1: now DERIVED/overridden, not rejected.)* | `403 forbidden` (no row on POST) |
| 1 — authoritative accept guard | `GET /negotiations/:rid/:id/accept` | The stored `lastModifiedBy.routingNumber` MUST equal **this** bank's routing (the local side last proposed; §3.6: the counterparty accepts). | `403 forbidden`; **no settlement SI-TX, no contract** |
| 2 — orphan accept | `GET /negotiations/:rid/:id/accept` | When this bank hosts the parent listing, an accept against a child of a **cancelled/consumed** listing is rejected authoritatively (independent of cascade timing). | `409 business_rule_violation` |
| 3 — non-well-formed local seller | `POST /negotiations` | `sellerId.id` (OURS) MUST be `bank`, `employee-<digits>`, or `client-<digits>`. A malformed id (`employee-abc`, `employee-`, …) is rejected — no junk row. | `400 validation_error` (no row) |

**Why this is spec-conformant:** validating that the authenticated peer only acts
as itself (`lastModifiedBy.routingNumber == peerRouting`) and that OUR OWN seller
id is a resolvable local participant are sender-authentication and own-side
resolution — both §-legitimate. The peer's **buyer** opaque id stays verbatim and
is NOT format-checked (§2.3). The accepting-party rule is exactly SI-TX §3.6 ("the
person whose negotiation term it is can choose to accept the other party's offer").

**Compatibility:** an honest peer following the protocol is unaffected — it always
stamps `lastModifiedBy` as itself, addresses a real local seller, and accepts only
as the counterparty. The change rejects malicious/malformed inbound traffic only.
Version bumped MINOR (2.7.6 → 2.8.0).

## 6.1 Refined HOLE-1 fix: DERIVE `lastModifiedBy` from the authenticated sender (2.8.1)

Replaces the 2.8.0 *reject-forged-lastModifiedBy* approach (Hole 1, row 1 above)
with a cleaner, more robust one. The receiving bank already KNOWS who sent each
inbound message — the authenticated peer (its routing = `peerRouting`, from the
peer-auth context). So instead of rejecting an inbound `POST`/`PUT` whose
`lastModifiedBy.routingNumber` disagrees with the sender, the bank now **DERIVES**
that routing from the authenticated identity and **overrides** the payload value:

| Endpoint | 2.8.0 behavior | 2.8.1 behavior |
|---|---|---|
| `POST /negotiations` (bid) | forged `lastModifiedBy.routingNumber != peerRouting` → `403` | succeeds `201`; persisted `lastModifiedBy.routingNumber` is **overridden** to `peerRouting` |
| `PUT /negotiations/:rid/:id` (counter) | forged value → `403` | succeeds `200`; persisted routing **overridden** to `peerRouting` |
| `GET /negotiations/:rid/:id/accept` | reads stored `lastModifiedBy`, requires `== ownRouting` | **unchanged** — still reads the persisted row; the stored routing is now derived, so it is trustworthy by construction |

The opaque `lastModifiedBy.id` is still kept **verbatim** (§2.3 — a bank MUST NOT
interpret another bank's opaque id). Our **outbound** counter
(`acceptRemoteNegotiation` / `counterRemoteNegotiation`) already stamps the local
mirror's `lastModifiedBy.routingNumber = ownRouting` — unchanged.

**Net effect on the attack:** a forged counter `lastModifiedBy={thisBank, …}` from
peer `222` now persists with routing **overridden to 222**, so the unchanged accept
guard sees `222 != ownRouting(111)` → `403 forbidden`, no settlement SI-TX, no
contract. The peer's claimed routing is **irrelevant to authorization**; only the
authenticated sender + this bank's persisted state decide who may accept.

**Why this is better:** it no longer rejects an honest peer that happens to fill
`lastModifiedBy` differently — a forged/odd payload routing is simply ignored, not
fatal. Wire shapes are unchanged. Version bumped PATCH (2.8.0 → 2.8.1).

---

## 7. Cross-bank OTC settlement honors the seller's NOMINATED account (2.9.0)

Money-path correctness fix. On a cross-bank OTC option **accept** and **exercise**,
when a party WE host receives funds — the **seller's premium credit** (accept) and
the **seller's strike credit** (exercise) — the destination now resolves to the
account the seller NOMINATED (the local listing's bound `account_id` /
`InitiatorAccountID`), instead of "the seller's first active account in that
currency". Spec-legal per §2.6 (`TxAccount` may target a specific account via
`ACCOUNT{num}`).

| Flow | Before (2.8.x) | After (2.9.0) |
|---|---|---|
| `…/negotiations/:nid/accept` (cross-bank, we host the seller) | seller premium-CREDIT leg carried the seller PARTICIPANT id → receiver picked the seller's **first active** `<premium-ccy>` account | seller premium-CREDIT leg carries the bound **account number** (`ACCOUNT{num}`) → premium lands in the **nominated** account |
| `…/contracts/:id/exercise` (cross-bank, we host the seller) | strike credit at the OPTION pseudo-account resolved the seller's **first active** `<strike-ccy>` account | strike credit targets the seller's **nominated** account, read back from the stored contract via the internal `LookupPeerOptionContract` |

**No REST request/response shape changed.** `account_id` (listing create / bid) and
`buyer_account_number` (exercise) are unchanged; the buyer-debit leg already pinned
the buyer's bound account. Only the **money destination** for the seller's receipts
improved (it now matches the local-accept saga, which already bound
`sellerAccountID = offer.InitiatorAccountID`). When no nomination is resolvable
(free-form negotiation with no local parent listing, an unbound account, or a
wrong-currency account) the legs fall back to the prior first-active resolution —
documented, conservation-preserving behavior. Verified live two-stack (premium and
strike land in the nominated account with a 2-account seller; conservation holds,
no stuck reservations). Version bumped MINOR (2.8.1 → 2.9.0).

---

## 8. Cross-bank OTC discovery is seller-centric — `buy_initiated` offers stay intra-bank (2.9.1)

Spec-conformance hardening, **no REST request/response shape changed**. The SI-TX
bank-to-bank protocol's OTC discovery + negotiation model is strictly
**seller-centric** — there is no wire representation for a buy-side ("I want to
acquire shares") listing:

- §3 / §3.1: a bank publishes only its **sellers'** public stock (`PublicStock`
  lists `sellers`); a buyer "is interested in publicly-listed stocks a Holder owns".
- §3.2: a negotiation is created by `POST /negotiations` sent "**from a Buyer's bank
  to a Seller's bank**" — the receiver is always the seller's bank.
- §3.6.1: "the option pseudo-account is always **in the bank of the seller**".

Our local OTC options marketplace supports both `sell_initiated` and `buy_initiated`
listings, but a `buy_initiated` listing's poster is a **BUYER**, which cannot be
conveyed cross-bank without mislabeling them as a `sellerId` and inverting the
economic roles on accept/exercise. The investigation concluded cross-bank
`buy_initiated` bidding is **out of scope of the spec**, and the correct outcome is
a clean, spec-grounded fail-closed. Three behavior changes enforce this end-to-end:

| Boundary | Before (≤ 2.9.0) | After (2.9.1) |
|---|---|---|
| **Publish** — `GET /api/v3/cross-bank-protocol/public-option-offers` | published BOTH directions (a `buy_initiated` row leaked out with its poster mislabeled as `sellerId`) | skips `buy_initiated` rows — only `sell_initiated` listings are exposed cross-bank |
| **Ingest** — discovery poll of a peer's `/public-option-offers` | mirrored any direction into a biddable remote listing | drops a peer's `buy_initiated` offer at the poll boundary (defense vs a non-conformant peer) |
| **Bid** — `POST /api/v3/otc/options/:id/bid` against a remote `buy_initiated` listing | already failed closed (`77269c2`); generic message | fails closed with a precise spec-grounded reason (HTTP 409 `business_rule_violation`) — now effectively unreachable since ingest drops such offers |

`LOCAL` `buy_initiated` offers/bids are **fully supported and unaffected**. Version
bumped PATCH (2.9.0 → 2.9.1) — no API contract change, peers simply see the correct
(seller-only) discovery set.

---

## 9. Inbound cross-bank counter-offer enforces turn + closed guards (2.9.2)

Spec-conformance fix, **no REST request/response shape changed** (same path, body,
and auth). The inbound `PUT /api/v3/cross-bank-protocol/negotiations/:rid/:id`
(counter-offer) previously persisted the new terms unconditionally and always
returned `200`, even when it was not the calling peer's turn or the negotiation was
already closed. SI-TX §3.3 requires a **409 Conflict** in both cases:

> "If the receiving bank deems that it is its turn to make a counter-offer, rather
> than the [other party's] bank, or if negotiations are closed, a 409 Conflict
> response code is produced." Turn rule: "the turn is buyers if
> `off.lastModifiedBy ≠ off.buyerId`" — generalized, a peer may counter iff the
> OTHER side made the last modification.

Two guards now run on the persisted row **before** the counter is persisted (in
`stock-service` `PeerOTCGRPCHandler.UpdateNegotiation`):

| Case | Before (≤ 2.9.1) | After (2.9.2) |
|---|---|---|
| Negotiation not ongoing (cancelled/accepted/rejected/expired) | persisted + `200` | `409 business_rule_violation`, no mutation |
| Calling peer already made the last modification (out of turn) | persisted + `200` | `409 business_rule_violation`, no mutation |
| In-turn counter on an ongoing negotiation (the other side last acted) | `200` | `200` (unchanged) |

The turn check reads the stored `lastModifiedBy.routingNumber` (which is DERIVED
from the authenticated sender, per the 2.8.1 HOLE-1 fix), so the peer may counter
only when *this* bank last proposed. Immediately after a peer's own bid the stored
routing is the peer's, so a peer counter right after its own bid is correctly out of
turn (the receiving side must counter or accept first). `FailedPrecondition` →
gateway → `409`. Version bumped PATCH (2.9.1 → 2.9.2).
