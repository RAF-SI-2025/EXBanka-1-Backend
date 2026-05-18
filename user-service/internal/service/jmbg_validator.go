// user-service/internal/service/jmbg_validator.go
package service

import "fmt"

func ValidateJMBG(jmbg string) error {
	if len(jmbg) != 13 {
		return fmt.Errorf("ValidateJMBG: JMBG must be exactly 13 digits: %w", ErrInvalidJMBG)
	}
	for _, c := range jmbg {
		if c < '0' || c > '9' {
			return fmt.Errorf("ValidateJMBG: JMBG must contain only digits: %w", ErrInvalidJMBG)
		}
	}
	return nil
}
