package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// priceAlertNotifier publishes the in-app notification when an alert fires.
// Narrow interface so tests can use a recording stub.
type priceAlertNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

type PriceAlertService struct {
	repo        *repository.PriceAlertRepository
	listingRepo ListingRepo
	notifier    priceAlertNotifier
}

func NewPriceAlertService(repo *repository.PriceAlertRepository, listings ListingRepo, notifier priceAlertNotifier) *PriceAlertService {
	s := &PriceAlertService{repo: repo, listingRepo: listings}
	// Typed-nil guard so callers can pass a (concrete *kafka.Producer)(nil)
	// without panicking in EvaluateForListing.
	if notifier != nil {
		s.notifier = notifier
	}
	return s
}

var (
	ErrPriceAlertListingNotFound = svcerr.New(codes.NotFound, "listing not found")
	ErrPriceAlertNotFound        = svcerr.New(codes.NotFound, "price alert not found")
)

// Create validates the listing exists, then persists the alert.
func (s *PriceAlertService) Create(a *model.PriceAlert) error {
	if _, err := s.listingRepo.GetByID(a.ListingID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPriceAlertListingNotFound
		}
		return err
	}
	return s.repo.Create(a)
}

func (s *PriceAlertService) Get(id uint64, ownerType model.OwnerType, ownerID *uint64) (*model.PriceAlert, error) {
	a, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPriceAlertNotFound
		}
		return nil, err
	}
	if a.OwnerType != ownerType || !ownerIDEqual(a.OwnerID, ownerID) {
		return nil, ErrPriceAlertNotFound
	}
	return a, nil
}

func (s *PriceAlertService) Update(a *model.PriceAlert) error {
	return s.repo.Save(a)
}

func (s *PriceAlertService) Delete(id uint64, ownerType model.OwnerType, ownerID *uint64) error {
	ok, err := s.repo.Delete(id, ownerType, ownerID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPriceAlertNotFound
	}
	return nil
}

func (s *PriceAlertService) ListMy(ownerType model.OwnerType, ownerID *uint64) ([]model.PriceAlert, error) {
	return s.repo.ListByOwner(ownerType, ownerID)
}

// EvaluateForListing is the reactive entrypoint called from the price-refresh
// path. It walks active alerts on listingID, fires notifications for each
// match, and either deactivates single-shot alerts or stamps LastTriggered
// for recurring ones (subject to Cooldown). Best-effort: emit failures log
// but do not propagate (callers treat alerts as observability, not
// transactional correctness).
func (s *PriceAlertService) EvaluateForListing(ctx context.Context, listingID uint64, latestPrice, dailyChange decimal.Decimal) {
	alerts, err := s.repo.ListActiveByListing(listingID)
	if err != nil {
		log.Printf("WARN: price-alert eval list failed: %v", err)
		return
	}
	if len(alerts) == 0 {
		return
	}
	listing, err := s.listingRepo.GetByID(listingID)
	if err != nil {
		log.Printf("WARN: price-alert eval listing lookup failed: %v", err)
		return
	}
	dailyChangePct := dailyChangePercent(latestPrice, dailyChange)
	now := time.Now().UTC()
	for i := range alerts {
		a := &alerts[i]
		if !alertMatches(a, latestPrice, dailyChangePct) {
			continue
		}
		// Cooldown gate for recurring alerts; single-shot always fires.
		if a.IsRecurring && a.LastTriggered != nil {
			if now.Sub(*a.LastTriggered) < time.Duration(a.Cooldown)*time.Second {
				continue
			}
		}
		s.fire(ctx, a, listing, latestPrice, dailyChangePct)
		// Persist new state.
		if !a.IsRecurring {
			a.Active = false
		}
		stamped := now
		a.LastTriggered = &stamped
		if err := s.repo.Save(a); err != nil {
			log.Printf("WARN: price-alert state-save failed for alert %d: %v", a.ID, err)
		}
	}
}

func alertMatches(a *model.PriceAlert, price, dailyChangePct decimal.Decimal) bool {
	switch a.Condition {
	case model.PriceAlertConditionGTE:
		return price.GreaterThanOrEqual(a.Threshold)
	case model.PriceAlertConditionLTE:
		return price.LessThanOrEqual(a.Threshold)
	case model.PriceAlertConditionDailyChangePctGTE:
		return dailyChangePct.GreaterThanOrEqual(a.Threshold)
	case model.PriceAlertConditionDailyChangePctLTE:
		return dailyChangePct.LessThanOrEqual(a.Threshold)
	}
	return false
}

func (s *PriceAlertService) fire(ctx context.Context, a *model.PriceAlert, listing *model.Listing, price, pct decimal.Decimal) {
	if s.notifier == nil || a.OwnerID == nil {
		return // bank-owner alerts have no human recipient; skip
	}
	_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID: *a.OwnerID,
		Type:   "PRICE_ALERT_TRIGGERED",
		Data: map[string]string{
			"listing_id":            kafkaUint(a.ListingID),
			"security_type":         listing.SecurityType,
			"price":                 price.StringFixed(4),
			"condition":             string(a.Condition),
			"threshold":             a.Threshold.StringFixed(4),
			"daily_change_percent":  pct.StringFixed(4),
			"email_too":             boolStr(a.EmailToo),
		},
		RefType: "listing",
		RefID:   a.ListingID,
	})
}

func kafkaUint(v uint64) string {
	// inlined fmt avoids fmt import — kafka payloads use bare base-10
	// integers throughout the codebase.
	return decimal.NewFromUint64(v).String()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
