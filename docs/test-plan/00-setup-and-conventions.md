# 00 — Setup & Conventions

> The single shared preamble for the whole test plan. Every Celina file
> (`celina-1`…`celina-5`, `cross-cutting-verification`) assumes you have read this
> file and have a live, seeded local stack. All values here are copied from the
> repo's real sources (`Makefile`, `docker-compose.yml`, `docs/api/REST_API_v3.md`,
> `docs/Specification.md`, `test-app/workflows/*`) — nothing is invented.

---

## 1. Environment bring-up

The whole stack (infra + every service + the api-gateway + the **seeder**) comes up
with one command from the repo root. The `seeder` is a regular compose service with
`depends_on` wiring, so it runs automatically — there is no separate `make seed`
target to invoke.

```bash
# from repo root
make docker-up        # == docker compose up --build -d   (starts infra, all services, AND the seeder)
make docker-logs      # == docker compose logs -f         (stream logs; watch for "seeder: all bootstrapping complete")
make docker-down      # == docker compose down            (tear everything down)
```

**Confirm readiness** — poll the public version endpoint (no auth) until it answers 200:

```bash
curl -s http://localhost:8080/api/v3/version
# → {"version":"2.16.13"}        (value = repo-root VERSION file, baked into the gateway binary)
```

**Confirm the seeder finished** — the four standard accounts (§3) only exist once
`make docker-logs` prints `seeder: all bootstrapping complete`. The seeder waits a
30s cooldown (`SEEDER_COOLDOWN`) then logs each account it provisions. Until it
finishes, logins for the seeded users will return 401.

