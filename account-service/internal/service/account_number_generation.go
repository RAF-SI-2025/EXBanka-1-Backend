package service

import (
	"crypto/rand"
	"math/big"
	"strconv"
)

const bankCode = "111"
const branchCode = "0001"

// accountTypeCode maps account kind to a 2-digit type code per spec.
func accountTypeCode(kind string) string {
	switch kind {
	case "current":
		return "11"
	case "foreign":
		return "21"
	default:
		return "10"
	}
}

// generateRandomDigits returns a string of n cryptographically random digits.
func generateRandomDigits(n int) string {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	val, _ := rand.Int(rand.Reader, max)
	if val == nil {
		return padLeft("0", n)
	}
	return padLeft(val.String(), n)
}

func padLeft(s string, length int) string {
	for len(s) < length {
		s = "0" + s
	}
	return s
}

// calculateModulo11CheckDigit computes a single digit (0-9) that makes the
// digit-sum of the full 18-digit number divisible by 11. The input
// numberWithPlaceholder has a '0' at the check digit position.
func calculateModulo11CheckDigit(numberWithPlaceholder string) int {
	sum := 0
	for _, c := range numberWithPlaceholder {
		sum += int(c - '0')
	}
	remainder := sum % 11
	if remainder == 0 {
		return 0
	}
	return 11 - remainder
}

// GenerateAccountNumber produces an 18-digit account number in the format:
// BBB-FFFF-NNNNNNNNN-TT where:
//   - BBB = bank code (3 digits)
//   - FFFF = branch code (4 digits)
//   - NNNNNNNNN = 8 random digits + 1 check digit (9 digits)
//   - TT = account type code (2 digits)
//
// The check digit is computed so the sum of all 18 digits is divisible by 11.
// If the check digit would be >= 10, the random digits are regenerated.
func GenerateAccountNumber(kind string) string {
	typeCode := accountTypeCode(kind)
	for {
		accountPart := generateRandomDigits(8)
		// Build partial: bankCode(3) + branchCode(4) + 8 random digits = 15 digits
		partial := bankCode + branchCode + accountPart
		// Full number with placeholder '0' for check digit: 15 + 1 + 2 = 18 digits
		checkDigit := calculateModulo11CheckDigit(partial + "0" + typeCode)
		if checkDigit >= 10 {
			// mod-11 can yield 10; regenerate random digits in that case
			continue
		}
		return partial + strconv.Itoa(checkDigit) + typeCode
	}
}
