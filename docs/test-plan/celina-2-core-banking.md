# Celina 2 — Osnovno poslovanje banke (Core Banking)

> Read [`00-setup-and-conventions.md`](./00-setup-and-conventions.md) first — it defines the
> base URL (`http://localhost:8080`, all routes under `/api/v3`), seed credentials, role→token
> recipes, the test-case template, the verification fast-path, the gRPC→HTTP error-code table,
> and how to read balances / scan Kafka. This file does not repeat them.

**Scope (requirements: `Celina 2 2026.docx.md`; spec: `Specification.md` §17/§18/§20/§21;
routes: `REST_API_v3.md` §5–§19):**

- **Accounts** — tekući (RSD-only) & devizni (EUR/CHF/USD/GBP/JPY/CAD/AUD); lični vs poslovni;
  all personal subtypes (standardni/štedni/penzionerski/za mlade/za studente/za nezaposlene) and
  business subtypes (DOO/AD/Fondacija); auto-card checkbox; initial balance; owner = existing vs
  new-client-inline; account-number format (3-digit bank prefix); list/filter/detail (personal vs
  business); set status active/inactive; maintenance-fee-by-type.
- **Companies** (firma — DOO/AD/Fondacija, informational record for business accounts).
- **Bank accounts** — Naša Banka = Firma; ≥1 RSD + ≥1 FX delete-guard; fee/commission collection.
- **Payments** (`/api/me/payments`) — between different clients; fee stacking (0.1% ≥1000 + 5% ≥5000)
  credited to bank RSD account; saved recipients CRUD; payment history; verification fast-path +
  full-flow ref.
- **Transfers** (`/api/me/transfers`) — own accounts same + different currency (FX via RSD); commission
  to bank; reserved-funds semantics.
- **Menjačnica** — kursna lista, equivalence calculator, 2-leg via RSD with per-leg commission.
- **Cards** — request/auto-create; max 2 physical/account (client) & 1/authorized-person; virtual
  single_use/multi_use(+max_uses); brands visa/mastercard/dinacard/amex; PIN 4-digit bcrypt
  lock-after-3; change limit; block (client) / unblock (employee); temp-block + auto-expiry;
  deactivate + "deactivated cannot be reactivated"; authorized persons for business accounts.
- **Loans** — submit request (all types × interest types); employee approve/reject with
  max-approval-limit gate; disbursement saga (debit bank + credit client; insufficient-bank-liquidity
  → 409); installment schedule + formula; fixed vs variable + bank margin + tier; automatic monthly
  installment deduction (success + failure).

### Implementation-vs-requirement discrepancies surfaced (test the IMPLEMENTED value, flag the gap)

| # | Requirement says | Implementation does | Where covered |
|---|---|---|---|
| **D1** | Loan types named gotovinski/stambeni/auto/refinansirajući/studentski. The stale `REST_API_v3.md` example body shows `PERSONAL/MORTGAGE/AUTO/STUDENT/BUSINESS`. | Gateway validates **`cash`,`housing`,`auto`,`refinancing`,`student`** (`credit_handler.go` `oneOf`). The doc example is wrong. | TCs use the real enum; matrix flags the doc gap. |
| **D2** | Payment between **different-currency** accounts should convert via the bank with a commission. | **Payments are single-currency** (`payment_service.go`: recipient credited `InitialAmount`, fee added on top, no FX). Cross-currency money movement lives in **transfers**. | TC-C2-PAY-020 marked NO-ENDPOINT / partial. |
| **D3** | Verification: client gets a code, 5-min validity, 3 attempts. Recommends TOTP/30s. | `verification_method` active = `code_pull` (default) + `email`; `qr_scan`,`number_match` **planned, not active**. | Full flow → `cross-cutting-verification.md`; methods gap noted. |
| **D4** | Virtual cards: single_use / multi_use / **unlimited**. | `POST /me/cards/virtual` accepts only **`single_use`,`multi_use`** (`card_handler.go` `oneOf`); `unlimited` is in the enum but not creatable via the API. | TC-C2-CARD-033 NO-ENDPOINT. |
| **D5** | Card temporary-block + auto-unblock; deactivated card cannot be reactivated. | No card `activate`/`reactivate` route exists; unblocking a **deactivated** card → `409` (`ErrCardDeactivated`). | TC-C2-CARD-046. |
| **D6** | Multi-currency card fees (bank 2% + Mastercard 0.5% conversion fee) on RSD card paying in FX. | No card-spend / POS endpoint exists in the backend API (cards are issued/managed, not charged). | TC-C2-CARD-050 NO-ENDPOINT. |
| **D7** | Variable-rate cron generates a monthly random shift `[-1.5%,+1.5%]`; or employee enters rate. | Implemented as **employee-driven** tier update + `POST /interest-rate-tiers/:id/apply` (Opcija 2); no autonomous random cron exposed via API. | TC-C2-LOAN-040. |
| **D8** | Auto-installment retry after 72h on insufficient funds; penalty rate bump. | Daily cron deducts due installments; on failure marks `overdue` + notifies. Retry-window / penalty-bump details are team-configurable and not asserted beyond status+notification. | TC-C2-INST-005/006. |

---

## A. Accounts

Account number format (spec §"Broj računa"): 18 digits = bank code (3) + branch (4) + random (9) +
type (2), with check digit `(sum of digits) % 11`. This bank's `OWN_BANK_CODE = 111`. The API
renders the number grouped (e.g. `265-1234567890123-56` in docs). Assert the rendered number's bank
prefix matches `OWN_BANK_CODE` and that the number is unique.

#### TC-C2-ACC-001 · Create tekući (current) RSD personal standard account (POSITIVE)
- **Feature:** Kreiranje tekućeg računa (current account) · **Spec:** Celina 2 §"Tekući račun" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_CreateCurrentAccount`
- **Actor:** employee (`accounts.create`)
- **Preconditions:** client `owner_id=1` exists.
- **Request:** `POST /api/v3/accounts`
  - Auth: `Bearer <employee>`
  - Body: `{"owner_id":1,"account_kind":"current","account_type":"standard","account_category":"personal","currency_code":"RSD","initial_balance":10000.00}`
- **Verification:** n/a
- **Expected:** `201` · account object with `account_kind="current"`, `currency_code="RSD"`, `status="active"`, `balance="10000.0000"`, `available_balance="10000.0000"`, `maintenance_fee="220.0000"`, `account_number` starts with `OWN_BANK_CODE`, `expires_at` ≈ created_at + 5 years. side-effects: account row created; owner email "account created" (`notification.send-email`).
- **Negative siblings:** missing `owner_id`/`account_kind`/`currency_code` → `400 validation_error` (`TestAccount_MissingRequiredFields`); `account_kind="savings"` → `400` (`TestAccount_CreateWithInvalidKind`); `currency_code="XXX"` → `400 validation_error` (`TestAccount_InvalidCurrencyCode`); unauthenticated → `401 unauthorized` (`TestAccount_UnauthenticatedCannotCreate`).

#### TC-C2-ACC-002 · Reject current account with non-RSD currency (NEGATIVE)
- **Feature:** Tekući = RSD only · **Spec:** Celina 2 §"Tekući račun"; §21 "current → RSD only" · **Existing test:** —
- **Actor:** employee (`accounts.create`)
- **Preconditions:** client exists.
- **Request:** `POST /api/v3/accounts`
  - Body: `{"owner_id":1,"account_kind":"current","account_type":"standard","currency_code":"EUR"}`
- **Expected:** `400 validation_error` — "current accounts can only use RSD currency" (account-service `ErrInvalidAccount`, `InvalidArgument`). side-effects: no account created.
- **Negative siblings:** `account_kind="foreign"` + `currency_code="RSD"` → `400` "foreign accounts cannot use RSD".

#### TC-C2-ACC-003 · Create devizni (foreign) account — one per currency EUR/CHF/USD/GBP/JPY/CAD/AUD (POSITIVE, enum sweep)
- **Feature:** Devizni račun, sve valute · **Spec:** Celina 2 §"Devizni račun" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_CreateForeignAccount`, `TestAccount_CreateForeignPersonalUSD`, `TestAccount_BankAccountCreateForeignEUR`
- **Actor:** employee (`accounts.create`)
- **Preconditions:** client exists; one TC per currency in {EUR, CHF, USD, GBP, JPY, CAD, AUD}.
- **Request:** `POST /api/v3/accounts`
  - Body: `{"owner_id":1,"account_kind":"foreign","account_type":"standard","account_category":"personal","currency_code":"<CUR>"}`
- **Expected:** `201` · `account_kind="foreign"`, `currency_code="<CUR>"`, `balance="0.0000"`. side-effects: account created; email sent.
- **Negative siblings:** `currency_code="USX"` (unsupported) → `400 validation_error`.

