package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNewProducer_ConstructAndClose(t *testing.T) {
	p := NewProducer("localhost:9999")
	require.NotNil(t, p)
	require.NoError(t, p.Close())
}

func TestProducer_PublishClientCreated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishClientCreated(cancelledContext(), kafkamsg.ClientCreatedMessage{ClientID: 1, Email: "x@y.z"})
	assert.Error(t, err)
}

func TestProducer_PublishClientUpdated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishClientUpdated(cancelledContext(), kafkamsg.ClientCreatedMessage{ClientID: 1})
	assert.Error(t, err)
}

func TestProducer_SendEmail_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.SendEmail(cancelledContext(), kafkamsg.SendEmailMessage{To: "x@y.z"})
	assert.Error(t, err)
}

func TestProducer_PublishClientLimitsUpdated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishClientLimitsUpdated(cancelledContext(), kafkamsg.ClientLimitsUpdatedMessage{ClientID: 1})
	assert.Error(t, err)
}
