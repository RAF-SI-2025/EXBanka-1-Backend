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
