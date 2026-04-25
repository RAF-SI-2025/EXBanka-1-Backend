# On-behalf trading permission split + session revocation on role-permission change

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `EmployeeAgent`/`EmployeeSupervisor` place on-behalf orders without granting them admin powers, AND force re-auth for affected employees within seconds when a role's permissions change.

**Architecture:**
- New `orders.place-on-behalf` permission, seeded on Agent + Supervisor + Admin. Two route gates flip from `securities.manage` to the new permission.
- Per-user Redis revocation epoch (`user_revoked_at:<id>`). user-service publishes `user.role-permissions-changed` Kafka event when role perms change. auth-service consumer sets the epoch and deletes the affected employees' refresh tokens. `AuthService.ValidateToken` rejects tokens whose `iat < epoch`.

**Tech Stack:** Go workspace (multi-module), GORM (Postgres), segmentio/kafka-go, go-redis/v9, Gin, gRPC.

**Spec:** [docs/superpowers/specs/2026-04-25-onbehalf-perm-and-session-revoke-design.md](../specs/2026-04-25-onbehalf-perm-and-session-revoke-design.md)

---

## Task 1: Add Kafka contract — topic constant + message type

**Files:**
- Modify: `contract/kafka/messages.go` (append new section near other user.* topics)

- [ ] **Step 1: Add the topic constant + message struct**

Open `contract/kafka/messages.go`. After the existing `TopicLimitTemplate*` block (around line 207-209) add a new block:

```go
// Role permissions change event — published by user-service when a role's
// permission set changes. auth-service consumes this to revoke active sessions
// of every employee currently holding the role so they pick up the new perms
// on next request.
const TopicUserRolePermissionsChanged = "user.role-permissions-changed"

// RolePermissionsChangedMessage carries the role identity and the list of
// employees who hold that role at the moment of the change. ChangedAt is unix
// seconds; auth-service uses it as the revocation epoch.
type RolePermissionsChangedMessage struct {
	RoleID              int64   `json:"role_id"`
	RoleName            string  `json:"role_name"`
	AffectedEmployeeIDs []int64 `json:"affected_employee_ids"`
	ChangedAt           int64   `json:"changed_at"`
	Source              string  `json:"source"` // "update_role_permissions" | "create_role"
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd contract && go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat(contract): add user.role-permissions-changed Kafka event"
```

---

## Task 2: RoleRepository.ListEmployeeIDsByRole — failing test

**Files:**
- Test: `user-service/internal/repository/role_repository_test.go` (create if absent)
- Modify: `user-service/internal/repository/role_repository.go`

- [ ] **Step 1: Check if a test file exists; if not create it with this content:**

```go
package repository

import (
	"sort"
	"testing"

	"github.com/exbanka/user-service/internal/model"
)

// TestRoleRepository_ListEmployeeIDsByRole inserts two roles, two employees with
// overlapping role assignments, and asserts the helper returns deduped, sorted IDs.
func TestRoleRepository_ListEmployeeIDsByRole(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Role{}, &model.Permission{}, &model.Employee{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	roleA := model.Role{Name: "RoleA"}
	roleB := model.Role{Name: "RoleB"}
	if err := db.Create(&roleA).Error; err != nil {
		t.Fatalf("create roleA: %v", err)
	}
	if err := db.Create(&roleB).Error; err != nil {
		t.Fatalf("create roleB: %v", err)
	}

	emp1 := model.Employee{Email: "e1@test", FirstName: "E", LastName: "1", JMBG: "1111111111111", Roles: []model.Role{roleA, roleB}}
	emp2 := model.Employee{Email: "e2@test", FirstName: "E", LastName: "2", JMBG: "2222222222222", Roles: []model.Role{roleA}}
	emp3 := model.Employee{Email: "e3@test", FirstName: "E", LastName: "3", JMBG: "3333333333333", Roles: []model.Role{roleB}}
	for _, e := range []*model.Employee{&emp1, &emp2, &emp3} {
		if err := db.Create(e).Error; err != nil {
			t.Fatalf("create employee: %v", err)
		}
	}

	repo := NewRoleRepository(db)

	idsA, err := repo.ListEmployeeIDsByRole(roleA.ID)
	if err != nil {
		t.Fatalf("list roleA: %v", err)
	}
	sort.Slice(idsA, func(i, j int) bool { return idsA[i] < idsA[j] })
	if len(idsA) != 2 || idsA[0] != emp1.ID || idsA[1] != emp2.ID {
		t.Errorf("roleA ids: got %v, want [%d %d]", idsA, emp1.ID, emp2.ID)
	}

	idsB, err := repo.ListEmployeeIDsByRole(roleB.ID)
	if err != nil {
		t.Fatalf("list roleB: %v", err)
	}
	sort.Slice(idsB, func(i, j int) bool { return idsB[i] < idsB[j] })
	if len(idsB) != 2 || idsB[0] != emp1.ID || idsB[1] != emp3.ID {
		t.Errorf("roleB ids: got %v, want [%d %d]", idsB, emp1.ID, emp3.ID)
	}

	emptyIDs, err := repo.ListEmployeeIDsByRole(999999)
	if err != nil {
		t.Fatalf("list missing role: %v", err)
	}
	if emptyIDs == nil || len(emptyIDs) != 0 {
		t.Errorf("missing role: got %v, want non-nil empty slice", emptyIDs)
	}
}
```

If `newTestDB` doesn't exist in this package, look for the SQLite-in-memory helper used by other repo tests in the same dir (e.g. `grep -rn "newTestDB\|sqlite\.Open" user-service/internal/repository/`). If none exists yet, create `user-service/internal/repository/test_helpers_test.go` with:

```go
package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestDB returns an in-memory SQLite gorm.DB suitable for repo unit tests.
// Each call returns a fresh isolated DB.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}
```
And add `github.com/glebarez/sqlite` to user-service `go.mod` if missing (`cd user-service && go get github.com/glebarez/sqlite`).

- [ ] **Step 2: Run it and verify failure**

Run: `cd user-service && go test ./internal/repository/ -run TestRoleRepository_ListEmployeeIDsByRole -v`
Expected: FAIL with `repo.ListEmployeeIDsByRole undefined`.

---

## Task 3: RoleRepository.ListEmployeeIDsByRole — implementation

**Files:**
- Modify: `user-service/internal/repository/role_repository.go`

- [ ] **Step 1: Append the method at the bottom of the file**

