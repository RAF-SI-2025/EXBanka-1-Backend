package kafka

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
)

// Event represents a captured Kafka event.
type Event struct {
	Topic     string
	Key       string
	Value     map[string]interface{}
	RawValue  []byte
	Timestamp time.Time
}

// EventListener subscribes to Kafka topics and collects events for assertion.
type EventListener struct {
	brokers string
	events  []Event
	mu      sync.Mutex
	readers []*kafkalib.Reader
	cancel  context.CancelFunc
}

// AllTopics lists every topic the test app should monitor.
var AllTopics = []string{
	"user.employee-created",
	"user.employee-updated",
	"user.employee-limits-updated",
	"user.limit-template-created",
	"user.limit-template-updated",
	"user.limit-template-deleted",
	"client.created",
	"client.updated",
	"client.limits-updated",
	"auth.account-status-changed",
	"notification.send-email",
	"notification.email-sent",
	"account.created",
	"account.status-changed",
	"account.name-updated",
	"account.limits-updated",
	"account.maintenance-charged",
	"account.spending-reset",
	"card.created",
	"card.status-changed",
	"card.temporary-blocked",
	"card.virtual-card-created",
	"transaction.payment-created",
	"transaction.payment-completed",
	"transaction.payment-failed",
	"transaction.transfer-created",
	"transaction.transfer-completed",
	"transaction.transfer-failed",
	"credit.loan-requested",
	"credit.loan-approved",
	"credit.loan-rejected",
	"credit.installment-collected",
	"credit.installment-failed",
	"credit.variable-rate-adjusted",
	"credit.late-penalty-applied",
	"auth.dead-letter",
}

// NewEventListener creates a listener (not yet started).
func NewEventListener(brokers string) *EventListener {
	return &EventListener{
		brokers: brokers,
		events:  make([]Event, 0),
	}
}

// Start begins consuming from all topics.
func (el *EventListener) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	el.cancel = cancel

	for _, topic := range AllTopics {
		r := kafkalib.NewReader(kafkalib.ReaderConfig{
			Brokers:  []string{el.brokers},
			Topic:    topic,
			GroupID:  "test-app-listener-" + topic,
			MinBytes: 1,
			MaxBytes: 1e6,
			MaxWait:  500 * time.Millisecond,
		})
		el.readers = append(el.readers, r)
		go el.consume(ctx, r, topic)
	}
}

func (el *EventListener) consume(ctx context.Context, r *kafkalib.Reader, topic string) {
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[kafka listener] error reading from topic %s: %v", topic, err)
			continue
		}

		evt := Event{
			Topic:     topic,
			Key:       string(msg.Key),
			RawValue:  msg.Value,
			Timestamp: msg.Time,
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(msg.Value, &parsed); err == nil {
			evt.Value = parsed
		}

		el.mu.Lock()
		el.events = append(el.events, evt)
		el.mu.Unlock()
	}
}

// Stop stops all consumers.
func (el *EventListener) Stop() {
	if el.cancel != nil {
		el.cancel()
	}
	for _, r := range el.readers {
		_ = r.Close()
	}
}

// Clear removes all collected events.
func (el *EventListener) Clear() {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.events = el.events[:0]
}

// Events returns a copy of all collected events.
func (el *EventListener) Events() []Event {
	el.mu.Lock()
	defer el.mu.Unlock()
	cp := make([]Event, len(el.events))
	copy(cp, el.events)
	return cp
}

// EventsByTopic returns events filtered by topic.
func (el *EventListener) EventsByTopic(topic string) []Event {
	el.mu.Lock()
	defer el.mu.Unlock()
	var result []Event
	for _, e := range el.events {
		if e.Topic == topic {
			result = append(result, e)
		}
	}
	return result
}

// WaitForEvent waits up to timeout for an event on the given topic matching the predicate.
func (el *EventListener) WaitForEvent(topic string, timeout time.Duration, match func(Event) bool) (*Event, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		el.mu.Lock()
		for _, e := range el.events {
			if e.Topic == topic && (match == nil || match(e)) {
				el.mu.Unlock()
				return &e, true
			}
		}
		el.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	return nil, false
}
