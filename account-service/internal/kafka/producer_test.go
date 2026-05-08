package kafka

// Lightweight unit tests for the Kafka producer wrapper. We never open a real
// connection — the underlying segmentio Writer lazily dials on first publish,
// so constructing the producer is a pure local operation. To exercise the
// PublishX methods without hitting a broker we drive them with a context that
// is already cancelled; segmentio surfaces this as the ctx error before any
// network IO.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestNewProducer_AndClose(t *testing.T) {
	p := NewProducer("localhost:9092")
	require.NotNil(t, p)
	// Close releases the writer. Idempotent — second call is a no-op.
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
}

// makeCancelledCtx returns a context that is already cancelled, so any
// segmentio WriteMessages call returns context.Canceled before opening a
// TCP connection.
func makeCancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestProducer_PublishAccountCreated_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishAccountCreated(makeCancelledCtx(), kafkamsg.AccountCreatedMessage{
		AccountNumber: "111000100000000011",
		OwnerID:       42,
		AccountKind:   "current",
		CurrencyCode:  "RSD",
	})
	assert.Error(t, err)
}

func TestProducer_PublishAccountStatusChanged_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishAccountStatusChanged(makeCancelledCtx(), AccountStatusChangedMsg{
		AccountNumber: "111000100000000011",
		Status:        "inactive",
	})
	assert.Error(t, err)
}

func TestProducer_SendEmail_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.SendEmail(makeCancelledCtx(), kafkamsg.SendEmailMessage{
		To:        "x@y.z",
		EmailType: kafkamsg.EmailTypeAccountCreated,
	})
	assert.Error(t, err)
}

func TestProducer_PublishAccountNameUpdated_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishAccountNameUpdated(makeCancelledCtx(), kafkamsg.AccountNameUpdatedMessage{
		AccountNumber: "111000100000000011",
		NewName:       "New",
	})
	assert.Error(t, err)
}

func TestProducer_PublishAccountLimitsUpdated_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishAccountLimitsUpdated(makeCancelledCtx(), kafkamsg.AccountLimitsUpdatedMessage{
		AccountNumber: "111000100000000011",
	})
	assert.Error(t, err)
}

func TestProducer_PublishMaintenanceFeeCharged_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishMaintenanceFeeCharged(makeCancelledCtx(), kafkamsg.MaintenanceFeeChargedMessage{
		AccountNumber: "111000100000000011",
	})
	assert.Error(t, err)
}

func TestProducer_PublishSpendingReset_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishSpendingReset(makeCancelledCtx(), kafkamsg.SpendingResetMessage{
		ResetType: "daily",
	})
	assert.Error(t, err)
}

func TestProducer_PublishGeneralNotification_ReturnsCtxError(t *testing.T) {
	p := NewProducer("localhost:0")
	defer p.Close()

	err := p.PublishGeneralNotification(makeCancelledCtx(), kafkamsg.GeneralNotificationMessage{
		UserID: 1,
		Type:   "test",
	})
	assert.Error(t, err)
}
