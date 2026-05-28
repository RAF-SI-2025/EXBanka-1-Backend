# Plan D — Global Audit Log Read Endpoints

> **For agentic workers:** Use superpowers:subagent-driven-development.

**Goal:** Admins can read every audit/changelog table without specifying an entity. Each table becomes a paginated, filterable list.

**Architecture:** New `/api/v3/admin/audit/...` gateway routes; each route fans out to one service's changelog repo (or notification-service's `admin_audit_logs` for the cron-action audit). One new permission `admin.audit.view` on EmployeeAdmin only.

**Files touched:**
- `contract/permissions/catalog.yaml` + regen
- `contract/proto/<each-service>.proto` — add `ListAllChangelogs` RPC per service
- Per-service handler + repo `ListAll` method
- `api-gateway/internal/handler/admin_audit_handler.go` (new)
- `api-gateway/internal/router/router_v3.go`
- `notification-service` — read endpoint on `admin_audit_logs`

---

## Routes (all `Auth + RequirePermission("admin.audit.view")`)

```
GET /api/v3/admin/audit/clients-changelog       — client-service
GET /api/v3/admin/audit/accounts-changelog      — account-service
GET /api/v3/admin/audit/cards-changelog         — card-service
GET /api/v3/admin/audit/loans-changelog         — credit-service
GET /api/v3/admin/audit/employees-changelog     — user-service
GET /api/v3/admin/audit/cron-actions            — notification-service
```

**Common query params:** `page` (default 1), `page_size` (default 50, max 200), `since=YYYY-MM-DD`, `until=YYYY-MM-DD`, `actor_id` (employee_id), `action` (string filter).

**Response shape:**
```json
{
  "entries": [
    {
      "id": 123,
      "entity_type": "client",
      "entity_id": 42,
      "action": "updated",
      "field": "first_name",
      "old_value": "Marko",
      "new_value": "Marija",
      "actor_id": 7,
      "timestamp": "2026-05-28T10:00:00Z"
    }
  ],
  "total": 1234,
  "page": 1,
  "page_size": 50
}
```

For `/admin/audit/cron-actions` the shape mirrors `AdminAuditLog`.

---

## Task D1: Add `admin.audit.view` permission

- Edit `contract/permissions/catalog.yaml` — add `admin.audit.view`
- Regenerate `perms.gen.go` (same mechanism as prior `admin.crons.*`)
- EmployeeAdmin gets it via wildcard `grants: ["*"]`
- Commit.

## Task D2: Add `ListAllChangelogs` RPC per service

For each of: client-service, account-service, card-service, credit-service, user-service:

- Repository method `ListAll(filters Filters, page, pageSize) ([]ChangelogEntry, int64, error)` — returns paginated, descending by created_at.
- Service method `ListAllChangelogs(ctx, filters, page, pageSize)`.
- gRPC RPC `ListAllChangelogs(ListAllChangelogsRequest) returns (ListAllChangelogsResponse)`.
- Response messages must include `total` for pagination.
- One commit per service.

## Task D3: Add `ListAdminAuditLogs` RPC on notification-service

- `notification-service/internal/repository/admin_audit_log_repository.go` (new) — `ListAll(filters, page, pageSize)`.
- gRPC RPC on notification-service.
- Wire in `cmd/main.go`.
- Commit.

## Task D4: Gateway aggregator handlers + routes

- `api-gateway/internal/handler/admin_audit_handler.go` — 6 handlers, one per route.
- Each handler decodes the common query params, validates them with existing helpers (`positiveInt`, ISO date parsers, `oneOf` for known actions).
- Calls the corresponding service-gRPC method.
- `api-gateway/internal/router/router_v3.go` — register the 6 routes under a new `auditAdmin := protected.Group("/admin/audit"); auditAdmin.Use(middleware.RequirePermission(perms.Admin.Audit.View))`.
- Swagger annotations on each route + `make swagger`.
- Commit.

## Task D5: REST_API + Specification docs

- `docs/api/REST_API_v3.md` — new "Admin / Audit Logs" section documenting all 6 routes.
- `docs/Specification.md` §17 + §6.
- Commit.

## Tests

- Unit tests on the new `ListAll` repo method for each service (filters compose, pagination respects total, date range excludes out-of-range rows).
- Gateway handler tests (query-param validation, gRPC error mapping).

## Out of scope (deferred)

- Unified aggregator that joins all 6 sources in one endpoint (`docs/superpowers/plans/2026-05-15-req-audit-log.md` still tracks this).
- Filling in missing changelog emits (covered by the deferred req-audit-log plan).
