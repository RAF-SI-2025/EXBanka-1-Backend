package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAccountNumber(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		num := GenerateAccountNumber()
		assert.Len(t, num, 18, "account number must be 18 chars")
		assert.Regexp(t, `^105\d{13}\d{2}$`, num, "format: 105 + 13 digits + 2 check digits")
		seen[num] = true
	}
	assert.Greater(t, len(seen), 90, "should generate mostly unique numbers")
}
