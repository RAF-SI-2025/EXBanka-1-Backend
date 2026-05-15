# Plan — System Audit Log (Celina 3)

> **Status (2026-05-15):** DEFERRED. Closing the audit-log gap requires
> changelog-emit additions across `user-service` (employee-limit edits +
> used-limit resets), `stock-service` (new `changelogs` table + order
> approve/reject + manual tax-run emits), and a gateway aggregator
> fan-out endpoint. The other Celina-3/4 features (watchlist, price
> alerts, recurring orders, OTC ratings, OTC history, SI-TX status,
> closed-end funds, recurring fund investments) ship first; this plan
> stays as the durable design doc and will land in a follow-up.



**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Scope:** Close the audit-log gaps relative to the requirements. Per-entity `changelogs` already exist in `accounts`, `cards`, `clients`, `loans`, `employees`. This plan adds:

1. New changelog emits for currently-uncovered actions (employee limit edits, used-limit reset, order approve/reject, manual tax run).
2. A new aggregated read endpoint usable by Admin/Supervisor — filter by actor, action type, date range, across all services via fan-out gRPC reads.

---

## Phase 1 — Emit missing changelog entries

### A. Employee limit edits (user-service)

`user-service/internal/service/employee_limit_service.go` — wherever `employee_limits` is upserted, append a `changelog` entry per changed field:

- `entity_type = "employee_limit"`, `entity_id = employee_id`
- `action = "limit_changed"`, `field = "max_loan_approval_amount"` (or whichever)
- `old_value`, `new_value`, `actor_id` (resolved from gRPC `ctx`).

Use the existing `changelog.Entry` contract (`contract/changelog/`).

### B. Used-limit reset (user-service)

Same pattern: action `used_limit_reset`, `old_value=<prior>`, `new_value=0`.

### C. Order approve / reject (stock-service)

In `stock-service/internal/service/order_service.go`:

- `ApproveOrder` — after success, write changelog `entity_type=order`, `entity_id=order_id`, `action=approved`, `actor_id=supervisor_id`.
- `RejectOrder` — symmetric with `action=rejected` and `reason`.

`stock-service` currently has NO changelogs table. Add `stock-service/internal/model/changelog.go` mirroring the user-service shape, with auto-migrate.

### D. Manual tax run (stock-service)

In `tax_service.RunCollection` (whatever the entry point is), changelog `entity_type=system`, `entity_id=0`, `action=tax_run_triggered`, `actor_id=employee_id` from gRPC context.

### Code quality

- Each changelog write happens in the same DB transaction as the underlying mutation (use the existing per-service `db.Transaction` wrapper).
- Failures to write a changelog are logged but do not block the underlying mutation if the changelog table is unreachable (best-effort, matching the existing pattern).

---

## Phase 2 — Aggregated audit-log read endpoint

### Approach

Add a thin "audit aggregator" handler in the api-gateway that fans out gRPC `ListChangelogs` calls to each service in parallel, merges by `created_at desc`, applies cursor pagination.

### Service-side changes

Each service that has a changelog table adds a gRPC method:

```
rpc ListChangelogs(ListChangelogsRequest) returns (ListChangelogsResponse);
```

with filters: `actor_id`, `entity_type`, `action`, `since`, `until`, `limit`. Returns `ChangelogEntry` rows.

Services affected:

- `user-service` (existing changelog table) — add ListChangelogs RPC.
- `account-service`, `card-service`, `client-service`, `credit-service` (existing tables) — add ListChangelogs RPC.
- `stock-service` (new table from Phase 1) — add ListChangelogs RPC.

Each `ListChangelogs` is a thin SELECT with the standard filter clauses.

### Gateway endpoint

`api-gateway/internal/handler/audit_log_handler.go`:

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/audit-log?actor_id=&action=&since=&until=&limit=` | AuthMiddleware | `audit_log.read.all` |

Behavior:

1. Validate query params.
2. Fan out gRPC ListChangelogs to all six services concurrently via `errgroup`.
3. Merge, sort by `created_at desc`, page-cap, return.

Response shape:

```json
{
  "items": [
    {"service": "user", "entity_type": "employee_limit", "entity_id": 42, "action": "limit_changed", "actor_id": 3, "field": "max_loan_approval_amount", "old_value": "10000", "new_value": "20000", "created_at": "..."}
  ],
  "next_cursor": "..."
}
```

### Permissions

`audit_log.read.all` — `EmployeeSupervisor`, `EmployeeAdmin`.

## Tests

Unit:

- For each service: changelog write happens in the same transaction as the underlying mutation; failure-isolation test.
- Gateway aggregator: mocks six gRPC clients, asserts parallel fan-out + merge order + pagination boundaries.

Integration (`test-app/workflows/audit_log_test.go`):

- Admin changes an employee limit → calls `/api/v3/audit-log?actor_id=...` → asserts the entry appears with correct fields.
- Supervisor approves an order → entry appears with `action=approved`.

## Docs

- §17: 1 new route.
- §18: stock-service `changelogs` model.
- §6: 1 new permission.
- §21: audit-log emit invariants per action.
- `docs/api/REST_API_v1.md`: Audit Log section.

## Verification

- `make build`, all service tests pass, `make lint`, integration suite.

## Commits

1. `feat(user-service): emit changelogs for limit/used-limit changes`
2. `feat(stock-service): changelogs table + emits for order approve/reject + manual tax run`
3. `feat(all): ListChangelogs gRPC across user/account/card/client/credit/stock`
4. `feat(api-gateway): /api/v3/audit-log aggregator`
5. `feat(user-service): seed audit_log.read.all`
6. `test: audit-log integration coverage`
7. `docs: audit log spec + REST_API_v1`

All on `Development`.
