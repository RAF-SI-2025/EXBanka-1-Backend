# Design: Owner Type Schema (Spec C)

**Status:** Approved
**Date:** 2026-04-27
**Scope:** Clean-break refactor. Drops the bank sentinel pattern. Schema migrations expected; no data preservation.

## Problem

`BankSentinelUserID = 1_000_000_000` and `system_type` strings are spread across the system as a workaround for "the bank also owns trading resources":

- `api-gateway/internal/handler/validation.go:159,165` — sentinel + bank system-type constants.
- 12 stock-service models carry composite ownership `(user_id, system_type)`.
- 30+ handler call sites across stock, OTC, fund, and tax handlers use one of three helpers: `meIdentity`, `mePortfolioIdentity`, `actingEmployeeID` — picking the wrong one causes silent ownership corruption (we hit this 12 times in the OTC + Fund audits).
- JWT carries `system_type` ("employee" | "client").
- ~40 proto fields carry `system_type` strings.
- Kafka payloads carry `system_type`.
- One model (`client_fund_position.go:11-12`) currently uses `system_type="employee"` for the bank's own fund position — itself a bug, the result of the strings-everywhere model letting any value slip in.

## The Insight: Principal vs Owner Are Two Different Concepts

The previously-documented design (in `MEMORY.md`) called for a single 3-value enum `owner_type ∈ {client, employee, bank}`. The audit revealed this conflates two distinct ideas:

1. **Principal type** — who is the *authenticated user* sending the request? Answers: `client` (logged in via client-login) or `employee` (logged in via employee-login). Drives middleware choice.

2. **Owner type** — who *owns the resource row* in the DB? Answers: `client` or `bank`. **Employees never own trading resources** — they ACT on behalf of either a client or the bank.

The clean design uses **two enums**:
- `principal_type ∈ {client, employee}` — in JWT, in auth tables.
- `owner_type ∈ {client, bank}` — in stock-service, OTC, fund tables (everywhere `system_type` exists today).

The old "identity swap" (the `mePortfolioIdentity` magic) is just the rule "for `/me/*` trading routes: principal=employee → owner=bank, else owner=principal." That rule moves into one middleware, applied per route group.

Auditing employee actions uses a separate `acting_employee_id` column on side-effect rows: nullable, set whenever the principal is an employee, regardless of whether the resolved owner is a client (on-behalf-of-client) or the bank (on-behalf-of-bank).

## Approach

### Schema

For each of the 12 stock-service models that today carry `(user_id, system_type)`:

```sql
-- Replaces (user_id, system_type)
owner_type VARCHAR(8) NOT NULL CHECK (owner_type IN ('client', 'bank')),
owner_id   BIGINT     NULL CHECK (
    (owner_type = 'bank'   AND owner_id IS NULL) OR
    (owner_type = 'client' AND owner_id IS NOT NULL)
),
acting_employee_id BIGINT NULL,
```

Existing indexes on `(user_id, system_type, ...)` become `(owner_type, owner_id, ...)`. Examples:
- `holdings`: unique `(owner_type, owner_id, security_type, security_id)`.
- `orders`: index `(owner_type, owner_id, status, created_at)`.
- `client_fund_positions`: unique `(owner_type, owner_id, fund_id)`.

For OTC offers (composite parties):
```sql
initiator_owner_type           VARCHAR(8) NOT NULL,
initiator_owner_id             BIGINT     NULL,
counterparty_owner_type        VARCHAR(8) NULL,
counterparty_owner_id          BIGINT     NULL,
last_modified_by_principal_type VARCHAR(8) NOT NULL,
last_modified_by_principal_id   BIGINT     NOT NULL,
acting_employee_id              BIGINT     NULL,
```

