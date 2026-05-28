# Admin Cron Viewer (Part C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admin-only endpoints to view, trigger, pause, and resume every cron job across all services.

**Architecture:**
- New shared package `contract/cronreg`: in-memory `Registry` with `Entry` per cron, gRPC server `AdminCron` (List/Get/Trigger/Pause/Resume), `cron_pause_state` DB table model.
- Each service that runs crons: imports `cronreg`, registers each cron, calls `entry.BeginRun()/EndRun(err)` around its work, gates ticks on `entry.IsPaused()`. Adds AutoMigrate for `cron_pause_state`. Adds AdminCron gRPC server to its existing listener.
- API gateway: per-service gRPC client, six routes (`GET /admin/crons`, `GET /admin/crons/:service/:name`, `POST .../trigger`, `POST .../pause`, `POST .../resume`), errgroup-based fan-out for the list endpoint, three new permissions (`admin.crons.view`, `admin.crons.trigger`, `admin.crons.manage`) seeded only on `EmployeeAdmin`.
- Kafka `admin.cron-action` topic emitted on every state-changing call; notification-service consumes and writes audit log row.

**Tech Stack:** Go workspace, gRPC + protobuf, GORM/Postgres for pause state, Kafka for audit, Gin, errgroup.

**Spec:** `docs/superpowers/specs/2026-05-28-final-audit-fixes-and-portfolio-cron-design.md` Part C.

---

## File Structure

**Shared (new):**
- Create: `contract/cronreg/registry.go` — Registry, Entry, control methods
- Create: `contract/cronreg/registry_test.go`
- Create: `contract/cronreg/model.go` — CronPauseState GORM model + repo
- Create: `contract/cronreg/grpc_server.go` — AdminCron gRPC handler backed by Registry
- Create: `contract/proto/admin/admin_cron.proto` — service + messages
- Modify: `contract/kafka/messages.go` — add CronActionMessage + topic constant

**Per-service changes (10 services):**
- Modify each service's `cmd/main.go`: construct Registry, AutoMigrate model, register AdminCron gRPC, EnsureTopics includes `admin.cron-action`
- Modify each cron file to register with Registry and gate on `IsPaused()`

**Gateway:**
- Create: `api-gateway/internal/grpc/admin_cron_clients.go` — per-service gRPC client pool
- Create: `api-gateway/internal/handler/admin_cron_handler.go`
- Modify: `api-gateway/internal/router/router_v3.go` — six routes under `RequirePermission`

**Notifications/Audit:**
- Create: `notification-service/internal/consumer/admin_audit_consumer.go`
- Create: `notification-service/internal/model/admin_audit_log.go`

**Tests:**
- Create: `contract/cronreg/registry_test.go`
- Create: `test-app/workflows/admin_cron_test.go`

---

## Task C1: Define Entry + Registry with tests

**Files:**
- Create: `contract/cronreg/registry.go`
- Create: `contract/cronreg/registry_test.go`

- [ ] **Step 1: Write failing tests**

```go
// contract/cronreg/registry_test.go
package cronreg

import (
	"errors"
	"testing"
	"time"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry("test-service", nil) // nil pauseStore = in-memory
	e := r.Register("test-cron", "A test cron", 30*time.Second)
	if e == nil { t.Fatal("Register returned nil") }
	infos := r.List()
	if len(infos) != 1 || infos[0].Name != "test-cron" {
		t.Fatalf("unexpected list: %+v", infos)
	}
}

func TestRegistry_BeginRun_EndRun_TracksTimes(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	if !e.BeginRun() { t.Fatal("BeginRun should return true when not paused") }
	e.EndRun(nil)
	info, _ := r.Get("c")
	if info.LastStartedAt == nil || info.LastFinishedAt == nil {
		t.Fatal("expected timestamps set")
	}
	if info.RunCount != 1 { t.Errorf("RunCount=%d", info.RunCount) }
	if info.ErrorCount != 0 { t.Errorf("ErrorCount=%d", info.ErrorCount) }
}

func TestRegistry_PauseSkipsRun(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	if err := r.Pause("c", 7); err != nil { t.Fatal(err) }
	if e.BeginRun() { t.Fatal("BeginRun should return false when paused") }
	info, _ := r.Get("c")
	if !info.IsPaused { t.Fatal("info should report paused") }
	if info.PausedByEmployee != 7 { t.Errorf("PausedByEmployee=%d", info.PausedByEmployee) }
}

func TestRegistry_ResumeAllowsRun(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	_ = r.Pause("c", 7)
	if err := r.Resume("c"); err != nil { t.Fatal(err) }
	if !e.BeginRun() { t.Fatal("BeginRun should be allowed after resume") }
}

func TestRegistry_TriggerEnqueuesOneShot(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Hour)
	if err := r.Trigger("c", false, 1); err != nil { t.Fatal(err) }
	select {
	case <-e.TriggerChan():
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected trigger channel to fire")
	}
}

func TestRegistry_TriggerOnPausedRequiresForce(t *testing.T) {
	r := NewRegistry("test", nil)
	_ = r.Register("c", "", time.Hour)
	_ = r.Pause("c", 7)
	err := r.Trigger("c", false, 1)
	if !errors.Is(err, ErrCronPaused) {
		t.Errorf("expected ErrCronPaused, got %v", err)
	}
	if err := r.Trigger("c", true, 1); err != nil {
		t.Errorf("force trigger failed: %v", err)
	}
}

func TestRegistry_EndRun_WithError_IncrementsErrorCount(t *testing.T) {
	r := NewRegistry("test", nil)
	e := r.Register("c", "", time.Minute)
	_ = e.BeginRun()
	e.EndRun(errors.New("boom"))
	info, _ := r.Get("c")
	if info.ErrorCount != 1 || info.LastError != "boom" {
		t.Errorf("got %+v", info)
	}
}
```

