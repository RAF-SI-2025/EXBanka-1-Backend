# Design: Typed Permission Schema (Spec D)

**Status:** Approved
**Date:** 2026-04-27
**Scope:** Clean-break refactor. Naming convention applied uniformly. Catalog immutable; role mappings runtime-editable.

## Problem

168 permission strings (audit count, not the 145 in MEMORY) are hand-edited across:

- `user-service/internal/service/role_service.go` lines 30-244 (the 168-element `AllPermissions` slice).
- 4 role builder functions (`basicPermissions`, `agentPermissions`, `supervisorPermissions`, `adminPermissions`).
- ~77 `RequirePermission(...)` / `RequireAnyPermission(...)` call sites in routers.
- Test fixtures with magic strings.
- JWT serialization.

Naming inconsistencies:
- Hyphens vs dots: `bank-accounts.manage` vs `clients.create.all`.
- Two-segment vs three-segment: `clients.create` vs `orders.read.all`.
- Umbrella codes coexist with granular: `clients.read` AND `clients.read.all` AND `clients.read.assigned`. Roles assign all three "for backwards compat."

Adding a new route requires editing three places (router gate, role seed, tests) with no compile-time check.

## Constraints (from user)

1. **All permissions follow ONE consistent naming form.**
2. **All permissions in YAML.**
3. **Admin must dynamically change what each role can access at runtime — but only assign/remove existing permissions, NOT create/delete permissions themselves.**
4. **Permissions must be specific (granular).** No umbrella codes.

## Approach

### Naming convention (uniform, enforced at codegen)

Every permission has the shape **`<resource>.<verb>.<scope>`** — three segments, always.

- All segments: `[a-z][a-z0-9_]*` (lowercase, snake_case, underscores allowed inside a segment).
- No hyphens. Compound resource names use snake_case: `bank_accounts.create.any`, not `bank-accounts.create.any`.
- Where no narrower scope exists, scope is `any`. Examples: `clients.create.any`, `cards.block.any`.
- Closed scope vocabulary: `any`, `all`, `own`, `assigned`. (Codegen warns on unknown scope but does not reject — extension is allowed if intentional.)
- Codegen regex: `^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`. Build fails on violation.

### YAML catalog (`contract/permissions/catalog.yaml`)

Single hand-edited source of truth for what permissions EXIST. Plus default role mappings used only for first-startup seeding.

```yaml
permissions:
  # clients
  - clients.create.any
  - clients.read.all
  - clients.read.assigned
  - clients.read.own
  - clients.update.profile
  - clients.update.contact
  - clients.update.limits

  # accounts
  - accounts.create.current
  - accounts.create.foreign
  - accounts.read.all
  - accounts.read.own
  - accounts.update.name
  - accounts.update.limits

  # bank accounts
  - bank_accounts.manage.any

  # orders
  - orders.read.all
  - orders.read.own
  - orders.place.own
  - orders.place.on_behalf_client
  - orders.place.on_behalf_bank
  - orders.cancel.own
  - orders.cancel.all

  # cards
  - cards.create.physical
  - cards.create.virtual
  - cards.block.any
  - cards.unblock.any
  - cards.approve.physical
  - cards.approve.virtual

  # credits
  - credits.approve.cash
  - credits.approve.housing
  - credits.disburse.any

  # securities
  - securities.read.holdings_all
  - securities.read.holdings_own
  - securities.manage.catalog
  - securities.trade.any

  # OTC
  - otc.trade.accept
  - otc.trade.exercise
  - otc.trade.expire

  # employees
  - employees.create.any
  - employees.read.all
  - employees.update.any
  - employees.roles.assign
  - employees.permissions.assign

  # roles
  - roles.read.all
  - roles.update.any
  - roles.permissions.assign
  - roles.permissions.revoke

  # limits
  - limits.employee.read
  - limits.employee.update
  - limit_templates.create.any
  - limit_templates.update.any

  # fees
  - fees.create.any
  - fees.update.any

  # verification
  - verification.skip.any
  - verification.manage.any

  # ... target ~140 entries total

default_roles:
  EmployeeBasic:
    grants:
      - clients.read.assigned
      - accounts.read.own
      - cards.create.virtual
      - cards.block.any
      - cards.unblock.any
      - orders.read.own

  EmployeeAgent:
    inherits: [EmployeeBasic]
    grants:
      - clients.create.any
      - clients.update.profile
      - clients.update.contact
      - accounts.create.current
      - accounts.create.foreign
      - cards.create.physical
      - orders.place.own
      - orders.place.on_behalf_client
      - securities.trade.any
      - securities.read.holdings_own
      - otc.trade.accept
      - otc.trade.exercise

  EmployeeSupervisor:
    inherits: [EmployeeAgent]
    grants:
      - clients.read.all
      - clients.update.limits
      - accounts.read.all
      - accounts.update.limits
      - orders.read.all
      - orders.place.on_behalf_bank
      - orders.cancel.all
      - cards.approve.physical
      - cards.approve.virtual
      - credits.approve.cash
      - credits.approve.housing
      - credits.disburse.any
      - securities.read.holdings_all
      - securities.manage.catalog
      - bank_accounts.manage.any
      - verification.skip.any
      - verification.manage.any

  EmployeeAdmin:
    inherits: [EmployeeSupervisor]
    grants:
      - "*"   # special: all permissions in the catalog
```

