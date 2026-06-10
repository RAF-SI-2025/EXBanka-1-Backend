# EXBanka-1 (111) ⇄ Banka 4 (444) — Cross-Bank OTC Live Test Results & Fix List

> Companion to [`bank-4-interop.md`](./bank-4-interop.md) (static analysis + payment results).
> This file records the **live OTC option** testing on 2026-06-10: both banks built from
> source and run in Docker (Banka 4 egress-restricted on an internal network, bridged to us
> over an internal `sitx_shared` network), driven with seed accounts.
>
> **Fault split:** EXBanka-1 items are **applied in this repo** (our bank). Banka 4 items are
> **action items to send Banka 4** — they were patched *locally only* to *verify* the fix
> resolves the issue; their team must apply the real change. Every fault is judged against the
> SI-TX spec ([`bank-to-bank-asset-exchange-protocol-spec.md`](./bank-to-bank-asset-exchange-protocol-spec.md)).

---

## 0. TL;DR

| Flow | Direction | Result |
|---|---|---|
| Cross-bank payment (2PC) | both | ✅ works (see `bank-4-interop.md` §6) |
| OTC discover → bid → counter (both sides) → accept → **contract** → reserve → premium → **exercise** | **Banka 4 buys our option** | ✅ **FULL lifecycle verified** after the fixes below |
| OTC turn enforcement (out-of-turn / self-accept) | both | ✅ rejected (409) |
| OTC, our bank **buys** a Banka 4 option | our bank = buyer | ⛔ blocked by a discovery-model gap (our-side item E-1) |

The complete OTC option lifecycle (Banka 4 as buyer, **us as seller** — the harder path) now
works end-to-end. It was broken by **4 EXBanka-1 bugs** (all fixed) + **1 Banka 4 bug** (their
action item). Real assets moved and settled correctly on both banks (ledger in §3).

---

## A. EXBanka-1 (OUR bank) — fixes APPLIED in this repo

All four are committed in the working tree, `gofmt`-clean, and spec-compliant. `VERSION`
bumped `2.22.2 → 2.22.3`.

### A-1. Outbound OTC counter dropped `buyerAccountNumber` → peer 400
- **File:** `stock-service/internal/handler/otc_negotiation_remote_action.go` — `counterRemoteNegotiation`, `offerBody` map.
- **Symptom:** every counter we PUT to Banka 4 → `400 "buyerAccountNumber is required"`.
- **Root cause:** the create path (`otc_negotiation_remote.go:210`) sends `buyerAccountNumber`, but the counter compose omitted it.
- **Fix:** echo `rc.offer.BuyerAccountNumber` on the counter body when present.
- **Spec:** `buyerAccountNumber` is a **cohort-agreed extension** (not in the base `OtcOffer`; Banka 4 requires it, immutable, on every PUT). The field is additive — base-spec peers ignore unknown fields — so emitting it is spec-compatible and required for cohort interop.

### A-2. Outbound counter's **local mirror** also dropped `buyerAccountNumber` (2nd counter failed)
- **File:** same file — `mirrorOffer` struct.
- **Symptom:** the *first* seller counter worked, but a *second* consecutive counter failed `400 "buyerAccountNumber is required"` again.
- **Root cause:** A-1 reads the field from the stored mirror, but the mirror compose didn't persist it → empty on the next round.
- **Fix:** set `BuyerAccountNumber: rc.offer.BuyerAccountNumber` on `mirrorOffer`.
- **Spec:** same as A-1.