- [ ] **Step 2: Verify failing**

```bash
cd contract && go test ./cronreg/... -v
```

Expected: FAIL — `undefined: NewRegistry`

- [ ] **Step 3: Implement**

```go
// contract/cronreg/registry.go
package cronreg

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCronNotFound = errors.New("cron not found")
	ErrCronPaused   = errors.New("cron is paused; pass force=true to override")
)

// CronInfo is the read-only view of an entry.
type CronInfo struct {
	Name             string
	Service          string
	Description      string
	Interval         time.Duration
	CronExpression   string
	LastStartedAt    *time.Time
	LastFinishedAt   *time.Time
	LastError        string
	NextScheduledAt  time.Time
	IsPaused         bool
	PausedByEmployee int64
	PausedAt         *time.Time
	RunCount         int64
	ErrorCount       int64
}

// Entry is the live, control surface for one cron job. Returned by Registry.Register.
type Entry struct {
	mu               sync.RWMutex
	name             string
	service          string
	description      string
	interval         time.Duration
	cronExpression   string
	lastStartedAt    *time.Time
	lastFinishedAt   *time.Time
	lastError        string
	nextScheduledAt  time.Time
	isPaused         atomic.Bool
	pausedByEmployee atomic.Int64
	pausedAt         atomic.Pointer[time.Time]
	runCount         atomic.Int64
	errorCount       atomic.Int64
	triggerCh        chan struct{}
}

func (e *Entry) IsPaused() bool { return e.isPaused.Load() }

// BeginRun returns false if the cron is paused. Cron loops should:
//   if !entry.BeginRun() { continue }
//   defer entry.EndRun(err)
func (e *Entry) BeginRun() bool {
	if e.isPaused.Load() { return false }
	now := time.Now().UTC()
	e.mu.Lock()
	e.lastStartedAt = &now
	e.mu.Unlock()
	return true
}

func (e *Entry) EndRun(err error) {
	now := time.Now().UTC()
	e.mu.Lock()
	e.lastFinishedAt = &now
	if err != nil {
		e.lastError = err.Error()
		e.errorCount.Add(1)
	} else {
		e.lastError = ""
	}
	e.runCount.Add(1)
	if e.interval > 0 {
		e.nextScheduledAt = now.Add(e.interval)
	}
	e.mu.Unlock()
}

// TriggerChan returns a channel that fires when an admin manually triggers
// this cron. The cron loop should select on it alongside its ticker.
func (e *Entry) TriggerChan() <-chan struct{} { return e.triggerCh }

// PauseStore persists pause state across restarts. nil means in-memory only.
type PauseStore interface {
	Load(cronName string) (paused bool, byEmployee int64, at time.Time, err error)
	Save(cronName string, paused bool, byEmployee int64, at time.Time) error
}

type Registry struct {
	mu       sync.RWMutex
	service  string
	entries  map[string]*Entry
	store    PauseStore
}

func NewRegistry(service string, store PauseStore) *Registry {
	return &Registry{service: service, entries: map[string]*Entry{}, store: store}
}

// Register declares a new cron. Idempotent: calling twice with the same name
// returns the existing entry.
func (r *Registry) Register(name, description string, interval time.Duration) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[name]; ok { return existing }
	e := &Entry{
		name: name, service: r.service, description: description, interval: interval,
		nextScheduledAt: time.Now().UTC().Add(interval),
		triggerCh: make(chan struct{}, 1),
	}
	// Load persisted pause state, if any.
	if r.store != nil {
		if paused, by, at, err := r.store.Load(name); err == nil && paused {
			e.isPaused.Store(true)
			e.pausedByEmployee.Store(by)
			a := at
			e.pausedAt.Store(&a)
		}
	}
	r.entries[name] = e
	return e
}

func (r *Registry) List() []CronInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CronInfo, 0, len(r.entries))
	for _, e := range r.entries { out = append(out, e.snapshot()) }
	return out
}

func (r *Registry) Get(name string) (CronInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok { return CronInfo{}, ErrCronNotFound }
	return e.snapshot(), nil
}

func (r *Registry) Pause(name string, byEmployee int64) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok { return ErrCronNotFound }
	now := time.Now().UTC()
	e.isPaused.Store(true)
	e.pausedByEmployee.Store(byEmployee)
	e.pausedAt.Store(&now)
	if r.store != nil {
		if err := r.store.Save(name, true, byEmployee, now); err != nil { return err }
	}
	return nil
}

func (r *Registry) Resume(name string) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok { return ErrCronNotFound }
	e.isPaused.Store(false)
	e.pausedByEmployee.Store(0)
	e.pausedAt.Store(nil)
	if r.store != nil {
		if err := r.store.Save(name, false, 0, time.Now().UTC()); err != nil { return err }
	}
	return nil
}

func (r *Registry) Trigger(name string, force bool, byEmployee int64) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok { return ErrCronNotFound }
	if e.isPaused.Load() && !force { return ErrCronPaused }
	select {
	case e.triggerCh <- struct{}{}:
	default:
		// Already one queued; that's fine for our purposes (a single fire-soon is enough).
	}
	return nil
}

func (e *Entry) snapshot() CronInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pa := e.pausedAt.Load()
	return CronInfo{
		Name: e.name, Service: e.service, Description: e.description,
		Interval: e.interval, CronExpression: e.cronExpression,
		LastStartedAt: copyTimePtr(e.lastStartedAt),
		LastFinishedAt: copyTimePtr(e.lastFinishedAt),
		LastError: e.lastError, NextScheduledAt: e.nextScheduledAt,
		IsPaused: e.isPaused.Load(),
		PausedByEmployee: e.pausedByEmployee.Load(),
		PausedAt: copyTimePtr(pa),
		RunCount: e.runCount.Load(), ErrorCount: e.errorCount.Load(),
	}
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil { return nil }
	c := *t
	return &c
}
```

