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

// generalNotificationCreator is the minimal subset of
// *repository.GeneralNotificationRepository used by GeneralNotificationConsumer.
type generalNotificationCreator interface {
	Create(n *model.GeneralNotification) error
}

type GeneralNotificationConsumer struct {
	reader    *kafkago.Reader
	notifRepo generalNotificationCreator
	templates templateRenderer
	dlq       DeadLetterWriter
	dedup     Deduper
}

func NewGeneralNotificationConsumer(brokers string, notifRepo *repository.GeneralNotificationRepository, templateSvc *service.TemplateService, dlq DeadLetterWriter, dedup Deduper) *GeneralNotificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicGeneralNotification,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &GeneralNotificationConsumer{reader: reader, notifRepo: notifRepo, templates: templateSvc, dlq: dlq, dedup: dedup}
}

// newGeneralNotificationConsumerForTest constructs a consumer without a Kafka reader.
func newGeneralNotificationConsumerForTest(repo generalNotificationCreator, r templateRenderer) *GeneralNotificationConsumer {
	return &GeneralNotificationConsumer{notifRepo: repo, templates: r}
}

func (c *GeneralNotificationConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "general_notification", c.reader, c.dlq, c.dedup, c.handleMessage)
}

func (c *GeneralNotificationConsumer) handleMessage(_ context.Context, data []byte) error {
	var event kafkamsg.GeneralNotificationMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("general notification consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}

	title, body := event.Title, event.Message
	if len(event.Data) > 0 {
		subject, rendered, err := c.templates.Render(event.Type, "push", event.Data)
		if err != nil {
			log.Printf("general notification consumer: render %q failed, dropping (not retryable): %v", event.Type, err)
			return nil
		}
		title, body = subject, rendered
	}

	notif := &model.GeneralNotification{
		UserID:     event.UserID,
		SystemType: event.SystemType, // "" (legacy/client) or "employee"; read scoping treats empty as client
		Type:       event.Type,
		Title:      title,
		Message:    body,
		RefType:    event.RefType,
		RefID:      event.RefID,
	}
	if err := c.notifRepo.Create(notif); err != nil {
		return err // transient DB failure — retry then dead-letter
	}
	service.NotificationGeneralCreatedTotal.Inc()
	return nil
}

func (c *GeneralNotificationConsumer) Close() error {
	return c.reader.Close()
}
