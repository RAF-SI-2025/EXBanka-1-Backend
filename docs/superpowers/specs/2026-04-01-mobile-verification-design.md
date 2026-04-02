# Mobile App Verification System — Design Spec

**Date:** 2026-04-01
**Status:** Approved
**Scope:** New verification-service, auth-service extensions, notification-service extensions, API gateway extensions, transaction-service migration

---

## 1. Problem Statement

Currently, transaction verification uses 6-digit codes sent via email. Users must switch to their email client, find the code, and type it into the browser. This is slow, insecure (email can be intercepted), and poor UX.

We need a mobile app-based verification system that:
- Replaces email as the primary verification channel for transactions/payments
- Provides three verification methods: code pull, QR scan, and number matching
- Ensures only the registered mobile app can access verification data (not a browser)
- Supports biometric UX (app auto-handles verification after Face ID / fingerprint)
- Falls back to email for users without a registered mobile device

---

## 2. Service Architecture

### New: verification-service (gRPC port 50060, DB port 5440)

Owns ALL verification logic. Replaces the inline verification code system in transaction-service.

**Responsibilities:**
- Create verification challenges (code_pull, qr_scan, number_match, email fallback)
- Validate verification responses
- Manage challenge state and expiry
- Publish Kafka events on challenge lifecycle

### Extended: auth-service

**Additions:**
- MobileDevice model — one active device per user, device-bound tokens
- Mobile activation flow (email code, not password)
- Device signature validation (HMAC verification)
- Device transfer/deactivation

### Extended: notification-service (new DB port 5441)

**Additions:**
- PostgreSQL database for mobile inbox storage
- MobileInboxItem model — pending verification items for mobile delivery
- Kafka consumer for `verification.challenge-created`
- gRPC endpoints for mobile polling (`GetPendingMobileItems`, `AckMobileItem`)
- Auto-cleanup of expired items

### Extended: api-gateway

**Additions:**
- `MobileAuthMiddleware` — validates device-bound JWT + `X-Device-ID` header
- `RequireDeviceSignature` — HMAC signature verification for sensitive endpoints
- WebSocket endpoint `/ws/mobile` — real-time push to connected mobile devices
- New route groups for mobile auth, verification, and device management

### Simplified: transaction-service

**Changes:**
- Remove inline verification code logic (6-digit generation, validation, attempt tracking)
- Call verification-service via gRPC to create challenges and check status
- Consume `verification.challenge-verified` Kafka events to unblock pending transactions

---

## 3. Data Models

### MobileDevice (auth-service, auth_db)

```
MobileDevice
  ID              uint64      primary key
  UserID          uint64      indexed, not null
  SystemType      string      "client" or "employee"
  DeviceID        string      unique, UUID — sent as X-Device-ID header
  DeviceSecret    string      HMAC-SHA256 key, 32 bytes hex — returned ONCE at activation
  DeviceName      string      user-friendly name, e.g. "Luka's iPhone"
  Status          string      "pending", "active", "deactivated"
  ActivatedAt     *time.Time  nullable
  DeactivatedAt   *time.Time  nullable
  LastSeenAt      time.Time   updated on each authenticated request
  Version         int64       optimistic locking
  CreatedAt       time.Time
  UpdatedAt       time.Time
```

One active device per user enforced at service level. Deactivated records kept for audit.

### MobileActivationCode (auth-service, auth_db)

```
MobileActivationCode
  ID              uint64      primary key
  Email           string      indexed
  Code            string      6-digit code
  ExpiresAt       time.Time   15 minutes from creation
  Attempts        int         max 3
  Used            bool        default false
  CreatedAt       time.Time
```

### VerificationChallenge (verification-service, verification_db)

