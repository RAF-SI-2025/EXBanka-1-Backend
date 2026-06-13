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

// ClientEmailLookup resolves a client's email for the limit-change notification
// (SP5 D1). Optional — when nil, only the in-app notification is sent.
type ClientEmailLookup interface {
	GetEmailByID(clientID int64) (string, error)
}

// employeeLimitReader is the narrow interface the cap-check uses against the
// local employee-limit replica. Satisfied by *repository.EmployeeLimitReplicaRepository.
type employeeLimitReader interface {
	GetByEmployeeID(ctx context.Context, id uint64) (model.EmployeeLimitReplica, error)
	Upsert(ctx context.Context, in model.EmployeeLimitReplica) error
}

// ClientLimitService manages client transaction limits.
type ClientLimitService struct {
	limitRepo     ClientLimitRepo
	userLimitSvc  userpb.EmployeeLimitServiceClient
	producer      *kafkaprod.Producer
	changelogRepo ChangelogRepo
	emailLookup   ClientEmailLookup   // optional (SP5 D1)
	limitReplica  employeeLimitReader // optional local employee-limit read-model (SP-2b)
}

// WithEmailLookup wires the client-email lookup used by the limit-change
// notification (SP5 D1). Optional.
func (s *ClientLimitService) WithEmailLookup(l ClientEmailLookup) *ClientLimitService {
	s.emailLookup = l
	return s
}

