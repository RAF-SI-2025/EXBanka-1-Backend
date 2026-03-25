# Verification Flow Refactor + Cards Access + Dev Console Logging

**Date:** 2026-03-25
**Status:** Approved

## Problem

Three issues with the current design:

1. **Verification flow is leaking internals.** Clients must manually call `POST /api/me/verification` to request a verification code and `POST /api/me/verification/validate` to check it before executing a payment or transfer. This is internal transaction authorization logic that should be invisible to the API surface. The client should only need to: (a) create the transaction, (b) receive the code by email, (c) execute with the code.

2. **`GET /api/cards/:id` docs gap.** The route is already under `AuthMiddleware` + `RequirePermission("cards.read")` (employee-only), but docs/swagger may not clearly reflect this distinction from `GET /api/me/cards/:id`.

3. **Local development friction.** Email delivery is too slow for local testing. Notification service should print all tokens and codes to stdout so developers can copy them immediately.

---

## Design

### 1. Auto-generate verification code on payment/transfer creation

**New flow:**
1. Client calls `POST /api/me/payments` or `POST /api/me/transfers`
2. Transaction-service creates the pending transaction **and** auto-generates a verification code, sends it to the client's email via Kafka
3. `CreatePayment`/`CreateTransfer` responses include `verification_code_expires_at` so the UI can show a countdown
4. Client enters the received code, calls `POST /api/me/payments/:id/execute` or `POST /api/me/transfers/:id/execute` with `verification_code`
5. Transaction-service validates internally and executes

**`POST /api/me/verification` and `POST /api/me/verification/validate` are removed.**

#### Proto changes (`contract/proto/transaction.proto`)

Add to `CreatePaymentRequest`:
```proto
uint64 client_id = 8;
string client_email = 9;
```

Add to `CreateTransferRequest`:
```proto
uint64 client_id = 8;
string client_email = 9;
```

Remove RPCs:
- `CreateVerificationCode`
- `ValidateVerificationCode`

Remove messages:
- `CreateVerificationCodeRequest`
- `CreateVerificationCodeResponse`
- `ValidateVerificationCodeRequest`
- `ValidateVerificationCodeResponse`

Run `make proto` after changes.

#### transaction-service: `grpc_handler.go`

In `CreatePayment`, after `paymentSvc.CreatePayment` succeeds:
```go
_, code, err := h.verificationSvc.CreateVerificationCode(ctx, req.GetClientId(), payment.ID, "payment")
if err != nil {
    log.Printf("warn: failed to create verification code for payment %d: %v", payment.ID, err)
} else if email := req.GetClientEmail(); email != "" {
    _ = h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
        To:        email,
        EmailType: kafkamsg.EmailTypeTransactionVerify,
        Data: map[string]string{
            "verification_code": code,
            "expires_in":        "5 minutes",
        },
    })
}
```

Return `verification_code_expires_at` in `PaymentResponse`.

Same pattern in `CreateTransfer`.

Remove `CreateVerificationCode` and `ValidateVerificationCode` handler methods.

#### transaction-service: proto response