#### TC-C2-ACC-004 · Create account with initial balance (POSITIVE, boundary)
- **Feature:** Polje za početno stanje · **Spec:** Celina 2 §"Napomena 3" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_CreateWithInitialBalance`
- **Actor:** employee (`accounts.create`)
- **Request:** `POST /api/v3/accounts` Body: `{...,"currency_code":"RSD","account_kind":"current","initial_balance":250000.50}`
- **Expected:** `201` · `balance="250000.5000"`, `available_balance="250000.5000"`. side-effect: ledger seeded.
- **Negative siblings:** `initial_balance:-1` → `400 validation_error` ("initial_balance must be >= 0").

#### TC-C2-ACC-005a..f · Create all PERSONAL subtypes (POSITIVE, enum sweep + maintenance-fee check)
- **Feature:** Podvrste ličnog računa · **Spec:** Celina 2 §"Tekući račun" (standardni/štedni/penzionerski/za mlade/za studente/za nezaposlene) · **Existing test:** —
- **Actor:** employee (`accounts.create`)
- **Preconditions:** `account_type` is free-form (no gateway enum); maintenance fee derives from a known set, else 220.
- **Request:** `POST /api/v3/accounts` Body: `{"owner_id":1,"account_kind":"current","account_category":"personal","currency_code":"RSD","account_type":"<TYPE>"}`
  - `a` `standard` → `maintenance_fee="220.0000"`
  - `b` `savings` (štedni) → `220.0000` (unmapped → default)
  - `c` `pension` (penzionerski) → `100.0000`
  - `d` `youth` (za mlade) → `0.0000`
  - `e` `student` (za studente) → `0.0000`
  - `f` `unemployed` (za nezaposlene) → `220.0000` (unmapped → default)
- **Expected:** `201` · `account_category="personal"`, `maintenance_fee` per row above.
- **Negative siblings:** `account_type` empty/missing → `400 validation_error` ("account_type required").

#### TC-C2-ACC-006a..c · Create all BUSINESS subtypes DOO/AD/Fondacija (POSITIVE, enum sweep)
- **Feature:** Podvrste poslovnog računa · **Spec:** Celina 2 §"Tekući račun" / §"Flow ako je poslovni račun" · **Existing test:** —
- **Actor:** employee (`accounts.create`)
- **Preconditions:** company created first (see TC-C2-CMP-001) → `company_id`.
- **Request:** `POST /api/v3/accounts` Body: `{"owner_id":1,"account_kind":"current","account_category":"business","currency_code":"RSD","account_type":"<doo|ad|fondacija>","company_id":<CID>}`
- **Expected:** `201` · `account_category="business"`, `company_id=<CID>`.
- **Negative siblings:** `account_category="enterprise"` → `400 validation_error` (`oneOf` personal/business).

#### TC-C2-ACC-007 · Create account with auto-card (`create_card=true`) (POSITIVE)
- **Feature:** Checkbox "Napravi karticu" · **Spec:** Celina 2 §"Napomena 1" / §"Kreiranje kartice" · **Existing test:** —
- **Actor:** employee (`accounts.create` + `cards.create`)
- **Request:** `POST /api/v3/accounts` Body: `{"owner_id":1,"account_kind":"current","account_type":"standard","currency_code":"RSD","create_card":true,"card_brand":"visa"}`
- **Expected:** `201` · account created **and** one card auto-created on it (assert via `GET /api/v3/accounts/:id/cards` → 1 card, brand `visa`, status `active`). side-effect: card row + card-create email.
- **Negative siblings:** `create_card:true,"card_brand":"jcb"` → `400 validation_error` (brand `oneOf`).

#### TC-C2-ACC-008 · Create account without auto-card (`create_card=false`) (POSITIVE)
- **Feature:** Checkbox not selected · **Spec:** Celina 2 §"Napomena 1" · **Existing test:** —
- **Actor:** employee
- **Request:** `POST /api/v3/accounts` Body: `{...,"create_card":false}`
- **Expected:** `201` · `GET /api/v3/accounts/:id/cards` → 0 cards.

#### TC-C2-ACC-009 · Owner = newly-created client inline, then account (POSITIVE, flow)
- **Feature:** Vlasnik: kreiraj novog klijenta · **Spec:** Celina 2 §"Opis podataka → Vlasnik" · **Existing test:** `test-app/workflows/client_test.go::TestClient_CreateMultipleClients`
- **Actor:** employee
- **Preconditions:** none.
- **Request:** chain `POST /api/v3/clients {…}` → capture new `id` → `POST /api/v3/accounts {"owner_id":<newId>,...}`
- **Expected:** both `201`; account's `owner_id` = the new client id; account appears in `GET /api/v3/clients/<newId>/accounts`.
- **Negative siblings:** `owner_id` = non-existent client id → `404 not_found` (or `400`).

#### TC-C2-ACC-010 · List all accounts (supervisor "find any") (POSITIVE)
- **Feature:** Portal za upravljanje računima · **Spec:** Celina 2 §"Portal za upravljanje Računima" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_ListAllAccounts`, `TestAccount_ListWithPagination`
- **Actor:** employee (`accounts.read`)
- **Request:** `GET /api/v3/accounts?page=1&page_size=20`
- **Expected:** `200` · `{accounts:[...],total:N}`.
- **Negative siblings:** two filters at once `?name_filter=x&account_number=y` → `400` (mutually exclusive); client token → `403 forbidden`.

#### TC-C2-ACC-011 · Lookup account by exact number (POSITIVE)
- **Feature:** Filter po broju računa · **Spec:** Celina 2 §"Portal za upravljanje Računima" (filter) · **Existing test:** `test-app/workflows/account_test.go::TestAccount_GetByAccountNumber`
- **Actor:** employee (`accounts.read`)
- **Request:** `GET /api/v3/accounts?account_number=<num>`
- **Expected:** `200` · array of 0 or 1 (never `404`).
- **Negative siblings:** unknown number → `200` empty array.

#### TC-C2-ACC-012 · List a client's own accounts via /me (POSITIVE)
- **Feature:** Opcija "Računi" (klijent) · **Spec:** Celina 2 §"Opcija Računi" (only active, sorted by available balance desc) · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/me/accounts`
- **Expected:** `200` · only the caller's accounts; each carries `available_balance`, `reserved_balance`, `balance`.
- **Negative siblings:** unauthenticated → `401`.

#### TC-C2-ACC-013 · Account detail — personal (POSITIVE)
- **Feature:** Detaljan prikaz računa - lični · **Spec:** Celina 2 §"Detaljan prikaz računa - ličnog" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_GetAccountByID`
- **Actor:** employee (`accounts.read`)
- **Request:** `GET /api/v3/accounts/:id`
- **Expected:** `200` · `account_number, account_name, owner_id, owner_name, account_kind, account_type, account_category, balance, available_balance, reserved_balance, currency_code, status, maintenance_fee, daily_limit, monthly_limit, daily_spending, monthly_spending`.
- **Negative siblings:** non-existent id → `404 not_found` (`TestAccount_GetNonExistent`).

#### TC-C2-ACC-014 · Account detail — business shows company (POSITIVE)
- **Feature:** Detaljan prikaz računa - poslovnog · **Spec:** Celina 2 §"Detaljan prikaz računa - poslovnog" · **Existing test:** —
- **Actor:** employee (`accounts.read`)
- **Preconditions:** a business account with `company_id`.
- **Request:** `GET /api/v3/accounts/:id`
- **Expected:** `200` · `account_category="business"`, `company_id` populated (firma info reachable via `GET /companies` / detail).

#### TC-C2-ACC-015 · Update account name (POSITIVE)
- **Feature:** Promena naziva računa · **Spec:** Celina 2 §"Detaljan prikaz računa - ličnog" (name unique per client, ≠ current) · **Existing test:** `test-app/workflows/account_test.go::TestAccount_UpdateName`
- **Actor:** employee (`accounts.update`)
- **Request:** `PUT /api/v3/accounts/:id/name` Body: `{"new_name":"Stedni 2026"}`
- **Expected:** `200` · `account_name="Stedni 2026"`. side-effect: changelog entry.
- **Negative siblings:** new name equal to current → `400`/`409`; name duplicate of another account of same client → `409 conflict`; empty name → `400 validation_error`.

#### TC-C2-ACC-016 · Update account limits with verification (POSITIVE)
- **Feature:** Promena limita (zahteva verifikaciju), only owner · **Spec:** Celina 2 §"Detaljan prikaz računa - ličnog" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_UpdateLimits`
- **Actor:** employee (`accounts.update`) — or client owner via verification
- **Request:** `PUT /api/v3/accounts/:id/limits` Body: `{"daily_limit":500000.00,"monthly_limit":5000000.00,"verification_code":"<code>"}`
- **Verification:** full-flow (code validated against transaction-service) | fast-path for `verification.skip`
- **Expected:** `200` · updated `daily_limit`/`monthly_limit`. side-effect: limit-change email.
- **Negative siblings:** `daily_limit:-5` → `400 validation_error` (`TestAccount_UpdateLimitsNegativeRejected`); invalid/absent `verification_code` → `400 validation_error`; `daily_limit:0` → service rejects ("must be > 0").

#### TC-C2-ACC-017 · Deactivate then reactivate account (POSITIVE, state transition)
- **Feature:** Status active/inactive · **Spec:** Celina 2 §"Tekući račun" (Status na zahtev); §20 `account_status` · **Existing test:** `test-app/workflows/account_test.go::TestAccount_UpdateStatus`
- **Actor:** employee (`accounts.deactivate.any`)
- **Request:** `POST /api/v3/accounts/:id/deactivate` then `POST /api/v3/accounts/:id/activate`
- **Expected:** `200` `{status:"inactive"}` then `200` `{status:"active"}`. side-effect: status changelog; inactive account excluded from client "Računi" list (only active shown).
- **Negative siblings:** deactivate non-existent id → `404` (`TestAccount_DeactivateNonExistent`); caller without `accounts.deactivate.any` → `403 forbidden`.

#### TC-C2-ACC-018 · List supported currencies (POSITIVE)
- **Feature:** Currency entitet · **Spec:** Celina 2 §"Opis podataka → Valute" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_ListCurrencies`
- **Actor:** employee (any role)
- **Request:** `GET /api/v3/currencies`
- **Expected:** `200` · includes RSD + EUR/CHF/USD/GBP/JPY/CAD/AUD with `code,name,symbol`.

---

## B. Companies

#### TC-C2-CMP-001 · Create company (firma) (POSITIVE)
- **Feature:** Firma (informational, for business accounts) · **Spec:** Celina 2 §"Flow ako je poslovni račun → Firma" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_CreateCompany`
- **Actor:** employee (`accounts.create`)
- **Preconditions:** owner client exists.
- **Request:** `POST /api/v3/companies` Body: `{"company_name":"EX Tech d.o.o.","registration_number":"12345678","tax_number":"987654321","activity_code":"62.01","address":"Bulevar 1, NS","owner_id":1}`
- **Expected:** `201` · company object echoing fields + `id`.
- **Negative siblings:** missing `company_name`/`registration_number`/`owner_id` → `400 validation_error`; duplicate `registration_number` → `409 conflict` (unique); `owner_id` non-existent → `404`/`400`.

---

## C. Bank Accounts (Naša Banka = Firma)

#### TC-C2-BANK-001 · List + create + delete bank account (POSITIVE)
- **Feature:** Računi banke · **Spec:** Celina 2 §"Naša Banka = Firma"; §21 "≥1 RSD + ≥1 FX" · **Existing test:** `test-app/workflows/account_test.go::TestAccount_BankAccountCRUD`, `TestAccount_DeleteBankAccount`, `TestAccount_BankAccountCreateForeignEUR`
- **Actor:** employee (`bank-accounts.manage`)
- **Request:** `GET /api/v3/bank-accounts`; `POST /api/v3/bank-accounts {"currency_code":"EUR","account_kind":"foreign","account_name":"EX EUR 2"}`; `DELETE /api/v3/bank-accounts/:id`
- **Expected:** list seeded with ≥1 RSD + ≥1 FX (`owner_id=1000000000`, `owner_name="EX Banka"`); create `201`; delete `200 {success:true}`.
- **Negative siblings:** `account_kind="x"` → `400`; client token → `403 forbidden`.

#### TC-C2-BANK-002 · Delete guard: cannot remove last RSD / last FX bank account (NEGATIVE)
- **Feature:** Delete-guard (≥1 RSD + ≥1 FX) · **Spec:** Celina 2 §"Naša Banka = Firma"; §21 · **Existing test:** —
- **Actor:** employee (`bank-accounts.manage`)
- **Preconditions:** exactly one RSD bank account remains.
- **Request:** `DELETE /api/v3/bank-accounts/:id` (the last RSD one)
- **Expected:** `400` — "cannot delete: bank must maintain at least one RSD account" (`ErrLastBankAccount`). Mirror for last FX account.
- **Negative siblings:** deleting a non-bank account id → `400`/`404`; non-existent id → `404`.

#### TC-C2-BANK-003 · Bank account ledger activity (POSITIVE)
- **Feature:** Praćenje transakcija banke · **Spec:** Celina 2 §"Naša Banka = Firma" (provizije, menjačnica) · **Existing test:** `test-app/workflows/bank_account_activity_test.go::TestBankAccountActivity_EmployeeCanView`
- **Actor:** employee (`bank-accounts.manage`)
- **Request:** `GET /api/v3/bank-accounts/:id/activity?page=1`
- **Expected:** `200` · `entries[]` with `entry_type, amount, balance_before, balance_after, reference_type` (e.g. fee `credit`).
- **Negative siblings:** passing a non-bank (client) account id → `404`/`403` (`TestBankAccountActivity_RejectsClientAccount`).

---

## D. Payments (`/api/v3/me/payments`)

Fee model (`payment_service.go`): fee is computed in the **sender's** currency and **added on top** —
`total_debit = amount + fee`, recipient receives `amount`, the bank's RSD account is credited `fee`.
Default seeded rules stack: 0.1% for amount ≥1000 **and** 5% for amount ≥5000.

#### TC-C2-PAY-001 · Payment to another client, same currency, below fee threshold (POSITIVE, end-to-end)
- **Feature:** Novo plaćanje (ista valuta, bez provizije) · **Spec:** Celina 2 §"Opcija Plaćanja → Novo plaćanje"; §"Terminologija" (iste valute → direkt, bez provizije) · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_EndToEnd`
- **Actor:** client (owner of source account)
- **Preconditions:** sender funded RSD account; recipient is a **different** client's RSD account.
- **Request:** `POST /api/v3/me/payments` Body: `{"from_account_number":"<A>","to_account_number":"<B>","amount":500.00,"recipient_name":"Mama","payment_code":"289","payment_purpose":"poklon"}`
- **Verification:** full-flow (client) → see `cross-cutting-verification.md`; then `POST /api/v3/me/payments/:id/execute {"challenge_id":<cid>}`
- **Expected:** create `201` `status="pending_verification"`, `commission=0`, `final_amount=500`; after execute `200` `status="completed"`. side-effects: sender −500, recipient +500, **bank RSD unchanged** (no fee under 1000); `notification.send-email` to both.
- **Negative siblings:** `amount:0` / negative → `400 validation_error`; missing `from`/`to` → `400`.