- [ ] **Step 4: Verify tests pass**

```bash
cd contract && go test ./cronreg/... -v
```

Expected: PASS (all 7)

- [ ] **Step 5: Commit**

```bash
git add contract/cronreg/registry.go contract/cronreg/registry_test.go
git commit -m "feat(cronreg): Registry with Pause/Resume/Trigger control"
```

---

## Task C2: CronPauseState model + repo as PauseStore

**Files:**
- Create: `contract/cronreg/model.go`
- Create: `contract/cronreg/pause_store_gorm.go`

- [ ] **Step 1: Write model + store**

```go
// contract/cronreg/model.go
package cronreg

import "time"

type CronPauseState struct {
	Name           string    `gorm:"primaryKey;size:100"`
	IsPaused       bool      `gorm:"not null;default:false"`
	PausedBy       int64
	PausedAt       *time.Time
	UpdatedAt      time.Time
}

func (CronPauseState) TableName() string { return "cron_pause_state" }
```

```go
// contract/cronreg/pause_store_gorm.go
package cronreg

import (
	"errors"
	"time"
	"gorm.io/gorm"
)

type GormPauseStore struct {
	db *gorm.DB
}

func NewGormPauseStore(db *gorm.DB) *GormPauseStore { return &GormPauseStore{db: db} }

func (s *GormPauseStore) Load(name string) (bool, int64, time.Time, error) {
	var row CronPauseState
	if err := s.db.Where("name = ?", name).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) { return false, 0, time.Time{}, nil }
		return false, 0, time.Time{}, err
	}
	at := time.Time{}
	if row.PausedAt != nil { at = *row.PausedAt }
	return row.IsPaused, row.PausedBy, at, nil
}

func (s *GormPauseStore) Save(name string, paused bool, by int64, at time.Time) error {
	pa := &at
	if !paused { pa = nil }
	row := CronPauseState{Name: name, IsPaused: paused, PausedBy: by, PausedAt: pa}
	return s.db.Save(&row).Error
}
```

- [ ] **Step 2: Build**

```bash
cd contract && go build ./cronreg/...
```

- [ ] **Step 3: Commit**

```bash
git add contract/cronreg/model.go contract/cronreg/pause_store_gorm.go
git commit -m "feat(cronreg): GORM-backed PauseStore"
```

---

## Task C3: AdminCron proto definition

**Files:**
- Create: `contract/proto/admin/admin_cron.proto`

- [ ] **Step 1: Write proto**

```protobuf
syntax = "proto3";
package admin;
option go_package = "github.com/exbanka/contract/contract/adminpb";

service AdminCron {
  rpc ListCrons(ListCronsRequest) returns (ListCronsResponse);
  rpc GetCron(GetCronRequest) returns (CronInfoMsg);
  rpc TriggerCron(TriggerRequest) returns (CronCtrlResponse);
  rpc PauseCron(PauseRequest) returns (CronCtrlResponse);
  rpc ResumeCron(ResumeRequest) returns (CronCtrlResponse);
}

message ListCronsRequest {}
message ListCronsResponse { repeated CronInfoMsg crons = 1; }

message GetCronRequest { string name = 1; }

message TriggerRequest {
  string name = 1;
  bool force = 2;
  int64 triggered_by = 3;
}
message PauseRequest { string name = 1; int64 paused_by = 2; }
message ResumeRequest { string name = 1; int64 resumed_by = 2; }
message CronCtrlResponse { string status = 1; }

message CronInfoMsg {
  string name = 1;
  string service = 2;
  string description = 3;
  string interval = 4;          // duration string e.g. "30s","24h"
  string cron_expression = 5;
  string last_started_at = 6;   // RFC3339, empty if never
  string last_finished_at = 7;
  string last_error = 8;
  string next_scheduled_at = 9;
  bool   is_paused = 10;
  int64  paused_by_employee = 11;
  string paused_at = 12;
  int64  run_count = 13;
  int64  error_count = 14;
}
```

