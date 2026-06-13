# EXBanka-1 (111) ⇄ Banka 4 (444) — Interop Analysis & Required Changes

> **Goal:** make our bank (EXBanka-1, routing `111`) and Banka 4 (routing `444`)
> exchange assets over the *Bank-to-Bank Asset Exchange Protocol*.
> **Fault is assigned strictly against the normative spec:**
> [`docs/protocol/bank-to-bank-asset-exchange-protocol-spec.md`](./bank-to-bank-asset-exchange-protocol-spec.md).
> This document only *specifies* changes — it does not refactor any code.
>
> Both implementations were read at the code level (not just from docs):
> ours under api-gateway `/api/v3/cross-bank-protocol` + `interbank-service` + `stock-service`,
> theirs under `Banka 4 be/services/interbank-service` (router `internal/server/rest.go`).
> Their `interbank-protocol-notes.md` was treated as a hint and verified against actual routes.

---

## 0. TL;DR — the headline

**There are no hard code-level blockers between the two banks.** Both sides are
remarkably spec-conformant and mutually tolerant:

- identical envelope / field names (`idempotenceKey`, `messageType`, `vote`, `reason(s)`, `posting`…),
- money is a **JSON number** on all 8 core endpoints on both sides,
- identical NO-vote reason codes incl. the singular `INSUFFICIENT_ASSET`,
- identical tagged-union discriminants (`PERSON`/`ACCOUNT`/`OPTION`, `MONAS`/`STOCK`/`OPTION`),
- identical accept/exercise posting shapes and signs,
- **both** send the cohort `buyerAccountNumber` extension,
- both accept date-only **and** RFC3339 settlement dates,
- both use lenient JSON decoding (no `DisallowUnknownFields`) and lenient outbound
  status-code checks (ours: any `2xx`; theirs: anything `< 400`), so the cosmetic
  status-code deviations below don't actually break anything.

So **the work to "make them talk" is almost entirely configuration** (Section 2),
plus a short list of cosmetic spec deviations worth fixing for conformance
(Section 4) and a few latent risks to verify live (Section 5).

> **✅ Live-verified 2026-06-10** (both stacks in Docker, Banka 4 egress-restricted):
> cross-bank payments settle **both directions**, and OTC negotiation create works both
> ways. See **Section 6** for the full results. **The one thing that actually blocked
> interop was a config trap: our peer registry must use `bank_code:"444"` (the 3-digit
> routing code), not a friendly name like `"banka4"` — otherwise outbound payments 404
> with "peer bank 444 not registered". This is the most likely cause of the team's
> "not working".**

---

## 1. Connection facts (verified)

| | EXBanka-1 (us) | Banka 4 (them) |
|---|---|---|
| Routing number | `111` | `444` |
| Protocol base path | `…/api/v3/cross-bank-protocol` (OTC + `/interbank` both under this prefix) | **root** of base URL — OTC/user at `/`, 2PC at `/interbank` |
| Their reachable base URL | (our hosted instance) | `http://rafsi.davidovic.io:8083` or `https://banka-4.radenko.rs/interbank-service` |
| Auth header | `X-Api-Key` (per-peer token in `peer_banks`; optional HMAC bundle) | `X-Api-Key` (per-peer `theirApiKey` in `peers.yaml`) |
| Shared token for this pair | `bank4-secret-key` | `bank4-secret-key` (their `ourApiKey`==`theirApiKey` for peer 111) |
| Money on the wire | JSON number (`DecimalNumber`, decimal-safe) | JSON number (`float64`) |

> The `/interbank-service` segment in their K8s URL is an ingress prefix stripped
> before their router; the direct `:8083` host serves the router at root.

---

## 2. REQUIRED SETUP (no code change — do this first; nothing works without it)

These are not anyone's "fault" — they are the out-of-band configuration the spec
(§1, §6.8) assumes is done ahead of time. **This is the bulk of the actual work.**

### 2-A. On OUR bank (EXBanka-1) — register Banka 4 as a peer

`POST /api/v3/peer-banks` (needs `peer_banks.manage.any`) with:

```json
{
  "bank_code": "banka4",
  "routing_number": 444,
  "base_url": "http://rafsi.davidovic.io:8083",
  "api_token": "bank4-secret-key",
  "active": true
}
```

