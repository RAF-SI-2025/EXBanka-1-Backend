# Celina 5 — Inter-Bank / Cross-Bank (SI-TX) — Test Cases

> Scope: **Komunikacija između banaka** — inter-bank money movement (2-phase
> commit) and cross-bank OTC option trading, both riding the SI-TX cohort wire
> protocol. Covers spec §6 "celina-5-cross-bank": two-stack bring-up + peer-bank
> registry, inter-bank 2PC payment (success / cross-currency / fee / audit-trail /
> all failure paths), cross-bank OTC discovery → negotiate → accept → SAGA
> exercise (+ compensation + CHECK_STATUS), SI-TX **wire-protocol conformance**
> (bodies pinned byte-for-byte to `contract/sitx/testdata/*.json`), frozen-route
> assertions, and adversarial ownership/double-charge cases.
>
> Sources of truth:
> - `docs/bank-requirements/Celina 5 2026.docx.md` (2PC payment flow + SAGA OTC
>   flow + option/premium tax + message JSON shapes).
> - `docs/protocol/bank-to-bank-asset-exchange-protocol-spec.md` (the **FROZEN**
>   wire spec) + `contract/sitx/testdata/*.json` (the authoritative wire objects).
> - `docs/api/REST_API_v3.md` §39 (Peer Banks), "Cross-Bank Protocol"
>   (`/api/v3/cross-bank-protocol/...`), `POST /api/v3/me/payments` (cross-bank
>   dispatch), §30 (`/otc/contracts/:id/exercise`), §44 (Transfer Status),
>   §47.2 (options bid/counter/accept).
> - `docs/Specification.md` §25 (inter-bank SI-TX) + §27 (cross-bank OTC) + §17
>   (routes) + §21 (business rules).
> - api-gateway handlers (`peer_bank_admin_handler.go`, `peer_tx_handler.go`,
>   `peer_tx_status_handler.go`, `peer_otc_handler.go`, `peer_user_handler.go`,
>   `peer_tx_dispatcher_handler.go`); `interbank-service/internal/handler/*`
>   (`peer_tx_grpc_handler.go`, `peer_bank_admin_grpc_handler.go`, …);
>   stock-service `peer_otc_grpc_handler.go`.
> - `docker-compose.yml`, `scripts/gen-bank-stacks.py`, `bank1/`, `bank2/`,
>   CLAUDE.md "Running a second bank instance".
> - `Banka 2025 - E2E testovi` ("Feature: Međubankarski prenosi i integritet
>   transakcija") + `Banka 2025 - odbrana flow` ("Provera 2 — OTC trade external",
>   "Provera 3 — plaćanje između banaka, različita valuta").

## Conventions used in this file

- Template per `docs/superpowers/specs/2026-06-07-comprehensive-test-plan-design.md` §4.
- ID scheme `TC-C5-<AREA>-<nnn>`; actor variants get `a/b/c` suffixes. Areas:
  `SETUP` (two-stack + peer registry), `PAY` (inter-bank payment), `PROTO`
  (inbound SI-TX wire conformance), `DISC` (peer OTC discovery), `NEG`
  (peer-facing negotiation routes), `OTC` (client-facing unified cross-bank OTC),
  `SAGA` (cross-bank exercise saga), `TAX` (cross-bank option/premium tax),
  `ADV` (adversarial), `E2E` (defense provere + Gherkin scenarios).
- Standard error codes: `validation_error`/400, `unauthorized`/401,
  `forbidden`/403, `not_found`/404, `conflict`/409, `business_rule_violation`/409,
  `rate_limited`/429, `internal_error`/500.
- **Protocol objects are pinned to the wire spec** (byte-for-byte against the
  fixtures); all OTHER request/response objects need only equivalent
  functionality. Where a TC asserts a protocol body, it cites the fixture.
- Two stacks: **bank1** (`OWN_BANK_CODE=111`, gateway host `:8080`) and **bank2**
  (`OWN_BANK_CODE=222`, gateway host `:8081`). "Bank A" = sender's bank, "Bank B"
  = receiver's bank. `routingNumber` = first 3 digits of an account number.
- PeerAuth = the hybrid `X-Api-Key` OR HMAC bundle (`X-Bank-Code` +
  `X-Bank-Signature` + `X-Timestamp` + `X-Nonce`). All PeerAuth failures → **401
  empty body** (constant-time, no info leak).
- **Sign convention (SI-TX §3):** posting `amount` **negative = credit** (asset
  *leaves* that account, the payer side) / **positive = debit** (asset *arrives*,
  the receiver side). A transaction is balanced when amounts sum to zero per
  asset. Money is a JSON **number**, handled as decimal — never a quoted string on
  emit (a quoted string is tolerated only inbound for legacy peers).

---

## Implementation-state callouts (read before executing)

These are the places where the Celina-5 **prose** and the shipped backend diverge.
Each is tested against the **implemented** behavior and surfaced as a matrix row;
unimplemented requirement variants are marked `NO-ENDPOINT`.

1. **The wire is NEW_TX / COMMIT_TX / ROLLBACK_TX — not the prose's
   Prepare/Ready/Commit.** Celina 5's narrative (Prepare → Ready{end value, FX
   rate, commission} → Commit → credit) describes a generic 2PC. The cohort SI-TX
   wire it points at replaced that with a **balanced double-entry** 2PC: one
   `POST /interbank` endpoint carrying `Message<NEW_TX|COMMIT_TX|ROLLBACK_TX>`; the
   `NEW_TX` reply is a `TransactionVote` (`{vote:"YES"}` / `{vote:"NO",reasons}`),
   **not** a `Ready` object with `end value / FX rate / commission` fields. There
   is **no** wire message carrying a receiver-computed FX rate or commission.
   Tests assert the implemented envelope/vote shapes; the prose's explicit
   `Ready{endValue, kurs, provizija}` payload is `NO-ENDPOINT` (TC-C5-PAY-040).
2. **Cross-bank money send is a PAYMENT, not a transfer.** `POST /api/v3/me/transfers`
   is intra-bank/same-client only and rejects a foreign `to_account_number`.
   Cross-bank dispatch lives in `POST /api/v3/me/payments` (foreign 3-digit prefix
   → `202 Accepted {transaction_id, poll_url, status}`). Tested via `/me/payments`.
