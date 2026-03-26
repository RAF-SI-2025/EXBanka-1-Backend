package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// PepperPassword applies an HMAC-SHA256 pepper to the raw password and returns
// a fixed-length 64-character hex string safe for use as bcrypt input.
//
// Using HMAC-SHA256 instead of simple concatenation:
//   - Guarantees fixed 64-byte output regardless of pepper or password length,
//     avoiding bcrypt's silent 72-byte truncation issue.
//   - Provides proper cryptographic binding between the pepper and the password.
//
// If pepper is empty, the HMAC degrades to a keyed hash with an empty key,
// which still produces a deterministic 64-byte output.
func PepperPassword(pepper, rawPassword string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(rawPassword))
	return hex.EncodeToString(mac.Sum(nil))
}