```
VerificationChallenge
  ID              uint64      primary key
  UserID          uint64      indexed
  SourceService   string      "transaction", "payment", "transfer"
  SourceID        uint64      the transaction/payment ID that triggered this
  Method          string      "code_pull", "qr_scan", "number_match", "email"
  Code            string      6-digit code (used for code_pull and email; internal use for qr/number)
  ChallengeData   JSONB       method-specific data (see Section 4)
  Status          string      "pending", "verified", "expired", "failed"
  Attempts        int         max 3
  ExpiresAt       time.Time   5 minutes from creation
  VerifiedAt      *time.Time  nullable
  DeviceID        string      nullable — filled when mobile app claims the challenge
  Version         int64       optimistic locking
  CreatedAt       time.Time
  UpdatedAt       time.Time
```

### MobileInboxItem (notification-service, notification_db)

```
MobileInboxItem
  ID              uint64      primary key
  UserID          uint64      indexed
  DeviceID        string      indexed
  ChallengeID     uint64      from verification-service
  Method          string      "code_pull", "qr_scan", "number_match"
  DisplayData     JSONB       what the app needs to show
  Status          string      "pending", "delivered", "expired"
  ExpiresAt       time.Time   matches challenge expiry
  DeliveredAt     *time.Time  nullable
  CreatedAt       time.Time
```

Auto-cleanup: background goroutine deletes items where `expires_at < now()` every minute.

---

## 4. Verification Methods

### Method 1: code_pull

**ChallengeData:** `{}`

**Flow:**
1. Verification-service generates 6-digit code, stores in `Code` field
2. Publishes to Kafka → notification-service stores in mobile inbox with `display_data: { "code": "482916" }`
3. Mobile app pulls pending items (poll or WebSocket) → shows code to user
4. User types code into browser
5. Browser calls `POST /api/verifications/:id/code` with the code
6. Verification-service validates → marks verified → publishes event

**With biometric:** App receives code, prompts biometric, on success app calls `SubmitVerification` directly (submits code to verification-service, bypassing browser entry). Browser polls status and sees "verified".

### Method 2: qr_scan

**ChallengeData:**
```json
{
  "token": "random-64-char-hex",
  "verify_url": "/api/verify/{challenge_id}"
}
```

**Flow:**
1. Verification-service generates challenge with unique token
2. Returns challenge_data to browser (via transaction-service → gateway)
3. Frontend renders QR code containing: `{gateway_base_url}/api/verify/{challenge_id}?token={token}`
4. Mobile app scans QR → extracts URL
5. App signs the request with device_secret (HMAC)
6. App POSTs to the URL with signature headers
7. Gateway validates: mobile JWT + device_id + signature → forwards to verification-service
8. Verification-service validates token → marks verified → publishes event
9. Browser polls status and sees "verified"

**Security:** The QR URL endpoint requires `MobileAuthMiddleware` + `RequireDeviceSignature`. A browser cannot execute it — no mobile JWT, no device_secret for signing.

### Method 3: number_match

**ChallengeData:**
```json
{
  "target": 42,
  "options": [17, 42, 68, 85, 31]
}
```

**Flow:**
1. Verification-service generates target number (1-99) + 4 random decoys
2. Browser receives `target` number and displays it prominently
3. Mobile app pulls challenge → receives only `options` (shuffled), NOT the target
4. User sees number on browser screen, picks matching number on phone
5. App submits selected number to verification-service
6. If correct → verified. If wrong → attempts incremented, new challenge data generated (new target + options), up to 3 total attempts
7. Browser polls status

**Notification-service delivers to mobile inbox:**
```json
{
  "challenge_id": 123,
  "method": "number_match",
  "display_data": { "options": [17, 42, 68, 85, 31] }
}
```

The `target` value is ONLY returned to the caller (browser-facing response), never stored in mobile inbox.

### Fallback: email

If user has no registered mobile device, `method` is forced to `"email"` regardless of request. Verification-service publishes with `delivery_channel: "email"`. Notification-service sends 6-digit code via SMTP. Browser shows code entry form. This preserves the existing flow for users without the mobile app.

---

## 5. Mobile Device Management (auth-service)

### Activation Flow

