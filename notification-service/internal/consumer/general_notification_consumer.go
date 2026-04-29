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
}

func NewGeneralNotificationConsumer(brokers string, notifRepo *repository.GeneralNotificationRepository) *GeneralNotificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicGeneralNotification,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &GeneralNotificationConsumer{reader: reader, notifRepo: notifRepo}
}

// newGeneralNotificationConsumerForTest constructs a consumer without a Kafka reader.
func newGeneralNotificationConsumerForTest(repo generalNotificationCreator) *GeneralNotificationConsumer {
	return &GeneralNotificationConsumer{notifRepo: repo}
}

func (c *GeneralNotificationConsumer) Start(ctx context.Context) {
	go func() {
		log.Println("general notification consumer started, listening on", kafkamsg.TopicGeneralNotification)
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					log.Println("general notification consumer shutting down")
					return
				}
				log.Printf("general notification consumer: read error: %v", err)
				continue
			}
			c.handleMessage(msg.Value)
		}
	}()
}

func (c *GeneralNotificationConsumer) handleMessage(data []byte) {
	var event kafkamsg.GeneralNotificationMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("general notification consumer: unmarshal error: %v", err)
		return
	}

	notif := &model.GeneralNotification{
		UserID:  event.UserID,
		Type:    event.Type,
		Title:   event.Title,
		Message: event.Message,
		RefType: event.RefType,
		RefID:   event.RefID,
	}
	if err := c.notifRepo.Create(notif); err != nil {
		log.Printf("general notification consumer: create error: %v", err)
		return
	}
	service.NotificationGeneralCreatedTotal.Inc()
}

func (c *GeneralNotificationConsumer) Close() error {
	return c.reader.Close()
}
