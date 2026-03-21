package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

var paymentCodeRegex = regexp.MustCompile(`^2\d{2}$`)

var pinRegex = regexp.MustCompile(`^\d{4}$`)

var activityCodeRegex = regexp.MustCompile(`^\d{2}\.\d{1,2}$`)

// validateActivityCode checks that the activity code is in format xx.xx (e.g., 10.1, 84.11).
func validateActivityCode(code string) error {
	if !activityCodeRegex.MatchString(code) {
		return fmt.Errorf("activity code must be in format xx.xx (e.g., 10.1, 84.11)")
	}
	return nil
}

// validatePaymentCode checks that payment_code is a 3-digit code starting with 2.
func validatePaymentCode(code string) error {
	if !paymentCodeRegex.MatchString(code) {
		return fmt.Errorf("payment_code must be a 3-digit code starting with 2 (e.g., 289)")
	}
	return nil
}

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

// grpcMessage extracts the human-readable message from a gRPC error,
// stripping the "rpc error: code = ... desc = ..." wrapper.
func grpcMessage(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Message()
	}
	return err.Error()
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

// ---------------------------------------------------------------------------
// Structured error response helpers
// ---------------------------------------------------------------------------

// ErrorCode constants for machine-readable error identification.
const (
	ErrCodeValidation           = "VALIDATION_ERROR"
	ErrCodeNotFound             = "NOT_FOUND"
	ErrCodeForbidden            = "FORBIDDEN"
	ErrCodeInsufficientFunds    = "INSUFFICIENT_FUNDS"
	ErrCodeLimitExceeded        = "LIMIT_EXCEEDED"
	ErrCodeDuplicate            = "DUPLICATE"
	ErrCodeVerificationRequired = "VERIFICATION_REQUIRED"
	ErrCodeVerificationFailed   = "VERIFICATION_FAILED"
	ErrCodeInternal             = "INTERNAL_ERROR"
)

// APIError represents a structured error response.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// errorResponse sends a structured error JSON response.
func errorResponse(c *gin.Context, httpStatus int, code, message string) {
	c.JSON(httpStatus, gin.H{"error": APIError{Code: code, Message: message}})
}

// validationError sends a 400 error with field information.
func validationError(c *gin.Context, field, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": APIError{
		Code:    ErrCodeValidation,
		Message: message,
		Field:   field,
	}})
}

// grpcToAPIError maps a gRPC error to an HTTP status and APIError.
func grpcToAPIError(err error) (int, APIError) {
	s, ok := status.FromError(err)
	if !ok {
		return 500, APIError{Code: ErrCodeInternal, Message: err.Error()}
	}
	switch s.Code() {
	case codes.NotFound:
		return 404, APIError{Code: ErrCodeNotFound, Message: s.Message()}
	case codes.InvalidArgument:
		return 400, APIError{Code: ErrCodeValidation, Message: s.Message()}
	case codes.PermissionDenied:
		return 403, APIError{Code: ErrCodeForbidden, Message: s.Message()}
	case codes.AlreadyExists:
		return 409, APIError{Code: ErrCodeDuplicate, Message: s.Message()}
	case codes.FailedPrecondition:
		return 422, APIError{Code: ErrCodeLimitExceeded, Message: s.Message()}
	case codes.ResourceExhausted:
		return 429, APIError{Code: ErrCodeLimitExceeded, Message: s.Message()}
	default:
		return 500, APIError{Code: ErrCodeInternal, Message: s.Message()}
	}
}

// handleGRPCError sends a structured error response based on a gRPC error.
func handleGRPCError(c *gin.Context, err error) {
	httpStatus, apiErr := grpcToAPIError(err)
	c.JSON(httpStatus, gin.H{"error": apiErr})
}
