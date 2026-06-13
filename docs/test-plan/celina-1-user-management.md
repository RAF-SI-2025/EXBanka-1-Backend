# Celina 1 — Upravljanje korisnicima (User Management) — Test Cases

**Scope.** Authentication & authorization for employees and clients: the login matrix,
the brute-force lockout boundary, password reset, account activation, employee CRUD
(admin-only portal), roles & per-employee permissions (RBAC), client field validation,
and the access/refresh-token lifecycle (refresh, logout, revoke-session, revoke-all,
session list, login history).

**Spec references.** Celina 1 `docs/bank-requirements/Celina 1 2026.docx.md` (whole document);
`docs/Specification.md` §6 (auth/roles/permissions), §17 (routes), §18 (entities:
`Account`, `AccountLock`, `LoginAttempt`, `Employee`, `Client`), §20 (enums), §21
(business rules, line 2513: "5 failed login attempts → 30-min lockout");
`docs/api/REST_API_v3.md` §1 (auth), §2 (employees), §3 (roles/permissions), §clients,
§36 (sessions & login history); handlers `api-gateway/internal/handler/{auth,employee,client,role,session}_handler.go`;
service logic `auth-service/internal/service/{auth_service,auth_account}.go`,
`auth-service/internal/repository/login_attempt_repository.go`,
`user-service/internal/service/{employee_service,jmbg_validator}.go`.
Defense flows: `Banka 2025 - odbrana flow.docx.md` §1 (Provera 1, Provera 2).
E2E seeds: `Banka 2025 - E2E testovi.docx.md` (Feature: Autentifikacija korisnika; Feature: Kreiranje i upravljanje zaposlenima).

All routes are under `http://localhost:8080/api/v3`. Error envelope is
`{"error":{"code","message","details?}}`. Standard error codes used below:
`validation_error`/400, `unauthorized`/401, `token_expired`/401, `forbidden`/403,
`not_found`/404, `conflict`/409, `business_rule_violation`/409, `rate_limited`/429,
`internal_error`/500.

---

## ⚠️ Documented threshold conflicts (test the IMPLEMENTED value)

The brute-force and reset-link parameters disagree across the three source documents.
**The TCs below assert the values that are actually implemented in `auth-service`** and
flag each conflict so graders/cohorts can reconcile.

| Parameter | Celina 1 doc | E2E doc | Spec §21 | **IMPLEMENTED (auth-service)** | TC asserts |
|---|---|---|---|---|---|
| Failed attempts before lock | **5** | 3 | **5** | **5** (`maxFailedAttempts=5`, `auth_service.go:190`) | 5 |
| Lockout duration | 10 min | — | **30 min** | **30 min** (`lockoutDuration`, `auth_service.go:192`) | 30 min |
| Failure-count window | — | — | — | **15 min** (`lockoutWindow`, `auth_service.go:191`) | 15 min |
| Lock-notification email | required | — | — | **NOT IMPLEMENTED** (no `EmailTypeAccountLocked`, no template) | gap (TC-C1-LOCK-050) |
| Reset unlocks lock + resets counter | required | — | — | **PARTIAL** — `ResetPassword` resets password + revokes sessions but **does not call `UnlockAccount`**, so an active `AccountLock` row persists until expiry | gap (TC-C1-LOCK-051) |
| Reset-link expiry | — | **15 min** | — | **1 hour** (`auth_account.go:170`) | 1 hour |
| Activation-link expiry | — | 24h | 24h (§ tokens) | **24 hours** (`auth_account.go:92`) | 24 hours |

> Note on the gateway rate limiter: `POST /auth/login` and `POST /auth/password/reset-request`
> sit behind a strict per-IP bucket (`RateLimit.LoginPer5Min` / `ResetPer5Min`,
> `router_v3.go:86,89`). When that bucket is configured low, the *gateway* returns
> `429 rate_limited` **before** the service-level lockout is reached. The lockout TCs
> assume the bucket is disabled or set high enough (test default) so the service path is exercised.

---

## A. Login matrix — `TC-C1-LOGIN-*`

#### TC-C1-LOGIN-001 · Employee login success (POSITIVE)
- **Feature:** Login — autentifikacija zaposlenog · **Spec:** Celina 1 §Login · **Existing test:** test-app/workflows/auth_test.go::TestAuth_LoginWithValidCredentials
- **Actor:** employee (seed admin)
- **Preconditions:** seed admin account active; password `AdminAdmin2026!.`
- **Request:** `POST /api/v3/auth/login`
  - Auth: none
  - Body: `{"email":"admin@exbanka.com","password":"AdminAdmin2026!."}`
- **Verification:** n/a
- **Expected:** `200` · body `{access_token, refresh_token}` both non-empty · side-effects: a `LoginAttempt{success:true}` row recorded; a session row created (visible later via `GET /me/sessions`); access-token claims carry `principal_type:"employee"`, `system_type:"employee"`, populated `roles`/`permissions`, and `sid`.
- **Negative siblings:** wrong password → TC-C1-LOGIN-010; unknown email → TC-C1-LOGIN-011.

#### TC-C1-LOGIN-002 · Client login success + system_type routing (POSITIVE)
- **Feature:** Login — autentifikacija klijenta + system_type routing · **Spec:** Celina 1 §Login; Spec §6 (system_type) · **Existing test:** test-app/workflows/wf_client_stock_banking_test.go (client login helper); helpers_test.go::loginAsClient
- **Actor:** client
- **Preconditions:** a client created (TC-C1-EMP/CLI fixtures) and its auth Account provisioned (via `client.client-created` Kafka → auth `client_consumer`) and activated.
- **Request:** `POST /api/v3/auth/login`
  - Auth: none
  - Body: `{"email":"<client-email>","password":"<client-pass>"}`
- **Verification:** n/a
- **Expected:** `200` · `{access_token, refresh_token}` · side-effects: token claims `principal_type:"client"`, `system_type:"client"`, `role:"client"`. The client token is admitted on `/api/me/*` (`AnyAuthMiddleware`) but **rejected** on employee routes like `GET /api/v3/employees` (→ 403 forbidden). This proves `system_type` routing.
- **Negative siblings:** client token on employee route → 403 (see TC-C1-RBAC-030); employee token on `/me` client routes → admitted (employee can act as bank).

#### TC-C1-LOGIN-010 · Login wrong password (NEGATIVE)
- **Feature:** Login — pogrešna lozinka · **Spec:** Celina 1 §Login · **Existing test:** test-app/workflows/auth_test.go::TestAuth_LoginWithInvalidPassword; auth_login_failure_modes_test.go::TestLogin_Wrong_Password_Returns_401_Unauthorized
- **Actor:** employee
- **Preconditions:** active account exists.
- **Request:** `POST /api/v3/auth/login` · Body: `{"email":"admin@exbanka.com","password":"WrongPass99"}`
- **Expected:** `401` · `error.code = unauthorized` ("invalid credentials") · side-effects: `LoginAttempt{success:false}` recorded; failure counter for the email increments. Message is the SAME as unknown-email to prevent enumeration.
- **Negative siblings:** unknown email → TC-C1-LOGIN-011 (identical 401 body).

