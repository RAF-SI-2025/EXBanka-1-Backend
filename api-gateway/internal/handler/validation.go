package handler

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// oneOf checks that value (lowercased) is one of the allowed values.
// Returns the normalized (lowercased) value and an error if invalid.
func oneOf(field, value string, allowed ...string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s must be one of: %s", field, strings.Join(allowed, ", "))
}

var pinRegex = regexp.MustCompile(`^\d{4}$`)

// validatePin checks that the PIN is exactly 4 digits.
func validatePin(pin string) error {
	if !pinRegex.MatchString(pin) {
		return fmt.Errorf("pin must be exactly 4 digits")
	}
	return nil
}

// positive checks that a numeric value is greater than zero.
func positive(field string, value float64) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

// nonNegative checks that a numeric value is >= 0.
func nonNegative(field string, value float64) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", field)
	}
	return nil
}

// inRange checks that an int32 value is within [min, max].
func inRange(field string, value, min, max int32) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d", field, min, max)
	}
	return nil
}

// notEqual checks that two string values are different.
func notEqual(field1, val1, field2, val2 string) error {
	if val1 == val2 {
		return fmt.Errorf("%s and %s must be different", field1, field2)
	}
	return nil
}

// enforceClientSelf checks that a client can only access their own resources.
// If the caller is a client (system_type == "client"), the path client_id must match their JWT user_id.
// Employees are allowed to access any client_id.
// Returns true if the request should continue, false if it was aborted.
func enforceClientSelf(c *gin.Context, pathClientID uint64) bool {
	sysType, _ := c.Get("system_type")
	if sysType == "client" {
		uid, _ := c.Get("user_id")
		userID, ok := uid.(int64)
		if !ok || uint64(userID) != pathClientID {
			c.AbortWithStatusJSON(403, gin.H{"error": "clients can only access their own resources"})
			return false
		}
	}
	return true
}
