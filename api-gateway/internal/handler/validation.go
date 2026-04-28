package handler

import (
	"fmt"
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

// enforceClientSelf checks that a client can only access their own resources.
// If the caller is a client (principal_type == "client"), the path client_id must match their JWT principal_id.
// Employees are allowed to access any client_id.
// Returns true if the request should continue, false if it was aborted.
func enforceClientSelf(c *gin.Context, pathClientID uint64) bool {
	pType, _ := c.Get("principal_type")
	if pType == "client" {
		uid, _ := c.Get("principal_id")
		userID, ok := uid.(int64)
		if !ok || uint64(userID) != pathClientID {
			c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"code": "forbidden", "message": "clients can only access their own resources"}})
			return false
		}
	}
	return true
}

// enforceOwnership verifies that a fetched resource belongs to the caller.
// Used inside /api/me/* handlers AFTER fetching a resource by an ID provided
// in the URL or body. If the caller is a client (principal_type == "client") and
// the resource owner does not match their JWT principal_id, a 404 not_found
// response is written and a non-nil error is returned — callers must return
// immediately. Employees bypass the check because their permissions gate
// access at the middleware layer.
//
// We return 404 (not 403) because confirming existence of another client's
// resource is itself a data leak.
func enforceOwnership(c *gin.Context, ownerID uint64) error {
	pType, _ := c.Get("principal_type")
	if pType != "client" {
		return nil
	}
	uid, _ := c.Get("principal_id")
	userID, ok := uid.(int64)
	if !ok || uint64(userID) != ownerID {
		apiError(c, 404, ErrNotFound, "resource not found")
		return fmt.Errorf("ownership mismatch: resource owner %d does not match caller %d", ownerID, userID)
	}
	return nil
}

// ownerToLegacyUserID converts a ResolvedIdentity OwnerID pointer to the
// legacy uint64 form still used by stock-service proto request shapes.
// Bank owners (OwnerID==nil) surface as 0; the proto rename is queued for
// Task 9 of the 2026-04-27 owner-type-schema plan, after which this helper
// can be deleted.
func ownerToLegacyUserID(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

// ownerToLegacySystemType converts an owner_type string to the legacy
// SystemType wire value carried by stock-service proto requests. The two
// vocabularies coincide today ("client"/"bank"); kept as an explicit
// helper so call sites read symmetrically with ownerToLegacyUserID and
// the eventual Task 9 rename has one place to delete.
func ownerToLegacySystemType(t string) string {
	return t
}

// derefU64Ptr returns *p when non-nil, else 0. Used to flatten the
// optional pointer fields on middleware.ResolvedIdentity (OwnerID,
// ActingEmployeeID) into the uint64 fields the gRPC requests still
// carry.
func derefU64Ptr(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// Standardized error response helpers
// ---------------------------------------------------------------------------

// Error code constants (lowercase, snake_case — machine-readable).
const (
	ErrValidation   = "validation_error"
	ErrUnauthorized = "unauthorized"
	ErrForbidden    = "forbidden"
	ErrNotFound     = "not_found"
	ErrConflict     = "conflict"
	ErrBusinessRule = "business_rule_violation"
	ErrRateLimited  = "rate_limited"
	ErrInternal     = "internal_error"
)

// apiError sends a structured error JSON response.
// Format: {"error": {"code": "...", "message": "...", "details": {...}}}
// The `details` parameter is optional; pass nil or omit to skip it.
func apiError(c *gin.Context, status int, code, message string, details ...map[string]interface{}) {
	body := gin.H{"code": code, "message": message}
	if len(details) > 0 && details[0] != nil {
		body["details"] = details[0]
	}
	c.JSON(status, gin.H{"error": body})
}

// grpcToHTTPError maps a gRPC error to an HTTP status code and error code string.
func grpcToHTTPError(err error) (int, string, string) {
	s, ok := status.FromError(err)
	if !ok {
		return 500, ErrInternal, err.Error()
	}
	switch s.Code() {
	case codes.NotFound:
		return 404, ErrNotFound, s.Message()
	case codes.InvalidArgument:
		return 400, ErrValidation, s.Message()
	case codes.Unauthenticated:
		return 401, ErrUnauthorized, s.Message()
	case codes.PermissionDenied:
		return 403, ErrForbidden, s.Message()
	case codes.AlreadyExists:
		return 409, ErrConflict, s.Message()
	case codes.FailedPrecondition:
		return 409, ErrBusinessRule, s.Message()
	case codes.ResourceExhausted:
		return 429, ErrRateLimited, s.Message()
	default:
		return 500, ErrInternal, s.Message()
	}
}

// handleGRPCError sends a structured error response based on a gRPC error.
func handleGRPCError(c *gin.Context, err error) {
	httpStatus, code, message := grpcToHTTPError(err)
	apiError(c, httpStatus, code, message)
}

// isNotFound reports whether err is a gRPC NotFound status.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.NotFound
}

// emptyIfNil returns an initialized empty slice when s is nil.
// This prevents encoding/json from serializing nil slices as null.
func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