**Running the existing Go integration tests** (used by this plan to link "existing
test" references; never authored anew here):

```bash
make test-integration   # cd test-app && go test -v -tags integration -timeout 60m ./workflows/...
make test               # unit tests, all services
make ci                 # full local CI: fmt-check + build + unit tests + lint + tidy-check
```

---

## 2. Base URL & versioning

| Item | Value |
|---|---|
| Base URL | `http://localhost:8080` |
| API prefix | **`/api/v3/`** — every route in this plan lives under it |
| Content-Type | `application/json` |
| Swagger UI | `http://localhost:8080/swagger/index.html` (generated docs are known-stale; trust `docs/api/REST_API_v3.md`) |
| Version probe | `GET /api/v3/version` → `{"version":"<semver>"}` (public) |

**v1 / v2 are retired.** Any request to `/api/v1/*` or `/api/v2/*` returns **HTTP 404**.
A negative TC for version routing asserts exactly this. A future breaking change would
add an explicit `/api/v4/` router (no transparent fallback).

---

## 3. Seed credentials & how to get a token per role

The seeder (`seeder/cmd/main.go`) provisions **four employees + three clients**, all
sharing the same password from `docker-compose.yml`
(`ADMIN_PASSWORD`, default **`AdminAdmin2026!.`** — note the trailing dot). Emails are
derived from `ADMIN_EMAIL` (`admin+testadmin@admin.com`) by swapping the `+suffix`:

| Role | Email | Password | Seeded role |
|---|---|---|---|
| **admin** | `admin+testadmin@admin.com` | `AdminAdmin2026!.` | `EmployeeAdmin` (all permissions) |
| **agent** | `admin+testagent@admin.com` | `AdminAdmin2026!.` | `EmployeeAgent` (+ securities trading) |
| **supervisor** | `admin+testsupervisor@admin.com` | `AdminAdmin2026!.` | `EmployeeSupervisor` (+ OTC/funds/`verification.skip`/`verification.manage`) |
| **client** | `admin+testclient@admin.com` | `AdminAdmin2026!.` | bank client |
| **client (2nd)** | `admin+testclient2@admin.com` | `AdminAdmin2026!.` | bank client (for counterparty/multi-bidder flows) |
| **client (3rd)** | `admin+testclient3@admin.com` | `AdminAdmin2026!.` | bank client |

> Roles map to permission sets per `Specification.md` §6 "Role Definitions":
> `EmployeeBasic` ⊂ `EmployeeAgent` ⊂ `EmployeeSupervisor` ⊂ `EmployeeAdmin`.

### Getting a token (the literal login exchange)

There is **one unified login** for employees and clients. The endpoint auto-detects
the principal type and mints the matching JWT (`system_type`/`principal_type` =
`employee` or `client`).

**Request** — `POST /api/v3/auth/login` (public, no auth):

```bash
curl -s -X POST http://localhost:8080/api/v3/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin+testadmin@admin.com","password":"AdminAdmin2026!."}'
```

Request body fields: **`email`**, **`password`** (both required).

**Response 200** — read the **`access_token`** field (the `refresh_token` is for renewal):

```json
{
  "access_token": "eyJhbGciOiJFUzI1Ni9...",
  "refresh_token": "a3f8c2d1e9b4..."
}
```

Failure shapes: `400` (validation), `401 invalid credentials` (wrong password / unknown
email — collapsed to one message to prevent email enumeration), `429 rate_limited`
(login bucket is 20 / 5 min per IP).

### Setting the token on subsequent requests

Put the `access_token` in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v3/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin+testadmin@admin.com","password":"AdminAdmin2026!."}' | jq -r .access_token)

curl -s http://localhost:8080/api/v3/clients -H "Authorization: Bearer $TOKEN"
```

Access tokens are ES256-signed and expire after 15 min; renew via
`POST /api/v3/auth/refresh` with `{"refresh_token":"..."}`. Two distinct 401s tell the
client what to do: **`401 token_expired`** → refresh and retry (claims stale or past
`exp`); **`401 unauthorized`** → re-authenticate (token invalid / session revoked).

### "employee-on-behalf" actor

There is no separate login for "employee acting on behalf of a client." You log in as
an **employee** that holds the relevant `*.on_behalf*` permission, then call the
employee on-behalf route and name the client:

- **Place an order for a client:** `POST /api/v3/orders` — employee JWT +
  `orders.place-on-behalf` permission; body carries `client_id` + `account_id` (the
  account **must** belong to `client_id`, else `403 forbidden`).
- **Accept an OTC offer for a client:** routes guarded by `otc.trade.on_behalf`.

The Go suite builds these actors with `setupAgentEmployee` / `setupSupervisorEmployee` /
`setupAdminEmployee` / `setupBasicEmployee` (in `test-app/workflows/stock_helpers_test.go`).
`EmployeeBasic` deliberately lacks `orders.place-on-behalf`, so it is the canonical
"unpermitted role → 403" actor.

---

## 4. Creating fixtures

### A funded client account (the common precondition)

The suite's `setupActivatedClient` (`test-app/workflows/helpers_test.go`) does the full
dance — create client → create RSD account funded with 100 000 → activate via the
Kafka activation token → log in. The two raw admin calls it makes (verified-working
bodies):

**Create a client** — `POST /api/v3/clients` (admin token):

```json
{
  "first_name": "Test", "last_name": "Client",
  "date_of_birth": 631152000, "gender": "other",
  "email": "client@example.com", "phone": "+381600000000",
  "address": "Test St 1", "jmbg": "1506995000099"
}
```
→ `201` with `{"id": <clientID>, ...}`.

**Create + fund an account** — `POST /api/v3/accounts` (admin token):

```json
{
  "owner_id": <clientID>,
  "account_kind": "current",
  "account_type": "personal",
  "currency_code": "RSD",
  "initial_balance": 100000
}
```
→ `201` with `{"id", "account_number", ...}`. `account_kind` ∈ `current` (RSD-only) /
`foreign` (EUR/CHF/USD/GBP/JPY/CAD/AUD). `initial_balance` funds **both** the `balance`
and the `available_balance` columns, so the account is immediately spendable.

Helper shortcuts: `createClientAccount` (current/RSD), `createClientForeignAccount`
(foreign), `setupActivatedClientWithForeignAccount` (RSD 100k + foreign 10k),
`setupClientWithCard`, `setupMobileDevice`.

> Activation requires reading the one-time token off Kafka — see §7 (`scanKafkaForActivationToken`).

### Funding the bank RSD sentinel (and topping up accounts) for testing

The **bank-owned** accounts (treasury / fee-collector) are flagged
`is_bank_account = true` with the sentinel `owner_id = 1_000_000_000`. Read the lowest-id
RSD bank account via `GET /api/v3/bank-accounts` (employee token) — see
`getBankRSDAccount`. To **directly** seed/top-up any account for a test (e.g. give the
bank RSD account enough liquidity to service cross-currency transfers or loan
disbursement), edit the account DB — and you **must update BOTH `balance` and
`available_balance`** (the available-balance column is independent; updating only
`balance` yields "insufficient available balance: have 0"):

```bash
# account-service DB:  host port 5435, db "accountdb", user/pass postgres/postgres
docker compose exec account-db psql -U postgres -d accountdb -c \
  "UPDATE accounts SET balance = balance + 1000000, available_balance = available_balance + 1000000 \
   WHERE owner_id = 1000000000 AND currency_code = 'RSD';"
```

### `testing_mode` — make securities orders fill fast

Outside simulated market hours, a placed order gets `after_hours = true` and the
order-engine waits ~30 min per portion before filling (looks like "order accepted but
holdings never update"). Flip `testing_mode` **before** placing the order (the first
wait is computed at placement):

- **Via API:** `POST /api/v3/stock-exchanges/testing-mode` with `{"enabled": true}`
  (employee JWT + `exchanges.manage`); read state with `GET /api/v3/stock-exchanges/testing-mode`.
- **Via DB:** stock-service DB is `stockdb` (also on the postgres user). Set the flag:
  ```bash
  docker compose exec stock-db psql -U postgres -d stockdb -c \
    "INSERT INTO system_settings(key,value) VALUES('testing_mode','true') \
     ON CONFLICT(key) DO UPDATE SET value='true';"
  ```

Helper to wait on a fill: `waitForOrderFill` / `tryWaitForOrderFill` (poll `is_done`);
`buyStock` places a market buy and blocks until filled.

---

## 5. The test-case template + ID scheme

Every test case in every Celina file uses this template **verbatim** (copied from
spec §4):

```
#### TC-C2-PAY-001 · <title> (POSITIVE|NEGATIVE)
- **Feature:** <Serbian → English>  · **Spec:** Celina N §x.y  · **Existing test:** test-app/workflows/<file>.go::<Test> (or "—")
- **Actor:** <client | agent | supervisor | admin | employee-on-behalf | unauthenticated>
- **Preconditions:** <seeded/funded state, prior TC dependencies>
- **Request:** `<METHOD> <path>`
  - Auth: `Bearer <role token>`  (or none)
  - Body: `<JSON>`  (omit for GET/DELETE)
- **Verification:** fast-path (`verification.skip`) | full-flow (→ cross-cutting-verification.md) | n/a
- **Expected:** `<HTTP status>` · `<error.code or response fields>` · side-effects: `<balance deltas, status transitions, Kafka topics, ledger/audit entries>`
- **Negative siblings:** <inline list of the invalid variants and their expected error codes>
```

**ID scheme:** `TC-C<celina>-<AREA>-<nnn>` — e.g. `TC-C3-ORD-014`. `<celina>` is 1–5
(or omitted/`X` for the cross-cutting file), `<AREA>` is a short uppercase domain tag
(e.g. `PAY`, `TRF`, `ORD`, `CARD`, `LOAN`, `FUND`, `OTC`, `SITX`), `<nnn>` is a
zero-padded sequence. Actor variants of one case take suffixes **`a/b/c`**
(`TC-C1-LOGIN-003a` = employee, `…003b` = client). **IDs are stable forever** — never
renumber; retire a case by marking it, not by reusing its ID.

**Mandatory rigor:** for any money-moving or state-changing case, asserting the HTTP
status alone is **not** sufficient — assert the side-effects too (balance deltas via
§7, entity status transitions, Kafka events, ledger/audit rows).

---

## 6. Verification fast-path

Payments, transfers, and OTC exercises are gated by a verification challenge. Two paths:

- **Fast-path (`verification.skip`):** employees holding the `verification.skip`
  permission (`EmployeeSupervisor`, `EmployeeAdmin`) bypass the challenge entirely. Use
  this when the actor is a supervisor/admin and the case is *about* the gated action,
  not the verification mechanism itself. In a TC, set **Verification:** `fast-path`.
- **Full-flow (clients & any actor without `verification.skip`):** the client must
  request a challenge, receive the code, submit it, then execute. In tests, `code_pull`
  challenges accept the universal **bypass code `"111111"`** (no Kafka/mobile-inbox
  round-trip needed for the code itself). The minimal sequence (helpers in
  `helpers_test.go`):

  1. `POST /api/v3/verifications` `{"source_service":"payment","source_id":<id>}` → `{"challenge_id":<cid>}` (`createChallengeOnly`)
  2. `POST /api/v3/verifications/<cid>/code` `{"code":"111111"}` → 200 (`submitVerificationCode`)
  3. `POST /api/v3/me/payments/<id>/execute` `{"verification_code":"111111","challenge_id":<cid>}` → 200

  `createAndVerifyChallenge` / `createVerificationAndGetChallengeID` wrap steps 1–2;
  `createAndExecutePayment` / `createAndExecuteTransfer` wrap the entire create→verify→execute chain.

The **real** challenge mechanism (request → code via Kafka/mobile inbox → submit), and
its negatives (wrong code, 5-min `VERIFICATION_CHALLENGE_EXPIRY` expiry, 3-attempt
`VERIFICATION_MAX_ATTEMPTS` cap → transaction cancelled, all
`verification_method` values), lives in **`cross-cutting-verification.md`**. In a TC,
set **Verification:** `full-flow (→ cross-cutting-verification.md)` and reference it
rather than re-documenting the flow.

---

## 7. Common assertions

### Error envelope

Every error response uses this exact shape (the `details` object is optional):

```json
{
  "error": {
    "code": "snake_case_error_code",
    "message": "Human-readable error message",
    "details": { }
  }
}
```

Assert on **`error.code`** (the stable machine string), not on `error.message`. The
HTTP status must always match the code's semantics (a 403 body never arrives with a 500
status).

### gRPC → HTTP error-code table (authoritative — `Specification.md` §14)

| gRPC code | HTTP status | `error.code` |
|---|---|---|
| `InvalidArgument` | 400 | `validation_error` |
| `Unauthenticated` | 401 | `unauthorized` |
| `PermissionDenied` | 403 | `forbidden` |
| `NotFound` | 404 | `not_found` |
| `AlreadyExists` | 409 | `conflict` |
| `FailedPrecondition` | 409 | `business_rule_violation` |
| `ResourceExhausted` | 429 | `rate_limited` |
| *(default / `Internal`)* | 500 | `internal_error` |

Additional codes surfaced directly by the gateway (per `REST_API_v3.md` "Error Response
Format"): `invalid_input` (400, malformed/out-of-range), `not_authenticated` (401,
missing/invalid bearer token), `not_implemented` (501, planned-but-absent endpoint).
When a requirement describes a feature with **no** matching endpoint, write the TC and
mark it `NO-ENDPOINT` in the coverage matrix (a real gap, never silently skipped).

> Note on ownership: a mismatched caller-supplied resource id may return **`404 not_found`**
> instead of `403 forbidden` where existence must not leak (`enforceOwnership`). State
> the expected one per TC.

### Reading a balance

`GET /api/v3/accounts?account_number=<num>` (employee or client token) returns an
envelope `{"accounts":[...],"total":N}`; read **`accounts[0].available_balance`**. The
balance fields are serialized as decimal **strings** (e.g. `"100000.0000"`) — parse to a
number before arithmetic (`getAccountBalance` / `parseJSONBalance`). Bank RSD account:
`GET /api/v3/bank-accounts` → lowest-id RSD entry (`getBankRSDAccount`). Use
`assertBalanceChanged(account, before, expectedDelta)` to assert a money delta with a
0.01 tolerance.

### Scanning Kafka for `notification.send-email`

Activation tokens, reset links, lock notices, and other emails are published to the
Kafka topic **`notification.send-email`** (brokers default `localhost:9092` from
outside Docker). Read it from the earliest offset on **partition 0 with no GroupID**
(a direct partition reader — a consumer group would be redirected to the in-cluster
`kafka:9092` advertised address, unreachable from the host). Message payload:

```json
{ "to": "<email>", "email_type": "ACTIVATION", "data": { "token": "<one-time-token>" } }
```

Filter by `to == <email>` and `email_type` (e.g. `"ACTIVATION"`), then read
`data["token"]`. Helpers: `scanKafkaForActivationToken(t, email)` (15s block) and
`scanKafkaForMobileActivationCode(t, email)`. To assert a notification was *emitted* as
a side-effect, scan for the matching message; absence within the window is a failure.

### Reading audit / ledger

Ledger entries back every money movement (account-service `LedgerEntry`,
`entry_type` ∈ `debit|credit`, `reference_type` ∈ `payment|transfer|fee|interest`).
Audit / changelog reads are available to admins via the changelog/audit-log routes
(`Specification.md` §6 `admin.audit.view`). When a TC's side-effect is a ledger or
audit row, assert its presence and key fields, not just the balance.

---

## 8. Functional-equivalence rule (protocol vs. everything else)

- **Non-protocol objects** (all the ordinary REST request/response bodies in
  Celina 1–4 and the client/employee-facing parts of Celina 5): tests assert
  **equivalent functionality**, not byte-identical shapes. Field names/ordering may
  differ from any external reference as long as the documented fields, status codes,
  and side-effects hold. Copy the real shapes from `REST_API_v3.md` / the handlers /
  the existing Go tests — never invent them.
- **Protocol objects** (the cross-bank **SI-TX** wire messages exchanged between peer
  banks): tests assert the bodies **match the wire spec exactly**. The pinned
  references are `contract/sitx/testdata/*.json`
  (`newtx_coffee.json`, `newtx_otc_accept.json`, `newtx_otc_exercise.json`,
  `public_stock.json`, `user.json`, `vote_no.json`) and
  `docs/protocol/bank-to-bank-asset-exchange-protocol-spec.md`. Conformance details:
  signed-amount tagged-union postings, `{vote, reasons}`, `transactionId` correlation,
  bare `/public-stock`, display-name `/user`, money-as-number. Cross-bank/peer routes
  are **frozen** — assert the spec'd verbs/paths even where they violate REST
  conventions (e.g. `GET /negotiations/:rid/:id/accept`); do not test "corrected" forms.
