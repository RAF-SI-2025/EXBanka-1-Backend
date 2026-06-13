# Cross-Cutting — Verification Challenge Mechanism — Test Cases

**Scope.** The end-to-end two-factor verification ("provera") mechanism that gates
money-moving actions: requesting a challenge, delivering the code (Kafka → mobile inbox /
push), submitting it (browser code, mobile response, biometric, QR), and how a gated action
(payment / transfer execute) references a verified challenge id. Covers every verification
method (`code_pull` active; `qr_scan`, `number_match`, `email` planned/removed), every
state transition (`pending` → `verified` / `failed` / `expired`), the attempt counter and
3-attempt ceiling, the 5-minute expiry, the `verification.skip` / fast-path, device binding,
ownership, and the TODO_final.pdf item 7 **Quick Approve** (approve from the push instead of
typing a code; 5-minute response window) — covered here from the verification-mechanism angle
(notification coverage lives in `todo-final-notifications-and-mobile.md`).

**Spec references.** `verification-service/` (model `internal/model/verification_challenge.go`,
service `internal/service/verification_service.go`, errors `internal/service/errors.go`,
repo `internal/repository/verification_challenge_repository.go`, config
`internal/config/config.go`); `api-gateway/internal/handler/verification_handler.go` +
router `api-gateway/internal/router/router_v3.go` (lines 429-455);
`transaction-service/internal/handler/grpc_handler.go` (`ExecutePayment`/`ExecuteTransfer`
challenge gate, lines 160-172, 294-309); `contract/kafka/messages.go` (lines 501-539, topics
`verification.challenge-created` / `-verified` / `-failed`, `notification.mobile-push`);
`notification-service/internal/consumer/verification_consumer.go` (challenge-created →
mobile inbox + push); `docs/api/REST_API_v3.md` §24 (Verification); `docs/Specification.md`
§6 (permissions: `verification.skip`, `verification.manage`), §20 (enum `verification_method`).
Config defaults: `VERIFICATION_CHALLENGE_EXPIRY=5m`, `VERIFICATION_MAX_ATTEMPTS=3`.
TODO_final: `docs/bank-requirements/TODO_final.pdf` mobile section item 7 (Quick Approve).
Existing Go tests: `test-app/workflows/verification_test.go`,
`test-app/workflows/wf_mobile_verification_test.go`,
`test-app/workflows/wf_verification_retry_test.go`,
helpers in `test-app/workflows/helpers_test.go`; unit suites
`verification-service/internal/service/*_test.go`,
`verification-service/internal/handler/grpc_handler_test.go`.

All routes are under `http://localhost:8080/api/v3`. Error envelope is
`{"error":{"code","message","details?}}`. Standard error codes: `validation_error`/400,
`unauthorized`/401, `forbidden`/403, `not_found`/404, `conflict`/409,
`business_rule_violation`/409, `rate_limited`/429, `internal_error`/500. The universal
bypass code is **`111111`** (`defaultBypassCode`, `verification_service.go:52`) — always
verifies, in addition to the real generated 6-digit code; the workflow helpers use it so no
Kafka scan is needed.

---

## ⚠️ Implementation reality (assert the IMPLEMENTED behaviour)

These divergences between the docs/spec and the code are flagged so graders/cohorts reconcile.
The TCs below assert what the code actually does.

| Topic | Doc/spec says | **IMPLEMENTED** | TC |
|---|---|---|---|
| Active methods | enum has `code_pull,qr_scan,number_match,email` | only **`code_pull`** is creatable (`validMethods`, `verification_service.go:39-42`; gateway `oneOf("method",…,"code_pull")`). `qr_scan`/`number_match`/`email` → 400 | TC-VERIF-020/021/022 |
| `verification.skip` bypass | "employees with `verification.skip` bypass verification entirely" | **No permission is checked anywhere.** The bypass is structural: `ExecutePayment`/`ExecuteTransfer` only verify when `challenge_id > 0` (`grpc_handler.go:162,296`). Omit `challenge_id` → no challenge ever required, for ANY caller | TC-VERIF-060/061/062 |
| `verification_code` field | execute body carries `verification_code` | **vestigial** — transaction-service ignores it; only `challenge_id` is consulted | TC-VERIF-050/060 |
| Failed challenge cancels the txn | `VerificationChallengeFailedMessage` "transaction-service consumes this to cancel the pending transaction" | **No service consumes `verification.challenge-*verified*`/`*-failed*`** (only `notification-service` consumes `challenge-created`). A failed/expired challenge leaves the payment in `pending_verification`; gating is synchronous at execute (`GetChallengeStatus`), not event-driven | TC-VERIF-041, TC-VERIF-053 |
| Expiry publishes `challenge-failed{reason:"expired"}` | message documents `reason:"expired"` | **`ExpireOldChallenges` never publishes** — the sweep only flips `pending`→`expired` (`verification_service.go:400-410`, `repository.ExpireOld`). No event emitted on expiry | TC-VERIF-053 |
| Code/QR submit ownership | — | `SubmitCode` (browser) and `SubmitVerification` (mobile) **do not check the caller owns the challenge**; only `VerifyByBiometric` checks `vc.UserID==userID`. Any authenticated caller can submit a code for any `challenge_id` | TC-VERIF-070 |
| Quick Approve (push-approve) | TODO_final item 7: approve from push, 5-min window, all verifiable actions | **No dedicated push-approve endpoint.** The closest implemented "approve without typing a code" is biometric verify; the 5-min window is the challenge expiry. Quick Approve as a discrete feature is NO-ENDPOINT | TC-VERIF-080/081/082 |
| `source_service` validation | gateway should validate | gateway does **not** `oneOf` it (only `binding:"required"`); the service rejects unknown values with `InvalidArgument`→400 | TC-VERIF-012 |

