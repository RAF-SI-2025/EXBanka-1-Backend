# Plan 9: Limit Blueprints

**Date:** 2026-04-04
**Spec:** `docs/superpowers/specs/2026-04-04-blueprints-biometrics-hierarchy-design.md` Section 1
**Scope:** Unified LimitBlueprint model, gRPC service, API gateway v1 endpoints, Kafka events, seed migration

---

## Overview

Generalize the existing employee `LimitTemplate` system to also cover actuary limits and client limits via a unified `LimitBlueprint` model in user-service. The old `/api/limits/templates` routes stay frozen.

**New model:** `LimitBlueprint` with a `Type` discriminator (`employee`, `actuary`, `client`) and a JSONB `Values` column for type-specific limit fields.

**New endpoints (v1 only, `limits.manage` permission):**
```
GET    /api/v1/blueprints?type=employee|actuary|client
POST   /api/v1/blueprints
GET    /api/v1/blueprints/:id
PUT    /api/v1/blueprints/:id
DELETE /api/v1/blueprints/:id
POST   /api/v1/blueprints/:id/apply   body: {"target_id": 123}
```

---

## Task 1: LimitBlueprint Model + Repository

### Files

| Action | Path |
|--------|------|
| Create | `user-service/internal/model/limit_blueprint.go` |
| Create | `user-service/internal/repository/limit_blueprint_repository.go` |
| Edit   | `user-service/cmd/main.go` (add to AutoMigrate) |
| Edit   | `user-service/internal/service/interfaces.go` (add interface) |
| Edit   | `user-service/go.mod` (add `gorm.io/datatypes`) |

### Steps

- [ ] **1.1** Add `gorm.io/datatypes` dependency to user-service:

```bash
cd user-service && go get gorm.io/datatypes@v1.2.7
```

- [ ] **1.2** Create `user-service/internal/model/limit_blueprint.go`:

```go
package model

import (
	"time"

	"gorm.io/datatypes"
)

// BlueprintType constants for the Type discriminator.
const (
	BlueprintTypeEmployee = "employee"
	BlueprintTypeActuary  = "actuary"
	BlueprintTypeClient   = "client"
)

// ValidBlueprintTypes is the set of allowed Type values.
var ValidBlueprintTypes = map[string]bool{
	BlueprintTypeEmployee: true,
	BlueprintTypeActuary:  true,
	BlueprintTypeClient:   true,
}

// LimitBlueprint is a named, reusable set of limit values that can be applied
// to employees, actuaries, or clients. The Values column stores type-specific
// JSON (employee limit fields, actuary limit fields, or client limit fields).
type LimitBlueprint struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string         `gorm:"size:100;not null;uniqueIndex:idx_blueprint_name_type" json:"name"`
	Description string         `gorm:"size:512" json:"description"`
	Type        string         `gorm:"size:20;not null;uniqueIndex:idx_blueprint_name_type" json:"type"`
	Values      datatypes.JSON `gorm:"type:jsonb;not null" json:"values"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// EmployeeBlueprintValues is the JSON schema for employee blueprint values.
type EmployeeBlueprintValues struct {
	MaxLoanApprovalAmount string `json:"max_loan_approval_amount"`
	MaxSingleTransaction  string `json:"max_single_transaction"`
	MaxDailyTransaction   string `json:"max_daily_transaction"`
	MaxClientDailyLimit   string `json:"max_client_daily_limit"`
	MaxClientMonthlyLimit string `json:"max_client_monthly_limit"`
}

// ActuaryBlueprintValues is the JSON schema for actuary blueprint values.
type ActuaryBlueprintValues struct {
	Limit        string `json:"limit"`
	NeedApproval bool   `json:"need_approval"`
}

// ClientBlueprintValues is the JSON schema for client blueprint values.
type ClientBlueprintValues struct {
	DailyLimit    string `json:"daily_limit"`
	MonthlyLimit  string `json:"monthly_limit"`
	TransferLimit string `json:"transfer_limit"`
}
```

- [ ] **1.3** Create `user-service/internal/repository/limit_blueprint_repository.go`:

```go
package repository

import (
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type LimitBlueprintRepository struct {
	db *gorm.DB
}

func NewLimitBlueprintRepository(db *gorm.DB) *LimitBlueprintRepository {
	return &LimitBlueprintRepository{db: db}
}

func (r *LimitBlueprintRepository) Create(bp *model.LimitBlueprint) error {
	return r.db.Create(bp).Error
}

func (r *LimitBlueprintRepository) GetByID(id uint64) (*model.LimitBlueprint, error) {
	var bp model.LimitBlueprint
	err := r.db.First(&bp, id).Error
	return &bp, err
}

func (r *LimitBlueprintRepository) List(bpType string) ([]model.LimitBlueprint, error) {
	var blueprints []model.LimitBlueprint
	q := r.db
	if bpType != "" {
		q = q.Where("type = ?", bpType)
	}
	err := q.Order("name ASC").Find(&blueprints).Error
	return blueprints, err
}

func (r *LimitBlueprintRepository) GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error) {
	var bp model.LimitBlueprint
	err := r.db.Where("name = ? AND type = ?", name, bpType).First(&bp).Error
	return &bp, err
}

func (r *LimitBlueprintRepository) Update(bp *model.LimitBlueprint) error {
	return r.db.Save(bp).Error
}

