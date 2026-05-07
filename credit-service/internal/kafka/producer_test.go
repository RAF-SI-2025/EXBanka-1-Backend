package kafka

import (
	"context"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All Publish* helpers funnel through inner.Publish; using a cancelled
// context keeps the segmentio writer from actually trying to dial Kafka,
// so the call returns quickly without needing a broker.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNewProducer_ReturnsNonNil(t *testing.T) {
	p := NewProducer("localhost:9999")
	require.NotNil(t, p)
	require.NoError(t, p.Close())
	// Idempotent close.
	require.NoError(t, p.Close())
}

func TestProducer_PublishLoanRequested_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishLoanRequested(cancelledContext(), kafkamsg.LoanStatusMessage{LoanRequestID: 1, Status: "requested"})
	assert.Error(t, err, "publish on cancelled context returns error without dialling broker")
}

func TestProducer_PublishLoanApproved_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishLoanApproved(cancelledContext(), kafkamsg.LoanStatusMessage{LoanRequestID: 1, Status: "approved"})
	assert.Error(t, err)
}

func TestProducer_PublishLoanRejected_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishLoanRejected(cancelledContext(), kafkamsg.LoanStatusMessage{LoanRequestID: 1, Status: "rejected"})
	assert.Error(t, err)
}

func TestProducer_PublishLoanDisbursed_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishLoanDisbursed(cancelledContext(), kafkamsg.LoanDisbursedMessage{LoanID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishInstallmentCollected_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishInstallmentCollected(cancelledContext(), kafkamsg.InstallmentResultMessage{LoanID: 1, Success: true})
	assert.Error(t, err)
}

func TestProducer_PublishInstallmentFailed_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishInstallmentFailed(cancelledContext(), kafkamsg.InstallmentResultMessage{LoanID: 1, Success: false})
	assert.Error(t, err)
}

func TestProducer_SendEmail_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.SendEmail(cancelledContext(), kafkamsg.SendEmailMessage{To: "x@y.z", EmailType: "test"})
	assert.Error(t, err)
}

func TestProducer_PublishVariableRateAdjusted_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishVariableRateAdjusted(cancelledContext(), kafkamsg.VariableRateAdjustedMessage{TierID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishLatePenaltyApplied_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishLatePenaltyApplied(cancelledContext(), kafkamsg.LatePenaltyAppliedMessage{LoanID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishGeneralNotification_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishGeneralNotification(cancelledContext(), kafkamsg.GeneralNotificationMessage{Title: "test"})
	assert.Error(t, err)
}