```go
// ListEmployeeIDsByRole returns every employee ID that currently holds the
// named role via the employee_roles join table. Returns a non-nil empty slice
// when the role exists but has zero employees (or doesn't exist at all).
// Order is unspecified — callers that need determinism should sort.
func (r *RoleRepository) ListEmployeeIDsByRole(roleID int64) ([]int64, error) {
	var ids []int64
	err := r.db.
		Table("employee_roles").
		Where("role_id = ?", roleID).
		Pluck("employee_id", &ids).Error
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}
```

- [ ] **Step 2: Run the test**

Run: `cd user-service && go test ./internal/repository/ -run TestRoleRepository_ListEmployeeIDsByRole -v`
Expected: PASS.

- [ ] **Step 3: Lint**

Run: `cd user-service && golangci-lint run ./internal/repository/`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add user-service/internal/repository/role_repository.go user-service/internal/repository/role_repository_test.go user-service/internal/repository/test_helpers_test.go user-service/go.mod user-service/go.sum
git commit -m "feat(user-service): RoleRepository.ListEmployeeIDsByRole"
```

(Skip files that don't exist locally — only stage what changed.)

---

## Task 4: user-service Producer.PublishRolePermissionsChanged

**Files:**
- Modify: `user-service/internal/kafka/producer.go`

- [ ] **Step 1: Append the publisher method**

Open `user-service/internal/kafka/producer.go`. After `PublishBlueprint` add:

```go
func (p *Producer) PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error {
	return p.publish(ctx, kafkamsg.TopicUserRolePermissionsChanged, msg)
}
```

- [ ] **Step 2: Verify build**

Run: `cd user-service && go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add user-service/internal/kafka/producer.go
git commit -m "feat(user-service): PublishRolePermissionsChanged"
```

---

## Task 5: Wire RoleService to publish the event — failing test first

**Files:**
- Test: `user-service/internal/service/role_service_test.go`
- Modify: `user-service/internal/service/role_service.go`

- [ ] **Step 1: Inspect existing test file structure**

Run: `grep -n "func Test\|mock.*Repo\|mock.*Producer\|fakeProducer" user-service/internal/service/role_service_test.go | head -20`

The repo this codebase already uses test mocks defined in `interfaces.go` for service tests. Look for `roleSvcDeps` or similar harness.

- [ ] **Step 2: Add an RolePermPublisher interface and inject it**

In `user-service/internal/service/interfaces.go`, add:

```go
// RolePermPublisher is the narrow Kafka surface RoleService needs.
type RolePermPublisher interface {
	PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error
}
```

If `interfaces.go` doesn't already import `context` and `kafkamsg`, add:
```go
import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
)
```

Extend `RoleService` in `user-service/internal/service/role_service.go`:

```go
type RoleService struct {
	roleRepo  RoleRepo
	permRepo  PermissionRepo
	publisher RolePermPublisher // optional — nil-safe via guard in publish helper
}

func NewRoleService(roleRepo RoleRepo, permRepo PermissionRepo) *RoleService {
	return &RoleService{roleRepo: roleRepo, permRepo: permRepo}
}

// WithPublisher wires the Kafka publisher used to emit
// role-permissions-changed events. Returns the same receiver mutated for
// builder-style wiring in cmd/main.go. Pass nil to disable publishing.
func (s *RoleService) WithPublisher(p RolePermPublisher) *RoleService {
	s.publisher = p
	return s
}
```

The existing `RoleRepo` interface needs the new method. Find the interface (likely in `interfaces.go`) and add:

```go
ListEmployeeIDsByRole(roleID int64) ([]int64, error)
```

- [ ] **Step 3: Add a fake publisher used by tests**

In `role_service_test.go` (create if missing), add a recorder type:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/exbanka/user-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type fakeRolePermPublisher struct {
	calls []kafkamsg.RolePermissionsChangedMessage
	err   error
}

func (f *fakeRolePermPublisher) PublishRolePermissionsChanged(_ context.Context, msg kafkamsg.RolePermissionsChangedMessage) error {
	f.calls = append(f.calls, msg)
	return f.err
}
```

- [ ] **Step 4: Add the failing tests**

Append to `role_service_test.go`:

```go
func TestRoleService_UpdateRolePermissions_PublishesEvent(t *testing.T) {
	roleRepo := newFakeRoleRepo() // existing helper in this package
	permRepo := newFakePermRepo()

	role := &model.Role{Name: "EmployeeAgent"}
	roleRepo.add(role)
	permRepo.add(model.Permission{Code: "clients.read"})

	roleRepo.employeesByRole[role.ID] = []int64{42, 43}

	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	before := time.Now().Unix()
	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after := time.Now().Unix()

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(pub.calls))
	}
	got := pub.calls[0]
	if got.RoleID != role.ID || got.RoleName != "EmployeeAgent" {
		t.Errorf("role identity: got %+v", got)
	}
	if got.Source != "update_role_permissions" {
		t.Errorf("source: got %q", got.Source)
	}
	if len(got.AffectedEmployeeIDs) != 2 || got.AffectedEmployeeIDs[0] != 42 || got.AffectedEmployeeIDs[1] != 43 {
		t.Errorf("affected ids: got %v", got.AffectedEmployeeIDs)
	}
	if got.ChangedAt < before || got.ChangedAt > after {
		t.Errorf("ChangedAt %d not within [%d,%d]", got.ChangedAt, before, after)
	}
}

func TestRoleService_UpdateRolePermissions_KafkaFailureDoesNotFailUpdate(t *testing.T) {
	roleRepo := newFakeRoleRepo()
	permRepo := newFakePermRepo()
	role := &model.Role{Name: "EmployeeAgent"}
	roleRepo.add(role)
	permRepo.add(model.Permission{Code: "clients.read"})
	roleRepo.employeesByRole[role.ID] = []int64{1}

	pub := &fakeRolePermPublisher{err: errors.New("kafka down")}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read"}); err != nil {
		t.Fatalf("update should not propagate kafka errors: %v", err)
	}
}

func TestRoleService_UpdateRolePermissions_NoPublisherIsAllowed(t *testing.T) {
	roleRepo := newFakeRoleRepo()
	permRepo := newFakePermRepo()
	role := &model.Role{Name: "EmployeeAgent"}
	roleRepo.add(role)
	permRepo.add(model.Permission{Code: "clients.read"})

	svc := NewRoleService(roleRepo, permRepo) // no publisher
	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read"}); err != nil {
		t.Fatalf("update: %v", err)
	}
}
```

