package service

import (
	"fmt"
	"math/rand"
	"time"
)

func GenerateAccountNumber() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Bank code: 105 + 13 random digits
	middle := fmt.Sprintf("%013d", rng.Int63n(9999999999999))
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