func (r *LimitBlueprintRepository) Delete(id uint64) error {
	return r.db.Delete(&model.LimitBlueprint{}, id).Error
}
```

- [ ] **1.4** Add `LimitBlueprintRepo` interface to `user-service/internal/service/interfaces.go`:

```go
type LimitBlueprintRepo interface {
	Create(bp *model.LimitBlueprint) error
	GetByID(id uint64) (*model.LimitBlueprint, error)
	List(bpType string) ([]model.LimitBlueprint, error)
	GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error)
	Update(bp *model.LimitBlueprint) error
	Delete(id uint64) error
}
```

- [ ] **1.5** Also add `Upsert` to the `ActuaryRepo` interface in `interfaces.go` (the concrete repo already has it, but the interface is missing it --- needed for BlueprintService to call it):

```go
// Add to existing ActuaryRepo interface:
Upsert(limit *model.ActuaryLimit) error
```

- [ ] **1.6** Add `&model.LimitBlueprint{}` to the `AutoMigrate` call in `user-service/cmd/main.go`:

Find:
```go
	if err := db.AutoMigrate(
		&model.Permission{},
		&model.Role{},
		&model.Employee{},
		&model.EmployeeLimit{},
		&model.LimitTemplate{},
		&model.ActuaryLimit{},
		&model.Changelog{},
	); err != nil {
```

Replace with:
```go
	if err := db.AutoMigrate(
		&model.Permission{},
		&model.Role{},
		&model.Employee{},
		&model.EmployeeLimit{},
		&model.LimitTemplate{},
		&model.ActuaryLimit{},
		&model.LimitBlueprint{},
		&model.Changelog{},
	); err != nil {
```

### Test Command

```bash
cd user-service && go build ./...
```

### Commit

```
feat(user-service): add LimitBlueprint model and repository
```

---

## Task 2: BlueprintService

### Files

| Action | Path |
|--------|------|
| Create | `user-service/internal/service/blueprint_service.go` |
| Create | `user-service/internal/service/blueprint_service_test.go` |
| Edit   | `user-service/internal/service/interfaces.go` (add ClientLimitClient interface) |

### Dependencies

user-service does NOT currently have a gRPC client for client-service. For the "client" type apply, we need a gRPC client interface. We define an interface locally rather than importing `clientpb` directly (keeps the dependency graph clean --- the service layer uses the interface, the handler/main wires the real client).

### Steps

- [ ] **2.1** Add `ClientLimitClient` interface to `user-service/internal/service/interfaces.go`:

```go
// ClientLimitClient is the subset of clientpb.ClientLimitServiceClient that
// BlueprintService needs. Defined as an interface to avoid tight coupling to
// the generated gRPC client and to enable unit testing with mocks.
type ClientLimitClient interface {
	SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error
}
```

Note: The real implementation (Task 3) will wrap `clientpb.ClientLimitServiceClient` and translate the call. This interface is intentionally simplified for the service layer.

- [ ] **2.2** Create `user-service/internal/service/blueprint_service.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/exbanka/contract/changelog"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
)

// BlueprintService manages limit blueprints and applies them to targets.
type BlueprintService struct {
	blueprintRepo LimitBlueprintRepo
	limitRepo     EmployeeLimitRepo
	actuaryRepo   ActuaryRepo
	clientClient  ClientLimitClient
	producer      *kafkaprod.Producer
	changelogRepo ChangelogRepo
}

func NewBlueprintService(
	blueprintRepo LimitBlueprintRepo,
	limitRepo EmployeeLimitRepo,
	actuaryRepo ActuaryRepo,
	clientClient ClientLimitClient,
	producer *kafkaprod.Producer,
	changelogRepo ChangelogRepo,
) *BlueprintService {
	return &BlueprintService{
		blueprintRepo: blueprintRepo,
		limitRepo:     limitRepo,
		actuaryRepo:   actuaryRepo,
		clientClient:  clientClient,
		producer:      producer,
		changelogRepo: changelogRepo,
	}
}

// CreateBlueprint validates and persists a new blueprint.
func (s *BlueprintService) CreateBlueprint(ctx context.Context, bp model.LimitBlueprint) (*model.LimitBlueprint, error) {
	if bp.Name == "" {
		return nil, errors.New("name is required")
	}
	if !model.ValidBlueprintTypes[bp.Type] {
		return nil, fmt.Errorf("invalid blueprint type: must be one of employee, actuary, client")
	}
	if err := s.validateValues(bp.Type, bp.Values); err != nil {
		return nil, fmt.Errorf("invalid values: %w", err)
	}
	if err := s.blueprintRepo.Create(&bp); err != nil {
		return nil, err
	}
	s.publishEvent(ctx, kafkamsg.BlueprintMessage{
		BlueprintID:   bp.ID,
		BlueprintName: bp.Name,
		BlueprintType: bp.Type,
		Action:        "created",
	})
	return &bp, nil
}

// GetBlueprint returns a single blueprint by ID.
func (s *BlueprintService) GetBlueprint(id uint64) (*model.LimitBlueprint, error) {
	return s.blueprintRepo.GetByID(id)
}

// ListBlueprints returns all blueprints, optionally filtered by type.
func (s *BlueprintService) ListBlueprints(bpType string) ([]model.LimitBlueprint, error) {
	if bpType != "" && !model.ValidBlueprintTypes[bpType] {
		return nil, fmt.Errorf("invalid blueprint type filter: must be one of employee, actuary, client")
	}
	return s.blueprintRepo.List(bpType)
}

// UpdateBlueprint updates an existing blueprint.
func (s *BlueprintService) UpdateBlueprint(ctx context.Context, id uint64, name, description string, values json.RawMessage) (*model.LimitBlueprint, error) {
	existing, err := s.blueprintRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		existing.Name = name
	}
	existing.Description = description
	if len(values) > 0 {
		if err := s.validateValues(existing.Type, values); err != nil {
			return nil, fmt.Errorf("invalid values: %w", err)
		}
		existing.Values = values
	}
	if err := s.blueprintRepo.Update(existing); err != nil {
		return nil, err
	}
	s.publishEvent(ctx, kafkamsg.BlueprintMessage{
		BlueprintID:   existing.ID,
		BlueprintName: existing.Name,
		BlueprintType: existing.Type,
		Action:        "updated",
	})
	return existing, nil
}

// DeleteBlueprint removes a blueprint.
func (s *BlueprintService) DeleteBlueprint(ctx context.Context, id uint64) error {
	existing, err := s.blueprintRepo.GetByID(id)
	if err != nil {
		return err
	}
	if err := s.blueprintRepo.Delete(id); err != nil {
		return err
	}
	s.publishEvent(ctx, kafkamsg.BlueprintMessage{
		BlueprintID:   id,
		BlueprintName: existing.Name,
		BlueprintType: existing.Type,
		Action:        "deleted",
	})
	return nil
}

// ApplyBlueprint applies a blueprint's values to the target entity.
// targetID is interpreted based on blueprint type:
//   - employee: employee ID -> EmployeeLimit
//   - actuary:  employee ID -> ActuaryLimit
//   - client:   client ID   -> ClientLimit (via client-service gRPC)
func (s *BlueprintService) ApplyBlueprint(ctx context.Context, blueprintID uint64, targetID int64, appliedBy int64) error {
	bp, err := s.blueprintRepo.GetByID(blueprintID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("blueprint not found")
		}
		return err
	}

	switch bp.Type {
	case model.BlueprintTypeEmployee:
		return s.applyEmployeeBlueprint(ctx, bp, targetID, appliedBy)
	case model.BlueprintTypeActuary:
		return s.applyActuaryBlueprint(ctx, bp, targetID, appliedBy)
	case model.BlueprintTypeClient:
		return s.applyClientBlueprint(ctx, bp, targetID, appliedBy)
	default:
		return fmt.Errorf("unknown blueprint type: %s", bp.Type)
	}
}

func (s *BlueprintService) applyEmployeeBlueprint(ctx context.Context, bp *model.LimitBlueprint, employeeID int64, appliedBy int64) error {
	var vals model.EmployeeBlueprintValues
	if err := json.Unmarshal(bp.Values, &vals); err != nil {
		return fmt.Errorf("failed to parse employee blueprint values: %w", err)
	}

	maxLoan, _ := decimal.NewFromString(vals.MaxLoanApprovalAmount)
	maxSingle, _ := decimal.NewFromString(vals.MaxSingleTransaction)
	maxDaily, _ := decimal.NewFromString(vals.MaxDailyTransaction)
	maxClientDaily, _ := decimal.NewFromString(vals.MaxClientDailyLimit)
	maxClientMonthly, _ := decimal.NewFromString(vals.MaxClientMonthlyLimit)

	limit := &model.EmployeeLimit{
		EmployeeID:            employeeID,
		MaxLoanApprovalAmount: maxLoan,
		MaxSingleTransaction:  maxSingle,
		MaxDailyTransaction:   maxDaily,
		MaxClientDailyLimit:   maxClientDaily,
		MaxClientMonthlyLimit: maxClientMonthly,
	}
	if err := s.limitRepo.Upsert(limit); err != nil {
		return err
	}

	s.recordChangelog(appliedBy, "employee_limit", employeeID, bp.Name)
	s.publishAppliedEvent(ctx, bp, targetID(employeeID))
	return nil
}

