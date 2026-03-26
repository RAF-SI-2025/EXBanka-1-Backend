package shared

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrOptimisticLock(t *testing.T) {
	assert.True(t, errors.Is(ErrOptimisticLock, ErrOptimisticLock))
	assert.Contains(t, ErrOptimisticLock.Error(), "optimistic lock")
}
