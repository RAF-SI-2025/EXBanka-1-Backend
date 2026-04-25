# EXBanka REST API v3

**Base URL:** `http://<gateway>/api/v3`
**Content-Type:** `application/json`
**Swagger UI:** `http://<gateway>/swagger/index.html`

**Authentication:** Bearer JWT in `Authorization` header — same scheme as v1/v2. Tokens issued by v1/v2 work transparently on v3. v3 does NOT re-host v1/v2 routes; only feature surfaces unique to v3 are listed below. To call a v1 or v2 route, use the matching `/api/v1` or `/api/v2` prefix.

**Common error shape:** identical to v2 — `{"error": {"code": "...", "message": "...", "details": {...}}}`.

---

## Investment Funds (Celina 4)

Supervisor-managed pools of cash + securities. Each fund owns one bank-side RSD account; clients invest RSD (or cross-currency, converted to RSD) and receive a proportional share of fund P&L. The bank itself can hold a position via the `1_000_000_000` user sentinel.

Manager actions require the `funds.manage` permission. Bank-position reads require `funds.bank-position-read`. Browsing, invest, and redeem are open to any authenticated user (clients or employees).

### POST /api/v3/investment-funds — Create fund

**Auth:** `funds.manage`

**Request body:**
```json
{
  "name": "Alpha Fund",
  "description": "IT-focused growth fund",
  "minimum_contribution_rsd": "1000"
}
```

**Responses:**
- `201 Created` — `{"fund": <FundResponse>}`
- `400 validation_error` — name missing or `minimum_contribution_rsd` not a decimal
- `403 forbidden` — caller lacks `funds.manage`
- `409 conflict` — a fund with the same name already exists (active funds; case-insensitive)

### GET /api/v3/investment-funds — List funds

**Auth:** any authenticated user

**Query params:**
- `page` (default `1`)
- `page_size` (default `20`)
- `search` — case-insensitive name substring
- `active_only` — `true` to filter to active funds

**Response (200):**
```json
{ "funds": [<FundResponse>], "total": 12 }
```

### GET /api/v3/investment-funds/{id} — Fund detail

**Auth:** any authenticated user

**Response (200):**
```json
{
  "fund": <FundResponse>,
  "holdings": [<FundHoldingItem>]
}
```

`holdings` is currently always `null` until the position-reads service (Task 20) lands.

### PUT /api/v3/investment-funds/{id} — Update fund

**Auth:** `funds.manage`. The fund-service rejects updates from any actor that is not the fund's `manager_employee_id`.

**Request body — all fields optional; omitted fields stay unchanged:**
```json
{
  "name": "Alpha Plus",
  "description": "...",
  "minimum_contribution_rsd": "2000",
  "active": false
}
```

**Responses:**
- `200 OK` — `{"fund": <FundResponse>}`
- `403 forbidden` — actor isn't the fund manager (and isn't admin yet — admin override is a follow-up)
- `409 conflict` — name change collides with another active fund

### POST /api/v3/investment-funds/{id}/invest — Invest in fund

**Auth:** any authenticated user

**Request body:**
```json
{
  "source_account_id": 4001,
  "amount": "500",
  "currency": "RSD",
  "on_behalf_of_type": "self"
}
```