#### TC-C2-PAY-002 · Payment with stacked fees ≥5000 RSD (POSITIVE, fee math + bank credit)
- **Feature:** Provizija (0.1% ≥1000 + 5% ≥5000) na račun banke · **Spec:** Celina 2 §"Novo plaćanje"; §21 "Default seeded fees" · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_WithFee`
- **Actor:** client
- **Preconditions:** sender funded with ≥ 5000 + fee.
- **Request:** `POST /api/v3/me/payments` Body: `{"from_account_number":"<A>","to_account_number":"<B>","amount":5000.00}` then execute.
- **Verification:** full-flow / fast-path
- **Expected:** `commission = 5000×0.1% + 5000×5% = 5 + 250 = 255.0000`; `final_amount=5255`. side-effects: sender −5255, recipient +5000, **bank RSD account +255** (assert via `GET /bank-accounts/:id/activity` credit `reference_type="payment"`).
- **Negative siblings:** with fee-service down → transaction rejected (fee lookup failure rejects, not silent).

#### TC-C2-PAY-003 · Fee threshold boundary 1000 (POSITIVE, boundary each side)
- **Feature:** Fee min_amount threshold · **Spec:** §21 "0.1% for ≥1000" · **Existing test:** —
- **Actor:** client
- **Request:** two payments: `amount=999.99` and `amount=1000.00`
- **Expected:** `999.99` → `commission=0`; `1000.00` → `commission=1.0000` (0.1%). bank credited only on the second.

#### TC-C2-PAY-004 · Fee threshold boundary 5000 (POSITIVE, boundary)
- **Feature:** Fee 5% min_amount=5000 · **Spec:** §21 · **Existing test:** —
- **Request:** `amount=4999.99` vs `amount=5000.00`
- **Expected:** `4999.99` → `commission≈5.0000` (only 0.1% rule); `5000.00` → `commission=255.0000` (both rules stack).

#### TC-C2-PAY-005 · Insufficient funds (NEGATIVE)
- **Feature:** Nedovoljno sredstava · **Spec:** Celina 2; E2E "Neuspešno plaćanje zbog nedovoljnih sredstava" · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_InsufficientBalance`
- **Actor:** client
- **Preconditions:** sender balance < amount + fee.
- **Request:** `POST /api/v3/me/payments` (+ execute)
- **Expected:** `409 business_rule_violation` ("Nedovoljno sredstava" / insufficient funds). side-effects: no balance change; payment ends `failed` (no partial debit).

#### TC-C2-PAY-006 · Wrong-owner source account (NEGATIVE, ownership)
- **Feature:** Resource ownership · **Spec:** CLAUDE.md Resource Ownership; §21 ownership · **Existing test:** —
- **Actor:** client B
- **Request:** `POST /api/v3/me/payments` with `from_account_number` belonging to client A
- **Expected:** `403 forbidden` (or `404 not_found` to avoid leaking). side-effects: none.

#### TC-C2-PAY-007 · Wrong verification code rejects execute (NEGATIVE)
- **Feature:** Verifikacija (3 attempts, 5 min) · **Spec:** Celina 2 §"Verifikacioni kod" · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_WrongOTPCodeRejected`
- **Actor:** client
- **Request:** create payment → `POST /api/v3/me/payments/:id/execute {"challenge_id":<unverified>}`
- **Expected:** `409` (verification not completed). After 3 wrong codes the challenge → cancelled (→ cross-cutting). side-effects: no money moved.

#### TC-C2-PAY-008 · Inactive / non-existent recipient account (NEGATIVE)
- **Feature:** Primalac neaktivan/nepostojeći · **Spec:** Celina 2 §"Novo plaćanje" · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/payments` `to_account_number` = inactive or unknown intra-bank number
- **Expected:** unknown → `404 not_found`; inactive → `409 business_rule_violation` (account not active). side-effects: none.

#### TC-C2-PAY-009 · Over client daily/monthly/transfer limit (NEGATIVE, boundary)
- **Feature:** Dnevni/mesečni limit · **Spec:** Celina 2 §"Tekući račun" (Dnevni/Mesečni limit); §21 spending limits atomic in account-service · **Existing test:** —
- **Actor:** client
- **Preconditions:** account `daily_limit=10000`, `daily_spending=9500`.
- **Request:** `POST /api/v3/me/payments` `amount=1000` (+ fee) → execute
- **Expected:** `409 business_rule_violation` (daily limit exceeded — authoritative check in account-service `UpdateBalance`). Boundary: `amount=500` (exactly to limit) succeeds.

#### TC-C2-PAY-010 · Payment preview (POSITIVE)
- **Feature:** Info polje "preostali limit" / fee preview · **Spec:** Celina 2 §"Novo plaćanje" (Info polje) · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_PreviewAndStatus`
- **Actor:** client
- **Request:** `POST /api/v3/me/payments/preview {"from_account_number":"<A>","to_account_number":"<B>","amount":1000.00}`
- **Expected:** `200` · `{currency,input_amount,total_fee,fee_breakdown[],total_debit,amount_received}`; `total_debit=input+fee`, `amount_received=input`.
- **Negative siblings:** `amount<=0` → `400 validation_error`.

#### TC-C2-PAY-011 · Payment history + status (POSITIVE)
- **Feature:** Pregled plaćanja (filter, status Realizovano/Odbijeno/U Obradi) · **Spec:** Celina 2 §"Pregled plaćanja" · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_EmployeeCanReadPayments`, `TestPayment_PreviewAndStatus`
- **Actor:** client (own) / employee (`payments.read`)
- **Request:** `GET /api/v3/me/payments`; `GET /api/v3/me/payments/:id/status`; employee `GET /api/v3/accounts/:id/payments?status_filter=COMPLETED&date_from=...&amount_min=...`
- **Expected:** `200` · list scoped to caller; status maps `pending`/`completed`/`failed`. employee filters apply.
- **Negative siblings:** `GET /api/v3/me/payments/:id` for another user's payment → `404 not_found`.

#### TC-C2-PAY-012 · Kafka events emitted on payment (POSITIVE, side-effect)
- **Feature:** Obaveštenja (email + in-app) · **Spec:** Celina 2 §"Kreiranje računa klijenata" (obaveštenja); Kafka requirement · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_KafkaEventsOnPayment`
- **Actor:** client
- **Expected:** on completion `notification.send-email` published for sender (and recipient); in-app notification stored.

#### TC-C2-PAY-013 · Employee cannot create a /me payment as themselves blindly (NEGATIVE, auth)
- **Feature:** /me payments require valid principal · **Spec:** REST conventions (AnyAuthMiddleware) · **Existing test:** `test-app/workflows/payment_test.go::TestPayment_UnauthenticatedCannotCreatePayment`
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/me/payments` no token
- **Expected:** `401 unauthorized`.

#### TC-C2-PAY-020 · Cross-currency payment with FX conversion (NEGATIVE / NO-ENDPOINT — discrepancy D2)
- **Feature:** Plaćanje razl. valuta → preko banke, sa provizijom · **Spec:** Celina 2 §"Terminologija"; §"Logika" menjačnica · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/payments` from RSD account to a EUR account
- **Expected:** **Implementation gap.** Payments are single-currency (`payment_service.go` credits raw `InitialAmount` with no FX). Either currencies must match (use **transfers** for FX), or the recipient is credited un-converted. Mark `NO-ENDPOINT`/partial; the FX path is covered by transfers (TC-C2-TRF-002). For different-owner cross-currency money movement, there is no FX-aware path today — log as gap.

---

## E. Transfers (`/api/v3/me/transfers`)

Transfers move money between the **same client's own** accounts; same-currency = no fee, cross-currency
converts via RSD with commission and credits the bank.

#### TC-C2-TRF-001 · Same-currency transfer between own accounts (POSITIVE, end-to-end)
- **Feature:** Prenos (ista valuta, provizija=0, kurs=/) · **Spec:** Celina 2 §"Opcija Transferi → Logika" · **Existing test:** `test-app/workflows/transfer_test.go::TestTransfer_SameCurrency_EndToEnd`
- **Actor:** client (owns both accounts)
- **Preconditions:** two RSD accounts of same client; source funded.
- **Request:** `POST /api/v3/me/transfers {"from_account_number":"<A>","to_account_number":"<B>","amount":1000.00}` → execute with `challenge_id`
- **Verification:** full-flow / fast-path
- **Expected:** create `201` `status="pending_verification"`, `commission=0`, `exchange_rate=1`, `final_amount=1000`; execute `200` `completed`. side-effects: A −1000, B +1000, bank unchanged.
- **Negative siblings:** `amount<=0` → `400`; insufficient funds → `409 business_rule_violation` (`TestTransfer_InsufficientBalance`).

#### TC-C2-TRF-002 · Cross-currency transfer RSD→EUR (POSITIVE, FX + commission to bank)
- **Feature:** Prenos razl. valuta (provizija 0-1%, dnevni kurs, preko RSD) · **Spec:** Celina 2 §"Transferi → Logika"; §"Menjačnica → Logika" · **Existing test:** `test-app/workflows/transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR`
- **Actor:** client (owns RSD + EUR accounts)
- **Request:** `POST /api/v3/me/transfers {"from_account_number":"<RSD>","to_account_number":"<EUR>","amount":11700.00}` → execute
- **Expected:** `exchange_rate` = sell rate, `commission > 0`, `final_amount` ≈ converted EUR; status `completed`. side-effects: RSD debited, EUR credited converted amount, **bank account credited the commission** (assert via bank activity). Conversion is via RSD (X→RSD→Y) with per-leg sell rate.
- **Negative siblings:** exchange-service unavailable → transfer fails (cross-currency requires exchange service); to-account of a **different client** → validation fails (transfers are intra-client) → `400`/`403`.

#### TC-C2-TRF-003 · Transfer preview (POSITIVE)
- **Feature:** "Potvrdi transfer" prikaz kurs + provizija · **Spec:** Celina 2 §"Transferi → Klijentovo iskustvo" · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/transfers/preview {"from_account_number":"<RSD>","to_account_number":"<EUR>","amount":5000}`
- **Expected:** `200` · `{from_currency,to_currency,input_amount,total_fee,fee_breakdown[],converted_amount,exchange_rate,exchange_commission_rate}`; for same-currency `converted_amount=input`, `exchange_rate="1.0000"`.
- **Negative siblings:** missing fields → `400 validation_error`; unknown account → `404 not_found`.