- [ ] **Step 2: Regenerate**

```bash
make proto
```

- [ ] **Step 3: Commit**

```bash
git add contract/proto/admin/ contract/adminpb/
git commit -m "feat(proto): AdminCron service"
```

---

## Task C4: AdminCron gRPC server backed by Registry

**Files:**
- Create: `contract/cronreg/grpc_server.go`

- [ ] **Step 1: Implement**

```go
// contract/cronreg/grpc_server.go
package cronreg

import (
	"context"
	"time"

	adminpb "github.com/exbanka/contract/contract/adminpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	adminpb.UnimplementedAdminCronServer
	r *Registry
}

func NewGRPCServer(r *Registry) *GRPCServer { return &GRPCServer{r: r} }

func (s *GRPCServer) ListCrons(ctx context.Context, _ *adminpb.ListCronsRequest) (*adminpb.ListCronsResponse, error) {
	infos := s.r.List()
	resp := &adminpb.ListCronsResponse{Crons: make([]*adminpb.CronInfoMsg, 0, len(infos))}
	for _, i := range infos {
		resp.Crons = append(resp.Crons, toMsg(i))
	}
	return resp, nil
}

func (s *GRPCServer) GetCron(ctx context.Context, req *adminpb.GetCronRequest) (*adminpb.CronInfoMsg, error) {
	info, err := s.r.Get(req.Name)
	if err != nil { return nil, status.Error(codes.NotFound, err.Error()) }
	return toMsg(info), nil
}

func (s *GRPCServer) TriggerCron(ctx context.Context, req *adminpb.TriggerRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Trigger(req.Name, req.Force, req.TriggeredBy); err != nil {
		if err == ErrCronNotFound { return nil, status.Error(codes.NotFound, err.Error()) }
		if err == ErrCronPaused { return nil, status.Error(codes.FailedPrecondition, err.Error()) }
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "triggered"}, nil
}

func (s *GRPCServer) PauseCron(ctx context.Context, req *adminpb.PauseRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Pause(req.Name, req.PausedBy); err != nil {
		if err == ErrCronNotFound { return nil, status.Error(codes.NotFound, err.Error()) }
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "paused"}, nil
}

func (s *GRPCServer) ResumeCron(ctx context.Context, req *adminpb.ResumeRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Resume(req.Name); err != nil {
		if err == ErrCronNotFound { return nil, status.Error(codes.NotFound, err.Error()) }
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "resumed"}, nil
}

func toMsg(i CronInfo) *adminpb.CronInfoMsg {
	return &adminpb.CronInfoMsg{
		Name: i.Name, Service: i.Service, Description: i.Description,
		Interval: i.Interval.String(), CronExpression: i.CronExpression,
		LastStartedAt: tFmt(i.LastStartedAt),
		LastFinishedAt: tFmt(i.LastFinishedAt),
		LastError: i.LastError,
		NextScheduledAt: i.NextScheduledAt.UTC().Format(time.RFC3339),
		IsPaused: i.IsPaused, PausedByEmployee: i.PausedByEmployee,
		PausedAt: tFmt(i.PausedAt),
		RunCount: i.RunCount, ErrorCount: i.ErrorCount,
	}
}

func tFmt(t *time.Time) string {
	if t == nil { return "" }
	return t.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 2: Build**

```bash
cd contract && go build ./cronreg/...
```

- [ ] **Step 3: Commit**

```bash
git add contract/cronreg/grpc_server.go
git commit -m "feat(cronreg): gRPC server backed by Registry"
```

---

## Task C5: Add three permissions

**Files:**
- Modify: `contract/permissions/perms.gen.go` or its source

- [ ] **Step 1: Add permissions**

```go
"admin.crons.view",
"admin.crons.trigger",
"admin.crons.manage",
```

- [ ] **Step 2: Seed only on EmployeeAdmin**

In `DefaultRoles["EmployeeAdmin"]`, append the three permissions. Do not add to other roles.

- [ ] **Step 3: Commit**

```bash
git add contract/permissions/ user-service/internal/service/role_service.go
git commit -m "feat(permissions): admin.crons.{view,trigger,manage}"
```

---

## Task C6: Add admin.cron-action Kafka message + topic

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Add message + topic**

```go
const TopicAdminCronAction = "admin.cron-action"

