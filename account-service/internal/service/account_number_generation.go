package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

func GenerateAccountNumber() string {
	max := big.NewInt(9999999999999)
	n, _ := rand.Int(rand.Reader, max)
	// Bank code: 105 + 13 random digits
	middle := fmt.Sprintf("%013d", n.Int64())
	base := "105" + middle
	// Compute mod97 check digits
	checkVal := 98 - mod97(base+"00")
	return fmt.Sprintf("%s%02d", base, checkVal)
}

func mod97(s string) int {
	remainder := 0
	for _, c := range s {
		d := int(c - '0')
		remainder = (remainder*10 + d) % 97
	}
	return remainder
}