`on_behalf_of_type` is `"self"` (default) or `"bank"` (for supervisors investing the bank's stake).

Cross-currency invests are accepted; the gateway forwards `currency` to stock-service which calls exchange-service to convert to RSD. The minimum-contribution check is enforced on the *RSD-equivalent* amount.

**Responses:**
- `201 Created` — `{"contribution": <ContributionResponse>}`
- `400 validation_error` — missing `source_account_id`/`amount`/`currency`
- `409 business_rule_violation` — `minimum_contribution_not_met`, fund inactive, or saga compensation triggered

### POST /api/v3/investment-funds/{id}/redeem — Redeem from fund

**Auth:** any authenticated user

**Request body:**
```json
{
  "amount_rsd": "200",
  "target_account_id": 5001,
  "on_behalf_of_type": "self"
}
```

Redemption fee (default `0.005` = 0.5%) is taken from the fund and credited to the bank's RSD account. Bank redeems (`on_behalf_of_type: "bank"`) pay no fee.

**Responses:**
- `201 Created` — `{"contribution": <ContributionResponse>}`
- `409 business_rule_violation` —
  - `insufficient_fund_cash` — fund cash short of `amount_rsd + fee`. Liquidation sub-saga is a follow-up; until it lands, redeems that exceed cash reject.
  - `amount_rsd exceeds position contribution` — caller's recorded contribution can't cover the redemption.

### GET /api/v3/me/investment-funds — Caller's positions

**Auth:** any authenticated user

**Response (200):**
```json
{ "positions": [<PositionItem>] }
```

Returns one entry per fund the caller has contributed to. Derived `current_value_rsd` / `profit_rsd` / `percentage_fund` fields are zeroed until the position-reads service (Task 20) wires fund mark-to-market.

### GET /api/v3/investment-funds/positions — Bank's positions

**Auth:** `funds.bank-position-read`

Same shape as `/me/investment-funds`, scoped to the bank's own contributions (`user_id = 1_000_000_000`, `system_type = "employee"`).

### GET /api/v3/actuaries/performance — Actuary realised-profit summary

**Auth:** `funds.bank-position-read`

**Response (200):**
```json
{ "actuaries": [<ActuaryPerformance>] }
```

Returns realised capital gains aggregated per acting employee. Currently returns an empty list until Task 21 (capital-gain summation + name decoration) lands.

---

## Schemas

### FundResponse
```json
{
  "id": 1,
  "name": "Alpha Fund",
  "description": "IT-focused growth fund",
  "manager_employee_id": 25,
  "manager_full_name": "",
  "minimum_contribution_rsd": "1000",
  "rsd_account_id": 4812,
  "rsd_account_number": "",
  "active": true,
  "created_at": "2026-04-25T12:34:56Z",
  "updated_at": "2026-04-25T12:34:56Z",
  "value_rsd": "",
  "liquid_rsd": "",
  "profit_rsd": ""
}
```

`manager_full_name`, `rsd_account_number`, and the derived `value_rsd` / `liquid_rsd` / `profit_rsd` fields are populated once the position-reads + actuary-decoration services land.

### ContributionResponse
```json
{
  "id": 17,
  "fund_id": 1,
  "direction": "invest",
  "amount_native": "500",
  "native_currency": "RSD",
  "amount_rsd": "500",
  "fx_rate": "",
  "fee_rsd": "0",
  "status": "completed",
  "saga_id": 0
}
```

`fx_rate` is empty for same-currency invest. `direction` is `"invest"` or `"redeem"`. `status` is `"pending" | "completed" | "failed"`.

### PositionItem
```json
{
  "fund_id": 1,
  "fund_name": "Alpha Fund",
  "manager_full_name": "",
  "contribution_rsd": "500",
  "percentage_fund": "",
  "current_value_rsd": "",
  "profit_rsd": "",
  "last_changed_at": "2026-04-25T12:34:56Z"
}
```

### ActuaryPerformance
```json
{
  "employee_id": 25,
  "full_name": "",
  "role": "",
  "realized_profit_rsd": ""
}
```

---

## Inter-Bank 2PC Transfers (Celina 5 / Spec 3)

v3 hosts the public-facing transfer surface that is aware of cross-bank routing. The handler inspects the first three digits of the receiver account number:

- Prefix equals this bank's code (`OWN_BANK_CODE`, default `111`) → falls through to the existing intra-bank handler. Response is unchanged from v1: `201 Created` with the full transfer record.
- Prefix is registered as an active peer in the `banks` table on transaction-service → routed through the inter-bank 2PC handler. Response is `202 Accepted` with a poll URL — the caller must follow up with `GET /api/v3/me/transfers/{transactionId}` until `status` reaches `committed` or a terminal failure.
- Prefix is unknown / inactive → `400 validation_error`.

The intra-bank routes on v1 (`POST /api/v1/me/transfers`) are unchanged and remain intra-bank-only.

### POST /api/v3/me/transfers — Create transfer (intra- or inter-bank aware)

**Auth:** `AnyAuth` (client or employee).

**Request body** (snake_case is canonical; camelCase aliases are accepted for clients that mirror the SI-TX-PROTO 2024/25 wire shape):

```json
{
  "from_account_number": "1110000000001234",
  "to_account_number":   "2220000000005678",
  "amount":              150,
  "currency":            "EUR",
  "memo":                "payment for invoice 42"
}
```

| Field                 | Type    | Required | Notes                                                                                       |
|-----------------------|---------|----------|---------------------------------------------------------------------------------------------|
| `from_account_number` | string  | yes      | Sender's account on this bank.                                                              |
| `to_account_number`   | string  | yes      | Receiver's account. First 3 chars determine the destination bank.                            |
| `amount`              | number  | yes      | Decimal amount in `currency`.                                                               |
| `currency`            | string  | inter-bank only | ISO 4217 3-letter code. Optional for intra-bank (resolved from sender account).      |
| `memo`                | string  | no       | ≤140 chars; included in the inter-bank Prepare envelope.                                     |

`senderAccount`, `receiverAccount`, `amountStr` are accepted as camelCase aliases.

**Response — intra-bank** (`201 Created`): identical shape to v1's transfer object.

**Response — inter-bank** (`202 Accepted`):
```json
{
  "transactionId": "f6d4f2a1-9c3e-4f2b-8c3a-1d1e1f1a1b1c",
  "status":        "preparing",
  "errorReason":   "",
  "pollUrl":       "/api/v3/me/transfers/f6d4f2a1-9c3e-4f2b-8c3a-1d1e1f1a1b1c"
}
```

`status` reflects the InterBankTransaction at the moment `InitiateInterBankTransfer` returned:

- `preparing` — Prepare in flight or awaiting reconciliation.
- `committed` — peer accepted Commit; transfer complete.
- `reconciling` — peer unreachable / 5xx / timeout. Caller should poll the URL.
- `rolled_back` — peer answered NotReady or reconciliation failed; sender credit-back already applied.

**Errors:**
- `400 validation_error` — missing fields, non-positive amount, account currency mismatch.
- `400 validation_error` with `details.code = "unknown_bank"` — `to_account_number` prefix is not in the registry.
- `403 forbidden` — token lacks `transfers.create`.
- `502 downstream_error` — peer rejected; gateway forwards as-is.

### GET /api/v3/me/transfers/{id} — Unified transfer lookup

**Auth:** `AnyAuth`.

`{id}` may be a numeric intra-bank transfer id or a UUID inter-bank `transactionId`. The handler tries the intra-bank `transfers` table first, then falls back to `inter_bank_transactions`.

**Response — intra-bank**: same as `/api/v1/me/transfers/{id}`.

**Response — inter-bank**:
```json
{
  "transactionId":   "f6d4f2a1-...",
  "kind":            "inter_bank_out",
  "role":            "sender",
  "status":          "committed",
  "remoteBankCode":  "222",
  "senderAccount":   "1110000000001234",
  "receiverAccount": "2220000000005678",
  "amount":          "150.0000",
  "currency":        "EUR",
  "finalAmount":     "150.0000",
  "finalCurrency":   "EUR",
  "fxRate":          "1",
  "fees":            "0",
  "errorReason":     "",
  "createdAt":       "2026-04-24T14:15:22Z",
  "updatedAt":       "2026-04-24T14:15:24Z"
}
```

`kind` is `inter_bank_out` for sender rows, `inter_bank_in` for receiver rows. `status` values are the internal enum from Spec 3 §5.3; clients SHOULD map them to a user-facing state:

| Internal status                                                   | User-facing       |
|-------------------------------------------------------------------|-------------------|
| `initiated`, `preparing`                                          | `preparing`       |
| `ready_received`, `committing`                                    | `processing`      |
| `committed`                                                       | `completed`       |
| `notready_received`, `rolled_back`, `abandoned`, `final_notready` | `rejected`        |
| `reconciling`                                                     | `reconciling`     |

### Internal HMAC-authenticated routes (peer banks only)

Peer banks call into the gateway's `/internal/inter-bank/*` routes per the SI-TX-PROTO 2024/25 wire format. These endpoints live OUTSIDE any `/api/v*` prefix and are HMAC-authenticated, NOT JWT — they are not for end users.

| Route                                            | Body                  | Notes                                                                |
|--------------------------------------------------|-----------------------|----------------------------------------------------------------------|
| `POST /internal/inter-bank/transfer/prepare`     | `Prepare` envelope    | Receiver-side validation + reservation. Returns `Ready` / `NotReady`.|
| `POST /internal/inter-bank/transfer/commit`      | `Commit` envelope     | Receiver-side credit. Returns `Committed`. `409 commit_mismatch` if final terms diverge from the prior `Ready`. |
| `POST /internal/inter-bank/check-status`         | `CheckStatus` envelope| Returns `Status` envelope; `404` for unknown `transactionId`.        |

Required headers on every request: `X-Bank-Code`, `X-Bank-Signature` (hex HMAC-SHA256 of body), `X-Idempotency-Key` (= `transactionId`), `X-Timestamp` (RFC3339, ±5 min skew), `X-Nonce` (16-byte hex, single-use within a 10-min window). Mismatched signatures or replayed nonces return `401`. Spec 3 §6 defines the envelope and body shapes; `docs/superpowers/refs/si-tx-proto-mapping.md` is the canonical field-name table.

---

## Intra-bank OTC Options (Celina 4 / Spec 2)

Negotiated option contracts between two parties on the same bank. Sellers write call options on stocks they hold; buyers can accept, counter, or reject. On accept the premium is paid immediately and the seller's shares are reserved against the contract. On exercise (any time before settlement_date), strike funds and shares move atomically. On expiry the seller keeps the premium and the reservation releases.

Trading actions (`POST /otc/offers`, counter, accept, reject, exercise) require **both** `securities.trade` and `otc.trade` permissions. Reads (`GET /otc/offers/{id}`, `GET /otc/contracts/{id}`) require auth and that the caller is a participant in the offer/contract.

### POST /api/v3/otc/offers — Create offer

**Auth:** `securities.trade` + `otc.trade`

**Request body:**
```json
{
  "direction": "sell_initiated",
  "stock_id": 42,
  "quantity": "100",
  "strike_price": "5000",
  "premium": "50000",
  "settlement_date": "2026-05-30",
  "counterparty_user_id": 87,
  "counterparty_system_type": "client"
}
```

`direction` ∈ {`sell_initiated`, `buy_initiated`}. `buy_initiated` requires a named counterparty. Sell-initiated offers are validated against the §4.6 seller invariant (held qty ≥ committed across active offers + contracts).

**Responses:**
- `201 Created` — `{"offer": <OTCOfferResponse>}`
- `400 validation_error` — quantity/strike non-positive, settlement_date in the past, unknown direction, missing counterparty for buy_initiated
- `403 forbidden` — caller lacks `securities.trade` + `otc.trade`
- `409 business_rule_violation` — insufficient available shares for the seller

### POST /api/v3/otc/offers/{id}/counter — Counter

**Request body:** `{ "quantity": "...", "strike_price": "...", "premium": "...", "settlement_date": "..." }`

Quantity changes re-run the seller invariant. Last-mover rule: caller must NOT be the side that last modified the offer.

**Responses:** 200 / 403 (last-mover violation) / 409 (terminal-state offer or insufficient shares).

### POST /api/v3/otc/offers/{id}/accept — Accept (premium-payment saga)

**Request body:**
```json
{
  "buyer_account_id": 5001,
  "seller_account_id": 6001
}
```

Same-currency only at the moment (cross-currency support is a follow-up). Saga: reserve seller's shares + create OptionContract → ReserveFunds(buyer) → PartialSettle(buyer) → CreditAccount(seller) → mark offer ACCEPTED → kafka event.

**Responses:** `201 Created` with `{"offer_id", "contract_id", "status", "saga_id", "contract"}`. `403` for last-mover, `409` for terminal offer / cross-currency.

### POST /api/v3/otc/offers/{id}/reject — Reject

**Responses:** 200 with `{"offer": ...}`, status flips to `REJECTED` and an `otc.offer-rejected` event is published.

### POST /api/v3/otc/contracts/{id}/exercise — Exercise

**Request body:** `{ "buyer_account_id": ..., "seller_account_id": ... }`

Only the contract buyer can exercise, and only while the contract is `ACTIVE` and `settlement_date` is in the future. Saga: ReserveFunds(buyer, strike) → settle → credit seller → ConsumeForOTCContract (seller holding -qty) → upsert buyer's holding (+qty) → mark `EXERCISED` + kafka.

**Responses:** `201 Created` with strike amounts, currencies, and saga_id.

### GET /api/v3/me/otc/offers — Caller's offers

**Query params:** `role` (`initiator`/`counterparty`/`either`, default `either`), `page`, `page_size`.

### GET /api/v3/me/otc/contracts — Caller's contracts

Same shape; role is `buyer`/`seller`/`either`.

### GET /api/v3/otc/offers/{id} — Offer detail + revision history

Returns the offer plus the full append-only revision list. Marks the read-receipt for the caller.

### GET /api/v3/otc/contracts/{id} — Contract detail

Restricted to the buyer or seller.

### Schemas

#### OTCOfferResponse
```json
{
  "id": 1,
  "direction": "sell_initiated",
  "stock_id": 42,
  "stock_ticker": "",
  "quantity": "100",
  "strike_price": "5000",
  "premium": "50000",
  "settlement_date": "2026-05-30",
  "status": "PENDING",
  "initiator": {"user_id": 55, "system_type": "client"},
  "counterparty": {"user_id": 87, "system_type": "client"},
  "last_modified_by": {"user_id": 55, "system_type": "client"},
  "version": 1,
  "unread": false
}
```

`status` ∈ {`PENDING`, `COUNTERED`, `ACCEPTED`, `REJECTED`, `EXPIRED`, `FAILED`}.

#### OptionContractResponse
Carries `id`, `offer_id`, `stock_id`, `quantity`, `strike_price`, `premium_paid`, `premium_currency`, `strike_currency`, `settlement_date`, `status` ∈ {`ACTIVE`, `EXERCISED`, `EXPIRED`, `FAILED`}, plus `buyer` / `seller` party refs and `premium_paid_at` / `exercised_at` / `expired_at` timestamps.
