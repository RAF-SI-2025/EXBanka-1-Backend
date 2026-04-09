package shared

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestErrOptimisticLock(t *testing.T) {
	assert.True(t, errors.Is(ErrOptimisticLock, ErrOptimisticLock))
	assert.Contains(t, ErrOptimisticLock.Error(), "optimistic lock")
}

func TestCheckRowsAffected_NilError_OneRow(t *testing.T) {
	result := &gorm.DB{RowsAffected: 1}
	assert.NoError(t, CheckRowsAffected(result))
}

func TestCheckRowsAffected_NilError_ZeroRows(t *testing.T) {
	result := &gorm.DB{RowsAffected: 0}
	err := CheckRowsAffected(result)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrOptimisticLock), "expected ErrOptimisticLock, got: %v", err)
}

func TestCheckRowsAffected_DBError(t *testing.T) {
	dbErr := fmt.Errorf("connection refused")
	result := &gorm.DB{Error: dbErr, RowsAffected: 0}
	err := CheckRowsAffected(result)
	assert.ErrorIs(t, err, dbErr)
	assert.False(t, errors.Is(err, ErrOptimisticLock))
}

func TestCheckRowsAffected_WrappedError_IsDetectable(t *testing.T) {
	// Simulate how repository code wraps the sentinel
	wrapped := fmt.Errorf("save account: %w", ErrOptimisticLock)
	assert.True(t, errors.Is(wrapped, ErrOptimisticLock))
}