---

## A. Create challenge — `TC-VERIF-0xx`

#### TC-VERIF-001 · Create code_pull challenge (POSITIVE)
- **Feature:** Verification — kreiranje izazova (code_pull) · **Spec:** REST §24 `POST /verifications`; `verification_service.go:CreateChallenge` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_CreateChallenge; verification-service/internal/service/verification_service_more_test.go::TestCreateChallenge_HappyPath_PublishesEvent
- **Actor:** client (or employee — AnyAuthMiddleware)
- **Preconditions:** a pending transfer/payment exists owned by the caller (use `setupTransferForVerification`); caller authenticated.
- **Request:** `POST /api/v3/verifications`
  - Auth: `Bearer <client token>`
  - Body: `{"source_service":"transfer","source_id":<transferID>}`  (method omitted → defaults `code_pull`)
- **Verification:** n/a (this IS the verification machinery)
- **Expected:** `200` · body `{challenge_id (uint64, >0), challenge_data:"{}", expires_at (RFC3339)}` · side-effects: a `VerificationChallenge{status:"pending", method:"code_pull", attempts:0, version:1, expires_at≈now+5m}` row persisted (`source_service`/`source_id` recorded); `expires_at` ≈ `now + VERIFICATION_CHALLENGE_EXPIRY (5m)`; Kafka **`verification.challenge-created`** published with `delivery_channel:"mobile"`, `method:"code_pull"`, `display_data` containing the 6-digit `code`; `VerificationChallengesCreatedTotal{method=code_pull}` incremented.
- **Negative siblings:** unauth → TC-VERIF-010; missing source_id → TC-VERIF-011; bad source_service → TC-VERIF-012; bad method → TC-VERIF-020/021/022.

#### TC-VERIF-002 · challenge-created → mobile inbox item + push (POSITIVE)
- **Feature:** Verification — isporuka koda u mobilni inbox + push · **Spec:** `notification-service/internal/consumer/verification_consumer.go` (handleMobileDelivery) · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_BrowserChallengeVisibleOnMobile
- **Actor:** client (browser creates) + mobile device (polls)
- **Preconditions:** TC-VERIF-001 produced a challenge; the client has an activated mobile device (`setupMobileDevice`).
- **Request:** `GET /api/v3/mobile/verifications/pending`
  - Auth: Mobile JWT + `X-Device-ID` + device signature
  - Body: —
- **Expected:** `200` · `items[]` includes an entry `{id, challenge_id == created id, method:"code_pull", display_data:{"code":"<6 digits>"}, expires_at}` · side-effects: a `MobileInboxItem` row was created by notification-service from the `challenge-created` event, AND a **`notification.mobile-push`** `MobilePushMessage{type:"verification_challenge", payload:{challenge_id,method,display_data,expires_at}}` was published (the WebSocket nudge that backs Quick Approve). The push failure path is best-effort (not retried) and never duplicates the inbox row.
- **Negative siblings:** browser/client token (non-mobile) on `/mobile/verifications/pending` → 403 (TC-VERIF-071).

#### TC-VERIF-010 · Create challenge unauthenticated (NEGATIVE)
- **Feature:** Verification — kreiranje bez tokena · **Spec:** AnyAuthMiddleware on `/verifications` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_UnauthenticatedRejected
- **Actor:** unauthenticated
- **Request:** `POST /api/v3/verifications` · Auth: none · Body: `{"source_service":"transfer","source_id":1}`
- **Expected:** `401` · `error.code = unauthorized` · side-effects: none (no challenge row).
- **Negative siblings:** —

#### TC-VERIF-011 · Create challenge missing/zero source_id (NEGATIVE)
- **Feature:** Verification — obavezna polja · **Spec:** gateway `binding:"required"`; service `sourceID==0` guard · **Existing test:** verification-service/internal/handler/grpc_handler_test.go (CreateChallenge ZeroSourceID via service_more_test.go::TestCreateChallenge_ZeroSourceID)
- **Actor:** client
- **Request:** `POST /api/v3/verifications` · Body A `{"source_service":"transfer"}` (missing); Body B `{"source_service":"transfer","source_id":0}`
- **Expected:** `400` · `error.code = validation_error` · side-effects: none. (Gateway rejects missing/zero via binding; if it ever reaches the service, `ErrInvalidArguments`→400.)
- **Negative siblings:** missing source_service → 400 (same shape).

#### TC-VERIF-012 · Create challenge invalid source_service (NEGATIVE)
- **Feature:** Verification — nevažeći source_service · **Spec:** `validSourceServices={transaction,payment,transfer}`; `ErrInvalidSourceService` · **Existing test:** verification-service/internal/service/verification_service_more_test.go::TestCreateChallenge_InvalidSourceService
- **Actor:** client
- **Request:** `POST /api/v3/verifications` · Body: `{"source_service":"loan","source_id":1}`
- **Expected:** `400` · `error.code = validation_error` ("invalid source_service") · side-effects: none. NOTE: gateway does not `oneOf` this field; rejection happens service-side (`InvalidArgument`→400).
- **Negative siblings:** `source_service:"order"` / `"card"` → same 400.

