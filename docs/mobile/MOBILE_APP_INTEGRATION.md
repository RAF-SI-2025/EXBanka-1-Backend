# Mobile App Integration Guide

This guide covers everything the mobile team needs to integrate with the EXBanka backend for device authentication, transaction verification, and real-time notifications.

**Base URL:** `https://api.exbanka.rs` (production) / `http://localhost:8080` (development)

---

## 1. Authentication Flow

### 1.1 Device Activation (First-Time Setup)

Mobile devices must go through a two-step activation before they can authenticate.

#### Step 1: Request Activation Code

```
POST /api/mobile/auth/request-activation
Content-Type: application/json

{
  "email": "user@example.com"
}
```

**Response (200):**
```json
{
  "message": "activation code sent to email",
  "expires_in_seconds": 900
}
```

The user receives a 6-digit code via email (valid for 15 minutes, max 3 attempts).

#### Step 2: Activate Device

```
POST /api/mobile/auth/activate
Content-Type: application/json

{
  "email": "user@example.com",
  "code": "482916",
  "device_name": "Luka's iPhone 16"
}
```

**Response (200):**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs...",
  "device_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "device_secret": "f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4"
}
```

**CRITICAL:** `device_secret` is returned **only once** at activation. Store it immediately in:
- **iOS:** Keychain Services (`kSecClassGenericPassword`)
- **Android:** Android Keystore / EncryptedSharedPreferences

If `device_secret` is lost, the user must re-activate the device.

### 1.2 JWT Claims

The access token contains these claims:

```json
{
  "user_id": 12345,
  "email": "user@example.com",
  "roles": ["client"],
  "permissions": [],
  "system_type": "client",
  "device_type": "mobile",
  "device_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "exp": 1743500000
}
```

`device_type: "mobile"` and `device_id` are the additional claims that distinguish mobile tokens from browser tokens. Use `roles` and `permissions` for selective UI rendering (e.g., employee-only screens).

---

## 2. Token Management

### 2.1 Token Expiry

| Token | Lifetime | Configurable Via |
|-------|----------|------------------|
| Access token | 15 minutes | `JWT_ACCESS_EXPIRY` |
| Refresh token | 90 days | `MOBILE_REFRESH_EXPIRY` |

### 2.2 Token Refresh

```
POST /api/mobile/auth/refresh
Content-Type: application/json
Authorization: Bearer <expired_or_valid_access_token>
X-Device-ID: <device_id>

