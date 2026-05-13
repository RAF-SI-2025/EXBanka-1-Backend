package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestProducer_PublishRatesUpdated_CancelledContext(t *testing.T) {
	p := NewProducer("localhost:9999")
	defer p.Close()
	err := p.PublishRatesUpdated(cancelledContext(), []string{"EUR", "USD"}, "2026-05-08T10:00:00Z")
	assert.Error(t, err)
}