### Codegen output (`contract/permissions/perms.gen.go`)

Generated by `make permissions` (`Makefile` adds the target alongside `make proto`). Regenerate after every YAML edit.

```go
// Code generated by perm-codegen. DO NOT EDIT.
package permissions

type Permission string

func (p Permission) String() string { return string(p) }

// Catalog is the immutable set of all valid permissions.
// Every grant in the DB must reference a permission in this set.
var Catalog = []Permission{
    "clients.create.any",
    "clients.read.all",
    "clients.read.assigned",
    // ... every permission from YAML
}

var catalogSet = func() map[Permission]struct{} {
    m := make(map[Permission]struct{}, len(Catalog))
    for _, p := range Catalog { m[p] = struct{}{} }
    return m
}()

// IsValid returns true iff p is in the catalog.
func IsValid(p Permission) bool { _, ok := catalogSet[p]; return ok }

// Typed groupings — for compile-time safety in router middleware and tests.
var Clients = struct {
    Create struct{ Any Permission }
    Read   struct{ All, Assigned, Own Permission }
    Update struct{ Profile, Contact, Limits Permission }
}{
    Create: struct{ Any Permission }{Any: "clients.create.any"},
    Read:   struct{ All, Assigned, Own Permission }{
        All: "clients.read.all", Assigned: "clients.read.assigned", Own: "clients.read.own",
    },
    Update: struct{ Profile, Contact, Limits Permission }{
        Profile: "clients.update.profile", Contact: "clients.update.contact", Limits: "clients.update.limits",
    },
}

var Orders = struct {
    Read   struct{ All, Own Permission }
    Place  struct{ Own, OnBehalfClient, OnBehalfBank Permission }
    Cancel struct{ Own, All Permission }
}{...}

// ... one var per resource

// DefaultRoles is the seed for the role_permissions table on FIRST startup
// (when the table is empty). After that, DB is authoritative — admins manage
// role membership via the API.
var DefaultRoles = map[string][]Permission{
    "EmployeeBasic":      {Clients.Read.Assigned, Accounts.Read.Own, ...},
    "EmployeeAgent":      {/* flattened union of EmployeeBasic + agent grants */},
    "EmployeeSupervisor": {/* flattened union */},
    "EmployeeAdmin":      Catalog, // "*" expands to full catalog
}
```

The codegen flattens `inherits:` transitively, dedupes, and validates that every grant string is in the catalog.

### Runtime behavior

1. **First startup (empty `role_permissions` table):** `user-service` seeds from `permissions.DefaultRoles`. Each role gets its flattened permission set inserted as `(role_id, permission_string)` rows.

2. **Catalog drift check on every startup:** Walk every row in `role_permissions`. For each row, assert `permissions.IsValid(perm)`. Orphans (DB has a permission not in catalog — typically because YAML removed it) are logged at WARN level: `WARN: role X has orphan permission Y; admin can remove it but new grants are blocked`. Existing grants continue to work; the JWT will still carry the orphan permission. **Orphans are NOT auto-cleaned** — silent revocation is too dangerous.

3. **Admin grants permission to role:** `POST /api/v3/roles/:name/permissions` with `{"permission": "orders.place.on_behalf_bank"}`.
   - Server validates the permission against `permissions.Catalog`. Reject with 400 `permission_not_in_catalog` if not present.
   - Server validates the role exists. Reject with 404 if not.
   - Insert `(role_id, permission)` row. Idempotent (UNIQUE constraint; conflict → 200 OK with no-op).

