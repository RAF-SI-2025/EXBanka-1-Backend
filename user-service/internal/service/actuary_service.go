package service

import (
	"context"
	"errors"
	"log"

	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
)

type ActuaryService struct {
	actuaryRepo ActuaryRepo
	empRepo     ActuaryEmpRepo
	producer    *kafkaprod.Producer
}

func NewActuaryService(actuaryRepo ActuaryRepo, empRepo ActuaryEmpRepo, producer *kafkaprod.Producer) *ActuaryService {
	return &ActuaryService{
		actuaryRepo: actuaryRepo,
		empRepo:     empRepo,
		producer:    producer,
	}
}

func (s *ActuaryService) ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
	return s.actuaryRepo.ListActuaries(search, position, page, pageSize)
}

func (s *ActuaryService) GetActuaryInfo(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
	return s.getOrCreateActuaryLimit(employeeID)
}

func (s *ActuaryService) SetActuaryLimit(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	if limitAmount.IsNegative() {
		return nil, errors.New("limit must not be negative")
	}
	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.Limit = limitAmount
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "limit_set")
	return limit, nil
}

func (s *ActuaryService) ResetUsedLimit(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.UsedLimit = decimal.NewFromInt(0)
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "used_limit_reset")
	return limit, nil
}

func (s *ActuaryService) SetNeedApproval(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error) {
	// Hierarchy enforcement: caller must outrank target.
	if err := checkHierarchy(s.empRepo, changedBy, employeeID); err != nil {
		return nil, err
	}

	limit, _, err := s.getOrCreateActuaryLimit(employeeID)
	if err != nil {
		return nil, err
	}
	limit.NeedApproval = needApproval
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "need_approval_changed")
	return limit, nil
}

// UpdateUsedLimit atomically adjusts the actuary's used_limit by the signed
// RSD amount. Positive values increment (e.g., on successful order placement
// or supervisor approval); negative values decrement (e.g., on order
// cancellation). The floor is clamped at zero to guard against double-refunds.
// Called by stock-service as part of the actuary limit enforcement flow.
func (s *ActuaryService) UpdateUsedLimit(ctx context.Context, id int64, amountRSD decimal.Decimal) (*model.ActuaryLimit, error) {
	limit, err := s.actuaryRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	next := limit.UsedLimit.Add(amountRSD)
	if next.IsNegative() {
		next = decimal.NewFromInt(0)
	}
	limit.UsedLimit = next
	if err := s.actuaryRepo.Save(limit); err != nil {
		return nil, err
	}
	s.publishActuaryEvent(ctx, limit.EmployeeID, "used_limit_updated")
	return limit, nil
}

func (s *ActuaryService) publishActuaryEvent(ctx context.Context, employeeID int64, action string) {
	if s.producer == nil {
		return
	}
	msg := kafkamsg.ActuaryLimitUpdatedMessage{
		EmployeeID: employeeID,
		Action:     action,
	}
	if err := s.producer.PublishActuaryLimitUpdated(ctx, msg); err != nil {
		log.Printf("warn: failed to publish actuary-limit-updated event: %v", err)
	}
}

// getOrCreateActuaryLimit looks up an actuary limit by employee ID, auto-creating
// a default one if the employee is an actuary but has no limit row yet.
func (s *ActuaryService) getOrCreateActuaryLimit(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
	emp, err := s.empRepo.GetByIDWithRoles(employeeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("employee not found")
		}
		return nil, nil, err
	}
	if !isActuary(emp) {
		return nil, nil, errors.New("employee is not an actuary (must have EmployeeAgent or EmployeeSupervisor role)")
	}

	limit, err := s.actuaryRepo.GetByEmployeeID(employeeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			limit = &model.ActuaryLimit{
				EmployeeID:   employeeID,
				Limit:        decimal.NewFromInt(0),
				UsedLimit:    decimal.NewFromInt(0),
				NeedApproval: !isSupervisor(emp),
			}
			if err := s.actuaryRepo.Create(limit); err != nil {
				return nil, nil, err
			}
		} else {
			return nil, nil, err
		}
	}
	return limit, emp, nil
}

func isActuary(emp *model.Employee) bool {
	for _, r := range emp.Roles {
		if r.Name == "EmployeeAgent" || r.Name == "EmployeeSupervisor" || r.Name == "EmployeeAdmin" {
			return true
		}
	}
	return false
}

func isSupervisor(emp *model.Employee) bool {
	for _, r := range emp.Roles {
		if r.Name == "EmployeeSupervisor" || r.Name == "EmployeeAdmin" {
			return true
		}
	}
	return false
}