#### TC-C1-LOGIN-011 · Login non-existent email (NEGATIVE)
- **Feature:** Login — nepostojeći email (anti-enumeration) · **Spec:** Celina 1 §Login · **Existing test:** test-app/workflows/auth_test.go::TestAuth_LoginWithNonexistentEmail
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/login` · Body: `{"email":"nobody@nowhere.test","password":"Whatever12"}`
- **Expected:** `401` · `error.code = unauthorized` ("invalid credentials") · side-effects: `LoginAttempt{success:false}` recorded under that email (drives lockout even for unknown emails). Response body is byte-identical to TC-C1-LOGIN-010 (no enumeration leak).
- **Negative siblings:** —

#### TC-C1-LOGIN-012 · Login inactive/disabled account (NEGATIVE)
- **Feature:** Login — deaktiviran nalog · **Spec:** Celina 1 §Portal (deaktivacija); §Login · **Existing test:** test-app/workflows/auth_login_failure_modes_test.go::TestLogin_Pending_Account_Returns_409_BusinessRule (sibling), role_permission_revocation_test.go (deactivation path)
- **Actor:** employee whose account was disabled (`PUT /employees/:id {active:false}`)
- **Preconditions:** target account status = `disabled`.
- **Request:** `POST /api/v3/auth/login` · Body valid credentials for the disabled account.
- **Expected:** `409` · `error.code = business_rule_violation` (`ErrAccountDisabled` = FailedPrecondition) · side-effects: failed `LoginAttempt` recorded; no tokens issued.
- **Negative siblings:** pending (never activated) account → TC-C1-LOGIN-013.

#### TC-C1-LOGIN-013 · Login pending (not yet activated) account (NEGATIVE)
- **Feature:** Login — nalog nije aktiviran · **Spec:** Celina 1 §Kreiranje i aktivacija · **Existing test:** test-app/workflows/auth_login_failure_modes_test.go::TestLogin_Pending_Account_Returns_409_BusinessRule
- **Actor:** newly-created employee (no password set yet)
- **Preconditions:** employee created via `POST /employees` but activation token not consumed.
- **Request:** `POST /api/v3/auth/login` · Body: `{"email":"<new-emp>","password":"anything"}`
- **Expected:** `409` · `error.code = business_rule_violation` (`ErrAccountPending`) · side-effects: failed attempt recorded.
- **Negative siblings:** —

#### TC-C1-LOGIN-014 · Login missing/empty fields (NEGATIVE)
- **Feature:** Login — validacija unosa · **Spec:** Celina 1 §Login · **Existing test:** test-app/workflows/auth_test.go::TestAuth_LoginWithEmptyFields; auth_login_failure_modes_test.go::TestLogin_Missing_Email_Returns_401_Unauthorized
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/login` · Body variants: `{}`, `{"email":"admin@exbanka.com"}`, `{"password":"x"}`, `{"email":"not-an-email","password":"x"}`
- **Expected:** `400` · `error.code = validation_error` for missing `email`/`password` and for malformed email (gin `binding:"required,email"`). No `LoginAttempt` row. (Note: the existing failure-modes test asserts 401 for a missing *email* because some inputs reach the service path; the gateway binding returns 400 when a required field is absent — assert 400 for the empty-body and malformed-email variants.)
- **Negative siblings:** —

---

## B. Brute-force lockout boundary — `TC-C1-LOCK-*`

#### TC-C1-LOCK-040 · 4th consecutive failure still allowed (NEGATIVE boundary, lower side)
- **Feature:** Brute-force — 4. pokušaj dozvoljen · **Spec:** Celina 1 §Bezbednosni mehanizam; Spec §21 · **Existing test:** test-app/workflows/auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden (drives the same counter)
- **Actor:** unauthenticated, single email
- **Preconditions:** fresh email (no prior failures in the 15-min window); gateway login rate-limit disabled/high.
- **Request:** `POST /api/v3/auth/login` ×4 with wrong password for an existing active account.
- **Expected:** each of attempts 1–4 → `401 unauthorized` (invalid credentials), **NOT** locked. After the 4th, `remaining = 1`; no `AccountLock` row exists; a 5th attempt with the CORRECT password would still succeed (account not yet locked).
- **Negative siblings:** 5th wrong attempt locks → TC-C1-LOCK-041.

#### TC-C1-LOCK-041 · 5th consecutive failure locks the account (NEGATIVE boundary, upper side)
- **Feature:** Brute-force — 5. pokušaj zaključava · **Spec:** Celina 1 §Bezbednosni mehanizam; Spec §21 · **Existing test:** test-app/workflows/auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden
- **Actor:** unauthenticated, single email
- **Preconditions:** continue from TC-C1-LOCK-040 (4 failures present) within the 15-min window.
- **Request:** 5th `POST /api/v3/auth/login` with wrong password.
- **Expected:** `403` · `error.code = forbidden` (`ErrAccountLocked` = PermissionDenied) · side-effects: an `AccountLock{email, locked_at, expires_at = now+30m, unlocked_at:null}` row is created; counter at 5.
- **Negative siblings:** login while locked (even with correct password) → TC-C1-LOCK-042.

#### TC-C1-LOCK-042 · Login blocked while locked, even with correct password (NEGATIVE)
- **Feature:** Brute-force — zaključan nalog blokira login · **Spec:** Celina 1 §Bezbednosni mehanizam · **Existing test:** test-app/workflows/auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden (6th attempt)
- **Actor:** unauthenticated
- **Preconditions:** account locked (TC-C1-LOCK-041); within the 30-min lock.
- **Request:** `POST /api/v3/auth/login` with the **correct** password.
- **Expected:** `403` · `error.code = forbidden` ("account locked", `GetActiveLock` short-circuits before password check) · side-effects: no new failed-attempt row is appended (the active-lock check returns before `RecordFailureAndCheckLock`); no tokens.
- **Negative siblings:** after 30-min expiry, correct password succeeds → TC-C1-LOCK-043.

