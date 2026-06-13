package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/service"
	kafkago "github.com/segmentio/kafka-go"
)

// watchlistNotifCreator is the minimal DB write interface used by
// WatchlistAlertConsumer. Narrow so tests can inject a stub.
type watchlistNotifCreator interface {
	// CreateWithIdempotency creates the general notification only if no
	// notification with the same idempotency_key already exists for the user.
	// Returns (created, error) — created=false means the row already existed
	// (dedup hit, not an error).
	CreateWithIdempotency(n *model.GeneralNotification, idempotencyKey string) (bool, error)
}

// WatchlistAlertConsumer reads from the notification.watchlist-alert Kafka
// topic and creates a GeneralNotification for each user whose watchlisted
// ticker moved more than ±5% on a given day. Consumer-side idempotency is
// enforced via the message's IdempotencyKey field.
type WatchlistAlertConsumer struct {
	reader    *kafkago.Reader
	notifRepo watchlistNotifCreator
	templates templateRenderer
	dlq       DeadLetterWriter
	dedup     Deduper
}

// NewWatchlistAlertConsumer creates a consumer backed by a real Kafka reader.
func NewWatchlistAlertConsumer(
	brokers string,
	notifRepo *repository.GeneralNotificationRepository,
	templateSvc *service.TemplateService,
	dlq DeadLetterWriter,
	dedup Deduper,
) *WatchlistAlertConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicWatchlistAlert,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &WatchlistAlertConsumer{
		reader:    reader,
		notifRepo: &watchlistNotifCreatorAdapter{repo: notifRepo},
		templates: templateSvc,
		dlq:       dlq,
		dedup:     dedup,
	}
}

// newWatchlistAlertConsumerForTest constructs a WatchlistAlertConsumer with
// no Kafka reader — tests call handleMessage directly.
func newWatchlistAlertConsumerForTest(repo watchlistNotifCreator, r templateRenderer) *WatchlistAlertConsumer {
	return &WatchlistAlertConsumer{notifRepo: repo, templates: r}
}

func (c *WatchlistAlertConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "watchlist_alert", c.reader, c.dlq, c.dedup, c.handleMessage)
}

func (c *WatchlistAlertConsumer) handleMessage(_ context.Context, data []byte) error {
	var event kafkamsg.WatchlistPriceMoveMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("watchlist alert consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}

	// Render the WATCHLIST_PRICE_MOVE push template.
	subject, body, err := c.templates.Render("WATCHLIST_PRICE_MOVE", "push", map[string]string{
		"ticker":        event.Ticker,
		"percent_move":  event.PercentMove,
		"current_price": event.CurrentPrice,
	})
	if err != nil {
		log.Printf("watchlist alert consumer: render failed for user %d ticker %s (not retryable): %v", event.UserID, event.Ticker, err)
		return nil
	}

	notif := &model.GeneralNotification{
		UserID:  event.UserID,
		Type:    "WATCHLIST_PRICE_MOVE",
		Title:   subject,
		Message: body,
		RefType: "listing",
	}
	created, err := c.notifRepo.CreateWithIdempotency(notif, event.IdempotencyKey)
	if err != nil {
		return err // transient DB failure — retry then dead-letter (idempotent, safe)
	}
	if !created {
		log.Printf("watchlist alert consumer: dedup hit — already notified user %d for %s (key=%s)", event.UserID, event.Ticker, event.IdempotencyKey)
	}
	return nil
}

func (c *WatchlistAlertConsumer) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

// watchlistNotifCreatorAdapter adapts *repository.GeneralNotificationRepository
// to watchlistNotifCreator using an idempotency_key column lookup.
// The general_notifications table gains an optional idempotency_key column
// via AutoMigrate when this consumer is wired in.
type watchlistNotifCreatorAdapter struct {
	repo *repository.GeneralNotificationRepository
}

func (a *watchlistNotifCreatorAdapter) CreateWithIdempotency(
	n *model.GeneralNotification,
	idempotencyKey string,
) (bool, error) {
	return a.repo.CreateWithIdempotency(n, idempotencyKey)
}
