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

type EmailConsumer struct {
	reader   *kafkago.Reader
	sender   *sender.EmailSender
	producer *kafkaprod.Producer
}

func NewEmailConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer) *EmailConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicSendEmail,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &EmailConsumer{
		reader:   reader,
		sender:   emailSender,
		producer: producer,
	}
}

func (c *EmailConsumer) Start(ctx context.Context) {
	log.Println("email consumer started, listening on", kafkamsg.TopicSendEmail)
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("email consumer shutting down")
				return
			}
			log.Printf("error reading kafka message: %v", err)
			continue
		}
		c.handleMessage(ctx, msg.Value)
	}
}

func (c *EmailConsumer) handleMessage(ctx context.Context, data []byte) {
	var emailMsg kafkamsg.SendEmailMessage
	if err := json.Unmarshal(data, &emailMsg); err != nil {
		log.Printf("error unmarshaling email message: %v", err)
		return
	}
	log.Printf("[DEV] email queued | type=%s to=%s data=%v", emailMsg.EmailType, emailMsg.To, emailMsg.Data)

	if isTestAddress(emailMsg.To) {
		log.Printf("[TEST] skipping send to %s | type=%s data=%v", emailMsg.To, emailMsg.EmailType, emailMsg.Data)
		if pubErr := c.producer.PublishEmailSent(ctx, kafkamsg.EmailSentMessage{
			To:        emailMsg.To,
			EmailType: emailMsg.EmailType,
			Success:   true,
		}); pubErr != nil {
			log.Printf("failed to publish email-sent confirmation: %v", pubErr)
		}
		return
	}

	subject, body := sender.BuildEmail(emailMsg.EmailType, emailMsg.Data)
	sendStart := time.Now()
	err := c.sender.Send(emailMsg.To, subject, body)
	svc.NotificationEmailSendDuration.Observe(time.Since(sendStart).Seconds())

	confirmation := kafkamsg.EmailSentMessage{
		To:        emailMsg.To,
		EmailType: emailMsg.EmailType,
		Success:   err == nil,
	}
	if err != nil {
		svc.NotificationEmailsSentTotal.WithLabelValues("failure").Inc()
		log.Printf("failed to send email to %s: %v", emailMsg.To, err)
		confirmation.Error = err.Error()
	} else {
		svc.NotificationEmailsSentTotal.WithLabelValues("success").Inc()
		log.Printf("email sent successfully to %s (type: %s)", emailMsg.To, emailMsg.EmailType)
	}

	if pubErr := c.producer.PublishEmailSent(ctx, confirmation); pubErr != nil {
		log.Printf("failed to publish email-sent confirmation: %v", pubErr)
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
