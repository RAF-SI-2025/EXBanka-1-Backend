package service

import (
	"context"
	"errors"
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
			return nil, fmt.Errorf("failed to verify employee limits: %w", err)
		}

		maxClientDaily, err := decimal.NewFromString(empLimits.MaxClientDailyLimit)
		if err != nil {
			return nil, errors.New("invalid employee max_client_daily_limit")
		}
		maxClientMonthly, err := decimal.NewFromString(empLimits.MaxClientMonthlyLimit)
		if err != nil {
			return nil, errors.New("invalid employee max_client_monthly_limit")
		}

		if limit.DailyLimit.GreaterThan(maxClientDaily) {
			return nil, fmt.Errorf("daily limit %s exceeds employee authorization %s",
				limit.DailyLimit.String(), maxClientDaily.String())
		}
		if limit.MonthlyLimit.GreaterThan(maxClientMonthly) {
			return nil, fmt.Errorf("monthly limit %s exceeds employee authorization %s",
				limit.MonthlyLimit.String(), maxClientMonthly.String())
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