The "last modified by" stays as a principal (it's an audit field, not an ownership field).

### JWT claims

```go
type Claims struct {
    PrincipalType string   `json:"principal_type"`  // "client" | "employee"
    PrincipalID   uint64   `json:"principal_id"`
    Permissions   []string `json:"permissions"`
    Roles         []string `json:"roles"`
    // ... standard claims
}
```

Renames:
- `system_type` → `principal_type`
- `user_id` → `principal_id`

### Identity middleware (`api-gateway/internal/middleware/identity.go`)

```go
type ResolvedIdentity struct {
    PrincipalType    string   // "client" | "employee"
    PrincipalID      uint64
    OwnerType        string   // "client" | "bank"
    OwnerID          *uint64  // nil iff OwnerType == "bank"
    ActingEmployeeID *uint64  // = &PrincipalID if PrincipalType == "employee", else nil
}

type IdentityRule int
const (
    OwnerIsPrincipal IdentityRule = iota   // /me/profile, /me/cards
    OwnerIsBankIfEmployee                  // /me/orders, /me/portfolios, /me/otc, /me/funds
    OwnerFromURLParam                      // /clients/:client_id/* (admin acts on a specific client)
)

func ResolveIdentity(rule IdentityRule, urlParam ...string) gin.HandlerFunc {
    return func(c *gin.Context) {
        principalType := c.GetString("principal_type")
        principalID   := c.GetUint64("principal_id")

        id := &ResolvedIdentity{PrincipalType: principalType, PrincipalID: principalID}
        if principalType == "employee" {
            empID := principalID
            id.ActingEmployeeID = &empID
        }

        switch rule {
        case OwnerIsPrincipal:
            id.OwnerType = principalType
            ownerID := principalID
            id.OwnerID = &ownerID
        case OwnerIsBankIfEmployee:
            if principalType == "employee" {
                id.OwnerType = "bank"
                id.OwnerID = nil
            } else {
                id.OwnerType = "client"
                ownerID := principalID
                id.OwnerID = &ownerID
            }
        case OwnerFromURLParam:
            paramName := "client_id"
            if len(urlParam) > 0 { paramName = urlParam[0] }
            clientID, err := strconv.ParseUint(c.Param(paramName), 10, 64)
            if err != nil {
                apiError(c, http.StatusBadRequest, "invalid_client_id", "client_id must be a positive integer")
                c.Abort()
                return
            }
            id.OwnerType = "client"
            id.OwnerID = &clientID
        }

        c.Set("identity", id)
        c.Next()
    }
}
```

Handlers consume:
```go
id := c.MustGet("identity").(*ResolvedIdentity)
order := stockpb.PlaceOrderRequest{
    OwnerType:        id.OwnerType,
    OwnerId:          id.OwnerID,           // proto-level: nullable via wrapper or oneof
    ActingEmployeeId: id.ActingEmployeeID,
    // ...
}
```

### Functional behavior preserved

The two on-behalf-of patterns are both expressed via this design:

| User action | Route | Rule | Resolved identity |
|---|---|---|---|
| Employee buys for the bank | `POST /api/v3/me/orders` | `OwnerIsBankIfEmployee` | `owner_type=bank, owner_id=nil, acting_employee_id=<emp>` |
| Employee buys on behalf of a client | `POST /api/v3/clients/:client_id/orders` | `OwnerFromURLParam` | `owner_type=client, owner_id=<client>, acting_employee_id=<emp>` |
| Client buys for themselves | `POST /api/v3/me/orders` | `OwnerIsBankIfEmployee` | `owner_type=client, owner_id=<client>, acting_employee_id=nil` |

Stock-service uses `owner_id` for the holding/order; account debits use the corresponding account; ledger row carries `acting_employee_id` whenever the principal is an employee.

### Actuary-limit gate

The actuary-limit gate (Phase 3 regression source) keys on `acting_employee_id`. It is non-nil whenever the principal is an employee, regardless of resolved owner. The gate fires correctly for both on-behalf-of-client and on-behalf-of-bank trades.

### Proto changes

Define a proto enum:
```proto
enum OwnerType {
    OWNER_TYPE_UNSPECIFIED = 0;
    OWNER_TYPE_CLIENT      = 1;
    OWNER_TYPE_BANK        = 2;
}
```

Every message that has `string system_type = N` becomes:
```proto
OwnerType owner_type = N;
google.protobuf.UInt64Value owner_id = N+1;  // null for bank
optional uint64 acting_employee_id = N+2;
```

Field renames:
- `user_id` → `owner_id` where it represents resource ownership.
- Stays `user_id` only in auth/notification messages where it represents the principal (and even there, prefer `principal_id`).

### Kafka payloads

Same renames as proto. Specifically:
- `OTCParty.SystemType` → `OTCParty.OwnerType` + `OTCParty.OwnerID`
- `AuthSessionCreatedMessage.SystemType` → `.PrincipalType`
- `StockFundInvestedMessage.SystemType` → `.OwnerType`
- `StockFundRedeemedMessage.SystemType` → `.OwnerType`

### Files DELETED

- `api-gateway/internal/handler/validation.go:159` — `BankSentinelUserID`
- `api-gateway/internal/handler/validation.go:165` — `BankSystemType`
- `api-gateway/internal/handler/validation.go:140` — `meIdentity()`
- `api-gateway/internal/handler/validation.go:185` — `mePortfolioIdentity()`
- `api-gateway/internal/handler/validation.go:203` — `actingEmployeeID()`
- All call sites of the three deleted helpers (~30 handler functions across 6 handler files) — rewritten to consume `ResolvedIdentity`.
- The `system_type` column from 12 stock-service models.
- The `user_id` column where it overlaps with `owner_id` (rename in place).
- Tests that hardcoded `BankSentinelUserID`: `validation_test.go:189-227`, `stock_order_handler_test.go:236-281`.

### Files ADDED

- `api-gateway/internal/middleware/identity.go` — `ResolvedIdentity`, `ResolveIdentity`.
- `api-gateway/internal/middleware/identity_test.go` — exhaustive table tests.

### Files MODIFIED

- All proto files in `contract/stockpb/`, `contract/authpb/`, `contract/notificationpb/` that reference `system_type` or composite ownership.
- All 12 stock-service models (`order.go`, `holding.go`, `capital_gain.go`, `tax_collection.go`, `client_fund_position.go`, `otc_offer.go`, `otc_offer_revision.go`, `option_contract.go`, `fund_contribution.go`, `otc_offer_read_receipt.go`, etc.).
- All 12 corresponding repository files.
- `auth-service/internal/service/jwt_service.go:18-31` — Claims renamed.
- `auth-service/internal/service/auth_service.go` — token generation passes `principal_type` / `principal_id`.
- `api-gateway/internal/middleware/auth.go:51-63` — `setTokenContext` stores `principal_type` / `principal_id` (not `system_type` / `user_id`).
- `contract/kafka/messages.go:592, 639, 653, 692` — payload field renames.

### Schema migration

`db.AutoMigrate` handles the new column shape. Per project policy ("if db needs a new schema then make it and delete the old one"), test data is wiped on the next deploy. No production data preservation concern (pre-prod system).

## Test Plan

### Unit tests (`api-gateway/internal/middleware/identity_test.go`)

Table test with 9 cases (3 rules × 3 principals — though `bank` isn't a principal, so 3 × 2 = 6 + 3 invariants):

| Rule | Principal | Expected `owner_type` | Expected `owner_id` | Expected `acting_employee_id` |
|---|---|---|---|---|
| `OwnerIsPrincipal` | client(42) | `"client"` | `&42` | `nil` |
| `OwnerIsPrincipal` | employee(7) | `"employee"` | `&7` | `&7` |
| `OwnerIsBankIfEmployee` | client(42) | `"client"` | `&42` | `nil` |
| `OwnerIsBankIfEmployee` | employee(7) | `"bank"` | `nil` | `&7` |
| `OwnerFromURLParam("client_id")` | client(42) calling /clients/99/orders | `"client"` | `&99` | `nil` |
| `OwnerFromURLParam("client_id")` | employee(7) calling /clients/99/orders | `"client"` | `&99` | `&7` |
| `OwnerFromURLParam("client_id")` | client/employee, missing param | 400 abort | — | — |
| `OwnerFromURLParam("client_id")` | client/employee, non-numeric param | 400 abort | — | — |

### Unit tests (stock-service repositories)

- `holdings`: insert with `owner_type='bank', owner_id=nil` succeeds; insert with `owner_type='bank', owner_id=42` fails the CHECK constraint.
- `orders`: list filtered by `(owner_type, owner_id)` returns only matching rows; mixing owner types in a single query returns disjoint sets.

### Unit tests (validation tests refactored)

- `wf_owner_type_isolation_test.go` (replaces `wf_systemtype_isolation_test.go`):
  - Client A logs in, lists their orders → sees only their owner_type=client+owner_id=A rows.
  - Client B logs in, lists their orders → sees only B's rows. No leakage.
  - Employee logs in, hits /me/orders → sees only owner_type=bank rows.

### Integration tests

- **Actuary-limit regression test** — re-create the Phase 3 bug scenario:
  - Employee with low actuary limit calls `POST /api/v3/me/orders` with a large amount.
  - Gateway resolves identity to `owner_type=bank, acting_employee_id=<emp>`.
  - Stock-service enforces actuary limit on `acting_employee_id`.
  - Expect 403 — confirms the gate fires.
- **On-behalf-of-client trade** — employee calls `POST /api/v3/clients/42/orders`:
  - Gateway resolves `owner_type=client, owner_id=42, acting_employee_id=<emp>`.
  - Account debit is from client 42's account; holding credited to client 42; ledger row carries `acting_employee_id`.

## Out of Scope

- Multi-bank tenancy. This is a single-bank deployment.
- Full row-level audit history (we keep `acting_employee_id` for the *current* state; row history is a future-ideas item).
- Per-employee additional permissions table (`employee_additional_permissions`) — stays as-is; orthogonal to ownership.
