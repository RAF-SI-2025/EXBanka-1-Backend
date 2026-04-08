package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	svc "github.com/exbanka/notification-service/internal/service"
	kafkago "github.com/segmentio/kafka-go"
	"gorm.io/datatypes"
)

type VerificationConsumer struct {
	reader    *kafkago.Reader
	sender    *sender.EmailSender
	producer  *kafkaprod.Producer
	inboxRepo *repository.MobileInboxRepository
}

func NewVerificationConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, inboxRepo *repository.MobileInboxRepository) *VerificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicVerificationChallengeCreated,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &VerificationConsumer{
		reader:    reader,
		sender:    emailSender,
		producer:  producer,
		inboxRepo: inboxRepo,
	}
}

func (c *VerificationConsumer) Start(ctx context.Context) {
	go func() {
		log.Println("verification consumer started, listening on", kafkamsg.TopicVerificationChallengeCreated)
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					log.Println("verification consumer shutting down")
					return
				}
				log.Printf("verification consumer: read error: %v", err)
				continue
			}
			c.handleMessage(ctx, msg.Value)
		}
	}()
}

func (c *VerificationConsumer) handleMessage(ctx context.Context, data []byte) {
	var event kafkamsg.VerificationChallengeCreatedMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("verification consumer: unmarshal error: %v", err)
		return
	}

	switch event.DeliveryChannel {
	case "email":
		c.handleEmailDelivery(event)
	case "mobile":
		c.handleMobileDelivery(ctx, event)
	default:
		log.Printf("verification consumer: unknown delivery channel: %s", event.DeliveryChannel)
	}
}

func (c *VerificationConsumer) handleEmailDelivery(event kafkamsg.VerificationChallengeCreatedMessage) {
	// Extract code from display_data JSON
	var displayData map[string]interface{}
	code := ""
	if err := json.Unmarshal([]byte(event.DisplayData), &displayData); err == nil {
		if c, ok := displayData["code"].(string); ok {
			code = c
		}
	}

	subject, body := sender.BuildEmail(kafkamsg.EmailTypeTransactionVerify, map[string]string{
		"verification_code": code,
		"expires_in":        "5 minutes",
	})
	// We need the user's email — for email delivery, the verification-service
	// should have set it. We extract from display_data if available.
	email := ""
	if e, ok := displayData["email"].(string); ok {
		email = e
	}
	if email == "" {
		log.Printf("verification consumer: email delivery requested but no email in display_data for challenge %d", event.ChallengeID)
		return
	}
	if err := c.sender.Send(email, subject, body); err != nil {
		log.Printf("verification consumer: email send error: %v", err)
	}
}

func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	expiresAt, err := time.Parse(time.RFC3339, event.ExpiresAt)
	if err != nil {
		log.Printf("verification consumer: invalid expires_at: %v", err)
		return
	}

	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		DeviceID:    event.DeviceID, // may be empty — mobile app queries by user_id
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(event.DisplayData),
		ExpiresAt:   expiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		log.Printf("verification consumer: inbox create error: %v", err)
		return
	}

	// Publish to mobile-push topic for WebSocket delivery
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"challenge_id": event.ChallengeID,
		"method":       event.Method,
		"display_data": event.DisplayData,
		"expires_at":   event.ExpiresAt,
	})
	pushMsg := kafkamsg.MobilePushMessage{
		UserID:   event.UserID,
		DeviceID: event.DeviceID,
		Type:     "verification_challenge",
		Payload:  string(payloadJSON),
	}
	if err := c.producer.Publish(ctx, kafkamsg.TopicMobilePush, pushMsg); err != nil {
		log.Printf("verification consumer: mobile push publish error: %v", err)
	} else {
		svc.NotificationMobilePushTotal.Inc()
	}
}

func (c *VerificationConsumer) Close() error {
	return c.reader.Close()
}
