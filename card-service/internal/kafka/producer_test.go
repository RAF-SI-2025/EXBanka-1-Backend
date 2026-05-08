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

func TestProducer_PublishCardCreated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardCreated(cancelledContext(), kafkamsg.CardCreatedMessage{CardID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishCardStatusChanged_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardStatusChanged(cancelledContext(), kafkamsg.CardStatusChangedMessage{CardID: 1, NewStatus: "blocked"})
	assert.Error(t, err)
}

func TestProducer_SendEmail_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.SendEmail(cancelledContext(), kafkamsg.SendEmailMessage{To: "x@y.z"})
	assert.Error(t, err)
}

func TestProducer_PublishCardTemporaryBlocked_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardTemporaryBlocked(cancelledContext(), kafkamsg.CardTemporaryBlockedMessage{CardID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishVirtualCardCreated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishVirtualCardCreated(cancelledContext(), kafkamsg.VirtualCardCreatedMessage{CardID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishCardRequestCreated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardRequestCreated(cancelledContext(), kafkamsg.CardRequestCreatedMessage{RequestID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishCardRequestApproved_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardRequestApproved(cancelledContext(), kafkamsg.CardRequestApprovedMessage{RequestID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishCardRequestRejected_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishCardRequestRejected(cancelledContext(), kafkamsg.CardRequestRejectedMessage{RequestID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishGeneralNotification_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishGeneralNotification(cancelledContext(), kafkamsg.GeneralNotificationMessage{Title: "x"})
	assert.Error(t, err)
}