If `newFakeRoleRepo` and `newFakePermRepo` don't already exist with the shape used here (e.g. `add()` helper, `employeesByRole` map, returning the role from `GetByID`), create them in a new file `role_service_fakes_test.go`:

```go
package service

import (
	"errors"

	"github.com/exbanka/user-service/internal/model"
)

type fakeRoleRepo struct {
	roles           map[int64]*model.Role
	nextID          int64
	employeesByRole map[int64][]int64
	setPermsCalls   []struct {
		roleID int64
		perms  []model.Permission
	}
}

func newFakeRoleRepo() *fakeRoleRepo {
	return &fakeRoleRepo{
		roles:           map[int64]*model.Role{},
		employeesByRole: map[int64][]int64{},
	}
}

func (f *fakeRoleRepo) add(r *model.Role) {
	f.nextID++
	r.ID = f.nextID
	f.roles[r.ID] = r
}

func (f *fakeRoleRepo) Create(r *model.Role) error          { f.add(r); return nil }
func (f *fakeRoleRepo) GetByID(id int64) (*model.Role, error) {
	r, ok := f.roles[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}
func (f *fakeRoleRepo) GetByName(name string) (*model.Role, error) {
	for _, r := range f.roles {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeRoleRepo) List() ([]model.Role, error) {
	out := make([]model.Role, 0, len(f.roles))
	for _, r := range f.roles {
		out = append(out, *r)
	}
	return out, nil
}
func (f *fakeRoleRepo) Update(r *model.Role) error { f.roles[r.ID] = r; return nil }
func (f *fakeRoleRepo) SetPermissions(roleID int64, perms []model.Permission) error {
	f.setPermsCalls = append(f.setPermsCalls, struct {
		roleID int64
		perms  []model.Permission
	}{roleID, perms})
	return nil
}
func (f *fakeRoleRepo) Delete(id int64) error                                   { delete(f.roles, id); return nil }
func (f *fakeRoleRepo) GetByNames(names []string) ([]model.Role, error)         { return nil, nil }
func (f *fakeRoleRepo) ListEmployeeIDsByRole(roleID int64) ([]int64, error) {
	ids, ok := f.employeesByRole[roleID]
	if !ok {
		return []int64{}, nil
	}
	return ids, nil
}

type fakePermRepo struct {
	perms map[string]model.Permission
}

func newFakePermRepo() *fakePermRepo { return &fakePermRepo{perms: map[string]model.Permission{}} }
func (f *fakePermRepo) add(p model.Permission)         { f.perms[p.Code] = p }
func (f *fakePermRepo) Create(p *model.Permission) error { f.perms[p.Code] = *p; return nil }
func (f *fakePermRepo) GetByCode(code string) (*model.Permission, error) {
	if p, ok := f.perms[code]; ok {
		return &p, nil
	}
	return nil, errors.New("not found")
}
func (f *fakePermRepo) List() ([]model.Permission, error) {
	out := make([]model.Permission, 0, len(f.perms))
	for _, p := range f.perms {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakePermRepo) ListByCodes(codes []string) ([]model.Permission, error) {
	out := make([]model.Permission, 0, len(codes))
	for _, c := range codes {
		if p, ok := f.perms[c]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}
```

If existing fakes already exist with different shapes, adapt the test to use them rather than introducing duplicates. Re-use is mandatory — never duplicate fakes.

- [ ] **Step 5: Run tests and verify failure**

Run: `cd user-service && go test ./internal/service/ -run "TestRoleService_UpdateRolePermissions" -v`
Expected: FAIL — `WithPublisher` undefined or `len(pub.calls) != 1`.

---

## Task 6: Implement publish in UpdateRolePermissions and CreateRole

**Files:**
- Modify: `user-service/internal/service/role_service.go`

- [ ] **Step 1: Replace the existing `UpdateRolePermissions` and `CreateRole` methods**

```go
// CreateRole creates a new role with the given permission codes. Publishes a
// best-effort role-permissions-changed event so any pre-existing employees
// added to this role mid-flight (rare but possible) get session revocation.
func (s *RoleService) CreateRole(name, description string, permissionCodes []string) (*model.Role, error) {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return nil, err
	}
	role := &model.Role{Name: name, Description: description, Permissions: perms}
	if err := s.roleRepo.Create(role); err != nil {
		return nil, err
	}
	s.publishRolePermissionsChanged(role.ID, name, "create_role")
	return role, nil
}

// UpdateRolePermissions replaces the permissions on a role and publishes a
// best-effort Kafka event so auth-service can revoke active sessions for every
// employee currently holding the role. A Kafka failure is logged but does NOT
// fail the update; the DB write is the source of truth.
func (s *RoleService) UpdateRolePermissions(roleID int64, permissionCodes []string) error {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return err
	}
	if err := s.roleRepo.SetPermissions(roleID, perms); err != nil {
		return err
	}
	role, err := s.roleRepo.GetByID(roleID)
	roleName := ""
	if err == nil && role != nil {
		roleName = role.Name
	}
	s.publishRolePermissionsChanged(roleID, roleName, "update_role_permissions")
	return nil
}

// publishRolePermissionsChanged fans out the event. Nil publisher → no-op.
// Errors are logged at warn but never propagated; the DB state is canonical.
func (s *RoleService) publishRolePermissionsChanged(roleID int64, roleName, source string) {
	if s.publisher == nil {
		return
	}
	ids, err := s.roleRepo.ListEmployeeIDsByRole(roleID)
	if err != nil {
		log.Printf("WARN: role-perm-changed: list employees for role %d: %v", roleID, err)
		return
	}
	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              roleID,
		RoleName:            roleName,
		AffectedEmployeeIDs: ids,
		ChangedAt:           time.Now().Unix(),
		Source:              source,
	}
	if err := s.publisher.PublishRolePermissionsChanged(context.Background(), msg); err != nil {
		log.Printf("WARN: role-perm-changed: publish for role %d: %v", roleID, err)
	}
}
```

Make sure the file imports `context`, `log`, `time`, and `kafkamsg "github.com/exbanka/contract/kafka"`.

- [ ] **Step 2: Run the unit tests**

Run: `cd user-service && go test ./internal/service/ -run "TestRoleService_UpdateRolePermissions" -v`
Expected: PASS for all three tests.

- [ ] **Step 3: Run the entire user-service test suite**