3. **No receiver-side FX; cross-bank is single-currency.** SI-TX postings must
   balance per `asset_id` across banks, so the buyer's/sender's bank cannot
   convert at execution time (Spec §27 "Cross-bank FX limitation", Fix #2). A
   cross-bank payment credits the receiver in the **same** currency it left in.
   Therefore odbrana **Provera 3 "različita valuta"** (receiver gets converted
   currency) has **no conformant wire representation**: a currency-mismatched
   cross-bank OTC bid is rejected `400`; a cross-bank payment posts a single
   currency. The "receiver converts to recipient's account currency" requirement
   is `NO-ENDPOINT` (TC-C5-PAY-041 / TC-C5-E2E-030).
4. **Cross-bank fee is SENDER-side, not "due to the receiving bank".** The
   Celina-5 prose + odbrana Provera 3 say the commission is computed by Bank B and
   credited to Bank B. The shipped fee model (`POST /me/payments/preview` /
   `transfer_fees`) is **sender-side** (`total_debit = amount + fee`, recipient
   gets `amount`); there is no receiver-computed commission on the SI-TX wire.
   "Fee due to receiving bank Bank B" is `NO-ENDPOINT` (TC-C5-PAY-042); the
   sender-side fee is covered in `celina-2-core-banking.md`.
5. **Timeout backstop is ~10 minutes, not 10 seconds.** The prose / E2E say "Bank
   B no response in 10 s → cancel + refund". The implementation refunds via:
   (a) the inline NO-vote / error path releasing the held funds, (b) the
   `OutboundReplayCron` 4-attempt cap → `failed` + `ReverseOutboundLocal`, and
   (c) the `OutgoingReservationTimeoutCron` (account-service, `OUTGOING_RESERVATION_TTL`
   default **10m**) backstop. The literal 10-second SLA is `NO-ENDPOINT`
   (TC-C5-PAY-043); the refund-on-no-response invariant IS tested
   (TC-C5-PAY-021/022).
6. **Audit-trail fields are split across endpoints, not one log row.** E2E asks
   for one record with `{sender bank, receiver bank, send time, receive time,
   status}`. The implementation surfaces: sender-side state via
   `GET /api/v3/me/payments/:id/status` (cross-bank UUID → `{transaction_id,
   status, role, last_action_at, last_error}`) and `GET /api/v3/cross-bank-protocol/interbank/:txid/status`
   (`{transaction_id, state, our_role, last_action_at, last_error}`); receiver +
   send/receive times are not a single combined "audit log" object. Covered as
   `partial` (TC-C5-PAY-030/031).
7. **Cross-bank OTC has no separate `/me/peer-otc/*` surface (SP-2b).** Client
   cross-bank bid/counter/accept/cancel/exercise use the **same** unified routes as
   local OTC (`POST /otc/options/:id/bid`, `…/negotiations/:nid/{counter,accept,reject}`,
   `DELETE …/:nid`, `POST /otc/contracts/:id/exercise`); stock-service dispatches by
   the listing's routing. Tested on the unified routes.
8. **Cross-bank OTC buyer tax is deferred (Spec §21).** The frozen exercise wire
   carries neither premium nor market price, so the buyer's bank cannot compute
   `15% × ((market−strike)×qty − premium)`. Cross-bank **sellers** are still taxed
   (15% on premium at accept; strike-gain on exercise); cross-bank **buyers** are
   taxed only via their eventual stock sale. Buyer-side cross-bank exercise tax is
   `NO-ENDPOINT` (TC-C5-TAX-030).
9. **`buy_initiated` OTC listings are intra-bank only (2.9.1).** The seller-centric
   SI-TX discovery model has no conformant representation for a buyer-poster, so
   `buy_initiated` listings are never published cross-bank and a remote
   `buy_initiated` bid is rejected `409`. Tested (TC-C5-OTC-040).
10. **Frozen routes intentionally violate REST.** `GET /negotiations/:rid/:id/accept`
    is a GET that mutates (forms a contract); `GET /interbank/:txid/status` is the
    CHECK_STATUS read. Tests assert the spec'd verbs/paths verbatim — never a
    "corrected" POST/PATCH (memory: interbank-protocol-frozen).

---

## Precondition: two-stack bring-up (cross-bank harness)

Every cross-bank TC below depends on **two independent stacks** that can reach each
other and are registered as peers. Steps (per CLAUDE.md "Running a second bank
instance" + `scripts/gen-bank-stacks.py`):

1. **Build the shared images once** from the repo root: `make docker-up` (or
   `docker compose build`) so `exbanka-1-backend-<svc>:latest` exist.
2. **Generate the per-bank stacks:** `python3 scripts/gen-bank-stacks.py` → writes
   `bank1/docker-compose.yml` (project `bank1`, gateway host `:8080`,
   `OWN_BANK_CODE=111`) and `bank2/docker-compose.yml` (project `bank2`, gateway
   host `:8081`, `OWN_BANK_CODE=222`). Each stack namespaces its own
   containers/volumes/networks and publishes **only** its gateway port.
3. **Bring up both:** `docker compose --env-file bank1/.env -f bank1/docker-compose.yml up -d`
   and `docker compose --env-file bank2/.env -f bank2/docker-compose.yml up -d`.
   Confirm readiness: `GET http://localhost:8080/api/v3/version` and
   `GET http://localhost:8081/api/v3/version` both 200.
4. **Register each as the other's peer.** On bank1 (admin JWT,
   `peer_banks.manage.any`): `POST http://localhost:8080/api/v3/peer-banks` with
   `base_url = http://host.docker.internal:8081/api/v3/cross-bank-protocol`,
   `bank_code="222"`, `routing_number=222`, a shared `api_token`, `active=true`.
   Symmetrically on bank2: register `111` with
   `base_url = http://host.docker.internal:8080/api/v3/cross-bank-protocol`.
   The outbound client appends only the leaf names (`/interbank`, `/public-stock`,
   `/negotiations`, `/user`) to `base_url`.
5. **Seed parties** on each bank (activated client + funded account; optionally a
   supervisor for supervisor↔supervisor OTC).

**Single-stack fallback (used by the existing Go tests):** stand up ONE stack and
register a **mock peer** (an `httptest.Server` whose `/interbank` votes
`{"vote":"YES"}` then `204`) — this exercises the **sender-side** path
(`sitx_conformance_test.go`, `cohort_dry_run_test.go`) and the **inbound** routes
this bank exposes (`sitx_public_stock_seller_id_test.go`) without a real second
bank. TCs note which harness applies.

> **Note on existing Go links.** Most genuinely-two-stack cases are documented as
> `*_RequiresTwoStacks` **skips** in `otc_sp3_test.go` (they need a live partner).
> The byte-shape of the OUTBOUND envelopes is covered single-stack by
> `sitx_conformance_test.go`; the INBOUND wire shapes by gateway handler unit
> tests + `sitx_public_stock_seller_id_test.go`. SAGA compensation is covered for
> the **local** exercise saga by `saga_sg_test.go` (the cross-bank saga reuses the
> same orchestrator). Links reflect this split honestly.

---

## 1. Two-stack setup & peer-bank registry (`/api/v3/peer-banks`)

#### TC-C5-SETUP-001 · Register a peer bank (POSITIVE)
- **Feature:** Peer-bank registry (registracija banke partnera) · **Spec:** Celina 5 §Komunikacija; REST §39 · **Existing test:** api-gateway/internal/handler/peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList; test-app/workflows/cohort_dry_run_test.go::TestCohortDryRun
- **Actor:** admin (`peer_banks.manage.any`)
- **Preconditions:** logged in as admin on bank1.
- **Request:** `POST /api/v3/peer-banks`
  - Auth: `Bearer <admin>`
  - Body: `{"bank_code":"222","routing_number":222,"base_url":"http://host.docker.internal:8081/api/v3/cross-bank-protocol","api_token":"shared-111-222","active":true}`
- **Verification:** n/a
- **Expected:** `201` · body is the peer object with `id`, `bank_code:"222"`, `routing_number:222`, `api_token_preview` (last 4 chars only — full token never returned), `hmac_enabled:false`, `active:true` · side-effect: row in `interbank_db.peer_banks` with bcrypt-hashed token; the peer is now resolvable by PeerAuth.
- **Negative siblings:** missing `bank_code`/`routing_number`/`base_url`/`api_token` → `400 validation_error`; `bank_code`/`routing_number` equal to this bank's OWN (`111`/111) → `400 validation_error` (peer-collision guard SP-2a, TC-C5-SETUP-005).

#### TC-C5-SETUP-002 · List / read peer banks (POSITIVE)
- **Feature:** Peer registry read · **Spec:** REST §39 · **Existing test:** peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList / TestPeerBankAdmin_GetUpdateDelete
- **Actor:** admin
- **Preconditions:** TC-C5-SETUP-001 ran.
- **Request:** `GET /api/v3/peer-banks?active_only=true` then `GET /api/v3/peer-banks/:id`
  - Auth: `Bearer <admin>`
- **Expected:** `200` · `{"peer_banks":[{...,"api_token_preview":"…-222"}]}`; the `:id` read returns the same object; **no full token** in either body · `active_only=true` hides inactive peers.
- **Negative siblings:** `GET /api/v3/peer-banks/999999` (unknown) → `404 not_found`.

#### TC-C5-SETUP-003 · Update mutable peer fields (POSITIVE)
- **Feature:** Peer update (rotate token / base_url / toggle active) · **Spec:** REST §39 · **Existing test:** peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete
- **Actor:** admin
- **Preconditions:** peer 222 exists.
- **Request:** `PUT /api/v3/peer-banks/:id`
  - Auth: `Bearer <admin>`
  - Body: `{"base_url":"http://host.docker.internal:8081/api/v3/cross-bank-protocol","api_token":"rotated-token","active":true}`
- **Expected:** `200` · updated object (only present fields changed; token re-hashed) · side-effect: old token immediately invalid for PeerAuth, new token valid.
- **Negative siblings:** `PUT` on unknown id → `404 not_found`.

#### TC-C5-SETUP-004 · Delete a peer bank (POSITIVE)
- **Feature:** Peer delete · **Spec:** REST §39 · **Existing test:** peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete; cohort_dry_run_test.go::TestCohortDryRun (cleanup path)
- **Actor:** admin
- **Request:** `DELETE /api/v3/peer-banks/:id` · Auth: `Bearer <admin>`
- **Expected:** `204` no body · side-effect: row removed; subsequent cross-bank payment to `222…` → `404 not_found` ("peer bank 222 not registered").
- **Negative siblings:** delete unknown id → `404 not_found`.

#### TC-C5-SETUP-005 · Peer-collision guard: cannot register own code/routing (NEGATIVE)
- **Feature:** Peer-collision invariant (SP-2a) · **Spec:** Spec §25 "Peer-collision invariant"; REST §39 · **Existing test:** —
- **Actor:** admin on bank1 (`OWN_BANK_CODE=111`)
- **Request:** `POST /api/v3/peer-banks` · Body: `{"bank_code":"111","routing_number":111,"base_url":"http://x/api/v3/cross-bank-protocol","api_token":"t","active":true}`
- **Expected:** `400 validation_error` · side-effect: **no row** persisted (keeps `routing_number==OwnRouting()` a reliable local-vs-remote discriminator).

#### TC-C5-SETUP-006 · Peer registry is admin-only (NEGATIVE, actor matrix)
- **Feature:** RBAC on peer registry · **Spec:** REST §39 (`peer_banks.manage.any`, EmployeeAdmin only) · **Existing test:** peer_bank_admin_handler_test.go (permission middleware)
- **Actor:** (a) supervisor, (b) agent, (c) client, (d) unauthenticated
- **Request:** `GET/POST/PUT/DELETE /api/v3/peer-banks…` with each token.
- **Expected:** (a)/(b)/(c) `403 forbidden` (no `peer_banks.manage.any`); (d) `401 unauthorized`.

---

## 2. Inter-bank 2PC payment (Bank A → Bank B)

> Money path = `POST /api/v3/me/payments` with a foreign-prefix `to_account_number`.
> Outbound flow (Spec §25 sender side): detect peer by 3-digit prefix → reserve
> sender funds (HOLD on `available_balance`) → `Message<NEW_TX>` → on `{vote:"YES"}`
> send `Message<COMMIT_TX>` + settle (money leaves) → on `{vote:"NO"}` or error,
> release the hold (refund) + send `ROLLBACK_TX`.

#### TC-C5-PAY-001 · Successful cross-bank payment, same currency (POSITIVE)
- **Feature:** Uspešan prenos sredstava između banaka · **Spec:** Celina 5 §Plaćanja steps 1-8; REST `POST /me/payments`; E2E "Uspešan prenos sredstava između banaka" · **Existing test:** test-app/workflows/cohort_dry_run_test.go::TestCohortDryRun; test-app/workflows/sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped
- **Actor:** client on bank1 (with funded RSD account `111…`)
- **Preconditions:** two stacks up + peered; sender account funded ≥ amount + sender-side fee; receiver account `222…` exists + active on bank2.
- **Request:** `POST /api/v3/me/payments`
  - Auth: `Bearer <client>`
  - Body: `{"from_account_number":"111…","to_account_number":"222999999999999999","amount":10000.00,"currency":"RSD","recipient_name":"Nikola Petrović"}`
- **Verification:** full-flow if client self-serves (→ cross-cutting-verification.md) unless `verification.skip`.
- **Expected:** `202 Accepted` · `{transaction_id:<uuid>, poll_url:"/api/v3/me/payments/<uuid>", status}` · side-effects: **sender debited** 10000 (+ sender-side fee) once COMMIT settles; **receiver credited** 10000 on bank2; outbound row `committed`; `GET /api/v3/me/payments/<uuid>/status` polls to `status:"committed"`; receiver's `222…` balance += 10000 (E2E "račun primaoca treba da se poveća za 10.000 RSD"). Wire: NEW_TX has a balanced negative leg (sender, asset leaves) + positive leg (receiver, asset arrives) — see TC-C5-PROTO-001.
- **Negative siblings:** unknown/inactive destination bank code → `404 not_found` (TC-C5-PAY-010); `amount ≤ 0` → `400 validation_error`; `from_account_number` not owned by caller → `403 forbidden` (TC-C5-ADV-001).

#### TC-C5-PAY-002 · Bank B identified by first 3 digits of account number (POSITIVE)
- **Feature:** Identifikacija banke primaoca po prve 3 cifre · **Spec:** Celina 5 §Plaćanja 1.2; Spec §25 sender side step 1 · **Existing test:** test-app/workflows/sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped (foreign-prefix `222…` dispatches cross-bank)
- **Actor:** client on bank1
- **Request:** `POST /api/v3/me/payments` with `to_account_number` prefix `222…` (peer) vs `111…` (own).
- **Expected:** prefix `222…` (peer, registered) → `202` cross-bank dispatch; prefix `111…` (own) → `201 Created` intra-bank payment (no SI-TX traffic). The 3-digit prefix is the sole router; no separate "destination bank" field is required.
- **Negative siblings:** prefix `333…` (not registered/active) → `404 not_found` before any funds move.

#### TC-C5-PAY-010 · Not-Ready: recipient inactive/nonexistent at Bank B → release reservation + notify (NEGATIVE)
- **Feature:** Otkazivanje uplate ako je banka primalac neodgovarajuća / Not-Ready · **Spec:** Celina 5 §Plaćanja 4-5 (Not Ready → prekid + oslobađanje rezervacije + obaveštenje); E2E "Otkazivanje uplate ako je banka primalac neodgovarajuća"; Spec §25 NoVote codes · **Existing test:** test-app/peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason (mock-peer NotReady harness); api-gateway/internal/handler/peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting
- **Actor:** client on bank1; Bank B's receiver account is **inactive** (or does not exist).
- **Preconditions:** peer 222 registered + active; `222…` receiver account inactive on bank2.
- **Request:** `POST /api/v3/me/payments` to the inactive `222…` account.
- **Expected:** SI-TX `NEW_TX` → Bank B votes `{"vote":"NO","reasons":[{"reason":"UNACCEPTABLE_ASSET"|"NO_SUCH_ACCOUNT","posting":{…}}]}` · side-effects: Bank A **releases the sender's reservation** (held funds restored to `available_balance`, no debit), outbound row → `rolled_back`, a `ROLLBACK_TX` is sent to Bank B, sender notified of failure (E2E: "uplata otkazana, sredstva refundirana pošiljaocu"; Primer: *"Transakcija nije uspela! Račun primaoca je neaktivan"*). Poll status → `rolled_back`/`failed`.
- **Negative siblings:** nonexistent receiver → NO `NO_SUCH_ACCOUNT`; currency the receiver can't hold → NO `NO_SUCH_ASSET`/`UNACCEPTABLE_ASSET`.

#### TC-C5-PAY-020 · Sender insufficient funds → reject "Nedovoljno sredstava" (NEGATIVE)
- **Feature:** Odbijanje prenosa kada pošiljalac nema dovoljno sredstava · **Spec:** Celina 5 §Plaćanja 2 (availableBalance check); E2E "Odbijanje prenosa kada pošiljalac nema dovoljno sredstava" · **Existing test:** —
- **Actor:** client on bank1 with `available_balance = 100 RSD`.
- **Request:** `POST /api/v3/me/payments` `{from:"111…",to:"222…",amount:200,currency:"RSD"}`
- **Expected:** `409 business_rule_violation` (insufficient funds; functional-equivalent of *"Nedovoljno sredstava"*) · side-effects: **no reservation placed / no NEW_TX dispatched** (the sender-side `ReserveOutgoing` fails fast, so no money moves and no peer traffic is generated); balance unchanged.
- **Negative siblings:** amount exactly equal to available (boundary) → succeeds (no fee) / `409` if a sender-side fee pushes total over available.

#### TC-C5-PAY-021 · Bank B no response → cancel + refund sender (timeout/abandon) (NEGATIVE)
- **Feature:** Banka B ne odgovara → otkazivanje + refundacija · **Spec:** Celina 5 §Plaćanja "Scenario neuspeha"; E2E "da Banka B ne odgovara 10 sekundi … sredstva refundirana"; Spec §25 retry/replay + reverse-on-terminal-failure · **Existing test:** test-app/peerbank/server_test.go::TestMock_ConfigureFiveXX_PrepareReturns503 (mock-peer unreachable/5xx harness)
- **Actor:** client on bank1; Bank B's `/interbank` returns 5xx / never answers.
- **Preconditions:** peer 222 registered but its endpoint unreachable.
- **Request:** `POST /api/v3/me/payments` to `222…`.
- **Expected:** `202 Accepted` immediately (funds held). On no-YES within the retry budget, the `OutboundReplayCron` (4-attempt cap, 30 s tick) marks the row `failed` and `ReverseOutboundLocal` **releases the hold (refund)**; the `OutgoingReservationTimeoutCron` (TTL `OUTGOING_RESERVATION_TTL`, default 10m) is the final backstop. Poll `…/status` → `failed`/`rolled_back`; sender funds restored, **no debit ever applied**.
- **Negative siblings:** a late YES racing the timeout — `SettleOutgoing` refuses a non-pending row, so a committed-then-timed-out double-debit cannot occur.

#### TC-C5-PAY-022 · Any-step failure → full rollback + reservation release (NEGATIVE)
- **Feature:** Scenario neuspeha — sve promene se poništavaju · **Spec:** Celina 5 §Plaćanja "Scenario neuspeha"/"Napomena" (mehanizmi za oslobađanje rezervisanih sredstava i vraćanje u prethodno stanje) · **Existing test:** interbank-service/internal/handler/reverse_outbound_local_test.go (ReverseOutboundLocal); inline_rollback_test.go
- **Actor:** client on bank1; inject a failure at COMMIT (peer voted YES, COMMIT_TX errors).
- **Expected:** the outbound row stays `pending` (never `committed`) until the cron settles or reverses; because funds are held (reserve-then-settle), **either** the settle eventually commits cleanly **or** the reversal releases the hold — money is never stranded in a terminal row and never half-applied. Net: an all-or-nothing outcome.

#### TC-C5-PAY-030 · Audit trail of a completed inter-bank transaction (POSITIVE, partial)
- **Feature:** Evidentirati kompletan audit trag međubankarske transakcije · **Spec:** E2E "Evidentirati kompletan audit trag" (fields: Banka Pošiljalac, Banka Primalac, Vreme Slanja, Vreme Prijema, Status) · **Existing test:** api-gateway/internal/handler/peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus
- **Actor:** client (sender) on bank1; admin/peer on bank2.
- **Preconditions:** a payment from TC-C5-PAY-001 completed.
- **Request:** `GET /api/v3/me/payments/<uuid>/status` (sender) and `GET /api/v3/cross-bank-protocol/interbank/<txid>/status` (peer).
- **Expected:** sender status `{transaction_id, status:"committed", role:"sender", last_action_at, last_error:""}`; peer status `{transaction_id, state:"committed", our_role:"receiver", last_action_at, last_error:""}`. **Maps the E2E audit fields:** sender bank = own routing on the sender side / `peer_bank_code` on the receiver record; receiver bank likewise; "Vreme Slanja"/"Vreme Prijema" ≈ each side's `last_action_at`; "Status" = `committed`/`Uspešno`.
- **Status note:** there is no single combined audit-log object carrying all five fields → **partial** (the data exists, split across the two status endpoints).

#### TC-C5-PAY-031 · Sender-facing four-state transfer/payment lifecycle (POSITIVE)
- **Feature:** Client-facing status polling · **Spec:** REST §44 (Transfer Status) + `GET /me/payments/:id/status` · **Existing test:** peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus / TestPeerTxDispatcher_OTCCrossBankStatus
- **Actor:** client (sender)
- **Request:** `GET /api/v3/me/payments/<uuid>/status` (UUID = cross-bank tx id).
- **Expected:** `200` with the cross-bank status object; the UUID is unguessable + only handed to the initiator, so holding it authorizes the read. A numeric id on the same route → intra-bank payment status (`404` if not owned by caller).
- **Negative siblings:** a foreign/guessed UUID → status `unknown` (no leak).

#### TC-C5-PAY-040 · Prose `Ready{endValue, FX rate, commission}` message (NEGATIVE / NO-ENDPOINT)
- **Feature:** Ready odgovor sa krajnjom vrednošću / kursom / provizijom · **Spec:** Celina 5 §Plaćanja 4.4.1 · **Existing test:** — (callout #1)
- **Expected:** **NO-ENDPOINT** — the implemented wire's `NEW_TX` reply is a `TransactionVote` (`{vote:"YES"}` / `{vote:"NO",reasons}`), with no `Ready` payload carrying end value / FX rate / commission. Documented divergence.

#### TC-C5-PAY-041 · Receiver-side currency conversion (NEGATIVE / NO-ENDPOINT)
- **Feature:** Konverzija na strani Banke B (krajnja vrednost u valuti primaoca) · **Spec:** Celina 5 §Plaćanja 4.4.1.2; odbrana Provera 3 · **Existing test:** — (callout #3)
- **Expected:** **NO-ENDPOINT** — SI-TX postings balance per `asset_id`; a cross-bank payment credits the receiver in the same currency it left in. No execution-time FX on the cross-bank wire.

#### TC-C5-PAY-042 · Commission credited to the receiving bank Bank B (NEGATIVE / NO-ENDPOINT)
- **Feature:** Provizija dospeva na račun banke B (banka primalac) · **Spec:** Celina 5 §Plaćanja 4.4.1.6; odbrana Provera 3 · **Existing test:** — (callout #4)
- **Expected:** **NO-ENDPOINT** — the shipped fee is sender-side (`/me/payments/preview`, `transfer_fees`); there is no receiver-computed commission on the SI-TX wire and nothing credits Bank B's account from a cross-bank payment.

#### TC-C5-PAY-043 · 10-second no-response SLA (NEGATIVE / NO-ENDPOINT)
- **Feature:** 10 s timeout · **Spec:** Celina 5 §Napomena; E2E "ne odgovara 10 sekundi" · **Existing test:** — (callout #5)
- **Expected:** **NO-ENDPOINT** for the literal 10 s SLA — refund happens via the replay-cron cap + 10-minute reservation TTL backstop (the refund invariant itself IS tested in TC-C5-PAY-021).

---

## 3. SI-TX protocol conformance — inbound `/interbank` & vote shapes

> These pin the WIRE to the frozen spec + fixtures. All routes are
> `/api/v3/cross-bank-protocol/...`, PeerAuth'd. (Legacy `/api/v3/interbank` etc.
> were removed 2026-05-29 → 404.)

#### TC-C5-PROTO-001 · Inbound NEW_TX "coffee" transfer → YES vote (POSITIVE, byte-pinned)
- **Feature:** NEW_TX envelope + balanced postings + YES vote · **Spec:** protocol-spec §6.2/§6.3/§6.4; Spec §25 receiver side · **Existing test:** api-gateway/internal/handler/peer_tx_handler_test.go::TestPostInterbank_NewTx_SpecShape / TestPeerTxHandler_NewTx_YesPassthrough · **Fixture:** `contract/sitx/testdata/newtx_coffee.json`
- **Actor:** peer bank (PeerAuth via `X-Api-Key`)
- **Preconditions:** the local receiver account in the +amount posting exists, is active, currency matches; peer registered + active.
- **Request:** `POST /api/v3/cross-bank-protocol/interbank`
  - Auth: `X-Api-Key: <peer token>`
  - Body: **exactly** `newtx_coffee.json` — `{idempotenceKey:{routingNumber,locallyGeneratedKey}, messageType:"NEW_TX", message:{postings:[{account:{type:"ACCOUNT",num},amount:-260,asset:{type:"MONAS",asset:{currency:"RSD"}}},{…,amount:260,…}], transactionId:{routingNumber,id}, message:"coffee", paymentCode:"289", paymentPurpose:"debt"}}`
- **Expected:** `200` · body **exactly** `{"vote":"YES"}` (no extra fields) · side-effect: the +amount (arriving) leg's reservation is recorded; `(peer_bank_code, locallyGeneratedKey)` cached in `peer_idempotence_records`.
- **Negative siblings:** see TC-C5-PROTO-003..008 for each NO reason.

#### TC-C5-PROTO-002 · COMMIT_TX / ROLLBACK_TX finalise/release → 204 (POSITIVE)
- **Feature:** COMMIT_TX / ROLLBACK_TX correlation by `transactionId` · **Spec:** protocol-spec §6.3; Spec §25 receiver side; REST cross-bank-protocol · **Existing test:** sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped (asserts COMMIT_TX correlates to NEW_TX with a *distinct* per-message idem key)
- **Actor:** peer bank
- **Preconditions:** TC-C5-PROTO-001 left a prepared/reserved tx.
- **Request:** `POST /interbank` `{idempotenceKey:{…,locallyGeneratedKey:<new>}, messageType:"COMMIT_TX", message:{transactionId:{routingNumber:111,id:"k-coffee-1"}}}` (then a ROLLBACK_TX variant on a different tx).
- **Expected:** `204` empty body · COMMIT finalises the reservation (money/credit applied); ROLLBACK releases it. Both idempotent (replay → `204`). **Correlation is via `message.transactionId`, NOT the envelope idem key** (which is unique per message).
- **Negative siblings:** COMMIT for an unknown `transactionId` → idempotent no-op `204` (no record).

#### TC-C5-PROTO-003 · Inbound NEW_TX, unbalanced → NO `UNBALANCED_TX` (NEGATIVE)
- **Feature:** Balance check · **Spec:** protocol-spec §6.6 step 1 / §10; Spec §25 NoVote codes · **Existing test:** peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting · **Fixture:** `vote_no.json` (NO-vote shape)
- **Actor:** peer bank
- **Request:** `POST /interbank` NEW_TX whose postings do **not** sum to zero per asset.
- **Expected:** `200` · `{"vote":"NO","reasons":[{"reason":"UNBALANCED_TX"}]}` — note `UNBALANCED_TX` is the **only** reason that carries **no** `posting`. No reservation placed.

#### TC-C5-PROTO-004 · Inbound NEW_TX, unknown account → NO `NO_SUCH_ACCOUNT` (NEGATIVE)
- **Feature:** Account-exists check · **Spec:** protocol-spec §6.6 step 2 / §10 · **Existing test:** — · **Fixture:** `vote_no.json` (shape)
- **Request:** NEW_TX whose local posting names a non-existent `ACCOUNT.num`.
- **Expected:** `200` · `{"vote":"NO","reasons":[{"reason":"NO_SUCH_ACCOUNT","posting":{<the full offending posting>}}]}` — reason echoes the **entire** posting (not an index).

#### TC-C5-PROTO-005 · Inbound NEW_TX, inactive account → NO `UNACCEPTABLE_ASSET` (NEGATIVE)
- **Feature:** Account can hold asset / active check · **Spec:** protocol-spec §6.6 step 4 / §10; Spec §25 (`UNACCEPTABLE_ASSET` = inactive account, or debit-posting on our routing) · **Existing test:** —
- **Request:** NEW_TX targeting an **inactive** local account (or ordering us to debit on our own routing).
- **Expected:** `200` · `{"vote":"NO","reasons":[{"reason":"UNACCEPTABLE_ASSET","posting":{…}}]}`.

#### TC-C5-PROTO-006 · Inbound NEW_TX, currency mismatch → NO `NO_SUCH_ASSET` (NEGATIVE)
- **Feature:** Asset-exists / currency check · **Spec:** protocol-spec §10; Spec §25 (`NO_SUCH_ASSET` = account currency ≠ posting assetId) · **Existing test:** —
- **Request:** NEW_TX whose `MONAS.currency` ≠ the target account's currency.
- **Expected:** `200` · `{"vote":"NO","reasons":[{"reason":"NO_SUCH_ASSET","posting":{…}}]}`.

#### TC-C5-PROTO-007 · Inbound NEW_TX, insufficient funds on credited account → NO `INSUFFICIENT_ASSET` (NEGATIVE)
- **Feature:** Sufficient-funds-to-reserve check (singular wire form) · **Spec:** protocol-spec §6.6 step 3 / §10 (wire `INSUFFICIENT_ASSET`, not `INSUFFICIENT_ASSETS`) · **Existing test:** — · **Fixture:** `vote_no.json`
- **Request:** NEW_TX whose credited (−amount, money-leaving) leg lacks reservable funds.
- **Expected:** `200` · body **byte-matches** `vote_no.json`: `{"vote":"NO","reasons":[{"reason":"INSUFFICIENT_ASSET","posting":{"account":{"type":"ACCOUNT","num":"111000141215476411"},"amount":260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}}}]}`. Assert the singular `INSUFFICIENT_ASSET` spelling.

#### TC-C5-PROTO-008 · Inbound OTC option NEW_TX reason codes (NEGATIVE)
- **Feature:** Option-leg verification · **Spec:** protocol-spec §6.6 steps 5-6 / §10 (`OPTION_AMOUNT_INCORRECT`, `OPTION_USED_OR_EXPIRED`, `OPTION_NEGOTIATION_NOT_FOUND`) · **Existing test:** —
- **Request:** NEW_TX with (a) an option leg whose money ≠ k·π → `OPTION_AMOUNT_INCORRECT`; (b) an exercise of an already-used/expired option → `OPTION_USED_OR_EXPIRED`; (c) an option leg with an unknown `negotiationId` → `OPTION_NEGOTIATION_NOT_FOUND`.
- **Expected:** `200` · `{"vote":"NO","reasons":[{"reason":<code>,"posting":{…}}]}` per case.

#### TC-C5-PROTO-009 · Receiver-side 202 for slow reserve (POSITIVE)
- **Feature:** Async receive (202) + retransmit collects vote · **Spec:** REST cross-bank-protocol "Receiver-side 202"; protocol-spec §6.1 (202 = accepted, retry later) · **Existing test:** peer_tx_handler_test.go::TestPostInterbank_NewTx_Pending_Returns202
- **Request:** NEW_TX whose local reserve exceeds `SITX_RECEIVE_SYNC_DEADLINE` (default 5 s).
- **Expected:** `202 Accepted` empty body; sender retransmits the **same idempotence key**; once reserve completes the retransmit returns `200` with the vote. COMMIT/ROLLBACK always synchronous (`204`).

#### TC-C5-PROTO-010 · Idempotence-key replay returns the cached vote (POSITIVE)
- **Feature:** At-most-once effect via idempotence key · **Spec:** protocol-spec §6.7; Spec §25 receiver step 1 · **Existing test:** peer_tx_grpc_handler_test.go (interbank-service replay cache)
- **Request:** POST the **same** NEW_TX envelope (same `(routingNumber, locallyGeneratedKey)`) twice.
- **Expected:** both `200`; the second returns the **same** vote body without re-reserving (recorded in the same DB tx that moved assets). Replay safety across network faults.

#### TC-C5-PROTO-011 · PeerAuth failures → 401 empty body (NEGATIVE, auth matrix)
- **Feature:** Authentication (X-Api-Key / HMAC) · **Spec:** protocol-spec §6.8; Spec §25 Authentication + failure semantics · **Existing test:** test-app/peerbank/server_test.go::TestMock_BadSignature_Returns401
- **Actor:** unregistered/forged peer
- **Request:** `POST /interbank` (and any `/cross-bank-protocol/*` route) with: (a) no auth header; (b) wrong `X-Api-Key`; (c) HMAC with bad signature; (d) HMAC with stale `X-Timestamp` (>±5 min); (e) replayed `X-Nonce` (within 10-min window).
- **Expected:** **`401` empty body** in every case (constant-time compare; no info about which header failed, whether the bank is registered, or window state).

#### TC-C5-PROTO-012 · Unknown messageType → 400 (NEGATIVE)
- **Feature:** Envelope validation · **Spec:** protocol-spec §6.2 (`messageType ∈ {NEW_TX,COMMIT_TX,ROLLBACK_TX}`) · **Existing test:** peer_tx_handler_test.go::TestPeerTxHandler_UnknownMessageType_Returns400
- **Request:** `POST /interbank` `{…,"messageType":"PREPARE",…}` (a non-spec type).
- **Expected:** `400` (rejected before dispatch). (A genuine backend `Unimplemented` would surface as `501`, covered by TestPeerTxHandler_NewTx_Unimplemented_Returns501 — not the current build.)

#### TC-C5-PROTO-013 · CHECK_STATUS read endpoint (POSITIVE)
- **Feature:** CHECK_STATUS (stuck-saga resolution) · **Spec:** Celina 5 §Mehanizam za Retry (`{transactionId, action:"CHECK_STATUS"}`); REST `GET /interbank/:txid/status`; frozen-route (GET that reads) · **Existing test:** peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath / TestPeerTxStatusHandler_Unknown
- **Actor:** peer bank
- **Request:** `GET /api/v3/cross-bank-protocol/interbank/<transaction_id>/status` · Auth: PeerAuth
- **Expected:** `200` · `{transaction_id, state:"prepared"|"committed"|"rolled_back"|"dead_letter"|"unknown", our_role:"sender"|"receiver"|"", last_action_at, last_error}`. Unknown tx → `200` with `state:"unknown"` (never 404 here). Missing path param → `400`.
- **Negative siblings:** PeerAuth fail → `401`.

---

## 4. Cross-bank OTC discovery & user resolution (peer-facing)

#### TC-C5-DISC-001 · `GET /public-stock` returns a bare array with standard seller ids (POSITIVE, byte-pinned)
- **Feature:** Dobavljanje OTC ponuda druge banke (public stock) · **Spec:** protocol-spec §8.1; Celina 5 §Dobavljanje OTC ponuda; REST `GET /public-stock`; Spec §27 · **Existing test:** test-app/workflows/sitx_public_stock_seller_id_test.go::TestSITX_PublicStockSellerIdAndOpaqueBuyerId; api-gateway/internal/handler/peer_otc_handler_test.go::TestPeerOTC_GetPublicStocks · **Fixture:** `contract/sitx/testdata/public_stock.json`
- **Actor:** peer bank (PeerAuth)
- **Preconditions:** a local holding flagged public (`public_quantity > 0`, `security_type='stock'`).
- **Request:** `GET /api/v3/cross-bank-protocol/public-stock` · Auth: `X-Api-Key`
- **Expected:** `200` · a **BARE JSON array** (no wrapper), shape per `public_stock.json`: `[{"stock":{"ticker":"AAPL"},"sellers":[{"seller":{"routingNumber":111,"id":"client-3"},"amount":50},…]}]`. **Invariant:** `seller.id` is the **standard opaque** form (`client-<N>` or `bank`), never a bare numeric (a peer must be able to echo it back as `sellerId`). No price/currency field (discovery only).
- **Negative siblings:** PeerAuth fail → `401`; no public holdings → empty array `[]`.

#### TC-C5-DISC-002 · `GET /public-option-offers` lists OPEN sell_initiated option listings (POSITIVE)
- **Feature:** Cross-bank option-offer discovery (Phase 6) · **Spec:** REST cross-bank-protocol route summary; Spec §27 (publish skips `buy_initiated`) · **Existing test:** —
- **Actor:** peer bank
- **Request:** `GET /api/v3/cross-bank-protocol/public-option-offers` · Auth: PeerAuth
- **Expected:** `200` · this bank's OPEN OTC option listings; **only `sell_initiated`** rows are exposed cross-bank (`buy_initiated` skipped — callout #9). Bank-owned offers advertise `sellerId="employee-<ActingEmployeeID>"` (never literal `"bank"`); legacy bank offers with no acting employee are not exposed.

#### TC-C5-DISC-003 · `GET /user/:rid/:id` resolves a counterparty display name (POSITIVE, byte-pinned)
- **Feature:** Resolving friendly names · **Spec:** protocol-spec §9; REST `GET /user/:rid/:id`; Spec §27 · **Existing test:** api-gateway/internal/handler/peer_user_handler_test.go::TestPeerUser_Found · **Fixture:** `contract/sitx/testdata/user.json`
- **Actor:** peer bank
- **Request:** `GET /api/v3/cross-bank-protocol/user/111/client-1` · Auth: PeerAuth
- **Expected:** `200` · shape per `user.json`: `{"bankDisplayName":"EXBanka","displayName":"Marko Marković"}`. `bankDisplayName` from `OWN_BANK_NAME`; `displayName` = first + last name. `client-<n>` → client-service, `employee-<n>` → user-service.
- **Negative siblings:** foreign `rid` ≠ own routing → `404` (we don't proxy cross-bank lookups, TestPeerUser_NotFound_404); unknown user id → `404`; bad `rid` (non-numeric) → `400` (TestPeerUser_BadRid_400).

---

## 5. Cross-bank OTC negotiation (peer-facing frozen routes)

> Frozen routes per protocol-spec §8. `{rid}`/`{id}` = the negotiation's
> `ForeignBankId`. Whose-turn rule: a party may act only when the OTHER side made
> the last modification. `lastModifiedBy.routingNumber` is **derived from the
> authenticated sender** (never trusted from the payload).

#### TC-C5-NEG-001 · `POST /negotiations` opens a cross-bank negotiation (POSITIVE)
- **Feature:** Pregovaranje — inicijalna ponuda (buyer's bank → seller's bank) · **Spec:** protocol-spec §8.2; Celina 5 §Pregovaranje Primer 1; REST `POST /negotiations` · **Existing test:** peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation / TestPeerOTC_CreateNegotiation_NumericAmount / TestPeerOTC_CreateNegotiation_SellerBankForm / TestPeerOTC_CreateNegotiation_ForwardsParentOfferId; sitx_public_stock_seller_id_test.go
- **Actor:** peer bank (the buyer's bank), PeerAuth
- **Preconditions:** our `sellerId` (`client-<N>`/`bank`) is a public seller from `/public-stock`.
- **Request:** `POST /api/v3/cross-bank-protocol/negotiations`
  - Auth: `X-Api-Key`
  - Body: SI-TX `OtcOffer` (the body IS the OtcOffer, no wrapper): `{"stock":{"ticker":"AAPL"},"settlementDate":"2026-12-31T00:00:00Z","pricePerUnit":{"amount":180.50,"currency":"USD"},"premium":{"amount":700,"currency":"USD"},"buyerId":{"routingNumber":222,"id":"550e8400-…"},"sellerId":{"routingNumber":111,"id":"client-1"},"amount":50,"lastModifiedBy":{"routingNumber":222,"id":"550e8400-…"}}`
- **Expected:** `201` · returns a `ForeignBankId` **directly** (not wrapped): `{"routingNumber":111,"id":"<neg-uuid>"}` · side-effect: persisted as a REMOTE row in unified `otc_negotiations`; `lastModifiedBy.routingNumber` stored as the **authenticated peer's** routing (overriding the payload).
- **Negative siblings:** see TC-C5-NEG-002/003.

#### TC-C5-NEG-002 · Opaque buyerId accepted, malformed sellerId / overlong id rejected (NEGATIVE, §2.3 matrix)
- **Feature:** Participant-id validation (ForeignBankId opacity) · **Spec:** protocol-spec §3 (opaque ids; ≤64 bytes) / §5.3; REST `POST /negotiations` participant-id rules; Spec §27 routing assertions · **Existing test:** peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation_OpaqueBuyerId / TestPeerOTC_CreateNegotiation_RejectsOverlongOrEmptyId; sitx_public_stock_seller_id_test.go
- **Request:** vary the offer ids:
  - **buyerId.id** (peer's, opaque): UUID / `acc-42` / any scheme → **accepted `201`** (we MUST NOT interpret another bank's id).
  - **buyerId.id** empty or > 64 bytes → `400 validation_error` (the only §2.3 bounds).
  - **sellerId** (ours): malformed (`employee-abc`, `employee-`, bare `1`) → `400 validation_error`, **no row persisted**; non-existent `client-<N>` → `404 not_found`.
  - **buyer-routing spoof:** `buyerId.routingNumber` ≠ the authenticated peer → reject (security; prevents debiting a third bank's user on accept).
  - **foreign-seller bid:** `sellerId.routingNumber` ≠ our routing → reject (inbound bids must target a local seller).
- **Expected:** as listed per variant.

#### TC-C5-NEG-003 · `PUT /negotiations/:rid/:id` counter-offer — turn & closed guards (NEGATIVE)
- **Feature:** Slanje kontraponude · **Spec:** protocol-spec §8.3 (`409` if not your turn / closed); Celina 5 §Pregovaranje (kontraponuda menja cenu/premium); Spec §27 §3.3 guards · **Existing test:** —
- **Actor:** peer bank
- **Request:** `PUT /api/v3/cross-bank-protocol/negotiations/111/<neg-id>` with an updated `OtcOffer`.
- **Expected:** `200` empty body when it IS the peer's turn (stored `lastModifiedBy.routingNumber == our routing`, i.e. we last proposed). **`409 Conflict`** when (a) the negotiation is closed (cancelled/accepted/rejected/expired) or (b) out of turn (stored routing == the calling peer's own — it already moved last; e.g. a counter immediately after the peer's own POST). No mutation on `409`.

#### TC-C5-NEG-004 · `GET /negotiations/:rid/:id` reads authoritative state (POSITIVE)
- **Feature:** Read negotiation state · **Spec:** protocol-spec §8.4; REST `GET /negotiations/:rid/:id` · **Existing test:** peer_otc_handler_test.go::TestPeerOTC_GetNegotiation / TestPeerOTC_GetNegotiation_BadRid_400
- **Request:** `GET /api/v3/cross-bank-protocol/negotiations/111/<neg-id>` · Auth: PeerAuth
- **Expected:** `200` · SI-TX `OtcNegotiation` = `OtcOffer & {isOngoing:boolean}`; monetary `amount` fields are JSON **numbers**. `isOngoing:true` iff status `ongoing`.
- **Negative siblings:** unknown id → `404`; bad `rid` → `400`.

#### TC-C5-NEG-005 · `DELETE /negotiations/:rid/:id` soft-cancels (POSITIVE)
- **Feature:** Odustajanje od ponude (sets isOngoing=false) · **Spec:** protocol-spec §8.5; Celina 5 §Pregovaranje action 2; Spec §27 (soft-cancel, not physical delete) · **Existing test:** peer_otc_handler_test.go::TestPeerOTC_DeleteNegotiation_Returns204
- **Request:** `DELETE /api/v3/cross-bank-protocol/negotiations/111/<neg-id>` · Auth: PeerAuth
- **Expected:** `204` no body · side-effect: status flips to `cancelled`; a subsequent `GET` returns `200` with `isOngoing:false` (NOT 404 — the row persists).

#### TC-C5-NEG-006 · `GET /negotiations/:rid/:id/accept` forms the contract (POSITIVE, frozen GET-mutates)
- **Feature:** Prihvatanje ponude → 4-posting option-formation TX · **Spec:** protocol-spec §8.6; Celina 5 §Primer 2 (postignut dogovor: buyer pays premium, seller's shares locked); REST `GET …/accept`; frozen-route (GET that mutates) · **Existing test:** peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches
- **Actor:** the **counterparty's** bank (the side that did NOT last propose).
- **Preconditions:** stored `lastModifiedBy.routingNumber == this bank's routing` (we last proposed → it is the peer's turn to accept).
- **Request:** `GET /api/v3/cross-bank-protocol/negotiations/111/<neg-id>/accept` · Auth: PeerAuth
- **Expected:** `200` · `{"transactionId":"<tx-uuid>","status":"pending"}` · side-effect: composes the 4-posting `Transaction` (premium money DEBIT-buyer/CREDIT-seller + 1× `OptionDescription` DEBIT-seller/CREDIT-buyer) and dispatches via `InitiateOutboundTxWithPostings`; on COMMIT the seller's shares are reserved (locked) and the option contract is minted on both sides. Byte-shape of this accept NEW_TX → TC-C5-PROTO-014 / `newtx_otc_accept.json`.
- **Negative siblings:** accept when stored routing ≠ our routing (a peer trying to accept its own/forged proposal) → `403 forbidden`, **no settlement, no contract**; accept against a child of a cancelled/consumed parent listing → `409 business_rule_violation` (orphan-accept guard).

#### TC-C5-PROTO-014 · Accept-shape NEW_TX (OPTION-as-asset, 4 postings) (POSITIVE, byte-pinned)
- **Feature:** Option-formation transaction wire shape · **Spec:** protocol-spec §8.6 (optionContract posting table) + §5.7 OptionDescription; Spec §27 acceptance · **Existing test:** — · **Fixture:** `contract/sitx/testdata/newtx_otc_accept.json`
- **Actor:** coordinator (the accepting party's bank) dispatching to the peer.
- **Expected (wire body):** matches `newtx_otc_accept.json` — 4 postings: (1) buyer ACCOUNT `−1000 MONAS RSD` (premium leaves buyer), (2) buyer `PERSON{222,client-1}` `+1000 MONAS RSD`… and the two OPTION-as-**asset** legs: seller `PERSON` `−1 OPTION{negotiationId{111,neg-1}, stock{WMT}, pricePerUnit{50,RSD}, settlementDate, amount:10}` (seller gives up the contract → reserves stock) + buyer `PERSON{111,client-1}` `+1 OPTION{…}` (buyer receives it). `transactionId{routingNumber,id}`, `paymentCode:""`, `paymentPurpose:""`. **OPTION legs carry participant ids** (become the contract's buyer_id/seller_id); option `amount` is integer > 0.

#### TC-C5-PROTO-015 · Exercise-shape NEW_TX (OPTION-pseudo-account + STOCK legs) (POSITIVE, byte-pinned)
- **Feature:** Option-exercise transaction wire shape · **Spec:** protocol-spec §7 (exercise posting table); Spec §27 exercise wire encoding · **Existing test:** — · **Fixture:** `contract/sitx/testdata/newtx_otc_exercise.json`
- **Expected (wire body):** matches `newtx_otc_exercise.json` — 4 postings: (1) buyer ACCOUNT `−500 MONAS RSD` (pays strike = π·k), (2) `OPTION{id:{111,neg-1}}` pseudo-account `+500 MONAS RSD` (strike to seller via pseudo-account), (3) `OPTION{id:{111,neg-1}}` `−10 STOCK{WMT}` (underlying leaves pseudo-account), (4) buyer `PERSON{111,client-1}` `+10 STOCK{WMT}` (underlying to buyer). The receiver distinguishes **exercise** (OPTION-as-account **with STOCK legs**) from **accept** (OPTION-as-asset) by transaction **shape** — no `intent` flag on the wire. Money amount must equal `strikePrice × quantity` (else `OPTION_AMOUNT_INCORRECT`).

---

## 6. Client-facing cross-bank OTC (unified routes)

> Client cross-bank trading uses the SAME routes as local OTC (callout #7);
> stock-service dispatches remote when the listing's routing ≠ own. The
> end-to-end cross-bank cases below **require two stacks**; the existing Go tests
> are the documented `*_RequiresTwoStacks` skips.

#### TC-C5-OTC-001 · Bid on a remote (peer-hosted) option listing (POSITIVE)
- **Feature:** Kupac pravi ponudu na akcije druge banke · **Spec:** Celina 5 §Pregovaranje; REST §47.2 `POST /otc/options/:id/bid` (remote branch); Spec §27 client-facing initiate · **Existing test:** test-app/workflows/otc_sp3_test.go::TestSP3_BankBidsCrossBank_RequiresTwoStacks (bank-principal variant; client variant analogous)
- **Actor:** (a) client, (b) employee-as-bank
- **Preconditions:** the remote `sell_initiated` listing was discovered (folded-in remote surrogate `:id`); bidder account currency == listing premium currency (no FX).
- **Request:** `POST /api/v3/otc/options/:id/bid`
  - Auth: `Bearer <client|bank-employee>`
  - Body: `{"bidder_account_id":42,"quantity":"50","strike_price":"200","premium":"700","settlement_date":"2026-12-31"}`
- **Expected:** `201` · `{"negotiation":{…,"kind":"remote","status":"ongoing"}}` · side-effects: stock-service composes the SI-TX `OtcOffer` (client → `buyerId="client-<ownerID>"`; bank-employee → `buyerId="employee-<actingEmployeeID>"`, settles a BANK account), POSTs to the seller bank's `/cross-bank-protocol/negotiations`, and persists a buyer-side REMOTE mirror row. The `bidder_account_id` is validated for ownership + active + currency before threading its account number to the seller's bank.
- **Negative siblings:** bidder account currency ≠ premium currency → `400` (no cross-bank FX); `bidder_account_id` not owned (client) / not a BANK account (bank-employee) → `403`; quantity/strike ≤ 0 → `400`; premium < 0 → `400`; second chain by same bidder on same listing → `409`; parent listing not open → `412`.

#### TC-C5-OTC-002 · Counter / accept / cancel a remote chain (POSITIVE, supervisor↔supervisor & client↔client)
- **Feature:** Pregovaranje preko banaka (OTC trade external — supervizori ili klijenti) · **Spec:** Celina 5 §OTC Trgovina ("2 klijenta ili 2 supervizora"); odbrana Provera 2 (OTC external supervisor↔supervisor); REST §47.2 counter/accept; Spec §27 · **Existing test:** otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks; TestSP3_PeerBidsOnOurBankOffer_RequiresTwoStacks
- **Actor:** client↔client across banks; and supervisor (bank-as-principal)↔supervisor
- **Request:** `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter`, `…/accept`, `DELETE …/:nid`.
- **Expected:** counter `200` (status `countered`, forwarded over SI-TX, local mirror updated); accept `200`/`201` with the formed contract (premium debited buyer → credited seller; seller's shares locked); cancel `204`. **Clients see only client offers, supervisors/aktuari see only aktuar offers** (Celina 5 §Dobavljanje: "Klijenti vide ponude Klijenata, Aktuari vide ponude Aktuara").
- **Negative siblings:** accept your own last offer → `403` (must be the opposite party); counter out of turn / closed → `409`.

#### TC-C5-OTC-003 · `GET /me/otc/options/negotiations` merges local + remote chains (POSITIVE)
- **Feature:** Aktivne ponude (own chains, local + cross-bank) · **Spec:** REST §47.2 / §41; Spec §27 ListMyNegotiations convergence · **Existing test:** test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteNegotiation_MergesIntoMyNegotiations / TestSP1_MyNegotiations_HasProvenanceFields
- **Actor:** client (only client principals get remote items)
- **Request:** `GET /api/v3/me/otc/options/negotiations?statuses=ongoing`
- **Expected:** `200` · one merged list; remote rows carry `kind:"remote"`, `routing_number`/`bank_code` = the counterparty peer, `me_owner` true iff we host the seller side; paging applies to the local set, remote chains appended in full.

#### TC-C5-OTC-010 · `GET /me/otc/contracts` merges local + remote contracts (POSITIVE)
- **Feature:** Sklopljeni ugovori (local + cross-bank) · **Spec:** REST §30 `GET /me/otc/contracts`; Spec §27 contracts list · **Existing test:** test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteContract_AppearsWithKindRemote / TestSP1_MyContracts_BuyerIsOwner
- **Actor:** client
- **Expected:** `200` · `contracts[]` merges LOCAL + REMOTE rows; each carries `kind`/`routing_number`/`bank_code`/`me_owner`. `me_owner=true` **only** for the contract's buyer/holder (for remote: `direction=="CREDIT"`). Legacy `peer_contracts`/`peer_total` fields are gone.

#### TC-C5-OTC-040 · Cross-bank `buy_initiated` bid is rejected (NEGATIVE)
- **Feature:** buy_initiated cross-bank out of scope (seller-centric protocol) · **Spec:** Spec §27 "Seller-centric discovery limitation (2.9.1)" · **Existing test:** — (callout #9)
- **Request:** bid on a remote `buy_initiated` listing.
- **Expected:** `409 business_rule_violation` (fail-closed; such offers are also dropped at the discovery-poll boundary so they never become biddable). LOCAL `buy_initiated` is unaffected.

---

## 7. Cross-bank OTC exercise SAGA (Bank A buyer ↔ Bank B seller)

> Spec SAGA phases (Celina 5 §Izvršavanje kupoprodaje): (1) RESERVE_FUNDS (buyer's
> Bank A) → (2) RESERVE_SHARES_CONFIRM | RESERVE_SHARES_FAIL (seller's Bank B) →
> (3) COMMIT_FUNDS (A→B) → (4) TRANSFER_OWNERSHIP (B→A) + OWNERSHIP_CONFIRM (A→B)
> → (5) FINAL_CONFIRM (double-check). On the implemented wire this is a single
> 4-posting exercise `Transaction` run through NEW_TX→COMMIT_TX with per-phase
> compensation + CHECK_STATUS retry. The Celina spec's discrete message names map
> onto the wire as: RESERVE_* = `NEW_TX` reserve/vote; COMMIT_FUNDS/TRANSFER_OWNERSHIP/
> FINAL_CONFIRM = `COMMIT_TX` materialisation; rollback = `ROLLBACK_TX`.

#### TC-C5-SAGA-001 · Exercise an in-the-money cross-bank option — happy path (POSITIVE)
- **Feature:** Iskorišćavanje opcionog ugovora (cross-bank) · **Spec:** Celina 5 §Izvršavanje + Primer 1 (profit positive → iskoristi); REST §30 `POST /otc/contracts/:id/exercise` (remote branch); Spec §27 exercise lifecycle · **Existing test:** otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks; (local-saga analogue) test-app/workflows/saga_sg_test.go::TestSG01_HappyPath
- **Actor:** the **buyer/holder** (this bank's row is `direction=CREDIT`)
- **Preconditions:** active cross-bank contract; settlement date not passed; market > strike (in-the-money); buyer's strike account in the strike currency.
- **Request:** `POST /api/v3/otc/contracts/:id/exercise`
  - Auth: `Bearer <buyer client | bank-employee>` + (`otc.trade.accept`|`securities.trade`)
  - Body: `{"buyer_account_number":"111…"}`
- **Verification:** full-flow for client self-serve unless `verification.skip`.
- **Expected:** `201` · cross-bank tx id rides in `saga_id`; `status:"pending"`. After COMMIT, **side-effects on both banks:** buyer ACCOUNT debited `strikePrice × quantity` (RESERVE_FUNDS→COMMIT_FUNDS); seller's reserved shares consumed + seller's holding decremented (RESERVE_SHARES_CONFIRM→TRANSFER_OWNERSHIP); **buyer receives the holding** (OWNERSHIP_CONFIRM); contract → `exercised` both sides; **reservations cleaned on BOTH sides** (FINAL_CONFIRM); option can no longer be exercised. Poll via `GET /api/v3/me/otc/transactions/:txid/status` → `committed`.
- **Negative siblings:** non-buyer / non-party → `404` (existence not leaked, TC-C5-ADV-010); missing `buyer_account_number` on a remote contract → `400`; strike account not entitled → `403`; contract expired/not active → `409`; insufficient buyer funds → `409`.

#### TC-C5-SAGA-002 · Decline (don't exercise) out-of-the-money — lose only premium (POSITIVE)
- **Feature:** Ne iskorišćavanje opcionog ugovora · **Spec:** Celina 5 §Primer 2 (profit negativan → ne iskoristi; gubitak = premija) · **Existing test:** —
- **Actor:** buyer
- **Preconditions:** market < strike; contract active.
- **Request:** the buyer simply does **not** call exercise; lets it lapse (or the expiry cron runs at settlement).
- **Expected:** no exercise tx; at/after settlement the option becomes unusable; buyer's only loss is the already-paid premium; seller keeps the premium + their shares are **unlocked** at expiry (TC-C5-SAGA-020). No money moves on the strike.

#### TC-C5-SAGA-010 · RESERVE_SHARES_FAIL → release buyer funds (compensation, phase 2) (NEGATIVE)
- **Feature:** Banka B ne može da rezerviše hartije → Banka A oslobađa sredstva · **Spec:** Celina 5 §Izvršavanje step 2.4 / Scenario Neuspeha; Spec §27 (CheckSellerCanDeliver → INSUFFICIENT_ASSET NoVote, money never moves) · **Existing test:** — (local-saga analogue: saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds)
- **Actor:** buyer; seller no longer holds enough free shares.
- **Expected:** at NEW_TX the seller's bank votes `{"vote":"NO","reasons":[{"reason":"INSUFFICIENT_ASSET",…}]}` (driven by `CheckSellerCanDeliver`); Bank A **releases the buyer's fund reservation** — strike never debited; contract stays `active` (claim reverts so it's retryable); a clean retry once shares are available succeeds (proves full rollback).

#### TC-C5-SAGA-011 · Ownership-transfer failure → refund + return shares + mark "Poništena" (NEGATIVE)
- **Feature:** Banka A ne potvrdi vlasništvo → Banka B vraća hartije, A refundira · **Spec:** Celina 5 §Izvršavanje step 4.3 (Rollback) · **Existing test:** saga_sg_test.go::TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds (local-saga full-compensation analogue)
- **Actor:** buyer; force-fail after the share transfer leg.
- **Expected:** the saga walks back all phases: shares return to the seller, the buyer's credit is reversed, strike refunded, reservations released, contract left **ACTIVE** (not EXERCISED) — proven by a subsequent clean retry succeeding. No partial state.

#### TC-C5-SAGA-012 · Fund-refund retry & dead-letter escalation (NEGATIVE)
- **Feature:** Retry ≤ N then escalate · **Spec:** Celina 5 §Mehanizam za Retry; Spec §21 "No saga can leave the system stuck" (retries → `transaction.saga-dead-letter` / `stock.saga-dead-letter`) · **Existing test:** —
- **Expected:** a repeatedly-failing compensation is retried by the recovery worker (up to 10×) then escalated to the service-scoped dead-letter Kafka topic; the row never silently strands money. (Implementation uses dead-letter escalation rather than the prose's "alert admin after 3".)

#### TC-C5-SAGA-013 · CHECK_STATUS resume after mid-flight comms break (POSITIVE)
- **Feature:** Retry preko transactionId — nastavljaju gde su stale · **Spec:** Celina 5 §Mehanizam za Retry (`CHECK_STATUS`); Spec §21 (PeerTxReconciler polls every 10 min); REST `GET /interbank/:txid/status` · **Existing test:** peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; (mock) test-app/peerbank/server_test.go::TestMock_StatusKnown_ReturnsState / TestMock_StatusUnknown_Returns404
- **Expected:** when communication breaks after one side committed, the other side's `PeerTxReconciler` (sender) / replay path queries `GET …/interbank/:txid/status`; a peer that committed locally reports `state:"committed"` so the asker closes its row without re-sending. Idempotence keys make any resend a safe no-op.

#### TC-C5-SAGA-014 · Concurrent double-exercise / double-accept prevented (NEGATIVE, adversarial)
- **Feature:** Double-reserve / double-charge prevention · **Spec:** Spec §27 "Concurrency & ownership guards (2026-05-30)" (CAS `active→exercising`, `ongoing→accepted` before dispatch) · **Existing test:** — (adversarial finding; memory: project_crossbank_adversarial_findings)
- **Request:** fire two concurrent `POST /otc/contracts/:id/exercise` (and two concurrent accepts on one negotiation).
- **Expected:** exactly **one** wins the compare-and-set; the second loses → `409 conflict`. Without the CAS each call would charge the buyer (strike/premium) and mint/reserve twice; the money legs are not row-locked-idempotent, so the CAS is the guard. On a synchronous dispatch failure the claim reverts (stays retryable).

#### TC-C5-SAGA-020 · Expired unused cross-bank option unlocks seller shares (POSITIVE)
- **Feature:** Istekla neiskorišćena opcija → oslobađanje hartija · **Spec:** protocol-spec §7 ("settlement passes → un-reserve, mark used once"); Spec §27 Expiry cron · **Existing test:** —
- **Expected:** `OTCExpiryCron` (daily 02:00 UTC + startup catch-up): for a remote `active` contract past `settlement_date`, the seller's bank (DEBIT side) releases the share reservation (shares unlock); both sides → `expired`; seller keeps the premium (no money movement). Buyer's bank (CREDIT side) does no holding op.

---

## 8. Cross-bank OTC option / premium tax

#### TC-C5-TAX-001 · Seller taxed 15% on premium at accept (POSITIVE)
- **Feature:** Prodavac prima premiju (OTC) → porez 15% × premija · **Spec:** Celina 5 §Obračun poreza "Prodavac prima premiju"; Spec §21 (cross-bank seller still taxed) · **Existing test:** (local analogue) test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Expected:** when a cross-bank option contract forms, the seller's bank records the premium income; tax = 15% × premium (e.g. premium $1150 → $172.50). Surfaced in the seller's tax-tracking portal.

#### TC-C5-TAX-002 · Seller taxed on strike gain at cross-bank exercise (POSITIVE)
- **Feature:** Cross-bank seller strike-gain tax · **Spec:** Spec §21 (recordOptionExercise DEBIT branch writes realised CapitalGain; sell price = StrikePrice, basis snapshotted under lock) · **Existing test:** —
- **Expected:** on a cross-bank exercise, the seller's bank writes a realised capital-gain (strike − cost basis) × qty for the delivered shares; idempotent on replay (a replayed COMMIT skips the non-idempotent gain write).

#### TC-C5-TAX-010 · Aktuar/bank exemption — profit to Bank Profit (POSITIVE)
- **Feature:** Izuzetak — aktuari ne plaćaju 15% · **Spec:** Celina 5 §Izuzetak (profit od opcija/premija ide u Profit Banke) · **Existing test:** —
- **Expected:** when the seller/buyer is the bank (aktuar trading on behalf of the bank), no 15% tax; option/premium profit accrues to the bank's profit portal (same as dividends).

#### TC-C5-TAX-030 · Buyer cross-bank exercise tax (NEGATIVE / NO-ENDPOINT)
- **Feature:** Kupac iskorišćava opciju (OTC) → 15% × ((tržišna − strike) × qty − premija) · **Spec:** Celina 5 §Obračun poreza "Kupac iskorišćava"; Spec §21 deferral · **Existing test:** — (callout #8)
- **Expected:** **NO-ENDPOINT** for the cross-bank buyer — the frozen exercise wire carries neither premium nor market price, so the buyer's bank cannot compute the formula at exercise time. The buyer is instead taxed via the eventual stock sale (shares credited at strike basis). (Intra-bank buyer-exercise tax IS covered in `celina-3-securities.md` / `celina-4-otc-and-funds.md`.)

#### TC-C5-TAX-031 · Expired option — buyer premium loss reduces month's gains, no extra seller tax (POSITIVE)
- **Feature:** Opcija istekne neiskorišćena (OTC) · **Spec:** Celina 5 §Obračun poreza "Opcija istekne neiskorišćena" · **Existing test:** —
- **Expected:** on expiry, the buyer's lost premium reduces that month's capital-gain total; the seller pays no additional tax (premium already taxed at accept). (Cross-bank buyer-side this is part of the deferred buyer computation — see callout #8 — so it is `partial` for the cross-bank path.)

---

## 9. Adversarial / security

#### TC-C5-ADV-001 · Cannot debit an account you don't own via cross-bank payment (NEGATIVE)
- **Feature:** Sender-account ownership enforced gateway-side · **Spec:** Spec §27 "Sender/strike account ownership is enforced gateway-side"; Resource Ownership Requirement · **Existing test:** api-gateway/internal/handler/peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_CreatePayment_ForeignFromNotOwned
- **Actor:** client A trying to send from client B's account.
- **Request:** `POST /api/v3/me/payments` `{from:"<client-B's 111…>",to:"222…",amount:…}`
- **Expected:** `403 forbidden` before any dispatch — authentication proves *who*, not that the source account is theirs.

#### TC-C5-ADV-002 · Cannot pay a cross-bank exercise strike from an account you don't own (NEGATIVE)
- **Feature:** Exercise strike-account gate (all principals) · **Spec:** Spec §27 "SP-3 Task 5 security fix" (bank-acting employee must bind a BANK account; client must own it; on-behalf must bind the client's account) · **Existing test:** —
- **Actor:** (a) client binding another client's account; (b) bank-employee binding a client's account (the old hole).
- **Request:** `POST /api/v3/otc/contracts/:id/exercise` `{"buyer_account_number":"<not-entitled>"}`
- **Expected:** `403 forbidden` gateway-side (`ResolveAndCheckAccountByNumber`), re-asserted in stock-service before dispatch. A bank exercise pays its strike only from a BANK account.

#### TC-C5-ADV-010 · Non-buyer cannot exercise a remote contract (existence not leaked) (NEGATIVE)
- **Feature:** Buyer-only exercise; existence privacy · **Spec:** Spec §27 (writer/seller + non-parties get 404) · **Existing test:** (local analogue) saga_sg_test.go::TestSG02a_NonBuyerRejected / TestSG02b_UnknownContract
- **Request:** the seller-side party (or a stranger) calls `POST /otc/contracts/:id/exercise`.
- **Expected:** `404 not_found` (not 403) — a remote contract's existence must not leak to non-buyers; an unknown id is also `404`/`403`.

#### TC-C5-ADV-011 · Receiver validates an OTC exercise's MONEY legs against its stored contract (forged-money defense) (NEGATIVE)
- **Feature:** Forged-low strike / buyer-overcharge / replay defense · **Spec:** Spec §27 "The receiver validates an OTC exercise's MONEY legs … (round 3)" (`ValidatePeerOptionMoneyLeg`) · **Existing test:** — (memory: project_crossbank_adversarial_findings)
- **Actor:** a buggy/malicious peer (PeerAuth'd with a shared key) posting arbitrary amounts.
- **Request:** NEW_TX exercise with (a) forged-low strike, (b) forged-high strike DEBIT to the buyer's bank, (c) a second exercise of an already-`exercised` contract.
- **Expected:** the receiver loads its **stored** contract and requires status ∈ {active, exercising}, matching ticker/quantity/strike, and `money_amount == StrikePrice × Quantity`; any mismatch → `{"vote":"NO","reasons":[{"reason":"UNACCEPTABLE_ASSET",…}]}`, **no hold placed**. Closes all three thefts (forged-low, overcharge, replay). Receiver-side only — no wire change.

#### TC-C5-ADV-012 · Buyer-routing spoof on inbound bid rejected (NEGATIVE)
- **Feature:** Inbound negotiation routing assertions (Fix #7/#8/#9) · **Spec:** Spec §27 cross-bank routing assertions · **Existing test:** peer_otc_handler_test.go (participant-id validation)
- **Request:** peer A POSTs `/negotiations` claiming `buyerId.routingNumber = 333` (a third bank).
- **Expected:** rejected (buyer routing must equal the authenticated peer) — prevents the accept from debiting a third bank's user; a foreign-seller bid (`sellerId.routingNumber` ≠ ours) is likewise rejected.

---

## 10. Defense-flow & E2E scenarios (chained)

#### TC-C5-E2E-001 · Provera 2 — OTC trade external (supervisor↔supervisor across banks) (POSITIVE)
- **Feature:** odbrana "Provera 2 — OTC trade external" · **Spec:** odbrana flow "4 — Provera 2"; Celina 5 §OTC Trgovina · **Existing test:** otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks
- **Chain:** discover peer seller (`/public-stock`) → bid (TC-C5-OTC-001) → counter-offer & accept across banks (TC-C5-OTC-002) → form contract (premium debited buyer, seller's shares locked) → buyer exercises (TC-C5-SAGA-001).
- **Expected (assert all side-effects):** buyer's money debited (premium + strike); buyer credited the shares; seller no longer has those shares; reservations cleaned both sides; contracts/negotiations terminal on both banks. Works for supervisor↔supervisor (bank principals) and client↔client.

#### TC-C5-E2E-002 · Inter-bank transfer happy path + audit trail (POSITIVE)
- **Feature:** E2E "Uspešan prenos sredstava između banaka" + "Evidentirati kompletan audit trag" · **Spec:** E2E "Feature: Međubankarski prenosi" · **Existing test:** cohort_dry_run_test.go::TestCohortDryRun; sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped
- **Chain:** TC-C5-PAY-001 (10000 RSD A→B) → TC-C5-PAY-030 (audit/status).
- **Expected:** receiver `222…` += 10000; sender debited; both status endpoints report `committed` with sender/receiver routing + timestamps.

#### TC-C5-E2E-010 · E2E "Otkazivanje uplate ako je banka primalac neodgovarajuća" (NEGATIVE)
- **Feature:** E2E cancel-on-bad-receiver / no-response · **Spec:** E2E "Otkazivanje uplate…" + "Banka B ne odgovara 10 sekundi" · **Existing test:** test-app/peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason / TestMock_ConfigureFiveXX_PrepareReturns503
- **Chain:** TC-C5-PAY-010 (NotReady inactive receiver) and TC-C5-PAY-021 (no-response timeout).
- **Expected:** uplata otkazana; sredstva refundirana pošiljaocu (held funds released, no debit); sender notified.

#### TC-C5-E2E-011 · E2E "Odbijanje prenosa kada pošiljalac nema dovoljno sredstava" (NEGATIVE)
- **Feature:** E2E insufficient-funds reject · **Spec:** E2E "Odbijanje prenosa…" · **Existing test:** —
- **Chain:** TC-C5-PAY-020 (100 RSD account, send 200 RSD).
- **Expected:** rejected with insufficient-funds (functional-equivalent of "Nedovoljno sredstava"); balance unchanged; no peer traffic.

#### TC-C5-E2E-030 · Provera 3 — plaćanje između banaka, RAZLIČITA valuta + fee to Bank B (NEGATIVE / NO-ENDPOINT)
- **Feature:** odbrana "Provera 3 — plaćanje između banaka, različita valuta" + "provizija na račun banke B" · **Spec:** odbrana flow "Provera 3"; Celina 5 §Plaćanja · **Existing test:** — (callouts #3 + #4)
- **Expected:** **NO-ENDPOINT** for the *different-currency* + *receiver-bank-commission* combination — the implemented cross-bank payment is single-currency (no execution-time FX) and the fee is sender-side. The same-currency cross-bank payment IS supported (TC-C5-PAY-001). Surfaced as a coverage gap, not silently skipped.

---

## Field-validation matrices

### Peer-bank registration (`POST /api/v3/peer-banks`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `bank_code` | `"222"` (≠ own `"111"`) | missing → 400; equals own `"111"` → 400 (collision) |
| `routing_number` | `222` | missing → 400; equals own `111` → 400 (collision) |
| `base_url` | `"http://host.docker.internal:8081/api/v3/cross-bank-protocol"` | missing → 400 |
| `api_token` | `"shared-111-222"` | missing → 400 (never returned in full afterward) |
| `hmac_inbound_key` / `hmac_outbound_key` | (optional) | n/a (omit ⇒ X-Api-Key mode) |
| `active` | `true` | n/a (bool) |

### SI-TX `OtcOffer` (inbound `POST /negotiations`) — participant ids (§2.3)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `buyerId.id` (peer's, opaque) | `"550e8400-…"` / `"acc-42"` | empty → 400; > 64 bytes → 400 (only the §2.3 bounds; scheme NOT checked) |
| `buyerId.routingNumber` | = authenticated peer | ≠ authenticated peer → reject (spoof) |
| `sellerId.id` (ours) | `"client-7"` / `"bank"` | `"employee-abc"`, `"employee-"`, bare `"1"` → 400 (no row); non-existent `client-<N>` → 404 |
| `sellerId.routingNumber` | = our routing | ≠ our routing → reject (foreign-seller bid) |
| `pricePerUnit.amount` / `premium.amount` | JSON number `180.50` | n/a (quoted string tolerated inbound; never emitted) |
| `amount` (stocks k) | integer `50` | ≤ 0 / non-integer → invalid (k must be integer > 0) |
| `lastModifiedBy.routingNumber` | (derived) | claimed value **overridden** to the authenticated sender's routing |

### SI-TX `Message<NEW_TX>` envelope (inbound `/interbank`)

| Field | Required | Invalid form → vote/HTTP |
|---|---|---|
| `idempotenceKey.routingNumber` | yes | replay of same key → cached vote (200) |
| `idempotenceKey.locallyGeneratedKey` | yes (≤ 64 bytes) | reuse → replay; per-message unique on emit |
| `messageType` | yes ∈ {NEW_TX,COMMIT_TX,ROLLBACK_TX} | other → 400 |
| `message.postings` | yes, balanced | unbalanced → NO `UNBALANCED_TX` |
| `posting.amount` | yes, JSON number, signed | quoted-string on emit → conformance fail |
| `posting.account.type` | yes ∈ {PERSON,ACCOUNT,OPTION} | unknown account → NO `NO_SUCH_ACCOUNT` |
| `posting.asset.type` | yes ∈ {MONAS,STOCK,OPTION} | currency mismatch → NO `NO_SUCH_ASSET`; inactive acct → NO `UNACCEPTABLE_ASSET`; short funds → NO `INSUFFICIENT_ASSET` |
| `message.transactionId{routingNumber,id}` | yes | COMMIT/ROLLBACK correlate by this (not the envelope idem key) |
| `message.paymentCode` / `paymentPurpose` | yes (may be `""`) | absent key → conformance fail |
| `message.callNumber` | **optional** (only optional field) | n/a |

---

## Protocol-conformance matrix (message → required shape → fixture)

| Message / object | Required shape (frozen) | Testdata fixture | Asserting test |
|---|---|---|---|
| `Message<NEW_TX>` transfer | `{idempotenceKey{routingNumber,locallyGeneratedKey}, messageType:"NEW_TX", message:Transaction}`; balanced signed-number postings; tagged-union `account`/`asset` | `contract/sitx/testdata/newtx_coffee.json` | sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped; peer_tx_handler_test.go::TestPostInterbank_NewTx_SpecShape |
| `Message<NEW_TX>` OTC accept | 4 postings: premium MONAS (buyer −/seller +) + `OptionDescription` OPTION-as-asset (seller −1 / buyer +1); OPTION legs carry participant ids; option `amount` int > 0 | `contract/sitx/testdata/newtx_otc_accept.json` | peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches |
| `Message<NEW_TX>` OTC exercise | 4 postings: buyer ACCOUNT −π·k MONAS / OPTION pseudo-acct +π·k MONAS / OPTION pseudo-acct −k STOCK / buyer PERSON +k STOCK; shape (OPTION-acct + STOCK legs) = exercise | `contract/sitx/testdata/newtx_otc_exercise.json` | — (two-stack: otc_sp3_test.go skips) |
| `Message<COMMIT_TX>` / `ROLLBACK_TX` | `{idempotenceKey(unique), messageType, message:{transactionId{routingNumber,id}}}`; correlate by `transactionId`; resp `204` | (documented) | sitx_conformance_test.go (COMMIT correlation + distinct idem key) |
| `TransactionVote` YES | `{"vote":"YES"}` (no extra fields); HTTP `200` | (inline) | peer_tx_handler_test.go::TestPeerTxHandler_NewTx_YesPassthrough; cohort_dry_run_test.go |
| `TransactionVote` NO | `{"vote":"NO","reasons":[{"reason":<code>,"posting":<full posting>}]}`; `UNBALANCED_TX` carries NO posting; `INSUFFICIENT_ASSET` (singular) | `contract/sitx/testdata/vote_no.json` | peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting |
| `PublicStocksResponse` | **bare array** `[{stock{ticker}, sellers[{seller{routingNumber,id}, amount}]}]`; `seller.id` standard opaque (`client-<N>`/`bank`), never bare numeric | `contract/sitx/testdata/public_stock.json` | sitx_public_stock_seller_id_test.go::TestSITX_PublicStockSellerIdAndOpaqueBuyerId; peer_otc_handler_test.go::TestPeerOTC_GetPublicStocks |
| `UserInformation` | `{bankDisplayName, displayName}` (display-name, not raw ids) | `contract/sitx/testdata/user.json` | peer_user_handler_test.go::TestPeerUser_Found |
| `OtcOffer` / `OtcNegotiation` | all-mandatory `OtcOffer` (+ `isOngoing`); monetary `amount` = JSON number | (REST §"POST/GET /negotiations" example) | peer_otc_handler_test.go::TestPeerOTC_GetNegotiation; sitx_public_stock_seller_id_test.go |
| `ForeignBankId` | `{routingNumber, id}`; `id` opaque ≤ 64 bytes | (returned by POST /negotiations) | peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation |
| CHECK_STATUS read | `{transaction_id, state, our_role, last_action_at, last_error}`; HTTP `200` even for unknown (`state:"unknown"`) | (documented) | peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath / _Unknown |

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| two-stack bring-up (gen-bank-stacks, peer registration, distinct OWN_BANK_CODE) | (precondition) TC-C5-SETUP-001 | test-app/workflows/cohort_dry_run_test.go::TestCohortDryRun | covered |
| peer-bank registry: create | TC-C5-SETUP-001 | api-gateway/internal/handler/peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList | covered |
| peer-bank registry: list/read | TC-C5-SETUP-002 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList/_GetUpdateDelete | covered |
| peer-bank registry: update/rotate | TC-C5-SETUP-003 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete | covered |
| peer-bank registry: delete | TC-C5-SETUP-004 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete | covered |
| peer-collision guard (own code/routing) | TC-C5-SETUP-005 | — | covered |
| peer registry RBAC (admin-only) | TC-C5-SETUP-006 | peer_bank_admin_handler_test.go | covered |
| inter-bank payment success (same currency) | TC-C5-PAY-001 | cohort_dry_run_test.go::TestCohortDryRun; sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| bank identified by first 3 digits | TC-C5-PAY-002 | sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| Not-Ready (inactive/nonexistent recipient) → release+notify | TC-C5-PAY-010 | test-app/peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason; peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting | covered |
| sender insufficient funds → reject "Nedovoljno sredstava" | TC-C5-PAY-020 | — | covered |
| Bank B no-response → cancel+refund | TC-C5-PAY-021 | peerbank/server_test.go::TestMock_ConfigureFiveXX_PrepareReturns503 | covered |
| any-step failure → full rollback + reservation release | TC-C5-PAY-022 | interbank-service/internal/handler/reverse_outbound_local_test.go; inline_rollback_test.go | covered |
| audit-trail fields (sender/receiver bank, send/receive time, status) | TC-C5-PAY-030 | api-gateway/internal/handler/peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus | partial |
| client-facing cross-bank status polling | TC-C5-PAY-031 | peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus/_OTCCrossBankStatus | covered |
| prose Ready{endValue,FX,commission} message | TC-C5-PAY-040 | — | NO-ENDPOINT |
| receiver-side currency conversion (cross-currency credit) | TC-C5-PAY-041 | — | NO-ENDPOINT |
| commission credited to receiving bank (Bank B) | TC-C5-PAY-042 | — | NO-ENDPOINT |
| literal 10-second no-response SLA | TC-C5-PAY-043 | — | NO-ENDPOINT |
| inbound NEW_TX transfer → YES vote (byte-pinned) | TC-C5-PROTO-001 | peer_tx_handler_test.go::TestPostInterbank_NewTx_SpecShape/TestPeerTxHandler_NewTx_YesPassthrough | covered |
| COMMIT_TX/ROLLBACK_TX correlation + 204 | TC-C5-PROTO-002 | sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| NO UNBALANCED_TX (no posting) | TC-C5-PROTO-003 | peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting | covered |
| NO NO_SUCH_ACCOUNT | TC-C5-PROTO-004 | — | covered |
| NO UNACCEPTABLE_ASSET (inactive/own-routing debit) | TC-C5-PROTO-005 | — | covered |
| NO NO_SUCH_ASSET (currency mismatch) | TC-C5-PROTO-006 | — | covered |
| NO INSUFFICIENT_ASSET (singular, byte-pinned) | TC-C5-PROTO-007 | — | covered |
| NO option codes (AMOUNT_INCORRECT/USED_OR_EXPIRED/NEGOTIATION_NOT_FOUND) | TC-C5-PROTO-008 | — | covered |
| receiver-side 202 async + retransmit | TC-C5-PROTO-009 | peer_tx_handler_test.go::TestPostInterbank_NewTx_Pending_Returns202 | covered |
| idempotence-key replay returns cached vote | TC-C5-PROTO-010 | interbank-service/internal/handler/peer_tx_grpc_handler_test.go | covered |
| PeerAuth failures → 401 empty body | TC-C5-PROTO-011 | peerbank/server_test.go::TestMock_BadSignature_Returns401 | covered |
| unknown messageType → 400 (501 only if backend Unimplemented) | TC-C5-PROTO-012 | peer_tx_handler_test.go::TestPeerTxHandler_UnknownMessageType_Returns400/TestPeerTxHandler_NewTx_Unimplemented_Returns501 | covered |
| CHECK_STATUS read endpoint (frozen GET) | TC-C5-PROTO-013 | peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath/_Unknown | covered |
| accept-shape NEW_TX (OPTION-as-asset, byte-pinned) | TC-C5-PROTO-014 | peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches | covered |
| exercise-shape NEW_TX (OPTION-pseudo-acct + STOCK, byte-pinned) | TC-C5-PROTO-015 | — (two-stack: otc_sp3_test.go skips) | partial |
| OTC discovery: /public-stock bare array + opaque seller id | TC-C5-DISC-001 | sitx_public_stock_seller_id_test.go::TestSITX_PublicStockSellerIdAndOpaqueBuyerId; peer_otc_handler_test.go::TestPeerOTC_GetPublicStocks | covered |
| OTC discovery: /public-option-offers (sell_initiated only) | TC-C5-DISC-002 | — | covered |
| friendly-name resolution: /user display name | TC-C5-DISC-003 | peer_user_handler_test.go::TestPeerUser_Found/_NotFound_404/_BadRid_400 | covered |
| negotiation: POST /negotiations (create) | TC-C5-NEG-001 | peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation/_NumericAmount/_SellerBankForm/_ForwardsParentOfferId | covered |
| negotiation: participant-id §2.3 validation matrix | TC-C5-NEG-002 | peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation_OpaqueBuyerId/_RejectsOverlongOrEmptyId; sitx_public_stock_seller_id_test.go | covered |
| negotiation: PUT counter — turn/closed 409 guards | TC-C5-NEG-003 | — | covered |
| negotiation: GET state (OtcNegotiation) | TC-C5-NEG-004 | peer_otc_handler_test.go::TestPeerOTC_GetNegotiation/_GetNegotiation_BadRid_400 | covered |
| negotiation: DELETE soft-cancel (isOngoing=false) | TC-C5-NEG-005 | peer_otc_handler_test.go::TestPeerOTC_DeleteNegotiation_Returns204 | covered |
| negotiation: GET …/accept (frozen GET-mutates) → contract | TC-C5-NEG-006 | peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches | covered |
| client cross-bank bid (client + bank principal) | TC-C5-OTC-001 | otc_sp3_test.go::TestSP3_BankBidsCrossBank_RequiresTwoStacks | partial |
| client cross-bank counter/accept/cancel (client↔client & supervisor↔supervisor) | TC-C5-OTC-002 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks/TestSP3_PeerBidsOnOurBankOffer_RequiresTwoStacks | partial |
| own chains merged (local+remote) | TC-C5-OTC-003 | test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteNegotiation_MergesIntoMyNegotiations | covered |
| contracts merged (local+remote) | TC-C5-OTC-010 | test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteContract_AppearsWithKindRemote | covered |
| cross-bank buy_initiated bid rejected | TC-C5-OTC-040 | — | covered |
| cross-bank exercise happy path (buyer paid+receives, seller delivers, reservations cleaned) | TC-C5-SAGA-001 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks; saga_sg_test.go::TestSG01_HappyPath (local analogue) | partial |
| decline OTM option (lose only premium) | TC-C5-SAGA-002 | — | covered |
| RESERVE_SHARES_FAIL → release buyer funds | TC-C5-SAGA-010 | saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds (local analogue) | partial |
| ownership-transfer failure → refund+return shares+ACTIVE | TC-C5-SAGA-011 | saga_sg_test.go::TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds (local analogue) | partial |
| compensation retry → dead-letter escalation | TC-C5-SAGA-012 | — | partial |
| CHECK_STATUS resume after comms break | TC-C5-SAGA-013 | peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; peerbank/server_test.go::TestMock_StatusKnown_ReturnsState/_StatusUnknown_Returns404 | covered |
| concurrent double-exercise/double-accept prevented (CAS) | TC-C5-SAGA-014 | — | partial |
| expired unused option unlocks seller shares | TC-C5-SAGA-020 | — | covered |
| cross-bank seller premium tax (15% at accept) | TC-C5-TAX-001 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle (local analogue) | partial |
| cross-bank seller strike-gain tax at exercise | TC-C5-TAX-002 | — | partial |
| aktuar/bank exemption → bank profit | TC-C5-TAX-010 | — | partial |
| cross-bank buyer exercise tax | TC-C5-TAX-030 | — | NO-ENDPOINT |
| expired option premium loss / no extra seller tax | TC-C5-TAX-031 | — | partial |
| adversarial: cannot debit unowned account (cross-bank payment) | TC-C5-ADV-001 | peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_CreatePayment_ForeignFromNotOwned | covered |
| adversarial: exercise strike-account gate (all principals) | TC-C5-ADV-002 | — | covered |
| adversarial: non-buyer cannot exercise (existence privacy) | TC-C5-ADV-010 | saga_sg_test.go::TestSG02a_NonBuyerRejected/TestSG02b_UnknownContract (local analogue) | partial |
| adversarial: forged-money exercise legs rejected | TC-C5-ADV-011 | — | covered |
| adversarial: buyer-routing spoof on inbound bid rejected | TC-C5-ADV-012 | peer_otc_handler_test.go (participant-id validation) | covered |
| defense Provera 2 — OTC trade external (chain) | TC-C5-E2E-001 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks | partial |
| E2E — inter-bank transfer success + audit (chain) | TC-C5-E2E-002 | cohort_dry_run_test.go::TestCohortDryRun; sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| E2E — cancel on bad/non-responsive receiver (chain) | TC-C5-E2E-010 | peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason/_ConfigureFiveXX_PrepareReturns503 | covered |
| E2E — reject insufficient funds (chain) | TC-C5-E2E-011 | — | covered |
| defense Provera 3 — cross-bank payment DIFFERENT currency + Bank B fee | TC-C5-E2E-030 | — | NO-ENDPOINT |
```