{
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

**Response (200):**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

### 2.3 Refresh Token Rejection

If the refresh returns **401**, the device has been deactivated (e.g., user activated a new device). Show a re-activation prompt:

```
"Your device has been deactivated. Please re-activate to continue."
→ Navigate to activation flow
```

### 2.4 Recommended Token Strategy

1. Store tokens in secure storage (Keychain / Keystore)
2. Refresh proactively when access token has <2 minutes remaining
3. Retry failed requests once after a successful refresh
4. On 401 from refresh → clear tokens → show re-activation screen

---

## 3. Secure Storage Requirements

| Item | iOS | Android |
|------|-----|---------|
| `device_secret` | Keychain (`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`) | Android Keystore with biometric binding |
| `access_token` | Keychain | EncryptedSharedPreferences |
| `refresh_token` | Keychain | EncryptedSharedPreferences |
| `device_id` | UserDefaults (not sensitive) | SharedPreferences |

**Never** store `device_secret` in:
- UserDefaults / SharedPreferences (unencrypted)
- Local files
- Logs or crash reports
- Source code or build artifacts

---

## 4. Request Signing (HMAC)

Sensitive endpoints require HMAC request signing. These endpoints use the `RequireDeviceSignature` middleware.

### 4.1 Signed Endpoints

- `POST /api/mobile/verifications/pending` — fetch pending verifications
- `POST /api/mobile/verifications/:id/submit` — submit verification response
- `POST /api/verify/:challenge_id` — QR code verification

### 4.2 Signature Computation

```
timestamp   = current unix timestamp (seconds)
method      = HTTP method (uppercase, e.g., "POST")
path        = URL path (e.g., "/api/mobile/verifications/pending")
body_hash   = SHA-256 hex digest of the raw request body (empty string → SHA-256 of "")

payload     = timestamp + ":" + method + ":" + path + ":" + body_hash
signature   = HMAC-SHA256(device_secret, payload)  → hex encoded
```

### 4.3 Required Headers

Every signed request must include:

```
Authorization:       Bearer <access_token>
X-Device-ID:         <device_id>
X-Device-Timestamp:  <unix_seconds>
X-Device-Signature:  <hex_encoded_hmac>
```

### 4.4 Code Examples

#### Swift (iOS)

```swift
import CryptoKit
import Foundation

func signRequest(method: String, path: String, body: Data?, deviceSecret: String, deviceId: String) -> [String: String] {
    let timestamp = String(Int(Date().timeIntervalSince1970))
    let bodyHash = SHA256.hash(data: body ?? Data()).map { String(format: "%02x", $0) }.joined()
    let payload = "\(timestamp):\(method):\(path):\(bodyHash)"
    
    let key = SymmetricKey(data: Data(hexString: deviceSecret)!)
    let hmac = HMAC<SHA256>.authenticationCode(for: Data(payload.utf8), using: key)
    let signature = hmac.map { String(format: "%02x", $0) }.joined()
    
    return [
        "X-Device-ID": deviceId,
        "X-Device-Timestamp": timestamp,
        "X-Device-Signature": signature
    ]
}
```

#### Kotlin (Android)

```kotlin
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import java.security.MessageDigest

fun signRequest(method: String, path: String, body: ByteArray?, deviceSecret: String, deviceId: String): Map<String, String> {
    val timestamp = (System.currentTimeMillis() / 1000).toString()
    val bodyHash = MessageDigest.getInstance("SHA-256")
        .digest(body ?: ByteArray(0))
        .joinToString("") { "%02x".format(it) }
    
    val payload = "$timestamp:$method:$path:$bodyHash"
    
    val mac = Mac.getInstance("HmacSHA256")
    mac.init(SecretKeySpec(deviceSecret.hexToByteArray(), "HmacSHA256"))
    val signature = mac.doFinal(payload.toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it) }
    
    return mapOf(
        "X-Device-ID" to deviceId,
        "X-Device-Timestamp" to timestamp,
        "X-Device-Signature" to signature
    )
}
```

### 4.5 Replay Protection

The server rejects signatures with timestamps older than **30 seconds**. Keep device clocks synced via NTP. If you get `401 signature_expired`, re-sign the request with a fresh timestamp and retry once.

---

## 5. Verification Flows

When a user initiates a payment or transfer from the browser, the backend creates a verification challenge. The mobile app receives the challenge and presents the appropriate UI.

### 5.1 Method: code_pull

The simplest method. The app displays a 6-digit code that the user types into the browser.

**Mobile inbox item:**
```json
{
  "challenge_id": 123,
  "method": "code_pull",
  "display_data": "{\"code\": \"482916\"}",
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**App UX:**
1. Receive notification (WebSocket or poll)
2. Parse `display_data` → extract `code`
3. Display code prominently: **"Enter this code in your browser: 482916"**
4. Show countdown timer based on `expires_at`
5. User types the code in the browser manually

**With biometric auto-submit:**
1. Receive notification
2. Display: **"Approve transaction of 5,000 RSD?"**
3. Prompt Face ID / fingerprint
4. On biometric success, auto-submit the code via API:

```
POST /api/mobile/verifications/123/submit
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
X-Device-Timestamp: <timestamp>
X-Device-Signature: <signature>
Content-Type: application/json

{
  "challenge_id": 123,
  "code": "482916"
}
```

**Response (200):**
```json
{
  "status": "verified"
}
```

### 5.2 Method: qr_scan

The browser displays a QR code. The mobile app scans it and submits a signed request.

**Mobile inbox item:**
```json
{
  "challenge_id": 124,
  "method": "qr_scan",
  "display_data": "{\"message\": \"Scan the QR code displayed in your browser\"}",
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**App UX:**
1. Receive notification
2. Display: **"Scan the QR code on your screen to approve"**
3. Open camera / QR scanner
4. QR code content: `https://api.exbanka.rs/api/verify/124?token=abc123def456...`
5. Extract `challenge_id` and `token` from URL
6. Sign and POST:

```
POST /api/verify/124
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
X-Device-Timestamp: <timestamp>
X-Device-Signature: <signature>
Content-Type: application/json

{
  "token": "abc123def456..."
}
```

**Response (200):**
```json
{
  "status": "verified"
}
```

**Security:** The QR endpoint requires `MobileAuthMiddleware` + `RequireDeviceSignature`. A browser cannot call it — only the registered mobile app with the device secret can generate a valid signature.

### 5.3 Method: number_match

The browser displays a target number. The mobile app shows 5 options — the user picks the matching one.

**Mobile inbox item:**
```json
{
  "challenge_id": 125,
  "method": "number_match",
  "display_data": "{\"options\": [17, 42, 68, 85, 31]}",
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**Important:** The mobile app receives ONLY the `options` array. The `target` number is displayed ONLY in the browser. This ensures the user must look at both screens.

**App UX:**
1. Receive notification
2. Parse `display_data` → extract `options` array
3. Display: **"Select the number shown in your browser:"** with 5 tappable buttons
4. User taps the matching number
5. Submit selection:

```
POST /api/mobile/verifications/125/submit
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
X-Device-Timestamp: <timestamp>
X-Device-Signature: <signature>
Content-Type: application/json

{
  "challenge_id": 125,
  "selected_number": 42
}
```

**Response (200):**
```json
{
  "status": "verified"
}
```

**Wrong answer (409):**
```json
{
  "error": {
    "code": "business_rule_violation",
    "message": "incorrect number selected",
    "details": {
      "attempts_remaining": 2,
      "new_options": [23, 56, 42, 91, 74]
    }
  }
}
```

On wrong answer, display the new options. After 3 failed attempts, the challenge is marked `failed`.

### 5.4 Fallback: Email

If the user has no registered mobile device, verification falls back to email automatically. The mobile app has no role in email fallback — the browser handles it entirely. No mobile inbox item is created.

---

## 6. WebSocket (Real-Time Push)

### 6.1 Connection

```
GET /ws/mobile?token=<access_token>&device_id=<device_id>
```

The connection is upgraded to WebSocket. Authentication is validated on connect (same as `MobileAuthMiddleware`).

### 6.2 Message Format

All messages are JSON:

```json
{
  "type": "verification_challenge",
  "challenge_id": 123,
  "method": "code_pull",
  "display_data": "{\"code\": \"482916\"}",
  "expires_at": "2026-04-01T12:05:00Z"
}
```

### 6.3 Keepalive

- Server sends **ping** every 30 seconds
- Client must respond with **pong** within 60 seconds
- If pong is missed, server closes the connection

### 6.4 Reconnection Strategy

```
1. On disconnect: wait 1 second, reconnect
2. On failure: exponential backoff (1s, 2s, 4s, 8s, 16s, max 30s)
3. On success: reset backoff to 1s
4. On app foreground: always reconnect immediately
5. On app background: disconnect after 30 seconds (save battery)
```

### 6.5 Connection Lifecycle

```
App Launch → Connect WebSocket
App Foreground → Reconnect (if disconnected)
App Background → Disconnect after 30s, switch to polling
Device Deactivated → Connection rejected on next attempt
Token Expired → Refresh token, reconnect with new access token
```

---

## 7. Polling Fallback

If WebSocket is unavailable (network restrictions, background mode), poll for pending verifications.

### 7.1 Endpoint

```
POST /api/mobile/verifications/pending
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
X-Device-Timestamp: <timestamp>
X-Device-Signature: <signature>
```

**Response (200):**
```json
{
  "items": [
    {
      "id": 1,
      "challenge_id": 123,
      "method": "code_pull",
      "display_data": "{\"code\": \"482916\"}",
      "expires_at": "2026-04-01T12:05:00Z"
    }
  ]
}
```

### 7.2 Polling Intervals

| App State | Interval | Rationale |
|-----------|----------|-----------|
| Foreground, WebSocket connected | No polling | WebSocket handles delivery |
| Foreground, WebSocket disconnected | 2 seconds | Fast UX |
| Background (iOS/Android) | 30 seconds | Battery-friendly |
| Screen off | No polling | Use push notification to wake |

### 7.3 Acknowledging Delivered Items

After displaying a verification to the user, acknowledge delivery to prevent re-delivery:

```
POST /api/mobile/verifications/:id/ack
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
```

This is informational only — it prevents the same challenge from appearing in future poll responses. Skipping acknowledgment is safe; expired items are auto-cleaned.

---

## 8. Biometric UX

For `code_pull` challenges, the app can auto-submit the verification code after biometric confirmation, removing the need for the user to manually type it in the browser.

### 8.1 Flow

```
1. Receive code_pull challenge (WebSocket or poll)
2. Extract code from display_data
3. Show: "Approve transaction: 5,000 RSD to Marko Markovic?"
4. Prompt biometric (Face ID / Touch ID / Fingerprint)
5. On biometric success:
   → Sign request with device_secret
   → POST /api/mobile/verifications/:challenge_id/submit { "code": "482916" }
6. On biometric failure:
   → Show code manually: "Enter this code in your browser: 482916"
7. On biometric unavailable (no enrollment):
   → Show code manually (same as step 6)
```

### 8.2 Biometric Binding (Recommended)

For maximum security, bind the `device_secret` to biometric access:

**iOS:**
```swift
let query: [String: Any] = [
    kSecClass: kSecClassGenericPassword,
    kSecAttrAccount: "device_secret",
    kSecValueData: secretData,
    kSecAttrAccessControl: SecAccessControlCreateWithFlags(
        nil,
        .privateKeyUsage,
        .biometryCurrentSet,  // Invalidates if biometrics change
        nil
    )!
]
```

**Android:**
```kotlin
val keyGenParameterSpec = KeyGenParameterSpec.Builder("device_secret_key", PURPOSE_ENCRYPT or PURPOSE_DECRYPT)
    .setUserAuthenticationRequired(true)
    .setUserAuthenticationParameters(0, AUTH_BIOMETRIC_STRONG)
    .build()
```

---

## 9. Error Codes

### 9.1 Authentication Errors

| HTTP | Code | Message | Action |
|------|------|---------|--------|
| 401 | `unauthorized` | Token expired or invalid | Refresh token, retry |
| 401 | `device_deactivated` | Device has been deactivated | Show re-activation screen |
| 401 | `signature_expired` | Timestamp >30s old | Re-sign with fresh timestamp |
| 401 | `signature_invalid` | HMAC mismatch | Check device_secret, re-sign |
| 403 | `device_mismatch` | X-Device-ID doesn't match JWT | Check headers, re-authenticate |

### 9.2 Activation Errors

| HTTP | Code | Message | Action |
|------|------|---------|--------|
| 400 | `validation_error` | Invalid email or code format | Show validation error |
| 404 | `not_found` | No pending activation | Re-request activation code |
| 409 | `business_rule_violation` | Code expired or max attempts reached | Re-request activation code |
| 429 | `rate_limited` | Too many activation requests | Wait and retry |

### 9.3 Verification Errors

| HTTP | Code | Message | Action |
|------|------|---------|--------|
| 404 | `not_found` | Challenge not found or expired | Remove from UI, refresh list |
| 409 | `business_rule_violation` | Wrong code/number, attempts remaining | Show error + new options (number_match) |
| 409 | `business_rule_violation` | Challenge already verified | Remove from UI |
| 409 | `business_rule_violation` | Max attempts exceeded | Show failure message |
| 410 | `expired` | Challenge has expired | Remove from UI |

### 9.4 General Error Response Format

All errors follow this structure:

```json
{
  "error": {
    "code": "validation_error",
    "message": "Human-readable description",
    "details": {
      "field": "additional context if available"
    }
  }
}
```

---

## 10. Device Management

### 10.1 Get Device Info

```
GET /api/mobile/device
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
```

**Response (200):**
```json
{
  "device_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "device_name": "Luka's iPhone 16",
  "status": "active",
  "activated_at": "2026-03-15T10:30:00Z",
  "last_seen_at": "2026-04-01T11:45:00Z"
}
```

### 10.2 Deactivate Device

```
POST /api/mobile/device/deactivate
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
```

**Response (200):**
```json
{
  "message": "device deactivated successfully"
}
```

After deactivation, all tokens are invalidated. The user must re-activate to use the app again.

### 10.3 Transfer to New Device

```
POST /api/mobile/device/transfer
Authorization: Bearer <access_token>
X-Device-ID: <device_id>
Content-Type: application/json

{
  "email": "user@example.com"
}
```

**Response (200):**
```json
{
  "message": "activation code sent, current device will be deactivated on new device activation"
}
```

This sends a new activation code. When the new device activates, the current device is automatically deactivated.

---

## 11. Full Request/Response Examples

### Complete Payment Verification Flow

**Step 1: User creates payment in browser** (browser-side, not mobile)

```
POST /api/me/payments
Authorization: Bearer <browser_access_token>
Content-Type: application/json

{
  "from_account_number": "265000000000000001",
  "to_account_number": "265000000000000099",
  "amount": "5000.00",
  "recipient_name": "Marko Markovic",
  "payment_code": "289",
  "reference_number": "1234567890",
  "payment_purpose": "Invoice payment"
}
```

**Step 2: Browser creates verification challenge** (browser-side)

```
POST /api/verifications
Authorization: Bearer <browser_access_token>
Content-Type: application/json

{
  "source_service": "payment",
  "source_id": 456,
  "method": "code_pull"
}
```

**Step 3: Mobile app receives challenge** (via WebSocket or poll)

```json
{
  "type": "verification_challenge",
  "challenge_id": 789,
  "method": "code_pull",
  "display_data": "{\"code\": \"482916\"}",
  "expires_at": "2026-04-01T12:05:00Z"
}
```

**Step 4a: User types code in browser** (browser submits)

```
POST /api/verifications/789/code
Authorization: Bearer <browser_access_token>
Content-Type: application/json

{
  "code": "482916"
}
```

**Step 4b: OR mobile app auto-submits with biometric** (mobile submits)

```
POST /api/mobile/verifications/789/submit
Authorization: Bearer <mobile_access_token>
X-Device-ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
X-Device-Timestamp: 1743508800
X-Device-Signature: 9f86d081884c7d659a2feaa0c55ad015...
Content-Type: application/json

{
  "challenge_id": 789,
  "code": "482916"
}
```

**Step 5: Browser executes payment** (after verification confirmed)

```
POST /api/me/payments/456/execute
Authorization: Bearer <browser_access_token>
Content-Type: application/json

{
  "challenge_id": 789
}
```

### Complete QR Scan Flow

**Step 1: Browser shows QR code** containing:
```
https://api.exbanka.rs/api/verify/790?token=abc123def456789...
```

**Step 2: Mobile scans QR, extracts URL, signs and POSTs:**

```
POST /api/verify/790
Authorization: Bearer <mobile_access_token>
X-Device-ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
X-Device-Timestamp: 1743508800
X-Device-Signature: d7a8fbb307d7809469ca9abcb0082e4f...
Content-Type: application/json

{
  "token": "abc123def456789..."
}
```

**Response (200):**
```json
{
  "status": "verified"
}
```

---

## 12. Quick Reference

### Headers for All Mobile Requests

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | Always | `Bearer <access_token>` |
| `X-Device-ID` | Always | Device UUID from activation |
| `X-Device-Timestamp` | Signed only | Unix seconds |
| `X-Device-Signature` | Signed only | HMAC-SHA256 hex |
| `Content-Type` | POST/PUT | `application/json` |

### Endpoint Summary

| Method | Path | Auth | Signed | Purpose |
|--------|------|------|--------|---------|
| POST | `/api/mobile/auth/request-activation` | None | No | Start activation |
| POST | `/api/mobile/auth/activate` | None | No | Complete activation |
| POST | `/api/mobile/auth/refresh` | Mobile | No | Refresh tokens |
| GET | `/api/mobile/device` | Mobile | No | Get device info |
| POST | `/api/mobile/device/deactivate` | Mobile | No | Deactivate device |
| POST | `/api/mobile/device/transfer` | Mobile | No | Transfer to new device |
| POST | `/api/mobile/verifications/pending` | Mobile | Yes | Poll pending verifications |
| POST | `/api/mobile/verifications/:id/submit` | Mobile | Yes | Submit verification |
| POST | `/api/verify/:challenge_id` | Mobile | Yes | QR verification |
| GET | `/ws/mobile` | Mobile (query) | No | WebSocket connection |