Run: `cd user-service && go test ./... -count=1`
Expected: PASS. If any pre-existing test fails because of the new `RoleRepo.ListEmployeeIDsByRole` interface method, add a stub to the failing fake (returning `[]int64{}, nil`).

- [ ] **Step 4: Lint**

Run: `cd user-service && golangci-lint run ./...`
Expected: no new findings.

- [ ] **Step 5: Commit**

```bash
git add user-service/internal/service/role_service.go user-service/internal/service/role_service_test.go user-service/internal/service/role_service_fakes_test.go user-service/internal/service/interfaces.go
git commit -m "feat(user-service): publish role-permissions-changed on perm updates"
```

---

## Task 7: Pre-create the Kafka topic + wire the publisher

**Files:**
- Modify: `user-service/cmd/main.go`

- [ ] **Step 1: Add the new topic to EnsureTopics and inject the publisher**

In `user-service/cmd/main.go`, locate the `kafkaprod.EnsureTopics(...)` call (around line 59-73) and add `"user.role-permissions-changed"` to the list. Then change the `roleSvc` construction (around line 90):

```go
	roleSvc := service.NewRoleService(roleRepo, permRepo).WithPublisher(producer)
```

- [ ] **Step 2: Build**

Run: `cd user-service && go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add user-service/cmd/main.go
git commit -m "chore(user-service): wire role-perm publisher + EnsureTopics"
```

---

## Task 8: Add `orders.place-on-behalf` permission + flip route gates

**Files:**
- Modify: `user-service/internal/service/role_service.go` (AllPermissions, DefaultRolePermissions)
- Modify: `api-gateway/internal/router/router_v1.go` (lines 587-600 area)
- Modify: `Specification.md` (Section 6 + 17)
- Modify: `docs/api/REST_API_v1.md` (auth requirement on the two routes)

- [ ] **Step 1: Add the permission code**

In `user-service/internal/service/role_service.go`, append to the `AllPermissions` slice (after the existing `securities.manage` entry, around line 57):

```go
	{"orders.place-on-behalf", "Place stock/OTC orders on behalf of a client", "orders"},
```

- [ ] **Step 2: Seed the permission on Agent, Supervisor, and Admin**

In the same file, add `"orders.place-on-behalf"` to the `DefaultRolePermissions["EmployeeAgent"]`, `DefaultRolePermissions["EmployeeSupervisor"]`, and `DefaultRolePermissions["EmployeeAdmin"]` slices. Place it next to the existing `"securities.trade"` entry in each.

- [ ] **Step 3: Flip the route gates**

In `api-gateway/internal/router/router_v1.go`:

- Line 589: change `middleware.RequirePermission("securities.manage")` → `middleware.RequirePermission("orders.place-on-behalf")` for `ordersOnBehalf`.
- Line 599: same change for `otcOnBehalf`.

Update the comments on lines 587 and 594 to reference the new permission name.

- [ ] **Step 4: Update Swagger annotations**

In `api-gateway/internal/handler/stock_order_handler.go::CreateOrderOnBehalf` (around line 260-273) and `api-gateway/internal/handler/portfolio_handler.go::BuyOTCOfferOnBehalf` (around line 198-211), change any swagger annotation that references the old permission to `orders.place-on-behalf`. If no such annotation exists, add `@Description Employee-only. Requires orders.place-on-behalf permission.` to both.

- [ ] **Step 5: Regenerate swagger**

Run: `cd api-gateway && swag init -g cmd/main.go --output docs`
Expected: regenerates `docs/swagger.json`, `docs/swagger.yaml`, `docs/docs.go`.

- [ ] **Step 6: Update REST_API_v1.md**

Locate the section for `POST /api/v1/orders` (the on-behalf one) and `POST /api/v1/otc/admin/offers/{id}/buy`. Change `Auth:` from `JWT + securities.manage` to `JWT + orders.place-on-behalf`.

- [ ] **Step 7: Update Specification.md**

- Section 6 (Roles & Permissions): in the table of permissions add a new row `| orders.place-on-behalf | Place stock/OTC orders on behalf of a client |`. In the role-permission matrix, mark it for EmployeeAgent, EmployeeSupervisor, EmployeeAdmin.
- Section 17 (REST routes): wherever `POST /api/v1/orders` and `POST /api/v1/otc/admin/offers/{id}/buy` appear, change the listed permission from `securities.manage` to `orders.place-on-behalf`.

- [ ] **Step 8: Build everything**

Run: `cd user-service && go build ./... && cd ../api-gateway && go build ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add user-service/internal/service/role_service.go api-gateway/internal/router/router_v1.go api-gateway/internal/handler/stock_order_handler.go api-gateway/internal/handler/portfolio_handler.go api-gateway/docs/ Specification.md docs/api/REST_API_v1.md
git commit -m "feat: orders.place-on-behalf permission for Agent/Supervisor/Admin"
```

---

## Task 9: Add Redis epoch helpers to auth-service cache

**Files:**
- Modify: `auth-service/internal/cache/redis.go`

- [ ] **Step 1: Append SetWithRawString + GetString helpers (or expose direct Redis ops)**

The existing cache wrapper marshals JSON. For the epoch we want a raw `SET key value EX ttl` and a raw `GET`. Append:

```go
// SetUserRevokedAt records the revocation epoch for a user. Tokens whose
// `iat` predates this timestamp must be rejected by ValidateToken.
// ttl is the access-token lifetime — after it expires no surviving token
// could possibly be older than the cutoff anyway, so the key is safe to drop.
func (c *RedisCache) SetUserRevokedAt(ctx context.Context, userID int64, atUnix int64, ttl time.Duration) error {
	key := userRevokedAtKey(userID)
	return c.client.Set(ctx, key, atUnix, ttl).Err()
}

// GetUserRevokedAt returns the revocation epoch in unix seconds, or 0 when
// no revocation key exists. A Redis error is propagated so callers can
// decide on fail-open vs fail-closed.
func (c *RedisCache) GetUserRevokedAt(ctx context.Context, userID int64) (int64, error) {
	key := userRevokedAtKey(userID)
	val, err := c.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return val, nil
}

func userRevokedAtKey(userID int64) string {
	return "user_revoked_at:" + strconv.FormatInt(userID, 10)
}
```

Add `"strconv"` to the imports.

- [ ] **Step 2: Build**

Run: `cd auth-service && go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add auth-service/internal/cache/redis.go
git commit -m "feat(auth-service): cache.SetUserRevokedAt/GetUserRevokedAt"
```

