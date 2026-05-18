package service

import (
	"context"
	"fmt"
	"log"

	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/contract/changelog"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/shopspring/decimal"
)

// ClientLimitRepo is the interface for client limit persistence.
type ClientLimitRepo interface {
	GetByClientID(clientID int64) (*model.ClientLimit, error)
	Upsert(limit *model.ClientLimit) error
}

// ClientLimitService manages client transaction limits.
type ClientLimitService struct {
	limitRepo     ClientLimitRepo
	userLimitSvc  userpb.EmployeeLimitServiceClient
	producer      *kafkaprod.Producer
	changelogRepo ChangelogRepo
}

// NewClientLimitService constructs a ClientLimitService.
func NewClientLimitService(
	limitRepo ClientLimitRepo,
	userLimitSvc userpb.EmployeeLimitServiceClient,
	producer *kafkaprod.Producer,
	changelogRepo ...ChangelogRepo,
) *ClientLimitService {
	svc := &ClientLimitService{
		limitRepo:    limitRepo,
		userLimitSvc: userLimitSvc,
		producer:     producer,
	}
	if len(changelogRepo) > 0 {
		svc.changelogRepo = changelogRepo[0]
	}
	return svc
}

// GetClientLimits returns the limits for a client, using defaults if none set.
func (s *ClientLimitService) GetClientLimits(clientID int64) (*model.ClientLimit, error) {
	return s.limitRepo.GetByClientID(clientID)
}

// SetClientLimits sets the transaction limits for a client.
// The setting employee's limits must be >= the values being set.
func (s *ClientLimitService) SetClientLimits(ctx context.Context, limit model.ClientLimit, changedBy int64) (*model.ClientLimit, error) {
	// Fetch old limits for changelog.
	oldLimit, _ := s.limitRepo.GetByClientID(limit.ClientID)
	// Verify the employee's own limits authorize these client limits
	if s.userLimitSvc != nil {
		empLimits, err := s.userLimitSvc.GetEmployeeLimits(ctx, &userpb.EmployeeLimitRequest{
			EmployeeId: limit.SetByEmployee,
		})
		if err != nil {
			log.Printf("warn: SetClientLimits employee-lookup gRPC failed: %v", err)
			return nil, fmt.Errorf("SetClientLimits(employee=%d): %w", limit.SetByEmployee, ErrEmployeeLookupFailed)
		}

		maxClientDaily, err := decimal.NewFromString(empLimits.MaxClientDailyLimit)
		if err != nil {
			log.Printf("warn: SetClientLimits invalid max_client_daily_limit decimal %q: %v", empLimits.MaxClientDailyLimit, err)
			return nil, fmt.Errorf("SetClientLimits: %w", ErrInvalidEmployeeLimits)
		}
		maxClientMonthly, err := decimal.NewFromString(empLimits.MaxClientMonthlyLimit)
		if err != nil {
			log.Printf("warn: SetClientLimits invalid max_client_monthly_limit decimal %q: %v", empLimits.MaxClientMonthlyLimit, err)
			return nil, fmt.Errorf("SetClientLimits: %w", ErrInvalidEmployeeLimits)
		}

		if limit.DailyLimit.GreaterThan(maxClientDaily) {
			return nil, fmt.Errorf("SetClientLimits: daily limit %s exceeds employee authorization %s: %w",
				limit.DailyLimit.String(), maxClientDaily.String(), ErrLimitsExceedEmployee)
		}
		if limit.MonthlyLimit.GreaterThan(maxClientMonthly) {
			return nil, fmt.Errorf("SetClientLimits: monthly limit %s exceeds employee authorization %s: %w",
				limit.MonthlyLimit.String(), maxClientMonthly.String(), ErrLimitsExceedEmployee)
		}
	}

	if err := s.limitRepo.Upsert(&limit); err != nil {
		return nil, err
	}
	ClientLimitUpdatesTotal.Inc()

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

	if s.producer != nil {
		if pubErr := s.producer.PublishClientLimitsUpdated(ctx, kafkamsg.ClientLimitsUpdatedMessage{
			ClientID:      limit.ClientID,
			SetByEmployee: limit.SetByEmployee,
			Action:        "set",
		}); pubErr != nil {
			log.Printf("warn: failed to publish client-limits-updated event: %v", pubErr)
		}
	}

	return result, nil
}
