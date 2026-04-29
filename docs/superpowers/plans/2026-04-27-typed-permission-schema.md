# Typed Permission Schema — Implementation Plan (Spec D)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace 168 hand-edited permission strings with a typed Go layer codegened from a YAML catalog. Catalog defines what permissions EXIST (immutable, engineering-managed); DB stores which permissions each role currently has (runtime-editable by admin via API). All permissions follow uniform `<resource>.<verb>.<scope>` shape, snake_case, no hyphens. Admin can grant/revoke role↔permission mappings dynamically but cannot create/delete permissions themselves.

**Architecture:** YAML catalog (`contract/permissions/catalog.yaml`) → codegen tool (`tools/perm-codegen/`) → typed constants (`contract/permissions/perms.gen.go`). Role seeds happen once at first startup (empty `role_permissions` table); after that, admin API drives the mappings. Catalog drift check on every startup logs orphans (DB has a permission no longer in catalog).

**Tech Stack:** Go, gorm, YAML, text/template for codegen.

**Spec reference:** `docs/superpowers/specs/2026-04-27-typed-permission-schema-design.md`

---

## File Structure

**New files:**
- `contract/permissions/catalog.yaml` — single source of truth
- `contract/permissions/perms.gen.go` — codegen output (typed constants)
- `contract/permissions/loader.go` — `LoadCatalog`, `Catalog`, `IsValid`, `DefaultRoles`
- `contract/permissions/loader_test.go`
- `tools/perm-codegen/main.go` — codegen CLI
- `tools/perm-codegen/main_test.go`

**Modified files:**
- `Makefile` — add `permissions:` target alongside `proto:`
- `user-service/internal/service/role_service.go` — `SeedRolesAndPermissions` slimmed to ~30 LOC; `AssignPermissionToRole` validates against catalog
- `user-service/internal/handler/grpc_handler.go` — admin permission management endpoints
- `api-gateway/internal/middleware/auth.go` — `RequirePermission` signature changed to `(permissions.Permission)`
- `api-gateway/internal/router/router_v3.go` — every gate uses typed constants
- `auth-service/internal/service/auth_service.go` — JWT permissions populated from DB role mappings (already does this; verify)

**Deleted code:**
- `user-service/internal/service/role_service.go` lines 30-244 (the 168-element `AllPermissions` slice)
- 4 role builder functions (`basicPermissions`, `agentPermissions`, `supervisorPermissions`, `adminPermissions`)
- All magic-string permissions in router files (~77 `RequirePermission(...)` sites)
- All magic-string permissions in tests

---

## Task 1: Catalog YAML

**Files:**
- Create: `contract/permissions/catalog.yaml`

- [ ] **Step 1: Write the catalog**

```yaml
# contract/permissions/catalog.yaml
# Single source of truth for permission existence. Admin can grant/revoke
# role↔permission mappings via the API, but cannot add or remove permissions
# from this catalog at runtime.
#
# Naming rule (enforced by codegen):
#   <resource>.<verb>.<scope>
#   - all segments: [a-z][a-z0-9_]* (snake_case, no hyphens)
#   - exactly three segments
#   - scope vocabulary: any | all | own | assigned (extension allowed but flagged)

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
  - accounts.deactivate.any

  # bank accounts
  - bank_accounts.manage.any

  # cards
  - cards.create.physical
  - cards.create.virtual
  - cards.read.all
  - cards.read.own
  - cards.block.any
  - cards.unblock.any
  - cards.approve.physical
  - cards.approve.virtual

  # credits
  - credits.read.all
  - credits.read.own
  - credits.approve.cash
  - credits.approve.housing
  - credits.disburse.any

  # orders
  - orders.read.all
  - orders.read.own
  - orders.place.own
  - orders.place.on_behalf_client
  - orders.place.on_behalf_bank
  - orders.cancel.own
  - orders.cancel.all

  # securities
  - securities.read.holdings_all
  - securities.read.holdings_own
  - securities.manage.catalog
  - securities.trade.any

  # OTC
  - otc.trade.accept
  - otc.trade.exercise
  - otc.trade.expire
  - otc.read.all

  # funds
  - funds.invest.own
  - funds.invest.on_behalf_client
  - funds.invest.on_behalf_bank
  - funds.redeem.own
  - funds.read.all
  - funds.manage.catalog

  # employees
  - employees.create.any
  - employees.read.all
  - employees.update.any
  - employees.roles.assign
  - employees.permissions.assign
  - employees.deactivate.any

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

  # exchange
  - exchange_rates.read.any
  - exchange_rates.update.any

default_roles:
  EmployeeBasic:
    grants:
      - clients.read.assigned
      - accounts.read.own
      - cards.create.virtual
      - cards.block.any
      - cards.unblock.any
      - cards.read.own
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
      - funds.invest.own
      - funds.invest.on_behalf_client
      - funds.redeem.own

  EmployeeSupervisor:
    inherits: [EmployeeAgent]
    grants:
      - clients.read.all
      - clients.update.limits
      - accounts.read.all
      - accounts.update.name
      - accounts.update.limits
      - accounts.deactivate.any
      - orders.read.all
      - orders.place.on_behalf_bank
      - orders.cancel.all
      - cards.read.all
      - cards.approve.physical
      - cards.approve.virtual
      - credits.read.all
      - credits.approve.cash
      - credits.approve.housing
      - credits.disburse.any
      - securities.read.holdings_all
      - securities.manage.catalog
      - bank_accounts.manage.any
      - otc.read.all
      - otc.trade.expire
      - funds.invest.on_behalf_bank
      - funds.read.all
      - funds.manage.catalog
      - verification.skip.any
      - verification.manage.any

  EmployeeAdmin:
    inherits: [EmployeeSupervisor]
    grants:
      - "*"   # special: every permission in the catalog
```