---

## Task 10: Plug the epoch check into ValidateToken — failing test

**Files:**
- Test: `auth-service/internal/service/auth_service_test.go` (extend if exists, create if not)
- Modify: `auth-service/internal/service/auth_service.go`

- [ ] **Step 1: Inspect the existing test scaffolding**

Run: `grep -n "func TestValidateToken\|fakeCache\|mockCache" auth-service/internal/service/auth_service_test.go 2>/dev/null | head -20`

Identify the existing test cache fake and JWT helper. If there isn't a unit test file for ValidateToken, you'll create the harness in this task.

- [ ] **Step 2: Add the failing test**

Add to `auth-service/internal/service/auth_service_test.go` (create the file with package + imports if missing):

```go
package service

import (
	"context"
	"testing"
	"time"
)

// fakeRevokeCache implements just the Set/Get pair the epoch check needs.
// If the existing test harness has a richer fake, extend it instead — do not
// duplicate.
type fakeRevokeCache struct {
	revokedAt map[int64]int64
}

func (f *fakeRevokeCache) GetUserRevokedAt(_ context.Context, userID int64) (int64, error) {
	return f.revokedAt[userID], nil
}

func (f *fakeRevokeCache) SetUserRevokedAt(_ context.Context, userID int64, atUnix int64, _ time.Duration) error {
	if f.revokedAt == nil {
		f.revokedAt = map[int64]int64{}
	}
	f.revokedAt[userID] = atUnix
	return nil
}

// (rest of the AuthCache surface as required by AuthService — re-use the
// existing helper if one already exists in this file. Do not invent a new one
// just for these tests.)

func TestValidateToken_RevokedByEpoch_RejectsToken(t *testing.T) {
	t.Skip("Implemented in Task 11 once AuthService accepts a RevokeCache")
}

func TestValidateToken_RevokedByEpoch_FreshTokenStillValid(t *testing.T) {
	t.Skip("Implemented in Task 11 once AuthService accepts a RevokeCache")
}
```

These intentionally start as `t.Skip`. They become real tests in Step 5. We start with skips so the suite stays green while the production change lands.

- [ ] **Step 3: Define the cache interface AuthService consumes**

In `auth-service/internal/service/auth_service.go`, locate the existing cache field on `AuthService`. It currently holds the concrete `*cache.RedisCache`. Add a small interface near the existing type declaration:

```go
// RevokeCache is the narrow surface AuthService needs for the per-user
// revocation epoch. Implemented by *cache.RedisCache.
type RevokeCache interface {
	GetUserRevokedAt(ctx context.Context, userID int64) (int64, error)
	SetUserRevokedAt(ctx context.Context, userID int64, atUnix int64, ttl time.Duration) error
}
```

If the AuthService already accepts the cache via interface, just extend that interface with the two methods above. **Do not add a second cache field** — the existing one already implements both methods after Task 9.

- [ ] **Step 4: Add the epoch gate inside ValidateToken**

Modify `auth-service/internal/service/auth_service.go` `ValidateToken` (currently at lines 272-311). Insert the epoch check after each path that produces `claims`, BEFORE returning them. Concretely:

After the cache-hit `return &cached, nil` at line ~286, change the block to:

```go
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			if cached.ID != "" {
				blacklisted, _ := s.cache.Exists(context.Background(), "blacklist:"+cached.ID)
				if blacklisted {
					return nil, fmt.Errorf("access token has been revoked; please log in again")
				}
			}
			if revoked, _ := s.checkRevokedByEpoch(&cached); revoked {
				return nil, fmt.Errorf("access token has been revoked; please log in again")
			}
			return &cached, nil
		}
```

After the JWT-parse path returns claims (around line 301-310), insert the same check before the cache-set + return:

```go
	if revoked, _ := s.checkRevokedByEpoch(claims); revoked {
		return nil, fmt.Errorf("access token has been revoked; please log in again")
	}

	if s.cache != nil && claims.ExpiresAt != nil {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			_ = s.cache.Set(context.Background(), cacheKey, claims, ttl)
		}
	}

	return claims, nil
```

Add the helper at the bottom of the file:

```go
// checkRevokedByEpoch returns true when the given claims' IssuedAt is older
// than the per-user revocation epoch in Redis. Redis errors are swallowed
// (fail-open), matching the existing posture of the JTI blacklist lookup.
// Returns (false, nil) when no epoch is set or the claim has no IssuedAt.
func (s *AuthService) checkRevokedByEpoch(claims *Claims) (bool, error) {
	if claims == nil || claims.IssuedAt == nil || s.cache == nil {
		return false, nil
	}
	revokedAt, err := s.cache.GetUserRevokedAt(context.Background(), claims.UserID)
	if err != nil || revokedAt == 0 {
		return false, err
	}
	return claims.IssuedAt.Unix() < revokedAt, nil
}
```

If `claims.UserID` doesn't exist on `Claims`, use the existing field name (likely `UserID`, `Sub`, or `Subject` — check `auth-service/internal/service/jwt_service.go` `Claims` struct).

- [ ] **Step 5: Replace the t.Skip in the tests with real bodies**

```go
func TestValidateToken_RevokedByEpoch_RejectsToken(t *testing.T) {
	jwtSvc := newTestJWTService(t)             // existing test helper; re-use
	cache := &fakeRevokeCache{}                // satisfies RevokeCache; full AuthCache
	svc := newTestAuthServiceWith(t, jwtSvc, cache) // existing test ctor

	tok, _ := jwtSvc.GenerateAccessToken(42, "x@y", []string{"EmployeeAgent"}, []string{"clients.read"}, "employee", TokenProfile{})

	// Revoke epoch in the future relative to the token's iat.
	cache.SetUserRevokedAt(context.Background(), 42, time.Now().Add(1*time.Hour).Unix(), 15*time.Minute)

	if _, err := svc.ValidateToken(tok); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revoked error, got %v", err)
	}
}

func TestValidateToken_RevokedByEpoch_FreshTokenStillValid(t *testing.T) {
	jwtSvc := newTestJWTService(t)
	cache := &fakeRevokeCache{}
	svc := newTestAuthServiceWith(t, jwtSvc, cache)

	cache.SetUserRevokedAt(context.Background(), 42, time.Now().Add(-1*time.Hour).Unix(), 15*time.Minute)
	tok, _ := jwtSvc.GenerateAccessToken(42, "x@y", []string{"EmployeeAgent"}, []string{"clients.read"}, "employee", TokenProfile{})

	if _, err := svc.ValidateToken(tok); err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
}
```

