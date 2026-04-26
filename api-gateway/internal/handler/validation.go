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
			c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"code": "forbidden", "message": "clients can only access their own resources"}})
			return false
		}
	}
	return true
}

// enforceOwnership verifies that a fetched resource belongs to the caller.
// Used inside /api/me/* handlers AFTER fetching a resource by an ID provided
// in the URL or body. If the caller is a client (system_type == "client") and
// the resource owner does not match their JWT user_id, a 404 not_found
// response is written and a non-nil error is returned — callers must return
// immediately. Employees bypass the check because their permissions gate
// access at the middleware layer.
//
// We return 404 (not 403) because confirming existence of another client's
// resource is itself a data leak.
func enforceOwnership(c *gin.Context, ownerID uint64) error {
	sysType, _ := c.Get("system_type")
	if sysType != "client" {
		return nil
	}
	uid, _ := c.Get("user_id")
	userID, ok := uid.(int64)
	if !ok || uint64(userID) != ownerID {
		apiError(c, 404, ErrNotFound, "resource not found")
		return fmt.Errorf("ownership mismatch: resource owner %d does not match caller %d", ownerID, userID)
	}
	return nil
}

// meIdentity returns the JWT-authenticated user's (userID, systemType) pair
// extracted from the gin context populated by the auth middleware.
//
// Used by /me/* handlers before calling any user-scoped RPC — without
// system_type, stock-service (and any other service that filters by
// (user_id, system_type)) would match across both client and employee
// namespaces, leaking data between users that happen to share the same
// numeric user_id.
//
// Writes a 401 response and returns ok=false if either value is missing
// or has an unexpected type; callers must return immediately.
func meIdentity(c *gin.Context) (userID uint64, systemType string, ok bool) {
	uid := c.GetInt64("user_id")
	if uid <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing user_id in JWT context")
		return 0, "", false
	}
	raw, _ := c.Get("system_type")
	st, _ := raw.(string)
	if st == "" {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing system_type in JWT context")
		return 0, "", false
	}
	return uint64(uid), st, true
}

// BankSentinelUserID is the synthetic user_id for bank-owned orders /
// holdings / portfolio data. Mirrors the sentinel account-service uses
// for bank-owned accounts (1_000_000_000) so the bank "owner" is
// represented consistently across services.
const BankSentinelUserID uint64 = 1_000_000_000

// BankSystemType is the system_type value for bank-owned trading data.
// Distinct from "employee" and "client" so stock-service queries can
// scope to the bank's portfolio without conflating with individual
// employee or client positions.
const BankSystemType = "bank"

// mePortfolioIdentity returns the (userID, systemType) pair to use when
// looking up portfolio / order / holdings data on behalf of the
// authenticated caller.
//
//   - Clients see their own data: returns (client.user_id, "client").
//   - Employees see the BANK's data: returns (BankSentinelUserID, "bank").
//     The employee's own user_id has no portfolio of its own; their work
//     is the bank's work, so /me/* surfaces what they manage.
//
// Use this in handlers that read or write trading data scoped by
// (user_id, system_type). Use plain meIdentity for non-trading /me/*
// endpoints (profile, permissions, etc.) where the employee's identity
// is what matters.
//
// Pair this with actingEmployeeID(c) when CREATING an order — without
// the employee's real id propagated as acting_employee_id, stock-service
// cannot enforce per-actuary limits (it would key the lookup on the
// bank sentinel user_id, which has no actuary row).
func mePortfolioIdentity(c *gin.Context) (userID uint64, systemType string, ok bool) {
	uid, st, ok := meIdentity(c)
	if !ok {
		return 0, "", false
	}
	if st == "employee" {
		return BankSentinelUserID, BankSystemType, true
	}
	return uid, st, true
}

// actingEmployeeID returns the JWT user_id when the caller is an employee,
// 0 otherwise. Use this to populate Order.ActingEmployeeID on order
// creation: stock-service's actuary-limit gate fires when this is non-zero
// regardless of whether the order ends up system_type="employee" (legacy),
// "bank" (employee acting for the bank, Phase 3), or "client" (employee
// on behalf of a client). Without this hook, Phase 3's identity swap
// would silently bypass the EmployeeLimit gate for every employee buy.
func actingEmployeeID(c *gin.Context) uint64 {
	uid := c.GetInt64("user_id")
	if uid <= 0 {
		return 0
	}
	raw, _ := c.Get("system_type")
	if st, _ := raw.(string); st != "employee" {
		return 0
	}
	return uint64(uid)
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

// emptyIfNil returns an initialized empty slice when s is nil.
// This prevents encoding/json from serializing nil slices as null.
func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
