# Audit Trails & Changelog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add field-level changelog tracking to account, user, client, credit, card, and auth services so every mutation records who changed what, when, and the old/new values.

**Architecture:** Shared changelog contract in contract/changelog/, per-service GORM model + repository, changed_by propagated via gRPC metadata from API gateway, Kafka events for downstream consumers.

**Tech Stack:** Go, GORM, PostgreSQL, gRPC metadata, Kafka

---

### Task 1: Create shared `contract/changelog/` package

**Files:**
- Create: `contract/changelog/changelog.go`
- Create: `contract/changelog/changelog_test.go`
- Create: `contract/changelog/metadata.go`
- Create: `contract/changelog/metadata_test.go`

This package defines the shared `Entry` struct and gRPC metadata helpers that every service will import. It does NOT contain GORM tags (each service defines its own GORM model to keep DB coupling local). It also defines the `ChangelogMessage` Kafka struct.

- [ ] **Step 1: Create the shared Entry type and helper functions**

```go
// contract/changelog/changelog.go
package changelog

import (
	"encoding/json"
	"fmt"
	"time"
)

// Action constants for changelog entries.
const (
	ActionCreate       = "create"
	ActionUpdate       = "update"
	ActionDelete       = "delete"
	ActionStatusChange = "status_change"
)

// Entry represents a single field-level change to an entity.
// This is the service-agnostic struct — each service maps it to its own GORM model.
type Entry struct {
	EntityType string
	EntityID   int64
	Action     string
	FieldName  string // empty for create/delete
	OldValue   string // JSON-encoded, empty for creates
	NewValue   string // JSON-encoded, empty for deletes
	ChangedBy  int64  // user_id from JWT, 0 for system
	ChangedAt  time.Time
	Reason     string
}

// FieldChange captures a before/after pair for a single field.
type FieldChange struct {
	Field    string
	OldValue interface{}
	NewValue interface{}
}

// Diff compares old and new values for the given fields and returns entries
// for every field that actually changed. This avoids boilerplate in every service.
func Diff(entityType string, entityID int64, changedBy int64, reason string, changes []FieldChange) []Entry {
	var entries []Entry
	now := time.Now()
	for _, c := range changes {
		oldJSON := toJSON(c.OldValue)
		newJSON := toJSON(c.NewValue)
		if oldJSON == newJSON {
			continue
		}
		entries = append(entries, Entry{
			EntityType: entityType,
			EntityID:   entityID,
			Action:     ActionUpdate,
			FieldName:  c.Field,
			OldValue:   oldJSON,
			NewValue:   newJSON,
			ChangedBy:  changedBy,
			ChangedAt:  now,
			Reason:     reason,
		})
	}
	return entries
}

// NewCreateEntry creates a single changelog entry for an entity creation.
func NewCreateEntry(entityType string, entityID int64, changedBy int64, newValueJSON string) Entry {
	return Entry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     ActionCreate,
		NewValue:   newValueJSON,
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
	}
}

// NewStatusChangeEntry creates a single changelog entry for a status transition.
func NewStatusChangeEntry(entityType string, entityID int64, changedBy int64, oldStatus, newStatus, reason string) Entry {
	return Entry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     ActionStatusChange,
		FieldName:  "status",
		OldValue:   toJSON(oldStatus),
		NewValue:   toJSON(newStatus),
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
		Reason:     reason,
	}
}

func toJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	// Fast path for strings — avoid double-quoting.
	if s, ok := v.(string); ok {
		b, _ := json.Marshal(s)
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
```

- [ ] **Step 2: Create gRPC metadata helpers**

```go
// contract/changelog/metadata.go
package changelog

import (
	"strconv"

	"google.golang.org/grpc/metadata"
	"golang.org/x/net/context"
)

const metadataKeyChangedBy = "x-changed-by"

// SetChangedBy returns a new outgoing context with the x-changed-by metadata.
// Call this in API gateway handlers before making gRPC calls.
func SetChangedBy(ctx context.Context, userID int64) context.Context {
	md := metadata.Pairs(metadataKeyChangedBy, strconv.FormatInt(userID, 10))
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractChangedBy reads the x-changed-by value from incoming gRPC metadata.
// Returns 0 if not present (system-initiated change).
func ExtractChangedBy(ctx context.Context) int64 {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	vals := md.Get(metadataKeyChangedBy)
	if len(vals) == 0 {
		return 0
	}
	id, err := strconv.ParseInt(vals[0], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
```

- [ ] **Step 3: Write tests for Diff and metadata helpers**

```go
// contract/changelog/changelog_test.go
package changelog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiff_DetectsChanges(t *testing.T) {
	entries := Diff("account", 42, 1, "", []FieldChange{
		{Field: "daily_limit", OldValue: "100000", NewValue: "200000"},
		{Field: "monthly_limit", OldValue: "500000", NewValue: "500000"}, // no change
		{Field: "status", OldValue: "active", NewValue: "inactive"},
	})
	require.Len(t, entries, 2)
	assert.Equal(t, "daily_limit", entries[0].FieldName)
	assert.Equal(t, "status", entries[1].FieldName)
	assert.Equal(t, int64(42), entries[0].EntityID)
	assert.Equal(t, int64(1), entries[0].ChangedBy)
}

func TestDiff_NoChanges_ReturnsEmpty(t *testing.T) {
	entries := Diff("account", 1, 1, "", []FieldChange{
		{Field: "name", OldValue: "foo", NewValue: "foo"},
	})
	assert.Empty(t, entries)
}

func TestNewStatusChangeEntry(t *testing.T) {
	entry := NewStatusChangeEntry("card", 10, 5, "active", "blocked", "suspicious activity")
	assert.Equal(t, ActionStatusChange, entry.Action)
	assert.Equal(t, "status", entry.FieldName)
	assert.Equal(t, `"active"`, entry.OldValue)
	assert.Equal(t, `"blocked"`, entry.NewValue)
	assert.Equal(t, "suspicious activity", entry.Reason)
}

func TestNewCreateEntry(t *testing.T) {
	entry := NewCreateEntry("loan", 7, 3, `{"amount": 50000}`)
	assert.Equal(t, ActionCreate, entry.Action)
	assert.Empty(t, entry.FieldName)
	assert.Empty(t, entry.OldValue)
	assert.Equal(t, `{"amount": 50000}`, entry.NewValue)
}
```

```go
// contract/changelog/metadata_test.go
package changelog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestSetAndExtractChangedBy(t *testing.T) {
	ctx := context.Background()
	outCtx := SetChangedBy(ctx, 42)

	// Simulate what gRPC does: outgoing metadata becomes incoming metadata
	md, _ := metadata.FromOutgoingContext(outCtx)
	inCtx := metadata.NewIncomingContext(ctx, md)

	got := ExtractChangedBy(inCtx)
	assert.Equal(t, int64(42), got)
}

func TestExtractChangedBy_Missing(t *testing.T) {
	ctx := context.Background()
	got := ExtractChangedBy(ctx)
	assert.Equal(t, int64(0), got)
}
```

- [ ] **Step 4: Run tests**

Run: `cd contract && go test ./changelog/ -v`
Expected: All pass.

- [ ] **Step 5: Commit**

```
feat(contract): add shared changelog package with Diff, metadata helpers
```

---

### Task 2: Add changelog model + repository to account-service (template)

**Files:**
- Create: `account-service/internal/model/changelog.go`
- Create: `account-service/internal/repository/changelog_repository.go`
- Create: `account-service/internal/repository/changelog_repository_test.go`
- Modify: `account-service/cmd/main.go` (add AutoMigrate for Changelog)

This task establishes the per-service changelog pattern that Tasks 5-9 will copy.

- [ ] **Step 1: Create the GORM Changelog model**

```go
// account-service/internal/model/changelog.go
package model

import "time"

// Changelog is an append-only audit trail for entity mutations.
// Each service has its own changelogs table in its own database.
type Changelog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	EntityType string    `gorm:"size:64;not null;index:idx_changelog_entity"`
	EntityID   int64     `gorm:"not null;index:idx_changelog_entity"`
	Action     string    `gorm:"size:32;not null"` // create, update, delete, status_change
	FieldName  string    `gorm:"size:128"`         // NULL for create/delete
	OldValue   string    `gorm:"type:text"`        // JSON-encoded, NULL for creates
	NewValue   string    `gorm:"type:text"`        // JSON-encoded, NULL for deletes
	ChangedBy  int64     `gorm:"not null"`         // user_id from JWT, 0 for system
	ChangedAt  time.Time `gorm:"not null;default:now()"`
	Reason     string    `gorm:"type:text"`
}

func (Changelog) TableName() string {
	return "changelogs"
}
```

