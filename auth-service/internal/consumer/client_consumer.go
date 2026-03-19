package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/auth-service/internal/service"
)

// ClientConsumer listens for client.created events and creates activation tokens.
type ClientConsumer struct {
	reader  *kafka.Reader
	authSvc *service.AuthService
}

func NewClientConsumer(brokers string, authSvc *service.AuthService) *ClientConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		Topic:   kafkamsg.TopicClientCreated,
		GroupID: "auth-service-client-consumer",
	})
	return &ClientConsumer{reader: r, authSvc: authSvc}
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

			if err := c.authSvc.CreateActivationToken(ctx, int64(evt.ClientID), evt.Email, evt.FirstName, "client"); err != nil {
				log.Printf("failed to create activation token for client %d: %v", evt.ClientID, err)
			}
		}
	}()
}

// Close shuts down the Kafka reader.
func (c *ClientConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("client consumer close error: %v", err)
	}
}