### A-3. Outbound accept/counter used the **wrong routing** in the path `{rn}`
- **File:** same file — `resolveRemoteNegAction`, `remoteNegContext.rid`.
- **Symptom:** our seller's accept → Banka 4 `400 "routingNumber does not identify this negotiation"`.
- **Root cause:** `rid = row.RoutingNumber`. For a chain **we host as seller** (we minted the negotiation id), `row.RoutingNumber` holds the *buyer's* bank (444), so we dispatched `/negotiations/444/<id>/...`. For a buyer-hosted chain it happened to coincide, masking the bug.
- **Fix:** `rid = sellerRouting` (the negotiation id's owner).
- **Spec §8.3/§8.6:** the path `{rn}/{id}` is the negotiation ID's `routingNumber`/`id`, and the **seller mints the id** — so `{rn}` is always the seller's routing. This makes us conformant (it was a bug).

### A-4. `incoming_reservations.reservation_key` was `varchar(64)` — overflowed on exercise
- **File:** `account-service/internal/model/incoming_reservation.go:34` (`size:64 → size:160`).
- **Symptom:** exercise incoming strike-credit → account-service `ERROR: value too long for type character varying(64)` → vote `UNACCEPTABLE_ASSET`.
- **Root cause:** the key is the composite `"<peerRoutingNumber>:<locallyGeneratedKey>"`; with a 64-byte `locallyGeneratedKey` (spec max) the column needs > 64. `OutgoingReservation` was already `size:160`; the incoming side was inconsistently left at 64.
- **Fix:** widen to `size:160` (auto-migrate widens the column; a runtime `ALTER` was also applied to the live DB for immediate effect).
- **Spec §5.2:** `locallyGeneratedKey ≤ 64 bytes`; our routing-prefixed composite legitimately exceeds 64. Internal storage only — no wire change.

### A-5 (OPEN, our-side recommendation — not a 1-line fix)
**Our cross-bank BUYER flow can't initiate an option negotiation off a peer's public STOCK.**
Our buyer bids on a remote **option offer** discovered via our `/public-option-offers`
*extension*; the base protocol only defines `/public-stock` (discover a stock, then negotiate
an option). Banka 4 (base-spec) doesn't expose `/public-option-offers`, so our buyer has
nothing to bid on → **"our bank buys a Banka 4 option" cannot start**. Recommended: support
negotiating an option off a peer's `/public-stock` listing (the spec's discovery model). This
is a feature change, not a bug patch, so it is **not** applied here.

---

### A-6. Verified (no change): inbound peer bids/counters accept any strike/premium/date

The inbound `/cross-bank-protocol` negotiation handlers (`peer_otc_grpc_handler.go`
`CreateNegotiation`/`UpdateNegotiation`) enforce **no minimum strike/premium floor** — they
validate only currency-in-enum, well-formed buyer/seller ids, `amount > 0`, and turn/`isOngoing`.
A peer (which cannot see our preset terms) may therefore bid/counter with any strike, premium,
or settlement date, and we accept it as a proposal to counter or decline (Direction-1 testing
already exercised arbitrary strikes 44→45). This is required for the new "negotiate an option off
our `/public-stock`" capability and needs no code change. Confirmed during the 2026-06-10
public-stock-option feature work.

## B. Banka 4 (THEIR bank) — ACTION ITEMS to report (NOT applied to their deployment)

> Patched locally only to **verify** each fix unblocks the flow. Banka 4 must implement these.

### B-1. Exercise `transactionId` / idempotence key exceeds the 64-byte cap → crashes their own DB  🔴
- **File:** `services/interbank-service/internal/service/peer_otc_service.go:677` (`coordinateExercise`/`executionKey`).
- **Symptom:** exercising a contract whose seller is us → Banka 4 returns **500**; their log: `INSERT INTO "interbank_prepared_transactions" ... ERROR: value too long for type character varying(64)`.
- **Root cause:** `executionKey = fmt.Sprintf("peer-otc-exercise-%d-%s-%s", AuthorityRoutingNumber, contract.ID, uuid.NewString())`. With our **36-char UUID** contract id, that's **95 chars** — it overflows their own `interbank_prepared_transactions.id varchar(64)` *before any dispatch*. Their own negotiation ids are short ("10"), so they never hit it; it only triggers against a peer (us) that uses UUID ids.
- **Spec §3/§5.3:** `ForeignBankId.id` and `locallyGeneratedKey` are **≤ 64 bytes**. A 95-char transactionId is a spec violation.
- **Required fix:** generate a bounded key ≤ 64 bytes that *also* survives the peer-routing prefix (`"<rn>:"`) and `-new`/`-commit` suffixes the value picks up downstream. Their `accept` key `peer-otc-accept-<rn>-<id>` already sits at the edge (our composite hit exactly 64); `exercise` (2 chars longer) tips over. **Verified locally:** shortening the prefix to e.g. `otc-exer-<rn>-<id>` makes every variant fit and the exercise completes end-to-end (§3). They should adopt a short, bounded scheme for *all* keys (accept new/commit, exercise new/commit).
- **Note:** even after dropping the trailing UUID, `peer-otc-exercise-<rn>-<id>` + `-commit` = 65 chars still violates 64 — so the prefix itself must be shortened, not just the suffix.

