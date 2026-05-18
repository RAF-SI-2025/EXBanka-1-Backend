package push

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopProvider_Send_ReturnsNil(t *testing.T) {
	p := NewNoopProvider()
	require.NotNil(t, p)

	err := p.Send(context.Background(), "device-token-123", "Hello", "Body text", map[string]string{
		"key": "value",
	})
	assert.NoError(t, err)
}

func TestNoopProvider_Send_NilDataReturnsNil(t *testing.T) {
	p := NewNoopProvider()
	err := p.Send(context.Background(), "tok", "title", "body", nil)
	assert.NoError(t, err)
}

func TestNoopProvider_Name(t *testing.T) {
	p := NewNoopProvider()
	assert.Equal(t, "noop", p.Name())
}

// Compile-time assertion: NoopProvider satisfies the Provider interface.
func TestNoopProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = NewNoopProvider()
}