#### TC-VERIF-020 · Create challenge method=qr_scan (NEGATIVE / NOT-AVAILABLE)
- **Feature:** Verification — qr_scan nije dostupan · **Spec:** gateway `oneOf("method",req,"code_pull")`; `validMethods` only code_pull · **Existing test:** test-app/workflows/verification_test.go::TestVerification_InvalidMethodRejected; verification-service/internal/service/verification_service_test.go::TestValidMethods_QRScanDisabled
- **Actor:** client
- **Request:** `POST /api/v3/verifications` · Body: `{"source_service":"transfer","source_id":<id>,"method":"qr_scan"}`
- **Expected:** `400` · `error.code = validation_error` (gateway: "method must be one of: code_pull") · side-effects: none.
- **Negative siblings:** number_match → TC-VERIF-021; email → TC-VERIF-022.

#### TC-VERIF-021 · Create challenge method=number_match (NEGATIVE / NOT-AVAILABLE)
- **Feature:** Verification — number_match nije dostupan · **Spec:** as above; `TestValidMethods_NumberMatchDisabled` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_InvalidMethodRejected
- **Actor:** client
- **Request:** `POST /api/v3/verifications` · Body: `{"source_service":"transfer","source_id":<id>,"method":"number_match"}`
- **Expected:** `400` · `error.code = validation_error` · side-effects: none.
- **Negative siblings:** TC-VERIF-020, TC-VERIF-022.

#### TC-VERIF-022 · Create challenge method=email (NEGATIVE / REMOVED)
- **Feature:** Verification — email metoda uklonjena · **Spec:** REST §24 ("email — Removed"); `TestValidMethods_EmailDisabled` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_InvalidMethodRejected
- **Actor:** client
- **Request:** `POST /api/v3/verifications` · Body: `{"source_service":"transfer","source_id":<id>,"method":"email"}`
- **Expected:** `400` · `error.code = validation_error` · side-effects: none. (`email` is in the enum doc but disabled; `code_pull` is the sole channel.)
- **Negative siblings:** uppercase `"CODE_PULL"` → normalised to `code_pull` by `oneOf` → 200 (case-insensitivity positive, TC-VERIF-001 variant).

---

## B. code_pull — full flow & status — `TC-VERIF-03x`

#### TC-VERIF-030 · Poll status: pending → verified (POSITIVE)
- **Feature:** Verification — status izazova · **Spec:** REST §24 `GET /verifications/:id/status` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_GetChallengeStatus; wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile (asserts "verified")
- **Actor:** client
- **Preconditions:** challenge created (TC-VERIF-001).
- **Request:** `GET /api/v3/verifications/<challengeID>/status`
- **Expected:** `200` · before submit `{status:"pending", method:"code_pull", verified_at:null, expires_at}`; after a correct submit `{status:"verified", verified_at:<RFC3339>}` · side-effects: read-only. NOTE: no ownership check — any authenticated caller can poll any id (status-only leak; documented gap).
- **Negative siblings:** unknown id → 404 (TC-VERIF-051).

#### TC-VERIF-031 · Browser submit correct/bypass code → verified (POSITIVE)
- **Feature:** Verification — unos koda (browser) · **Spec:** REST §24 `POST /verifications/:id/code`; `SubmitCode` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_SubmitBypassCode; helpers_test.go::submitVerificationCode
- **Actor:** client (browser)
- **Preconditions:** pending code_pull challenge.
- **Request:** `POST /api/v3/verifications/<challengeID>/code` · Body: `{"code":"111111"}` (bypass) or the real `code` from the mobile inbox display_data.
- **Verification:** this case exercises the submit step itself.
- **Expected:** `200` · `{success:true, remaining_attempts:2}` · side-effects: challenge `status` → `verified`, `verified_at` set, `attempts` incremented to 1, `version` bumped; Kafka **`verification.challenge-verified`** `{challenge_id,user_id,source_service,source_id,method:"code_pull",verified_at}` published; `VerificationAttemptsTotal{result=success}` incremented. (No consumer acts on the verified event — gating is synchronous at execute.)
- **Negative siblings:** wrong code → TC-VERIF-040; missing/empty code → 400 (binding); 4th submit after verified → 409 not-pending (TC-VERIF-052).

#### TC-VERIF-032 · Mobile submit response → verified (POSITIVE, full E2E)
- **Feature:** Verification — mobilna potvrda · **Spec:** REST §24 `POST /mobile/verifications/:id/submit`; `SubmitVerification` · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile, ::TestMobileVerification_MobileCreatedChallengeVisible
- **Actor:** mobile device
- **Preconditions:** activated device (`setupMobileDevice`); challenge created; mobile polled pending and extracted `code` from `display_data`.
- **Request:** `POST /api/v3/mobile/verifications/<challengeID>/submit` (signed) · Auth: Mobile JWT + device signature · Body: `{"response":"<code-from-inbox>"}`
- **Expected:** `200` · `{success:true, remaining_attempts:2}` · side-effects: challenge `verified`, `verified_at` set, **`device_id` bound** to the submitting device (first submit binds), `attempts`=1; `verification.challenge-verified` published; browser `GET …/status` subsequently returns `verified`.
- **Negative siblings:** second submit from a DIFFERENT device → 409 device-mismatch (TC-VERIF-072); non-mobile token → 403 (TC-VERIF-071).

#### TC-VERIF-033 · Acknowledge inbox item removes it from pending (POSITIVE)
- **Feature:** Verification — ACK mobilne stavke · **Spec:** REST §24 `POST /mobile/verifications/:id/ack` · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending
- **Actor:** mobile device
- **Preconditions:** challenge delivered to inbox; `inboxID` = the item's `id` (NOT the challenge_id).
- **Request:** `POST /api/v3/mobile/verifications/<inboxID>/ack` (signed)
- **Expected:** `200` · `{success:true}` · side-effects: the `MobileInboxItem` marked delivered; a subsequent `GET /mobile/verifications/pending` no longer lists that `challenge_id`.
- **Negative siblings:** non-mobile token → 403 (TC-VERIF-071 / TestVerification_AckEndpointRequiresMobileAuth); unknown/already-acked item id → 404; invalid id → 400.