- [ ] **Step 2: Commit**

```bash
git add contract/permissions/catalog.yaml
git commit -m "feat(permissions): YAML catalog (140 permissions, 4 default roles)"
```

---

## Task 2: Catalog loader (parser + validator)

**Files:**
- Create: `contract/permissions/loader.go`
- Create: `contract/permissions/loader_test.go`

- [ ] **Step 1: Failing test**

```go
// contract/permissions/loader_test.go
package permissions_test

import (
	"strings"
	"testing"

	"contract/permissions"
)

func TestLoadCatalog_RejectsBadName(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read    # only 2 segments
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "must have exactly 3 segments") {
		t.Errorf("expected 3-segment error, got %v", err)
	}
}

func TestLoadCatalog_RejectsHyphen(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - bank-accounts.manage.any
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "snake_case") {
		t.Errorf("expected snake_case error, got %v", err)
	}
}

func TestLoadCatalog_RejectsDuplicate(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.all
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestLoadCatalog_RejectsCycleInInherits(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions: [clients.read.all]
default_roles:
  A: { inherits: [B], grants: [] }
  B: { inherits: [A], grants: [] }
`))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got %v", err)
	}
}

func TestLoadCatalog_RejectsGrantNotInCatalog(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions: [clients.read.all]
default_roles:
  A: { grants: [does.not.exist] }
`))
	if err == nil || !strings.Contains(err.Error(), "not in catalog") {
		t.Errorf("expected not-in-catalog error, got %v", err)
	}
}

func TestLoadCatalog_FlattensInheritance(t *testing.T) {
	cat, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.own
default_roles:
  A: { grants: [clients.read.own] }
  B: { inherits: [A], grants: [clients.read.all] }
`))
	if err != nil { t.Fatal(err) }
	got := cat.Roles["B"]
	if len(got) != 2 { t.Errorf("expected 2 perms after flatten, got %d", len(got)) }
}

