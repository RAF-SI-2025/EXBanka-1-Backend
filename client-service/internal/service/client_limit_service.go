package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/shopspring/decimal"
)

// ClientLimitRepo is the interface for client limit persistence.
type ClientLimitRepo interface {
	GetByClientID(clientID int64) (*model.ClientLimit, error)
	Upsert(limit *model.ClientLimit) error
}

// ClientLimitService manages client transaction limits.
type ClientLimitService struct {
	limitRepo    ClientLimitRepo
	userLimitSvc userpb.EmployeeLimitServiceClient
	producer     *kafkaprod.Producer
}

// NewClientLimitService constructs a ClientLimitService.
func NewClientLimitService(
	limitRepo ClientLimitRepo,
	userLimitSvc userpb.EmployeeLimitServiceClient,
	producer *kafkaprod.Producer,
) *ClientLimitService {
	return &ClientLimitService{
		limitRepo:    limitRepo,
		userLimitSvc: userLimitSvc,
		producer:     producer,
	}
}

// GetClientLimits returns the limits for a client, using defaults if none set.
func (s *ClientLimitService) GetClientLimits(clientID int64) (*model.ClientLimit, error) {
	return s.limitRepo.GetByClientID(clientID)
}

// SetClientLimits sets the transaction limits for a client.
// The setting employee's limits must be >= the values being set.
func (s *ClientLimitService) SetClientLimits(ctx context.Context, limit model.ClientLimit) (*model.ClientLimit, error) {
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

	result, err := s.limitRepo.GetByClientID(limit.ClientID)
	if err != nil {
		return nil, err
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