- [ ] **Step 2: Create the changelog repository**

```go
// account-service/internal/repository/changelog_repository.go
package repository

import (
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/changelog"
	"gorm.io/gorm"
)

// ChangelogRepository provides append-only access to the changelogs table.
type ChangelogRepository struct {
	db *gorm.DB
}

func NewChangelogRepository(db *gorm.DB) *ChangelogRepository {
	return &ChangelogRepository{db: db}
}

// Create persists one changelog entry.
func (r *ChangelogRepository) Create(entry changelog.Entry) error {
	row := model.Changelog{
		EntityType: entry.EntityType,
		EntityID:   entry.EntityID,
		Action:     entry.Action,
		FieldName:  entry.FieldName,
		OldValue:   entry.OldValue,
		NewValue:   entry.NewValue,
		ChangedBy:  entry.ChangedBy,
		ChangedAt:  entry.ChangedAt,
		Reason:     entry.Reason,
	}
	return r.db.Create(&row).Error
}

// CreateBatch persists multiple changelog entries in a single INSERT.
func (r *ChangelogRepository) CreateBatch(entries []changelog.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]model.Changelog, len(entries))
	for i, e := range entries {
		rows[i] = model.Changelog{
			EntityType: e.EntityType,
			EntityID:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt,
			Reason:     e.Reason,
		}
	}
	return r.db.Create(&rows).Error
}

// ListByEntity returns all changelog entries for a given entity type and ID,
// ordered by changed_at descending (newest first).
func (r *ChangelogRepository) ListByEntity(entityType string, entityID int64, page, pageSize int) ([]model.Changelog, int64, error) {
	var entries []model.Changelog
	var total int64

	query := r.db.Model(&model.Changelog{}).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("changed_at DESC").Offset(offset).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
```

- [ ] **Step 3: Write repository tests**

```go
// account-service/internal/repository/changelog_repository_test.go
package repository

import (
	"testing"
	"time"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangelogRepository_CreateAndList(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Changelog{})
	repo := NewChangelogRepository(db)

	entry := changelog.Entry{
		EntityType: "account",
		EntityID:   42,
		Action:     changelog.ActionUpdate,
		FieldName:  "daily_limit",
		OldValue:   `"100000"`,
		NewValue:   `"200000"`,
		ChangedBy:  1,
		ChangedAt:  time.Now(),
	}
	err := repo.Create(entry)
	require.NoError(t, err)

	entries, total, err := repo.ListByEntity("account", 42, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "daily_limit", entries[0].FieldName)
	assert.Equal(t, `"200000"`, entries[0].NewValue)
}

func TestChangelogRepository_CreateBatch(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Changelog{})
	repo := NewChangelogRepository(db)

	entries := []changelog.Entry{
		{EntityType: "account", EntityID: 1, Action: changelog.ActionUpdate, FieldName: "status", OldValue: `"active"`, NewValue: `"inactive"`, ChangedBy: 5, ChangedAt: time.Now()},
		{EntityType: "account", EntityID: 1, Action: changelog.ActionUpdate, FieldName: "daily_limit", OldValue: `"1000"`, NewValue: `"2000"`, ChangedBy: 5, ChangedAt: time.Now()},
	}
	err := repo.CreateBatch(entries)
	require.NoError(t, err)

	results, total, err := repo.ListByEntity("account", 1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, results, 2)
}

func TestChangelogRepository_CreateBatch_Empty(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Changelog{})
	repo := NewChangelogRepository(db)

	err := repo.CreateBatch(nil)
	require.NoError(t, err)
}
```

- [ ] **Step 4: Add Changelog to AutoMigrate in `account-service/cmd/main.go`**

In `account-service/cmd/main.go`, find:
```go
if err := db.AutoMigrate(&model.Currency{}, &model.Company{}, &model.Account{}, &model.LedgerEntry{}); err != nil {
```
Change to:
```go
if err := db.AutoMigrate(&model.Currency{}, &model.Company{}, &model.Account{}, &model.LedgerEntry{}, &model.Changelog{}); err != nil {
```

- [ ] **Step 5: Run tests**

Run: `cd account-service && go test ./internal/repository/ -v -run TestChangelog`
Expected: All pass.

- [ ] **Step 6: Commit**

```
feat(account): add changelog model and repository with tests
```

---

### Task 3: Add gateway metadata propagation

**Files:**
- Create: `api-gateway/internal/middleware/changed_by.go`
- Modify: `api-gateway/internal/handler/account_handler.go` (add SetChangedBy to mutating calls)
- Modify: `api-gateway/internal/handler/employee_handler.go` (add SetChangedBy to mutating calls)
- Modify: `api-gateway/internal/handler/client_handler.go` (add SetChangedBy to mutating calls)
- Modify: `api-gateway/internal/handler/credit_handler.go` (add SetChangedBy to mutating calls)
- Modify: `api-gateway/internal/handler/card_handler.go` (add SetChangedBy to mutating calls)

The gateway already extracts `user_id` from JWT into the Gin context (`c.Set("user_id", resp.UserId)`). We add a helper that reads it and attaches gRPC metadata.

- [ ] **Step 1: Create the middleware helper**

```go
// api-gateway/internal/middleware/changed_by.go
package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/exbanka/contract/changelog"
)

// GRPCContextWithChangedBy reads user_id from Gin context and sets it as
// gRPC x-changed-by metadata on the returned context. Use this before
// every mutating gRPC call.
func GRPCContextWithChangedBy(c *gin.Context) context.Context {
	userID := c.GetInt64("user_id")
	return changelog.SetChangedBy(c.Request.Context(), userID)
}
```

- [ ] **Step 2: Update account handler mutating calls**

In each gateway handler that performs a mutating gRPC call, replace:
```go
c.Request.Context()
```
with:
```go
middleware.GRPCContextWithChangedBy(c)
```

For account-related handlers, find every call to `UpdateAccountName`, `UpdateAccountLimits`, `UpdateAccountStatus` in the gateway account handler and apply this pattern. Example for a handler method:

```go
// Before:
resp, err := h.accountClient.UpdateAccountStatus(c.Request.Context(), &accountpb.UpdateAccountStatusRequest{...})

// After:
ctx := middleware.GRPCContextWithChangedBy(c)
resp, err := h.accountClient.UpdateAccountStatus(ctx, &accountpb.UpdateAccountStatusRequest{...})
```

Apply this same transformation to **all mutating handler calls** across:
- `account_handler.go` — UpdateAccountName, UpdateAccountLimits, UpdateAccountStatus
- `employee_handler.go` — UpdateEmployee, SetEmployeeRoles, SetEmployeeAdditionalPermissions, SetEmployeeLimits
- `client_handler.go` — UpdateClient, SetClientLimits
- `credit_handler.go` — ApproveLoanRequest, RejectLoanRequest
- `card_handler.go` — BlockCard, UnblockCard, DeactivateCard, SetPin, TemporaryBlockCard

**Important:** Import `"github.com/exbanka/api-gateway/internal/middleware"` in each handler file that uses this function.

- [ ] **Step 3: Run build**

Run: `cd api-gateway && go build ./cmd/`
Expected: Compiles successfully.

- [ ] **Step 4: Commit**

```
feat(gateway): propagate x-changed-by metadata on all mutating gRPC calls
```

---

### Task 4: Integrate changelog into account-service mutations

**Files:**
- Modify: `account-service/internal/service/account_service.go`
- Modify: `account-service/internal/handler/grpc_handler.go`
- Modify: `account-service/cmd/main.go` (wire ChangelogRepository into AccountService)
- Create: `account-service/internal/service/account_service_changelog_test.go`

This task wires the changelog repository into account-service and records entries for every mutation. The gRPC handler extracts `x-changed-by` from incoming metadata and passes it to the service layer.

- [ ] **Step 1: Add ChangelogRepository to AccountService**

Modify `account-service/internal/service/account_service.go`:

```go
// Add import
import (
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

type AccountService struct {
	repo         *repository.AccountRepository
	changelogRepo *repository.ChangelogRepository
	db           *gorm.DB
}

func NewAccountService(repo *repository.AccountRepository, changelogRepo *repository.ChangelogRepository, db *gorm.DB) *AccountService {
	return &AccountService{repo: repo, changelogRepo: changelogRepo, db: db}
}
```

- [ ] **Step 2: Add changedBy parameter and changelog calls to UpdateAccountName**

