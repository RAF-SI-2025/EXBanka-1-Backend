package service

import (
	"context"
	"errors"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// fundInvester is the subset of FundService the cron needs. Decoupling
// via interface keeps the test surface minimal.
type fundInvester interface {
	Invest(ctx context.Context, in InvestInput) (*model.FundContribution, error)
}

type RecurringFundService struct {
	repo     *repository.RecurringFundInvestmentRepository
	fundRepo *repository.FundRepository
	invester fundInvester
	notifier recurringOrderNotifier
}

func NewRecurringFundService(
	repo *repository.RecurringFundInvestmentRepository,
	fundRepo *repository.FundRepository,
	invester fundInvester,
	notifier recurringOrderNotifier,
) *RecurringFundService {
	s := &RecurringFundService{repo: repo, fundRepo: fundRepo}
	if invester != nil {
		s.invester = invester
	}
	if notifier != nil {
		s.notifier = notifier
	}
	return s
}

var (
	ErrRecurringFundFundNotFound    = svcerr.New(codes.NotFound, "fund not found")
	ErrRecurringFundFundNotEligible = svcerr.New(codes.FailedPrecondition, "fund is not eligible for recurring investments")
	ErrRecurringFundNotFound        = svcerr.New(codes.NotFound, "recurring fund investment not found")
	ErrRecurringFundBelowMinimum    = svcerr.New(codes.FailedPrecondition, "amount below fund minimum")
)

func (s *RecurringFundService) Create(row *model.RecurringFundInvestment) error {
	fund, err := s.fundRepo.GetByID(row.FundID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRecurringFundFundNotFound
		}
		return err
	}
	// Eligibility: open funds always; closed funds only during fundraising
	// (otherwise the recurrence can never fire successfully).
	switch fund.FundType {
	case "", model.FundTypeOpen:
		// fine
	case model.FundTypeClosed:
		if fund.FundStatus != model.FundStatusFundraising {
			return ErrRecurringFundFundNotEligible
		}
	}
	if row.AmountRSD.LessThan(fund.MinimumContributionRSD) {
		return ErrRecurringFundBelowMinimum
	}
	if row.NextRun.IsZero() {
		row.NextRun = row.AdvanceNextRun(time.Now().UTC().AddDate(0, -1, 0))
	}
	return s.repo.Create(row)
}

func (s *RecurringFundService) Get(id, clientID uint64) (*model.RecurringFundInvestment, error) {
	row, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRecurringFundNotFound
		}
		return nil, err
	}
	if row.ClientID != clientID {
		return nil, ErrRecurringFundNotFound
	}
	return row, nil
}

func (s *RecurringFundService) Pause(id, clientID uint64) error {
	return s.setActive(id, clientID, false)
}
func (s *RecurringFundService) Resume(id, clientID uint64) error {
	return s.setActive(id, clientID, true)
}

func (s *RecurringFundService) setActive(id, clientID uint64, active bool) error {
	row, err := s.Get(id, clientID)
	if err != nil {
		return err
	}
	row.Active = active
	return s.repo.Save(row)
}

func (s *RecurringFundService) Cancel(id, clientID uint64) error {
	row, err := s.Get(id, clientID)
	if err != nil {
		return err
	}
	_, err = s.repo.Delete(row.ID, clientID)
	return err
}

func (s *RecurringFundService) ListMy(clientID uint64) ([]model.RecurringFundInvestment, error) {
	return s.repo.ListByClient(clientID)
}

// RunDue iterates active rows whose NextRun ≤ now. Each row attempts an
// Invest; on success emits FUND_RECURRING_EXECUTED, on insufficient
// funds (or fund-no-longer-eligible) emits FUND_RECURRING_SKIPPED.
// NextRun always advances so a failed run doesn't peg the loop.
func (s *RecurringFundService) RunDue(ctx context.Context, now time.Time) {
	if s.invester == nil {
		return
	}
	rows, err := s.repo.ListDue(now)
	if err != nil {
		log.Printf("WARN: recurring-fund ListDue failed: %v", err)
		return
	}
	for i := range rows {
		s.runOne(ctx, &rows[i], now)
	}
}

func (s *RecurringFundService) runOne(ctx context.Context, row *model.RecurringFundInvestment, now time.Time) {
	fund, err := s.fundRepo.GetByID(row.FundID)
	notifyType := "FUND_RECURRING_EXECUTED"
	reason := ""
	if err != nil {
		notifyType = "FUND_RECURRING_SKIPPED"
		reason = "fund_not_found"
	} else if fund.FundType == model.FundTypeClosed && fund.FundStatus != model.FundStatusFundraising {
		notifyType = "FUND_RECURRING_SKIPPED"
		reason = "fund_not_open"
	} else {
		// Best-effort invest. Insufficient-funds + any other failure is a
		// skip; we leave the recurrence active.
		_, ierr := s.invester.Invest(ctx, InvestInput{
			ActorUserID:     row.ClientID,
			ActorSystemType: "client",
			FundID:          row.FundID,
			SourceAccountID: row.SourceAccountID,
			Amount:          row.AmountRSD,
			Currency:        "RSD",
		})
		if ierr != nil {
			notifyType = "FUND_RECURRING_SKIPPED"
			reason = ierr.Error()
		}
	}
	data := map[string]string{
		"fund_id":    kafkaUint(row.FundID),
		"amount_rsd": row.AmountRSD.StringFixed(4),
	}
	if reason != "" {
		data["reason"] = reason
	}
	s.notify(ctx, row, notifyType, data)
	stamped := now
	row.LastRun = &stamped
	row.NextRun = row.AdvanceNextRun(now)
	if err := s.repo.Save(row); err != nil {
		log.Printf("WARN: recurring-fund save failed for %d: %v", row.ID, err)
	}
}

func (s *RecurringFundService) notify(ctx context.Context, row *model.RecurringFundInvestment, key string, data map[string]string) {
	if s.notifier == nil {
		return
	}
	_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  row.ClientID,
		Type:    key,
		Data:    data,
		RefType: "fund",
		RefID:   row.FundID,
	})
}

// RecurringFundCron mirrors RecurringOrderCron but for fund-investment
// templates. Default tick: 1 hour.
type RecurringFundCron struct {
	svc      *RecurringFundService
	interval time.Duration
	entry    *cronreg.Entry
}

func NewRecurringFundCron(svc *RecurringFundService, interval time.Duration, registry *cronreg.Registry) *RecurringFundCron {
	if interval <= 0 {
		interval = time.Hour
	}
	c := &RecurringFundCron{svc: svc, interval: interval}
	c.entry = registry.Register("recurring-fund-cron", "Execute due recurring fund investments", interval)
	return c
}

func (c *RecurringFundCron) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if !c.entry.BeginRun() {
				continue
			}
			c.svc.RunDue(ctx, t.UTC())
			c.entry.EndRun(nil)
		case <-c.entry.TriggerChan():
			if !c.entry.BeginRun() {
				continue
			}
			c.svc.RunDue(ctx, time.Now().UTC())
			c.entry.EndRun(nil)
		}
	}
}
