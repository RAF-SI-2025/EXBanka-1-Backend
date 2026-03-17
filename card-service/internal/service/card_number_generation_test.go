package service

import (
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
	for _, brand := range brands {
		t.Run(brand, func(t *testing.T) {
			num := GenerateCardNumber(brand)
			assert.Len(t, num, 16, "card number must be 16 digits")
			assert.True(t, LuhnCheck(num), "card number must pass Luhn check")
		})
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
