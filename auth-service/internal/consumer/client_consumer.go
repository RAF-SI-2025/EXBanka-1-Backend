package consumer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/exbanka/auth-service/internal/service"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// ClientConsumer listens for client.created events and creates activation tokens.
//
// Contract: consumes topic "client.created" (TopicClientCreated).
// Message type: ClientCreatedMessage{ClientID uint64, Email, FirstName, LastName string}.
// Note: ClientID is uint64 in the message; cast to int64 for CreateAccountAndActivationToken.
// Action: calls CreateAccountAndActivationToken(ctx, int64(ClientID), Email, FirstName, "client")
// which creates an Account (status=pending) and an ActivationToken, then publishes
// a notification.send-email event (type=ACTIVATION) for the notification-service to deliver.
type ClientConsumer struct {
	reader    *kafka.Reader
	authSvc   *service.AuthService
	dlqWriter *kafka.Writer
}

func NewClientConsumer(brokers string, authSvc *service.AuthService) *ClientConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		Topic:   kafkamsg.TopicClientCreated,
		GroupID: "auth-service-client-consumer",
	})
	dlq := &kafka.Writer{
		Addr:  kafka.TCP(brokers),
		Topic: kafkamsg.TopicAuthDeadLetter,
	}
	return &ClientConsumer{reader: r, authSvc: authSvc, dlqWriter: dlq}
}

// Start begins consuming messages in a background goroutine.
func (c *ClientConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return // context cancelled, shutting down
				}
				log.Printf("client consumer read error: %v", err)
				continue
			}

			var evt kafkamsg.ClientCreatedMessage
			if err := json.Unmarshal(msg.Value, &evt); err != nil {
				log.Printf("client consumer unmarshal error: %v", err)
				continue
			}

			var lastErr error
			for attempt := 1; attempt <= maxRetries; attempt++ {
				lastErr = c.authSvc.CreateAccountAndActivationToken(ctx, int64(evt.ClientID), evt.Email, evt.FirstName, "client")
				if lastErr == nil {
					break
				}
				log.Printf("client consumer: attempt %d/%d failed for client %d: %v", attempt, maxRetries, evt.ClientID, lastErr)
				if attempt < maxRetries {
					// Exponential backoff: 2s, 4s, 8s
					time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				}
			}
			if lastErr != nil {
				log.Printf("client consumer: all retries exhausted for client %d, sending to DLQ: %v", evt.ClientID, lastErr)
				if dlqErr := c.dlqWriter.WriteMessages(ctx, kafka.Message{Value: msg.Value}); dlqErr != nil {
					log.Printf("client consumer: DLQ write failed for client %d (payload: %s): %v", evt.ClientID, string(msg.Value), dlqErr)
				}
			}
		}
	}()
}

// Close shuts down the Kafka reader and DLQ writer.
func (c *ClientConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("client consumer close error: %v", err)
	}
	if err := c.dlqWriter.Close(); err != nil {
		log.Printf("client consumer DLQ writer close error: %v", err)
	}
}
