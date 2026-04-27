package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"gorm.io/datatypes"
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
		return nil, fmt.Errorf("CreateBlueprint: name is required: %w", ErrInvalidBlueprint)
	}
	if !model.ValidBlueprintTypes[bp.Type] {
		return nil, fmt.Errorf("CreateBlueprint(type=%s): invalid blueprint type, must be one of employee/actuary/client: %w", bp.Type, ErrInvalidBlueprint)
	}
	if err := s.validateValues(bp.Type, json.RawMessage(bp.Values)); err != nil {
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
		return nil, fmt.Errorf("ListBlueprints(type=%s): invalid blueprint type filter, must be one of employee/actuary/client: %w", bpType, ErrInvalidBlueprint)
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
		existing.Values = datatypes.JSON(values)
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
			return fmt.Errorf("ApplyBlueprint(id=%d): %w", blueprintID, ErrBlueprintNotFound)
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
	s.publishAppliedEvent(ctx, bp, employeeID)
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
	s.publishAppliedEvent(ctx, bp, employeeID)
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
	s.publishAppliedEvent(ctx, bp, clientID)
	return nil
}

func (s *BlueprintService) publishAppliedEvent(ctx context.Context, bp *model.LimitBlueprint, tid int64) {
	s.publishEvent(ctx, kafkamsg.BlueprintMessage{
		BlueprintID:   bp.ID,
		BlueprintName: bp.Name,
		BlueprintType: bp.Type,
		TargetID:      tid,
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