1. User opens mobile app, enters email
2. App calls `POST /api/mobile/auth/request-activation` with `{ email }`
3. Auth-service generates 6-digit activation code (15-min expiry, 3 attempts)
4. Publishes email via Kafka → notification-service sends code to user's email
5. User reads email, enters code in app
6. App calls `POST /api/mobile/auth/activate` with `{ email, code, device_name }`
7. Auth-service validates code, deactivates any existing device for this user
8. Creates new MobileDevice record with generated `device_id` and `device_secret`
9. Issues device-bound JWT (access + refresh tokens) with claims:
   - All standard claims (user_id, email, roles, permissions, system_type)
   - `device_type: "mobile"`
   - `device_id: "uuid"`
10. Returns: `{ access_token, refresh_token, device_id, device_secret }`
11. `device_secret` is returned ONLY at this point — app stores in iOS Keychain / Android Keystore

### Device-Bound JWT Claims

Same as existing JWT claims plus:
```
device_type   string   "mobile"
device_id     string   UUID matching the MobileDevice record
```

All existing roles, permissions, system_type are preserved. Mobile team uses roles/permissions for selective UI rendering.

### Token Expiry

- Access token: same as browser (15 min, configurable)
- Refresh token: 90 days (configurable via `MOBILE_REFRESH_EXPIRY`)
- On refresh: auth-service checks MobileDevice is still `active`. If deactivated → reject refresh, app prompts re-activation.

### Device Transfer

Activating a new device automatically deactivates the old one. The deactivated device's refresh token is revoked. Next time the old device tries to refresh → rejected → app shows "device deactivated, please re-activate."

Explicit transfer endpoint also available: `POST /api/mobile/device/transfer` deactivates current device and sends new activation code in one step.

---

## 6. Request Signing (Security)

### Two-Tier Security

**Tier 1 — Standard mobile auth (all mobile endpoints):**
- Valid JWT with `device_type: "mobile"` claim
- `X-Device-ID` header matches `device_id` claim in JWT
- Validated by `MobileAuthMiddleware`

**Tier 2 — Signed requests (sensitive endpoints only):**
- Everything from Tier 1, plus:
- HMAC-SHA256 signature of the request
- Validated by `RequireDeviceSignature` middleware

### Signature Computation

App computes:
```
payload   = timestamp + ":" + http_method + ":" + url_path + ":" + sha256(request_body)
signature = HMAC-SHA256(device_secret, payload)
```

Sent as headers:
```
X-Device-ID:        <device_id>
X-Device-Timestamp: <unix_seconds>
X-Device-Signature: <hex_encoded_hmac>
```

### Server Validation

Gateway `RequireDeviceSignature` middleware:
1. Extracts `device_id` from JWT claims
2. Extracts `X-Device-Timestamp` — rejects if >30s old (replay protection)
3. Reconstructs payload from request
4. Calls auth-service `ValidateDeviceSignature(device_id, timestamp, method, path, body_sha256, signature)`
5. Auth-service looks up `device_secret` from MobileDevice record, computes HMAC, compares
6. Rejects on mismatch

**The device_secret never leaves auth-service.** Gateway forwards the signature for validation; it cannot forge signatures.

### Signed Endpoints

- `GET /api/mobile/verifications/pending`
- `POST /api/mobile/verifications/:id/submit`
- `POST /api/verify/:challenge_id` (QR code endpoint)

---

## 7. Real-Time Delivery (Notification-Service + Gateway)

### WebSocket

**Endpoint:** `GET /ws/mobile`

**Connection setup:**
1. Mobile app opens WebSocket with `Authorization: Bearer <mobile_jwt>` and `X-Device-ID` header
2. Gateway validates token + device_id
3. Registers connection in `user_id → WebSocket` map
4. One connection per user (new replaces old)