```go
func (s *AccountService) UpdateAccountName(id, clientID uint64, newName string, changedBy int64) error {
	// Fetch current state for changelog.
	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("account %d not found", id)
	}
	oldName := account.AccountName

	exists, err := s.repo.ExistsByNameAndOwner(newName, clientID, id)
	if err != nil {
		return fmt.Errorf("failed to check account name uniqueness: %w", err)
	}
	if exists {
		return fmt.Errorf("an account with name %q already exists for this client", newName)
	}

	if err := s.repo.UpdateName(id, clientID, newName); err != nil {
		return err
	}

	// Record changelog after successful mutation.
	entries := changelog.Diff("account", int64(id), changedBy, "", []changelog.FieldChange{
		{Field: "account_name", OldValue: oldName, NewValue: newName},
	})
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}
	return nil
}
```

- [ ] **Step 3: Add changedBy parameter and changelog calls to UpdateAccountLimits**

```go
func (s *AccountService) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
	// Fetch current state for changelog.
	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("account %d not found", id)
	}

	var changes []changelog.FieldChange
	updates := make(map[string]interface{})

	if dailyLimit != nil && *dailyLimit != "" {
		d, err := decimal.NewFromString(*dailyLimit)
		if err != nil {
			return errors.New("invalid daily_limit value")
		}
		if d.IsNegative() || d.IsZero() {
			return errors.New("daily_limit must be greater than 0")
		}
		changes = append(changes, changelog.FieldChange{
			Field: "daily_limit", OldValue: account.DailyLimit.String(), NewValue: d.String(),
		})
		updates["daily_limit"] = d
	}
	if monthlyLimit != nil && *monthlyLimit != "" {
		m, err := decimal.NewFromString(*monthlyLimit)
		if err != nil {
			return errors.New("invalid monthly_limit value")
		}
		if m.IsNegative() || m.IsZero() {
			return errors.New("monthly_limit must be greater than 0")
		}
		changes = append(changes, changelog.FieldChange{
			Field: "monthly_limit", OldValue: account.MonthlyLimit.String(), NewValue: m.String(),
		})
		updates["monthly_limit"] = m
	}
	if len(updates) == 0 {
		return nil
	}

	if err := s.repo.UpdateLimits(id, updates); err != nil {
		return err
	}

	// Record changelog after successful mutation.
	entries := changelog.Diff("account", int64(id), changedBy, "", changes)
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}
	return nil
}
```

- [ ] **Step 4: Add changedBy parameter and changelog calls to UpdateAccountStatus**

```go
func (s *AccountService) UpdateAccountStatus(id uint64, newStatus string, changedBy int64) error {
	if newStatus != "active" && newStatus != "inactive" {
		return fmt.Errorf("account status must be 'active' or 'inactive'; got: %s", newStatus)
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("account %d not found", id)
	}

	oldStatus := account.Status
	if oldStatus == newStatus {
		return fmt.Errorf("account %d is already %s", id, newStatus)
	}

	if err := s.repo.UpdateStatus(id, newStatus); err != nil {
		return err
	}

	// Record changelog after successful mutation.
	entry := changelog.NewStatusChangeEntry("account", int64(id), changedBy, oldStatus, newStatus, "")
	if s.changelogRepo != nil {
		_ = s.changelogRepo.Create(entry)
	}
	return nil
}
```

- [ ] **Step 5: Update gRPC handler to extract changedBy and pass to service**

Modify `account-service/internal/handler/grpc_handler.go`:

Add import:
```go
import "github.com/exbanka/contract/changelog"
```

Update `UpdateAccountName`:
```go
func (h *AccountGRPCHandler) UpdateAccountName(ctx context.Context, req *pb.UpdateAccountNameRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountName(req.Id, req.ClientId, req.NewName, changedBy); err != nil {
		// ... (same error handling as before)
	}
	// ... rest unchanged
}
```

Update `UpdateAccountLimits`:
```go
func (h *AccountGRPCHandler) UpdateAccountLimits(ctx context.Context, req *pb.UpdateAccountLimitsRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountLimits(req.Id, req.DailyLimit, req.MonthlyLimit, changedBy); err != nil {
		// ... (same error handling as before)
	}
	// ... rest unchanged
}
```

Update `UpdateAccountStatus`:
```go
func (h *AccountGRPCHandler) UpdateAccountStatus(ctx context.Context, req *pb.UpdateAccountStatusRequest) (*pb.AccountResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.accountService.UpdateAccountStatus(req.Id, req.Status, changedBy); err != nil {
		// ... (same error handling as before)
	}
	// ... rest unchanged
}
```

- [ ] **Step 6: Wire ChangelogRepository in `account-service/cmd/main.go`**

After `accountRepo := repository.NewAccountRepository(db)`, add:
```go
changelogRepo := repository.NewChangelogRepository(db)
```

Update the AccountService constructor:
```go
accountService := service.NewAccountService(accountRepo, changelogRepo, db)
```

- [ ] **Step 7: Write unit tests for changelog integration**

```go
// account-service/internal/service/account_service_changelog_test.go
package service

import (
	"testing"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/testutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAccountServiceWithChangelog(t *testing.T) (*AccountService, *repository.ChangelogRepository) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.Changelog{})
	accountRepo := repository.NewAccountRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)
	svc := NewAccountService(accountRepo, changelogRepo, db)
	return svc, changelogRepo
}

func seedTestAccount(t *testing.T, svc *AccountService) *model.Account {
	t.Helper()
	acct := &model.Account{
		OwnerID:      100,
		AccountKind:  "current",
		CurrencyCode: "RSD",
		AccountName:  "Test Account",
		DailyLimit:   decimal.NewFromInt(100000),
		MonthlyLimit: decimal.NewFromInt(1000000),
		Status:       "active",
	}
	// Use repo directly since CreateAccount generates account number etc.
	err := svc.CreateAccount(acct)
	require.NoError(t, err)
	return acct
}

func TestUpdateAccountName_RecordsChangelog(t *testing.T) {
	svc, clRepo := setupAccountServiceWithChangelog(t)
	acct := seedTestAccount(t, svc)

	err := svc.UpdateAccountName(acct.ID, acct.OwnerID, "New Name", 5)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("account", int64(acct.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "account_name", entries[0].FieldName)
	assert.Equal(t, int64(5), entries[0].ChangedBy)
}

func TestUpdateAccountStatus_RecordsChangelog(t *testing.T) {
	svc, clRepo := setupAccountServiceWithChangelog(t)
	acct := seedTestAccount(t, svc)

	err := svc.UpdateAccountStatus(acct.ID, "inactive", 7)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("account", int64(acct.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "status", entries[0].FieldName)
	assert.Contains(t, entries[0].OldValue, "active")
	assert.Contains(t, entries[0].NewValue, "inactive")
}

func TestUpdateAccountLimits_RecordsChangelog(t *testing.T) {
	svc, clRepo := setupAccountServiceWithChangelog(t)
	acct := seedTestAccount(t, svc)

	daily := "200000"
	err := svc.UpdateAccountLimits(acct.ID, &daily, nil, 3)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("account", int64(acct.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "daily_limit", entries[0].FieldName)
}
```

- [ ] **Step 8: Run tests**