- `base_url` must be the **root** that serves their `/negotiations`, `/public-stock`,
  `/user`, and `/interbank` leaves — i.e. **no** trailing `/interbank` (that double-
  prefixes to `…/interbank/interbank` → 404). Our outbound egress appends the leaf
  names to whatever `base_url` you register.
- `api_token` is used **both** for the outbound `X-Api-Key` we send them **and** for
  the constant-time match of what they send us, so the single value `bank4-secret-key`
  covers both directions (their `ourApiKey` == `theirApiKey` == `bank4-secret-key`).
- Leave `hmac_inbound_key`/`hmac_outbound_key` unset — Banka 4 is API-key-only.

### 2-B. On THEIR bank (Banka 4) — register EXBanka-1 as a peer

Add an entry to their `services/interbank-service/peers.yaml` for us:

```yaml
- routingNumber: 111
  baseUrl: "https://<our-host>/api/v3/cross-bank-protocol"   # ROOT of our prefix, no /interbank
  ourApiKey: "bank4-secret-key"      # the key THEY send us in X-Api-Key
  theirApiKey: "bank4-secret-key"    # the key WE send them; they look us up by it
  displayName: "EXBanka 1"
```

- Their `theirApiKey` must equal the token **we** send (`bank4-secret-key`); their
  `ourApiKey` is what they put in `X-Api-Key` when calling us, so it must equal our
  registered `api_token` (`bank4-secret-key`). Keep both equal to the shared value.
- `baseUrl` must point at our `/api/v3/cross-bank-protocol` **root** (they already had
  a `…/cross-bank-protocol` entry for instance1 in their repo — just update the host
  to whichever EXBanka-1 instance we run for this pairing).

### 2-C. Shared, both sides

- **Same `X-Api-Key`** value on both registrations (above) — `bank4-secret-key`.
- **Currency:** both support the spec's 8 codes (`RSD EUR USD CHF JPY AUD CAD GBP`).
  A cross-bank OTC bid must use an account whose currency equals the listing's
  premium/strike currency — our side rejects a mismatch (no cross-bank FX). Make sure
  the buyer holds an account in that currency.
- **Stock universe (§5.7):** OTC only works for tickers both banks recognize. Confirm
  the demo ticker (e.g. their `CBSH`) exists in both catalogs before testing OTC.

---

## 3. Incompatibility / deviation matrix (with fault per spec)

Legend — Severity: 🔴 breaks interop · 🟡 spec deviation, tolerated by this peer · 🟢 latent / verify.