#### TC-C1-LOCK-043 · Lock auto-expires after 30 minutes (POSITIVE recovery)
- **Feature:** Brute-force — istek zaključavanja · **Spec:** Celina 1 (10 min) / Spec §21 (30 min) — assert IMPLEMENTED 30 min · **Existing test:** —
- **Actor:** unauthenticated
- **Preconditions:** account locked; lock `expires_at` in the past (advance clock / wait, or in tests update the `account_locks` row's `expires_at`).
- **Request:** `POST /api/v3/auth/login` with correct password after expiry.
- **Expected:** `200` · `{access_token, refresh_token}` · side-effects: `GetActiveLock` finds no live lock; login proceeds. Confirms the implemented duration is **30 min** (NOT the 10 min in the Celina-1 doc).
- **Negative siblings:** —

#### TC-C1-LOCK-050 · Email sent to user on lock (NEGATIVE — documented but NOT IMPLEMENTED)
- **Feature:** Brute-force — email obaveštenje o zaključavanju · **Spec:** Celina 1 §Bezbednosni mehanizam ("Sistem šalje email korisniku nakon što nalog bude zaključan") · **Existing test:** — (NO-ENDPOINT)
- **Actor:** system
- **Preconditions:** account just locked (TC-C1-LOCK-041).
- **Verification:** scan Kafka topic `notification.send-email` for an account-locked message.
- **Expected (per spec):** a `SendEmailMessage` with a lock-notification email type (containing a reset-password link) is published.
- **ACTUAL:** **no such message is emitted** — there is no `EmailTypeAccountLocked` in `contract/kafka/messages.go` and no template in `notification-service`. **Mark NO-ENDPOINT / gap.** Lockout works, the user is simply not notified.
- **Negative siblings:** —

#### TC-C1-LOCK-051 · Password reset unlocks account + resets failed counter (PARTIAL — documented, only partially implemented)
- **Feature:** Brute-force — reset otključava nalog i resetuje brojač · **Spec:** Celina 1 §Bezbednosni mehanizam ("Reset lozinke: otključava nalog i resetuje broj neuspešnih pokušaja") · **Existing test:** test-app/workflows/auth_test.go::TestAuth_PasswordResetRequest (request leg only)
- **Actor:** locked user → reset flow
- **Preconditions:** account locked (TC-C1-LOCK-041); a valid reset token obtained.
- **Request:** `POST /api/v3/auth/password/reset` with new valid password, then immediately `POST /api/v3/auth/login` with the new password (still inside the 30-min lock window).
- **Expected (per spec):** reset clears the active `AccountLock` (and failed counter), so the immediate login → `200`.
- **ACTUAL:** `ResetPassword` (`auth_account.go:184`) updates the password, revokes all sessions, and publishes a `password_changed` notification, but **does not call `LoginAttemptRepository.UnlockAccount`** — the `AccountLock` row persists. Therefore the immediate post-reset login still returns **`403 forbidden`** until the 30-min lock expires. **Mark PARTIAL / gap** (`UnlockAccount` exists in the repo but has no caller). Assert the actual 403 and note the spec-divergence.
- **Negative siblings:** —

---

## C. Password reset — `TC-C1-PWD-*`

#### TC-C1-PWD-060 · Request password reset for existing email (POSITIVE)
- **Feature:** Reset lozinke — zahtev šalje email link · **Spec:** Celina 1 §Login (reset preko email-a) · **Existing test:** test-app/workflows/auth_test.go::TestAuth_PasswordResetRequest
- **Actor:** unauthenticated
- **Preconditions:** an account exists for the email.
- **Request:** `POST /api/v3/auth/password/reset-request` · Body: `{"email":"admin@exbanka.com"}`
- **Verification:** scan Kafka `notification.send-email` for `EmailType=PASSWORD_RESET` with a `link` = `<FRONTEND_BASE_URL>/reset-password?token=<token>`.
- **Expected:** `200` · `{"message":"if the email exists, a reset link has been sent"}` · side-effects: a `PasswordResetToken{expires_at = now+1h}` row created; one `notification.send-email` event published.
- **Negative siblings:** unknown email → TC-C1-PWD-061 (still 200, no email).

#### TC-C1-PWD-061 · Request reset for unknown email (NEGATIVE — anti-enumeration)
- **Feature:** Reset lozinke — nepostojeći email · **Spec:** Celina 1 §Login · **Existing test:** test-app/workflows/auth_test.go::TestAuth_PasswordResetRequest (asserts always-200 shape)
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/password/reset-request` · Body: `{"email":"nobody@nowhere.test"}`
- **Expected:** `200` · identical message body · side-effects: **no** token, **no** Kafka email (does not reveal that the email is unknown).
- **Negative siblings:** missing/malformed email → 400 `validation_error`.

#### TC-C1-PWD-062 · Reset password with valid token (POSITIVE)
- **Feature:** Reset lozinke — postavljanje nove lozinke · **Spec:** Celina 1 §Login · **Existing test:** — (only invalid-token covered)
- **Actor:** user with a valid reset token
- **Preconditions:** valid unexpired `PasswordResetToken`.
- **Request:** `POST /api/v3/auth/password/reset` · Body: `{"token":"<valid>","new_password":"NewPass12","confirm_password":"NewPass12"}`
- **Expected:** `200` · `{"message":"password reset successfully"}` · side-effects: password hash updated; token marked used; **all sessions revoked**; `general-notification` `password_changed` published; subsequent login with the new password → 200, with the old password → 401.
- **Negative siblings:** expired token → TC-C1-PWD-063; mismatch → TC-C1-PWD-064; weak password → §PWD-constraints matrix; reused (already-used) token → 401 `unauthorized`.

#### TC-C1-PWD-063 · Reset with expired/invalid token (NEGATIVE)
- **Feature:** Reset lozinke — istekao/nevažeći token · **Spec:** Celina 1; reset token TTL = **1h** (implemented; E2E doc says 15 min — conflict) · **Existing test:** test-app/workflows/auth_test.go::TestAuth_ActivateAccountInvalidToken (sibling invalid-token shape)
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/password/reset` with an unknown token, and separately with a token whose `expires_at` is in the past.
- **Expected:** unknown token → `401 unauthorized` (`ErrInvalidToken`); expired token → `401 unauthorized` (`ErrTokenExpired`). No password change.
- **Negative siblings:** —

#### TC-C1-PWD-064 · Reset password / confirm mismatch (NEGATIVE)
- **Feature:** Reset lozinke — lozinke se ne poklapaju · **Spec:** Celina 1 · **Existing test:** —
- **Actor:** user with valid token
- **Request:** `POST /api/v3/auth/password/reset` · Body: `{"token":"<valid>","new_password":"NewPass12","confirm_password":"NewPass34"}`
- **Expected:** `400` · `error.code = validation_error` (`ErrPasswordsDoNotMatch`) · side-effects: token NOT consumed (still usable).
- **Negative siblings:** weak new password → §PWD-constraints matrix (TC-C1-PWD-070..074).

#### Password-constraint sub-cases (shared by reset AND activation) — `TC-C1-PWD-070..074`
Password rules (`validatePassword`, `auth_account.go:347`): **8–32 chars, ≥2 digits, ≥1 uppercase, ≥1 lowercase.** Each violation → `400 validation_error` (`ErrPasswordValidation`). Exercise on both `POST /auth/password/reset` and `POST /auth/activate`.

| TC | new_password | Violated rule | Expected |
|---|---|---|---|
| TC-C1-PWD-070 | `Ab12` | < 8 chars | 400 `validation_error` |
| TC-C1-PWD-071 | `Abcdefg1Abcdefg1Abcdefg1Abcdefg1X` (33) | > 32 chars | 400 `validation_error` |
| TC-C1-PWD-072 | `Abcdefg1` | only 1 digit (< 2) | 400 `validation_error` |
| TC-C1-PWD-073 | `abcdef12` | no uppercase | 400 `validation_error` |
| TC-C1-PWD-074 | `ABCDEF12` | no lowercase | 400 `validation_error` |
| (positive)   | `NewPass12` | none | 200 |

Existing unit coverage: `auth-service/internal/service/auth_service_test.go` (no-uppercase / no-lowercase / one-digit / no-digits). Integration-level positive: TC-C1-PWD-062 / TC-C1-ACT-080.

---

## D. Account activation — `TC-C1-ACT-*`

#### TC-C1-ACT-080 · Activate new employee account with valid token (POSITIVE)
- **Feature:** Aktivacija naloga zaposlenog · **Spec:** Celina 1 §Kreiranje i aktivacija; defense Provera 1 (step 2) · **Existing test:** — (full activation flow exercised via mobile in mobile_auth_test.go::TestMobileAuth_ActivateFullFlow; browser-activation flow not yet in workflows)
- **Actor:** newly-created employee
- **Preconditions:** employee created (TC-C1-EMP-100); activation token captured from Kafka `notification.send-email` (`EmailType=ACTIVATION`, `link=<base>/activate?token=<t>`).
- **Request:** `POST /api/v3/auth/activate` · Body: `{"token":"<valid>","password":"MyFirst12Pass","confirm_password":"MyFirst12Pass"}`
- **Verification:** after activation, scan Kafka for a `CONFIRMATION` email.
- **Expected:** `200` · `{"message":"account activated successfully"}` · side-effects: account status `pending → active`; password set; activation token marked used; `CONFIRMATION` email published; the employee can now log in (TC-C1-LOGIN-001 pattern).
- **Negative siblings:** invalid/expired token → TC-C1-ACT-081; weak/mismatched password → §PWD matrix; replayed token → 401.

#### TC-C1-ACT-081 · Activate with invalid/expired token (NEGATIVE)
- **Feature:** Aktivacija — nevažeći/istekao token (TTL = **24h**) · **Spec:** Celina 1; E2E "Link za aktivaciju ističe nakon 24h" · **Existing test:** test-app/workflows/auth_test.go::TestAuth_ActivateAccountInvalidToken
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/activate` with a bogus token; and separately a token whose `expires_at` < now.
- **Expected:** bogus → `401 unauthorized` (`ErrInvalidToken`); expired → `401 unauthorized` (`ErrTokenExpired`). Account stays `pending`.
- **Negative siblings:** —

#### TC-C1-ACT-082 · Resend activation email (POSITIVE / no-op when already active)
- **Feature:** Aktivacija — ponovno slanje email-a · **Spec:** REST §1 (resend-activation) · **Existing test:** —
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/auth/resend-activation` · Body: `{"email":"<pending-emp>"}`; then again for an already-active account.
- **Expected:** both → `200` with the always-200 anti-enumeration message. For a pending account, a NEW `ACTIVATION` email + token is published; for an active/unknown account it is a silent no-op (no Kafka email).
- **Negative siblings:** missing/malformed email → 400 `validation_error`.

---

## E. Employee CRUD (admin portal) — `TC-C1-EMP-*`

> Portal is admin-only. Read = `employees.read.all`; create = `employees.create.any`; update = `employees.update.any`. Admins may view both admins and basic employees but may **edit only non-admin** employees.

#### TC-C1-EMP-100 · Admin creates employee — all fields except password (POSITIVE; defense Provera 1)
- **Feature:** Kreiranje zaposlenog (admin unosi sva polja osim password-a; default aktivan) · **Spec:** Celina 1 §Kreiranje i aktivacija; defense Provera 1 · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_CreateWithBasicRole / WithAgentRole / WithSupervisorRole / WithAdminRole
- **Actor:** admin
- **Request:** `POST /api/v3/employees`
  - Auth: `Bearer <admin>`
  - Body: `{"first_name":"Petar","last_name":"Petrović","date_of_birth":631152000,"gender":"M","email":"petar.p@exbanka.com","phone":"+381645555555","address":"Njegoševa 25","jmbg":"0101990710006","username":"petar90","position":"Menadžer","department":"Finansije","role":"EmployeeBasic"}`
- **Verification:** scan Kafka `notification.send-email` for the `ACTIVATION` email (24h link).
- **Expected:** `201` · body echoes all fields, `active:false` (pending until activation), `role:"EmployeeBasic"` · side-effects: `Employee` row created; auth `Account` created `pending`; `user.employee-created` published → auth mints activation token → `ACTIVATION` email published. No password is accepted on this endpoint.
- **Negative siblings:** duplicate email → TC-C1-EMP-101; bad JMBG → TC-C1-EMP-102; missing required field → 400; non-admin caller → TC-C1-EMP-103.

#### TC-C1-EMP-100b · Create employee as inactive (POSITIVE — optional inactive at create)
- **Feature:** Kreiranje neaktivnog zaposlenog · **Spec:** Celina 1 ("moguće je napraviti i korisnika koji nije aktivan") · **Existing test:** — (NO direct create-time `active` flag)
- **Actor:** admin
- **Preconditions:** —
- **Request:** `POST /api/v3/employees` (as TC-C1-EMP-100). There is **no `active` field on `createEmployeeRequest`** — new accounts always start `pending`/inactive until activated.
- **Expected:** the spec's "create an inactive employee" is satisfied implicitly: every created employee is `active:false` until the activation password is set. An explicit create-time active/inactive toggle is **NO-ENDPOINT** (achieved post-create via `PUT /employees/:id {active:...}`). Mark NO-ENDPOINT for the create-time toggle; covered for the resulting inactive state.
- **Negative siblings:** —

#### TC-C1-EMP-101 · Create employee duplicate email (NEGATIVE — email unique)
- **Feature:** Unikatnost naloga (email unique) · **Spec:** Celina 1 §Validacija; §Kreiranje · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_CreateWithDuplicateEmail
- **Actor:** admin
- **Preconditions:** an employee with the email already exists.
- **Request:** `POST /api/v3/employees` reusing an existing email (unique username).
- **Expected:** `409` · `error.code = conflict` (`AlreadyExists`) · side-effects: no new row. (Username and JMBG uniqueness collide the same way.)
- **Negative siblings:** duplicate username → 409; duplicate JMBG → 409.

#### TC-C1-EMP-102 · Create employee invalid JMBG (NEGATIVE — 13 digits)
- **Feature:** JMBG validacija (tačno 13 cifara, samo cifre) · **Spec:** Spec §JMBG (1189); Celina 1 entitet · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_CreateWithInvalidJMBG
- **Actor:** admin
- **Request:** `POST /api/v3/employees` with `jmbg` variants: `"123"` (too short), `"01019907100061"` (14, too long), `"01019907100A6"` (non-digit).
- **Expected:** each → `400` · `error.code = validation_error` (`ErrInvalidJMBG`, `jmbg_validator.go`). No row created.
- **Negative siblings:** missing JMBG (binding required) → 400.

#### TC-C1-EMP-103 · Create employee as non-admin (NEGATIVE — RBAC)
- **Feature:** Portal samo za administratore · **Spec:** Celina 1 §Portal · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles (sibling); employee_onbehalf_test.go::TestEmployeeOnBehalf_AsBasic_Forbidden
- **Actor:** EmployeeBasic / agent / supervisor / client / unauthenticated
- **Request:** `POST /api/v3/employees` with a valid body.
- **Expected:** employee without `employees.create.any` → `403 forbidden`; unauthenticated → `401 unauthorized`. Per "users should be unaware of forbidden ops," the body is the generic error envelope (no field leak).
- **Negative siblings:** —

#### TC-C1-EMP-110 · List employees + filters (POSITIVE)
- **Feature:** Lista svih zaposlenih + filtriranje (email/ime/pozicija) · **Spec:** Celina 1 §Lista svih zaposlenih · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_ListAndGet
- **Actor:** admin (or any holder of `employees.read.all`)
- **Request:** `GET /api/v3/employees?page=1&page_size=20`, plus `?email=`, `?name=`, `?position=`.
- **Expected:** `200` · `{employees:[...], total_count}`; each row shows `first_name,last_name,email,position,phone,active,role,roles,permissions`. `active` is merged from auth-service status (batch lookup). Filters narrow results (partial match).
- **Negative siblings:** non-permitted caller → 403; unauthenticated → 401.

#### TC-C1-EMP-111 · Get employee by id / not-found (POSITIVE + NEGATIVE)
- **Feature:** Detalji zaposlenog · **Spec:** Celina 1 §Lista · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_ListAndGet; TestEmployee_GetNonExistent
- **Actor:** admin
- **Request:** `GET /api/v3/employees/:id` (valid id, then `999999`).
- **Expected:** valid → `200` with full record incl. `active`; missing → `404 not_found`; non-numeric id → `400 validation_error`.
- **Negative siblings:** —

#### TC-C1-EMP-112 · Update employee — editable fields only (POSITIVE)
- **Feature:** Izmena podataka zaposlenog (sve osim ID i password) · **Spec:** Celina 1 §Lista (klikom na zaposlenog) · **Existing test:** test-app/workflows/employee_test.go::TestEmployee_Update
- **Actor:** admin
- **Preconditions:** target is a non-admin employee.
- **Request:** `PUT /api/v3/employees/:id` · Body (any subset): `{"last_name":"Novak","gender":"M","phone":"+381601112233","address":"Nova 1","jmbg":"0101990710006","position":"Analitičar","department":"Rizik","role":"EmployeeAgent","active":true}`
- **Expected:** `200` · updated record returned; a changelog entry recorded (changed-by from caller JWT). **Editable:** last_name, gender, phone, address, jmbg, position, department, role, active. **NOT editable** (absent from `updateEmployeeRequest`): id, password, first_name, date_of_birth, email, username — these are intentionally immutable ("Ne menja se" in the entity table). NOTE: this is *narrower* than the Celina-1 phrase "može da izmeni sve informacije osim ID-a i passworda" — flag as a documented divergence (first_name/email/username/DOB are not editable here).
- **Negative siblings:** edit an admin → TC-C1-EMP-113; invalid JMBG on update → 400; bad id → 400; not-found → 404.

#### TC-C1-EMP-113 · Cannot edit an admin employee (NEGATIVE)
- **Feature:** Admin može da edituje samo običnog zaposlenog · **Spec:** Celina 1 §Portal · **Existing test:** —
- **Actor:** admin
- **Request:** `PUT /api/v3/employees/:adminId` with any body.
- **Expected:** `403` · `error.code = forbidden` ("cannot edit admin employees" — gateway pre-checks `target.Role == "EmployeeAdmin"`).
- **Negative siblings:** —

#### TC-C1-EMP-120 · Deactivate employee → active sessions killed + token immediately rejected (POSITIVE; defense Provera 2)
- **Feature:** Deaktivacija — prekid svih aktivnih sesija, momentalni izlogov · **Spec:** Celina 1 §Portal ("sve aktivne sesije se automatski prekidaju"); defense Provera 2; Spec §"deactivation → force-refresh" · **Existing test:** test-app/workflows/role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth (epoch-bump force-refresh path)
- **Actor:** admin deactivating a logged-in employee
- **Preconditions:** target employee logged in (has a live access token + ≥1 session).
- **Request:** `PUT /api/v3/employees/:id` · Body: `{"active":false}`
- **Verification:** immediately reuse the target's still-valid (≤15-min) access token on any protected route, e.g. `GET /api/v3/employees`.
- **Expected:** `200` for the deactivate call · side-effects: auth `SetAccountStatus(active=false)` revokes all refresh sessions AND bumps `user_revoked_at:<principal_id>` epoch; the still-valid access token is rejected on the **next** request with `401 token_expired` (NOT `unauthorized`); `auth.account-status-changed` Kafka event published. Subsequent fresh login → `409 business_rule_violation` (TC-C1-LOGIN-012).
- **Negative siblings:** reactivate `{active:true}` → account usable again, but the old token stays dead (epoch already moved).

---

## F. Roles & permissions (RBAC) — `TC-C1-RBAC-*`

#### TC-C1-RBAC-130 · List roles / permissions (POSITIVE)
- **Feature:** Pregled rola i permisija · **Spec:** Celina 1 §Permisije; REST §3 · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_ListRoles / TestRoles_GetRole / TestRoles_ListPermissions
- **Actor:** holder of `roles.read.all`
- **Request:** `GET /api/v3/roles`, `GET /api/v3/roles/:id`, `GET /api/v3/permissions`.
- **Expected:** `200`; roles list includes seeded `EmployeeBasic/EmployeeAgent/EmployeeSupervisor/EmployeeAdmin` each with `permissions[]`; permissions list returns `{code,description,category}` for the full catalog.
- **Negative siblings:** caller without `roles.read.all` → 403; unauthenticated → 401.

#### TC-C1-RBAC-131 · Assign roles to an employee (POSITIVE)
- **Feature:** Dodela rola zaposlenom · **Spec:** Celina 1 §Permisije · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_SetEmployeeRoles
- **Actor:** holder of `employees.roles.assign` (or `employees.permissions.assign`)
- **Request:** `PUT /api/v3/employees/:id/roles` · Body: `{"role_names":["EmployeeAgent","EmployeeSupervisor"]}`
- **Expected:** `200` · employee's effective `roles` + `permissions` recomputed (union of role perms + additional); change is audited; the employee must re-auth to pick up new claims (epoch path).
- **Negative siblings:** unknown role name → 400/validation_error; non-permitted caller → 403; empty `role_names` missing → 400 (binding required).

#### TC-C1-RBAC-132 · Set per-employee additional permissions / grant admin (POSITIVE)
- **Feature:** Per-employee dodatne permisije; dodela admin permisije · **Spec:** Celina 1 §Portal ("može i da dodeli admin permisiju") · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_SetEmployeeAdditionalPermissions
- **Actor:** holder of `employees.permissions.assign`
- **Request:** `PUT /api/v3/employees/:id/permissions` · Body: `{"permission_codes":["employees.create.any","employees.read.all"]}`
- **Expected:** `200` · additional permissions replace the per-employee set; effective permissions = role perms ∪ additional; audited (`permissions.set`). Granting the admin/management permissions promotes the employee's capabilities without changing the role.
- **Negative siblings:** unknown permission code → 400; non-permitted caller → 403.

#### TC-C1-RBAC-133 · Assign / revoke a single permission on a role (POSITIVE + NEGATIVE)
- **Feature:** Granularna izmena permisija role · **Spec:** REST §3 · **Existing test:** test-app/workflows/wf_role_admin_test.go::TestAdmin_AssignPermissionToRole_Success / _Idempotent / _NotInCatalog / _RoleNotFound; TestAdmin_RevokePermissionFromRole_Success / _Idempotent / _RoleNotFound; TestAdmin_AssignPermission_NoAuth
- **Actor:** holder of `roles.permissions.assign` / `roles.permissions.revoke`
- **Request:** `POST /api/v3/roles/:id/permissions` `{"permission":"securities.trade.any"}`; `DELETE /api/v3/roles/:id/permissions/:permission`.
- **Expected:** assign → `204`; idempotent re-assign → `204`; revoke → `204`; idempotent re-revoke → `204`. Permission not in catalog → `400 validation_error`; role not found → `404 not_found`; no/invalid auth → `401 unauthorized`; lacking `roles.permissions.*` → `403 forbidden`.
- **Negative siblings:** —

#### TC-C1-RBAC-134 · Replace all permissions on a role (POSITIVE)
- **Feature:** Zamena svih permisija role · **Spec:** REST §3 · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_UpdateRolePermissions
- **Actor:** holder of `roles.update.any` (or assign/revoke)
- **Request:** `PUT /api/v3/roles/:id/permissions` · Body: `{"permission_codes":["clients.read.all","accounts.read.all"]}`
- **Expected:** `200` · the role's permission set is fully replaced; audited (`permissions.set`). Members of that role must re-auth to pick up the change.
- **Negative siblings:** missing `permission_codes` → 400.

#### TC-C1-RBAC-135 · Role-permission change forces holders to re-auth (POSITIVE — claims invalidation)
- **Feature:** Promena permisija role → momentalno odbijanje starog tokena · **Spec:** Spec §6 (claims-changed epoch) · **Existing test:** test-app/workflows/role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth
- **Actor:** admin changing perms of a role held by a logged-in agent
- **Preconditions:** agent logged in with a live access token.
- **Request:** admin revokes a permission from the agent's role; agent reuses old access token.
- **Expected:** old token rejected `401 token_expired` (per-principal epoch bumped); after refresh/re-login the agent's new (reduced) permission set applies — the now-forbidden route returns `403 forbidden`.
- **Negative siblings:** —

#### TC-C1-RBAC-030 · Per-permission RBAC gating; unpermitted → 403; clients unaware (NEGATIVE matrix)
- **Feature:** Autorizacija — svaka operacija gejtovana permisijom; korisnik nesvestan zabranjenih operacija · **Spec:** Celina 1 §Autentifikacija i autorizacija · **Existing test:** test-app/workflows/roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles; employee_onbehalf_test.go::TestEmployeeOnBehalf_AsBasic_Forbidden
- **Actor:** each role × a route it must NOT reach + unauthenticated + client-on-employee-route
- **Request matrix (each → expected):**
  - EmployeeBasic → `POST /api/v3/employees` → `403 forbidden`
  - EmployeeAgent → `PUT /api/v3/roles/:id/permissions` → `403 forbidden`
  - client token → `GET /api/v3/employees` → `403 forbidden`
  - no token → any protected route → `401 unauthorized`
  - admin → its own permitted routes → `200/201` (positive control)
- **Expected:** unpermitted roles get a uniform `403 forbidden` envelope with no hint of the operation's existence/shape (satisfies "users should not be aware of operations they cannot perform"); missing token → `401 unauthorized`.
- **Negative siblings:** employee acting on-behalf without `*.on_behalf_client` permission → 403 (TestEmployeeOnBehalf_AsBasic_Forbidden, employee_onbehalf_test.go::TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403).

---

## G. Client field validation — `TC-C1-CLI-*`

> Client creation itself belongs to Celina 2 (employee creates a client while opening an account); Celina 1 owns the **client entity + validation rules** (email unique+format, phone digits/`+`, DOB not future). These TCs assert those rules on `POST /api/v3/clients` and `PUT /api/v3/clients/:id`.

#### TC-C1-CLI-140 · Create client (POSITIVE)
- **Feature:** Kreiranje klijenta (entitet + validacija) · **Spec:** Celina 1 §Entiteti/Validacija · **Existing test:** test-app/workflows/client_test.go::TestClient_CreateMultipleClients
- **Actor:** holder of `clients.create.any` (employee)
- **Request:** `POST /api/v3/clients` · Body: `{"first_name":"Jana","last_name":"Jović","date_of_birth":631152000,"gender":"F","email":"jana@example.com","phone":"+381601234567","address":"Bulevar 5","jmbg":"0101990710006"}`
- **Expected:** `201` · client returned with `active:false` (auth Account provisioned async via `client.client-created`); side-effects: `Client` row created.
- **Negative siblings:** duplicate email → 409; bad JMBG → TC-C1-CLI-141; missing required field → TC-C1-CLI-142; future DOB → TC-C1-CLI-143; bad phone → TC-C1-CLI-144.

#### TC-C1-CLI-141 · Create client invalid JMBG (NEGATIVE)
- **Feature:** JMBG validacija · **Spec:** Spec §JMBG · **Existing test:** test-app/workflows/client_test.go::TestClient_CreateWithInvalidJMBG
- **Request:** `POST /api/v3/clients` with `jmbg:"abc"` / `"123"` / 14 digits.
- **Expected:** `400 validation_error` (`ErrInvalidJMBG`).

#### TC-C1-CLI-142 · Create client missing required fields (NEGATIVE)
- **Feature:** Obavezna polja · **Spec:** Celina 1 §Validacija · **Existing test:** test-app/workflows/client_test.go::TestClient_CreateWithMissingRequiredFields
- **Request:** `POST /api/v3/clients` with `first_name`/`email`/`jmbg` omitted.
- **Expected:** `400 validation_error` (gin binding on required fields).

#### TC-C1-CLI-143 · Create client with future date of birth (NEGATIVE)
- **Feature:** Datum rođenja ne sme biti u budućnosti · **Spec:** Celina 1 §Validacija · **Existing test:** — (NO explicit DOB-future workflow test)
- **Request:** `POST /api/v3/clients` with `date_of_birth` = a future Unix timestamp.
- **Expected (per spec):** `400 validation_error`. **Verify the rule is enforced** in client-service; if it is not currently rejected, mark this row **partial / gap** in the matrix. Negative-of-the-boundary: DOB = today/past → accepted.

#### TC-C1-CLI-144 · Create client with malformed email / phone (NEGATIVE)
- **Feature:** Email format+jedinstven; telefon samo cifre i `+` na početku · **Spec:** Celina 1 §Validacija · **Existing test:** — (email-format via gin binding; phone-format not in a dedicated workflow test)
- **Request:** `POST /api/v3/clients` with `email:"not-an-email"`; separately `phone:"06a12"` / `phone:"+38160 12 34"` (spaces/letters).
- **Expected:** malformed email → `400 validation_error` (gateway `binding:"required,email"`). Phone digits/`+` rule per spec → `400 validation_error`; **verify** phone-format enforcement and mark partial/gap if not enforced. Duplicate email → `409 conflict`.

#### TC-C1-CLI-145 · List / get / update client (POSITIVE + NEGATIVE)
- **Feature:** Lista, detalji, izmena klijenta · **Spec:** REST §clients · **Existing test:** test-app/workflows/client_test.go::TestClient_ListAndGet / TestClient_Update / TestClient_GetNonExistent
- **Actor:** holder of `clients.read.*` / `clients.update.*`
- **Request:** `GET /api/v3/clients?page=1&page_size=20` (+ `email_filter`,`name_filter`); `GET /api/v3/clients/:id`; `PUT /api/v3/clients/:id` `{"phone":"+381600000000","address":"Druga 9"}`.
- **Expected:** list → `200 {clients,total}` with `active` merged from auth; get valid → 200, missing → `404 not_found`; update → `200` (changelog recorded). `PUT {active:false}` deactivates the client account (same session-kill/epoch path as employees — client deactivation is principal-type-agnostic).
- **Negative siblings:** update unknown id → 404; non-permitted caller → 403.

---

## H. Access / refresh token lifecycle & sessions — `TC-C1-TOK-*`

#### TC-C1-TOK-150 · Refresh access token (POSITIVE)
- **Feature:** Refresh token — obnova para tokena · **Spec:** Celina 1 §Autorizacija (access/refresh); REST §1 · **Existing test:** test-app/workflows/auth_test.go::TestAuth_RefreshToken
- **Actor:** any authenticated principal
- **Request:** `POST /api/v3/auth/refresh` · Body: `{"refresh_token":"<valid>"}`
- **Expected:** `200` · new `{access_token, refresh_token}` pair (refresh rotates); the old refresh token is consumed/rotated. New access token verifiable locally by the gateway.
- **Negative siblings:** invalid/revoked refresh → TC-C1-TOK-151.

#### TC-C1-TOK-151 · Refresh with invalid/revoked token (NEGATIVE)
- **Feature:** Refresh — nevažeći token · **Spec:** REST §1 · **Existing test:** test-app/workflows/auth_test.go::TestAuth_RefreshTokenInvalid
- **Request:** `POST /api/v3/auth/refresh` · Body: `{"refresh_token":"garbage"}`; and a token already logged-out.
- **Expected:** `401 unauthorized` (`ErrInvalidToken` / `ErrTokenRevoked`). Missing field → `400 validation_error`.

#### TC-C1-TOK-152 · Logout revokes refresh token (POSITIVE)
- **Feature:** Logout — gašenje sesije; ponovni login posle zatvaranja pretraživača · **Spec:** Celina 1 §Login (re-login after closing browser); REST §1 · **Existing test:** test-app/workflows/auth_test.go::TestAuth_Logout
- **Actor:** authenticated principal
- **Request:** `POST /api/v3/auth/logout` · Body: `{"refresh_token":"<current>"}`
- **Expected:** `200` `{"message":"logged out successfully"}` · side-effects: that refresh token revoked + its session's `sid` added to `blacklist:sid:<sid>` → the matching access token is rejected `401 unauthorized` on next use; a subsequent refresh with the revoked token → `401`.
- **Negative siblings:** missing refresh_token → 400.

#### TC-C1-TOK-153 · Protected route without / with invalid token (NEGATIVE)
- **Feature:** Autorizacija — pristup bez tokena · **Spec:** Celina 1 §Autorizacija · **Existing test:** test-app/workflows/auth_test.go::TestAuth_AccessProtectedRouteWithoutToken / TestAuth_AccessProtectedRouteWithInvalidToken
- **Request:** call a protected route with no `Authorization` header; then with `Bearer not-a-jwt`.
- **Expected:** no header → `401 unauthorized` ("missing authorization header"); bad format/garbage → `401 unauthorized` ("invalid or revoked token"); stale (epoch-bumped) but well-formed token → `401 token_expired`.

#### TC-C1-TOK-154 · List my active sessions (POSITIVE)
- **Feature:** Pregled aktivnih sesija · **Spec:** REST §36 · **Existing test:** test-app/workflows (session_handler unit: api-gateway/internal/handler/session_handler_test.go) — workflow link: helpers create sessions on login
- **Actor:** authenticated principal
- **Request:** `GET /api/v3/me/sessions` · Auth: `Bearer <token>`
- **Expected:** `200` · `{sessions:[{id,user_role,ip_address,user_agent,device_id,system_type,last_active_at,created_at,is_current}]}`; the current session is flagged `is_current:true`. Logging in from a 2nd device adds a 2nd row.
- **Negative siblings:** unauthenticated → 401.

#### TC-C1-TOK-155 · Revoke a specific session (POSITIVE + ownership NEGATIVE)
- **Feature:** Opoziv pojedinačne sesije · **Spec:** REST §36 · **Existing test:** api-gateway/internal/handler/session_handler_test.go (unit); workflow: —
- **Actor:** authenticated principal
- **Request:** `DELETE /api/v3/me/sessions/:id` (own session id), then a session id belonging to another user.
- **Expected:** own → `200` "session revoked successfully" (that device's tokens die); other user's session → `404 not_found` / `403 forbidden` (`ErrSessionForbidden`; ownership enforced via caller JWT, id never trusted from path alone); non-numeric/≤0 id → `400 validation_error`; unknown id → `404`.
- **Negative siblings:** double-revoke → `409 business_rule_violation` (`ErrSessionAlreadyRevoked`).

#### TC-C1-TOK-156 · Revoke all other sessions (POSITIVE)
- **Feature:** Opoziv svih ostalih sesija · **Spec:** REST §36 · **Existing test:** api-gateway/internal/handler/session_handler_test.go (unit)
- **Actor:** authenticated principal with ≥2 sessions
- **Request:** `POST /api/v3/me/sessions/revoke-others` · Body: `{"current_refresh_token":"<keep-this>"}`
- **Expected:** `200` · all sessions EXCEPT the one identified by `current_refresh_token` are revoked; `GET /me/sessions` afterwards returns only the current session.
- **Negative siblings:** missing `current_refresh_token` → 400; unauthenticated → 401.

#### TC-C1-TOK-157 · My login history (POSITIVE)
- **Feature:** Istorijat prijava · **Spec:** REST §36 · **Existing test:** api-gateway/internal/handler/session_handler_test.go (unit)
- **Actor:** authenticated principal
- **Request:** `GET /api/v3/me/login-history?limit=50` · Auth: `Bearer <token>`
- **Expected:** `200` · `{entries:[{id,ip_address,user_agent,device_type,success,created_at}]}` newest-first; includes both successful and failed attempts for the caller's email. `limit` clamps to 1..100 (default 50).
- **Negative siblings:** unauthenticated → 401.

---

## Field-validation matrix — Employee

`POST /api/v3/employees` (create) / `PUT /api/v3/employees/:id` (update). Required-on-create fields per `createEmployeeRequest`.

| Field | Valid example | Invalid form(s) → expected code |
|---|---|---|
| `first_name` | `"Petar"` | missing → 400 `validation_error`; (not editable on update — absent from update body) |
| `last_name` | `"Petrović"` | missing → 400 `validation_error` |
| `date_of_birth` (Unix sec) | `631152000` | missing/0 → 400 `validation_error`; future timestamp → per-spec 400 (verify; mark gap if unenforced) |
| `gender` | `"M"` | (free string; no enum gate) → accepted |
| `email` | `"petar.p@exbanka.com"` | missing → 400; malformed (`"abc"`) → 400 `validation_error`; duplicate → 409 `conflict` |
| `phone` | `"+381645555555"` | per spec only digits + leading `+` → 400 (verify; mark gap if unenforced) |
| `address` | `"Njegoševa 25"` | (optional) — |
| `jmbg` | `"0101990710006"` | missing → 400; ≠13 chars → 400 `validation_error`; non-digit → 400; duplicate → 409 `conflict` |
| `username` | `"petar90"` | missing → 400; duplicate → 409 `conflict` |
| `position` | `"Menadžer"` | (optional) — |
| `department` | `"Finansije"` | (optional) — |
| `role` | `"EmployeeBasic"` | missing → 400; unknown role → 400 `validation_error` (service-validated) |
| `active` (update only) | `true`/`false` | flips auth account status; no create-time field (gap) |
| `password` | n/a | **never accepted** on create/update — set via activation only |

## Field-validation matrix — Client

`POST /api/v3/clients` (create) / `PUT /api/v3/clients/:id` (update).

| Field | Valid example | Invalid form(s) → expected code |
|---|---|---|
| `first_name` | `"Jana"` | missing → 400 `validation_error` |
| `last_name` | `"Jović"` | missing → 400 `validation_error` |
| `date_of_birth` (Unix sec) | `631152000` | missing/0 → 400; future → per-spec 400 (verify; mark gap if unenforced) |
| `gender` | `"F"` | (free string) — |
| `email` | `"jana@example.com"` | missing → 400; malformed → 400 `validation_error`; duplicate → 409 `conflict` |
| `phone` | `"+381601234567"` | per spec only digits + leading `+` → 400 (verify; mark gap if unenforced) |
| `address` | `"Bulevar 5"` | (optional) — |
| `jmbg` | `"0101990710006"` | missing → 400; ≠13 → 400; non-digit → 400; duplicate → 409 `conflict` |
| `password` | n/a | **never accepted** here — client credentials provisioned async by auth-service |
| `active` (update only) | `true`/`false` | flips auth account status (deactivate kills sessions + epoch) |

---

## Defense-flow scenarios (Provere → end-to-end chains)

#### TC-C1-E2E-200 · Provera 1 — create → activate → login (POSITIVE chain)
- **Feature:** Defense Provera 1 · **Spec:** `Banka 2025 - odbrana flow` §1 Provera 1 · **Existing test:** chains employee_test.go + auth_test.go
- **Steps:** TC-C1-EMP-100 (admin creates employee) → capture `ACTIVATION` email from Kafka → TC-C1-ACT-080 (employee activates, sets password) → TC-C1-LOGIN-001 pattern (employee logs in).
- **Expected:** end-to-end `201 → (Kafka ACTIVATION) → 200 activate → 200 login`; account transitions `pending → active`; tokens issued.

#### TC-C1-E2E-201 · Provera 2 — admin deactivates employee (POSITIVE chain)
- **Feature:** Defense Provera 2 · **Spec:** `Banka 2025 - odbrana flow` §1 Provera 2 · **Existing test:** role_permission_revocation_test.go (epoch-revocation path)
- **Steps:** employee logged in (from TC-C1-E2E-200) → TC-C1-EMP-120 (`PUT /employees/:id {active:false}`).
- **Expected:** sessions revoked + epoch bumped; old access token → `401 token_expired`; fresh login → `409 business_rule_violation`.

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| Login: employee success | TC-C1-LOGIN-001 | auth_test.go::TestAuth_LoginWithValidCredentials | covered |
| Login: client success + system_type routing | TC-C1-LOGIN-002, TC-C1-RBAC-030 | wf_client_stock_banking_test.go (loginAsClient helper) | covered |
| Login: wrong password | TC-C1-LOGIN-010 | auth_test.go::TestAuth_LoginWithInvalidPassword; auth_login_failure_modes_test.go::TestLogin_Wrong_Password_Returns_401_Unauthorized | covered |
| Login: non-existent email (anti-enum) | TC-C1-LOGIN-011 | auth_test.go::TestAuth_LoginWithNonexistentEmail | covered |
| Login: inactive/disabled account | TC-C1-LOGIN-012 | role_permission_revocation_test.go | covered |
| Login: pending (not activated) | TC-C1-LOGIN-013 | auth_login_failure_modes_test.go::TestLogin_Pending_Account_Returns_409_BusinessRule | covered |
| Login: missing/empty/malformed fields | TC-C1-LOGIN-014 | auth_test.go::TestAuth_LoginWithEmptyFields; auth_login_failure_modes_test.go::TestLogin_Missing_Email_Returns_401_Unauthorized | covered |
| Brute-force: 4th attempt allowed (boundary low) | TC-C1-LOCK-040 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | partial |
| Brute-force: 5th attempt locks (boundary high) | TC-C1-LOCK-041 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | covered |
| Brute-force: locked login blocked (correct pass) | TC-C1-LOCK-042 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | covered |
| Brute-force: lock auto-expiry (30 min impl) | TC-C1-LOCK-043 | — | partial |
| Brute-force: lock-notification email | TC-C1-LOCK-050 | — | NO-ENDPOINT |
| Brute-force: reset unlocks + resets counter | TC-C1-LOCK-051 | auth_test.go::TestAuth_PasswordResetRequest | partial |
| Password reset: request (existing email) | TC-C1-PWD-060 | auth_test.go::TestAuth_PasswordResetRequest | covered |
| Password reset: request (unknown email anti-enum) | TC-C1-PWD-061 | auth_test.go::TestAuth_PasswordResetRequest | covered |
| Password reset: reset with valid token | TC-C1-PWD-062 | — | partial |
| Password reset: expired/invalid token | TC-C1-PWD-063 | auth_test.go::TestAuth_ActivateAccountInvalidToken (sibling) | partial |
| Password reset: confirm mismatch | TC-C1-PWD-064 | — | partial |
| Password constraints (each violation) | TC-C1-PWD-070..074 | auth_service_test.go (unit, complexity) | covered |
| Activation: valid token → active | TC-C1-ACT-080 | mobile_auth_test.go::TestMobileAuth_ActivateFullFlow (mobile variant) | partial |
| Activation: invalid/expired token (24h) | TC-C1-ACT-081 | auth_test.go::TestAuth_ActivateAccountInvalidToken | covered |
| Activation: resend email | TC-C1-ACT-082 | — | partial |
| Employee create: all fields (no password) | TC-C1-EMP-100 | employee_test.go::TestEmployee_CreateWith{Basic,Agent,Supervisor,Admin}Role | covered |
| Employee create: inactive at create | TC-C1-EMP-100b | — | NO-ENDPOINT |
| Employee create: duplicate email/username/JMBG | TC-C1-EMP-101 | employee_test.go::TestEmployee_CreateWithDuplicateEmail | covered |
| Employee create: invalid JMBG (13 digits) | TC-C1-EMP-102 | employee_test.go::TestEmployee_CreateWithInvalidJMBG | covered |
| Employee create: non-admin forbidden | TC-C1-EMP-103 | roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles; employee_onbehalf_test.go::TestEmployeeOnBehalf_AsBasic_Forbidden | covered |
| Employee list + filters | TC-C1-EMP-110 | employee_test.go::TestEmployee_ListAndGet | covered |
| Employee get / not-found | TC-C1-EMP-111 | employee_test.go::TestEmployee_ListAndGet; TestEmployee_GetNonExistent | covered |
| Employee update: editable fields | TC-C1-EMP-112 | employee_test.go::TestEmployee_Update | covered |
| Employee update: cannot edit admin | TC-C1-EMP-113 | — | partial |
| Employee deactivate → sessions killed + token_expired | TC-C1-EMP-120, TC-C1-E2E-201 | role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth | covered |
| Roles/permissions: list | TC-C1-RBAC-130 | roles_permissions_test.go::TestRoles_ListRoles/GetRole/ListPermissions | covered |
| Roles: assign roles to employee | TC-C1-RBAC-131 | roles_permissions_test.go::TestRoles_SetEmployeeRoles | covered |
| Permissions: per-employee additional + grant admin | TC-C1-RBAC-132 | roles_permissions_test.go::TestRoles_SetEmployeeAdditionalPermissions | covered |
| Role perms: assign/revoke single (catalog/idempotent/404/auth) | TC-C1-RBAC-133 | wf_role_admin_test.go::TestAdmin_Assign/RevokePermissionFromRole_* | covered |
| Role perms: replace all | TC-C1-RBAC-134 | roles_permissions_test.go::TestRoles_UpdateRolePermissions | covered |
| Role perm change → holder re-auth | TC-C1-RBAC-135 | role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth | covered |
| RBAC gating: unpermitted→403, unauth→401, clients unaware | TC-C1-RBAC-030 | roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles; employee_onbehalf_test.go::TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403/_AsBasic_Forbidden | covered |
| Client create | TC-C1-CLI-140 | client_test.go::TestClient_CreateMultipleClients | covered |
| Client create: invalid JMBG | TC-C1-CLI-141 | client_test.go::TestClient_CreateWithInvalidJMBG | covered |
| Client create: missing required fields | TC-C1-CLI-142 | client_test.go::TestClient_CreateWithMissingRequiredFields | covered |
| Client validation: DOB not future | TC-C1-CLI-143 | — | partial |
| Client validation: email format/unique + phone digits/+ | TC-C1-CLI-144 | client_test.go::TestClient_CreateMultipleClients (email-format via binding) | partial |
| Client list/get/update (+deactivate) | TC-C1-CLI-145 | client_test.go::TestClient_ListAndGet/Update/GetNonExistent | covered |
| Token: refresh | TC-C1-TOK-150 | auth_test.go::TestAuth_RefreshToken | covered |
| Token: refresh invalid/revoked | TC-C1-TOK-151 | auth_test.go::TestAuth_RefreshTokenInvalid | covered |
| Token: logout (revoke) | TC-C1-TOK-152 | auth_test.go::TestAuth_Logout | covered |
| Token: protected route without/invalid token | TC-C1-TOK-153 | auth_test.go::TestAuth_AccessProtectedRouteWithoutToken/WithInvalidToken | covered |
| Sessions: list my sessions | TC-C1-TOK-154 | session_handler_test.go (unit) | partial |
| Sessions: revoke specific (+ownership) | TC-C1-TOK-155 | session_handler_test.go (unit) | partial |
| Sessions: revoke all others | TC-C1-TOK-156 | session_handler_test.go (unit) | partial |
| Login history | TC-C1-TOK-157 | session_handler_test.go (unit) | partial |
| Defense Provera 1: create→activate→login | TC-C1-E2E-200 | employee_test.go + auth_test.go (chain) | partial |
| Defense Provera 2: deactivate employee | TC-C1-E2E-201 | role_permission_revocation_test.go | covered |
```

### Notable gaps (partial / NO-ENDPOINT)
- **TC-C1-LOCK-050 (NO-ENDPOINT):** Celina-1 requires an account-locked email; no `EmailTypeAccountLocked` / template exists — lockout works but the user is never notified.
- **TC-C1-LOCK-051 (partial):** `ResetPassword` does not call `UnlockAccount`, so a password reset does NOT clear an active 30-min `AccountLock` (login still 403 until expiry) — diverges from "reset otključava nalog".
- **TC-C1-EMP-100b (NO-ENDPOINT):** no create-time `active` flag; inactive-at-create is only reachable via the post-create `PUT {active:false}`.
- **TC-C1-CLI-143 / TC-C1-CLI-144 (partial):** DOB-not-future and phone digits/`+` rules are spec'd but need verification that client-service enforces them; no dedicated workflow test.
- **TC-C1-EMP-112 note:** update is narrower than the spec phrase — first_name/email/username/DOB are intentionally immutable.
- **Threshold conflicts (documented, tested against impl):** attempts 5 (E2E says 3); lock 30 min (Celina-1 says 10); reset link 1h (E2E says 15 min).
- **Session/login-history (partial):** only gateway unit tests exist (`session_handler_test.go`); no end-to-end workflow test yet.