#### TC-VERIF-034 · Multiple concurrent challenges all visible & verifiable (POSITIVE)
- **Feature:** Verification — više istovremenih izazova · **Spec:** `GetPendingByUser` returns all pending; inbox lists all · **Existing test:** test-app/workflows/wf_mobile_verification_test.go::TestMobileVerification_MultipleChallengesVisible
- **Actor:** client browser + mobile
- **Preconditions:** two pending transfers, one challenge each.
- **Request:** create 2 challenges → mobile polls → submit each code.
- **Expected:** both appear in `pending`, both verify independently (`success:true`), browser sees both `verified`; each emits its own `challenge-verified`.
- **Negative siblings:** —

---

## C. Wrong code, attempt counter, max attempts — `TC-VERIF-04x`

#### TC-VERIF-040 · Wrong code → success:false, attempt counter increments (NEGATIVE)
- **Feature:** Verification — pogrešan kod · **Spec:** `SubmitCode` else-branch · **Existing test:** test-app/workflows/verification_test.go::TestVerification_SubmitWrongCode; verification-service/internal/service/verification_service_test.go::TestCheckResponse_CodePull_WrongCode
- **Actor:** client
- **Preconditions:** fresh pending code_pull challenge (attempts=0).
- **Request:** `POST /api/v3/verifications/<id>/code` · Body: `{"code":"999999"}`
- **Expected:** `200` · `{success:false, remaining_attempts:2}` · side-effects: challenge stays `pending`, `attempts` → 1 (counter incremented), `version` bumped; `VerificationAttemptsTotal{result=failure}` incremented; **no** `challenge-verified` event.
- **Negative siblings:** 2nd wrong → remaining 1; 3rd wrong → TC-VERIF-041.

#### TC-VERIF-041 · 3 wrong codes → challenge failed, txn NOT executable (NEGATIVE / boundary)
- **Feature:** Verification — maksimum 3 pokušaja → izazov otkazan · **Spec:** `maxAttempts=3`; `remaining<=0 → status="failed"` · **Existing test:** test-app/workflows/wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry
- **Actor:** client
- **Preconditions:** pending code_pull challenge on a `pending_verification` payment.
- **Request:** submit wrong `{"code":"000000"}` three times.
- **Expected:** attempt 1 → `{success:false, remaining_attempts:2}`; attempt 2 → `remaining_attempts:1`; attempt 3 → `{success:false, remaining_attempts:0}` and challenge `status` → **`failed`** · side-effects on the 3rd: Kafka **`verification.challenge-failed`** `{challenge_id,user_id,source_service,source_id,reason:"max_attempts_exceeded"}` published. **Gap:** no consumer acts on it, so the payment remains in `pending_verification` (it is NOT auto-cancelled); the user must start a new payment + challenge (the workflow test does exactly this and the retry succeeds, balance decreases).
- **Negative siblings:** a 4th submit (even bypass `111111`) → 409 (TC-VERIF-052); executing the payment with the failed `challenge_id` → 409 (TC-VERIF-050).

#### TC-VERIF-042 · Boundary: 2 wrong then correct → verified before lockout (POSITIVE)
- **Feature:** Verification — granica pokušaja (2 loša pa tačan) · **Spec:** counter resets nothing; correct on attempt 3 still verifies (attempts becomes 3, remaining 0, but status=verified) · **Existing test:** — (covered indirectly by unit `TestValidateChallengeState_MaxAttemptsReached`)
- **Actor:** client
- **Preconditions:** fresh challenge.
- **Request:** wrong, wrong, then `{"code":"111111"}`.
- **Expected:** 1st `remaining 2`, 2nd `remaining 1`, 3rd `{success:true, remaining_attempts:0}`, `status:"verified"` · side-effects: `challenge-verified` published; the correct submit is allowed because `validateChallengeState` blocks only when `attempts >= maxAttempts` BEFORE the submit (at attempt 3 attempts is 2 < 3, so it proceeds and increments to 3).
- **Negative siblings:** a 3rd attempt that is ALSO wrong → fails the challenge (TC-VERIF-041).

---

## D. Expiry — `TC-VERIF-05x`

#### TC-VERIF-053 · Submit after 5-minute expiry → rejected (NEGATIVE / boundary)
- **Feature:** Verification — istek izazova posle 5 min · **Spec:** `VERIFICATION_CHALLENGE_EXPIRY=5m`; `validateChallengeState` `time.Now().After(ExpiresAt)` → `ErrChallengeExpired` · **Existing test:** verification-service/internal/service/verification_service_test.go::TestValidateChallengeState_Expired; handler grpc_handler_test.go::TestHandlerSubmitVerification_Expired_FailedPrecondition
- **Actor:** client
- **Preconditions:** a challenge whose `expires_at` is in the past (set `VERIFICATION_CHALLENGE_EXPIRY` low for the run, or wait >5m).
- **Request:** `POST /api/v3/verifications/<id>/code` · Body: `{"code":"111111"}`
- **Expected:** `409` · `error.code = business_rule_violation` ("challenge has expired") · side-effects: status NOT verified. Separately, the background sweep (`ExpireOldChallenges`) flips `pending`→`expired` and increments `VerificationChallengesExpiredTotal`, but **publishes no event** (documented gap) — so the gated payment stays `pending_verification`.
- **Negative siblings:** poll status after sweep → `{status:"expired"}` (TC-VERIF-030 variant); execute payment with expired challenge → 409 not-completed (TC-VERIF-050).