// NewClientLimitService constructs a ClientLimitService.
func NewClientLimitService(
	limitRepo ClientLimitRepo,
	userLimitSvc userpb.EmployeeLimitServiceClient,
	producer *kafkaprod.Producer,
	limitReplica employeeLimitReader,
	changelogRepo ...ChangelogRepo,
) *ClientLimitService {
	svc := &ClientLimitService{
		limitRepo:    limitRepo,
		userLimitSvc: userLimitSvc,
		producer:     producer,
		limitReplica: limitReplica,
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

// parseDecimalOrZeroSvc parses s as a decimal for the replica backfill path.
// Empty string → decimal.Zero (no limit set). Non-empty unparseable → decimal.Zero
// with a warning log (graceful degradation for non-critical fields).
func parseDecimalOrZeroSvc(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		log.Printf("warn: employee-limit replica backfill: unparseable decimal %q, using zero: %v", s, err)
		return decimal.Zero
	}
	return d
}

// resolveEmployeeClientCaps returns the employee's MaxClientDailyLimit and
// MaxClientMonthlyLimit for the cap check in SetClientLimits.
//
//   - Replica hit → (maxDaily, maxMonthly, true, nil); no gRPC call made.
//   - Replica miss/error + userLimitSvc nil → (zero, zero, false, nil); caller skips check.
//   - Replica miss/error + gRPC error → (zero, zero, false, wrapped ErrEmployeeLookupFailed).
//   - Replica miss/error + gRPC ok, parse error → (zero, zero, false, wrapped ErrInvalidEmployeeLimits).
//   - Replica miss/error + gRPC ok, parsed → backfills replica (Version=0), returns (vals, true, nil).
func (s *ClientLimitService) resolveEmployeeClientCaps(
	ctx context.Context,
	employeeID int64,
) (maxDaily, maxMonthly decimal.Decimal, ok bool, err error) {
	// Try local replica first.
	if s.limitReplica != nil {
		rep, repErr := s.limitReplica.GetByEmployeeID(ctx, uint64(employeeID))
		if repErr == nil {
			// Hit — return replica caps immediately (no gRPC call).
			return rep.MaxClientDailyLimit, rep.MaxClientMonthlyLimit, true, nil
		}
		// Any error (not found or transient) → fall through to gRPC.
	}

	// No replica hit — fall back to synchronous gRPC.
	if s.userLimitSvc == nil {
		return decimal.Zero, decimal.Zero, false, nil // skip check as today
	}

	empLimits, gRPCErr := s.userLimitSvc.GetEmployeeLimits(ctx, &userpb.EmployeeLimitRequest{
		EmployeeId: employeeID,
	})
	if gRPCErr != nil {
		log.Printf("warn: SetClientLimits employee-lookup gRPC failed: %v", gRPCErr)
		return decimal.Zero, decimal.Zero, false,
			fmt.Errorf("SetClientLimits(employee=%d): %w", employeeID, ErrEmployeeLookupFailed)
	}

	maxClientDaily, parseErr := decimal.NewFromString(empLimits.MaxClientDailyLimit)
	if parseErr != nil {
		log.Printf("warn: SetClientLimits invalid max_client_daily_limit decimal %q: %v", empLimits.MaxClientDailyLimit, parseErr)
		return decimal.Zero, decimal.Zero, false,
			fmt.Errorf("SetClientLimits: %w", ErrInvalidEmployeeLimits)
	}
	maxClientMonthly, parseErr := decimal.NewFromString(empLimits.MaxClientMonthlyLimit)
	if parseErr != nil {
		log.Printf("warn: SetClientLimits invalid max_client_monthly_limit decimal %q: %v", empLimits.MaxClientMonthlyLimit, parseErr)
		return decimal.Zero, decimal.Zero, false,
			fmt.Errorf("SetClientLimits: %w", ErrInvalidEmployeeLimits)
	}

	// Backfill the replica with a full 5-field snapshot so the next call is a
	// hit. Version=0 ensures real events (v>=1) always win via the monotonic guard.
	if s.limitReplica != nil {
		_ = s.limitReplica.Upsert(ctx, model.EmployeeLimitReplica{
			EmployeeID:            uint64(employeeID),
			MaxLoanApprovalAmount: parseDecimalOrZeroSvc(empLimits.MaxLoanApprovalAmount),
			MaxSingleTransaction:  parseDecimalOrZeroSvc(empLimits.MaxSingleTransaction),
			MaxDailyTransaction:   parseDecimalOrZeroSvc(empLimits.MaxDailyTransaction),
			MaxClientDailyLimit:   maxClientDaily,
			MaxClientMonthlyLimit: maxClientMonthly,
			Version:               0,
		})
	}

	return maxClientDaily, maxClientMonthly, true, nil
}

// SetClientLimits sets the transaction limits for a client.
// The setting employee's limits must be >= the values being set.
func (s *ClientLimitService) SetClientLimits(ctx context.Context, limit model.ClientLimit, changedBy int64) (*model.ClientLimit, error) {
	// Fetch old limits for changelog.
	oldLimit, _ := s.limitRepo.GetByClientID(limit.ClientID)

	// Verify the employee's own limits authorize these client limits.
	maxDaily, maxMonthly, capOK, capErr := s.resolveEmployeeClientCaps(ctx, limit.SetByEmployee)
	if capErr != nil {
		return nil, capErr
	}
	if capOK {
		if limit.DailyLimit.GreaterThan(maxDaily) {
			return nil, fmt.Errorf("SetClientLimits: daily limit %s exceeds employee authorization %s: %w",
				limit.DailyLimit.String(), maxDaily.String(), ErrLimitsExceedEmployee)
		}
		if limit.MonthlyLimit.GreaterThan(maxMonthly) {
			return nil, fmt.Errorf("SetClientLimits: monthly limit %s exceeds employee authorization %s: %w",
				limit.MonthlyLimit.String(), maxMonthly.String(), ErrLimitsExceedEmployee)
		}
	}

	if err := s.limitRepo.Upsert(&limit); err != nil {
		log.Printf("warn: SetClientLimits upsert failed for client %d: %v", limit.ClientID, err)
		return nil, ErrLimitPersistFailed
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
			DailyLimit:    result.DailyLimit.StringFixed(4),
			MonthlyLimit:  result.MonthlyLimit.StringFixed(4),
			TransferLimit: result.TransferLimit.StringFixed(4),
			Version:       result.Version,
		}); pubErr != nil {
			log.Printf("warn: failed to publish client-limits-updated event: %v", pubErr)
		}

		// SP5 D1: notify the client (in-app + best-effort email) of the change.
		data := map[string]string{
			"daily_limit":    result.DailyLimit.String(),
			"monthly_limit":  result.MonthlyLimit.String(),
			"transfer_limit": result.TransferLimit.String(),
			"currency":       "RSD",
		}
		if nErr := s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  uint64(limit.ClientID),
			Type:    "LIMIT_CHANGED",
			Data:    data,
			RefType: "client_limit",
			RefID:   uint64(limit.ClientID),
		}); nErr != nil {
			log.Printf("warn: failed to publish LIMIT_CHANGED notification: %v", nErr)
		}
		if s.emailLookup != nil {
			if email, eErr := s.emailLookup.GetEmailByID(limit.ClientID); eErr == nil && email != "" {
				_ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
					To:        email,
					EmailType: kafkamsg.EmailType("LIMIT_CHANGED"),
					Data:      data,
				})
			}
		}
	}

	return result, nil
}