type AdminCronActionMessage struct {
	Action     string    `json:"action"`       // "trigger"|"pause"|"resume"
	Service    string    `json:"service"`
	CronName   string    `json:"cron_name"`
	EmployeeID int64     `json:"employee_id"`
	Timestamp  time.Time `json:"timestamp"`
	Reason     string    `json:"reason,omitempty"`
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat(kafka): admin.cron-action message + topic"
```

---

## Task C7: Wire Registry into stock-service (example pattern; repeat for every service)

The full per-service integration is the same pattern repeated for: stock-service, credit-service, account-service, card-service, transaction-service, notification-service, user-service.

(Services without crons — auth-service, client-service, exchange-service, verification-service — only need to register the AdminCron gRPC if they ever might gain crons; skip them for now.)

**Files (per service):**
- Modify: `<service>/cmd/main.go`
- Modify: each cron file in `<service>/internal/service/*_cron.go`

### Stock-service example

- [ ] **Step 1: In stock-service/cmd/main.go, after db is open:**

```go
import (
	cronreg "github.com/exbanka/contract/contract/cronreg"
	adminpb "github.com/exbanka/contract/contract/adminpb"
)

// AutoMigrate the pause state table
if err := db.AutoMigrate(&cronreg.CronPauseState{}); err != nil {
	log.Fatal(err)
}

// Build registry
cronRegistry := cronreg.NewRegistry("stock-service", cronreg.NewGormPauseStore(db))

// Register AdminCron gRPC alongside existing servers — add to Register: func block
Register: func(s *grpc.Server) {
	// ... existing registrations ...
	adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
},

// Ensure topic exists
shared.EnsureTopics(cfg.KafkaBrokers,
	// ... existing topics ...
	"admin.cron-action",
)
```

- [ ] **Step 2: Pass cronRegistry to every cron constructor:**

```go
taxCronSvc := service.NewTaxCronService(..., cronRegistry)
listingCron := service.NewListingCronService(..., cronRegistry)
sagaRecovery := service.NewSagaRecovery(..., cronRegistry)
priceAlertCron := service.NewPriceAlertCronService(..., cronRegistry)
recurringOrderCron := service.NewRecurringOrderCron(..., cronRegistry)
// ... and so on for every cron in main.go's 540–870 range
```

- [ ] **Step 3: Inside each cron file, register and gate**

Example for `stock-service/internal/service/tax_cron.go`:

```go
type TaxCronService struct {
	// ... existing fields ...
	registry *cronreg.Registry
	entry    *cronreg.Entry
}

func NewTaxCronService(..., registry *cronreg.Registry) *TaxCronService {
	s := &TaxCronService{ /* ... */ registry: registry }
	s.entry = registry.Register("tax-collection",
		"Collect capital-gains tax monthly", 0) // 0 interval = wall-clock cron
	return s
}

func (s *TaxCronService) StartMonthlyCron(ctx context.Context) {
	go func() {
		for {
			now := time.Now()
			nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			triggerTime := nextMonth.Add(-1 * time.Minute)
			if triggerTime.Before(now) {
				triggerTime = time.Date(now.Year(), now.Month()+2, 1, 0, 0, 0, 0, time.UTC).Add(-1 * time.Minute)
			}
			waitDuration := triggerTime.Sub(now)
			select {
			case <-time.After(waitDuration):
				if !s.entry.BeginRun() {
					log.Println("tax cron: paused, skipping this tick")
					continue
				}
				err := s.runCollection()
				s.entry.EndRun(err)
			case <-s.entry.TriggerChan():
				if !s.entry.BeginRun() { continue }
				err := s.runCollection()
				s.entry.EndRun(err)
			case <-ctx.Done():
				log.Println("tax cron: shutting down")
				return
			}
		}
	}()
}

