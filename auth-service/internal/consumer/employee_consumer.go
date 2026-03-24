package consumer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/auth-service/internal/service"
)

const maxRetries = 3

// EmployeeConsumer listens for user.employee-created events and creates activation tokens.
//
// Contract: consumes topic "user.employee-created" (TopicEmployeeCreated).
// Message type: EmployeeCreatedMessage{EmployeeID int64, Email, FirstName, LastName string, Roles []string}.
// Action: calls CreateAccountAndActivationToken(ctx, EmployeeID, Email, FirstName, "employee")
// which creates an Account (status=pending) and an ActivationToken, then publishes
// a notification.send-email event (type=ACTIVATION) for the notification-service to deliver.
type EmployeeConsumer struct {
	reader    *kafka.Reader
	authSvc   *service.AuthService
	dlqWriter *kafka.Writer
}

func NewEmployeeConsumer(brokers string, authSvc *service.AuthService) *EmployeeConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokers},
		Topic:       kafkamsg.TopicEmployeeCreated,
		GroupID:     "auth-service-employee-consumer",
		StartOffset: kafka.FirstOffset,
	})
	dlq := &kafka.Writer{
		Addr:  kafka.TCP(brokers),
		Topic: kafkamsg.TopicAuthDeadLetter,
	}
	return &EmployeeConsumer{reader: r, authSvc: authSvc, dlqWriter: dlq}
}

// Start begins consuming messages in a background goroutine.
func (c *EmployeeConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return // context cancelled, shutting down
				}
				log.Printf("employee consumer read error: %v", err)
				continue
			}

			var evt kafkamsg.EmployeeCreatedMessage
			if err := json.Unmarshal(msg.Value, &evt); err != nil {
				log.Printf("employee consumer unmarshal error: %v", err)
				continue
			}

			var lastErr error
			for attempt := 1; attempt <= maxRetries; attempt++ {
				lastErr = c.authSvc.CreateAccountAndActivationToken(ctx, evt.EmployeeID, evt.Email, evt.FirstName, "employee")
				if lastErr == nil {
					break
				}
				log.Printf("employee consumer: attempt %d/%d failed for employee %d: %v", attempt, maxRetries, evt.EmployeeID, lastErr)
				if attempt < maxRetries {
					// Exponential backoff: 2s, 4s, 8s
					time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				}
			}
			if lastErr != nil {
				log.Printf("employee consumer: all retries exhausted for employee %d, sending to DLQ: %v", evt.EmployeeID, lastErr)
				if dlqErr := c.dlqWriter.WriteMessages(ctx, kafka.Message{Value: msg.Value}); dlqErr != nil {
					log.Printf("employee consumer: DLQ write failed for employee %d (payload: %s): %v", evt.EmployeeID, string(msg.Value), dlqErr)
				}
			}
		}
	}()
}

// Close shuts down the Kafka reader and DLQ writer.
func (c *EmployeeConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("employee consumer close error: %v", err)
	}
	if err := c.dlqWriter.Close(); err != nil {
		log.Printf("employee consumer DLQ writer close error: %v", err)
	}
}
