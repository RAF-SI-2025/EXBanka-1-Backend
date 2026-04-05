package testutil

import "sync"

// MockEvent represents a captured Kafka publish.
type MockEvent struct {
	Topic string
	Data  map[string]interface{}
}

// MockKafkaProducer captures published events for test assertions.
// Thread-safe.
type MockKafkaProducer struct {
	mu     sync.Mutex
	events []MockEvent
}

func NewMockKafkaProducer() *MockKafkaProducer {
	return &MockKafkaProducer{}
}

func (p *MockKafkaProducer) Publish(topic string, data map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, MockEvent{Topic: topic, Data: data})
}

func (p *MockKafkaProducer) Events() []MockEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]MockEvent, len(p.events))
	copy(cp, p.events)
	return cp
}

func (p *MockKafkaProducer) EventsByTopic(topic string) []MockEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result []MockEvent
	for _, e := range p.events {
		if e.Topic == topic {
			result = append(result, e)
		}
	}
	return result
}

func (p *MockKafkaProducer) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = nil
}