4. **Admin revokes permission from role:** `DELETE /api/v3/roles/:name/permissions/:permission`.
   - Server validates the role exists.
   - Delete the row. Idempotent (no-op if not present).
   - Emit Kafka event `role.permission-revoked` so other services can invalidate cached role-permission maps.

5. **Existing JWTs continue to grant the old permissions until they expire** (max 15 minutes). No live-revocation against active tokens; the user must wait for token refresh. (Live revocation is an existing facility for sessions, separate concern.)

6. **Router middleware:**
   ```go
   r.POST("/api/v3/me/orders",
       middleware.RequirePermission(permissions.Orders.Place.Own),
       handler.PlaceMyOrder)
   ```
   `RequirePermission` signature changes from `(string)` to `(permissions.Permission)`. Code that passes a magic string fails to compile.

7. **JWT wire format unchanged:** Still `permissions []string`. The typed layer is compile-time only. Cross-service consumers (or the frontend) parsing JWT continue to see strings.

### Files DELETED

- `user-service/internal/service/role_service.go` lines 30-244 (the 168-element `AllPermissions` slice).
- `basicPermissions()`, `agentPermissions()`, `supervisorPermissions()`, `adminPermissions()` functions.
- All magic-string permissions in `api-gateway/internal/router/router_v3.go` — replaced with typed constants.
- All magic-string permissions in test files.

### Files ADDED

- `contract/permissions/catalog.yaml`
- `contract/permissions/perms.gen.go` (codegen output)
- `tools/perm-codegen/main.go` (the codegen tool itself, ~150 LOC)
- `Makefile` target `permissions:` invoked alongside `proto:`

### Files MODIFIED

- `user-service/internal/service/role_service.go` — `SeedRolesAndPermissions` becomes ~30 lines (loop over `permissions.DefaultRoles`).
- `user-service/internal/service/role_service.go` — admin endpoints: `AssignPermissionToRole`, `RevokePermissionFromRole` validate against `permissions.Catalog`.
- `api-gateway/internal/middleware/auth.go` — `RequirePermission` typed signature.
- `api-gateway/internal/router/router_v3.go` — every permission gate uses typed constants.
- `auth-service/internal/service/auth_service.go` — emits Kafka event on role permission changes (or this lives in user-service — confirm during implementation).

## Test Plan

### Unit tests (codegen)

- `tools/perm-codegen` table tests:
  - Reject names that violate the regex (`Clients.create`, `clients-read-all`, `clients..read`, `clients.read`).
  - Reject duplicate permission entries.
  - Reject `inherits:` to a nonexistent role.
  - Reject cycle in `inherits:` graph.
  - Reject `grants:` referencing a permission not in `permissions:`.
  - Generate deterministic output for a fixture catalog (golden test).

### Unit tests (runtime)

- `permissions.IsValid` returns true for catalog entries, false otherwise.
- Catalog-drift check correctly identifies orphan permissions in DB.
- Admin grant rejects permissions not in catalog (400).
- Admin grant on existing (role, permission) is idempotent (200, no error).
- Admin revoke on missing (role, permission) is idempotent.

### Integration tests

- Existing role-permission API tests rewritten with typed constants.
- Add a "drift workflow" test:
  - Catalog has `foo.bar.baz`. Admin grants it to a role. Test asserts grant succeeded.
  - Catalog removes `foo.bar.baz` (simulate via test setup that overrides catalog).
  - Restart asserts WARN log appears.
  - Existing role still has the grant in DB.
  - New `POST /api/v3/roles/:name/permissions` with `foo.bar.baz` rejects with 400.
- "Admin manages role" workflow:
  - Login as admin. POST a permission to a role. Login as a user with that role. Assert JWT carries the new permission. Assert middleware-gated endpoint becomes accessible.

## Out of Scope

- Per-employee additional permissions table (`employee_additional_permissions`). Stays as-is — it's an additive override on top of role.
- Permission inheritance at runtime (e.g., `clients.read.all` implies `clients.read.assigned`). All permissions are flat. If admin wants both, admin grants both.
- Web admin UI for editing the YAML catalog. YAML is engineering-managed.
- Live revocation of active JWTs on permission change (separate session-revoke spec).
- Permission analytics ("which permissions are unused").