| # | Topic | Spec says | EXBanka-1 | Banka 4 | Fault | Sev | Net effect on this pairing |
|---|---|---|---|---|---|---|---|
| 1 | `OtcOffer.buyerAccountNumber` | Not in spec. §3: `callNumber` is the **only** optional field; the spec `OtcOffer` has **no** such field. | Sends it on every outbound offer; tolerates/uses it inbound (never rejects if absent). | **Requires** it on `POST` (400 if missing), immutable on `PUT`. | **Banka 4** (added a required non-spec field) | 🟡 | **Works** — it's an agreed cohort extension and we always send a valid one. No change needed for this pair. |
| 2 | `POST /negotiations` success code | `200` + `ForeignBankId` | Returns **`201 Created`** | Returns `200` | **EXBanka-1** | 🟡 | Harmless — their client only errors on `≥400`. Fix for conformance. |
| 3 | `PUT /negotiations/{rn}/{id}` success code | `200` | Returns `200` ✓ | Returns **`204`** | **Banka 4** | 🟡 | Harmless — our client accepts any `2xx`. Fix for conformance. |
| 4 | Monetary value internal type | §3: handle as `BigDecimal`, **never** `float64` | `DecimalNumber` (decimal-safe) ✓ | **`float64`** internally | **Banka 4** | 🟢 | Wire is still a number, so no break; precision/rounding risk on their side at scale. |
| 5 | Zero/again-required premium | §8 `premium` is a mandatory `MonetaryValue`; amount may be any decimal (no `>0` rule) | Accepts `premium.amount ≥ 0` | **Rejects** `premium.amount == 0` and a missing `premium` object (nested `binding:"required"`) → 400 | **Banka 4** (stricter than spec) | 🟢 | Only bites if we ever send a 0 premium — realistic OTC premiums are `>0`. Keep premium `>0`. |
| 6 | Money-as-string on the wire | §3: money is emitted as a JSON **number** | All 8 core endpoints emit numbers ✓. *Non-core* `/public-option-offers` emits strike/premium as **strings**. | Emits numbers; **rejects** a JSON string for money inbound (`float64` decode → 400) | EXBanka-1 *only* on the non-core `/public-option-offers` endpoint | 🟢 | No break: Banka 4 consumes `/public-stock` (numbers), not our `/public-option-offers`. |
| 7 | `NO_SUCH_ASSET` reason | Defined reason code | Defined & emitted | Defined but **never emitted** by their processor | Banka 4 (completeness only) | 🟢 | Cosmetic; both sides accept the code. |
| 8 | `idempotenceKey.routingNumber` on `/interbank` | §5.2: must be the **sender's** routing number | We set it to our own routing on every outbound ✓; we do **not** enforce it inbound | **Enforces** `== X-Api-Key sender` (401 otherwise) | Neither (their check is spec-correct; our inbound laxness is harmless) | 🟢 | Works — we already send our own routing. (Optional hardening: enforce it inbound on our side too.) |
| 9 | Accept response body | §8.6: `200` once the option-forming TX is submitted; body not specified | `{ "transactionId", "status" }` | `PeerContract` object | Neither (spec leaves body open) | 🟢 | No break — neither side depends on the other's accept body (money moves via the follow-up `NEW_TX` 2PC). Verify live. |
| 10 | Peer registration / base URL / token / tickers | §1, §6.8 assume out-of-band setup | — | — | Setup, not fault | 🔴→ | **The only thing that actually blocks interop.** See Section 2. |