#### TC-VERIF-050 · Execute payment with non-verified challenge → blocked (NEGATIVE)
- **Feature:** Verification — gated izvršenje plaćanja · **Spec:** `transaction-service/internal/handler/grpc_handler.go:160-172` (`GetChallengeStatus` must be "verified") · **Existing test:** wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry (asserts the failed-challenge path)
- **Actor:** client
- **Preconditions:** payment in `pending_verification`; a challenge that is `pending`/`failed`/`expired` (not verified).
- **Request:** `POST /api/v3/me/payments/<paymentID>/execute` · Body: `{"verification_code":"x","challenge_id":<unverifiedID>}`
- **Expected:** `409` · `error.code = business_rule_violation` ("verification not completed") · side-effects: payment stays `pending_verification`; no balance change; no ledger entry.
- **Negative siblings:** `challenge_id` pointing to a deleted/unknown id → transaction-service returns `Internal` ("verification check failed") → 500 (the GetChallengeStatus NotFound is wrapped as Internal); verified challenge → execute succeeds (TC-VERIF-060).

#### TC-VERIF-051 · Submit / status for nonexistent challenge → 404 (NEGATIVE)
- **Feature:** Verification — nepostojeći izazov · **Spec:** `ErrChallengeNotFound` (NotFound) · **Existing test:** verification-service/internal/handler/grpc_handler_test.go::TestHandlerSubmitCode_NotFound, ::TestHandlerGetChallengeStatus_NotFound
- **Actor:** client
- **Request:** `POST /api/v3/verifications/999999999/code` Body `{"code":"111111"}`; and `GET /api/v3/verifications/999999999/status`
- **Expected:** `404` · `error.code = not_found` ("challenge not found") · side-effects: none.
- **Negative siblings:** non-numeric id in path → 400 validation_error.

#### TC-VERIF-052 · Submit on already-consumed (verified/failed) challenge → 409 (NEGATIVE)
- **Feature:** Verification — izazov nije više pending · **Spec:** `validateChallengeState` `status != "pending"` → `ErrChallengeNotPending` · **Existing test:** verification-service/internal/service/verification_service_test.go::TestValidateChallengeState_AlreadyVerified, ::TestValidateChallengeState_StatusFailed
- **Actor:** client
- **Preconditions:** a challenge already `verified` (after TC-VERIF-031) or `failed` (after TC-VERIF-041).
- **Request:** `POST /api/v3/verifications/<id>/code` · Body: `{"code":"111111"}`
- **Expected:** `409` · `error.code = business_rule_violation` ("challenge is already verified/failed") · side-effects: no state change; no duplicate event (idempotent — re-verifying a verified challenge is rejected).
- **Negative siblings:** TC-VERIF-041 (failed), TC-VERIF-053 (expired).

---

## E. verification.skip / fast-path — `TC-VERIF-06x`

#### TC-VERIF-060 · Fast-path: execute WITHOUT challenge_id → no verification required (POSITIVE)
- **Feature:** Verification — fast-path (preskakanje provere) · **Spec:** `grpc_handler.go:162` gate is `if challenge_id > 0`; omit → no check; REST §24 "verification.skip … bypass entirely" · **Existing test:** — (helpers always pass a challenge; the no-challenge path is untested)
- **Actor:** client or employee (no permission is actually enforced — see reality note)
- **Preconditions:** payment created in `pending_verification`.
- **Request:** `POST /api/v3/me/payments/<id>/execute` · Body: `{"challenge_id":0}` (or omit the field entirely)
- **Verification:** fast-path
- **Expected:** `200` · payment executes · side-effects: sender debited (amount+commission), recipient credited, bank fee credited to RSD sentinel, ledger entries written, `payment.status:"completed"`, `payment.completed` Kafka event. **No** challenge is ever created or consulted.
- **Negative siblings:** providing a non-verified `challenge_id>0` → 409 (TC-VERIF-050). NOTE: because the gate is purely structural, this "fast-path" is available to ANY caller, not only holders of `verification.skip` — a real authorization gap to surface.

#### TC-VERIF-061 · Supervisor/admin holds verification.skip permission (POSITIVE, capability)
- **Feature:** RBAC — verification.skip dodeljen supervizoru/adminu · **Spec:** `docs/Specification.md` §6 (EmployeeSupervisor/Admin → `verification.skip`); `contract/permissions/perms.gen.go:74,756,845` · **Existing test:** — (permission seed is asserted in role seed tests)
- **Actor:** supervisor / admin
- **Preconditions:** seeded roles.
- **Request:** inspect effective permissions (e.g. token claims after login, or `GET /api/v3/roles/:id`).
- **Expected:** `verification.skip.any` (and `verification.manage.any`) present for EmployeeSupervisor and EmployeeAdmin; absent for EmployeeBasic/EmployeeAgent.
- **Negative siblings:** EmployeeBasic must NOT carry `verification.skip` (TC-VERIF-062). **Caveat:** the permission is currently decorative — no execute path checks it (see TC-VERIF-060).

#### TC-VERIF-062 · Basic/agent role lacks verification.skip (NEGATIVE)
- **Feature:** RBAC — odsustvo verification.skip · **Spec:** as above · **Existing test:** —
- **Actor:** basic / agent
- **Expected:** `verification.skip` NOT in effective permissions for EmployeeBasic / EmployeeAgent. (Functionally moot today because the execute path never checks it; flagged so the cohort wires the check.)
- **Negative siblings:** —