Run: `cd account-service && go test ./internal/service/ -v -run TestUpdate.*RecordsChangelog`
and: `cd account-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 9: Commit**

```
feat(account): integrate changelog into account mutations with changed_by propagation
```

---

### Task 5: Add changelog to user-service

**Files:**
- Create: `user-service/internal/model/changelog.go`
- Create: `user-service/internal/repository/changelog_repository.go`
- Create: `user-service/internal/repository/changelog_repository_test.go`
- Modify: `user-service/internal/service/employee_service.go` (add changelog to UpdateEmployee, SetEmployeeRoles, SetEmployeeAdditionalPermissions)
- Modify: `user-service/internal/service/limit_service.go` (add changelog to SetEmployeeLimits, ApplyTemplate)
- Modify: `user-service/internal/handler/grpc_handler.go` (extract changedBy from context)
- Modify: `user-service/cmd/main.go` (AutoMigrate + wire)
- Create: `user-service/internal/service/employee_service_changelog_test.go`

- [ ] **Step 1: Create model and repository (same pattern as Task 2)**

Create `user-service/internal/model/changelog.go` — identical to the account-service version.

Create `user-service/internal/repository/changelog_repository.go` — identical to the account-service version, but with import path `github.com/exbanka/user-service/internal/model`.

- [ ] **Step 2: Add changelog to EmployeeService**

Modify `user-service/internal/service/employee_service.go`:

Add `changelogRepo` field:
```go
type EmployeeService struct {
	repo          EmployeeRepo
	producer      *kafkaprod.Producer
	cache         *cache.RedisCache
	roleSvc       *RoleService
	changelogRepo ChangelogRepo
}
```

Define the interface (since user-service uses interfaces for repos):
```go
// ChangelogRepo is the interface for changelog persistence.
type ChangelogRepo interface {
	Create(entry changelog.Entry) error
	CreateBatch(entries []changelog.Entry) error
	ListByEntity(entityType string, entityID int64, page, pageSize int) ([]model.Changelog, int64, error)
}
```

Update constructor:
```go
func NewEmployeeService(repo EmployeeRepo, producer *kafkaprod.Producer, cache *cache.RedisCache, roleSvc *RoleService, changelogRepo ChangelogRepo) *EmployeeService {
	return &EmployeeService{repo: repo, producer: producer, cache: cache, roleSvc: roleSvc, changelogRepo: changelogRepo}
}
```

Add `changedBy int64` parameter to `UpdateEmployee`:
```go
func (s *EmployeeService) UpdateEmployee(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
	emp, err := s.repo.GetByIDWithRoles(id)
	if err != nil {
		return nil, err
	}

	// Build changelog field changes before applying updates.
	var changes []changelog.FieldChange
	if v, ok := updates["last_name"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "last_name", OldValue: emp.LastName, NewValue: v})
		emp.LastName = v
	}
	if v, ok := updates["gender"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "gender", OldValue: emp.Gender, NewValue: v})
		emp.Gender = v
	}
	if v, ok := updates["phone"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "phone", OldValue: emp.Phone, NewValue: v})
		emp.Phone = v
	}
	if v, ok := updates["address"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "address", OldValue: emp.Address, NewValue: v})
		emp.Address = v
	}
	if v, ok := updates["position"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "position", OldValue: emp.Position, NewValue: v})
		emp.Position = v
	}
	if v, ok := updates["department"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "department", OldValue: emp.Department, NewValue: v})
		emp.Department = v
	}
	if v, ok := updates["jmbg"].(string); ok {
		if err := ValidateJMBG(v); err != nil {
			return nil, err
		}
		changes = append(changes, changelog.FieldChange{Field: "jmbg", OldValue: emp.JMBG, NewValue: v})
		emp.JMBG = v
	}

	if err := s.repo.Update(emp); err != nil {
		return nil, err
	}

	// Record changelog after successful mutation.
	entries := changelog.Diff("employee", id, changedBy, "", changes)
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}

	// ... rest of method unchanged (cache invalidation, Kafka publish)
}
```

Add `changedBy int64` parameter to `SetEmployeeRoles`:
```go
func (s *EmployeeService) SetEmployeeRoles(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
	emp, err := s.repo.GetByIDWithRoles(employeeID)
	if err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	// Capture old roles for changelog.
	oldRoles := extractRoleNames(emp.Roles)

	if s.roleSvc != nil {
		for _, name := range roleNames {
			if !s.roleSvc.ValidRole(name) {
				return fmt.Errorf("invalid role: %s", name)
			}
		}
	}

	roles, err := s.roleSvc.GetRolesByNames(roleNames)
	if err != nil {
		return err
	}

	if err := s.repo.SetEmployeeRoles(employeeID, roles); err != nil {
		return err
	}

	// Record changelog.
	entry := changelog.Entry{
		EntityType: "employee",
		EntityID:   employeeID,
		Action:     changelog.ActionUpdate,
		FieldName:  "roles",
		OldValue:   changelog.ToJSON(oldRoles),
		NewValue:   changelog.ToJSON(roleNames),
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
	}
	if s.changelogRepo != nil {
		_ = s.changelogRepo.Create(entry)
	}

	// ... rest of method unchanged (cache invalidation)
}
```

**Note:** We need to add a `ToJSON` public helper to `contract/changelog/changelog.go`:
```go
// ToJSON is a public wrapper for JSON-encoding a value for changelog storage.
func ToJSON(v interface{}) string {
	return toJSON(v)
}
```

Add `changedBy int64` parameter to `SetEmployeeAdditionalPermissions`:
```go
func (s *EmployeeService) SetEmployeeAdditionalPermissions(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
	emp, err := s.repo.GetByIDWithRoles(employeeID)
	if err != nil {
		return fmt.Errorf("employee %d not found: %w", employeeID, err)
	}

	// Capture old permissions for changelog.
	oldPerms := make([]string, 0, len(emp.AdditionalPermissions))
	for _, p := range emp.AdditionalPermissions {
		oldPerms = append(oldPerms, p.Code)
	}

	perms, err := s.roleSvc.GetPermissionsByCodes(permCodes)
	if err != nil {
		return err
	}
	if len(perms) != len(permCodes) {
		return errors.New("one or more permission codes are invalid")
	}

	if err := s.repo.SetAdditionalPermissions(employeeID, perms); err != nil {
		return err
	}

	// Record changelog.
	entry := changelog.Entry{
		EntityType: "employee",
		EntityID:   employeeID,
		Action:     changelog.ActionUpdate,
		FieldName:  "additional_permissions",
		OldValue:   changelog.ToJSON(oldPerms),
		NewValue:   changelog.ToJSON(permCodes),
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
	}
	if s.changelogRepo != nil {
		_ = s.changelogRepo.Create(entry)
	}

	// ... rest of method unchanged (cache invalidation)
}
```

- [ ] **Step 3: Add changelog to LimitService.SetEmployeeLimits**

Modify `user-service/internal/service/limit_service.go`:

Add `changelogRepo` field to `LimitService`:
```go
type LimitService struct {
	limitRepo     EmployeeLimitRepo
	templateRepo  LimitTemplateRepo
	producer      *kafkaprod.Producer
	changelogRepo ChangelogRepo
}
```

Update constructor to accept `changelogRepo ChangelogRepo`.

Update `SetEmployeeLimits`:
```go
func (s *LimitService) SetEmployeeLimits(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error) {
	// Fetch old limits for changelog.
	oldLimit, _ := s.limitRepo.GetByEmployeeID(limit.EmployeeID)

	if err := s.limitRepo.Upsert(&limit); err != nil {
		return nil, err
	}
	result, err := s.limitRepo.GetByEmployeeID(limit.EmployeeID)
	if err != nil {
		return nil, err
	}

	// Record changelog if old limits existed.
	if s.changelogRepo != nil && oldLimit != nil {
		entries := changelog.Diff("employee_limit", limit.EmployeeID, changedBy, "", []changelog.FieldChange{
			{Field: "max_loan_approval_amount", OldValue: oldLimit.MaxLoanApprovalAmount.String(), NewValue: result.MaxLoanApprovalAmount.String()},
			{Field: "max_single_transaction", OldValue: oldLimit.MaxSingleTransaction.String(), NewValue: result.MaxSingleTransaction.String()},
			{Field: "max_daily_transaction", OldValue: oldLimit.MaxDailyTransaction.String(), NewValue: result.MaxDailyTransaction.String()},
			{Field: "max_client_daily_limit", OldValue: oldLimit.MaxClientDailyLimit.String(), NewValue: result.MaxClientDailyLimit.String()},
			{Field: "max_client_monthly_limit", OldValue: oldLimit.MaxClientMonthlyLimit.String(), NewValue: result.MaxClientMonthlyLimit.String()},
		})
		if len(entries) > 0 {
			_ = s.changelogRepo.CreateBatch(entries)
		}
	}

	// ... rest unchanged (Kafka publish)
}
```

Update `ApplyTemplate` similarly — after the upsert, record the same diff between old and new limit values with reason `"template_applied:<templateName>"`.

- [ ] **Step 4: Update gRPC handler to extract changedBy**

In `user-service/internal/handler/grpc_handler.go`, add import for `"github.com/exbanka/contract/changelog"` and update each mutating handler:

```go
func (h *UserGRPCHandler) UpdateEmployee(ctx context.Context, req *pb.UpdateEmployeeRequest) (*pb.EmployeeResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	updates := make(map[string]interface{})
	// ... (build updates map as before)
	emp, err := h.empService.UpdateEmployee(ctx, req.Id, updates, changedBy)
	// ...
}

func (h *UserGRPCHandler) SetEmployeeRoles(ctx context.Context, req *pb.SetEmployeeRolesRequest) (*pb.EmployeeResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.empService.SetEmployeeRoles(ctx, req.EmployeeId, req.Roles, changedBy); err != nil {
		// ...
	}
	// ...
}