If `newTestAuthServiceWith` and `newTestJWTService` don't exist with that signature, look for the closest existing constructor and adapt. Real names will exist — read the file before guessing.

- [ ] **Step 6: Run tests**

Run: `cd auth-service && go test ./internal/service/ -run TestValidateToken_Revoked -v`
Expected: PASS for both.

- [ ] **Step 7: Run the whole auth-service suite**

Run: `cd auth-service && go test ./... -count=1`
Expected: PASS. If a previously-green test now needs the new cache interface satisfied, extend the test fake.

- [ ] **Step 8: Lint**

Run: `cd auth-service && golangci-lint run ./...`
Expected: no new findings.

- [ ] **Step 9: Commit**

```bash
git add auth-service/internal/service/auth_service.go auth-service/internal/service/auth_service_test.go
git commit -m "feat(auth-service): per-user revocation epoch in ValidateToken"
```

---

## Task 11: Role-perm-change Kafka consumer in auth-service

**Files:**
- Create: `auth-service/internal/consumer/role_perm_change_consumer.go`
- Test: `auth-service/internal/consumer/role_perm_change_consumer_test.go`

- [ ] **Step 1: Write the failing consumer test**

```go
package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
)

type spyRevokeCache struct {
	calls []struct {
		userID int64
		atUnix int64
		ttl    time.Duration
	}
}

func (s *spyRevokeCache) SetUserRevokedAt(_ context.Context, userID int64, atUnix int64, ttl time.Duration) error {
	s.calls = append(s.calls, struct {
		userID int64
		atUnix int64
		ttl    time.Duration
	}{userID, atUnix, ttl})
	return nil
}

type spyTokenRepo struct {
	revoked []int64
}

func (s *spyTokenRepo) RevokeAllForPrincipal(_ string, principalID int64) error {
	s.revoked = append(s.revoked, principalID)
	return nil
}

func TestRolePermChangeHandler_SetsEpochAndRevokesRefresh(t *testing.T) {
	cache := &spyRevokeCache{}
	tokens := &spyTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, 15*time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              7,
		RoleName:            "EmployeeAgent",
		AffectedEmployeeIDs: []int64{10, 11, 12},
		ChangedAt:           1700000000,
		Source:              "update_role_permissions",
	}
	raw, _ := json.Marshal(msg)

	if err := h.Handle(context.Background(), raw); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(cache.calls) != 3 {
		t.Fatalf("expected 3 cache calls, got %d", len(cache.calls))
	}
	for i, want := range []int64{10, 11, 12} {
		if cache.calls[i].userID != want {
			t.Errorf("call[%d].userID = %d, want %d", i, cache.calls[i].userID, want)
		}
		if cache.calls[i].atUnix != 1700000000 {
			t.Errorf("call[%d].atUnix = %d, want 1700000000", i, cache.calls[i].atUnix)
		}
		if cache.calls[i].ttl != 15*time.Minute {
			t.Errorf("call[%d].ttl = %v, want 15m", i, cache.calls[i].ttl)
		}
	}
	if len(tokens.revoked) != 3 || tokens.revoked[0] != 10 || tokens.revoked[1] != 11 || tokens.revoked[2] != 12 {
		t.Errorf("revoked principals: %v", tokens.revoked)
	}
}
```

- [ ] **Step 2: Create the consumer skeleton**

Create `auth-service/internal/consumer/role_perm_change_consumer.go`:

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
)

// RevokeCache is the narrow Redis surface the handler needs.
type RevokeCache interface {
	SetUserRevokedAt(ctx context.Context, userID int64, atUnix int64, ttl time.Duration) error
}

// RefreshTokenRevoker revokes all refresh tokens for a given principal so the
// affected employee MUST go through full re-login (not just refresh) on next
// API call.
type RefreshTokenRevoker interface {
	RevokeAllForPrincipal(principalType string, principalID int64) error
}

// RolePermChangeHandler is split from the Kafka reader for direct unit testing.
type RolePermChangeHandler struct {
	cache   RevokeCache
	tokens  RefreshTokenRevoker
	epochTTL time.Duration
}

func NewRolePermChangeHandler(cache RevokeCache, tokens RefreshTokenRevoker, epochTTL time.Duration) *RolePermChangeHandler {
	return &RolePermChangeHandler{cache: cache, tokens: tokens, epochTTL: epochTTL}
}

func (h *RolePermChangeHandler) Handle(ctx context.Context, raw []byte) error {
	var msg kafkamsg.RolePermissionsChangedMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	for _, empID := range msg.AffectedEmployeeIDs {
		if err := h.cache.SetUserRevokedAt(ctx, empID, msg.ChangedAt, h.epochTTL); err != nil {
			log.Printf("WARN: role-perm-change: set epoch for employee %d: %v", empID, err)
		}
		if err := h.tokens.RevokeAllForPrincipal("employee", empID); err != nil {
			log.Printf("WARN: role-perm-change: revoke refresh tokens for employee %d: %v", empID, err)
		}
	}
	return nil
}

// RolePermChangeConsumer is the long-running Kafka consumer wrapper.
type RolePermChangeConsumer struct {
	reader  *kafka.Reader
	handler *RolePermChangeHandler
}

func NewRolePermChangeConsumer(brokers string, h *RolePermChangeHandler) *RolePermChangeConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokers},
		Topic:       kafkamsg.TopicUserRolePermissionsChanged,
		GroupID:     "auth-service-role-perm-change",
		StartOffset: kafka.LastOffset,
	})
	return &RolePermChangeConsumer{reader: r, handler: h}
}

func (c *RolePermChangeConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("role-perm-change consumer read error: %v", err)
				continue
			}
			if err := c.handler.Handle(ctx, msg.Value); err != nil {
				log.Printf("role-perm-change consumer handle error: %v (payload: %s)", err, string(msg.Value))
			}
		}
	}()
}

