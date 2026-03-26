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

// LimitService manages employee limits and limit templates.
type LimitService struct {
	limitRepo    EmployeeLimitRepo
	templateRepo LimitTemplateRepo
	producer     *kafkaprod.Producer
}

func NewLimitService(limitRepo EmployeeLimitRepo, templateRepo LimitTemplateRepo, producer *kafkaprod.Producer) *LimitService {
	return &LimitService{
		limitRepo:    limitRepo,
		templateRepo: templateRepo,
		producer:     producer,
	}
}

// GetEmployeeLimits returns the limits for an employee. Returns a zero-value limit if none configured.
func (s *LimitService) GetEmployeeLimits(employeeID int64) (*model.EmployeeLimit, error) {
	return s.limitRepo.GetByEmployeeID(employeeID)
}

// SetEmployeeLimits creates or updates the limits for an employee.
func (s *LimitService) SetEmployeeLimits(ctx context.Context, limit model.EmployeeLimit) (*model.EmployeeLimit, error) {
	if err := s.limitRepo.Upsert(&limit); err != nil {
		return nil, err
	}
	result, err := s.limitRepo.GetByEmployeeID(limit.EmployeeID)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if pubErr := s.producer.PublishEmployeeLimitsUpdated(ctx, kafkamsg.EmployeeLimitsUpdatedMessage{
			EmployeeID: limit.EmployeeID,
			Action:     "set",
		}); pubErr != nil {
			log.Printf("warn: failed to publish employee-limits-updated event: %v", pubErr)
		}
	}
	return result, nil
}

// ApplyTemplate copies template values to an employee's limit record.
func (s *LimitService) ApplyTemplate(ctx context.Context, employeeID int64, templateName string) (*model.EmployeeLimit, error) {
	tmpl, err := s.templateRepo.GetByName(templateName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("template not found: " + templateName)
		}
		return nil, err
	}

	limit := model.EmployeeLimit{
		EmployeeID:            employeeID,
		MaxLoanApprovalAmount: tmpl.MaxLoanApprovalAmount,
		MaxSingleTransaction:  tmpl.MaxSingleTransaction,
		MaxDailyTransaction:   tmpl.MaxDailyTransaction,
		MaxClientDailyLimit:   tmpl.MaxClientDailyLimit,
		MaxClientMonthlyLimit: tmpl.MaxClientMonthlyLimit,
	}
	if err := s.limitRepo.Upsert(&limit); err != nil {
		return nil, err
	}
	result, err := s.limitRepo.GetByEmployeeID(employeeID)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if pubErr := s.producer.PublishEmployeeLimitsUpdated(ctx, kafkamsg.EmployeeLimitsUpdatedMessage{
			EmployeeID: employeeID,
			Action:     "template_applied",
		}); pubErr != nil {
			log.Printf("warn: failed to publish employee-limits-updated event: %v", pubErr)
		}
	}
	return result, nil
}

// CreateTemplate creates a new limit template.
func (s *LimitService) CreateTemplate(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error) {
	if err := s.templateRepo.Create(&t); err != nil {
		return nil, err
	}
	if s.producer != nil {
		if pubErr := s.producer.PublishLimitTemplate(ctx, kafkamsg.LimitTemplateMessage{
			TemplateID:   t.ID,
			TemplateName: t.Name,
			Action:       "created",
		}); pubErr != nil {
			log.Printf("warn: failed to publish limit-template-created event: %v", pubErr)
		}
	}
	return &t, nil
}

// ListTemplates returns all limit templates.
func (s *LimitService) ListTemplates() ([]model.LimitTemplate, error) {
	return s.templateRepo.List()
}

// UpdateTemplate updates an existing limit template.
func (s *LimitService) UpdateTemplate(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error) {
	existing, err := s.templateRepo.GetByID(t.ID)
	if err != nil {
		return nil, err
	}
	existing.Name = t.Name
	existing.Description = t.Description
	existing.MaxLoanApprovalAmount = t.MaxLoanApprovalAmount
	existing.MaxSingleTransaction = t.MaxSingleTransaction
	existing.MaxDailyTransaction = t.MaxDailyTransaction
	existing.MaxClientDailyLimit = t.MaxClientDailyLimit
	existing.MaxClientMonthlyLimit = t.MaxClientMonthlyLimit
	if err := s.templateRepo.Update(existing); err != nil {
		return nil, err
	}
	if s.producer != nil {
		if pubErr := s.producer.PublishLimitTemplate(ctx, kafkamsg.LimitTemplateMessage{
			TemplateID:   existing.ID,
			TemplateName: existing.Name,
			Action:       "updated",
		}); pubErr != nil {
			log.Printf("warn: failed to publish limit-template-updated event: %v", pubErr)
		}
	}
	return existing, nil
}

// DeleteTemplate deletes a limit template by ID.
func (s *LimitService) DeleteTemplate(ctx context.Context, id int64) error {
	existing, err := s.templateRepo.GetByID(id)
	if err != nil {
		return err
	}
	if err := s.templateRepo.Delete(id); err != nil {
		return err
	}
	if s.producer != nil {
		if pubErr := s.producer.PublishLimitTemplate(ctx, kafkamsg.LimitTemplateMessage{
			TemplateID:   id,
			TemplateName: existing.Name,
			Action:       "deleted",
		}); pubErr != nil {
			log.Printf("warn: failed to publish limit-template-deleted event: %v", pubErr)
		}
	}
	return nil
}

// SeedDefaultTemplates creates the default limit templates if they don't exist.
func (s *LimitService) SeedDefaultTemplates() error {
	defaults := []model.LimitTemplate{
		{
			Name:                  "BasicTeller",
			Description:           "Default limits for basic teller employees",
			MaxLoanApprovalAmount: decimal.NewFromInt(50000),
			MaxSingleTransaction:  decimal.NewFromInt(100000),
			MaxDailyTransaction:   decimal.NewFromInt(500000),
			MaxClientDailyLimit:   decimal.NewFromInt(250000),
			MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
		},
		{
			Name:                  "SeniorAgent",
			Description:           "Default limits for senior agent employees",
			MaxLoanApprovalAmount: decimal.NewFromInt(500000),
			MaxSingleTransaction:  decimal.NewFromInt(1000000),
			MaxDailyTransaction:   decimal.NewFromInt(5000000),
			MaxClientDailyLimit:   decimal.NewFromInt(1000000),
			MaxClientMonthlyLimit: decimal.NewFromInt(10000000),
		},
		{
			Name:                  "Supervisor",
			Description:           "Default limits for supervisor employees",
			MaxLoanApprovalAmount: decimal.NewFromInt(5000000),
			MaxSingleTransaction:  decimal.NewFromInt(10000000),
			MaxDailyTransaction:   decimal.NewFromInt(50000000),
			MaxClientDailyLimit:   decimal.NewFromInt(5000000),
			MaxClientMonthlyLimit: decimal.NewFromInt(50000000),
		},
	}

	for _, tmpl := range defaults {
		_, err := s.templateRepo.GetByName(tmpl.Name)
		if err == nil {
			// Already exists
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		t := tmpl
		if err := s.templateRepo.Create(&t); err != nil {
			return err
		}
		log.Printf("seeded limit template: %s", t.Name)
	}
	return nil
}
