package kafka

import (
	"context"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cancelledContext returns a context that's already done so the segmentio
// writer fails fast instead of dialing Kafka.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNewProducer_ReturnsNonNil(t *testing.T) {
	p := NewProducer("localhost:9999")
	require.NotNil(t, p)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close(), "Close is idempotent")
}

func TestProducer_SendEmail_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.SendEmail(cancelledContext(), kafkamsg.SendEmailMessage{To: "x@x"})
	assert.Error(t, err)
}

func TestProducer_PublishEmployeeCreated_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishEmployeeCreated(cancelledContext(), kafkamsg.EmployeeCreatedMessage{EmployeeID: 1, Email: "x@x"})
	assert.Error(t, err)
}

func TestProducer_PublishEmployeeUpdated_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishEmployeeUpdated(cancelledContext(), kafkamsg.EmployeeCreatedMessage{EmployeeID: 1, Email: "x@x"})
	assert.Error(t, err)
}

func TestProducer_PublishEmployeeLimitsUpdated_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishEmployeeLimitsUpdated(cancelledContext(), kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 1, Action: "set"})
	assert.Error(t, err)
}

func TestProducer_PublishLimitTemplate_AllActions(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	for _, action := range []string{"created", "updated", "deleted", "unknown"} {
		err := p.PublishLimitTemplate(cancelledContext(), kafkamsg.LimitTemplateMessage{Action: action})
		assert.Error(t, err, "action=%s", action)
	}
}

func TestProducer_PublishActuaryLimitUpdated_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishActuaryLimitUpdated(cancelledContext(), kafkamsg.ActuaryLimitUpdatedMessage{EmployeeID: 1, Action: "set"})
	assert.Error(t, err)
}

func TestProducer_PublishBlueprint_AllActions(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	for _, action := range []string{"created", "updated", "deleted", "applied", "weird"} {
		err := p.PublishBlueprint(cancelledContext(), kafkamsg.BlueprintMessage{Action: action})
		assert.Error(t, err, "action=%s", action)
	}
}

func TestProducer_PublishRolePermissionsChanged_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishRolePermissionsChanged(cancelledContext(), kafkamsg.RolePermissionsChangedMessage{RoleID: 1})
	assert.Error(t, err)
}

func TestProducer_PublishRaw_WithCancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishRaw(cancelledContext(), "topic", []byte(`{}`))
	assert.Error(t, err)
}
