package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/auth-service/internal/service"
)

// EmployeeConsumer listens for user.employee-created events and creates activation tokens.
type EmployeeConsumer struct {
	reader  *kafka.Reader
	authSvc *service.AuthService
}

func NewEmployeeConsumer(brokers string, authSvc *service.AuthService) *EmployeeConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		Topic:   kafkamsg.TopicEmployeeCreated,
		GroupID: "auth-service-employee-consumer",
	})
	return &EmployeeConsumer{reader: r, authSvc: authSvc}
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

			if err := c.authSvc.CreateAccountAndActivationToken(ctx, evt.EmployeeID, evt.Email, evt.FirstName, "employee"); err != nil {
				log.Printf("failed to create activation token for employee %d: %v", evt.EmployeeID, err)
			}
		}
	}()
}

// Close shuts down the Kafka reader.
func (c *EmployeeConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("employee consumer close error: %v", err)
	}
}