**Push flow:**
1. Notification-service stores mobile inbox item
2. Publishes to Kafka topic `notification.mobile-push`
3. Gateway consumes topic, looks up user_id in connection map
4. Pushes JSON message to WebSocket:
```json
{
  "type": "verification_challenge",
  "challenge_id": 123,
  "method": "code_pull",
  "display_data": { "code": "482916" },
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**Keepalive:** Ping/pong every 30s. Dead connections (no pong for 60s) removed from map and closed.

### Polling Fallback

`GET /api/mobile/verifications/pending` — gateway routes to notification-service gRPC.

**When to poll:**
- WebSocket connection failed or not established
- App returns to foreground (immediate check)
- Every 2s in foreground if WebSocket not connected
- Every 30s in background

---

## 8. Permissions

### New Permissions

| Permission | Description | Default Roles |
|------------|-------------|---------------|
| `verification.skip` | Skip mobile verification for transactions | `EmployeeSupervisor`, `EmployeeAdmin` |
| `verification.manage` | View/edit verification settings per role | `EmployeeSupervisor`, `EmployeeAdmin` |

### Verification Decision Flow

When a transaction/payment is created:
1. Check if user has `verification.skip` permission → if yes, execute immediately
2. Check if user has a registered mobile device → if no, force `method: "email"` (existing SMTP flow)
3. Otherwise, create challenge with user's chosen method

---

## 9. API Routes

### Mobile Auth (public — no auth required)

```
POST /api/mobile/auth/request-activation
  Body:     { "email": "user@example.com" }
  Response: { "message": "activation code sent to email" }

POST /api/mobile/auth/activate
  Body:     { "email": "...", "code": "123456", "device_name": "Luka's iPhone" }
  Response: { "access_token": "...", "refresh_token": "...", "device_id": "uuid", "device_secret": "hex" }

POST /api/mobile/auth/refresh
  Body:     { "refresh_token": "..." }
  Headers:  X-Device-ID: <device_id>
  Response: { "access_token": "...", "refresh_token": "..." }
```

### Mobile Device Management (MobileAuthMiddleware)

```
GET /api/mobile/device
  Response: { "device_id": "...", "device_name": "...", "status": "active", "activated_at": "...", "last_seen_at": "..." }

POST /api/mobile/device/deactivate
  Response: { "message": "device deactivated" }

POST /api/mobile/device/transfer
  Body:     { "email": "..." }
  Response: { "message": "device deactivated, activation code sent to email" }
```

### Mobile Verification (MobileAuthMiddleware + RequireDeviceSignature)

```
GET /api/mobile/verifications/pending
  Response: { "items": [{ "id": 1, "challenge_id": 123, "method": "code_pull", "display_data": {...}, "expires_at": "..." }] }

POST /api/mobile/verifications/:challenge_id/submit
  Body:     { "response": "42" }  // or { "response": "482916" } depending on method
  Response: { "success": true, "remaining_attempts": 2 }
```

Note: `:challenge_id` is the verification challenge ID (from `challenge_id` field in pending items), not the inbox item ID.

### QR Verification (MobileAuthMiddleware + RequireDeviceSignature)

```
POST /api/verify/:challenge_id?token=<hex_token>
  Response: { "success": true }
```

### Browser-Facing Verification (AnyAuthMiddleware)

```
POST /api/verifications
  Body:     { "source_service": "transaction", "source_id": 456, "method": "code_pull" }
  Response: { "challenge_id": 123, "challenge_data": {...}, "expires_at": "..." }

GET /api/verifications/:id/status
  Response: { "status": "pending", "method": "code_pull", "expires_at": "..." }

POST /api/verifications/:id/code
  Body:     { "code": "482916" }
  Response: { "success": true, "remaining_attempts": 2 }
```

### Verification Settings (AuthMiddleware + RequirePermission("verification.manage"))

```
GET /api/verifications/settings
  Response: { "role_settings": [{ "role": "client", "requires_verification": true }, ...] }

PUT /api/verifications/settings
  Body:     { "role_settings": [{ "role": "client", "requires_verification": true }, ...] }
  Response: { "message": "settings updated" }
```

### WebSocket

```
GET /ws/mobile
  Headers:  Authorization: Bearer <mobile_jwt>, X-Device-ID: <device_id>
  Messages: { "type": "verification_challenge", "challenge_id": 123, "method": "...", "display_data": {...} }
