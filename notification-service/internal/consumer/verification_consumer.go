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

// genericPublisher is the minimal Producer subset used by VerificationConsumer.
type genericPublisher interface {
	Publish(ctx context.Context, topic string, msg interface{}) error
}

// inboxItemCreator is the minimal MobileInboxRepository subset used here.
type inboxItemCreator interface {
	Create(item *model.MobileInboxItem) error
}

type VerificationConsumer struct {
	reader    *kafkago.Reader
	sender    emailDispatcher
	producer  genericPublisher
	inboxRepo inboxItemCreator
	templates templateRenderer
	dlq       DeadLetterWriter
	dedup     Deduper
}

func NewVerificationConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, inboxRepo *repository.MobileInboxRepository, templateSvc *svc.TemplateService, dlq DeadLetterWriter, dedup Deduper) *VerificationConsumer {
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
		templates: templateSvc,
		dlq:       dlq,
		dedup:     dedup,
	}
}

// newVerificationConsumerForTest constructs a consumer with mocks; no reader.
func newVerificationConsumerForTest(s emailDispatcher, p genericPublisher, repo inboxItemCreator, r templateRenderer) *VerificationConsumer {
	return &VerificationConsumer{sender: s, producer: p, inboxRepo: repo, templates: r}
}

func (c *VerificationConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "verification", c.reader, c.dlq, c.dedup, c.handleMessage)
}

func (c *VerificationConsumer) handleMessage(ctx context.Context, data []byte) error {
	var event kafkamsg.VerificationChallengeCreatedMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("verification consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}

	switch event.DeliveryChannel {
	case "email":
		return c.handleEmailDelivery(event)
	case "mobile":
		return c.handleMobileDelivery(ctx, event)
	default:
		log.Printf("verification consumer: unknown delivery channel: %s", event.DeliveryChannel)
		return nil
	}
}

func (c *VerificationConsumer) handleEmailDelivery(event kafkamsg.VerificationChallengeCreatedMessage) error {
	// Extract code from display_data JSON
	var displayData map[string]interface{}
	code := ""
	if err := json.Unmarshal([]byte(event.DisplayData), &displayData); err == nil {
		if c, ok := displayData["code"].(string); ok {
			code = c
		}
	}

	subject, body, renderErr := c.templates.Render(string(kafkamsg.EmailTypeTransactionVerify), "email", map[string]string{
		"verification_code": code,
		"expires_in":        "5 minutes",
	})
	if renderErr != nil {
		log.Printf("verification consumer: render error (not retryable): %v", renderErr)
		return nil
	}
	// We need the user's email — for email delivery, the verification-service
	// should have set it. We extract from display_data if available.
	email := ""
	if e, ok := displayData["email"].(string); ok {
		email = e
	}
	if email == "" {
		log.Printf("verification consumer: email delivery requested but no email in display_data for challenge %d", event.ChallengeID)
		return nil // missing data — retrying won't help
	}
	if err := c.sender.Send(email, subject, body); err != nil {
		return err // transient SMTP failure — retry then dead-letter
	}
	return nil
}

func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) error {
	expiresAt, err := time.Parse(time.RFC3339, event.ExpiresAt)
	if err != nil {
		log.Printf("verification consumer: invalid expires_at (not retryable): %v", err)
		return nil
	}

	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(event.DisplayData),
		ExpiresAt:   expiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		return err // transient DB failure — retry (atomic insert, safe to re-run)
	}
	// The inbox row is the source of truth — the mobile app polls it via
	// GET /api/v3/mobile/verifications/pending (GetPendingMobileItems). The old
	// best-effort WebSocket "push nudge" (notification.mobile-push) was removed
	// 2026-06-11 along with the unused api-gateway WebSocket handler.
	return nil
}

func (c *VerificationConsumer) Close() error {
	return c.reader.Close()
}
