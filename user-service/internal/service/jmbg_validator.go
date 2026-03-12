// user-service/internal/service/jmbg_validator.go
package service

import "errors"

func ValidateJMBG(jmbg string) error {
	if len(jmbg) != 13 {
		return errors.New("JMBG must be exactly 13 digits")
	}
	for _, c := range jmbg {
		if c < '0' || c > '9' {
			return errors.New("JMBG must contain only digits")
		}
	}
	return nil
}