**Bottom line on fault:** measured against the spec, Banka 4 carries the most
deviations (#1 required field, #3 `204`, #4 `float64`, #5 zero-premium, #7), and we
carry two (#2 `201`, #6 money-as-string on a non-core endpoint) — but **every one of
them is currently tolerated by the other side**, so none block the demo. The blocker
is configuration (#10).

---

## 4. CHANGES NEEDED — checklist per bank

### 4-A. EXBanka-1 (our bank) — to do

| Priority | Item | Why | Where |
|---|---|---|---|
| **P0 — required** | Register Banka 4 as a peer (routing `444`, base `http://rafsi.davidovic.io:8083`, token `bank4-secret-key`, active). | Without it, all outbound calls 401/route-fail. | `POST /api/v3/peer-banks` (§2-A) |
| **P0 — required** | Confirm the demo ticker(s) exist in our stock catalog and the buyer holds an account in the listing's currency. | OTC bid rejects on unknown ticker / currency mismatch. | data/seed |
| P2 — conformance | `POST /api/v3/cross-bank-protocol/negotiations` should return **`200`**, not `201`. | Spec §4 row 3 says `200`. (Tolerated by Banka 4 today.) | api-gateway `peer_otc_handler.go` `CreateNegotiation` |
| P3 — optional polish | Emit money as a JSON **number** on `/public-option-offers` (currently strings). | Spec §3; harmless for Banka 4 but breaks any strict peer. | api-gateway `peer_otc_handler.go` (`GetPublicOptionOffers`) |
| P3 — optional hardening | Enforce `idempotenceKey.routingNumber == authenticated sender` on inbound `/interbank`. | Spec §5.2; defense-in-depth (Banka 4 already does this). | `interbank-service` peer-tx path |

> **No code change is required from us to interoperate with Banka 4** — only the P0
> configuration. P2/P3 are spec-conformance polish that also helps other cohort banks
> that may be stricter than Banka 4.

### 4-B. Banka 4 (their bank) — to do

| Priority | Item | Why | Where |
|---|---|---|---|
| **P0 — required** | Register EXBanka-1 as a peer (routing `111`, `baseUrl` = our `/api/v3/cross-bank-protocol` root, `ourApiKey`/`theirApiKey` = `bank4-secret-key`). | Without it they 401 us / can't reach us. | `services/interbank-service/peers.yaml` (§2-B) |
| P2 — conformance | `PUT /negotiations/{rn}/{id}` should return **`200`**, not `204`. | Spec §4 row 4 / §8.3 say `200`. (Tolerated by us today.) | their `peer_otc_handler.go` (UpdateCounter) |
| P3 — correctness | Handle money as a decimal (`BigDecimal`), not `float64`. | Spec §3. Precision/rounding safety; no wire change. | their `dto/primitives.go`, `transaction.go`, etc. |
| P3 — leniency | Don't reject `premium.amount == 0` (drop the nested `binding:"required"` on `MonetaryValue.Amount`, or relax to a service-layer `≥0`). | Spec allows any decimal; a strict peer could legitimately send 0. | their `dto/primitives.go` + `validateOffer` |
| P4 — optional | (Their `buyerAccountNumber` requirement is a sanctioned cohort extension — no change needed; listed only so it's not mistaken for a bug.) | — | — |

> **No code change is required from Banka 4 to interoperate with us either** — only
> the P0 `peers.yaml` entry. P2–P3 are spec-conformance / robustness for the cohort.

---

## 5. What already works — do NOT touch (verified)

- **2PC envelope & transport:** `Message{idempotenceKey,messageType,message}`, the three
  `messageType`s, `200`/`202`/`204` transport semantics, NO-vote returned as `200` body.
- **Vote shape:** `{ "vote":"YES" }` / `{ "vote":"NO", "reasons":[{ "reason":CODE, "posting":… }] }`
  — field names and the full reason-code set (incl. singular `INSUFFICIENT_ASSET`) match.
- **Tagged unions:** `TxAccount.type` ∈ `PERSON|ACCOUNT|OPTION` (`id`/`num`), `Asset.type` ∈
  `MONAS|STOCK|OPTION`, `OptionDescription` body — all field names match.
- **Sign convention:** negative = credit/asset-leaves, positive = debit/asset-arrives — both.
- **Accept TX (4 postings):** buyer `ACCOUNT(num=buyerAccountNumber) −premium MONAS`,
  seller `PERSON(sellerId) +premium MONAS`, seller `PERSON(sellerId) −1 OPTION`,
  buyer `PERSON(buyerId) +1 OPTION` — identical on both sides; the bank **receiving**
  the `GET …/accept` forms the TX on both sides.
- **Exercise TX (4 postings):** buyer `ACCOUNT −(qty·strike) MONAS`,
  `OPTION(id=negId) +(qty·strike) MONAS`, `OPTION(id=negId) −qty STOCK`,
  buyer `PERSON +qty STOCK` — identical; `OPTION` used as **account** here vs **asset**
  in accept, matching on both.
- **`/public-stock`:** identical bare array `[{stock,sellers:[{seller,amount}]}]`.
- **`/user/{rn}/{id}`:** identical `{bankDisplayName, displayName}`; each bank only ever
  parses its **own** id format (ours `client-N`/`employee-N`, theirs bare uint), so the
  opaque-id rule (§3) holds and there's no cross-parse conflict.
- **Idempotency / retries:** both persist keys and replay prior responses.
- **Lenient decoding both ways:** no `DisallowUnknownFields` anywhere on either side, so
  extra fields (e.g. our richer OPTION asset body, their `parentOfferId` absence) never
  cause rejections.

---

## 6. LIVE TEST RESULTS — 2026-06-10

Both stacks were built from source and run in Docker (Banka 4 egress-restricted on an
internal network; the two interbank endpoints bridged over a shared internal network
`sitx_shared`). Tested with default seed accounts. **All core flows pass.**

| Test | Direction | Result |
|---|---|---|
| `/public-stock` (auth) | both | ✅ 200 (both empty — no seeded public stock); **401 without `X-Api-Key`** |
| `/user/{rn}/{id}` | ours→B4 | ✅ `{"bankDisplayName":"Banka 4","displayName":"Ana Anic"}` |
| **Cross-bank payment (2PC)** | **111→444** | ✅ **committed**; recipient `444…911` 50000→50300, sender `111…311` 100000→99700 |
| **Cross-bank payment (2PC)** | **444→111** | ✅ **completed**; our EUR `111…521` +50; Banka-4 confirmed YES-vote + COMMIT in logs |
| OTC `POST /negotiations` (+`buyerAccountNumber`) | ours→B4 | ✅ **200** + negotiation id |
| OTC `POST /negotiations` (no `buyerAccountNumber`) | ours→B4 | ✅ **400** — confirms B4 requires it (we always send it) |
| OTC `POST /negotiations` | B4→ours | ✅ **201** + negotiation id — confirms our 201 deviation, **tolerated by B4** |

**Confirmed at runtime:** the 2PC layer is fully bidirectional (each bank acts as both
coordinator and participant); money-as-number, posting signs, vote handling, idempotency
keys, and `X-Api-Key` auth all interoperate; both 201/200 and 200/204 status deviations
are mutually tolerated; `buyerAccountNumber` is threaded correctly.

### New operational findings (from the live run)

1. **🔴 Peer `bank_code` MUST be the 3-digit routing code (`"444"`), not a friendly name.**
   Our outbound dispatcher resolves the destination peer by `GetByBankCode(account[:3])`
   (`interbank-service/.../peer_tx_grpc_handler.go:553`). Registering Banka 4 with
   `bank_code:"banka4"` made every outbound payment fail **404 "peer bank 444 not
   registered"** even though the row existed. **This is the most likely thing the team
   hit.** Fix: register with `{"bank_code":"444","routing_number":444,...}`. (Banka 4's
   side keys peers by `routingNumber` in `peers.yaml`, so it has no equivalent trap.)
2. **🟡 Banka 4 payments need a `verify` step before they dispatch.** `POST
   /api/clients/{id}/payments` only creates the payment in `processing`; the cross-bank
   NEW_TX is sent only after `POST /payments/{id}/verify` (dev magic code `123456`, or a
   TOTP). Not a bug — but a foreign payment sitting in `processing` with no settlement is
   expected until verified. (Our side dispatches immediately on `POST /api/me/payments`.)
3. **🟡 Our reconciler polls Banka 4's `/public-option-offers` every ~5s → 404.** That
   endpoint is **our extension**, not in the base protocol (which only has
   `/public-stock`), so Banka 4 correctly 404s it. Harmless noise, but it means we cannot
   discover a peer's public *option* offers unless the peer implements our extension —
   cross-bank option **stock** discovery works via `/public-stock`; option-*offer*
   discovery does not. Fault: ours (non-spec endpoint). Consider gating the option-offer
   poll to peers known to support it, or treating 404 as "peer has none."
4. **Local (non-interop) gates encountered while testing**, noted so they aren't mistaken
   for interop bugs: our seed client account `daily_limit` is **400 RSD** (raise via
   `PUT /api/v3/accounts/{id}/limits` to send more); Banka 4's seed accounts likewise have
   per-account daily limits.

### Still not exercised live (needs data seeding, not a blocker)

Full **OTC accept → option-contract → exercise** was not run end-to-end because neither
bank has a seeded public OTC stock with a backing share holding (both `/public-stock` and
Banka 4 `/api/otc/public` are empty). The negotiation **create** wire is proven (above);
accept/exercise would additionally require a seller client holding the shares to reserve.
Static analysis (§5) shows the accept/exercise posting shapes match; a live run needs:
a Banka 4 (or our) client with shares → declare public → bid → accept → exercise.

---

## 7. Sources

- Normative spec: `docs/protocol/bank-to-bank-asset-exchange-protocol-spec.md`
- Their deviation notes (verified against code): `Banka 4 be/interbank-protocol-notes.md`
- Our wiring: `api-gateway/internal/router/router_v3.go` (`/cross-bank-protocol` group),
  `api-gateway/internal/handler/peer_*_handler.go`, `interbank-service/…`,
  `stock-service/internal/handler/peer_otc_grpc_handler.go`,
  `stock-service/internal/handler/otc_negotiation_remote*.go`, `contract/sitx/`.
- Their wiring: `services/interbank-service/internal/server/rest.go`,
  `internal/handler/{interbank,peer_otc}_handler.go`,
  `internal/service/{message_processor,peer_otc_service,peer_otc_client}.go`,
  `internal/dto/*`, `internal/config/peers.go`, `peers.yaml`.
