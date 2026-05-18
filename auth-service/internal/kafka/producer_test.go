package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestNewProducer_ConstructsWrapper(t *testing.T) {
	// NewProducer does not dial — it just builds a kafka-go writer.
	p := NewProducer("localhost:1")
	assert.NotNil(t, p)
}

func TestProducer_Close_NoLeak(t *testing.T) {
	p := NewProducer("localhost:1")
	// Close on an unused writer must not panic and should return nil.
	assert.NoError(t, p.Close())
}

func TestProducer_SendEmail_WriteFails_NoBroker(t *testing.T) {
	// Without a real broker, SendEmail should error out within the writeTimeout.
	// We just want to exercise the code path — coverage, not Kafka semantics.
	p := NewProducer("127.0.0.1:1")
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        "u@test.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data:      map[string]string{"k": "v"},
	})
	// Either ctx-deadline or dial-failure — both surface as an error.
	assert.Error(t, err)
}

func TestProducer_Publish_WriteFails_NoBroker(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Publish(ctx, "some.topic", map[string]string{"k": "v"})
	assert.Error(t, err)
}
