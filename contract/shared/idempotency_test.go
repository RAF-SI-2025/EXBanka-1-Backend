package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateIdempotencyKey_Length(t *testing.T) {
	key := GenerateIdempotencyKey()
	assert.Len(t, key, 36)
}

func TestGenerateIdempotencyKey_Unique(t *testing.T) {
	key1 := GenerateIdempotencyKey()
	key2 := GenerateIdempotencyKey()
	assert.NotEqual(t, key1, key2)
}

func TestValidateIdempotencyKey_Valid(t *testing.T) {
	assert.True(t, ValidateIdempotencyKey(GenerateIdempotencyKey()))
}

func TestValidateIdempotencyKey_Invalid(t *testing.T) {
	assert.False(t, ValidateIdempotencyKey(""))
	assert.False(t, ValidateIdempotencyKey("not-a-uuid"))
}