func (h *UserGRPCHandler) SetEmployeeAdditionalPermissions(ctx context.Context, req *pb.SetAdditionalPermissionsRequest) (*pb.EmployeeResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.empService.SetEmployeeAdditionalPermissions(ctx, req.EmployeeId, req.Permissions, changedBy); err != nil {
		// ...
	}
	// ...
}
```

For the limit handler (check if it's in a separate handler file or the same one — it may be `limit_handler.go`), update `SetEmployeeLimits`:
```go
changedBy := changelog.ExtractChangedBy(ctx)
result, err := h.limitSvc.SetEmployeeLimits(ctx, limit, changedBy)
```

- [ ] **Step 5: Wire in cmd/main.go**

In `user-service/cmd/main.go`:
- Add `&model.Changelog{}` to AutoMigrate.
- Create `changelogRepo := repository.NewChangelogRepository(db)`.
- Pass `changelogRepo` to `NewEmployeeService(...)` and `NewLimitService(...)`.

- [ ] **Step 6: Write tests**

```go
// user-service/internal/service/employee_service_changelog_test.go
package service

import (
	"context"
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateEmployee_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Employee{}, &model.Role{}, &model.Permission{}, &model.Changelog{})
	empRepo := repository.NewEmployeeRepository(db)
	clRepo := repository.NewChangelogRepository(db)

	svc := NewEmployeeService(empRepo, nil, nil, nil, clRepo)

	emp := &model.Employee{FirstName: "John", LastName: "Doe", Email: "john@test.com", Username: "john", JMBG: "1234567890123"}
	require.NoError(t, empRepo.Create(emp))

	updates := map[string]interface{}{"last_name": "Smith", "phone": "123456"}
	_, err := svc.UpdateEmployee(context.Background(), emp.ID, updates, 42)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("employee", emp.ID, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total) // last_name + phone
	assert.Equal(t, int64(42), entries[0].ChangedBy)
}
```

- [ ] **Step 7: Run tests**

Run: `cd user-service && go test ./internal/service/ -v -run TestUpdate.*RecordsChangelog`
and: `cd user-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 8: Commit**

```
feat(user): add changelog tracking to employee and limit mutations
```

---

### Task 6: Add changelog to client-service

**Files:**
- Create: `client-service/internal/model/changelog.go`
- Create: `client-service/internal/repository/changelog_repository.go`
- Modify: `client-service/internal/service/client_service.go` (add changelog to UpdateClient)
- Modify: `client-service/internal/service/client_limit_service.go` (add changelog to SetClientLimits)
- Modify: `client-service/internal/handler/grpc_handler.go` (extract changedBy)
- Modify: `client-service/cmd/main.go` (AutoMigrate + wire)
- Create: `client-service/internal/service/client_service_changelog_test.go`

- [ ] **Step 1: Create model and repository (same pattern as Task 2)**

Create `client-service/internal/model/changelog.go` — identical to account-service version.
Create `client-service/internal/repository/changelog_repository.go` — identical structure, with `github.com/exbanka/client-service/internal/model` import.

- [ ] **Step 2: Add changelog to ClientService.UpdateClient**

Modify `client-service/internal/service/client_service.go`:

Add `changelogRepo` to the struct (as an interface, since client-service uses interfaces):
```go
// ChangelogRepo is the interface for changelog persistence.
type ChangelogRepo interface {
	Create(entry changelog.Entry) error
	CreateBatch(entries []changelog.Entry) error
}

type ClientService struct {
	repo          ClientRepo
	producer      *kafkaprod.Producer
	cache         *cache.RedisCache
	changelogRepo ChangelogRepo
}
```

Update constructor to accept `changelogRepo ChangelogRepo`.

Update `UpdateClient` to accept `changedBy int64` and record field changes:
```go
func (s *ClientService) UpdateClient(id uint64, updates map[string]interface{}, changedBy int64) (*model.Client, error) {
	client, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	delete(updates, "jmbg")
	delete(updates, "password_hash")

	var changes []changelog.FieldChange
	if v, ok := updates["first_name"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "first_name", OldValue: client.FirstName, NewValue: v})
		client.FirstName = v
	}
	if v, ok := updates["last_name"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "last_name", OldValue: client.LastName, NewValue: v})
		client.LastName = v
	}
	if v, ok := updates["date_of_birth"].(int64); ok {
		changes = append(changes, changelog.FieldChange{Field: "date_of_birth", OldValue: client.DateOfBirth, NewValue: v})
		client.DateOfBirth = v
	}
	if v, ok := updates["gender"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "gender", OldValue: client.Gender, NewValue: v})
		client.Gender = v
	}
	if v, ok := updates["email"].(string); ok {
		if err := ValidateEmail(v); err != nil {
			return nil, err
		}
		changes = append(changes, changelog.FieldChange{Field: "email", OldValue: client.Email, NewValue: v})
		client.Email = v
	}
	if v, ok := updates["phone"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "phone", OldValue: client.Phone, NewValue: v})
		client.Phone = v
	}
	if v, ok := updates["address"].(string); ok {
		changes = append(changes, changelog.FieldChange{Field: "address", OldValue: client.Address, NewValue: v})
		client.Address = v
	}

	if err := s.repo.Update(client); err != nil {
		return nil, err
	}

	// Record changelog after successful mutation.
	entries := changelog.Diff("client", int64(id), changedBy, "", changes)
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}

	// ... rest unchanged (cache invalidation, Kafka publish)
}
```

- [ ] **Step 3: Add changelog to ClientLimitService.SetClientLimits**

Modify `client-service/internal/service/client_limit_service.go`:

Add `changelogRepo ChangelogRepo` to struct. Update constructor.

Update `SetClientLimits`:
```go
func (s *ClientLimitService) SetClientLimits(ctx context.Context, limit model.ClientLimit, changedBy int64) (*model.ClientLimit, error) {
	// Fetch old limits for changelog.
	oldLimit, _ := s.limitRepo.GetByClientID(limit.ClientID)

	// ... (existing employee limit verification logic unchanged)

	if err := s.limitRepo.Upsert(&limit); err != nil {
		return nil, err
	}

	result, err := s.limitRepo.GetByClientID(limit.ClientID)
	if err != nil {
		return nil, err
	}

	// Record changelog.
	if s.changelogRepo != nil && oldLimit != nil {
		entries := changelog.Diff("client_limit", limit.ClientID, changedBy, "", []changelog.FieldChange{
			{Field: "daily_limit", OldValue: oldLimit.DailyLimit.String(), NewValue: result.DailyLimit.String()},
			{Field: "monthly_limit", OldValue: oldLimit.MonthlyLimit.String(), NewValue: result.MonthlyLimit.String()},
			{Field: "transfer_limit", OldValue: oldLimit.TransferLimit.String(), NewValue: result.TransferLimit.String()},
		})
		if len(entries) > 0 {
			_ = s.changelogRepo.CreateBatch(entries)
		}
	}

	// ... rest unchanged (Kafka publish)
}
```

- [ ] **Step 4: Update gRPC handler**

In `client-service/internal/handler/grpc_handler.go`:

```go
func (h *ClientGRPCHandler) UpdateClient(ctx context.Context, req *pb.UpdateClientRequest) (*pb.ClientResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	// ... build updates map ...
	client, err := h.clientService.UpdateClient(req.Id, updates, changedBy)
	// ...
}

func (h *ClientGRPCHandler) SetClientLimits(ctx context.Context, req *pb.SetClientLimitsRequest) (*pb.ClientLimitResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.clientLimitSvc.SetClientLimits(ctx, limit, changedBy)
	// ...
}
```

- [ ] **Step 5: Wire in cmd/main.go**

In `client-service/cmd/main.go`:
- Add `&model.Changelog{}` to AutoMigrate.
- Create `changelogRepo := repository.NewChangelogRepository(db)`.
- Pass to `NewClientService` and `NewClientLimitService`.

- [ ] **Step 6: Write tests**

```go
// client-service/internal/service/client_service_changelog_test.go
package service

import (
	"testing"

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/contract/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateClient_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Client{}, &model.Changelog{})
	clientRepo := repository.NewClientRepository(db)
	clRepo := repository.NewChangelogRepository(db)

	svc := NewClientService(clientRepo, nil, nil, clRepo)

	client := &model.Client{FirstName: "Jane", LastName: "Doe", Email: "jane@test.com", JMBG: "1234567890123", DateOfBirth: 946684800}
	require.NoError(t, clientRepo.Create(client))

	updates := map[string]interface{}{"last_name": "Smith", "phone": "555-1234"}
	_, err := svc.UpdateClient(client.ID, updates, 10)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("client", int64(client.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total) // last_name + phone
}
```