func (s *BlueprintService) applyActuaryBlueprint(ctx context.Context, bp *model.LimitBlueprint, employeeID int64, appliedBy int64) error {
	var vals model.ActuaryBlueprintValues
	if err := json.Unmarshal(bp.Values, &vals); err != nil {
		return fmt.Errorf("failed to parse actuary blueprint values: %w", err)
	}

	limitVal, _ := decimal.NewFromString(vals.Limit)

	actuaryLimit := &model.ActuaryLimit{
		EmployeeID:   employeeID,
		Limit:        limitVal,
		NeedApproval: vals.NeedApproval,
	}
	if err := s.actuaryRepo.Upsert(actuaryLimit); err != nil {
		return err
	}

	s.recordChangelog(appliedBy, "actuary_limit", employeeID, bp.Name)
	s.publishAppliedEvent(ctx, bp, targetID(employeeID))
	return nil
}

func (s *BlueprintService) applyClientBlueprint(ctx context.Context, bp *model.LimitBlueprint, clientID int64, appliedBy int64) error {
	var vals model.ClientBlueprintValues
	if err := json.Unmarshal(bp.Values, &vals); err != nil {
		return fmt.Errorf("failed to parse client blueprint values: %w", err)
	}

	if s.clientClient == nil {
		return errors.New("client-service gRPC client not configured")
	}
	if err := s.clientClient.SetClientLimits(ctx, clientID, vals.DailyLimit, vals.MonthlyLimit, vals.TransferLimit, appliedBy); err != nil {
		return fmt.Errorf("failed to apply client limits: %w", err)
	}

	s.recordChangelog(appliedBy, "client_limit", clientID, bp.Name)
	s.publishAppliedEvent(ctx, bp, targetID(clientID))
	return nil
}

// targetID is a helper type alias for readability in publishAppliedEvent.
type targetID int64

func (s *BlueprintService) publishAppliedEvent(ctx context.Context, bp *model.LimitBlueprint, tid targetID) {
	s.publishEvent(ctx, kafkamsg.BlueprintMessage{
		BlueprintID:   bp.ID,
		BlueprintName: bp.Name,
		BlueprintType: bp.Type,
		TargetID:      int64(tid),
		Action:        "applied",
	})
}

func (s *BlueprintService) publishEvent(ctx context.Context, msg kafkamsg.BlueprintMessage) {
	if s.producer == nil {
		return
	}
	if err := s.producer.PublishBlueprint(ctx, msg); err != nil {
		log.Printf("warn: failed to publish blueprint event: %v", err)
	}
}

func (s *BlueprintService) recordChangelog(changedBy int64, entityType string, entityID int64, blueprintName string) {
	if s.changelogRepo == nil {
		return
	}
	entries := changelog.Diff(entityType, entityID, changedBy, "", []changelog.FieldChange{
		{Field: "blueprint_applied", OldValue: "", NewValue: blueprintName},
	})
	if len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}
}

// validateValues checks that the JSON payload is valid for the given blueprint type.
// All decimal string fields must parse as valid decimals.
func (s *BlueprintService) validateValues(bpType string, data json.RawMessage) error {
	switch bpType {
	case model.BlueprintTypeEmployee:
		var v model.EmployeeBlueprintValues
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("invalid employee values JSON: %w", err)
		}
		for field, val := range map[string]string{
			"max_loan_approval_amount": v.MaxLoanApprovalAmount,
			"max_single_transaction":   v.MaxSingleTransaction,
			"max_daily_transaction":    v.MaxDailyTransaction,
			"max_client_daily_limit":   v.MaxClientDailyLimit,
			"max_client_monthly_limit": v.MaxClientMonthlyLimit,
		} {
			if val == "" {
				return fmt.Errorf("%s is required", field)
			}
			if _, err := decimal.NewFromString(val); err != nil {
				return fmt.Errorf("%s must be a valid decimal: %w", field, err)
			}
		}

	case model.BlueprintTypeActuary:
		var v model.ActuaryBlueprintValues
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("invalid actuary values JSON: %w", err)
		}
		if v.Limit == "" {
			return fmt.Errorf("limit is required")
		}
		if _, err := decimal.NewFromString(v.Limit); err != nil {
			return fmt.Errorf("limit must be a valid decimal: %w", err)
		}

	case model.BlueprintTypeClient:
		var v model.ClientBlueprintValues
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("invalid client values JSON: %w", err)
		}
		for field, val := range map[string]string{
			"daily_limit":    v.DailyLimit,
			"monthly_limit":  v.MonthlyLimit,
			"transfer_limit": v.TransferLimit,
		} {
			if val == "" {
				return fmt.Errorf("%s is required", field)
			}
			if _, err := decimal.NewFromString(val); err != nil {
				return fmt.Errorf("%s must be a valid decimal: %w", field, err)
			}
		}

	default:
		return fmt.Errorf("unknown type: %s", bpType)
	}
	return nil
}

// SeedFromTemplates converts existing LimitTemplate records into LimitBlueprint
// records with type "employee". Idempotent: skips if a blueprint with the same
// name and type already exists.
func (s *BlueprintService) SeedFromTemplates(templates []model.LimitTemplate) error {
	for _, tmpl := range templates {
		_, err := s.blueprintRepo.GetByNameAndType(tmpl.Name, model.BlueprintTypeEmployee)
		if err == nil {
			continue // already exists
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		vals := model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: tmpl.MaxLoanApprovalAmount.String(),
			MaxSingleTransaction:  tmpl.MaxSingleTransaction.String(),
			MaxDailyTransaction:   tmpl.MaxDailyTransaction.String(),
			MaxClientDailyLimit:   tmpl.MaxClientDailyLimit.String(),
			MaxClientMonthlyLimit: tmpl.MaxClientMonthlyLimit.String(),
		}
		data, err := json.Marshal(vals)
		if err != nil {
			return err
		}

		bp := model.LimitBlueprint{
			Name:        tmpl.Name,
			Description: tmpl.Description,
			Type:        model.BlueprintTypeEmployee,
			Values:      data,
		}
		if err := s.blueprintRepo.Create(&bp); err != nil {
			return err
		}
		log.Printf("seeded blueprint from template: %s (type=employee)", tmpl.Name)
	}
	return nil
}
```

- [ ] **2.3** Create `user-service/internal/service/blueprint_service_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- Mock LimitBlueprintRepo ---

type mockBlueprintRepo struct {
	blueprints map[uint64]*model.LimitBlueprint
	byNameType map[string]*model.LimitBlueprint // key: "name|type"
	nextID     uint64
}

func newMockBlueprintRepo() *mockBlueprintRepo {
	return &mockBlueprintRepo{
		blueprints: make(map[uint64]*model.LimitBlueprint),
		byNameType: make(map[string]*model.LimitBlueprint),
		nextID:     1,
	}
}

func (m *mockBlueprintRepo) Create(bp *model.LimitBlueprint) error {
	key := bp.Name + "|" + bp.Type
	if _, exists := m.byNameType[key]; exists {
		return gorm.ErrDuplicatedKey
	}
	bp.ID = m.nextID
	m.nextID++
	copy := *bp
	m.blueprints[copy.ID] = &copy
	m.byNameType[key] = m.blueprints[copy.ID]
	return nil
}

