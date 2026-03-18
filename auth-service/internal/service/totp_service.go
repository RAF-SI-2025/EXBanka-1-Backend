package service

import (
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPService handles TOTP 2FA operations.
type TOTPService struct{}

func NewTOTPService() *TOTPService {
	return &TOTPService{}
}

// GenerateSecret creates a new TOTP secret for a user.
// Returns the base32 secret, the OTP auth URL (for QR code), and any error.
func (s *TOTPService) GenerateSecret(email, issuer string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: email,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateCode checks if a TOTP code is valid for the given secret (±1 period).
func (s *TOTPService) ValidateCode(secret, code string) bool {
	valid, _ := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return valid
}
