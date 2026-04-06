# Limit Blueprints, Biometric Verification & Hierarchy Enforcement Design Spec

**Date:** 2026-04-04
**Scope:** Generalized limit blueprints, mobile biometric verification bypass, role hierarchy enforcement for limit operations, REST_API_v1.md updates

---

## 1. Generalized Limit Blueprints

### 1.1 Current State

Employee limits have a `LimitTemplate` model (user-service) with copy-on-apply semantics — applying a template copies values to an `EmployeeLimit` record with no FK back to the template. Deleting a template doesn't affect applied limits. This behavior is correct.

However:
- Actuary limits (`ActuaryLimit`) have no template/blueprint system
- Client limits (`ClientLimit`) have no template/blueprint system
- The naming ("template") is inconsistent with the broader concept

### 1.2 Design

Introduce a unified `LimitBlueprint` model in user-service that supports three types:

```go
type LimitBlueprint struct {
    ID          uint64          `gorm:"primaryKey;autoIncrement"`
    Name        string          `gorm:"size:100;not null;uniqueIndex:idx_blueprint_name_type"`
    Description string          `gorm:"size:512"`
    Type        string          `gorm:"size:20;not null;uniqueIndex:idx_blueprint_name_type"` // "employee", "actuary", "client"
    Values      datatypes.JSON  `gorm:"type:jsonb;not null"` // type-specific limit values
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

**Type-specific value schemas:**

Employee blueprint values:
```json
{
  "max_loan_approval_amount": "500000",
  "max_single_transaction": "100000",
  "max_daily_transaction": "1000000",
  "max_client_daily_limit": "500000",
  "max_client_monthly_limit": "5000000"
}
```

Actuary blueprint values:
```json
{
  "limit": "1000000",
  "need_approval": true
}
```

Client blueprint values:
```json
{
  "daily_limit": "100000",
  "monthly_limit": "1000000",
  "transfer_limit": "500000"
}
```

### 1.3 Apply Semantics

`ApplyBlueprint(blueprintID, targetID)`:
1. Load blueprint by ID
2. Validate blueprint type matches context (can't apply an employee blueprint to a client)
3. Parse JSON values
4. Copy values into the target's limit record (Upsert)
5. No FK stored — fully decoupled
6. Changelog entry recorded (who applied which blueprint to whom)

### 1.4 Migration from Current Templates

The existing `LimitTemplate` model and `/api/limits/templates` routes stay frozen (backward compatibility). The new `LimitBlueprint` model is separate. A one-time migration seed converts existing templates to blueprints on startup (idempotent).

### 1.5 New V1 Endpoints

All require `AuthMiddleware` + `RequirePermission("limits.manage")` + hierarchy check.

```
GET    /api/v1/blueprints?type=employee|actuary|client   — list blueprints filtered by type
POST   /api/v1/blueprints                                — create blueprint
GET    /api/v1/blueprints/:id                            — get blueprint
PUT    /api/v1/blueprints/:id                            — update blueprint
DELETE /api/v1/blueprints/:id                            — delete blueprint
POST   /api/v1/blueprints/:id/apply                      — apply to target
```

**Apply request body:**
```json
{"target_id": 123}
```

The `target_id` is interpreted based on blueprint type:
- `employee` → employee ID (sets EmployeeLimit)
- `actuary` → employee ID (sets ActuaryLimit)
- `client` → client ID (sets ClientLimit via client-service gRPC)

---

## 2. Biometric Verification Bypass

### 2.1 Current State

Mobile verification (code_pull) works by: app receives code via push → submits code → server validates code matches. Each challenge type has its own submission logic. There is no way to bypass the type-specific flow using device-level authentication.

### 2.2 Design

Allow the mobile app to verify ANY challenge type using biometrics, authenticated by the existing device signature mechanism (HMAC-SHA256 with device secret).

**MobileDevice model change** (auth-service):
```go
type MobileDevice struct {
    // ... existing fields ...
    BiometricsEnabled bool `gorm:"default:false"`
}
```

**Biometrics toggle endpoints:**
```
POST /api/v1/mobile/device/biometrics   {"enabled": true/false}
GET  /api/v1/mobile/device/biometrics   → {"enabled": true/false}
```

Both require `MobileAuthMiddleware` + `RequireDeviceSignature`.

**Biometric verification endpoint:**
```
POST /api/v1/mobile/verifications/:challenge_id/biometric
```

Requires `MobileAuthMiddleware` + `RequireDeviceSignature`. No request body needed — the device signature IS the proof of authentication.

### 2.3 Flow

1. App gets pending challenge (any type: code_pull, email, qr_scan, number_match)
2. App checks device has `BiometricsEnabled == true` (cached locally + confirmed via GET endpoint)
3. App prompts user for local biometric auth (FaceID, fingerprint — app's choice, backend doesn't care)
4. If biometrics succeeds locally, app calls `POST /api/v1/mobile/verifications/:challenge_id/biometric` with headers:
   - `Authorization: Bearer <jwt>`
   - `X-Device-ID: <device_uuid>`
   - `X-Device-Timestamp: <unix_timestamp>`
   - `X-Device-Signature: <hmac_sha256>`
5. Gateway validates device signature via `RequireDeviceSignature` middleware
6. Verification-service receives the call, checks:
   - Challenge exists, is pending, not expired
   - Challenge belongs to this user
   - Device has biometrics enabled (calls auth-service gRPC to check)
7. Marks challenge as verified with `verified_by: "biometric"` for audit
8. Publishes `verification.challenge-verified` Kafka event as normal

### 2.4 When Biometrics Disabled

The `/biometric` endpoint returns `403 Forbidden` with error: "biometrics not enabled for this device". The app must fall back to the normal verification flow for that challenge type.

### 2.5 Auth-Service Changes

New gRPC RPCs:
```protobuf
rpc SetBiometricsEnabled(SetBiometricsRequest) returns (SetBiometricsResponse);
rpc GetBiometricsEnabled(GetBiometricsRequest) returns (GetBiometricsResponse);
rpc CheckBiometricsEnabled(CheckBiometricsRequest) returns (CheckBiometricsResponse);
```

`SetBiometricsEnabled` and `GetBiometricsEnabled` are called by the gateway handlers. `CheckBiometricsEnabled` is called by verification-service internally to validate that biometrics is enabled before accepting a biometric verification.

### 2.6 Verification-Service Changes

New method: `VerifyByBiometric(ctx, challengeID, userID, deviceID) error`

This method:
1. Loads challenge, validates it's pending and belongs to userID
2. Calls auth-service `CheckBiometricsEnabled(deviceID)` — returns error if disabled
3. Marks challenge as verified (same as SubmitVerification success path)
4. Sets `ChallengeData` to include `"verified_by": "biometric"` for audit
5. Publishes Kafka event

No code validation — the device signature at the gateway level IS the authentication.

---

## 3. Role Hierarchy Enforcement

### 3.1 Current State

Zero hierarchy enforcement. Anyone with `limits.manage` permission can change anyone's limits.

### 3.2 Role Rank Mapping

```go
var roleRanks = map[string]int{
    "EmployeeBasic":      1,
    "EmployeeAgent":      2,
    "EmployeeSupervisor": 3,
    "EmployeeAdmin":      4,
}
```

An employee's effective rank is the **maximum rank** across all their assigned roles.

### 3.3 Enforcement Rule

**Caller's max role rank must be strictly greater than target's max role rank.**

- Admin (4) can manage Supervisor (3), Agent (2), Basic (1)
- Supervisor (3) can manage Agent (2), Basic (1)
- Agent (2) can manage Basic (1)
- No one can manage someone at their own rank or above
- No one can modify their own limits

### 3.4 Where Enforced

All enforcement happens in user-service's service layer, using the `changed_by` (caller ID) from gRPC metadata:

| Operation | Service Method | Check |
|-----------|---------------|-------|
| Set employee limits | `LimitService.SetEmployeeLimits` | caller rank > target rank |
| Apply blueprint (employee) | `BlueprintService.ApplyBlueprint` | caller rank > target rank |
| Set actuary limit | `ActuaryService.SetActuaryLimit` | caller rank > target rank |
| Reset actuary used limit | `ActuaryService.ResetUsedLimit` | caller rank > target rank |
| Set actuary need_approval | `ActuaryService.SetNeedApproval` | caller rank > target rank |
| Apply blueprint (actuary) | `BlueprintService.ApplyBlueprint` | caller rank > target rank |
| Set client limits | No change | Already constrained by employee's own limits |
| Apply blueprint (client) | `BlueprintService.ApplyBlueprint` | caller has `limits.manage` (no rank check — clients don't have ranks) |

### 3.5 Implementation

Shared helper in user-service:

```go
func (s *SomeService) checkHierarchy(callerID, targetEmployeeID int64) error {
    caller, err := s.empRepo.GetByIDWithRoles(callerID)
    if err != nil { return status.Error(codes.Internal, "failed to load caller") }
    
    target, err := s.empRepo.GetByIDWithRoles(targetEmployeeID)
    if err != nil { return status.Error(codes.Internal, "failed to load target") }
    
    if callerID == targetEmployeeID {
        return status.Error(codes.PermissionDenied, "cannot modify own limits")
    }
    
    callerRank := maxRoleRank(caller.Roles)
    targetRank := maxRoleRank(target.Roles)
    
    if callerRank <= targetRank {
        return status.Error(codes.PermissionDenied, 
            "insufficient rank to modify this employee's limits")
    }
    return nil
}
```

### 3.6 Error Responses

- Caller rank <= target rank → `403 Forbidden` with `"insufficient rank to modify this employee's limits"`
- Caller == target → `403 Forbidden` with `"cannot modify own limits"`

---

## 4. REST_API_v1.md Updates

The following sections must be added/updated in `docs/api/REST_API_v1.md`:

### New sections to add:
- **Limit Blueprints** — all 6 blueprint endpoints with request/response examples
- **Biometric Verification** — toggle endpoint + biometric verify endpoint
- **Biometric Device Settings** — GET/POST biometrics enabled

### Existing sections to update:
- **Actuary Management** — note that hierarchy enforcement is now active
- **Employee Limits** — note that hierarchy enforcement is now active
- **Mobile Verification** — add biometric verification as an alternative method

---

## 5. Kafka Events

New topics:
- `user.blueprint-created` — when a blueprint is created
- `user.blueprint-applied` — when a blueprint is applied to a target (includes blueprint name, target ID, applied values)
- `user.blueprint-deleted` — when a blueprint is deleted

Existing topics enhanced:
- `verification.challenge-verified` — already exists, now includes `verified_by: "biometric"` in payload when applicable

---

## Non-Goals

- No UI for blueprint management (API only)
- No migration of existing LimitTemplate data to LimitBlueprint (seed convertor runs on startup, but old table stays)
- No biometric verification for browser/desktop (mobile only)
- No changes to existing `/api/` routes (frozen)
- Session management already complete (no changes needed)