#### TC-C2-TRF-004 · Transfer to another client's account rejected (NEGATIVE, intra-client guard)
- **Feature:** Transfer = isti klijent · **Spec:** Celina 2 §"Opcija Transferi"; §21 "transfers between same client only" · **Existing test:** —
- **Actor:** client A
- **Request:** `POST /api/v3/me/transfers` to client B's account
- **Expected:** `400 validation_error` / `403 forbidden` (must be same client / own accounts). Use **payments** for other people.

#### TC-C2-TRF-005 · Transfer history + listings (POSITIVE)
- **Feature:** Istorija transfera (hronološki) · **Spec:** Celina 2 §"Transferi → Klijentovo iskustvo" · **Existing test:** `test-app/workflows/transfer_test.go::TestTransfer_EmployeeCanReadTransfers`, `TestTransfer_ListByClient`
- **Actor:** client (own) / employee (`payments.read`)
- **Request:** `GET /api/v3/me/transfers`; employee `GET /api/v3/transfers/:id`, `GET /api/v3/clients/:id/transfers`
- **Expected:** `200` · transfers scoped/sorted newest-first.
- **Negative siblings:** `GET /api/v3/me/transfers/:id` other user's → `404`; unauthenticated create → `401` (`TestTransfer_UnauthenticatedCannotCreateTransfer`).

#### TC-C2-TRF-006 · Reserved-funds semantics for intra-bank transfer (POSITIVE, side-effect)
- **Feature:** Rezervisana sredstva (uvek 0 unutar banke) · **Spec:** Celina 2 §"Opis podataka" (Raspoloživo = stanje − rezervisana; internal = instant, no reservation) · **Existing test:** —
- **Actor:** client
- **Request:** complete an intra-bank transfer, read both accounts before/after.
- **Expected:** `reserved_balance` stays `0`; `available_balance == balance` at rest (internal transactions are instant, no reservation). (Reserved funds only arise in cross-bank — Celina 5.)

---

## F. Payment Recipients

#### TC-C2-RCP-001 · Recipient CRUD (POSITIVE)
- **Feature:** Primaoci plaćanja (pregled/kreiranje/izmena/brisanje) · **Spec:** Celina 2 §"Primaoci plaćanja" · **Existing test:** `test-app/workflows/transfer_test.go::TestTransfer_PaymentRecipientCRUD`
- **Actor:** client
- **Request:** `POST /api/v3/me/payment-recipients {"client_id":1,"recipient_name":"Mama","account_number":"<num>"}`; `GET /api/v3/me/payment-recipients`; `PUT /api/v3/me/payment-recipients/:id {"recipient_name":"Mama nova"}`; `DELETE /api/v3/me/payment-recipients/:id`
- **Expected:** create `201`; list contains it; update `200`; delete `200 {success:true}`.
- **Negative siblings:** missing `recipient_name`/`account_number` → `400 validation_error`; `PUT`/`DELETE` another user's recipient → `404 not_found`; unauthenticated → `401`.

---

## G. Menjačnica (Exchange)

Public, informational. Bank sells the `toCurrency` (uses the **sell** rate) and takes a per-leg
commission (0-1%, default 0.5%); cross-currency always routes via RSD (X→RSD→Y).

#### TC-C2-FX-001 · Kursna lista (list all rates) (POSITIVE)
- **Feature:** Kursna lista · **Spec:** Celina 2 §"Menjačnica → Kursna lista" · **Existing test:** `test-app/workflows/exchange_rate_test.go::TestExchangeRates_ListAll`
- **Actor:** unauthenticated (public)
- **Request:** `GET /api/v3/exchange/rates`
- **Expected:** `200` · `rates[]` with `from_currency,to_currency,buy_rate,sell_rate,updated_at` for supported pairs.

#### TC-C2-FX-002 · Specific pair (POSITIVE)
- **Feature:** Kurs za par · **Spec:** Celina 2 §"Menjačnica" · **Existing test:** `test-app/workflows/exchange_rate_test.go::TestExchangeRates_GetSpecific`
- **Actor:** public
- **Request:** `GET /api/v3/exchange/rates/EUR/RSD`
- **Expected:** `200` · single rate object.
- **Negative siblings:** unknown pair `GET /exchange/rates/EUR/ZZZ` → `404 not_found`.

#### TC-C2-FX-003 · Equivalence calculator (POSITIVE)
- **Feature:** Proveri ekvivalentnost (kalkulator) · **Spec:** Celina 2 §"Menjačnica → Proveri ekvivalentnost" · **Existing test:** `test-app/workflows/exchange_rate_test.go::TestExchangeRates_Calculate`
- **Actor:** public
- **Request:** `POST /api/v3/exchange/calculate {"fromCurrency":"EUR","toCurrency":"RSD","amount":"100.00"}`
- **Expected:** `200` · `{from_currency,to_currency,input_amount,converted_amount,commission_rate,effective_rate}`; commission applied; informational (no transaction created).
- **Negative siblings:** missing fields → `400 validation_error` (`TestExchangeRates_Calculate_MissingFields`); `amount<=0`/non-numeric → `400` (`TestExchangeRates_Calculate_InvalidAmount`); unsupported currency → `400`/`404` (`TestExchangeRates_Calculate_UnsupportedCurrency`).