---

## F. Ownership, device binding, biometric, QR — `TC-VERIF-07x`

#### TC-VERIF-070 · Code submission has no caller-ownership check (NEGATIVE / adversarial GAP)
- **Feature:** Verification — vlasništvo nad izazovom (browser submit) · **Spec:** `SubmitCode`/`SubmitVerification` take no `userID` — no ownership assertion · **Existing test:** —
- **Actor:** client B (attacker), challenge belongs to client A
- **Preconditions:** client A created challenge `X`; client B authenticated and knows/guesses `X`.
- **Request:** `POST /api/v3/verifications/<X>/code` as client B · Body: `{"code":"111111"}`
- **Expected (IMPLEMENTED):** `200` · `{success:true}` — B can verify A's challenge. This is a documented authorization gap: only `VerifyByBiometric` checks `vc.UserID==userID`. **Desired** behaviour is `403 forbidden`; the TC records the current (insecure) result so it is visible in the matrix.
- **Negative siblings:** biometric on another user's challenge → correctly 403 (TC-VERIF-073).

#### TC-VERIF-071 · Mobile endpoints reject non-mobile (browser/client) token → 403 (NEGATIVE)
- **Feature:** Verification — mobilni endpoints zahtevaju mobile token · **Spec:** `MobileAuthMiddleware`+`RequireDeviceSignature` on `/mobile/verifications/*`, `/verify/*` · **Existing test:** test-app/workflows/verification_test.go::TestVerification_AckEndpointRequiresMobileAuth
- **Actor:** client/employee with a browser (non-mobile) token
- **Request:** `POST /api/v3/mobile/verifications/1/ack`; also `…/submit`, `…/biometric`, `POST /api/v3/verify/1?token=x`
- **Expected:** `403` · `error.code = forbidden` (browser token lacks `device_type=mobile` / device signature) · side-effects: none.
- **Negative siblings:** unauthenticated → 401.

#### TC-VERIF-072 · Mobile submit from a second device → device mismatch 409 (NEGATIVE)
- **Feature:** Verification — vezivanje uređaja · **Spec:** `SubmitVerification` binds `DeviceID` on first submit; mismatch → `ErrDeviceMismatch` (FailedPrecondition) · **Existing test:** verification-service/internal/handler/grpc_handler_test.go::TestHandlerSubmitVerification_DeviceMismatch_FailedPrecondition
- **Actor:** two mobile devices of the same user
- **Preconditions:** device 1 already submitted (binding it), challenge still pending (e.g. it submitted wrong once).
- **Request:** `POST /api/v3/mobile/verifications/<id>/submit` from device 2.
- **Expected:** `409` · `error.code = business_rule_violation` ("challenge already bound to a different device") · side-effects: no state change; caller must retry from the bound device.
- **Negative siblings:** same device retry → allowed.

#### TC-VERIF-073 · Biometric verify (Quick-Approve-style) success + ownership + prerequisites
- **Feature:** Verification — biometrijska potvrda · **Spec:** REST §24 `POST /mobile/verifications/:id/biometric`; `VerifyByBiometric` (ownership + `CheckBiometricsEnabled`) · **Existing test:** verification-service/internal/handler/grpc_handler_test.go::TestHandlerVerifyByBiometric_Success, ::_OwnershipDenied, ::_BiometricsDisabled
- **Actor:** mobile device (owner)
- **Preconditions:** biometrics enabled via `POST /api/v3/mobile/device/biometrics`; a pending challenge owned by the user.
- **Request:** `POST /api/v3/mobile/verifications/<challengeID>/biometric` (signed, no body)
- **Expected:** `200` · `{success:true}` · side-effects: challenge `verified`, `verified_at` set, `challenge_data.verified_by="biometric"` audit stamp, device bound; `verification.challenge-verified` published; `VerificationAttemptsTotal{result=biometric_success}` incremented.
- **Negative siblings:** challenge of another user → `403 forbidden` ("challenge does not belong to this user"); biometrics not enabled → `403 forbidden`; expired/already-verified challenge → `409`; non-mobile token → `403` (TC-VERIF-071).

#### TC-VERIF-074 · QR verify endpoint unusable (no qr_scan challenge can exist) (NEGATIVE / NOT-AVAILABLE)
- **Feature:** Verification — QR potvrda nije dostupna · **Spec:** REST §24 ("Not available"); `validMethods` excludes qr_scan so no qr challenge exists; `POST /verify/:challenge_id` routes through `SubmitVerification(response=token)` · **Existing test:** —
- **Actor:** mobile device
- **Request:** `POST /api/v3/verify/<code_pull_challenge_id>?token=<hex>` (no qr_scan challenge can be created, so the only reachable target is a code_pull challenge)
- **Expected:** the endpoint is reachable but `checkResponse` compares the token against the 6-digit code → mismatch → `200 {success:false}` (attempt counter increments, device binds). With no `token` query param → `400 validation_error`. Effectively **NO-ENDPOINT** for its intended qr_scan purpose.
- **Negative siblings:** unknown challenge id → 404; expired → 409.

---

## G. Quick Approve (TODO_final.pdf item 7) — `TC-VERIF-08x`

> TODO_final item 7 (mobile): *"klijent može da odobri akciju direktno sa notifikacije umesto
> da kuca kod. Ako klijent ne reaguje na notifikaciju u roku od 5 minuta, zahtev ističe. Quick
> Approve se primenjuje na sve akcije koje zahtevaju verifikaciju."* (approve from the push
> instead of typing a code; expires if no response within 5 minutes; applies to all
> verification-requiring actions.) The notification-coverage angle lives in
> `todo-final-notifications-and-mobile.md`; here we assert the verification mechanism.

