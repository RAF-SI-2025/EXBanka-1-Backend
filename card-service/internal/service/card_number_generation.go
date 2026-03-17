package service

import (
	"fmt"
	"math/rand"
	"time"
)

// IIN prefixes per brand
var brandPrefixes = map[string]string{
	"visa":       "4",
	"mastercard": "51",
	"dinacard":   "9891",
	"amex":       "34",
}

func GenerateCardNumber(brand string) string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	prefix := brandPrefixes[brand]
	if prefix == "" {
		prefix = "4" // default to visa-like
	}
	// Fill remaining digits (leaving last for check digit)
	remaining := 15 - len(prefix)
	middle := fmt.Sprintf("%0*d", remaining, rng.Int63n(int64(pow10(remaining))))
	partial := prefix + middle
	// Compute Luhn check digit
	check := luhnCheckDigit(partial)
	return fmt.Sprintf("%s%d", partial, check)
}

func LuhnCheck(number string) bool {
	sum := 0
	nDigits := len(number)
	parity := nDigits % 2
	for i, c := range number {
		d := int(c - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

func luhnCheckDigit(partial string) int {
	sum := 0
	parity := (len(partial) + 1) % 2
	for i, c := range partial {
		d := int(c - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - (sum % 10)) % 10
}

func MaskCardNumber(full string) string {
	if len(full) < 8 {
		return full
	}
	return full[:4] + "********" + full[len(full)-4:]
}

func GenerateCVV() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%03d", rng.Intn(1000))
}

func pow10(n int) int {
	result := 1
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}