```

---

## 10. Kafka Topics

| Topic | Publisher | Consumer | Payload |
|-------|-----------|----------|---------|
| `verification.challenge-created` | verification-svc | notification-svc | `{ challenge_id, user_id, device_id, method, display_data, delivery_channel, expires_at }` |
| `verification.challenge-verified` | verification-svc | transaction-svc | `{ challenge_id, user_id, source_service, source_id, method, verified_at }` |
| `verification.challenge-failed` | verification-svc | transaction-svc | `{ challenge_id, user_id, source_service, source_id, reason }` |
| `notification.mobile-push` | notification-svc | api-gateway | `{ user_id, device_id, type, payload }` |

Existing topics (`notification.send-email`, `notification.email-sent`) unchanged.

---

## 11. Transaction Integration

### Current Flow (to be migrated)

```
POST /api/me/payments → transaction-service generates 6-digit code + emails it
POST /api/me/payments/:id/verify → transaction-service validates code + executes
```

### New Flow

```
POST /api/me/payments (with optional "method" field)
  → gateway checks verification.skip permission
  → if skip: transaction-service creates + executes immediately
  → if needs verification:
      1. transaction-service creates payment in pending_verification
      2. gateway calls verification-service CreateChallenge
      3. returns { payment_id, challenge_id, challenge_data } to browser

Browser displays verification UI based on method:
  - code_pull: "Enter the code from your mobile app" + input field
  - qr_scan: renders QR code from challenge_data
  - number_match: shows target number prominently

Browser polls GET /api/verifications/:id/status until "verified"

verification-service publishes verified event
  → transaction-service consumes event
  → executes payment
  → browser poll sees completed transaction
```

The `method` field in payment/transfer request bodies is optional. Default: `code_pull`. Options: `code_pull`, `qr_scan`, `number_match`. If user has no mobile device, forced to `email` regardless.

---

## 12. Environment Variables

### New Variables

| Variable | Default | Service | Notes |
|----------|---------|---------|-------|
| `VERIFICATION_GRPC_ADDR` | `localhost:50060` | verification-service, api-gateway, transaction-service | |
| `VERIFICATION_DB_HOST` | `localhost` | verification-service | |
| `VERIFICATION_DB_PORT` | `5440` | verification-service | |
| `NOTIFICATION_DB_HOST` | `localhost` | notification-service | |
| `NOTIFICATION_DB_PORT` | `5441` | notification-service | |
| `MOBILE_REFRESH_EXPIRY` | `2160h` (90 days) | auth-service | |
| `MOBILE_ACTIVATION_EXPIRY` | `15m` | auth-service | |
| `VERIFICATION_CHALLENGE_EXPIRY` | `5m` | verification-service | |
| `VERIFICATION_MAX_ATTEMPTS` | `3` | verification-service | |

---

## 13. Documentation Deliverables

| File | Action |
|------|--------|
| `docs/mobile/MOBILE_APP_INTEGRATION.md` | **Create** — full guide for mobile team |
| `docs/api/REST_API.md` | **Update** — add all new routes, update payment/transfer flows |
| `Specification.md` | **Update** — add verification-service, models, topics, permissions |
| `CLAUDE.md` | **Update** — add verification-service, notification DB, env vars |
| `docker-compose.yml` | **Update** — add verification-service + DB, notification DB |

### Mobile App Integration Guide Contents

1. Authentication flow (activation step-by-step with request/response examples)
2. Token management (refresh, expiry, deactivation handling)
3. Request signing (exact HMAC formula, which endpoints, code examples)
4. Verification flows (code_pull, qr_scan, number_match — what to display, what to submit)
5. WebSocket connection (URL, headers, message format, keepalive, reconnection)
6. Polling fallback (intervals, endpoint)
7. Biometric UX (auto-handling for code_pull)
8. Security requirements (Keychain/Keystore for device_secret)
9. Error codes and handling
10. Full request/response examples for every endpoint

---

## 14. What Does NOT Change

- Existing email verification works as fallback for users without mobile app
- Client/employee browser login flows unchanged
- Existing permissions system — we add to it, not modify it
- Other services (card-service, credit-service, exchange-service, account-service, stock-service) untouched
- Existing Kafka topics preserved
- All current API routes continue to work