// runCollection should return an error so it propagates to entry.EndRun
func (s *TaxCronService) runCollection() error {
	// ... existing logic ...
	return nil
}
```

- [ ] **Step 4: Build stock-service**

```bash
cd stock-service && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add stock-service/cmd/main.go stock-service/internal/service/*_cron.go stock-service/internal/service/saga_recovery.go stock-service/internal/service/recurring_order_svc.go ... # all touched cron files
git commit -m "feat(stock): integrate AdminCron registry across all stock crons"
```

---

## Task C8: Repeat C7 pattern for other services

Apply the same three-step (register cron, gate tick on BeginRun, register AdminCron gRPC) to each of:

- [ ] credit-service (`credit-installment-collection`)
- [ ] account-service (`daily-spending-reset`, `monthly-spending-reset`, `monthly-maintenance-charge`)
- [ ] card-service (`card-maintenance`)
- [ ] transaction-service (`outbound-replay-cron`, `compensation-recovery`, the new `peer-tx-reconciler` from Plan A)
- [ ] notification-service (`inbox-cleanup`)
- [ ] user-service (`outbox-relay`, `limit-cron`, `actuary-cron`)

Each is a single commit per service, same pattern. Cite the cron names from the audit's full inventory (`docs/superpowers/plans/2026-05-28-...` audit findings) — they must match exactly so the gateway aggregator can identify them.

For brevity in this plan, the per-service implementation steps are identical to C7 — repeat the pattern. **Each service is its own task with its own commit.**

---

## Task C9: API gateway — per-service AdminCron clients

**Files:**
- Create: `api-gateway/internal/grpc/admin_cron_clients.go`

- [ ] **Step 1: Implement client pool**

```go
// api-gateway/internal/grpc/admin_cron_clients.go
package grpc

import (
	adminpb "github.com/exbanka/contract/contract/adminpb"
	"google.golang.org/grpc"
)

type AdminCronClient struct {
	Service string
	Client  adminpb.AdminCronClient
	Conn    *grpc.ClientConn
}

func NewAdminCronClient(service, addr string) (*AdminCronClient, error) {
	conn, err := sagaDial(addr)
	if err != nil { return nil, err }
	return &AdminCronClient{Service: service, Client: adminpb.NewAdminCronClient(conn), Conn: conn}, nil
}
```

- [ ] **Step 2: In api-gateway/cmd/main.go, construct one client per service:**

```go
adminCronClients := []*grpcclient.AdminCronClient{}
for _, svc := range []struct{ name, addr string }{
	{"stock-service", cfg.StockGRPCAddr},
	{"credit-service", cfg.CreditGRPCAddr},
	{"account-service", cfg.AccountGRPCAddr},
	{"card-service", cfg.CardGRPCAddr},
	{"transaction-service", cfg.TransactionGRPCAddr},
	{"notification-service", cfg.NotificationGRPCAddr},
	{"user-service", cfg.UserGRPCAddr},
} {
	c, err := grpcclient.NewAdminCronClient(svc.name, svc.addr)
	if err != nil {
		log.Printf("admin cron: failed to dial %s: %v (will be reported unreachable)", svc.name, err)
		continue
	}
	adminCronClients = append(adminCronClients, c)
}
```

- [ ] **Step 3: Build**

```bash
cd api-gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/grpc/admin_cron_clients.go api-gateway/cmd/main.go
git commit -m "feat(gateway): per-service AdminCron gRPC client pool"
```

---

## Task C10: API gateway — admin_cron_handler + six routes

**Files:**
- Create: `api-gateway/internal/handler/admin_cron_handler.go`
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Implement handler with errgroup fan-out**

```go
// api-gateway/internal/handler/admin_cron_handler.go
package handler

import (
	"context"
	"net/http"
	"strconv"

	adminpb "github.com/exbanka/contract/contract/adminpb"
	grpcclient "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

type AdminCronHandler struct {
	clients map[string]*grpcclient.AdminCronClient // keyed by service name
	kafka   AuditPublisher
}

type AuditPublisher interface {
	PublishCronAction(ctx context.Context, action, service, cronName string, employeeID int64, reason string) error
}

func NewAdminCronHandler(pool []*grpcclient.AdminCronClient, kafka AuditPublisher) *AdminCronHandler {
	m := make(map[string]*grpcclient.AdminCronClient, len(pool))
	for _, c := range pool { m[c.Service] = c }
	return &AdminCronHandler{clients: m, kafka: kafka}
}

// GET /api/v3/admin/crons
func (h *AdminCronHandler) List(c *gin.Context) {
	type serviceCrons struct {
		Service string                 `json:"service"`
		Status  string                 `json:"status"`
		Crons   []*adminpb.CronInfoMsg `json:"crons"`
	}
	results := make([]serviceCrons, 0, len(h.clients))
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(c.Request.Context())
	for svc, cli := range h.clients {
		svc, cli := svc, cli
		g.Go(func() error {
			resp, err := cli.Client.ListCrons(ctx, &adminpb.ListCronsRequest{})
			entry := serviceCrons{Service: svc, Status: "ok", Crons: nil}
			if err != nil {
				entry.Status = "unreachable"
			} else {
				entry.Crons = resp.Crons
			}
			mu.Lock()
			results = append(results, entry)
			mu.Unlock()
			return nil // never fail the whole request
		})
	}
	_ = g.Wait()
	c.JSON(http.StatusOK, gin.H{"services": results})
}

// GET /api/v3/admin/crons/:service/:name
func (h *AdminCronHandler) Get(c *gin.Context) {
	cli, ok := h.clients[c.Param("service")]
	if !ok { apiError(c, 404, ErrNotFound, "unknown service"); return }
	resp, err := cli.Client.GetCron(c.Request.Context(), &adminpb.GetCronRequest{Name: c.Param("name")})
	if err != nil { handleGRPCError(c, err); return }
	c.JSON(http.StatusOK, resp)
}

// POST /api/v3/admin/crons/:service/:name/trigger
func (h *AdminCronHandler) Trigger(c *gin.Context) {
	cli, ok := h.clients[c.Param("service")]
	if !ok { apiError(c, 404, ErrNotFound, "unknown service"); return }
	var body struct {
		Force  bool   `json:"force"`
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	empID, _ := c.Get("principal_id")
	emp64, _ := empID.(int64)
	resp, err := cli.Client.TriggerCron(c.Request.Context(), &adminpb.TriggerRequest{
		Name: c.Param("name"), Force: body.Force, TriggeredBy: emp64,
	})
	if err != nil { handleGRPCError(c, err); return }
	_ = h.kafka.PublishCronAction(c.Request.Context(), "trigger", c.Param("service"), c.Param("name"), emp64, body.Reason)
	c.JSON(http.StatusOK, resp)
}

// POST /api/v3/admin/crons/:service/:name/pause
func (h *AdminCronHandler) Pause(c *gin.Context) {
	cli, ok := h.clients[c.Param("service")]
	if !ok { apiError(c, 404, ErrNotFound, "unknown service"); return }
	var body struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&body)
	empID, _ := c.Get("principal_id")
	emp64, _ := empID.(int64)
	resp, err := cli.Client.PauseCron(c.Request.Context(), &adminpb.PauseRequest{Name: c.Param("name"), PausedBy: emp64})
	if err != nil { handleGRPCError(c, err); return }
	_ = h.kafka.PublishCronAction(c.Request.Context(), "pause", c.Param("service"), c.Param("name"), emp64, body.Reason)
	c.JSON(http.StatusOK, resp)
}

// POST /api/v3/admin/crons/:service/:name/resume
func (h *AdminCronHandler) Resume(c *gin.Context) {
	cli, ok := h.clients[c.Param("service")]
	if !ok { apiError(c, 404, ErrNotFound, "unknown service"); return }
	var body struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&body)
	empID, _ := c.Get("principal_id")
	emp64, _ := empID.(int64)
	resp, err := cli.Client.ResumeCron(c.Request.Context(), &adminpb.ResumeRequest{Name: c.Param("name"), ResumedBy: emp64})
	if err != nil { handleGRPCError(c, err); return }
	_ = h.kafka.PublishCronAction(c.Request.Context(), "resume", c.Param("service"), c.Param("name"), emp64, body.Reason)
	c.JSON(http.StatusOK, resp)
}

var _ = middleware.GetCallerPermissions // avoid unused import when GetCallerPermissions lives elsewhere
```

- [ ] **Step 2: Register routes**

In `api-gateway/internal/router/router_v3.go`, in the protected admin group:

```go
admin := protected.Group("/admin")
adminCronRead := admin.Group("/crons")
adminCronRead.Use(middleware.RequirePermission("admin.crons.view"))
{
	adminCronRead.GET("", h.AdminCron.List)
	adminCronRead.GET("/:service/:name", h.AdminCron.Get)
}
adminCronTrigger := admin.Group("/crons")
adminCronTrigger.Use(middleware.RequirePermission("admin.crons.trigger"))
{
	adminCronTrigger.POST("/:service/:name/trigger", h.AdminCron.Trigger)
}
adminCronManage := admin.Group("/crons")
adminCronManage.Use(middleware.RequirePermission("admin.crons.manage"))
{
	adminCronManage.POST("/:service/:name/pause", h.AdminCron.Pause)
	adminCronManage.POST("/:service/:name/resume", h.AdminCron.Resume)
}
```

- [ ] **Step 3: AuditPublisher implementation in api-gateway/internal/kafka/**

Add a small producer wrapper that publishes `AdminCronActionMessage` to `admin.cron-action`. Wire it as the `AuditPublisher` argument when constructing the handler in main.go.

- [ ] **Step 4: Build api-gateway**

```bash
cd api-gateway && go build ./...
```

- [ ] **Step 5: Swagger + REST_API_v1**

```bash
make swagger
```

Add an "Admin / Cron Management" section to `docs/api/REST_API_v1.md`.

- [ ] **Step 6: Commit**

```bash
git add api-gateway/internal/handler/admin_cron_handler.go api-gateway/internal/router/router_v3.go api-gateway/internal/kafka/ api-gateway/cmd/main.go api-gateway/docs/ docs/api/REST_API_v1.md
git commit -m "feat(gateway): admin cron viewer endpoints with errgroup fan-out"
```

---

## Task C11: Notification-service consumer + audit log

**Files:**
- Create: `notification-service/internal/model/admin_audit_log.go`
- Create: `notification-service/internal/consumer/admin_audit_consumer.go`
- Modify: `notification-service/cmd/main.go`

- [ ] **Step 1: Model**

```go
package model

import "time"

type AdminAuditLog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	Action     string    `gorm:"size:32;not null;index"`
	Service    string    `gorm:"size:64;not null;index"`
	CronName   string    `gorm:"size:100;not null;index"`
	EmployeeID int64     `gorm:"not null;index"`
	Reason     string    `gorm:"size:512"`
	Timestamp  time.Time `gorm:"not null;index"`
}
func (AdminAuditLog) TableName() string { return "admin_audit_logs" }
```

- [ ] **Step 2: Consumer**

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/exbanka/contract/kafka/kafkamsg"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type AdminAuditConsumer struct {
	db     *gorm.DB
	reader *kafka.Reader
}

func NewAdminAuditConsumer(db *gorm.DB, brokers []string, groupID string) *AdminAuditConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, Topic: kafkamsg.TopicAdminCronAction, GroupID: groupID,
	})
	return &AdminAuditConsumer{db: db, reader: r}
}

func (c *AdminAuditConsumer) Start(ctx context.Context) {
	go func() {
		for {
			m, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil { return }
				log.Printf("admin audit consumer: read err: %v", err); continue
			}
			var msg kafkamsg.AdminCronActionMessage
			if err := json.Unmarshal(m.Value, &msg); err != nil { log.Printf("decode err: %v", err); continue }
			row := &model.AdminAuditLog{
				Action: msg.Action, Service: msg.Service, CronName: msg.CronName,
				EmployeeID: msg.EmployeeID, Reason: msg.Reason, Timestamp: msg.Timestamp,
			}
			if err := c.db.Create(row).Error; err != nil { log.Printf("insert err: %v", err) }
		}
	}()
}
```

- [ ] **Step 3: Wire in notification-service/cmd/main.go**

AutoMigrate `&model.AdminAuditLog{}`. Start the consumer. Add `admin.cron-action` to EnsureTopics.

- [ ] **Step 4: Build**

```bash
cd notification-service && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add notification-service/internal/model/admin_audit_log.go notification-service/internal/consumer/admin_audit_consumer.go notification-service/cmd/main.go
git commit -m "feat(notification): admin cron action audit log consumer"
```

---

## Task C12: Integration test

**Files:**
- Create: `test-app/workflows/admin_cron_test.go`

- [ ] **Step 1: Write test**

```go
package workflows

import (
	"testing"
	"time"
)

func TestAdminCron_ListPauseResume(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()

	// 1. List crons — expect at least one from each major service
	resp := tc.GET("/api/v3/admin/crons")
	if resp.StatusCode != 200 { t.Fatalf("list status=%d", resp.StatusCode) }

	// 2. Pause card-maintenance (1-min interval; we'll watch it not fire)
	pauseResp := tc.POST("/api/v3/admin/crons/card-service/card-maintenance/pause",
		map[string]any{"reason": "test"})
	if pauseResp.StatusCode != 200 { t.Fatalf("pause status=%d", pauseResp.StatusCode) }

	// 3. Get the cron's state — IsPaused must be true
	getResp := tc.GET("/api/v3/admin/crons/card-service/card-maintenance")
	body := tc.decodeJSON(getResp)
	if body["is_paused"] != true { t.Errorf("expected is_paused=true, got %v", body["is_paused"]) }

	// 4. Try trigger without force — expect 412 / 409
	triggerNoForce := tc.POST("/api/v3/admin/crons/card-service/card-maintenance/trigger",
		map[string]any{"force": false})
	if triggerNoForce.StatusCode == 200 { t.Error("expected non-200 for trigger on paused cron without force") }

	// 5. Trigger WITH force — expect 200
	triggerForce := tc.POST("/api/v3/admin/crons/card-service/card-maintenance/trigger",
		map[string]any{"force": true})
	if triggerForce.StatusCode != 200 { t.Errorf("force trigger status=%d", triggerForce.StatusCode) }

	// 6. Resume — IsPaused must be false
	resumeResp := tc.POST("/api/v3/admin/crons/card-service/card-maintenance/resume", nil)
	if resumeResp.StatusCode != 200 { t.Fatalf("resume status=%d", resumeResp.StatusCode) }
	getResp2 := tc.GET("/api/v3/admin/crons/card-service/card-maintenance")
	body2 := tc.decodeJSON(getResp2)
	if body2["is_paused"] != false { t.Errorf("expected is_paused=false, got %v", body2["is_paused"]) }
}

func TestAdminCron_NonAdminDenied(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()
	supervisorID := tc.createEmployee("sup@x.com", "EmployeeSupervisor")
	tc.loginEmployee(supervisorID)
	resp := tc.GET("/api/v3/admin/crons")
	if resp.StatusCode != 403 { t.Errorf("expected 403 for non-admin, got %d", resp.StatusCode) }
}

func TestAdminCron_PauseSurvivesRestart(t *testing.T) {
	t.Skip("Requires docker restart between assertions — manual or with docker SDK")
}
```

- [ ] **Step 2: Run**

```bash
make docker-up
sleep 30
cd test-app && go test ./workflows/... -run TestAdminCron -v -timeout 3m
```

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/admin_cron_test.go
git commit -m "test: admin cron viewer integration tests"
```

---

## Task C13: Update Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Section 6** — add `admin.crons.view`, `admin.crons.trigger`, `admin.crons.manage`, all only on `EmployeeAdmin`.
- [ ] **Step 2: Section 11** — add `admin.AdminCron` service (exposed by every service that has crons).
- [ ] **Step 3: Section 17** — document the 5 admin/crons routes.
- [ ] **Step 4: Section 18** — add `cron_pause_state`, `admin_audit_logs`.
- [ ] **Step 5: Section 19** — add `admin.cron-action` topic.
- [ ] **Step 6: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): document admin cron viewer surface"
```

---

## Self-Review

- [ ] Spec coverage:
  - C1 Registry: Task C1, C2 ✓
  - C2 Pause persistence: Task C2 ✓
  - C3 gRPC service: Task C3, C4 ✓
  - C4 Gateway aggregation: Task C9, C10 ✓
  - C5 Authorization: Task C5 (perms), C10 (routing under RequirePermission) ✓
  - C6 Audit trail: Task C6 (msg), C10 (publish), C11 (consume) ✓
  - C7 Response shape: Task C10 ✓
  - C8 Implementation surface: Task C7, C8 ✓
- [ ] Placeholder scan: One `t.Skip` for restart-test (acceptable — manual verification). C8 is templated rather than fully expanded per service to avoid bloat, but the pattern from C7 is fully specified.
- [ ] Type consistency: `CronInfo`, `Entry`, `Registry`, `GormPauseStore`, `AdminCronHandler` consistently named throughout.

---

## Execution Handoff

Implementation will proceed via **subagent-driven-development**.