### B-2. `PUT /negotiations` counter-offer returns `204` instead of spec `200`  🟡
- Spec §4/§8.3 say `200`. Harmless — our client accepts any 2xx — but a conformance deviation. (Detail in `bank-4-interop.md` §3 #3.)

### B-3. Requires `buyerAccountNumber` on every `OtcOffer` (create AND counter)  🟡
- Their cohort extension; stricter than the base spec (which has no such field). **Not a bug to fix** — it's the agreed extension — but listed so it's understood: our side now always echoes it (A-1/A-2). They should keep tolerating peers that send it.

### B-4. (Informational) money handled as `float64`; rejects money-as-JSON-string; doesn't expose `/public-option-offers`
- See `bank-4-interop.md` §3 (#4, #6) and A-5 above. `/public-option-offers` is **our** non-spec extension, so its absence is not a Banka 4 fault — the discovery fix is on our side (A-5).

---

## C. Full OTC lifecycle — verified ledger (Banka 4 buys our AAPL option, EUR)

Seller = our client-1 (EUR acct `111000158369546221`, holds 100 AAPL). Buyer = Banka 4 client 2
"marko" (EUR acct `444000112345678921`). Negotiation `0563ae2f…`, hosted by us (seller).

| Step | Action | Result |
|---|---|---|
| discover | marko reads our `/public-stock` | sees seller `{111,client-1}` AAPL ×50 ✅ |
| bid | marko `POST /api/peer-otc/negotiations` (strike 44, prem 2) | negotiation created ✅ |
| turn guard | marko counters own offer | `409` turn violation ✅ |
| **counter (seller)** | our client-1 counters (strike 46, prem 3) | ✅ (fixed by A-1/A-3) |
| turn guard | our client-1 counters again | rejected ✅ |
| **counter (buyer)** | marko counters (strike 45, prem 2.5) | `204` ✅ |
| **accept (seller)** | our client-1 accepts marko's offer | `200`, status `accepted` ✅ (fixed by A-3) |
| contract | both banks | `active` contract, AAPL ×10 @ 45 EUR on **both** ✅ |
| reservation | seller shares | holding `reserved=10` ✅ |
| premium | buyer→seller | marko −2.5 EUR, client-1 +2.5 EUR ✅ |
| **exercise** | marko exercises (strike 450 EUR) | `exercised` ✅ (after B-1 + A-4) |

**Settlement (before → after exercise):**

| Account / holding | Before | After | Δ |
|---|---|---|---|
| our client-1 AAPL holding | qty 100, reserved 10 | **qty 90, reserved 0** | −10 delivered, reservation released |
| our client-1 EUR acct | 2.5 | **452.5** | +450 strike (had +2.5 premium) |
| marko EUR acct | 1947.5 | **1497.5** | −450 strike |
| marko AAPL ownership | 100 | **110** | +10 shares received |
| contract status | active | **exercised** | — |

Double-entry balanced; reservation consumed; option marked exercised on both sides.

---

## D. Test environment / reproduce

- Banka 4: `docker compose -f docker-compose.yml -f docker-compose.exbanka.yml up -d --build` (built from source, default network `internal:true` = no internet egress; ports 8090–8094). Driven via `docker exec banka4be-<svc>-1 curl localhost:<port>`.
- EXBanka-1: `docker compose up -d --build` (gateway on `:8080`).
- Bridge: `docker network create --internal sitx_shared`; connect our `api-gateway`+`interbank-service`+`stock-service` and Banka 4 `interbank_service` (aliases `exbanka-gateway`, `exbanka-interbank`, `exbanka-stock`, `banka4-interbank`). **Note:** `docker compose up` reconciles and drops manual `network connect`s — reconnect after any rebuild.
- Peer registration: our side `POST /api/v3/peer-banks {"bank_code":"444","routing_number":444,"base_url":"http://banka4-interbank:8093","api_token":"bank4-secret-key","active":true}` (**`bank_code` must be the 3-digit routing code**, see `bank-4-interop.md` §6). Banka 4 side: `peers.yaml` entry for 111 → `http://exbanka-gateway:8080/api/v3/cross-bank-protocol`.
- OTC seed: shares via direct holding insert (egress-blocked stacks can't fetch a live stock catalog); declare public via `POST /api/v3/me/otc/stocks` (ours) / `asset_ownerships.public_amount` (Banka 4). No 2FA on any OTC step. Keep deals in a currency both clients hold an account in (here EUR) — neither side does cross-bank FX.

---

## E. Status summary

- **EXBanka-1:** 4 bugs found, **all fixed in-repo** (A-1…A-4), spec-compliant, `VERSION 2.22.3`. One open feature gap (A-5, discovery) blocking us-as-buyer.
- **Banka 4:** 1 blocking bug to fix (B-1, key length), verified; plus minor conformance notes (B-2, B-3).
- **Net:** with the EXBanka-1 fixes applied and Banka 4's B-1 fixed, the **entire cross-bank OTC option lifecycle works end-to-end** (Banka-4-buys-our-option direction, proven with real settlement). The reverse direction additionally needs A-5.
