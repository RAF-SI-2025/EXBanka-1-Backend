package consumer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	kafka "github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/google/uuid"
)

// SupervisorDemotedConsumer consumes user.supervisor-demoted events and
// reassigns the demoted supervisor's funds to the admin who demoted them.
// On success it publishes stock.funds-reassigned for downstream observability.
type SupervisorDemotedConsumer struct {
	reader   *kafka.Reader
	fundRepo *repository.FundRepository
	producer *kafkaprod.Producer
}

func NewSupervisorDemotedConsumer(brokers string, fundRepo *repository.FundRepository, producer *kafkaprod.Producer) *SupervisorDemotedConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokers},
		Topic:       kafkamsg.TopicUserSupervisorDemoted,
		GroupID:     "stock-service-supervisor-demoted",
		StartOffset: kafka.FirstOffset,
	})
	return &SupervisorDemotedConsumer{reader: r, fundRepo: fundRepo, producer: producer}
}

// Start begins consuming messages in a background goroutine.
func (c *SupervisorDemotedConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("supervisor-demoted consumer read error: %v", err)
				continue
			}
			var evt kafkamsg.UserSupervisorDemotedMessage
			if err := json.Unmarshal(msg.Value, &evt); err != nil {
				log.Printf("supervisor-demoted unmarshal: %v", err)
				continue
			}
			ids, err := c.fundRepo.ReassignManager(evt.SupervisorID, evt.AdminID)
			if err != nil {
				log.Printf("supervisor-demoted reassign supervisor=%d admin=%d: %v", evt.SupervisorID, evt.AdminID, err)
				continue
			}
			if len(ids) == 0 {
				continue
			}
			if c.producer != nil {
				payload := kafkamsg.StockFundsReassignedMessage{
					MessageID:    uuid.NewString(),
					OccurredAt:   time.Now().UTC().Format(time.RFC3339),
					SupervisorID: evt.SupervisorID,
					AdminID:      evt.AdminID,
					FundIDs:      ids,
				}
				if data, err := json.Marshal(payload); err == nil {
					_ = c.producer.PublishRaw(ctx, kafkamsg.TopicStockFundsReassigned, data)
				}
			}
		}
	}()
}

// Close releases the underlying Kafka reader. Idempotent.
func (c *SupervisorDemotedConsumer) Close() error {
	return c.reader.Close()
}
