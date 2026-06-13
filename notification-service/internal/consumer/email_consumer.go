package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/sender"
	svc "github.com/exbanka/notification-service/internal/service"
	kafkago "github.com/segmentio/kafka-go"
)

// emailDispatcher is the minimal subset of *sender.EmailSender used by EmailConsumer.
type emailDispatcher interface {
	Send(to, subject, body string) error
}

// emailSentPublisher is the minimal subset of *kafkaprod.Producer used by EmailConsumer.
type emailSentPublisher interface {
	PublishEmailSent(ctx context.Context, msg kafkamsg.EmailSentMessage) error
}

// templateRenderer is the minimal subset of *service.TemplateService used here.
type templateRenderer interface {
	Render(typ, channel string, data map[string]string) (subject, body string, err error)
}

type EmailConsumer struct {
	reader    *kafkago.Reader
	sender    emailDispatcher
	producer  emailSentPublisher
	templates templateRenderer
	dlq       DeadLetterWriter
	dedup     Deduper
}

func NewEmailConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, templateSvc *svc.TemplateService, dlq DeadLetterWriter, dedup Deduper) *EmailConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicSendEmail,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &EmailConsumer{
		reader:    reader,
		sender:    emailSender,
		producer:  producer,
		templates: templateSvc,
		dlq:       dlq,
		dedup:     dedup,
	}
}

// newEmailConsumerForTest constructs an EmailConsumer with no Kafka reader.
// Tests call handleMessage directly.
func newEmailConsumerForTest(d emailDispatcher, p emailSentPublisher, r templateRenderer) *EmailConsumer {
	return &EmailConsumer{sender: d, producer: p, templates: r}
}

func (c *EmailConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "email", c.reader, c.dlq, c.dedup, c.handleMessage)
}

// handleMessage returns an error ONLY for transient failures (SMTP send) so the
// runner retries then dead-letters. Non-retryable failures (malformed payload,
// bad template) are logged and return nil — retrying them would never succeed.
func (c *EmailConsumer) handleMessage(ctx context.Context, data []byte) error {
	var emailMsg kafkamsg.SendEmailMessage
	if err := json.Unmarshal(data, &emailMsg); err != nil {
		log.Printf("email consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}
	log.Printf("[DEV] email queued | type=%s to=%s data=%v", emailMsg.EmailType, emailMsg.To, emailMsg.Data)

	if isTestAddress(emailMsg.To) {
		log.Printf("[TEST] skipping send to %s | type=%s data=%v", emailMsg.To, emailMsg.EmailType, emailMsg.Data)
		c.publishConfirmation(ctx, kafkamsg.EmailSentMessage{To: emailMsg.To, EmailType: emailMsg.EmailType, Success: true})
		return nil
	}

	subject, body, renderErr := c.templates.Render(string(emailMsg.EmailType), "email", emailMsg.Data)
	if renderErr != nil {
		log.Printf("email consumer: render error for %s (not retryable): %v", emailMsg.EmailType, renderErr)
		c.publishConfirmation(ctx, kafkamsg.EmailSentMessage{To: emailMsg.To, EmailType: emailMsg.EmailType, Success: false, Error: renderErr.Error()})
		return nil // bad template — retrying won't help
	}

	sendStart := time.Now()
	err := c.sender.Send(emailMsg.To, subject, body)
	svc.NotificationEmailSendDuration.Observe(time.Since(sendStart).Seconds())
	if err != nil {
		svc.NotificationEmailsSentTotal.WithLabelValues("failure").Inc()
		// Transient SMTP failure — return the error so the runner retries, then
		// dead-letters. The failure confirmation is NOT published per-attempt;
		// the dead-letter record is the durable signal on permanent failure.
		return err
	}

	svc.NotificationEmailsSentTotal.WithLabelValues("success").Inc()
	log.Printf("email sent successfully to %s (type: %s)", emailMsg.To, emailMsg.EmailType)
	c.publishConfirmation(ctx, kafkamsg.EmailSentMessage{To: emailMsg.To, EmailType: emailMsg.EmailType, Success: true})
	return nil
}

func (c *EmailConsumer) publishConfirmation(ctx context.Context, msg kafkamsg.EmailSentMessage) {
	if c.producer == nil {
		return
	}
	if err := c.producer.PublishEmailSent(ctx, msg); err != nil {
		log.Printf("email consumer: failed to publish email-sent confirmation: %v", err)
	}
}

func (c *EmailConsumer) Close() error {
	return c.reader.Close()
}

// isTestAddress returns true for plus-addressed emails (e.g. user+test@domain.com).
// These are treated as test recipients: the email is not sent but the token/code
// is logged to the console and a success confirmation is published.
func isTestAddress(email string) bool {
	at := strings.Index(email, "@")
	return at > 0 && strings.Contains(email[:at], "+test")
}