func (m *mockBlueprintRepo) GetByID(id uint64) (*model.LimitBlueprint, error) {
	if bp, ok := m.blueprints[id]; ok {
		return bp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockBlueprintRepo) List(bpType string) ([]model.LimitBlueprint, error) {
	var result []model.LimitBlueprint
	for _, bp := range m.blueprints {
		if bpType == "" || bp.Type == bpType {
			result = append(result, *bp)
		}
	}
	return result, nil
}

func (m *mockBlueprintRepo) GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error) {
	key := name + "|" + bpType
	if bp, ok := m.byNameType[key]; ok {
		return bp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockBlueprintRepo) Update(bp *model.LimitBlueprint) error {
	if _, ok := m.blueprints[bp.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	copy := *bp
	m.blueprints[copy.ID] = &copy
	key := copy.Name + "|" + copy.Type
	m.byNameType[key] = m.blueprints[copy.ID]
	return nil
}

func (m *mockBlueprintRepo) Delete(id uint64) error {
	bp, ok := m.blueprints[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	key := bp.Name + "|" + bp.Type
	delete(m.blueprints, id)
	delete(m.byNameType, key)
	return nil
}

// --- Mock ClientLimitClient ---

type mockClientLimitClient struct {
	calls []clientLimitCall
	err   error
}

type clientLimitCall struct {
	ClientID      int64
	DailyLimit    string
	MonthlyLimit  string
	TransferLimit string
	SetByEmployee int64
}

func (m *mockClientLimitClient) SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error {
	m.calls = append(m.calls, clientLimitCall{
		ClientID:      clientID,
		DailyLimit:    dailyLimit,
		MonthlyLimit:  monthlyLimit,
		TransferLimit: transferLimit,
		SetByEmployee: setByEmployee,
	})
	return m.err
}

// --- Mock ActuaryRepo with Upsert ---

type mockActuaryRepoWithUpsert struct {
	mockActuaryRepo
	upserted []*model.ActuaryLimit
}

func newMockActuaryRepoWithUpsert() *mockActuaryRepoWithUpsert {
	return &mockActuaryRepoWithUpsert{
		mockActuaryRepo: *newMockActuaryRepo(),
	}
}

func (m *mockActuaryRepoWithUpsert) Upsert(limit *model.ActuaryLimit) error {
	copy := *limit
	m.upserted = append(m.upserted, &copy)
	m.byEmpID[limit.EmployeeID] = &copy
	return nil
}

// --- Helper to build JSON ---

func mustMarshal(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// --- Tests ---

func TestCreateBlueprint_Employee(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:        "BasicTeller",
		Description: "Default teller limits",
		Type:        model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "50000",
			MaxSingleTransaction:  "100000",
			MaxDailyTransaction:   "500000",
			MaxClientDailyLimit:   "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	result, err := svc.CreateBlueprint(context.Background(), bp)
	require.NoError(t, err)
	assert.Equal(t, "BasicTeller", result.Name)
	assert.Equal(t, model.BlueprintTypeEmployee, result.Type)
	assert.NotZero(t, result.ID)
}

func TestCreateBlueprint_InvalidType(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:   "Bad",
		Type:   "invalid",
		Values: mustMarshal(map[string]string{}),
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid blueprint type")
}

func TestCreateBlueprint_InvalidValues(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:   "Bad",
		Type:   model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{}), // all empty
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is required")
}

func TestListBlueprints_FilterByType(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// Create one of each type
	empBp := model.LimitBlueprint{
		Name: "EmpBP", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	}
	actBp := model.LimitBlueprint{
		Name: "ActBP", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000", NeedApproval: true}),
	}
	_, _ = svc.CreateBlueprint(context.Background(), empBp)
	_, _ = svc.CreateBlueprint(context.Background(), actBp)

	// List all
	all, err := svc.ListBlueprints("")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Filter employee
	empOnly, err := svc.ListBlueprints(model.BlueprintTypeEmployee)
	require.NoError(t, err)
	assert.Len(t, empOnly, 1)
	assert.Equal(t, "EmpBP", empOnly[0].Name)
}

func TestApplyBlueprint_Employee(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestEmp", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "50000", MaxSingleTransaction: "100000",
			MaxDailyTransaction: "500000", MaxClientDailyLimit: "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 42, 1)
	require.NoError(t, err)

	// Verify the limit was upserted
	limit, _ := limitRepo.GetByEmployeeID(42)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(50000)))
	assert.True(t, limit.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(2500000)))
}

func TestApplyBlueprint_Actuary(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	actuaryRepo := newMockActuaryRepoWithUpsert()
	svc := NewBlueprintService(bpRepo, nil, actuaryRepo, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestAct", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000000", NeedApproval: true}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 55, 1)
	require.NoError(t, err)
	require.Len(t, actuaryRepo.upserted, 1)
	assert.Equal(t, int64(55), actuaryRepo.upserted[0].EmployeeID)
	assert.True(t, actuaryRepo.upserted[0].Limit.Equal(decimal.NewFromInt(1000000)))
	assert.True(t, actuaryRepo.upserted[0].NeedApproval)
}

func TestApplyBlueprint_Client(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	clientClient := &mockClientLimitClient{}
	svc := NewBlueprintService(bpRepo, nil, nil, clientClient, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestClient", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{
			DailyLimit: "100000", MonthlyLimit: "1000000", TransferLimit: "500000",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 99, 7)
	require.NoError(t, err)
	require.Len(t, clientClient.calls, 1)
	assert.Equal(t, int64(99), clientClient.calls[0].ClientID)
	assert.Equal(t, "100000", clientClient.calls[0].DailyLimit)
	assert.Equal(t, int64(7), clientClient.calls[0].SetByEmployee)
}