func (c *RolePermChangeConsumer) Close() error {
	return c.reader.Close()
}
```

- [ ] **Step 3: Run the test**

Run: `cd auth-service && go test ./internal/consumer/ -run TestRolePermChangeHandler -v`
Expected: PASS.

- [ ] **Step 4: Lint**

Run: `cd auth-service && golangci-lint run ./internal/consumer/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add auth-service/internal/consumer/role_perm_change_consumer.go auth-service/internal/consumer/role_perm_change_consumer_test.go
git commit -m "feat(auth-service): role-perm-change Kafka consumer + handler"
```

---

## Task 12: Add `RevokeAllForPrincipal` on TokenRepository, wire consumer in main

**Files:**
- Modify: `auth-service/internal/repository/token_repository.go`
- Modify: `auth-service/internal/repository/account_repository.go` (only if a join helper is needed)
- Modify: `auth-service/cmd/main.go`

- [ ] **Step 1: Add the principal-scoped revoke helper**

In `auth-service/internal/repository/token_repository.go`, append:

```go
// RevokeAllForPrincipal revokes every refresh token belonging to the named
// principal (employee or client) by joining through the accounts table.
// Used by the role-perm-change consumer to force re-login.
func (r *TokenRepository) RevokeAllForPrincipal(principalType string, principalID int64) error {
	return r.db.Exec(`
		UPDATE refresh_tokens
		SET revoked = true
		WHERE account_id IN (
			SELECT id FROM accounts WHERE principal_type = ? AND principal_id = ?
		)
	`, principalType, principalID).Error
}
```

- [ ] **Step 2: Wire the consumer + topic in main**

In `auth-service/cmd/main.go`:
1. Add `kafkamsg.TopicUserRolePermissionsChanged` to the `EnsureTopics(...)` arg list (around lines 116-126).
2. After the existing `clientConsumer` is started, wire the new one:

```go
	rolePermHandler := consumer.NewRolePermChangeHandler(redisCache, tokenRepo, cfg.JWTAccessExpiry)
	rolePermConsumer := consumer.NewRolePermChangeConsumer(cfg.KafkaBrokers, rolePermHandler)
	rolePermConsumer.Start(ctx)
	defer rolePermConsumer.Close()
```

If `redisCache` doesn't satisfy `consumer.RevokeCache` because the existing var is named differently, use the actual name. If `cfg.JWTAccessExpiry` isn't a `time.Duration` directly, convert it with `cfg.JWTAccessExpiry` or the corresponding parsed duration variable.

Also confirm `tokenRepo` is in scope at that point; if it's named differently, use the actual local var.

- [ ] **Step 3: Build**

Run: `cd auth-service && go build ./...`
Expected: no output.

- [ ] **Step 4: Tests**

Run: `cd auth-service && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Lint**

Run: `cd auth-service && golangci-lint run ./...`
Expected: no new findings.

- [ ] **Step 6: Commit**

```bash
git add auth-service/internal/repository/token_repository.go auth-service/cmd/main.go
git commit -m "feat(auth-service): wire role-perm-change consumer + per-principal revoke"
```

---

## Task 13: Update Specification.md for the Kafka topic + business rule

**Files:**
- Modify: `Specification.md`

- [ ] **Step 1: Add the topic + rule**

- Section 19 (Kafka topics): add `user.role-permissions-changed` with payload schema (RoleID, RoleName, AffectedEmployeeIDs, ChangedAt, Source).
- Section 21 (Business rules): add "Role permission updates revoke active sessions for affected employees within seconds via the user.role-permissions-changed Kafka event; auth-service rejects access tokens whose iat predates the per-user revocation epoch."

- [ ] **Step 2: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): role-permissions-changed Kafka topic + revocation rule"
```

---

## Task 14: Integration test — agent on-behalf order lands in client portfolio

**Files:**
- Modify: `test-app/workflows/employee_onbehalf_test.go`

- [ ] **Step 1: Extend the existing happy-path test**

Open `test-app/workflows/employee_onbehalf_test.go`. Find `TestEmployeeOnBehalf_CreateOrder` (line 14). After the assertion that `acting_employee_id != 0`, add:

```go
	orderID := int(helpers.GetNumberField(t, resp, "id"))

	// Wait for the order to fill (best-effort — same pattern as wf_full_day).
	tryWaitForOrderFill(t, adminC, orderID, 15*time.Second)

	// Log in as the client and confirm the holding shows up in their portfolio.
	clientC := loginAsClient(t, clientID)
	portResp, err := clientC.GET("/api/v1/me/portfolio")
	if err != nil {
		t.Fatalf("client portfolio: %v", err)
	}
	helpers.RequireStatus(t, portResp, 200)
	holdings, _ := portResp.Body["holdings"].([]interface{})
	if len(holdings) == 0 {
		t.Errorf("client %d portfolio is empty after on-behalf buy", clientID)
	}
