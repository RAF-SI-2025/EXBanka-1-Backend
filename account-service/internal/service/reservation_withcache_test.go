package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// WithCache wires the optional Redis cache and returns the receiver for
// chaining; a nil cache is a supported "no cache" configuration.
func TestReservationService_WithCache_ChainsAndStores(t *testing.T) {
	svc := NewReservationService(nil, nil, nil, nil)
	got := svc.WithCache(nil)
	assert.Same(t, svc, got, "WithCache must return the receiver for chaining")
	assert.Nil(t, svc.cache)
}
