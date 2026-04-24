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

// BackfillDefaultLimits applies the role-based default ActuaryLimit to any
// existing rows whose Limit is zero. Safe to call on every startup: rows
// that already have a non-zero limit are skipped. Needed because the
// defaultActuaryLimit path only fires on row creation — legacy rows
// created before role-based defaults existed (or via a SetActuaryLimit(0)
// call) stay at zero otherwise.
func (s *ActuaryService) BackfillDefaultLimits() error {
	rows, _, err := s.actuaryRepo.ListActuaries("", "", 1, 100000)
	if err != nil {
		return err
	}
	updated := 0
	for _, row := range rows {
		if row.Limit != 0 {
			continue
		}
		emp, err := s.empRepo.GetByIDWithRoles(row.EmployeeID)
		if err != nil {
			log.Printf("WARN: actuary backfill: employee %d lookup failed: %v", row.EmployeeID, err)
			continue
		}
		def := defaultActuaryLimit(emp)
		if def.IsZero() {
			continue
		}
		limit, err := s.actuaryRepo.GetByEmployeeID(row.EmployeeID)
		if err != nil {
			continue
		}
		limit.Limit = def
		if err := s.actuaryRepo.Save(limit); err != nil {
			log.Printf("WARN: actuary backfill: save failed for employee %d: %v", row.EmployeeID, err)
			continue
		}
		updated++
	}
	if updated > 0 {
		log.Printf("actuary backfill: seeded default limit on %d legacy rows", updated)
	}
	return nil
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
				Limit:        defaultActuaryLimit(emp),
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

// defaultActuaryLimit seeds the per-role RSD trading budget when an
// ActuaryLimit row is first auto-created. Mirrors the scale of
// EmployeeLimit.MaxDailyTransaction so the two limit systems don't get
// wildly out of sync. Supervisors / admins get an effectively-unlimited
// number so the gate never triggers for them (they also have
// NeedApproval=false so it wouldn't matter anyway, but keeping numbers
// sensible helps reporting).
func defaultActuaryLimit(emp *model.Employee) decimal.Decimal {
	switch {
	case hasRole(emp, "EmployeeAdmin"):
		return decimal.NewFromInt(999_999_999)
	case hasRole(emp, "EmployeeSupervisor"):
		return decimal.NewFromInt(50_000_000)
	case hasRole(emp, "EmployeeAgent"):
		return decimal.NewFromInt(5_000_000)
	}
	return decimal.NewFromInt(0)
}

func hasRole(emp *model.Employee, name string) bool {
	for _, r := range emp.Roles {
		if r.Name == name {
			return true
		}
	}
	return false
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
