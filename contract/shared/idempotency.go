package shared

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// GenerateIdempotencyKey returns a new UUID v4 string.
func GenerateIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ValidateIdempotencyKey checks if a string is a valid UUID v4.
func ValidateIdempotencyKey(key string) bool {
	return uuidRegex.MatchString(key)
}
