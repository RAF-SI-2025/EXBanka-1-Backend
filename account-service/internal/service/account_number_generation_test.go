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

func TestSetBankCode_OverridesPrefix(t *testing.T) {
	t.Cleanup(func() { SetBankCode("111") })

	SetBankCode("222")
	num := GenerateAccountNumber("current")
	assert.Equal(t, "222", num[:3], "prefix should reflect SetBankCode override")
	assert.Equal(t, "0001", num[3:7], "branch code unchanged")
	assert.Equal(t, "11", num[16:18], "type code unchanged")

	sum := 0
	for _, c := range num {
		sum += int(c - '0')
	}
	assert.Equal(t, 0, sum%11, "check digit still valid after prefix swap")
}

func TestSetBankCode_RejectsInvalid(t *testing.T) {
	t.Cleanup(func() { SetBankCode("111") })

	cases := []string{"", "12", "1234", "abc", "11a"}
	for _, c := range cases {
		c := c
		t.Run("input_"+c, func(t *testing.T) {
			assert.Panics(t, func() { SetBankCode(c) })
		})
	}
}
