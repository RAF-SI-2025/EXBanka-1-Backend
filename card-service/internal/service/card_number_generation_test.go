package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLuhnCheck(t *testing.T) {
	// Known valid Luhn numbers
	tests := []struct {
		number string
		valid  bool
	}{
		{"4532015112830366", true},  // Visa
		{"5425233430109903", true},  // Mastercard
		{"4532015112830367", false}, // invalid
		{"1234567890123456", false}, // invalid
	}
	for _, tt := range tests {
		t.Run(tt.number, func(t *testing.T) {
			assert.Equal(t, tt.valid, LuhnCheck(tt.number))
		})
	}
}

func TestGenerateCardNumber(t *testing.T) {
	brands := []string{"visa", "mastercard", "dinacard", "amex"}
	expectedLengths := map[string]int{
		"visa":       16,
		"mastercard": 16,
		"dinacard":   16,
		"amex":       15,
	}
	for _, brand := range brands {
		t.Run(brand, func(t *testing.T) {
			num := GenerateCardNumber(brand)
			assert.Len(t, num, expectedLengths[brand], "card number must be %d digits for %s", expectedLengths[brand], brand)
			assert.True(t, LuhnCheck(num), "card number must pass Luhn check")
		})
	}
}

func TestGenerateCardNumber_AmexLength(t *testing.T) {
	for i := 0; i < 100; i++ {
		num := GenerateCardNumber("amex")
		assert.Len(t, num, 15, "Amex card number must be 15 digits")
		assert.True(t, LuhnCheck(num), "Amex card number must pass Luhn check")
		prefix := num[:2]
		assert.True(t, prefix == "34" || prefix == "37", "Amex card must start with 34 or 37, got %s", prefix)
	}
}

func TestGenerateCardNumber_MastercardPrefix(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		num := GenerateCardNumber("mastercard")
		assert.Len(t, num, 16, "Mastercard number must be 16 digits")
		assert.True(t, LuhnCheck(num), "Mastercard number must pass Luhn check")
		prefix := num[:2]
		validPrefixes := []string{"51", "52", "53", "54", "55"}
		assert.Contains(t, validPrefixes, prefix, "Mastercard prefix must be in 51-55 range, got %s", prefix)
		seen[prefix] = true
	}
	// With 500 iterations and 5 equally likely prefixes, the probability
	// of missing any single prefix is astronomically low.
	assert.True(t, len(seen) > 1, "Expected multiple different Mastercard prefixes across 500 generations, only saw: %v", seen)
}

func TestGenerateCardNumber_VisaPrefix(t *testing.T) {
	for i := 0; i < 100; i++ {
		num := GenerateCardNumber("visa")
		assert.Len(t, num, 16, "Visa card number must be 16 digits")
		assert.True(t, LuhnCheck(num), "Visa card number must pass Luhn check")
		assert.True(t, strings.HasPrefix(num, "4"), "Visa card must start with 4, got %s", num[:1])
	}
}

func TestGenerateCardNumber_DinaCardPrefix(t *testing.T) {
	for i := 0; i < 100; i++ {
		num := GenerateCardNumber("dinacard")
		assert.Len(t, num, 16, "DinaCard number must be 16 digits")
		assert.True(t, LuhnCheck(num), "DinaCard number must pass Luhn check")
		assert.True(t, strings.HasPrefix(num, "9891"), "DinaCard must start with 9891, got %s", num[:4])
	}
}

func TestMaskCardNumber(t *testing.T) {
	masked := MaskCardNumber("5798123456785571")
	assert.Equal(t, "5798********5571", masked)
}

func TestGenerateCVV(t *testing.T) {
	cvv := GenerateCVV()
	assert.Len(t, cvv, 3, "CVV must be 3 digits")
	for _, c := range cvv {
		assert.True(t, c >= '0' && c <= '9', "CVV must be numeric")
	}
}
