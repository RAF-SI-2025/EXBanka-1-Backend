package service

import (
	"context"
	"errors"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// recurringOrderNotifier publishes per-tick notifications. Narrow
// interface so tests don't drag in *kafka.Producer.
type recurringOrderNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

// orderPlacer is the subset of OrderService the cron needs. Decoupling
// via interface keeps the test surface minimal and avoids importing the
// full OrderService transitively.
type orderPlacer interface {
	PlaceMarketOrder(ctx context.Context, in PlaceMarketInput) error
}

// PlaceMarketInput is the wire-shape OrderPlacer expects. The cron
// fills it from the RecurringOrder template.
type PlaceMarketInput struct {
	OwnerType model.OwnerType
	OwnerID   *uint64
	ListingID uint64
	AccountID uint64
	Side      string
	Quantity  int64
}

type RecurringOrderService struct {
	repo        *repository.RecurringOrderRepository
	listingRepo ListingRepo
	placer      orderPlacer
	notifier    recurringOrderNotifier
}

func NewRecurringOrderService(repo *repository.RecurringOrderRepository, listings ListingRepo, placer orderPlacer, notifier recurringOrderNotifier) *RecurringOrderService {
	s := &RecurringOrderService{repo: repo, listingRepo: listings, placer: placer}
	if notifier != nil {
		s.notifier = notifier
	}
	return s
}

var (
	ErrRecurringOrderListingNotFound = svcerr.New(codes.NotFound, "listing not found")
	ErrRecurringOrderNotFound        = svcerr.New(codes.NotFound, "recurring order not found")
)

func (s *RecurringOrderService) Create(row *model.RecurringOrder) error {
	if _, err := s.listingRepo.GetByID(row.ListingID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRecurringOrderListingNotFound
		}
		return err
	}
	if row.NextRun.IsZero() {
		// Compute the first execution from StartDate.
		row.NextRun = row.AdvanceNextRun(row.StartDate.Add(-time.Second))
	}
	return s.repo.Create(row)
}

func (s *RecurringOrderService) Get(id uint64, ownerType model.OwnerType, ownerID *uint64) (*model.RecurringOrder, error) {
	r, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRecurringOrderNotFound
		}
		return nil, err
	}
	if r.OwnerType != ownerType || !ownerIDEqual(r.OwnerID, ownerID) {
		return nil, ErrRecurringOrderNotFound
	}
	return r, nil
}

func (s *RecurringOrderService) Pause(id uint64, ownerType model.OwnerType, ownerID *uint64) error {
	return s.transition(id, ownerType, ownerID, model.RecurringOrderStatusActive, model.RecurringOrderStatusPaused)
}

func (s *RecurringOrderService) Resume(id uint64, ownerType model.OwnerType, ownerID *uint64) error {
	return s.transition(id, ownerType, ownerID, model.RecurringOrderStatusPaused, model.RecurringOrderStatusActive)
}

func (s *RecurringOrderService) Cancel(id uint64, ownerType model.OwnerType, ownerID *uint64) error {
	r, err := s.Get(id, ownerType, ownerID)
	if err != nil {
		return err
	}
	r.Status = model.RecurringOrderStatusCancelled
	return s.repo.Save(r)
}

func (s *RecurringOrderService) transition(id uint64, ownerType model.OwnerType, ownerID *uint64, from, to string) error {
	r, err := s.Get(id, ownerType, ownerID)
	if err != nil {
		return err
	}
	if r.Status != from {
		return svcerr.New(codes.FailedPrecondition, "recurring order is not in "+from+" status")
	}
	r.Status = to
	return s.repo.Save(r)
}

func (s *RecurringOrderService) ListMy(ownerType model.OwnerType, ownerID *uint64) ([]model.RecurringOrder, error) {
	return s.repo.ListByOwner(ownerType, ownerID)
}

// RunDue is the cron entrypoint. Iterates due rows, places one Market
// order per row via the placer, then advances NextRun. Insufficient-
// funds (or any non-fatal placement error) skips the run, notifies the
// owner, and still advances NextRun so the template doesn't get stuck.
// Placer-nil short-circuits to a no-op so callers can wire the cron
// before the OrderService has finished initialising.
func (s *RecurringOrderService) RunDue(ctx context.Context, now time.Time) {
	if s.placer == nil {
		return
	}
	rows, err := s.repo.ListDue(now)
	if err != nil {
		log.Printf("WARN: recurring-order ListDue failed: %v", err)
		return
	}
	for i := range rows {
		row := &rows[i]
		s.runOne(ctx, row, now)
	}
}

func (s *RecurringOrderService) runOne(ctx context.Context, row *model.RecurringOrder, now time.Time) {
	// If end_date has passed, mark finished and skip.
	if row.EndDate != nil && !row.EndDate.After(now) {
		row.Status = model.RecurringOrderStatusFinished
		if err := s.repo.Save(row); err != nil {
			log.Printf("WARN: recurring-order finish save failed for %d: %v", row.ID, err)
		}
		return
	}
	err := s.placer.PlaceMarketOrder(ctx, PlaceMarketInput{
		OwnerType: row.OwnerType,
		OwnerID:   row.OwnerID,
		ListingID: row.ListingID,
		AccountID: row.AccountID,
		Side:      row.Side,
		Quantity:  row.Quantity,
	})
	notifyType := "RECURRING_ORDER_EXECUTED"
	data := map[string]string{
		"listing_id": kafkaUint(row.ListingID),
		"quantity":   kafkaUint(uint64(row.Quantity)),
		"side":       row.Side,
	}
	if err != nil {
		notifyType = "RECURRING_ORDER_SKIPPED"
		data["reason"] = err.Error()
	}
	s.notify(ctx, row, notifyType, data)
	stamped := now
	row.LastRun = &stamped
	row.NextRun = row.AdvanceNextRun(now)
	if err := s.repo.Save(row); err != nil {
		log.Printf("WARN: recurring-order save after tick failed for %d: %v", row.ID, err)
	}
}

func (s *RecurringOrderService) notify(ctx context.Context, row *model.RecurringOrder, notifyType string, data map[string]string) {
	if s.notifier == nil || row.OwnerID == nil {
		return
	}
	_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  *row.OwnerID,
		Type:    notifyType,
		Data:    data,
		RefType: "recurring_order",
		RefID:   row.ID,
	})
}