#### TC-VERIF-080 · Approve from push without typing a code (POSITIVE, biometric as closest impl)
- **Feature:** Quick Approve — odobravanje sa notifikacije · **Spec:** TODO_final item 7; closest endpoint = biometric verify (no code typed); push delivered via `notification.mobile-push` `type:"verification_challenge"` · **Existing test:** — (biometric covered by unit grpc_handler_test.go::TestHandlerVerifyByBiometric_Success)
- **Actor:** mobile device (owner)
- **Preconditions:** challenge created → `notification.mobile-push` `{type:"verification_challenge", payload:{challenge_id,…}}` delivered (TC-VERIF-002); biometrics enabled.
- **Request:** from the push, `POST /api/v3/mobile/verifications/<challenge_id>/biometric` (signed, no body) — no 6-digit code typed.
- **Expected:** `200` · `{success:true}` · side-effects identical to TC-VERIF-073 (challenge verified, `verified_by:"biometric"`, `challenge-verified` event); the gated payment/transfer can then execute with that `challenge_id`.
- **Negative siblings:** dedicated "approve" action keyed off the push id (rather than challenge id) → NO-ENDPOINT (TC-VERIF-082).

#### TC-VERIF-081 · Quick Approve 5-minute window: no response → request expires (NEGATIVE / boundary)
- **Feature:** Quick Approve — istek za 5 min · **Spec:** TODO_final item 7; mechanism = challenge `expires_at = now+5m`; expiry sweep / `validateChallengeState` · **Existing test:** verification-service/internal/service/verification_service_test.go::TestValidateChallengeState_Expired, ::TestExpireOldChallenges_MarksOnlyExpired
- **Actor:** mobile device (no action)
- **Preconditions:** challenge created, user does not respond.
- **Request:** after >5 min, attempt `POST …/biometric` (or `/submit`).
- **Expected:** `409` · `error.code = business_rule_violation` ("challenge has expired") · side-effects: challenge `status:"expired"` (via sweep); `VerificationChallengesExpiredTotal` incremented; the gated action remains un-executed (must restart). The 5-minute window is exactly `VERIFICATION_CHALLENGE_EXPIRY`.
- **Negative siblings:** approve at minute 4 → still allowed (within window); approve at minute 6 → 409 (this case).

#### TC-VERIF-082 · Dedicated push-approve feature is NO-ENDPOINT (GAP)
- **Feature:** Quick Approve — namenski endpoint za odobravanje sa push-a · **Spec:** TODO_final item 7 · **Existing test:** —
- **Actor:** —
- **Expected:** **NO-ENDPOINT.** There is no endpoint that approves a verification by the *push/inbox item id* or with a one-tap "approve" semantic; the only "approve without code" path is biometric (which still requires biometrics enabled + device signature) and the only expiry is the generic 5-minute challenge expiry. Quick Approve "applies to all verification-requiring actions", but only payment/transfer require verification (see matrix) — so even via biometric it cannot cover OTC exercise / limit changes, which are not gated at all.
- **Negative siblings:** —

---

## Gated-action matrix (does the action require verification? fast-path?)

The verification challenge is only consulted by **transaction-service** (`ExecutePayment` /
`ExecuteTransfer`), and only when the caller supplies `challenge_id > 0`. Valid
`source_service` values are `payment`, `transfer`, `transaction`.

| Gated action | Endpoint | Requires verification? | How it references the challenge | Fast-path available? |
|---|---|---|---|---|
| Payment execute | `POST /api/v3/me/payments/:id/execute` | **Advisory** — only if `challenge_id>0` is passed; `GetChallengeStatus` must be `verified` else 409 | body `{"challenge_id":<id>}` (`source_service:"payment"`) | **Yes** — omit `challenge_id` (or `0`) → no check (TC-VERIF-060) |
| Transfer execute | `POST /api/v3/me/transfers/:id/execute` | **Advisory** — same gate (`grpc_handler.go:294-309`) | body `{"challenge_id":<id>}` (`source_service:"transfer"`) | **Yes** — omit `challenge_id` |
| Generic "transaction" | (no specific route) | source_service `transaction` is *valid* to create a challenge but no action wires it | — | n/a |
| OTC option exercise | `POST /api/v3/…/exercise` | **No** — no verification integration | — | n/a (NO-ENDPOINT for verification) |
| Client/account limit change | `PUT /api/v3/clients/:id/limits` etc. | **No** — not gated | — | n/a (NO-ENDPOINT for verification) |
| Order placement / OTC accept / fund invest | various | **No** — not gated | — | n/a |

> Quick Approve is specified to apply to **all** verification-requiring actions; in the
> implementation that set is exactly {payment, transfer}, and even there the approval path
> (biometric) is a partial fit (TC-VERIF-082).

---

## Field-validation matrix — `POST /api/v3/verifications`

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `source_service` | `"transfer"` / `"payment"` / `"transaction"` | missing → 400 `validation_error` (binding); `"loan"`,`"order"`,`"card"` → 400 `validation_error` (service `ErrInvalidSourceService`) |
| `source_id` | `123` (uint64 >0) | missing → 400; `0` → 400; negative/non-numeric → 400 (JSON/binding) |
| `method` | `"code_pull"` (default if omitted); `"CODE_PULL"` normalised | `"qr_scan"`/`"number_match"`/`"email"`/any other → 400 `validation_error` (gateway `oneOf`) |