Add `verification_code_expires_at` (int64 unix timestamp) to `PaymentResponse` and `TransferResponse`. Set it when creating (zero/omitted when the field doesn't apply, e.g. on execute or list).

#### api-gateway: `transaction_handler.go`

In `CreatePayment` (me route):
- Extract `user_id` and `email` from JWT context (`c.Get("user_id")`, `c.Get("email")`)
- Pass as `ClientId` and `ClientEmail` in `CreatePaymentRequest`
- Include `verification_code_expires_at` in the JSON response

In `CreateTransfer` (me route): same.

Remove `CreateVerificationCode` and `ValidateVerificationCode` handler functions.

#### api-gateway: `router.go`

Remove:
```go
me.POST("/verification", txHandler.CreateVerificationCode)
me.POST("/verification/validate", txHandler.ValidateVerificationCode)
```

---

### 2. GET /api/cards/:id — employee-only (no code change)

`GET /api/cards/:id` is already correctly gated:
- Route is under `cardsRead` group with `middleware.AuthMiddleware` + `middleware.RequirePermission("cards.read")`
- Clients use `GET /api/me/cards/:id` (ownership-verified via JWT)

Action: update `docs/api/REST_API.md` and swagger annotations to explicitly note this is an employee-only route. No code changes.

---

### 3. Notification service: console logging of all tokens and codes

In `email_consumer.go`, `handleMessage`, add a dev-visibility log immediately after parsing the message and before sending:

```go
log.Printf("[DEV] email queued | type=%s to=%s data=%v", emailMsg.EmailType, emailMsg.To, emailMsg.Data)
```

This prints all data fields (which include tokens, codes, links, CVVs depending on email type) to stdout for every outbound notification. Covers:
- `ACTIVATION` → prints activation `link`
- `PASSWORD_RESET` → prints reset `link`
- `TRANSACTION_VERIFICATION` → prints `verification_code`
- `CARD_VERIFICATION` → prints `cvv`
- All others → prints whatever data was passed

No configuration flag needed — this is a backend service running locally and in Docker Compose. The log is a development convenience, not a security concern in this context.

---

## Files Changed

| File | Change |
|---|---|
| `contract/proto/transaction.proto` | Add `client_id`/`client_email` to Create*Request; remove Verification RPCs |
| `contract/transactionpb/` | Regenerated (make proto) |
| `transaction-service/internal/handler/grpc_handler.go` | Auto-generate code in Create*; remove Verification handlers |
| `transaction-service/internal/model/payment.go` | No change (verification_code_expires_at is proto-only response field) |
| `api-gateway/internal/handler/transaction_handler.go` | Pass client_id/email on create; remove Verification handlers; update `paymentToJSON`/`transferToJSON` to include `verification_code_expires_at` |
| `api-gateway/internal/router/router.go` | Remove verification routes |
| `api-gateway/docs/` | Regenerated swagger |
| `notification-service/internal/consumer/email_consumer.go` | Add dev console log |
| `docs/api/REST_API.md` | Remove verification sections; clarify cards/:id is employee-only |
| `test-app/workflows/payment_test.go` | Remove verification steps; replace with `scanKafkaForVerificationCode` helper; remove/replace `TestPayment_VerificationCodeRequired` |
| `test-app/workflows/transfer_test.go` | Remove verification steps; replace with `scanKafkaForVerificationCode` helper |
| `test-app/workflows/workflow_test.go` | Remove verification steps; replace with `scanKafkaForVerificationCode` helper |
| `test-app/workflows/negative_test.go` | Remove `{"POST", "/api/me/verification"}` entry from route table |
| `test-app/workflows/helpers_test.go` | Add `scanKafkaForVerificationCode(t, email)` helper |

---

## Error Handling

- Verification code generation failure on create is **non-fatal** (log warning, still return the created payment/transfer). The client won't receive a code but the transaction is created. This matches the existing pattern used for Kafka publish failures.
- If `client_email` is empty, skip the email publish silently (same as existing `CreateVerificationCode` behavior).

---

## Test-app Updates

All tests that follow the pattern:
```
CreatePayment → POST /api/me/verification → POST /api/me/verification/validate → ExecutePayment
```

Should become:
```
CreatePayment (code sent automatically via Kafka) → scanKafkaForVerificationCode → ExecutePayment with verification_code
```

**Test-app has no DB access** — codes cannot be read from the `verification_codes` table. The correct approach is to scan the `notification.send-email` Kafka topic, exactly like the existing `scanKafkaForActivationToken` helper in `helpers_test.go`. Add a `scanKafkaForVerificationCode(t, email string) string` helper that:
1. Reads from the `notification.send-email` topic (consumer group `test-verification-scanner` or similar)
2. Waits for a message where `EmailType == "TRANSACTION_VERIFICATION"` and `To == email`
3. Returns `data["verification_code"]`

**Additional test changes:**
- `negative_test.go`: Remove `{"POST", "/api/me/verification"}` from the `TestNeg_EmployeeCannotAccessClientOnlyRoutes` route table — the route no longer exists and would return 404 instead of 401.
- `payment_test.go` `TestPayment_VerificationCodeRequired`: This test verifies that `POST /api/me/verification` returns 401 for unauthenticated callers. Since the route is deleted, the test must be **deleted** (it tests a removed endpoint, not the new flow).
