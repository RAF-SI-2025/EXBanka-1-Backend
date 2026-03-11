package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/sender"
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

	subject, body := sender.BuildEmail(emailMsg.EmailType, emailMsg.Data)
	err := c.sender.Send(emailMsg.To, subject, body)

	confirmation := kafkamsg.EmailSentMessage{
		To:        emailMsg.To,
		EmailType: emailMsg.EmailType,
		Success:   err == nil,
	}
	if err != nil {
		log.Printf("failed to send email to %s: %v", emailMsg.To, err)
		confirmation.Error = err.Error()
	} else {
		log.Printf("email sent successfully to %s (type: %s)", emailMsg.To, emailMsg.EmailType)
	}

	if pubErr := c.producer.PublishEmailSent(ctx, confirmation); pubErr != nil {
		log.Printf("failed to publish email-sent confirmation: %v", pubErr)
	}
}

func (c *EmailConsumer) Close() error {
	return c.reader.Close()
}