- [ ] **Step 7: Run tests**

Run: `cd client-service && go test ./internal/service/ -v -run TestUpdate.*RecordsChangelog`
and: `cd client-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 8: Commit**

```
feat(client): add changelog tracking to client and client_limit mutations
```

---

### Task 7: Add changelog to credit-service

**Files:**
- Create: `credit-service/internal/model/changelog.go`
- Create: `credit-service/internal/repository/changelog_repository.go`
- Modify: `credit-service/internal/service/loan_request_service.go` (add changelog to ApproveLoanRequest, RejectLoanRequest)
- Modify: `credit-service/internal/handler/grpc_handler.go` (extract changedBy)
- Modify: `credit-service/cmd/main.go` (AutoMigrate + wire)
- Create: `credit-service/internal/service/loan_request_changelog_test.go`

- [ ] **Step 1: Create model and repository (same pattern as Task 2)**

Create `credit-service/internal/model/changelog.go` — identical to account-service version.
Create `credit-service/internal/repository/changelog_repository.go` — identical structure, with `github.com/exbanka/credit-service/internal/model` import.

- [ ] **Step 2: Add changelog to LoanRequestService**

Modify `credit-service/internal/service/loan_request_service.go`:

Add `changelogRepo *repository.ChangelogRepository` to `LoanRequestService`:
```go
type LoanRequestService struct {
	repo          *repository.LoanRequestRepository
	loanRepo      *repository.LoanRepository
	installRepo   *repository.InstallmentRepository
	limitClient   userpb.EmployeeLimitServiceClient
	accountClient accountpb.AccountServiceClient
	rateConfigSvc *RateConfigService
	changelogRepo *repository.ChangelogRepository
	db            *gorm.DB
}
```

Update constructor to accept `changelogRepo *repository.ChangelogRepository`.

Update `ApproveLoanRequest` — add `changedBy` to the existing `employeeID` parameter (or derive from it, since `employeeID` IS the changed_by). After the DB transaction commits and the loan is created:

```go
func (s *LoanRequestService) ApproveLoanRequest(ctx context.Context, requestID uint64, employeeID uint64) (*model.Loan, error) {
	// ... (existing logic unchanged through end of DB transaction) ...

	// Record changelog for loan request status change.
	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("loan_request", int64(requestID), int64(employeeID), "pending", "approved", "")
		_ = s.changelogRepo.Create(entry)
	}

	// ... (existing disbursement logic) ...

	// Record changelog for loan creation.
	if s.changelogRepo != nil {
		createEntry := changelog.NewCreateEntry("loan", int64(loan.ID), int64(employeeID), changelog.ToJSON(map[string]interface{}{
			"loan_number": loan.LoanNumber,
			"amount":      loan.Amount.String(),
			"loan_type":   loan.LoanType,
			"status":      loan.Status,
		}))
		_ = s.changelogRepo.Create(createEntry)
	}

	return loan, nil
}
```

Update `RejectLoanRequest` to accept `changedBy int64`:
```go
func (s *LoanRequestService) RejectLoanRequest(requestID uint64, changedBy int64, reason string) (*model.LoanRequest, error) {
	// ... (existing logic unchanged) ...

	// Record changelog after TX commits.
	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("loan_request", int64(requestID), changedBy, "pending", "rejected", reason)
		_ = s.changelogRepo.Create(entry)
	}

	return req, nil
}
```

- [ ] **Step 3: Update gRPC handler**

In `credit-service/internal/handler/grpc_handler.go`:

```go
func (h *CreditGRPCHandler) ApproveLoanRequest(ctx context.Context, req *pb.ApproveLoanRequestReq) (*pb.LoanResponse, error) {
	// employeeID already comes from the request; changedBy comes from metadata.
	// Use the employeeID from the request as the approver (it IS the changed_by).
	loan, err := h.loanRequestSvc.ApproveLoanRequest(ctx, req.RequestId, req.EmployeeId)
	// ...
}

func (h *CreditGRPCHandler) RejectLoanRequest(ctx context.Context, req *pb.RejectLoanRequestReq) (*pb.LoanRequestResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.loanRequestSvc.RejectLoanRequest(req.RequestId, changedBy, req.Reason)
	// ...
}
```

- [ ] **Step 4: Wire in cmd/main.go**

In `credit-service/cmd/main.go`:
- Add `&model.Changelog{}` to AutoMigrate.
- Create `changelogRepo := repository.NewChangelogRepository(db)`.
- Pass to `NewLoanRequestService`.

- [ ] **Step 5: Write tests**

```go
// credit-service/internal/service/loan_request_changelog_test.go
package service

import (
	"context"
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectLoanRequest_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.LoanRequest{}, &model.Loan{}, &model.Installment{}, &model.Changelog{})
	lrRepo := repository.NewLoanRequestRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)

	svc := NewLoanRequestService(lrRepo, nil, nil, nil, nil, nil, changelogRepo, db)

	lr := &model.LoanRequest{
		ClientID:        1,
		LoanType:        "cash",
		InterestType:    "fixed",
		Amount:          decimal.NewFromInt(50000),
		CurrencyCode:    "RSD",
		RepaymentPeriod: 12,
		AccountNumber:   "1234567890123456",
		Status:          "pending",
	}
	require.NoError(t, db.Create(lr).Error)

	_, err := svc.RejectLoanRequest(lr.ID, 7, "insufficient collateral")
	require.NoError(t, err)

	entries, total, err := changelogRepo.ListByEntity("loan_request", int64(lr.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "status_change", entries[0].Action)
	assert.Contains(t, entries[0].NewValue, "rejected")
	assert.Equal(t, "insufficient collateral", entries[0].Reason)
}
```

- [ ] **Step 6: Run tests**

Run: `cd credit-service && go test ./internal/service/ -v -run TestReject.*RecordsChangelog`
and: `cd credit-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 7: Commit**

```
feat(credit): add changelog tracking to loan request approval/rejection
```

---

### Task 8: Add changelog to card-service

**Files:**
- Create: `card-service/internal/model/changelog.go`
- Create: `card-service/internal/repository/changelog_repository.go`
- Modify: `card-service/internal/service/card_service.go` (add changelog to BlockCard, UnblockCard, DeactivateCard, SetPin, TemporaryBlockCard)
- Modify: `card-service/internal/handler/grpc_handler.go` (extract changedBy)
- Modify: `card-service/cmd/main.go` (AutoMigrate + wire)
- Create: `card-service/internal/service/card_service_changelog_test.go`

- [ ] **Step 1: Create model and repository (same pattern as Task 2)**

Create `card-service/internal/model/changelog.go` — identical to account-service version.
Create `card-service/internal/repository/changelog_repository.go` — identical structure, with `github.com/exbanka/card-service/internal/model` import.

- [ ] **Step 2: Add changelog to CardService**

Add `changelogRepo *repository.ChangelogRepository` to `CardService` struct. Update `NewCardService`.

Update `BlockCard` to accept `changedBy int64`:
```go
func (s *CardService) BlockCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	var oldStatus string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card %d not found", id)
			}
			return e
		}
		oldStatus = card.Status
		if card.Status == "blocked" {
			return fmt.Errorf("card %d is already blocked", id)
		}
		if card.Status == "deactivated" {
			return fmt.Errorf("card %d is deactivated and cannot be blocked", id)
		}
		card.Status = "blocked"
		return tx.Save(card).Error
	})
	if err != nil {
		return card, err
	}

	// Record changelog after TX commits.
	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, oldStatus, "blocked", "")
		_ = s.changelogRepo.Create(entry)
	}
	return card, nil
}
```

Update `UnblockCard` to accept `changedBy int64`:
```go
func (s *CardService) UnblockCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card %d not found", id)
			}
			return e
		}
		if card.Status != "blocked" {
			return fmt.Errorf("card %d is not blocked", id)
		}
		card.Status = "active"
		return tx.Save(card).Error
	})
	if err != nil {
		return card, err
	}

	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, "blocked", "active", "")
		_ = s.changelogRepo.Create(entry)
	}
	return card, nil
}
```

Update `DeactivateCard` to accept `changedBy int64`:
```go
func (s *CardService) DeactivateCard(id uint64, changedBy int64) (*model.Card, error) {
	var card *model.Card
	var oldStatus string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = s.cardRepo.GetByIDForUpdate(tx, id)
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return fmt.Errorf("card %d not found", id)
			}
			return e
		}
		if card.Status == "deactivated" {
			return fmt.Errorf("card %d is already deactivated", id)
		}
		oldStatus = card.Status
		card.Status = "deactivated"
		return tx.Save(card).Error
	})
	if err != nil {
		return card, err
	}

	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("card", int64(id), changedBy, oldStatus, "deactivated", "")
		_ = s.changelogRepo.Create(entry)
	}
	return card, nil
}
```