```

If `loginAsClient` doesn't exist with that signature, find the equivalent helper in `test-app/workflows/helpers_test.go` (look for `loginAsClient`, `setupClientSession`, etc.) and use it.

If `tryWaitForOrderFill` is in another test file in the same package, no import needed; otherwise look for the same helper used in `wf_full_day_test.go:136`.

Add `time` to imports if not present.

- [ ] **Step 2: Add a Forbidden test for EmployeeBasic**

Append to the same file:

```go
// TestEmployeeOnBehalf_AsBasic_Forbidden asserts that an EmployeeBasic role
// (no orders.place-on-behalf permission) cannot place an on-behalf order.
func TestEmployeeOnBehalf_AsBasic_Forbidden(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	basicC := setupEmployeeWithRole(t, adminC, "EmployeeBasic")
	clientID, _, _, _ := setupActivatedClient(t, adminC)

	acctsResp, _ := adminC.GET("/api/v1/accounts?client_id=" + strconv.Itoa(clientID))
	accts, _ := acctsResp.Body["accounts"].([]interface{})
	if len(accts) == 0 {
		t.Skip("no accounts for client")
	}
	first, _ := accts[0].(map[string]interface{})
	accountID := int(first["id"].(float64))

	listResp, _ := adminC.GET("/api/v1/securities/stocks?page=1&page_size=1")
	stocks, _ := listResp.Body["stocks"].([]interface{})
	if len(stocks) == 0 {
		t.Skip("no listings")
	}
	listing, _ := stocks[0].(map[string]interface{})["listing"].(map[string]interface{})
	if listing == nil {
		t.Skip("no listing")
	}
	listingID := int(listing["id"].(float64))

	resp, err := basicC.POST("/api/v1/orders", map[string]interface{}{
		"client_id":  clientID,
		"account_id": accountID,
		"listing_id": listingID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 (basic lacks orders.place-on-behalf), got %d body=%v", resp.StatusCode, resp.Body)
	}
}
```

If `setupEmployeeWithRole` doesn't exist, look in `helpers_test.go` for an equivalent (e.g., `createEmployee` + `assignRole` two-step). Use the existing pattern; do not invent new helpers.

- [ ] **Step 3: Run the new tests**

Bring services up first if not already running: `make docker-up` (or whatever is canonical for this repo's integration suite). Then:

Run: `cd test-app && go test -tags integration ./workflows/ -run "TestEmployeeOnBehalf" -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/employee_onbehalf_test.go
git commit -m "test(integration): on-behalf order lands in client portfolio + basic-forbidden"
```

---

## Task 15: Integration test — role permission update revokes active sessions

**Files:**
- Create: `test-app/workflows/role_permission_revocation_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package workflows

import (
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth asserts that
// changing a role's permission set forces every employee currently holding the
// role to re-authenticate. We pick a permission gate the agent currently holds
// (clients.read), strip it from the role via PUT /api/v1/roles/<id>/permissions,
// then assert the agent's next call to a clients.read endpoint returns 401.
func TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth(t *testing.T) {
	t.Parallel()

	adminC := loginAsAdmin(t)
	agentC := setupEmployeeWithRole(t, adminC, "EmployeeAgent")

	// Sanity: agent can list clients (clients.read is in EmployeeAgent default).
	preResp, err := agentC.GET("/api/v1/clients?page=1&page_size=1")
	if err != nil {
		t.Fatalf("pre-revoke list: %v", err)
	}
	helpers.RequireStatus(t, preResp, 200)

	// Find the EmployeeAgent role ID.
	rolesResp, err := adminC.GET("/api/v1/roles")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	roles, _ := rolesResp.Body["roles"].([]interface{})
	var agentRoleID int
	for _, r := range roles {
		m, _ := r.(map[string]interface{})
		if m["name"] == "EmployeeAgent" {
			agentRoleID = int(m["id"].(float64))
			break
		}
	}
	if agentRoleID == 0 {
		t.Fatal("EmployeeAgent role not found")
	}

	// Strip clients.read from EmployeeAgent (keep one harmless permission so
	// the role isn't empty — that path may behave differently).
	updResp, err := adminC.PUT("/api/v1/roles/"+strconv.Itoa(agentRoleID)+"/permissions", map[string]interface{}{
		"permission_codes": []string{"securities.read"},
	})
	if err != nil {
		t.Fatalf("update perms: %v", err)
	}
	helpers.RequireStatus(t, updResp, 200)

	// Give Kafka + consumer a moment.
	time.Sleep(2 * time.Second)

	// Now the agent's existing access token is older than the revocation epoch.
	postResp, err := agentC.GET("/api/v1/clients?page=1&page_size=1")
	if err != nil {
		t.Fatalf("post-revoke list: %v", err)
	}
	if postResp.StatusCode != 401 {
		t.Errorf("expected 401 after role-perm change, got %d body=%v", postResp.StatusCode, postResp.Body)
	}

	// Restore the original permission set so other tests don't break.
	restoreResp, _ := adminC.PUT("/api/v1/roles/"+strconv.Itoa(agentRoleID)+"/permissions", map[string]interface{}{
		"permission_codes": []string{
			"clients.create", "clients.read", "clients.update",
			"accounts.create", "accounts.read", "accounts.update",
			"cards.create", "cards.read", "cards.update", "cards.approve",
			"payments.read",
			"credits.read", "credits.approve",
			"securities.trade", "securities.read",
			"orders.place-on-behalf",
		},
	})
	helpers.RequireStatus(t, restoreResp, 200)
}
```

If the body shape for `PUT /api/v1/roles/:id/permissions` is `permission_ids` rather than `permission_codes`, adapt — read the gateway handler first to confirm.

If `setupEmployeeWithRole` doesn't exist, build the agent the same way other integration tests do (see e.g. `wf_full_day_test.go` which logs in `agentC`).

- [ ] **Step 2: Run it**

`make docker-up` first if not running. Then:

Run: `cd test-app && go test -tags integration ./workflows/ -run TestRoleRevocation -v`
Expected: PASS. The 2-second sleep is the Kafka propagation budget; if it's flaky bump to 4s once and do not chase further (longer than that signals a real perf regression).

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/role_permission_revocation_test.go
git commit -m "test(integration): role-perm change revokes active agent session"
```

---

## Task 16: Final verification sweep

- [ ] **Step 1: Run the full unit test matrix**

Run: `make test`
Expected: PASS.

- [ ] **Step 2: Lint everything**

Run: `make lint`
Expected: no new findings.

- [ ] **Step 3: Build all services**

Run: `make build`
Expected: every binary produced, no errors.

- [ ] **Step 4: docker-compose sanity (optional but recommended)**

Run: `make docker-up && sleep 10 && make docker-logs | tail -50`
Expected: user-service logs `Pre-creating Kafka topics ... user.role-permissions-changed`; auth-service logs include `auth-service-role-perm-change` consumer start. Then `make docker-down`.

- [ ] **Step 5: Smoke-test the integration suite end-to-end**

Run: `cd test-app && go test -tags integration ./workflows/ -count=1`
Expected: PASS.

- [ ] **Step 6: Final commit (no-op safety net)**

If any docs drifted or `make swagger` produced uncommitted regen artifacts, stage and commit:

```bash
git status
# stage anything left over and commit "chore: post-implementation cleanup"
```

---

## Self-review against the spec

- ✅ New `orders.place-on-behalf` permission added (Task 8) — covers spec Part A.
- ✅ Route gates flipped (Task 8) — covers spec Part A.
- ✅ Specification.md + REST_API_v1.md + swagger updated (Tasks 8 + 13).
- ✅ Kafka contract added (Task 1) — covers spec Part B "data" + topic.
- ✅ user-service producer + service publish path (Tasks 4 + 6) — covers spec Part B "Producer".
- ✅ Topic pre-creation in user-service main (Task 7).
- ✅ auth-service consumer + handler (Tasks 11 + 12) — covers spec Part B "Consumer".
- ✅ ValidateToken epoch check on both cache + fresh paths (Task 10) — covers spec Part B "Validation gate".
- ✅ Refresh tokens revoked for affected principals (Task 12) — covers spec Part B requirement.
- ✅ Unit tests for repo, service, consumer, and ValidateToken (Tasks 2-3, 5-6, 10-11).
- ✅ Integration tests for both halves (Tasks 14 + 15).
- ✅ Failure-mode handling (Redis fail-open, Kafka best-effort) baked into Tasks 6 + 10.

No spec gaps identified.
