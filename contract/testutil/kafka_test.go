package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockKafkaProducer_CapturesEvents(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("user.created", map[string]interface{}{"id": 1})
	p.Publish("user.updated", map[string]interface{}{"id": 1})

	require.Len(t, p.Events(), 2)
	assert.Equal(t, "user.created", p.Events()[0].Topic)
	assert.Equal(t, 1, p.Events()[0].Data["id"])
}

func TestMockKafkaProducer_EventsByTopic(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("user.created", map[string]interface{}{"id": 1})
	p.Publish("user.created", map[string]interface{}{"id": 2})
	p.Publish("user.updated", map[string]interface{}{"id": 1})

	created := p.EventsByTopic("user.created")
	assert.Len(t, created, 2)
}

func TestMockKafkaProducer_Clear(t *testing.T) {
	p := NewMockKafkaProducer()
	p.Publish("topic", map[string]interface{}{})
	p.Clear()
	assert.Empty(t, p.Events())
}