func TestApplyBlueprint_NotFound(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	err := svc.ApplyBlueprint(context.Background(), 999, 1, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteBlueprint(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "ToDelete", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.DeleteBlueprint(context.Background(), created.ID)
	require.NoError(t, err)

	_, err = svc.GetBlueprint(created.ID)
	assert.Error(t, err)
}

func TestSeedFromTemplates(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	templates := []model.LimitTemplate{
		{
			Name:                  "BasicTeller",
			Description:           "Default teller limits",
			MaxLoanApprovalAmount: decimal.NewFromInt(50000),
			MaxSingleTransaction:  decimal.NewFromInt(100000),
			MaxDailyTransaction:   decimal.NewFromInt(500000),
			MaxClientDailyLimit:   decimal.NewFromInt(250000),
			MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
		},
	}

	err := svc.SeedFromTemplates(templates)
	require.NoError(t, err)

	bps, _ := svc.ListBlueprints(model.BlueprintTypeEmployee)
	assert.Len(t, bps, 1)
	assert.Equal(t, "BasicTeller", bps[0].Name)

	// Idempotent
	err = svc.SeedFromTemplates(templates)
	require.NoError(t, err)

	bps2, _ := svc.ListBlueprints(model.BlueprintTypeEmployee)
	assert.Len(t, bps2, 1, "should not duplicate on re-seed")
}

func TestUpdateBlueprint(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "OldName", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000", NeedApproval: false}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	newValues := mustMarshal(model.ActuaryBlueprintValues{Limit: "5000", NeedApproval: true})
	updated, err := svc.UpdateBlueprint(context.Background(), created.ID, "NewName", "new desc", newValues)
	require.NoError(t, err)
	assert.Equal(t, "NewName", updated.Name)
	assert.Equal(t, "new desc", updated.Description)

	// Verify values changed
	var vals model.ActuaryBlueprintValues
	_ = json.Unmarshal(updated.Values, &vals)
	assert.Equal(t, "5000", vals.Limit)
	assert.True(t, vals.NeedApproval)
}
```

### Test Command

```bash
cd user-service && go test ./internal/service/ -run TestBlueprint -v
cd user-service && go test ./internal/service/ -run TestSeedFromTemplates -v
```

### Commit

```
feat(user-service): add BlueprintService with CRUD and apply logic
```

---

## Task 3: gRPC Handler + Proto Changes

### Files

| Action | Path |
|--------|------|
| Edit   | `contract/proto/user/user.proto` (add BlueprintService) |
| Run    | `make proto` |
| Create | `user-service/internal/handler/blueprint_handler.go` |
| Create | `user-service/internal/grpc_client/client_limit_adapter.go` |
| Edit   | `user-service/cmd/main.go` (register gRPC service, wire dependencies) |
| Edit   | `user-service/internal/config/config.go` (add CLIENT_GRPC_ADDR) |
| Edit   | `user-service/go.mod` (add clientpb dependency via contract) |

### Steps

- [ ] **3.1** Add `BlueprintService` to `contract/proto/user/user.proto` (append after ActuaryService):

```protobuf
// ==================== Blueprints ====================

service BlueprintService {
  rpc CreateBlueprint(CreateBlueprintRequest) returns (BlueprintResponse);
  rpc GetBlueprint(GetBlueprintRequest) returns (BlueprintResponse);
  rpc ListBlueprints(ListBlueprintsRequest) returns (ListBlueprintsResponse);
  rpc UpdateBlueprint(UpdateBlueprintRequest) returns (BlueprintResponse);
  rpc DeleteBlueprint(DeleteBlueprintRequest) returns (DeleteBlueprintResponse);
  rpc ApplyBlueprint(ApplyBlueprintRequest) returns (ApplyBlueprintResponse);
}

message CreateBlueprintRequest {
  string name = 1;
  string description = 2;
  string type = 3;           // "employee", "actuary", "client"
  string values_json = 4;    // JSON string of type-specific values
}

message GetBlueprintRequest {
  uint64 id = 1;
}

message ListBlueprintsRequest {
  string type = 1;           // optional filter; empty = all
}

message UpdateBlueprintRequest {
  uint64 id = 1;
  string name = 2;
  string description = 3;
  string values_json = 4;    // JSON string; empty = keep existing
}

message DeleteBlueprintRequest {
  uint64 id = 1;
}

message DeleteBlueprintResponse {}

message ApplyBlueprintRequest {
  uint64 blueprint_id = 1;
  int64 target_id = 2;       // employee_id, employee_id, or client_id depending on type
}

message ApplyBlueprintResponse {}

message BlueprintResponse {
  uint64 id = 1;
  string name = 2;
  string description = 3;
  string type = 4;
  string values_json = 5;    // JSON string
  string created_at = 6;     // RFC3339
  string updated_at = 7;     // RFC3339
}

message ListBlueprintsResponse {
  repeated BlueprintResponse blueprints = 1;
}
```

- [ ] **3.2** Run `make proto` from repo root.

- [ ] **3.3** Add `CLIENT_GRPC_ADDR` to `user-service/internal/config/config.go`:

```go
type Config struct {
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
	GRPCAddr       string
	KafkaBrokers   string
	RedisAddr      string
	MetricsPort    string
	ClientGRPCAddr string
}
```

And in `Load()`:
```go
ClientGRPCAddr: getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
```

- [ ] **3.4** Create `user-service/internal/grpc_client/client_limit_adapter.go`:

This adapter wraps the real `clientpb.ClientLimitServiceClient` and implements the `service.ClientLimitClient` interface.

```go
package grpc_client

import (
	"context"

	clientpb "github.com/exbanka/contract/clientpb"
)

// ClientLimitAdapter adapts clientpb.ClientLimitServiceClient to the
// service.ClientLimitClient interface used by BlueprintService.
type ClientLimitAdapter struct {
	client clientpb.ClientLimitServiceClient
}

func NewClientLimitAdapter(client clientpb.ClientLimitServiceClient) *ClientLimitAdapter {
	return &ClientLimitAdapter{client: client}
}

func (a *ClientLimitAdapter) SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error {
	_, err := a.client.SetClientLimits(ctx, &clientpb.SetClientLimitRequest{
		ClientId:      clientID,
		DailyLimit:    dailyLimit,
		MonthlyLimit:  monthlyLimit,
		TransferLimit: transferLimit,
		SetByEmployee: setByEmployee,
	})
	return err
}
```

- [ ] **3.5** Create `user-service/internal/handler/blueprint_handler.go`:

```go
package handler

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
)

type BlueprintGRPCHandler struct {
	pb.UnimplementedBlueprintServiceServer
	svc *service.BlueprintService
}

func NewBlueprintGRPCHandler(svc *service.BlueprintService) *BlueprintGRPCHandler {
	return &BlueprintGRPCHandler{svc: svc}
}

func (h *BlueprintGRPCHandler) CreateBlueprint(ctx context.Context, req *pb.CreateBlueprintRequest) (*pb.BlueprintResponse, error) {
	bp := model.LimitBlueprint{
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Values:      json.RawMessage(req.ValuesJson),
	}
	result, err := h.svc.CreateBlueprint(ctx, bp)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create blueprint: %v", err)
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) GetBlueprint(ctx context.Context, req *pb.GetBlueprintRequest) (*pb.BlueprintResponse, error) {
	result, err := h.svc.GetBlueprint(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "blueprint not found")
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) ListBlueprints(ctx context.Context, req *pb.ListBlueprintsRequest) (*pb.ListBlueprintsResponse, error) {
	blueprints, err := h.svc.ListBlueprints(req.Type)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list blueprints: %v", err)
	}
	resp := &pb.ListBlueprintsResponse{
		Blueprints: make([]*pb.BlueprintResponse, 0, len(blueprints)),
	}
	for _, bp := range blueprints {
		bp := bp
		resp.Blueprints = append(resp.Blueprints, toBlueprintResponse(&bp))
	}
	return resp, nil
}

func (h *BlueprintGRPCHandler) UpdateBlueprint(ctx context.Context, req *pb.UpdateBlueprintRequest) (*pb.BlueprintResponse, error) {
	var values json.RawMessage
	if req.ValuesJson != "" {
		values = json.RawMessage(req.ValuesJson)
	}
	result, err := h.svc.UpdateBlueprint(ctx, req.Id, req.Name, req.Description, values)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update blueprint: %v", err)
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) DeleteBlueprint(ctx context.Context, req *pb.DeleteBlueprintRequest) (*pb.DeleteBlueprintResponse, error) {
	if err := h.svc.DeleteBlueprint(ctx, req.Id); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to delete blueprint: %v", err)
	}
	return &pb.DeleteBlueprintResponse{}, nil
}

