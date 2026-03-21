package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAccountNumber_CurrentFormat(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		num := GenerateAccountNumber("current")
		assert.Len(t, num, 18, "account number must be 18 chars")
		assert.Regexp(t, `^111\d{15}$`, num, "format: 111 + 15 digits")
		// Must start with bank code + branch code
		assert.Equal(t, "111", num[:3], "bank code must be 111")
		assert.Equal(t, "0001", num[3:7], "branch code must be 0001")
		// Must end with type code "11" for current
		assert.Equal(t, "11", num[16:18], "type code must be 11 for current")
		seen[num] = true
	}
	assert.Greater(t, len(seen), 90, "should generate mostly unique numbers")
}

func TestGenerateAccountNumber_ForeignFormat(t *testing.T) {
	for i := 0; i < 20; i++ {
		num := GenerateAccountNumber("foreign")
		assert.Len(t, num, 18)
		assert.Equal(t, "111", num[:3])
		assert.Equal(t, "0001", num[3:7])
		assert.Equal(t, "21", num[16:18], "type code must be 21 for foreign")
	}
}

func TestGenerateAccountNumber_DefaultFormat(t *testing.T) {
	num := GenerateAccountNumber("other")
	assert.Len(t, num, 18)
	assert.Equal(t, "10", num[16:18], "type code must be 10 for unknown kind")
}

func TestGenerateAccountNumber_CheckDigit(t *testing.T) {
	for i := 0; i < 100; i++ {
		num := GenerateAccountNumber("current")
		sum := 0
		for _, c := range num {
			sum += int(c - '0')
		}
		assert.Equal(t, 0, sum%11, "digit sum must be divisible by 11")
	}
}