**`POST /api/v3/verifications/:id/code`**

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `:id` (path) | numeric challenge id | non-numeric → 400; unknown → 404 `not_found` |
| `code` | `"111111"` (bypass) or the real 6-digit code | missing/empty → 400 `validation_error` (binding); wrong → 200 `{success:false}` + attempt++; after expiry/verified/failed → 409 `business_rule_violation` |

**`POST /api/v3/mobile/verifications/:id/submit`**

| Field | Valid example | Invalid form → expected |
|---|---|---|
| `:id` (path) | numeric challenge id | non-numeric → 400; unknown → 404 |
| `response` | the code from `display_data` | missing → 400; wrong device → 409; non-mobile token → 403 |

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| Create challenge (code_pull, default) + persisted pending row | TC-VERIF-001 | verification_test.go::TestVerification_CreateChallenge; verification_service_more_test.go::TestCreateChallenge_HappyPath_PublishesEvent | covered |
| challenge-created → mobile inbox item + mobile-push | TC-VERIF-002 | wf_mobile_verification_test.go::TestMobileVerification_BrowserChallengeVisibleOnMobile | covered |
| Create challenge: unauthenticated → 401 | TC-VERIF-010 | verification_test.go::TestVerification_UnauthenticatedRejected | covered |
| Create challenge: missing/zero source_id → 400 | TC-VERIF-011 | verification_service_more_test.go::TestCreateChallenge_ZeroSourceID | covered |
| Create challenge: invalid source_service → 400 | TC-VERIF-012 | verification_service_more_test.go::TestCreateChallenge_InvalidSourceService | covered |
| Method qr_scan rejected → 400 | TC-VERIF-020 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_QRScanDisabled | covered |
| Method number_match rejected → 400 | TC-VERIF-021 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_NumberMatchDisabled | covered |
| Method email rejected/removed → 400 | TC-VERIF-022 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_EmailDisabled | covered |
| Poll status pending→verified | TC-VERIF-030 | verification_test.go::TestVerification_GetChallengeStatus; wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile | covered |
| Browser submit correct/bypass → verified + challenge-verified event | TC-VERIF-031 | verification_test.go::TestVerification_SubmitBypassCode | covered |
| Mobile submit response → verified + device bound (E2E) | TC-VERIF-032 | wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile, ::TestMobileVerification_MobileCreatedChallengeVisible | covered |
| ACK inbox item removes from pending | TC-VERIF-033 | wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending | covered |
| Multiple concurrent challenges all verifiable | TC-VERIF-034 | wf_mobile_verification_test.go::TestMobileVerification_MultipleChallengesVisible | covered |
| Wrong code → success:false, attempt counter++ | TC-VERIF-040 | verification_test.go::TestVerification_SubmitWrongCode; verification_service_test.go::TestCheckResponse_CodePull_WrongCode | covered |
| 3 wrong → challenge failed + challenge-failed event; txn not executable | TC-VERIF-041 | wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry | covered |
| Boundary: 2 wrong then correct → verified | TC-VERIF-042 | verification_service_test.go::TestValidateChallengeState_MaxAttemptsReached (unit) | partial |
| Submit after 5-min expiry → 409; no expiry event | TC-VERIF-053 | verification_service_test.go::TestValidateChallengeState_Expired; grpc_handler_test.go::TestHandlerSubmitVerification_Expired_FailedPrecondition | covered |
| Execute payment/transfer with non-verified challenge → 409 | TC-VERIF-050 | wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry | covered |
| Submit/status on nonexistent challenge → 404 | TC-VERIF-051 | grpc_handler_test.go::TestHandlerSubmitCode_NotFound, ::TestHandlerGetChallengeStatus_NotFound | covered |
| Submit on already-consumed (verified/failed) → 409 | TC-VERIF-052 | verification_service_test.go::TestValidateChallengeState_AlreadyVerified, ::TestValidateChallengeState_StatusFailed | covered |
| Fast-path: execute without challenge_id → no verification | TC-VERIF-060 | — | NO-ENDPOINT |
| verification.skip held by supervisor/admin (capability) | TC-VERIF-061 | — | partial |
| verification.skip absent from basic/agent | TC-VERIF-062 | — | partial |
| Code submit has no ownership check (adversarial GAP) | TC-VERIF-070 | — | partial |
| Mobile endpoints reject non-mobile token → 403 | TC-VERIF-071 | verification_test.go::TestVerification_AckEndpointRequiresMobileAuth | covered |
| Mobile submit from second device → device mismatch 409 | TC-VERIF-072 | grpc_handler_test.go::TestHandlerSubmitVerification_DeviceMismatch_FailedPrecondition | covered |
| Biometric verify: success + ownership + biometrics-enabled | TC-VERIF-073 | grpc_handler_test.go::TestHandlerVerifyByBiometric_Success, ::_OwnershipDenied, ::_BiometricsDisabled | covered |
| QR verify endpoint unusable (no qr_scan challenge) | TC-VERIF-074 | — | NO-ENDPOINT |
| Quick Approve: approve from push w/o code (biometric) | TC-VERIF-080 | grpc_handler_test.go::TestHandlerVerifyByBiometric_Success | partial |
| Quick Approve: 5-min no-response → expires | TC-VERIF-081 | verification_service_test.go::TestValidateChallengeState_Expired, ::TestExpireOldChallenges_MarksOnlyExpired | partial |
| Quick Approve: dedicated push-approve endpoint | TC-VERIF-082 | — | NO-ENDPOINT |
| Gated-action matrix (payment/transfer gated; OTC/limit not gated) | (matrix) | wf_verification_retry_test.go; helpers_test.go::createAndExecutePayment/createAndExecuteTransfer | partial |
```