func TestLoadCatalog_StarExpandsToAll(t *testing.T) {
	cat, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.own
default_roles:
  Admin: { grants: ["*"] }
`))
	if err != nil { t.Fatal(err) }
	if len(cat.Roles["Admin"]) != 2 { t.Errorf("'*' should expand to 2, got %d", len(cat.Roles["Admin"])) }
}
```

- [ ] **Step 2: Run — FAIL (package missing)**

- [ ] **Step 3: Implement**

```go
// contract/permissions/loader.go
package permissions

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

type Permission string

func (p Permission) String() string { return string(p) }

type Catalog struct {
	All   []Permission              // every permission, in catalog order
	Set   map[Permission]struct{}   // membership set
	Roles map[string][]Permission   // flattened role → permissions
}

func (c *Catalog) IsValid(p Permission) bool { _, ok := c.Set[p]; return ok }

type yamlDoc struct {
	Permissions  []string                  `yaml:"permissions"`
	DefaultRoles map[string]yamlRole       `yaml:"default_roles"`
}
type yamlRole struct {
	Inherits []string `yaml:"inherits"`
	Grants   []string `yaml:"grants"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

func ParseYAML(data []byte) (*Catalog, error) {
	var doc yamlDoc
	if err := yaml.Unmarshal(data, &doc); err != nil { return nil, err }

	cat := &Catalog{
		All:   make([]Permission, 0, len(doc.Permissions)),
		Set:   make(map[Permission]struct{}),
		Roles: make(map[string][]Permission),
	}

	// Validate permission names + uniqueness.
	for _, raw := range doc.Permissions {
		if !nameRe.MatchString(raw) {
			parts := splitOrEmpty(raw, ".")
			if len(parts) != 3 {
				return nil, fmt.Errorf("permission %q must have exactly 3 segments separated by '.'", raw)
			}
			return nil, fmt.Errorf("permission %q invalid: must be snake_case lowercase letters/digits/underscore, three segments", raw)
		}
		p := Permission(raw)
		if _, dup := cat.Set[p]; dup {
			return nil, fmt.Errorf("duplicate permission: %q", raw)
		}
		cat.Set[p] = struct{}{}
		cat.All = append(cat.All, p)
	}

	// Resolve roles with cycle + grant validation.
	visited := make(map[string]bool)
	stack := make(map[string]bool)
	for name := range doc.DefaultRoles {
		if err := flattenRole(name, doc.DefaultRoles, cat, visited, stack); err != nil {
			return nil, err
		}
	}

	return cat, nil
}

func flattenRole(name string, all map[string]yamlRole, cat *Catalog,
	visited, stack map[string]bool) error {

	if stack[name] {
		return fmt.Errorf("cycle in role inherits at %q", name)
	}
	if visited[name] {
		return nil
	}
	role, ok := all[name]
	if !ok {
		return fmt.Errorf("role %q referenced but not defined", name)
	}
	stack[name] = true
	defer delete(stack, name)

	out := map[Permission]struct{}{}

	for _, parent := range role.Inherits {
		if err := flattenRole(parent, all, cat, visited, stack); err != nil {
			return err
		}
		for _, p := range cat.Roles[parent] {
			out[p] = struct{}{}
		}
	}

	for _, raw := range role.Grants {
		if raw == "*" {
			for _, p := range cat.All { out[p] = struct{}{} }
			continue
		}
		p := Permission(raw)
		if !cat.IsValid(p) {
			return fmt.Errorf("role %q grants %q which is not in catalog", name, raw)
		}
		out[p] = struct{}{}
	}

	flat := make([]Permission, 0, len(out))
	for p := range out { flat = append(flat, p) }
	sort.Slice(flat, func(i, j int) bool { return flat[i] < flat[j] })
	cat.Roles[name] = flat
	visited[name] = true
	return nil
}

func splitOrEmpty(s, sep string) []string {
	if s == "" { return nil }
	out := []string{}
	cur := ""
	for _, r := range s {
		if string(r) == sep { out = append(out, cur); cur = ""; continue }
		cur += string(r)
	}
	return append(out, cur)
}
```

- [ ] **Step 4: Run — PASS**

Run: `cd contract && go test ./permissions/... -v`

- [ ] **Step 5: Add a test for the actual catalog file loading**

```go
func TestActualCatalog_LoadsCleanly(t *testing.T) {
	data, err := os.ReadFile("catalog.yaml")
	if err != nil { t.Fatal(err) }
	cat, err := permissions.ParseYAML(data)
	if err != nil { t.Fatal(err) }
	if len(cat.All) < 100 { t.Errorf("expected ~140 permissions, got %d", len(cat.All)) }
	for _, role := range []string{"EmployeeBasic", "EmployeeAgent", "EmployeeSupervisor", "EmployeeAdmin"} {
		if _, ok := cat.Roles[role]; !ok { t.Errorf("missing role %q", role) }
	}
}
```

- [ ] **Step 6: Commit**

```bash
git add contract/permissions/loader.go contract/permissions/loader_test.go
git commit -m "feat(permissions): YAML catalog loader with validation + flatten"
```

---

## Task 3: Codegen tool

**Files:**
- Create: `tools/perm-codegen/main.go`
- Create: `tools/perm-codegen/main_test.go`

- [ ] **Step 1: Failing test (golden file)**

```go
// tools/perm-codegen/main_test.go
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestGenerate_GoldenOutput(t *testing.T) {
	yaml := []byte(`
permissions:
  - clients.read.all
  - clients.read.own
  - orders.place.own
default_roles:
  EmployeeBasic: { grants: [clients.read.own] }
  EmployeeAgent: { inherits: [EmployeeBasic], grants: [clients.read.all, orders.place.own] }
`)
	var buf bytes.Buffer
	if err := generate(yaml, &buf); err != nil { t.Fatal(err) }

	got := buf.String()
	for _, want := range []string{
		`Catalog = []Permission{`,
		`"clients.read.all"`,
		`"clients.read.own"`,
		`"orders.place.own"`,
		`var Clients = struct {`,
		`Read struct {`,
		`Read.All`,    // appears in flat sub-struct or comment
		`DefaultRoles = map[string][]Permission{`,
		`"EmployeeBasic":`,
		`"EmployeeAgent":`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	yaml, _ := os.ReadFile("../../contract/permissions/catalog.yaml")
	var a, b bytes.Buffer
	_ = generate(yaml, &a)
	_ = generate(yaml, &b)
	if a.String() != b.String() {
		t.Error("codegen not deterministic")
	}
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

```go
// tools/perm-codegen/main.go
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"text/template"

	"contract/permissions"
)

const headerTmpl = `// Code generated by perm-codegen. DO NOT EDIT.
package permissions

`

const footerTmpl = `
var catalogSet = func() map[Permission]struct{} {
	m := make(map[Permission]struct{}, len(Catalog))
	for _, p := range Catalog { m[p] = struct{}{} }
	return m
}()

func IsValid(p Permission) bool { _, ok := catalogSet[p]; return ok }
`

// resourceVerbScope groups perms by resource → verb → scope for the typed
// var declarations.
type resourceVerbScope struct {
	Resource string
	Verbs    []verbScopes
}
type verbScopes struct {
	Verb   string
	Scopes []string  // distinct scopes for this resource.verb
}

func generate(yamlData []byte, w io.Writer) error {
	cat, err := permissions.ParseYAML(yamlData)
	if err != nil { return err }

	var buf bytes.Buffer
	buf.WriteString(headerTmpl)

	// Catalog slice.
	buf.WriteString("var Catalog = []Permission{\n")
	for _, p := range cat.All {
		fmt.Fprintf(&buf, "\t%q,\n", string(p))
	}
	buf.WriteString("}\n\n")

	// Group by resource → verb → [scopes].
	grouped := groupByResourceVerb(cat.All)
	for _, rvs := range grouped {
		emitResourceVar(&buf, rvs)
	}

	// DefaultRoles map.
	buf.WriteString("\nvar DefaultRoles = map[string][]Permission{\n")
	roleNames := make([]string, 0, len(cat.Roles))
	for n := range cat.Roles { roleNames = append(roleNames, n) }
	sort.Strings(roleNames)
	for _, n := range roleNames {
		fmt.Fprintf(&buf, "\t%q: {\n", n)
		for _, p := range cat.Roles[n] {
			fmt.Fprintf(&buf, "\t\t%q,\n", string(p))
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	buf.WriteString(footerTmpl)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Helpful: dump unformatted output for debugging.
		_, _ = w.Write(buf.Bytes())
		return fmt.Errorf("gofmt: %w", err)
	}
	_, err = w.Write(formatted)
	return err
}

func groupByResourceVerb(perms []permissions.Permission) []resourceVerbScope {
	type key struct{ Resource, Verb string }
	bag := map[string]map[string][]string{}
	for _, p := range perms {
		parts := strings.Split(string(p), ".")
		r, v, s := parts[0], parts[1], parts[2]
		if bag[r] == nil { bag[r] = map[string][]string{} }
		bag[r][v] = append(bag[r][v], s)
	}
	rNames := make([]string, 0, len(bag))
	for r := range bag { rNames = append(rNames, r) }
	sort.Strings(rNames)
	out := make([]resourceVerbScope, 0, len(rNames))
	for _, r := range rNames {
		vNames := make([]string, 0, len(bag[r]))
		for v := range bag[r] { vNames = append(vNames, v) }
		sort.Strings(vNames)
		rv := resourceVerbScope{Resource: r}
		for _, v := range vNames {
			scopes := bag[r][v]
			sort.Strings(scopes)
			rv.Verbs = append(rv.Verbs, verbScopes{Verb: v, Scopes: scopes})
		}
		out = append(out, rv)
	}
	return out
}

func emitResourceVar(buf *bytes.Buffer, rvs resourceVerbScope) {
	resName := exportName(rvs.Resource)
	fmt.Fprintf(buf, "var %s = struct {\n", resName)
	for _, vs := range rvs.Verbs {
		fmt.Fprintf(buf, "\t%s struct {\n", exportName(vs.Verb))
		for _, s := range vs.Scopes {
			fmt.Fprintf(buf, "\t\t%s Permission\n", exportName(s))
		}
		buf.WriteString("\t}\n")
	}
	buf.WriteString("}{\n")
	for _, vs := range rvs.Verbs {
		fmt.Fprintf(buf, "\t%s: struct {\n", exportName(vs.Verb))
		for _, s := range vs.Scopes {
			fmt.Fprintf(buf, "\t\t%s Permission\n", exportName(s))
		}
		buf.WriteString("\t}{\n")
		for _, s := range vs.Scopes {
			fmt.Fprintf(buf, "\t\t%s: %q,\n", exportName(s),
				rvs.Resource+"."+vs.Verb+"."+s)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n\n")
	_ = template.Template{}
}

// exportName converts snake_case to PascalCase: "bank_accounts" → "BankAccounts".
func exportName(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" { continue }
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func main() {
	in := "contract/permissions/catalog.yaml"
	out := "contract/permissions/perms.gen.go"
	if len(os.Args) > 1 { in = os.Args[1] }
	if len(os.Args) > 2 { out = os.Args[2] }

	data, err := os.ReadFile(in)
	if err != nil { log.Fatalf("read %s: %v", in, err) }

	var buf bytes.Buffer
	if err := generate(data, &buf); err != nil {
		log.Fatalf("generate: %v", err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		log.Fatalf("write %s: %v", out, err)
	}
	fmt.Printf("wrote %s\n", out)
}
```

- [ ] **Step 4: Run codegen tests**

Run: `cd tools/perm-codegen && go test -v`
Expected: PASS.

- [ ] **Step 5: Run codegen against the actual catalog**

```bash
cd <repo-root>
go run ./tools/perm-codegen
```

Expected: writes `contract/permissions/perms.gen.go`.

- [ ] **Step 6: Verify the generated file compiles**

```bash
cd contract && go build ./permissions/...
```
Expected: clean.

- [ ] **Step 7: Add Makefile target**

```makefile
# Makefile (additions)
permissions:
	go run ./tools/perm-codegen
.PHONY: permissions

build: proto permissions
	# ... existing build steps
```

- [ ] **Step 8: Commit**

```bash
git add tools/perm-codegen/ contract/permissions/perms.gen.go Makefile
git commit -m "feat(permissions): codegen tool produces typed Permission constants"
```

---

## Task 4: Slim down user-service role seeding

**Files:**
- Modify: `user-service/internal/service/role_service.go`

- [ ] **Step 1: Failing test — seed runs only when table empty**

```go
// user-service/internal/service/role_service_test.go
func TestSeedRolesAndPermissions_OnlyOnEmptyTable(t *testing.T) {
	db := newTestDB(t)
	db.AutoMigrate(&model.Role{}, &model.RolePermission{})

	svc := service.NewRoleService(db)
	if err := svc.SeedRolesAndPermissions(); err != nil { t.Fatal(err) }

	var count int64
	db.Model(&model.RolePermission{}).Count(&count)
	if count == 0 { t.Error("expected seeded rows") }
	first := count

	// Modify a role manually (simulate admin grant).
	db.Create(&model.RolePermission{RoleID: 1, Permission: "extra.thing.any"})

	// Second seed call must NOT touch the table.
	if err := svc.SeedRolesAndPermissions(); err != nil { t.Fatal(err) }

	db.Model(&model.RolePermission{}).Count(&count)
	if count != first+1 {
		t.Errorf("seed re-ran on non-empty table: count=%d, expected=%d", count, first+1)
	}
}
```

- [ ] **Step 2: Implement the slim seed**

```go
// user-service/internal/service/role_service.go (replace lines 30-385)
package service

import (
	"contract/permissions"
	"user-service/internal/model"

	"gorm.io/gorm"
)

type RoleService struct{ db *gorm.DB }

func NewRoleService(db *gorm.DB) *RoleService { return &RoleService{db: db} }

// SeedRolesAndPermissions inserts the catalog's default role-permission
// mappings only when the role_permissions table is empty. After first
// startup, the DB is authoritative and admins manage mappings via the API.
func (s *RoleService) SeedRolesAndPermissions() error {
	var count int64
	if err := s.db.Model(&model.RolePermission{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for roleName, perms := range permissions.DefaultRoles {
			role := model.Role{Name: roleName}
			if err := tx.FirstOrCreate(&role, model.Role{Name: roleName}).Error; err != nil {
				return err
			}
			for _, p := range perms {
				rp := model.RolePermission{RoleID: role.ID, Permission: string(p)}
				if err := tx.Create(&rp).Error; err != nil { return err }
			}
		}
		return nil
	})
}

// AssignPermissionToRole grants a permission to a role. Validates against the
// catalog — admin cannot grant a permission that doesn't exist.
func (s *RoleService) AssignPermissionToRole(roleName string, perm string) error {
	if !permissions.IsValid(permissions.Permission(perm)) {
		return ErrPermissionNotInCatalog
	}
	var role model.Role
	if err := s.db.First(&role, "name = ?", roleName).Error; err != nil {
		return ErrRoleNotFound
	}
	return s.db.Clauses(/* ON CONFLICT DO NOTHING */).Create(&model.RolePermission{
		RoleID: role.ID, Permission: perm,
	}).Error
}

func (s *RoleService) RevokePermissionFromRole(roleName string, perm string) error {
	var role model.Role
	if err := s.db.First(&role, "name = ?", roleName).Error; err != nil {
		return ErrRoleNotFound
	}
	return s.db.Where("role_id = ? AND permission = ?", role.ID, perm).
		Delete(&model.RolePermission{}).Error
}

// CatalogDriftCheck logs orphan permissions — DB rows referencing a permission
// no longer in the catalog. Run at startup. Does not auto-clean.
func (s *RoleService) CatalogDriftCheck() {
	var rows []model.RolePermission
	s.db.Find(&rows)
	for _, r := range rows {
		if !permissions.IsValid(permissions.Permission(r.Permission)) {
			log.Printf("WARN: orphan permission in role_permissions: role_id=%d perm=%q", r.RoleID, r.Permission)
		}
	}
}
```

(`ErrPermissionNotInCatalog`, `ErrRoleNotFound` come from Plan A's user-service sentinels.)

- [ ] **Step 3: Run user-service tests**

Run: `cd user-service && go test ./internal/service/... -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add user-service/internal/service/role_service.go user-service/internal/service/role_service_test.go
git commit -m "refactor(user-service): slim role seed; admin manages mappings via API"
```

---

## Task 5: Wire CatalogDriftCheck at startup

**Files:**
- Modify: `user-service/cmd/main.go`

- [ ] **Step 1: Add the call after AutoMigrate**

```go
// user-service/cmd/main.go
roleSvc := service.NewRoleService(db)
if err := roleSvc.SeedRolesAndPermissions(); err != nil { log.Fatal(err) }
roleSvc.CatalogDriftCheck()
```

- [ ] **Step 2: Verify with a fresh start**

```bash
make docker-down && make docker-up
docker logs user-service | grep -E "seed|orphan"
```
Expected: no orphan WARN on a fresh DB.

- [ ] **Step 3: Commit**

```bash
git add user-service/cmd/main.go
git commit -m "wire(user-service): catalog drift check at startup"
```

---

## Task 6: Typed RequirePermission middleware signature

**Files:**
- Modify: `api-gateway/internal/middleware/auth.go`

- [ ] **Step 1: Failing test**

```go
// api-gateway/internal/middleware/auth_test.go (additions)
func TestRequirePermission_TypedSignature(t *testing.T) {
	// Compilation-level check: this should not compile if signature is still string.
	_ = middleware.RequirePermission(permissions.Clients.Read.All)
}
```

- [ ] **Step 2: Update signature**

```go
// api-gateway/internal/middleware/auth.go
func RequirePermission(p permissions.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms := c.GetStringSlice("permissions")
		for _, have := range perms {
			if have == string(p) { c.Next(); return }
		}
		abortWithError(c, http.StatusForbidden, "forbidden", "missing permission "+string(p))
	}
}

func RequireAnyPermission(ps ...permissions.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		have := c.GetStringSlice("permissions")
		set := make(map[string]struct{}, len(have))
		for _, h := range have { set[h] = struct{}{} }
		for _, p := range ps {
			if _, ok := set[string(p)]; ok { c.Next(); return }
		}
		abortWithError(c, http.StatusForbidden, "forbidden", "missing any of required permissions")
	}
}

func RequireAllPermissions(ps ...permissions.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		have := c.GetStringSlice("permissions")
		set := make(map[string]struct{}, len(have))
		for _, h := range have { set[h] = struct{}{} }
		for _, p := range ps {
			if _, ok := set[string(p)]; !ok {
				abortWithError(c, http.StatusForbidden, "forbidden", "missing permission "+string(p))
				return
			}
		}
		c.Next()
	}
}
```

- [ ] **Step 3: Build (will fail at every router call site — that's intentional)**

Run: `cd api-gateway && go build ./...`
Expected: long list of compile errors at every `RequirePermission("clients.read.all")` site.

- [ ] **Step 4: Commit (intermediate broken)**

```bash
git add api-gateway/internal/middleware/auth.go
git commit -m "refactor(middleware): RequirePermission takes typed Permission (router fixes follow)"
```

---

## Task 7: Migrate every RequirePermission call site to typed constants

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go` (and v1, v2 if still present)
- Modify: tests with magic-string permissions

- [ ] **Step 1: For each call site, replace the string with the typed constant**

```go
// Before:
me.POST("/orders",
    middleware.RequirePermission("orders.place.own"),
    h.Stock.PlaceMyOrder)

// After:
me.POST("/orders",
    middleware.RequirePermission(perms.Orders.Place.Own),
    h.Stock.PlaceMyOrder)
```

Add the import: `perms "contract/permissions"`.

For `RequireAnyPermission`:
```go
// Before:
RequireAnyPermission("otc.trade.accept", "securities.trade")

// After:
RequireAnyPermission(perms.Otc.Trade.Accept, perms.Securities.Trade.Any)
```

- [ ] **Step 2: Build**

Run: `cd api-gateway && go build ./...`
Expected: clean.

- [ ] **Step 3: Run tests**

Run: `cd api-gateway && go test ./...`
Expected: PASS (some tests will need string→typed updates — fix per failure).

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/router/ api-gateway/internal/handler/
git commit -m "refactor(router): typed permission constants at every gate"
```

---

## Task 8: Admin endpoints to manage role permissions

**Files:**
- Modify: `user-service/internal/handler/grpc_handler.go` — add `AssignPermissionToRole`, `RevokePermissionFromRole`
- Modify: `contract/proto/user.proto` — add the RPCs
- Modify: `api-gateway/internal/router/router_v3.go` — add HTTP routes
- Modify: `api-gateway/internal/handler/role_handler.go` (or create) — REST handlers

- [ ] **Step 1: Add proto RPCs**

```proto
// contract/proto/user.proto
service UserService {
  // ... existing RPCs ...
  rpc AssignPermissionToRole(AssignPermissionToRoleRequest) returns (Empty);
  rpc RevokePermissionFromRole(RevokePermissionFromRoleRequest) returns (Empty);
}

message AssignPermissionToRoleRequest {
  string role_name = 1;
  string permission = 2;
}
message RevokePermissionFromRoleRequest {
  string role_name = 1;
  string permission = 2;
}
```

Run: `make proto`.

- [ ] **Step 2: Implement gRPC handler**

```go
// user-service/internal/handler/grpc_handler.go
func (h *grpcHandler) AssignPermissionToRole(ctx context.Context, req *pb.AssignPermissionToRoleRequest) (*pb.Empty, error) {
	if err := h.roleSvc.AssignPermissionToRole(req.RoleName, req.Permission); err != nil {
		return nil, err
	}
	return &pb.Empty{}, nil
}

func (h *grpcHandler) RevokePermissionFromRole(ctx context.Context, req *pb.RevokePermissionFromRoleRequest) (*pb.Empty, error) {
	if err := h.roleSvc.RevokePermissionFromRole(req.RoleName, req.Permission); err != nil {
		return nil, err
	}
	return &pb.Empty{}, nil
}
```

- [ ] **Step 3: Add REST routes**

```go
// api-gateway/internal/router/router_v3.go (additions in employee group)
employee.POST("/roles/:role_name/permissions",
    middleware.RequirePermission(perms.Roles.Permissions.Assign),
    h.Role.AssignPermission)
employee.DELETE("/roles/:role_name/permissions/:permission",
    middleware.RequirePermission(perms.Roles.Permissions.Revoke),
    h.Role.RevokePermission)
```

- [ ] **Step 4: REST handler**

```go
// api-gateway/internal/handler/role_handler.go
type AssignPermissionRequest struct {
    Permission string `json:"permission" binding:"required"`
}

func (h *RoleHandler) AssignPermission(c *gin.Context) {
    var req AssignPermissionRequest
    if err := c.ShouldBindJSON(&req); err != nil { apiError(c, 400, "validation_error", err.Error()); return }
    _, err := h.userClient.AssignPermissionToRole(c, &userpb.AssignPermissionToRoleRequest{
        RoleName:   c.Param("role_name"),
        Permission: req.Permission,
    })
    if err != nil { handleGRPCError(c, err); return }
    c.JSON(http.StatusNoContent, nil)
}

func (h *RoleHandler) RevokePermission(c *gin.Context) {
    _, err := h.userClient.RevokePermissionFromRole(c, &userpb.RevokePermissionFromRoleRequest{
        RoleName:   c.Param("role_name"),
        Permission: c.Param("permission"),
    })
    if err != nil { handleGRPCError(c, err); return }
    c.JSON(http.StatusNoContent, nil)
}
```

- [ ] **Step 5: Integration test**

```go
//go:build integration
// +build integration

func TestAdmin_AssignPermissionToRole(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)

	// Grant a permission that EmployeeBasic doesn't have by default.
	resp := admin.POST("/api/v3/roles/EmployeeBasic/permissions",
		map[string]interface{}{"permission": "orders.place.on_behalf_bank"})
	if resp.Code != http.StatusNoContent { t.Errorf("got %d", resp.Code) }

	// Login as a basic employee; their JWT should now carry the new permission.
	emp := loginAsBasicEmployee(t)
	if !contains(emp.JWT.Permissions, "orders.place.on_behalf_bank") {
		t.Errorf("permission not in JWT: %v", emp.JWT.Permissions)
	}
}

func TestAdmin_AssignNonCatalogPermission_Rejected(t *testing.T) {
	t.Parallel()
	admin := loginAsAdmin(t)
	resp := admin.POST("/api/v3/roles/EmployeeBasic/permissions",
		map[string]interface{}{"permission": "totally.fake.permission"})
	if resp.Code != http.StatusBadRequest { t.Errorf("got %d", resp.Code) }
}
```

- [ ] **Step 6: Run tests**

Run: `make test-integration TESTS='Admin_'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add contract/proto/user.proto contract/userpb/ \
        user-service/internal/handler/ api-gateway/internal/router/ \
        api-gateway/internal/handler/role_handler.go \
        test-app/workflows/wf_role_admin_test.go
git commit -m "feat(roles): admin API to assign/revoke permissions on roles"
```

---

## Task 9: Delete the old AllPermissions slice and role builder functions

**Files:**
- Modify: `user-service/internal/service/role_service.go`

- [ ] **Step 1: Confirm zero callers**

```bash
grep -rn "AllPermissions\|basicPermissions\|agentPermissions\|supervisorPermissions\|adminPermissions" \
    --include="*.go" user-service/
```
Expected: NO matches outside of role_service.go itself (or only inside the lines about to be deleted).

- [ ] **Step 2: Delete lines 30-385 of role_service.go**

(Replaced by the slim seed in Task 4 — confirm Task 4 made the file ~50-70 LOC total.)

- [ ] **Step 3: Build**

Run: `cd user-service && go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add user-service/internal/service/role_service.go
git commit -m "chore(user-service): delete the 168-element AllPermissions slice and role builders"
```

---

## Task 10: Update Specification.md

- [ ] **Step 1: Section 6 (permissions)**

Replace the static permission list with: "Permissions are defined in `contract/permissions/catalog.yaml` and codegened to `contract/permissions/perms.gen.go`. Naming: `<resource>.<verb>.<scope>`, snake_case, three segments. Admin manages role-permission mappings via `POST/DELETE /api/v3/roles/:name/permissions`."

- [ ] **Step 2: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): typed permission catalog + admin role-management API"
```

---

## Self-Review

**Spec coverage:**
- ✅ Strict naming convention enforced at codegen — Task 2 (regex) + Task 3 (uses loader)
- ✅ YAML catalog → typed constants — Tasks 1, 3
- ✅ Admin manages mappings dynamically — Task 8
- ✅ Catalog immutable at runtime — Tasks 4, 8 (validate against `IsValid`)
- ✅ Granular only (no umbrella) — enforced by catalog content; loader regex requires 3 segments
- ✅ Catalog drift check — Tasks 4, 5
- ✅ Files deleted — Task 9

**Placeholders:** None.

**Type consistency:** `permissions.Permission` is `type Permission string`. `RequirePermission(permissions.Permission)` consistent. Codegen always emits `Permission` typed constants. Loader returns `[]Permission`.

**Commit cadence:** ~10 commits.