func (h *BlueprintGRPCHandler) ApplyBlueprint(ctx context.Context, req *pb.ApplyBlueprintRequest) (*pb.ApplyBlueprintResponse, error) {
	appliedBy := changelog.ExtractChangedBy(ctx)
	if err := h.svc.ApplyBlueprint(ctx, req.BlueprintId, req.TargetId, appliedBy); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to apply blueprint: %v", err)
	}
	return &pb.ApplyBlueprintResponse{}, nil
}

func toBlueprintResponse(bp *model.LimitBlueprint) *pb.BlueprintResponse {
	return &pb.BlueprintResponse{
		Id:          bp.ID,
		Name:        bp.Name,
		Description: bp.Description,
		Type:        bp.Type,
		ValuesJson:  string(bp.Values),
		CreatedAt:   bp.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   bp.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
```

- [ ] **3.6** Wire everything in `user-service/cmd/main.go`.

After the existing `actuarySvc` creation block, add:

```go
	// Blueprint service
	blueprintRepo := repository.NewLimitBlueprintRepository(db)

	// Connect to client-service for client blueprint apply
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("warn: failed to connect to client service: %v (client blueprints will not work)", err)
	}
	if clientConn != nil {
		defer clientConn.Close()
	}
	var clientLimitClient service.ClientLimitClient
	if clientConn != nil {
		clientLimitClient = grpc_client.NewClientLimitAdapter(
			clientpb.NewClientLimitServiceClient(clientConn),
		)
	}

	blueprintSvc := service.NewBlueprintService(blueprintRepo, employeeLimitRepo, actuaryRepo, clientLimitClient, producer, changelogRepo)
	blueprintHandler := handler.NewBlueprintGRPCHandler(blueprintSvc)
```

Add to the imports:
```go
	"google.golang.org/grpc/credentials/insecure"
	clientpb "github.com/exbanka/contract/clientpb"
	grpc_client "github.com/exbanka/user-service/internal/grpc_client"
```

Seed from templates after `limitSvc.SeedDefaultTemplates()`:
```go
	// Seed blueprints from existing templates
	templates, err := limitTemplateRepo.List()
	if err != nil {
		log.Printf("warn: failed to list templates for blueprint seeding: %v", err)
	} else {
		if err := blueprintSvc.SeedFromTemplates(templates); err != nil {
			log.Printf("warn: failed to seed blueprints from templates: %v", err)
		}
	}
```

Register the gRPC service:
```go
	pb.RegisterBlueprintServiceServer(s, blueprintHandler)
```

Add blueprint topics to `EnsureTopics`:
```go
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		// ... existing topics ...
		"user.blueprint-created",
		"user.blueprint-updated",
		"user.blueprint-deleted",
		"user.blueprint-applied",
	)
```

### Test Command

```bash
make proto
cd user-service && go build ./...
```

### Commit

```
feat(user-service): add Blueprint gRPC handler and client-service adapter
```

---

## Task 4: API Gateway Handler + v1 Routes

### Files

| Action | Path |
|--------|------|
| Create | `api-gateway/internal/handler/blueprint_handler.go` |
| Create | `api-gateway/internal/grpc/blueprint_client.go` |
| Edit   | `api-gateway/internal/router/router_v1.go` (add blueprint routes) |
| Edit   | `api-gateway/internal/router/router.go` (add blueprint client param) |
| Edit   | `api-gateway/cmd/main.go` (wire blueprint client) |

### Steps

- [ ] **4.1** Create `api-gateway/internal/grpc/blueprint_client.go`:

```go
package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	userpb "github.com/exbanka/contract/userpb"
)

// NewBlueprintClient creates a gRPC client for BlueprintService (user-service).
func NewBlueprintClient(addr string) (userpb.BlueprintServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewBlueprintServiceClient(conn), conn, nil
}
```

- [ ] **4.2** Create `api-gateway/internal/handler/blueprint_handler.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	userpb "github.com/exbanka/contract/userpb"
)

// createBlueprintBody is the request body for creating a blueprint.
type createBlueprintBody struct {
	Name        string          `json:"name" binding:"required" example:"BasicTeller"`
	Description string          `json:"description" example:"Default teller blueprint"`
	Type        string          `json:"type" binding:"required" example:"employee"`
	Values      json.RawMessage `json:"values" binding:"required"`
}

// updateBlueprintBody is the request body for updating a blueprint.
type updateBlueprintBody struct {
	Name        string          `json:"name" example:"BasicTeller"`
	Description string          `json:"description" example:"Default teller blueprint"`
	Values      json.RawMessage `json:"values"`
}

// applyBlueprintBody is the request body for applying a blueprint.
type applyBlueprintBody struct {
	TargetID int64 `json:"target_id" binding:"required" example:"123"`
}

// BlueprintHandler handles REST endpoints for limit blueprints.
type BlueprintHandler struct {
	client userpb.BlueprintServiceClient
}

// NewBlueprintHandler constructs a BlueprintHandler.
func NewBlueprintHandler(client userpb.BlueprintServiceClient) *BlueprintHandler {
	return &BlueprintHandler{client: client}
}