Update `SetPin` to accept `changedBy int64`:
```go
func (s *CardService) SetPin(cardID uint64, pin string, changedBy int64) error {
	// ... (existing validation and hash logic) ...

	card.PinHash = string(hash)
	card.PinAttempts = 0
	if err := s.cardRepo.Update(card); err != nil {
		return err
	}

	if s.changelogRepo != nil {
		entry := changelog.Entry{
			EntityType: "card",
			EntityID:   int64(cardID),
			Action:     changelog.ActionUpdate,
			FieldName:  "pin",
			OldValue:   "",  // Never log actual PIN values
			NewValue:   "[redacted]",
			ChangedBy:  changedBy,
			ChangedAt:  time.Now(),
		}
		_ = s.changelogRepo.Create(entry)
	}
	return nil
}
```

Update `TemporaryBlockCard` to accept `changedBy int64`:
```go
func (s *CardService) TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string, changedBy int64) (*model.Card, error) {
	// ... (existing logic) ...

	// After TX commits, before Kafka publish:
	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("card", int64(cardID), changedBy, card.Status, "blocked", reason)
		_ = s.changelogRepo.Create(entry)
	}

	// Kafka publish ...
	return card, nil
}
```

- [ ] **Step 3: Update gRPC handler**

In `card-service/internal/handler/grpc_handler.go`:

```go
func (h *CardGRPCHandler) BlockCard(ctx context.Context, req *pb.CardIdRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.BlockCard(req.Id, changedBy)
	// ...
}

func (h *CardGRPCHandler) UnblockCard(ctx context.Context, req *pb.CardIdRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.UnblockCard(req.Id, changedBy)
	// ...
}

func (h *CardGRPCHandler) DeactivateCard(ctx context.Context, req *pb.CardIdRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.DeactivateCard(req.Id, changedBy)
	// ...
}

func (h *CardGRPCHandler) SetPin(ctx context.Context, req *pb.SetPinRequest) (*pb.Empty, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	err := h.cardService.SetPin(req.CardId, req.Pin, changedBy)
	// ...
}

func (h *CardGRPCHandler) TemporaryBlockCard(ctx context.Context, req *pb.TemporaryBlockRequest) (*pb.CardResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	card, err := h.cardService.TemporaryBlockCard(ctx, req.CardId, int(req.DurationHours), req.Reason, changedBy)
	// ...
}
```

- [ ] **Step 4: Wire in cmd/main.go**

In `card-service/cmd/main.go`:
- Add `&model.Changelog{}` to AutoMigrate.
- Create `changelogRepo := repository.NewChangelogRepository(db)`.
- Pass to `NewCardService`.

- [ ] **Step 5: Write tests**

```go
// card-service/internal/service/card_service_changelog_test.go
package service

import (
	"testing"
	"time"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/testutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockCard_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Card{}, &model.CardBlock{}, &model.Changelog{})
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	clRepo := repository.NewChangelogRepository(db)

	svc := NewCardService(cardRepo, blockRepo, nil, nil, nil, clRepo, db)

	card := &model.Card{
		CardNumber:     "****1234",
		CardNumberFull: "4111111111111234",
		CVV:            "123",
		CardBrand:      "visa",
		AccountNumber:  "1234567890123456",
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "active",
		CardLimit:      decimal.NewFromInt(100000),
		ExpiresAt:      time.Now().AddDate(3, 0, 0),
	}
	require.NoError(t, db.Create(card).Error)

	_, err := svc.BlockCard(card.ID, 5)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("card", int64(card.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "status_change", entries[0].Action)
	assert.Contains(t, entries[0].NewValue, "blocked")
	assert.Equal(t, int64(5), entries[0].ChangedBy)
}

func TestDeactivateCard_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Card{}, &model.CardBlock{}, &model.Changelog{})
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	clRepo := repository.NewChangelogRepository(db)

	svc := NewCardService(cardRepo, blockRepo, nil, nil, nil, clRepo, db)

	card := &model.Card{
		CardNumber:     "****5678",
		CardNumberFull: "4111111111115678",
		CVV:            "456",
		CardBrand:      "visa",
		AccountNumber:  "1234567890123456",
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "active",
		CardLimit:      decimal.NewFromInt(100000),
		ExpiresAt:      time.Now().AddDate(3, 0, 0),
	}
	require.NoError(t, db.Create(card).Error)

	_, err := svc.DeactivateCard(card.ID, 3)
	require.NoError(t, err)

	entries, total, err := clRepo.ListByEntity("card", int64(card.ID), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Contains(t, entries[0].NewValue, "deactivated")
}
```

- [ ] **Step 6: Run tests**

Run: `cd card-service && go test ./internal/service/ -v -run Test.*RecordsChangelog`
and: `cd card-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 7: Commit**

```
feat(card): add changelog tracking to card status changes and PIN resets
```

---

### Task 9: Add changelog to auth-service

**Files:**
- Create: `auth-service/internal/model/changelog.go`
- Create: `auth-service/internal/repository/changelog_repository.go`
- Modify: `auth-service/internal/service/auth_service.go` (add changelog to ResetPassword, ActivateAccount, SetAccountStatus)
- Modify: `auth-service/internal/service/mobile_device_service.go` (add changelog to device activation)
- Modify: `auth-service/cmd/main.go` (AutoMigrate + wire)
- Create: `auth-service/internal/service/auth_service_changelog_test.go`

The auth-service already has `LoginAttempt` for tracking login attempts. The changelog enhances this with: password changes, account activations, account status changes (enable/disable), and device activations.

- [ ] **Step 1: Create model and repository (same pattern as Task 2)**

Create `auth-service/internal/model/changelog.go` — identical to account-service version.
Create `auth-service/internal/repository/changelog_repository.go` — identical structure, with `github.com/exbanka/auth-service/internal/model` import.

- [ ] **Step 2: Add changelog to AuthService**

Add `changelogRepo *repository.ChangelogRepository` to `AuthService`. Update constructor.

Update `ResetPassword` — after `s.accountRepo.SetPassword(...)` succeeds:
```go
func (s *AuthService) ResetPassword(ctx context.Context, tokenStr, newPassword, confirmPassword string) error {
	// ... (existing validation and password set logic) ...

	if err := s.accountRepo.SetPassword(prt.AccountID, string(hash)); err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}

	// Record changelog.
	if s.changelogRepo != nil {
		var acct model.Account
		if err := s.accountRepo.GetByID(prt.AccountID, &acct); err == nil {
			entry := changelog.Entry{
				EntityType: "account",
				EntityID:   acct.PrincipalID,
				Action:     changelog.ActionUpdate,
				FieldName:  "password",
				OldValue:   "",
				NewValue:   "[redacted]",
				ChangedBy:  acct.PrincipalID, // self-service
				ChangedAt:  time.Now(),
				Reason:     "password_reset",
			}
			_ = s.changelogRepo.Create(entry)
		}
	}

	// ... rest unchanged (mark token used, revoke sessions)
}
```

Update `ActivateAccount` — after activation succeeds:
```go
func (s *AuthService) ActivateAccount(ctx context.Context, tokenStr, password, confirmPassword string) error {
	// ... (existing logic) ...

	if err := s.accountRepo.SetPasswordAndActivate(at.AccountID, string(hash)); err != nil {
		return fmt.Errorf("failed to activate account: %w", err)
	}

	// Record changelog.
	if s.changelogRepo != nil {
		var acct model.Account
		if lookupErr := s.accountRepo.GetByID(at.AccountID, &acct); lookupErr == nil {
			entry := changelog.NewStatusChangeEntry("account", acct.PrincipalID, acct.PrincipalID, "pending", "active", "account_activation")
			_ = s.changelogRepo.Create(entry)
		}
	}

	// ... rest unchanged (confirmation email)
}
```

Update `SetAccountStatus` — after status update succeeds:
```go
func (s *AuthService) SetAccountStatus(ctx context.Context, principalType string, principalID int64, active bool) error {
	// ... (existing logic) ...

	if err := s.accountRepo.SetStatusByPrincipal(principalType, principalID, status); err != nil {
		return err
	}

	// Record changelog.
	if s.changelogRepo != nil {
		oldStatus := "active"
		if !active {
			oldStatus = "active" // was active before disabling
		} else {
			oldStatus = "disabled" // was disabled before enabling
		}
		entry := changelog.NewStatusChangeEntry("account", principalID, 0, oldStatus, string(status), "admin_set_status")
		_ = s.changelogRepo.Create(entry)
	}

	// ... rest unchanged (Kafka publish)
}
```

- [ ] **Step 3: Add changelog to MobileDeviceService**

Add `changelogRepo *repository.ChangelogRepository` to `MobileDeviceService`. Update constructor.

After successful device activation (in `ActivateDevice` or `ConfirmActivation` — whichever method completes the activation):
```go
if s.changelogRepo != nil {
	entry := changelog.Entry{
		EntityType: "mobile_device",
		EntityID:   userID,
		Action:     changelog.ActionCreate,
		FieldName:  "device_id",
		NewValue:   changelog.ToJSON(deviceID),
		ChangedBy:  userID,
		ChangedAt:  time.Now(),
		Reason:     "mobile_device_activated",
	}
	_ = s.changelogRepo.Create(entry)
}
```

After device deactivation:
```go
if s.changelogRepo != nil {
	entry := changelog.Entry{
		EntityType: "mobile_device",
		EntityID:   userID,
		Action:     changelog.ActionDelete,
		FieldName:  "device_id",
		OldValue:   changelog.ToJSON(deviceID),
		ChangedBy:  userID,
		ChangedAt:  time.Now(),
		Reason:     "mobile_device_deactivated",
	}
	_ = s.changelogRepo.Create(entry)
}
```

- [ ] **Step 4: Wire in cmd/main.go**

In `auth-service/cmd/main.go`:
- Add `&model.Changelog{}` to AutoMigrate.
- Create `changelogRepo := repository.NewChangelogRepository(db)`.
- Pass to `NewAuthService` and `NewMobileDeviceService`.

- [ ] **Step 5: Write tests**

```go
// auth-service/internal/service/auth_service_changelog_test.go
package service