#### TC-C2-FX-004 · 2-leg conversion via RSD with per-leg commission (POSITIVE, formula)
- **Feature:** EUR→USD ide preko RSD, provizija po koraku · **Spec:** Celina 2 §"Menjačnica → Logika" (Primer 2) · **Existing test:** `test-app/workflows/transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR` (FX path via transfer)
- **Actor:** public (calculate) / client (executed via transfer)
- **Request:** `POST /api/v3/exchange/calculate {"fromCurrency":"EUR","toCurrency":"USD","amount":"100.00"}`
- **Expected:** `converted_amount` reflects EUR→RSD→USD with sell rate + commission per leg. (Execution path: a cross-currency **transfer** between the client's EUR and USD accounts routes the from-money to the bank's RSD account and credits from the bank's target-currency account, per §"Naša Banka = Firma" #2.)

---

## H. Cards

Card number: 16 digits (Amex 15), CVV 3; brand by prefix (Visa 4, Mastercard 51-55/2221-2720,
DinaCard 9891, Amex 34/37); Luhn check digit. `card_number_full`+`cvv` returned only at create.

#### TC-C2-CARD-001a..d · Issue card per brand (POSITIVE, enum sweep)
- **Feature:** Vrste kartica (visa/mastercard/dinacard/amex) · **Spec:** Celina 2 §"Osnovne informacije" (MII/IIN) · **Existing test:** `test-app/workflows/card_test.go::TestCard_CreateAllBrands`, `TestCard_AllBrandsDebitAndCredit`
- **Actor:** employee (`cards.create`)
- **Request:** `POST /api/v3/cards {"account_number":"<num>","owner_id":1,"owner_type":"CLIENT","card_brand":"<VISA|MASTERCARD|DINA|AMEX>"}`
- **Expected:** `201` · masked `card_number`, `card_number_full` matching the brand prefix + valid Luhn, `cvv` (3 digits), `card_type="DEBIT"`, `status="ACTIVE"`. side-effect: card-create/verification email.
- **Negative siblings:** `card_brand="jcb"` → `400 validation_error` (`TestCard_CreateWithInvalidBrand`); `owner_type="X"` → `400` (`oneOf client/authorized_person`).

#### TC-C2-CARD-002 · Max 2 physical cards per personal account (NEGATIVE, boundary)
- **Feature:** Lični račun → max 2 kartice · **Spec:** Celina 2 §"Osnovne informacije"; §21 "max 2 per account" · **Existing test:** —
- **Actor:** employee (`cards.create`)
- **Preconditions:** account already has 2 active cards.
- **Request:** `POST /api/v3/cards` (3rd card, owner_type CLIENT)
- **Expected:** `409 business_rule_violation` ("personal accounts can have at most 2 cards", `ErrCardLimitReached`). Boundary: 1st and 2nd succeed.

#### TC-C2-CARD-003 · Max 1 card per authorized person per business account (NEGATIVE, boundary)
- **Feature:** Poslovni račun → max 1 kartica po osobi · **Spec:** Celina 2 §"Osnovne informacije"; §"Kreiranje kartice" · **Existing test:** —
- **Actor:** employee (`cards.create`)
- **Preconditions:** authorized person already holds 1 card on the account.
- **Request:** `POST /api/v3/cards {"owner_type":"AUTHORIZED_PERSON","owner_id":<apId>,...}` (2nd)
- **Expected:** `409 business_rule_violation` ("business accounts can have at most 1 card per person").

#### TC-C2-CARD-004 · Block (employee) / unblock (employee) / deactivate (POSITIVE, state machine)
- **Feature:** Blokiranje/odblokiranje/deaktivacija · **Spec:** Celina 2 §"Blokiranje kartice"; §"Portal za upravljanje Računima" · **Existing test:** `test-app/workflows/card_test.go::TestCard_BlockUnblockDeactivate`
- **Actor:** employee (`cards.update`)
- **Request:** `POST /api/v3/cards/:id/block` → `POST /api/v3/cards/:id/unblock` → `POST /api/v3/cards/:id/deactivate`
- **Expected:** `200` `status` transitions `ACTIVE→BLOCKED→ACTIVE→DEACTIVATED`. side-effect: client (and authorized-person + owner for business) notified by email on each change.
- **Negative siblings:** block a non-existent card → `404 not_found`; unblock a card that isn't blocked → `409` (`ErrCardNotBlocked`); block a deactivated card → `409` (`ErrCardDeactivated`).

#### TC-C2-CARD-005 · Client can block own card but NOT unblock (NEGATIVE/POSITIVE)
- **Feature:** Klijent samo blokira; odblokira samo zaposleni · **Spec:** Celina 2 §"Blokiranje kartice" · **Existing test:** —
- **Actor:** client (owner) + employee
- **Request:** client `POST /api/v3/me/cards/:id/temporary-block` (self-block); attempting unblock has no client route → unblock only via employee `POST /api/v3/cards/:id/unblock`
- **Expected:** client self-block `200`; unblock requires employee `cards.update`. A client calling an employee card route → `403 forbidden`.

#### TC-C2-CARD-006 · Deactivated card cannot be reactivated (NEGATIVE) — discrepancy D5
- **Feature:** "Kartica je deaktivirana i ne može se ponovo aktivirati" · **Spec:** Celina 2 §"Blokiranje kartice"; E2E "Klijent pokušava da aktivira deaktiviranu karticu" · **Existing test:** —
- **Actor:** employee / client
- **Request:** on a `DEACTIVATED` card → `POST /api/v3/cards/:id/unblock` (no activate route exists)
- **Expected:** `409 business_rule_violation` ("card is deactivated", `ErrCardDeactivated`). There is **no** card reactivation endpoint by design — assert none exists.

#### TC-C2-CARD-007 · Virtual card single_use (POSITIVE)
- **Feature:** Virtuelna kartica single_use · **Spec:** §18/§20 `usage_type`; §21 "single_use (1 use)" · **Existing test:** `test-app/workflows/card_test.go::TestCard_VirtualCardSingleUse`, `TestCard_VirtualSingleUseWithClientAuth`
- **Actor:** client
- **Request:** `POST /api/v3/me/cards/virtual {"account_number":"<num>","owner_id":1,"card_brand":"visa","usage_type":"single_use","expiry_months":1,"card_limit":"5000.0000"}`
- **Expected:** `201` · `usage_type` single_use, `expires_at` ≈ +1 month, owner derived from JWT (body `owner_id` ignored).
- **Negative siblings:** `usage_type="bogus"` → `400` (`TestCard_VirtualInvalidUsageType`); `expiry_months=4` → `400` (`inRange 1-3`).

#### TC-C2-CARD-008 · Virtual card multi_use with max_uses (POSITIVE, boundary)
- **Feature:** multi_use + max_uses ≥ 2 · **Spec:** §20 `usage_type`; card_handler `max_uses>=2` · **Existing test:** `test-app/workflows/card_test.go::TestCard_VirtualMultiUseWithClientAuth`
- **Actor:** client
- **Request:** `POST /api/v3/me/cards/virtual {...,"usage_type":"multi_use","max_uses":5,"expiry_months":2,"card_limit":"5000.0000"}`
- **Expected:** `201` · `max_uses=5`.
- **Negative siblings:** `usage_type="multi_use","max_uses":1` → `400 validation_error` ("multi_use cards must have max_uses >= 2"); `max_uses` omitted for multi_use → `400`.

#### TC-C2-CARD-009 · Virtual card `unlimited` (NO-ENDPOINT — discrepancy D4)
- **Feature:** Virtuelna kartica unlimited · **Spec:** Celina 2 §"Kreiranje kartice"; §20 `usage_type` includes `unlimited` · **Existing test:** `test-app/workflows/card_test.go::TestCard_VirtualUnlimitedWithClientAuth` (covers an unlimited-style path indirectly)
- **Actor:** client
- **Request:** `POST /api/v3/me/cards/virtual {...,"usage_type":"unlimited",...}`
- **Expected:** **`400 validation_error`** — gateway `oneOf` accepts only `single_use`/`multi_use`. `unlimited` exists in the enum/model but is **not creatable** via this route. Mark NO-ENDPOINT/partial.

#### TC-C2-CARD-010 · PIN set + verify (POSITIVE)
- **Feature:** PIN (4 cifre) · **Spec:** §21 "PIN 4 digits, bcrypt" · **Existing test:** `test-app/workflows/card_test.go::TestCard_PINManagement`, `TestCard_PINSetAndVerify`, `TestCard_ChangePin`
- **Actor:** client (owner)
- **Request:** `POST /api/v3/me/cards/:id/pin {"pin":"1234"}` then `POST /api/v3/me/cards/:id/verify-pin {"pin":"1234"}`
- **Expected:** set `200 {success:true}`; verify `200 {valid:true}`. PIN stored bcrypt-hashed.
- **Negative siblings:** `pin:"12"` / `"abcd"` → `400 validation_error` (`validatePin` exactly 4 digits); set/verify on a card not owned → `404 not_found`.

#### TC-C2-CARD-011 · PIN locks card after 3 wrong attempts (NEGATIVE, boundary)
- **Feature:** Zaključavanje nakon 3 pogrešna PIN-a · **Spec:** §21 "locked after 3 failed attempts" · **Existing test:** `test-app/workflows/card_test.go::TestCard_PINWrongThreeTimes_LocksCard`
- **Actor:** client
- **Request:** `verify-pin` with wrong PIN ×3.
- **Expected:** attempts 1-2 → `200 {valid:false}`; 3rd → card `status="blocked"`; subsequent verify → `403 forbidden` ("card locked", `ErrCardLocked`). side-effect: PIN-attempt metric, card blocked.

#### TC-C2-CARD-012 · Temporary block + auto-expiry unblock (POSITIVE, side-effect)
- **Feature:** Privremeno blokiranje + auto-odblok · **Spec:** §21 "temp blocks auto-unblocked every 1 min" · **Existing test:** `test-app/workflows/card_test.go::TestCard_TemporaryBlockWithExpiry`
- **Actor:** client (owner)
- **Request:** `POST /api/v3/me/cards/:id/temporary-block {"duration_hours":1,"reason":"Lost"}`
- **Expected:** `200` `status="blocked"`; background cron auto-unblocks when the window elapses. side-effect: `CARD_TEMPORARY_BLOCKED` notification.
- **Negative siblings:** `duration_hours:0` / `>720` → `400 validation_error` (`inRange 1-720`); temp-block a card not owned → `404`.

#### TC-C2-CARD-013 · List cards (client masked, employee by account/client) (POSITIVE)
- **Feature:** Lista kartica (maska 1234********5678) · **Spec:** Celina 2 §"Lista kartica" · **Existing test:** `test-app/workflows/card_test.go::TestCard_GetCard`, `TestCard_ListByAccount`
- **Actor:** client (`/me`) / employee (`cards.read`)
- **Request:** `GET /api/v3/me/cards`; `GET /api/v3/me/cards/:id`; employee `GET /api/v3/accounts/:id/cards`, `GET /api/v3/clients/:id/cards`
- **Expected:** `200` · masked numbers (first 4 + last 4); each card shows account name+number.
- **Negative siblings:** `GET /api/v3/me/cards/:id` for a card not owned → `404`; client calling `GET /api/v3/cards/:id` (employee route) → `403`.

#### TC-C2-CARD-014 · Create authorized person for business account (POSITIVE)
- **Feature:** OvlascenoLice (informational, holds business card) · **Spec:** Celina 2 §"Flow ako je poslovni račun"; §"Kreiranje kartice" · **Existing test:** —
- **Actor:** employee (`cards.manage`)
- **Request:** `POST /api/v3/cards/authorized-persons {"first_name":"Ana","last_name":"Jovanovic","account_id":<bizAcc>}`
- **Expected:** `201` · authorized-person record (informational, cannot log in). Then a card can be issued to them (owner_type AUTHORIZED_PERSON), max 1/person.
- **Negative siblings:** missing `first_name`/`last_name`/`account_id` → `400 validation_error`.

#### TC-C2-CARD-015 · Multi-currency card spend fee (NO-ENDPOINT — discrepancy D6)
- **Feature:** RSD kartica plaća u stranoj valuti: bankina provizija 2% + Mastercard 0.5% · **Spec:** Celina 2 §"Opcija Kartice → Osnovne informacije" · **Existing test:** —
- **Actor:** —
- **Expected:** **No card-spend / POS / authorization endpoint exists** in the backend API; cards are issued and managed but not charged through a transaction route. Mark `NO-ENDPOINT` (frontend/POS-simulation feature).

---

## I. Card Requests

#### TC-C2-CREQ-001 · Client requests a card → employee approves (creates card) (POSITIVE, lifecycle)
- **Feature:** Klijent zahteva karticu · **Spec:** Celina 2 §"Kreiranje kartice" (klijent traži); E2E "Klijent zahteva novu karticu" · **Existing test:** `test-app/workflows/card_request_test.go::TestCardRequest_FullLifecycle`, `TestCardRequest_EmployeeApproveAndRejectFlow`
- **Actor:** client (create) + employee (`cards.approve`)
- **Request:** client `POST /api/v3/me/cards/requests {"account_number":"<num>","card_brand":"visa"}`; employee `GET /api/v3/cards/requests?status=pending`; `POST /api/v3/cards/requests/:id/approve`
- **Expected:** create `201` `status="pending"`; approve `200` `{request:{status:"approved"},card:{...}}` — real card created on the account.
- **Negative siblings:** approve already-decided request → `409` (`ErrCardRequestAlreadyDecided`); request on an account at the 2-card max → approve fails `409` (`ErrCardLimitReached`).

#### TC-C2-CREQ-002 · Employee rejects with reason (POSITIVE/NEGATIVE)
- **Feature:** Odbijanje zahteva za karticu · **Spec:** Celina 2 §"Kreiranje kartice" · **Existing test:** `test-app/workflows/card_request_test.go::TestCardRequest_RejectRequiresReason`, `TestCardRequest_RejectNonExistentRequest`
- **Actor:** employee (`cards.approve`)
- **Request:** `POST /api/v3/cards/requests/:id/reject {"reason":"Insufficient history"}`
- **Expected:** `200` `status="rejected"`. side-effect: client notified.
- **Negative siblings:** missing `reason` → `400 validation_error`; reject non-existent → `404 not_found`.

#### TC-C2-CREQ-003 · Auth/role boundaries on card requests (NEGATIVE, actor sweep)
- **Feature:** RBAC on card requests · **Spec:** REST conventions; permissions · **Existing test:** `test-app/workflows/card_request_test.go::TestCardRequest_UnauthenticatedCannotCreateRequest`, `TestCardRequest_EmployeeCannotCreateRequest`, `TestCardRequest_EmployeeCanListRequests`, `TestCardRequest_EmployeeCanFilterByStatus`, `TestCardRequest_InvalidStatusFilterRejected`
- **Actor:** unauthenticated / employee / client
- **Expected:** unauthenticated create → `401`; employee cannot create a `/me` client request → `403`/identity-scoped; invalid `?status=foo` → `400`; client without permission cannot list all → `403`.

#### TC-C2-CREQ-004 · Client tracks own request via /me (POSITIVE, ownership)
- **Feature:** Klijent prati svoj zahtev · **Spec:** REST `/me` routes · **Existing test:** `test-app/workflows/card_request_test.go::TestCardRequest_GetNonExistentRequest`
- **Actor:** client
- **Request:** `GET /api/v3/me/cards/requests`; `GET /api/v3/me/cards/requests/:id`
- **Expected:** `200` own requests; another client's request id → `404 not_found`.

---

## J. Loans & Loan Requests

Loan types (gateway-validated — **discrepancy D1**): `cash`, `housing`, `auto`, `refinancing`,
`student`. Repayment periods per type: cash/auto/refinancing/student ∈ {12,24,36,48,60,72,84};
housing ∈ {60,120,180,240,300,360}. Interest = `InterestRateTier`(fixed or variable base) +
`BankMargin`(by type). Annual rate → monthly = annual/12. Installment A = P·r·(1+r)^N / ((1+r)^N − 1).

#### TC-C2-LOAN-001a..e · Submit loan request — all loan types × FIXED (POSITIVE, enum sweep)
- **Feature:** Podnošenje zahteva (sve vrste kredita) · **Spec:** Celina 2 §"Stranica Podnošenje zahteva" · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_AllLoanTypes`, `TestLoan_FullLifecycle`
- **Actor:** client
- **Preconditions:** client has an account in `currency_code`.
- **Request:** `POST /api/v3/me/loan-requests {"loan_type":"<cash|housing|auto|refinancing|student>","interest_type":"fixed","amount":500000,"currency_code":"RSD","repayment_period":<valid>,"account_number":"<num>","monthly_salary":120000,"employment_status":"EMPLOYED","employment_period":5,"purpose":"…","phone":"+381…"}`
- **Expected:** `201` · `status="pending"`. side-effect: request stored; employee portal lists it.
- **Negative siblings:** `loan_type="personal"` (the stale doc value) → `400 validation_error` (D1); `repayment_period=18` for cash → `400` ("not allowed for cash loans"); `repayment_period=24` for **housing** → `400`; `amount<=0` → `400`; missing `repayment_period`/`account_number`/`currency_code` → `400`; unauthenticated → `401` (`TestLoan_UnauthenticatedCannotCreateLoanRequest`).

#### TC-C2-LOAN-002 · Submit VARIABLE-interest request (POSITIVE)
- **Feature:** Tip kamate varijabilni · **Spec:** Celina 2 §"Kamatne stope i marža banke" · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_FullLifecycle`
- **Actor:** client
- **Request:** `POST /api/v3/me/loan-requests {...,"interest_type":"variable",...}`
- **Expected:** `201` `status="pending"`.
- **Negative siblings:** `interest_type="floating"` → `400 validation_error` (`oneOf fixed/variable`).

#### TC-C2-LOAN-003 · Employee approves → loan created + disbursed (POSITIVE, saga)
- **Feature:** Odobravanje kredita + isplata · **Spec:** Celina 2 §"Portal za Upravljanje Kreditima"; §21 "approval atomic, bank debited + borrower credited"; defense Provera 5 · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_FullLifecycle`, `test-app/workflows/loan_disbursement_test.go::TestLoanDisbursement_Saga_HappyPath`
- **Actor:** employee (`credits.approve`)
- **Preconditions:** pending request; bank has liquidity in loan currency; approving employee's `MaxLoanApprovalAmount` ≥ amount.
- **Request:** `POST /api/v3/loan-requests/:id/approve`
- **Expected:** `200` · loan object `status="ACTIVE"`, `loan_number`, `nominal_interest_rate`, `effective_interest_rate`, `next_installment_amount`, `next_installment_date`, `maturity_date`, `remaining_debt=amount`. side-effects: **bank currency account −amount**, **client account +amount** (assert both), approval email, request `status="approved"`.
- **Negative siblings:** approve non-existent → `404` (`TestLoan_ApproveNonExistentRequest`).

#### TC-C2-LOAN-004 · Approval blocked by employee MaxLoanApprovalAmount (NEGATIVE, gate)
- **Feature:** Limit odobrenja zaposlenog · **Spec:** §21 "Employee approval limited by MaxLoanApprovalAmount" · **Existing test:** —
- **Actor:** employee with `MaxLoanApprovalAmount=100000`
- **Preconditions:** pending request amount `500000`.
- **Request:** `POST /api/v3/loan-requests/:id/approve`
- **Expected:** `409 business_rule_violation` — "loan amount 500000.00 exceeds your approval limit of 100000.00". side-effects: no loan created, no disbursement.

#### TC-C2-LOAN-005 · Insufficient bank liquidity → 409, no partial debit (NEGATIVE, saga compensation)
- **Feature:** Likvidnost banke za isplatu · **Spec:** §21 "Bank must have sufficient liquidity … 409; saga compensates" · **Existing test:** `test-app/workflows/loan_disbursement_test.go::TestLoanDisbursement_BankInsufficientLiquidity_Returns409`
- **Actor:** employee (`credits.approve`)
- **Preconditions:** bank currency account balance < loan amount.
- **Request:** `POST /api/v3/loan-requests/:id/approve`
- **Expected:** `409 business_rule_violation`. side-effects: neither bank debited nor client credited (atomic); on partial-failure path loan → `disbursement_failed`, `BankOperation` log prevents double-debit on retry.

#### TC-C2-LOAN-006 · Reject loan request (POSITIVE)
- **Feature:** Odbijanje kredita · **Spec:** Celina 2 §"Portal za Upravljanje Kreditima" · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_RejectLoanRequest`, `TestLoan_RejectNonExistentRequest`
- **Actor:** employee (`credits.approve`)
- **Request:** `POST /api/v3/loan-requests/:id/reject`
- **Expected:** `200` `status="REJECTED"`. side-effect: rejection email.
- **Negative siblings:** reject non-existent → `404`.

#### TC-C2-LOAN-007 · Loan currency must match account currency (NEGATIVE)
- **Feature:** Valuta računa = valuta kredita · **Spec:** Celina 2 §"Stranica Podnošenje zahteva" (Broj računa — valuta mora da se poklapa); §21 · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/loan-requests {"currency_code":"EUR","account_number":"<RSD account>",...}`
- **Expected:** `400 validation_error` / `409 business_rule_violation` — loan currency ≠ account currency.

#### TC-C2-LOAN-008 · Loan registry & detail views (POSITIVE)
- **Feature:** Krediti — spisak + detalji · **Spec:** Celina 2 §"Stranica Krediti"; §"Portal za Upravljanje Kreditima" · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_ListAllLoans`, `TestLoan_ListLoansByClient`, `TestLoan_ListLoanRequests`, `TestLoan_ListLoanRequestsByClient`, `TestLoan_GetMyLoanRequest_SelfRoute`
- **Actor:** employee (`credits.read`) / client (`/me`)
- **Request:** `GET /api/v3/loans?loan_type_filter=cash&status_filter=ACTIVE`; `GET /api/v3/clients/:id/loans`; `GET /api/v3/loans/:id`; `GET /api/v3/loan-requests?status_filter=PENDING&client_id=1`; client `GET /api/v3/me/loans`, `GET /api/v3/me/loans/:id`, `GET /api/v3/me/loan-requests/:id`
- **Expected:** `200` · filtered lists; detail shows nominal vs effective rate, contract/maturity dates, next-installment, remaining debt, currency, status.
- **Negative siblings:** `GET /api/v3/loans/:id` non-existent → `404` (`TestLoan_GetNonExistentLoan`); `GET /api/v3/me/loans/:id` another user's → `404`.

#### TC-C2-LOAN-009 · Variable-rate recalculation via tier apply (POSITIVE) — discrepancy D7
- **Feature:** Varijabilna kamata — ažuriranje (Opcija 2: zaposleni unosi stopu) · **Spec:** Celina 2 §"Formula → Varijabilna kamatna stopa"; §21 "Variable-rate loans recalculate when tiers change" · **Existing test:** —
- **Actor:** employee (`interest-rates.manage`)
- **Request:** `PUT /api/v3/interest-rate-tiers/:id {"variable_base":4.0,...}` then `POST /api/v3/interest-rate-tiers/:id/apply`
- **Expected:** `200 {affected_loans:N}` — variable-rate loans in that amount range recompute `effective_interest_rate = base + bank_margin`. (Autonomous random `[-1.5%,+1.5%]` cron is not API-exposed — D7.)
- **Negative siblings:** `variable_base:-1` → `400`; apply on non-existent tier → `404`.

#### TC-C2-LOAN-010 · Interest-rate tiers & bank margins CRUD (POSITIVE, supporting config)
- **Feature:** Kamatne stope + marža banke · **Spec:** Celina 2 §"Kamatne stope i marža banke" (tiers + margin per type) · **Existing test:** —
- **Actor:** employee (`interest-rates.manage`)
- **Request:** `GET /api/v3/interest-rate-tiers`; `POST /api/v3/interest-rate-tiers {...}`; `GET /api/v3/bank-margins`; `PUT /api/v3/bank-margins/:id {"margin":1.75}`
- **Expected:** `200/201` · tiers reflect the 7 amount bands; margins per loan_type (cash 1.75, housing 1.50, auto 1.25, refinancing 1.00, student 0.75 per the spec table).
- **Negative siblings:** `fixed_rate`/`variable_base`/`margin` negative → `400 validation_error`; update non-existent → `404`.

---

## K. Installments & Automatic Deduction

#### TC-C2-INST-001 · Installment schedule generated on loan creation (POSITIVE, formula)
- **Feature:** Plan otplate + formula rate · **Spec:** Celina 2 §"Entiteti - kredit i rata"; §"Formula" · **Existing test:** `test-app/workflows/loan_test.go::TestLoan_FullLifecycle`
- **Actor:** client / employee
- **Request:** `GET /api/v3/loans/:id/installments` (employee) or `GET /api/v3/me/loans/:id/installments` (client)
- **Expected:** `200` · `installments[]`: at least history + 1 future installment; each `amount` matches A = P·r·(1+r)^N/((1+r)^N−1) within rounding; first `status="unpaid"`, `expected_date` set, `interest_rate` recorded per installment (matters for variable). Sum of principal repays the loan over `repayment_period`.
- **Negative siblings:** installments of a loan not owned (via `/me`) → `404`.

#### TC-C2-INST-002 · Installment amount formula boundary by amount tier (POSITIVE)
- **Feature:** Kamatna stopa po iznosu (tabela) · **Spec:** Celina 2 §"Kamatne stope i marža banke" · **Existing test:** —
- **Actor:** verifier
- **Expected:** for amount `500000` (tier 0–500k, fixed 6.25%) cash margin 1.75% → effective per rules; for amount `500001` (next tier 6.00%) the nominal steps down — assert the rate chosen matches the band the amount falls in.

#### TC-C2-INST-005 · Automatic monthly deduction — success (POSITIVE, cron side-effect)
- **Feature:** Automatsko skidanje rata (cron) · **Spec:** Celina 2 §"Automatsko skidanje rata" · **Existing test:** —
- **Actor:** system cron (daily)
- **Preconditions:** loan with an installment due today; client account funded.
- **Expected:** on cron run, installment `status` → `paid`, `actual_date` set; client account debited the installment; `next_installment_date` advances +1 month; `remaining_debt` decreases. side-effect: payment notification.
- **Negative siblings:** n/a (covered by failure case below).

#### TC-C2-INST-006 · Automatic deduction — insufficient funds (NEGATIVE, failure notice) — discrepancy D8
- **Feature:** Nedovoljno sredstava pri naplati rate · **Spec:** Celina 2 §"Automatsko skidanje rata" (retry 72h, penalty, email) · **Existing test:** —
- **Actor:** system cron
- **Preconditions:** loan installment due; client balance < installment.
- **Expected:** installment `status` → `overdue` (not `paid`); client notified by email/SMS; system retries on its configured window; persistent default may raise base rate / escalate. Assert: no partial debit, status `overdue`, notification emitted. (Exact 72h retry window + penalty bump are team-configurable and only asserted as "retry scheduled + notice sent".)

---

## L. Transfer Fees (supporting configuration)

#### TC-C2-FEE-001 · Fee rule CRUD (POSITIVE)
- **Feature:** Konfiguracija provizija · **Spec:** §16 Transfer Fees; §21 "fees cumulative, lookup failure rejects" · **Existing test:** (exercised indirectly by `TestPayment_WithFee`)
- **Actor:** employee (`fees.manage`)
- **Request:** `GET /api/v3/fees`; `POST /api/v3/fees {"name":"X","fee_type":"percentage","fee_value":"0.1","min_amount":"1000","transaction_type":"all"}`; `PUT /api/v3/fees/:id {"active":false}`; `DELETE /api/v3/fees/:id`
- **Expected:** seeded rules present (0.1% ≥1000, 5% ≥5000); create `201`; deactivate soft-removes; deleted rule no longer applies.
- **Negative siblings:** `fee_type="ratio"` → `400 validation_error`; client token → `403 forbidden`; update non-existent → `404`.

---

## M. Defense-flow end-to-end scenarios (Provere — `Banka 2025 - odbrana flow.docx.md` §2)

Each chains the relevant TCs above into one named must-pass grading flow.

#### TC-C2-E2E-P1 · Provera 1 — business current account, no card, new client + new company, ×2 FX accounts
- **Spec:** odbrana flow §2 Provera 1
- **Flow:** create client A (TC-C2-ACC-009) → create company (TC-C2-CMP-001) → create **business current** account (no `create_card`) (TC-C2-ACC-006) → create 2 **foreign** accounts for A (TC-C2-ACC-003).
- **Expected:** all `201`; business account links the company; no card created; A owns 1 current + 2 FX.

#### TC-C2-E2E-P2 · Provera 2 — personal current account WITH card, new client, verify card in list, ×2 FX
- **Spec:** odbrana flow §2 Provera 2
- **Flow:** create client B → create **personal current** account with `create_card=true` (TC-C2-ACC-007) → verify card via `GET /accounts/:id/cards` (TC-C2-CARD-013) → create 2 FX accounts.
- **Expected:** account + 1 auto card visible in list; B owns 1 current + 2 FX.

#### TC-C2-E2E-P3 · Provera 3 — transfer same + different currency, bank gets commission
- **Spec:** odbrana flow §2 Provera 3
- **Flow:** same-currency transfer (TC-C2-TRF-001) → cross-currency transfer (TC-C2-TRF-002) → check commission landed on bank account (TC-C2-BANK-003).
- **Expected:** same-currency commission 0; cross-currency commission > 0 credited to bank; balances reconcile.

#### TC-C2-E2E-P4 · Provera 4 — payment between different clients same + different currency, bank commission
- **Spec:** odbrana flow §2 Provera 4
- **Flow:** same-currency payment (TC-C2-PAY-001/002) → check bank commission credit (TC-C2-BANK-003). Different-currency payment → see D2 gap (TC-C2-PAY-020): FX payment between different clients is not implemented; document as gap during defense.
- **Expected:** same-currency payment + fee to bank verified; different-currency payment flagged as the D2 limitation.

#### TC-C2-E2E-P5 · Provera 5 — loan request → approve → bank debited, client credited
- **Spec:** odbrana flow §2 Provera 5
- **Flow:** client submits request (TC-C2-LOAN-001) → employee approves (TC-C2-LOAN-003) → assert bank account −amount and client account +amount.
- **Expected:** loan `ACTIVE`; bank debited; client credited; approval email.

#### TC-C2-E2E-P6 · Provera 6 — card request honoring constraints → auto-appears → client changes limit → client blocks → employee deactivates
- **Spec:** odbrana flow §2 Provera 6
- **Flow:** client requests card respecting max-2 (TC-C2-CREQ-001) → card appears → client changes card limit (`POST /api/v3/cards/:id/...` limit / via card update) → client blocks (TC-C2-CARD-005) → employee deactivates (TC-C2-CARD-004) → verify `DEACTIVATED` and not reactivatable (TC-C2-CARD-006).
- **Expected:** full card lifecycle; deactivated terminal.

#### TC-C2-E2E-P7 · Provera 7 — menjačnica shows equivalent value in a 2nd currency
- **Spec:** odbrana flow §2 Provera 7
- **Flow:** `POST /api/v3/exchange/calculate` (TC-C2-FX-003) and `GET /api/v3/exchange/rates` (TC-C2-FX-001).
- **Expected:** equivalent value computed with sell rate + commission.

---

## Field-Validation Matrices

### Account (`POST /api/v3/accounts`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `owner_id` | `1` | missing → `400 validation_error`; non-existent client → `404`/`400` |
| `account_kind` | `current` / `foreign` | `savings` → `400 validation_error` (oneOf) |
| `account_type` | `standard`,`savings`,`pension`,`youth`,`student`,`unemployed`,`doo`,`ad`,`fondacija` | missing/empty → `400` (required); unknown → accepted (free-form, fee defaults 220) |
| `account_category` | `personal` / `business` | `enterprise` → `400` (oneOf) |
| `currency_code` | `RSD`(current), `EUR/CHF/USD/GBP/JPY/CAD/AUD`(foreign) | unsupported `XXX` → `400`; `current`+non-RSD → `400`; `foreign`+`RSD` → `400` |
| `initial_balance` | `10000.00` | `-1` → `400` (nonNegative); omitted → `0` |
| `create_card` | `true`/`false` | — |
| `card_brand` (if create_card) | `visa/mastercard/dinacard/amex` | `jcb` → `400` (oneOf) |
| `company_id` | `1` (business) | non-existent → `404`/`400` |

### Company (`POST /api/v3/companies`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `company_name` | `EX Tech d.o.o.` | missing → `400 validation_error` |
| `registration_number` | `12345678` | missing → `400`; duplicate → `409 conflict` |
| `tax_number` | `987654321` | (optional) |
| `activity_code` | `62.01` | (optional; format xx.xx) |
| `address` | `Bulevar 1, NS` | (optional) |
| `owner_id` | `1` | missing → `400`; non-existent → `404`/`400` |

### Payment (`POST /api/v3/me/payments`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `from_account_number` | `265-…-56` | missing → `400`; not owned by caller → `403`/`404`; fund account → `403 fund_account_outflow_restricted` |
| `to_account_number` | `265-…-12` | missing → `400`; unknown intra-bank → `404`; inactive → `409` |
| `amount` | `5000.00` | `0`/negative → `400` (positive); insufficient funds → `409` |
| `currency` | (cross-bank only) | defaults to sender currency; cross-currency intra-bank → see D2 |
| `recipient_name`/`payment_code`/`reference_number`/`payment_purpose` | `289` etc. | optional |
| execute `challenge_id` | verified challenge | unverified/missing → `409` verification not completed |

### Transfer (`POST /api/v3/me/transfers`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `from_account_number` | own account | not owned → `403`/`404`; fund account → `403 fund_account_outflow_restricted` |
| `to_account_number` | own account (same client) | another client's account → `400`/`403`; cross-bank → rejected |
| `amount` | `1000.00` | `0`/negative → `400`; insufficient → `409` |
| execute `challenge_id` | verified | unverified → `409` |

### Card (`POST /api/v3/cards`, `POST /api/v3/me/cards/virtual`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `account_number` | `265-…-00` | missing → `400`; account at card max → `409 ErrCardLimitReached` |
| `owner_type` | `client`/`authorized_person` | other → `400` (oneOf) |
| `card_brand` | `visa/mastercard/dinacard/amex` | `jcb` → `400` |
| `usage_type` (virtual) | `single_use`/`multi_use` | `unlimited` → `400` (D4); `bogus` → `400` |
| `max_uses` (virtual) | `>=2` for multi_use | `1`/missing for multi_use → `400` |
| `expiry_months` (virtual) | `1`,`2`,`3` | `0`/`4` → `400` (inRange) |
| `card_limit` (virtual) | `"5000.0000"` | non-decimal → `400` |
| `pin` | `"1234"` | non-4-digit → `400` (validatePin); wrong ×3 → card blocked, then `403` |
| `duration_hours` (temp-block) | `1`–`720` | `0`/`>720` → `400` |

### Loan / LoanRequest (`POST /api/v3/me/loan-requests`)

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `loan_type` | `cash/housing/auto/refinancing/student` | `personal`/`mortgage` (stale doc) → `400` (D1) |
| `interest_type` | `fixed`/`variable` | `floating` → `400` |
| `amount` | `500000` | `0`/negative → `400` |
| `currency_code` | matches account | mismatch with account → `400`/`409` |
| `repayment_period` | per-type allowed set | out-of-set (e.g. `18` cash, `24` housing) → `400` |
| `account_number` | own account in currency | missing → `400`; not owned → `403`/`404` |
| `monthly_salary`/`employment_status`/`employment_period`/`purpose`/`phone` | optional | — |
| approve (employee) | — | amount > `MaxLoanApprovalAmount` → `409`; bank illiquid → `409` |

### Installment (read-only; generated)

| Field | Valid example | Notes |
|---|---|---|
| `amount` | `9755.50` | = formula A; recomputed for variable rate |
| `interest_rate` | `6.5` | snapshot per installment (matters for variable) |
| `currency_code` | `RSD` | = loan currency |
| `expected_date` | `2026-04-13` | due date; `actual_date` null until paid |
| `status` | `unpaid`/`paid`/`overdue` | `unpaid`→`paid` on success, `unpaid`→`overdue` on failed deduction |

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| Account: create current (RSD-only) | TC-C2-ACC-001, TC-C2-ACC-002 | account_test.go::TestAccount_CreateCurrentAccount, TestAccount_CreateWithInvalidKind | covered |
| Account: create foreign (all 7 FX currencies) | TC-C2-ACC-003 | account_test.go::TestAccount_CreateForeignAccount, TestAccount_CreateForeignPersonalUSD | covered |
| Account: initial balance | TC-C2-ACC-004 | account_test.go::TestAccount_CreateWithInitialBalance | covered |
| Account: all personal subtypes + maintenance fee | TC-C2-ACC-005a..f | — | partial |
| Account: all business subtypes (DOO/AD/Fondacija) | TC-C2-ACC-006a..c | — | partial |
| Account: auto-card checkbox (on/off) | TC-C2-ACC-007, TC-C2-ACC-008 | — | partial |
| Account: owner existing vs new-client-inline | TC-C2-ACC-009 | client_test.go::TestClient_CreateMultipleClients | covered |
| Account: list/filter (name/number) + pagination | TC-C2-ACC-010, TC-C2-ACC-011 | account_test.go::TestAccount_ListAllAccounts, TestAccount_GetByAccountNumber, TestAccount_ListWithPagination | covered |
| Account: client /me list (active, sorted) | TC-C2-ACC-012 | — | partial |
| Account: detail personal vs business | TC-C2-ACC-013, TC-C2-ACC-014 | account_test.go::TestAccount_GetAccountByID, TestAccount_GetNonExistent | partial |
| Account: update name (uniqueness) | TC-C2-ACC-015 | account_test.go::TestAccount_UpdateName | covered |
| Account: update limits + verification | TC-C2-ACC-016 | account_test.go::TestAccount_UpdateLimits, TestAccount_UpdateLimitsNegativeRejected | covered |
| Account: status active/inactive | TC-C2-ACC-017 | account_test.go::TestAccount_UpdateStatus, TestAccount_DeactivateNonExistent | covered |
| Account: list currencies | TC-C2-ACC-018 | account_test.go::TestAccount_ListCurrencies | covered |
| Account-number format (bank prefix + check digit) | TC-C2-ACC-001 | — | partial |
| Company: create (firma) | TC-C2-CMP-001 | account_test.go::TestAccount_CreateCompany | covered |
| Bank accounts: list/create/delete | TC-C2-BANK-001 | account_test.go::TestAccount_BankAccountCRUD, TestAccount_DeleteBankAccount, TestAccount_BankAccountCreateForeignEUR | covered |
| Bank accounts: delete guard (>=1 RSD + >=1 FX) | TC-C2-BANK-002 | — | partial |
| Bank accounts: ledger activity (commission credit) | TC-C2-BANK-003 | bank_account_activity_test.go::TestBankAccountActivity_EmployeeCanView, TestBankAccountActivity_RejectsClientAccount | covered |
| Payment: same-currency to different client | TC-C2-PAY-001 | payment_test.go::TestPayment_EndToEnd | covered |
| Payment: fee stacking 0.1%+5% to bank RSD | TC-C2-PAY-002, TC-C2-PAY-003, TC-C2-PAY-004 | payment_test.go::TestPayment_WithFee | covered |
| Payment: insufficient funds | TC-C2-PAY-005 | payment_test.go::TestPayment_InsufficientBalance | covered |
| Payment: wrong-owner source (403) | TC-C2-PAY-006 | — | partial |
| Payment: wrong verification code | TC-C2-PAY-007 | payment_test.go::TestPayment_WrongOTPCodeRejected | covered |
| Payment: inactive/nonexistent recipient | TC-C2-PAY-008 | — | partial |
| Payment: over client daily/monthly limit | TC-C2-PAY-009 | — | partial |
| Payment: preview (fee/limit info) | TC-C2-PAY-010 | payment_test.go::TestPayment_PreviewAndStatus | covered |
| Payment: history + status filters | TC-C2-PAY-011 | payment_test.go::TestPayment_EmployeeCanReadPayments, TestPayment_PreviewAndStatus | covered |
| Payment: Kafka notifications | TC-C2-PAY-012 | payment_test.go::TestPayment_KafkaEventsOnPayment | covered |
| Payment: unauthenticated rejected | TC-C2-PAY-013 | payment_test.go::TestPayment_UnauthenticatedCannotCreatePayment | covered |
| Payment: cross-currency FX (different clients) | TC-C2-PAY-020 | — | NO-ENDPOINT |
| Transfer: same-currency own accounts | TC-C2-TRF-001 | transfer_test.go::TestTransfer_SameCurrency_EndToEnd, TestTransfer_InsufficientBalance | covered |
| Transfer: cross-currency FX + commission to bank | TC-C2-TRF-002 | transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR | covered |
| Transfer: preview (rate + commission) | TC-C2-TRF-003 | — | partial |
| Transfer: intra-client guard (reject other client) | TC-C2-TRF-004 | — | partial |
| Transfer: history + listings | TC-C2-TRF-005 | transfer_test.go::TestTransfer_EmployeeCanReadTransfers, TestTransfer_ListByClient, TestTransfer_UnauthenticatedCannotCreateTransfer | covered |
| Transfer: reserved-funds semantics (internal=0) | TC-C2-TRF-006 | — | partial |
| Payment recipients: CRUD + ownership | TC-C2-RCP-001 | transfer_test.go::TestTransfer_PaymentRecipientCRUD | covered |
| Menjačnica: kursna lista | TC-C2-FX-001 | exchange_rate_test.go::TestExchangeRates_ListAll | covered |
| Menjačnica: specific pair | TC-C2-FX-002 | exchange_rate_test.go::TestExchangeRates_GetSpecific | covered |
| Menjačnica: equivalence calculator | TC-C2-FX-003 | exchange_rate_test.go::TestExchangeRates_Calculate, _MissingFields, _InvalidAmount, _UnsupportedCurrency | covered |
| Menjačnica: 2-leg via RSD + per-leg commission | TC-C2-FX-004 | transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR | partial |
| Card: issue per brand (visa/mc/dina/amex) | TC-C2-CARD-001a..d | card_test.go::TestCard_CreateAllBrands, TestCard_AllBrandsDebitAndCredit, TestCard_CreateWithInvalidBrand | covered |
| Card: max 2 physical per personal account | TC-C2-CARD-002 | — | partial |
| Card: max 1 per authorized person per business acct | TC-C2-CARD-003 | — | partial |
| Card: block/unblock/deactivate (employee) | TC-C2-CARD-004 | card_test.go::TestCard_BlockUnblockDeactivate | covered |
| Card: client blocks own, employee unblocks | TC-C2-CARD-005 | — | partial |
| Card: deactivated cannot be reactivated | TC-C2-CARD-006 | — | partial |
| Card: virtual single_use | TC-C2-CARD-007 | card_test.go::TestCard_VirtualCardSingleUse, TestCard_VirtualSingleUseWithClientAuth, TestCard_VirtualInvalidUsageType | covered |
| Card: virtual multi_use + max_uses boundary | TC-C2-CARD-008 | card_test.go::TestCard_VirtualMultiUseWithClientAuth | covered |
| Card: virtual unlimited | TC-C2-CARD-009 | card_test.go::TestCard_VirtualUnlimitedWithClientAuth | NO-ENDPOINT |
| Card: PIN set + verify | TC-C2-CARD-010 | card_test.go::TestCard_PINManagement, TestCard_PINSetAndVerify, TestCard_ChangePin | covered |
| Card: PIN lock after 3 fails | TC-C2-CARD-011 | card_test.go::TestCard_PINWrongThreeTimes_LocksCard | covered |
| Card: temporary block + auto-expiry | TC-C2-CARD-012 | card_test.go::TestCard_TemporaryBlockWithExpiry | covered |
| Card: list (masked, by account/client) | TC-C2-CARD-013 | card_test.go::TestCard_GetCard, TestCard_ListByAccount | covered |
| Card: authorized person (business) | TC-C2-CARD-014 | — | partial |
| Card: multi-currency spend fee (2%+0.5%) | TC-C2-CARD-015 | — | NO-ENDPOINT |
| Card request: client request -> employee approve | TC-C2-CREQ-001 | card_request_test.go::TestCardRequest_FullLifecycle, TestCardRequest_EmployeeApproveAndRejectFlow | covered |
| Card request: reject with reason | TC-C2-CREQ-002 | card_request_test.go::TestCardRequest_RejectRequiresReason, TestCardRequest_RejectNonExistentRequest | covered |
| Card request: auth/role boundaries + filters | TC-C2-CREQ-003 | card_request_test.go::TestCardRequest_UnauthenticatedCannotCreateRequest, TestCardRequest_EmployeeCannotCreateRequest, TestCardRequest_EmployeeCanListRequests, TestCardRequest_EmployeeCanFilterByStatus, TestCardRequest_InvalidStatusFilterRejected | covered |
| Card request: client tracks own (/me) | TC-C2-CREQ-004 | card_request_test.go::TestCardRequest_GetNonExistentRequest | covered |
| Loan: submit all types x fixed | TC-C2-LOAN-001a..e | loan_test.go::TestLoan_AllLoanTypes, TestLoan_FullLifecycle, TestLoan_UnauthenticatedCannotCreateLoanRequest | covered |
| Loan: submit variable interest | TC-C2-LOAN-002 | loan_test.go::TestLoan_FullLifecycle | covered |
| Loan: approve -> create + disburse (bank debit/client credit) | TC-C2-LOAN-003 | loan_test.go::TestLoan_FullLifecycle, loan_disbursement_test.go::TestLoanDisbursement_Saga_HappyPath, TestLoan_ApproveNonExistentRequest | covered |
| Loan: approval limit gate (MaxLoanApprovalAmount) | TC-C2-LOAN-004 | — | partial |
| Loan: insufficient bank liquidity -> 409 | TC-C2-LOAN-005 | loan_disbursement_test.go::TestLoanDisbursement_BankInsufficientLiquidity_Returns409 | covered |
| Loan: reject request | TC-C2-LOAN-006 | loan_test.go::TestLoan_RejectLoanRequest, TestLoan_RejectNonExistentRequest | covered |
| Loan: currency must match account | TC-C2-LOAN-007 | — | partial |
| Loan: registry & detail views | TC-C2-LOAN-008 | loan_test.go::TestLoan_ListAllLoans, TestLoan_ListLoansByClient, TestLoan_ListLoanRequests, TestLoan_ListLoanRequestsByClient, TestLoan_GetMyLoanRequest_SelfRoute, TestLoan_GetNonExistentLoan | covered |
| Loan: variable-rate recalculation (tier apply) | TC-C2-LOAN-009 | — | partial |
| Loan: interest tiers + bank margins config | TC-C2-LOAN-010 | — | partial |
| Installment: schedule + formula on creation | TC-C2-INST-001 | loan_test.go::TestLoan_FullLifecycle | partial |
| Installment: rate-tier boundary by amount | TC-C2-INST-002 | — | partial |
| Installment: auto monthly deduction success | TC-C2-INST-005 | — | NO-ENDPOINT |
| Installment: auto deduction failure (overdue + notice) | TC-C2-INST-006 | — | NO-ENDPOINT |
| Transfer fees: rule CRUD + stacking | TC-C2-FEE-001 | payment_test.go::TestPayment_WithFee | partial |
| Defense Provera 1 (business acct, no card, new client+company, x2 FX) | TC-C2-E2E-P1 | — | partial |
| Defense Provera 2 (personal acct + card, new client, x2 FX) | TC-C2-E2E-P2 | — | partial |
| Defense Provera 3 (transfer same+diff currency, bank commission) | TC-C2-E2E-P3 | transfer_test.go::TestTransfer_SameCurrency_EndToEnd, TestTransfer_CrossCurrencyRSDtoEUR | partial |
| Defense Provera 4 (payment same+diff currency, bank commission) | TC-C2-E2E-P4 | payment_test.go::TestPayment_WithFee | partial |
| Defense Provera 5 (loan request -> approve -> disburse) | TC-C2-E2E-P5 | loan_disbursement_test.go::TestLoanDisbursement_Saga_HappyPath | covered |
| Defense Provera 6 (card request -> auto -> limit -> block -> deactivate) | TC-C2-E2E-P6 | card_test.go::TestCard_BlockUnblockDeactivate | partial |
| Defense Provera 7 (menjacnica equivalent value) | TC-C2-E2E-P7 | exchange_rate_test.go::TestExchangeRates_Calculate | covered |
```

**Summary:** ~95 test cases across 13 areas (accounts, companies, bank accounts, payments, transfers,
recipients, menjačnica, cards, card requests, loans, installments, fees, + 7 defense-flow E2E scenarios),
each with positive and negative siblings. **Notable gaps:** (1) cross-currency **payment** between
different clients with FX is unimplemented — payments are single-currency, FX lives only in transfers
(TC-C2-PAY-020, NO-ENDPOINT); (2) virtual `unlimited` usage_type is not creatable via the API though it
exists in the enum (TC-C2-CARD-009); (3) no card-spend/POS endpoint, so multi-currency card-spend fees
(2% + 0.5%) cannot be exercised (TC-C2-CARD-015); (4) automatic installment deduction (success/overdue)
is a cron with no API trigger, untested by the Go suite (TC-C2-INST-005/006); (5) loan-type naming in
`REST_API_v3.md` example (`PERSONAL/MORTGAGE/...`) is stale — the gateway actually validates
`cash/housing/auto/refinancing/student` (D1).