// ListBlueprints godoc
// @Summary      List limit blueprints
// @Description  Returns all limit blueprints, optionally filtered by type
// @Tags         blueprints
// @Produce      json
// @Param        type  query  string  false  "Filter by type (employee, actuary, client)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "list of blueprints"
// @Failure      400  {object}  map[string]interface{}  "invalid type filter"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints [get]
func (h *BlueprintHandler) ListBlueprints(c *gin.Context) {
	bpType := c.Query("type")
	if bpType != "" {
		var err error
		bpType, err = oneOf("type", bpType, "employee", "actuary", "client")
		if err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.client.ListBlueprints(c.Request.Context(), &userpb.ListBlueprintsRequest{
		Type: bpType,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"blueprints": emptyIfNil(resp.Blueprints)})
}

// CreateBlueprint godoc
// @Summary      Create a limit blueprint
// @Description  Creates a new named limit blueprint
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        body  body  createBlueprintBody  true  "Blueprint definition"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      409  {object}  map[string]interface{}  "duplicate name+type"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints [post]
func (h *BlueprintHandler) CreateBlueprint(c *gin.Context) {
	var body createBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	bpType, err := oneOf("type", body.Type, "employee", "actuary", "client")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.client.CreateBlueprint(c.Request.Context(), &userpb.CreateBlueprintRequest{
		Name:        body.Name,
		Description: body.Description,
		Type:        bpType,
		ValuesJson:  string(body.Values),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// GetBlueprint godoc
// @Summary      Get a limit blueprint
// @Description  Returns a single blueprint by ID
// @Tags         blueprints
// @Produce      json
// @Param        id  path  int  true  "Blueprint ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid id"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [get]
func (h *BlueprintHandler) GetBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	resp, err := h.client.GetBlueprint(c.Request.Context(), &userpb.GetBlueprintRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// UpdateBlueprint godoc
// @Summary      Update a limit blueprint
// @Description  Updates an existing blueprint's name, description, or values
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        id    path  int                  true  "Blueprint ID"
// @Param        body  body  updateBlueprintBody  true  "Updated fields"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [put]
func (h *BlueprintHandler) UpdateBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	var body updateBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.client.UpdateBlueprint(c.Request.Context(), &userpb.UpdateBlueprintRequest{
		Id:          id,
		Name:        body.Name,
		Description: body.Description,
		ValuesJson:  string(body.Values),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteBlueprint godoc
// @Summary      Delete a limit blueprint
// @Description  Deletes a blueprint by ID (does not affect already-applied limits)
// @Tags         blueprints
// @Produce      json
// @Param        id  path  int  true  "Blueprint ID"
// @Security     BearerAuth
// @Success      204  "deleted"
// @Failure      400  {object}  map[string]interface{}  "invalid id"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [delete]
func (h *BlueprintHandler) DeleteBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	_, err = h.client.DeleteBlueprint(c.Request.Context(), &userpb.DeleteBlueprintRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ApplyBlueprint godoc
// @Summary      Apply a blueprint to a target
// @Description  Copies blueprint limit values to the target entity (employee, actuary, or client based on blueprint type)
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        id    path  int                 true  "Blueprint ID"
// @Param        body  body  applyBlueprintBody  true  "Target to apply to"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "applied"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "blueprint not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id}/apply [post]
func (h *BlueprintHandler) ApplyBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	var body applyBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	if body.TargetID <= 0 {
		apiError(c, 400, ErrValidation, "target_id must be positive")
		return
	}

	_, err = h.client.ApplyBlueprint(middleware.GRPCContextWithChangedBy(c), &userpb.ApplyBlueprintRequest{
		BlueprintId: id,
		TargetId:    body.TargetID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "blueprint applied successfully"})
}
```

- [ ] **4.3** Add `blueprintClient userpb.BlueprintServiceClient` parameter to both `Setup()` and `SetupV1Routes()` in the router files.

In `api-gateway/internal/router/router_v1.go`, add `blueprintClient userpb.BlueprintServiceClient` as a new parameter to `SetupV1Routes()`.

In the handler creation section of `SetupV1Routes()`:
```go
	blueprintHandler := handler.NewBlueprintHandler(blueprintClient)
```

In the protected routes section of `SetupV1Routes()`, add after the existing `limitsEmployee` block:
```go
			// Limit blueprint management
			blueprintsAdmin := protected.Group("/blueprints")
			blueprintsAdmin.Use(middleware.RequirePermission("limits.manage"))
			{
				blueprintsAdmin.GET("", blueprintHandler.ListBlueprints)
				blueprintsAdmin.POST("", blueprintHandler.CreateBlueprint)
				blueprintsAdmin.GET("/:id", blueprintHandler.GetBlueprint)
				blueprintsAdmin.PUT("/:id", blueprintHandler.UpdateBlueprint)
				blueprintsAdmin.DELETE("/:id", blueprintHandler.DeleteBlueprint)
				blueprintsAdmin.POST("/:id/apply", blueprintHandler.ApplyBlueprint)
			}
```

Do the same for `router.go` `Setup()` function --- add the `blueprintClient` parameter and the same route block.

- [ ] **4.4** Wire in `api-gateway/cmd/main.go`:

After the `actuaryClient` block:
```go
	blueprintClient, blueprintConn, err := grpcclients.NewBlueprintClient(cfg.UserGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to blueprint service: %v", err)
	}
	defer blueprintConn.Close()
```

Pass `blueprintClient` to both `router.Setup(...)` and `router.SetupV1Routes(...)` calls.

### Test Command

```bash
cd api-gateway && go build ./...
```

### Commit

```
feat(api-gateway): add blueprint v1 endpoints with limits.manage permission
```

---

## Task 5: Kafka Events

### Files

| Action | Path |
|--------|------|
| Edit   | `contract/kafka/messages.go` (add topic constants + message struct) |
| Edit   | `user-service/internal/kafka/producer.go` (add PublishBlueprint method) |

### Steps

- [ ] **5.1** Add to `contract/kafka/messages.go`:

```go
// Blueprint event topic constants
const (
	TopicBlueprintCreated = "user.blueprint-created"
	TopicBlueprintUpdated = "user.blueprint-updated"
	TopicBlueprintDeleted = "user.blueprint-deleted"
	TopicBlueprintApplied = "user.blueprint-applied"
)

// BlueprintMessage is published for all blueprint lifecycle events.
type BlueprintMessage struct {
	BlueprintID   uint64 `json:"blueprint_id"`
	BlueprintName string `json:"blueprint_name"`
	BlueprintType string `json:"blueprint_type"` // "employee", "actuary", "client"
	TargetID      int64  `json:"target_id,omitempty"`
	Action        string `json:"action"` // "created", "updated", "deleted", "applied"
}
```

- [ ] **5.2** Add `PublishBlueprint` method to `user-service/internal/kafka/producer.go`:

```go
func (p *Producer) PublishBlueprint(ctx context.Context, msg kafkamsg.BlueprintMessage) error {
	var topic string
	switch msg.Action {
	case "created":
		topic = kafkamsg.TopicBlueprintCreated
	case "updated":
		topic = kafkamsg.TopicBlueprintUpdated
	case "deleted":
		topic = kafkamsg.TopicBlueprintDeleted
	case "applied":
		topic = kafkamsg.TopicBlueprintApplied
	default:
		topic = kafkamsg.TopicBlueprintCreated
	}
	return p.publish(ctx, topic, msg)
}
```

### Test Command

```bash
cd contract && go build ./...
cd user-service && go build ./...
```

### Commit

```
feat(contract): add blueprint Kafka event topics and message struct
```

---

## Task 6: Seed Migration + Docker Compose

### Files

| Action | Path |
|--------|------|
| Edit   | `docker-compose.yml` (add CLIENT_GRPC_ADDR to user-service environment) |

### Steps

- [ ] **6.1** The seed logic is already implemented in `BlueprintService.SeedFromTemplates()` and wired in `cmd/main.go` (Task 3.6). No separate migration is needed.

- [ ] **6.2** Add `CLIENT_GRPC_ADDR` to user-service in `docker-compose.yml`:

```yaml
  user-service:
    environment:
      # ... existing vars ...
      CLIENT_GRPC_ADDR: "client-service:50054"
    depends_on:
      # ... existing deps ...
      - client-service
```

### Test Command

```bash
make docker-up
# Check logs to verify blueprint seeding:
make docker-logs | grep "seeded blueprint"
```

### Commit

```
chore(docker): add CLIENT_GRPC_ADDR to user-service environment
```

---

## Task 7: Update REST_API_v1.md

### Files

| Action | Path |
|--------|------|
| Edit   | `docs/api/REST_API_v1.md` (add Limit Blueprints section) |

### Steps

- [ ] **7.1** Add a new section "Limit Blueprints (new in v1)" to `docs/api/REST_API_v1.md`:

```markdown
## 4. Limit Blueprints (new in v1)

Named, reusable limit configurations that can be applied to employees, actuaries, or clients.
All endpoints require `AuthMiddleware` + `RequirePermission("limits.manage")`.

### GET /api/v1/blueprints

List all blueprints, optionally filtered by type.

**Query parameters:**
| Parameter | Type   | Required | Description |
|-----------|--------|----------|-------------|
| type      | string | No       | Filter: `employee`, `actuary`, or `client` |

**Response (200):**
```json
{
  "blueprints": [
    {
      "id": 1,
      "name": "BasicTeller",
      "description": "Default teller limits",
      "type": "employee",
      "values_json": "{\"max_loan_approval_amount\":\"50000\",\"max_single_transaction\":\"100000\",\"max_daily_transaction\":\"500000\",\"max_client_daily_limit\":\"250000\",\"max_client_monthly_limit\":\"2500000\"}",
      "created_at": "2026-04-04T10:00:00Z",
      "updated_at": "2026-04-04T10:00:00Z"
    }
  ]
}
```

### POST /api/v1/blueprints

Create a new blueprint.

**Request body:**
```json
{
  "name": "BasicTeller",
  "description": "Default teller limits",
  "type": "employee",
  "values": {
    "max_loan_approval_amount": "50000",
    "max_single_transaction": "100000",
    "max_daily_transaction": "500000",
    "max_client_daily_limit": "250000",
    "max_client_monthly_limit": "2500000"
  }
}
```

Type-specific `values` schemas:
- **employee:** `max_loan_approval_amount`, `max_single_transaction`, `max_daily_transaction`, `max_client_daily_limit`, `max_client_monthly_limit` (all decimal strings)
- **actuary:** `limit` (decimal string), `need_approval` (boolean)
- **client:** `daily_limit`, `monthly_limit`, `transfer_limit` (all decimal strings)

**Response (201):** Blueprint object.

### GET /api/v1/blueprints/:id

Get a blueprint by ID.

**Response (200):** Blueprint object.
**Response (404):** Blueprint not found.

### PUT /api/v1/blueprints/:id

Update a blueprint. The `type` field cannot be changed.

**Request body:**
```json
{
  "name": "UpdatedName",
  "description": "Updated description",
  "values": { ... }
}
```

**Response (200):** Updated blueprint object.

### DELETE /api/v1/blueprints/:id

Delete a blueprint. Does not affect already-applied limits.

**Response (204):** No content.
**Response (404):** Blueprint not found.

### POST /api/v1/blueprints/:id/apply

Apply a blueprint to a target entity. The `target_id` is interpreted based on blueprint type:
- `employee` type: `target_id` is the employee ID (sets EmployeeLimit)
- `actuary` type: `target_id` is the employee ID (sets ActuaryLimit)
- `client` type: `target_id` is the client ID (sets ClientLimit via client-service)

**Request body:**
```json
{
  "target_id": 123
}
```

**Response (200):**
```json
{
  "message": "blueprint applied successfully"
}
```
```

- [ ] **7.2** Update the Table of Contents in `docs/api/REST_API_v1.md` to include:
```markdown
4. [Limit Blueprints](#4-limit-blueprints-new-in-v1)
```

### Commit

```
docs: add Limit Blueprints section to REST_API_v1.md
```

---

## Task 8: Swagger Regeneration

### Steps

- [ ] **8.1** Regenerate swagger docs:

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **8.2** Verify swagger renders correctly at `http://localhost:8080/swagger/index.html` (manual check after docker-up).

### Commit

```
docs: regenerate swagger for blueprint endpoints
```

---

## Task 9: Update Specification.md

### Steps

- [ ] **9.1** Add `LimitBlueprint` to Section 18 (Entities).
- [ ] **9.2** Add blueprint API routes to Section 17 (API Routes).
- [ ] **9.3** Add blueprint Kafka topics to Section 19 (Kafka Topics).
- [ ] **9.4** Add `blueprint_type` enum values (`employee`, `actuary`, `client`) to Section 20 (Enum Values).
- [ ] **9.5** Document the `CLIENT_GRPC_ADDR` dependency for user-service.

### Commit

```
docs: update Specification.md with LimitBlueprint feature
```

---

## Testing Summary

### Unit Tests (Task 2)

| Test | What It Validates |
|------|-------------------|
| `TestCreateBlueprint_Employee` | Employee blueprint creation with valid values |
| `TestCreateBlueprint_InvalidType` | Rejection of unknown type |
| `TestCreateBlueprint_InvalidValues` | Rejection of missing required fields |
| `TestListBlueprints_FilterByType` | Type filter returns correct subset |
| `TestApplyBlueprint_Employee` | Employee limit upserted from blueprint values |
| `TestApplyBlueprint_Actuary` | Actuary limit upserted from blueprint values |
| `TestApplyBlueprint_Client` | Client-service gRPC called with correct values |
| `TestApplyBlueprint_NotFound` | Error on non-existent blueprint ID |
| `TestDeleteBlueprint` | Blueprint removed, subsequent get fails |
| `TestSeedFromTemplates` | Templates converted to blueprints; idempotent |
| `TestUpdateBlueprint` | Name, description, and values updated |

### Integration Tests (to add in `test-app/workflows/`)

| Test | What It Validates |
|------|-------------------|
| `TestBlueprintCRUD` | Full create/list/get/update/delete lifecycle via HTTP |
| `TestBlueprintApplyEmployee` | Apply employee blueprint, verify limits via GET |
| `TestBlueprintApplyActuary` | Apply actuary blueprint, verify actuary info |
| `TestBlueprintApplyClient` | Apply client blueprint, verify client limits |
| `TestBlueprintTypeValidation` | 400 on invalid type |
| `TestBlueprintPermission` | 403 without limits.manage |
| `TestBlueprintSeedFromTemplates` | Existing templates appear as employee blueprints |

### Test Commands

```bash
# Unit tests
cd user-service && go test ./internal/service/ -run TestBlueprint -v
cd user-service && go test ./internal/service/ -run TestSeedFromTemplates -v

# Full suite
make test
```

---

## Dependency Graph

```
Task 1 (model + repo)
  |
  v
Task 2 (service) -----> Task 5 (kafka events - can start in parallel with Task 3)
  |
  v
Task 3 (proto + gRPC handler)
  |
  v
Task 4 (gateway handler + routes)
  |
  v
Task 6 (docker-compose)
  |
  v
Task 7 (REST_API_v1.md) + Task 8 (swagger) + Task 9 (Specification.md)
```

Tasks 5 and 3 can be partially parallelized: the Kafka message struct (Task 5.1) is needed by the service code (Task 2), so do Task 5.1 before or during Task 2, and Task 5.2 (producer method) can be done alongside Task 3.

---

## Key Decisions

1. **JSONB `Values` column:** Uses `gorm.io/datatypes.JSON` (same as verification-service and notification-service). This avoids needing separate columns per type and allows future types without schema changes.

2. **No FK to target:** Applying a blueprint copies values (same as existing `ApplyTemplate`). Deleting a blueprint does not affect already-applied limits.

3. **ClientLimitClient interface:** Defined in `service/interfaces.go` as a Go interface, not a direct `clientpb` import. This keeps the service layer testable with mocks and avoids circular dependencies.

4. **gRPC adapter pattern:** The `grpc_client/client_limit_adapter.go` bridges the interface gap between the simplified `ClientLimitClient` interface and the generated protobuf client. This is the same pattern used in other services.

5. **Old routes frozen:** `/api/limits/templates` endpoints remain unchanged. The new `/api/v1/blueprints` endpoints are additive only.

6. **Composite unique index:** `(name, type)` allows the same name across different types (e.g., "Default" can exist for both employee and client blueprints).