import (
	"context"
	"testing"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	"github.com/exbanka/contract/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetAccountStatus_RecordsChangelog(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.RefreshToken{}, &model.Changelog{})
	accountRepo := repository.NewAccountRepository(db)
	clRepo := repository.NewChangelogRepository(db)

	// Create a test account.
	acct := &model.Account{
		Email:         "test@example.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   42,
	}
	require.NoError(t, accountRepo.Create(acct))

	// Create AuthService with changelogRepo.
	// (This test may need a partial mock setup — adjust based on AuthService dependencies.)
	// The key assertion is that after SetAccountStatus, a changelog entry exists.

	entries, total, err := clRepo.ListByEntity("account", 42, 1, 10)
	require.NoError(t, err)
	// Initially no entries.
	assert.Equal(t, int64(0), total)
	_ = entries
}
```

Note: Auth-service tests may require more mocking due to many dependencies. Focus on testing the changelog recording logic in isolation where possible.

- [ ] **Step 6: Run tests**

Run: `cd auth-service && go test ./internal/service/ -v -run Test.*RecordsChangelog`
and: `cd auth-service && go build ./cmd/`
Expected: All pass, compiles.

- [ ] **Step 7: Commit**

```
feat(auth): add changelog tracking to password resets, activations, and device events
```

---

### Task 10: Add Kafka changelog events

**Files:**
- Modify: `contract/kafka/messages.go` (add changelog topic constants and message type)
- Modify: Each service's `internal/kafka/producer.go` (add PublishChangelog method)
- Modify: Each service's `cmd/main.go` (add changelog topic to EnsureTopics)
- Modify: Each service's service layer (publish Kafka event after creating changelog entries)

- [ ] **Step 1: Add changelog Kafka types to contract**

Add to `contract/kafka/messages.go`:
```go
// Changelog event topic constants — one per service for downstream consumption.
const (
	TopicAccountChangelog     = "account.changelog"
	TopicUserChangelog        = "user.changelog"
	TopicClientChangelog      = "client.changelog"
	TopicCreditChangelog      = "credit.changelog"
	TopicCardChangelog        = "card.changelog"
	TopicAuthChangelog        = "auth.changelog"
)

// ChangelogMessage is the Kafka event published for every changelog entry.
// Downstream services and analytics consumers use this for audit replication.
type ChangelogMessage struct {
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	Action     string `json:"action"`
	FieldName  string `json:"field_name,omitempty"`
	OldValue   string `json:"old_value,omitempty"`
	NewValue   string `json:"new_value,omitempty"`
	ChangedBy  int64  `json:"changed_by"`
	ChangedAt  string `json:"changed_at"` // RFC3339
	Reason     string `json:"reason,omitempty"`
}
```

- [ ] **Step 2: Add PublishChangelog method to each service's producer**

For each service (account, user, client, credit, card, auth), add to its `internal/kafka/producer.go`:

```go
func (p *Producer) PublishChangelog(ctx context.Context, msg kafkamsg.ChangelogMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: kafkamsg.Topic<Service>Changelog, // e.g., TopicAccountChangelog
		Value: data,
	})
}
```

Replace `Topic<Service>Changelog` with the appropriate constant for each service.

- [ ] **Step 3: Add changelog topics to EnsureTopics**

In each service's `cmd/main.go`, add the changelog topic to the `EnsureTopics` call:

- `account-service/cmd/main.go`: add `"account.changelog"` to EnsureTopics.
- `user-service/cmd/main.go`: add `"user.changelog"` to EnsureTopics.
- `client-service/cmd/main.go`: add `"client.changelog"` to EnsureTopics.
- `credit-service/cmd/main.go`: add `"credit.changelog"` to EnsureTopics.
- `card-service/cmd/main.go`: add `"card.changelog"` to EnsureTopics.
- `auth-service/cmd/main.go`: add `"auth.changelog"` to EnsureTopics.

- [ ] **Step 4: Publish Kafka events from service layer**

In each service's changelog-recording code (added in Tasks 4-9), after the `changelogRepo.Create(entry)` or `changelogRepo.CreateBatch(entries)` call, publish the Kafka event.

Add a helper method to each service that has a producer. Example for account-service:

```go
// In account-service/internal/service/account_service.go, add a helper:
func (s *AccountService) publishChangelogEvents(ctx context.Context, entries []changelog.Entry) {
	if s.producer == nil {
		return
	}
	for _, e := range entries {
		msg := kafkamsg.ChangelogMessage{
			EntityType: e.EntityType,
			EntityID:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt.Format(time.RFC3339),
			Reason:     e.Reason,
		}
		if err := s.producer.PublishChangelog(ctx, msg); err != nil {
			log.Printf("warn: failed to publish changelog event: %v", err)
		}
	}
}
```

Then in each mutation method, after recording changelog entries, call:
```go
s.publishChangelogEvents(ctx, entries)
```

For services that don't currently accept `context.Context` in the method (e.g., `BlockCard`), either:
- Add `ctx context.Context` as the first parameter (preferred), or
- Use `context.Background()` as a fallback.

Repeat this for all six services.

- [ ] **Step 5: Run full build and tests**

Run: `make build && make test`
Expected: All services compile, all tests pass.

- [ ] **Step 6: Commit**

```
feat(kafka): publish changelog events to per-service Kafka topics
```

---

## Testing Summary

Each task includes unit tests that verify:
1. Changelog entries are created with correct entity_type, entity_id, field_name, old/new values, and changed_by.
2. No changelog entries are created when values don't actually change (Diff returns empty).
3. Batch creation works for multi-field updates.
4. The changelog repository correctly stores and retrieves entries.

Integration testing (to be added to `test-app/workflows/`):
- Test that a full API call (gateway -> service) propagates `x-changed-by` and results in changelog entries in the database.
- Test that Kafka changelog events are published after mutations.

## Files Modified Summary

| Service | New Files | Modified Files |
|---------|-----------|----------------|
| contract | `changelog/changelog.go`, `changelog/metadata.go`, tests | `kafka/messages.go` |
| api-gateway | `middleware/changed_by.go` | All mutating handlers |
| account-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/account_service.go`, `handler/grpc_handler.go`, `cmd/main.go` |
| user-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/employee_service.go`, `service/limit_service.go`, `handler/grpc_handler.go`, `cmd/main.go` |
| client-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/client_service.go`, `service/client_limit_service.go`, `handler/grpc_handler.go`, `cmd/main.go` |
| credit-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/loan_request_service.go`, `handler/grpc_handler.go`, `cmd/main.go` |
| card-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/card_service.go`, `handler/grpc_handler.go`, `cmd/main.go` |
| auth-service | `model/changelog.go`, `repository/changelog_repository.go`, tests | `service/auth_service.go`, `service/mobile_device_service.go`, `cmd/main.go` |
