// api-gateway/internal/handler/validation_test.go
package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// oneOf
// ---------------------------------------------------------------------------

func TestOneOf_NormalizesAndAcceptsValid(t *testing.T) {
	got, err := oneOf("account_kind", "CURRENT", "current", "foreign")
	require.NoError(t, err)
	assert.Equal(t, "current", got, "oneOf should normalize value to lowercase")
}

func TestOneOf_AcceptsLowercaseValid(t *testing.T) {
	got, err := oneOf("card_brand", "visa", "visa", "mastercard", "dinacard", "amex")
	require.NoError(t, err)
	assert.Equal(t, "visa", got)
}

func TestOneOf_TrimsWhitespace(t *testing.T) {
	got, err := oneOf("interest_type", "  fixed  ", "fixed", "variable")
	require.NoError(t, err)
	assert.Equal(t, "fixed", got, "oneOf should trim surrounding whitespace")
}

func TestOneOf_RejectsInvalidValue(t *testing.T) {
	_, err := oneOf("account_kind", "savings", "current", "foreign")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_kind")
	assert.Contains(t, err.Error(), "current, foreign")
}

func TestOneOf_RejectsEmptyValue(t *testing.T) {
	_, err := oneOf("loan_type", "", "cash", "housing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loan_type")
}

// ---------------------------------------------------------------------------
// positive
// ---------------------------------------------------------------------------

func TestPositive_AcceptsPositiveValue(t *testing.T) {
	err := positive("amount", 0.01)
	assert.NoError(t, err)
}

func TestPositive_RejectsZero(t *testing.T) {
	err := positive("amount", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount")
	assert.Contains(t, err.Error(), "positive")
}

func TestPositive_RejectsNegativeValue(t *testing.T) {
	err := positive("amount", -5.5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount")
}

// ---------------------------------------------------------------------------
// nonNegative
// ---------------------------------------------------------------------------

func TestNonNegative_AcceptsZero(t *testing.T) {
	err := nonNegative("limit", 0)
	assert.NoError(t, err, "zero should be allowed by nonNegative")
}

func TestNonNegative_AcceptsPositiveValue(t *testing.T) {
	err := nonNegative("limit", 100.5)
	assert.NoError(t, err)
}

func TestNonNegative_RejectsNegativeValue(t *testing.T) {
	err := nonNegative("limit", -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

// ---------------------------------------------------------------------------
// validatePin
// ---------------------------------------------------------------------------

func TestValidatePin_AcceptsExactlyFourDigits(t *testing.T) {
	err := validatePin("1234")
	assert.NoError(t, err)
}

func TestValidatePin_AcceptsLeadingZeros(t *testing.T) {
	err := validatePin("0000")
	assert.NoError(t, err)
}

func TestValidatePin_RejectsTooShort(t *testing.T) {
	err := validatePin("123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4 digits")
}

func TestValidatePin_RejectsTooLong(t *testing.T) {
	err := validatePin("12345")
	require.Error(t, err)
}

func TestValidatePin_RejectsNonDigits(t *testing.T) {
	err := validatePin("12ab")
	require.Error(t, err)
}

func TestValidatePin_RejectsEmpty(t *testing.T) {
	err := validatePin("")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// enforceOwnership
// ---------------------------------------------------------------------------

func TestEnforceOwnership_Match_ReturnsNil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("principal_type", "client")
	c.Set("principal_id", int64(42))

	err := enforceOwnership(c, 42)
	require.NoError(t, err)
	require.Equal(t, 200, w.Code, "no response should be written on match")
}

func TestEnforceOwnership_Mismatch_Writes404(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("principal_type", "client")
	c.Set("principal_id", int64(42))

	err := enforceOwnership(c, 99)
	require.Error(t, err)
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "not_found")
}

func TestEnforceOwnership_EmployeeBypass_ReturnsNil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("principal_type", "employee")
	c.Set("principal_id", int64(1))

	err := enforceOwnership(c, 9999)
	require.NoError(t, err, "employees must bypass ownership check")
	require.Equal(t, 200, w.Code)
}

func TestEnforceOwnership_MissingSystemType_ReturnsNil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	// No system_type set — treat as employee (bypass).
	err := enforceOwnership(c, 9999)
	require.NoError(t, err)
	require.Equal(t, 200, w.Code)
}

// ---------------------------------------------------------------------------
// owner-type-schema legacy converters
// ---------------------------------------------------------------------------

// TestOwnerToLegacyUserID_NilIsBank locks the contract that a bank owner
// (OwnerID==nil on ResolvedIdentity) flattens to the legacy uint64 sentinel
// 0 used by stock-service proto request shapes pending Task 9 of the
// 2026-04-27 owner-type-schema plan.
func TestOwnerToLegacyUserID_NilIsBank(t *testing.T) {
	require.Equal(t, uint64(0), ownerToLegacyUserID(nil),
		"bank owner has nil OwnerID; legacy form is 0")
}

func TestOwnerToLegacyUserID_PtrPassThrough(t *testing.T) {
	v := uint64(42)
	require.Equal(t, uint64(42), ownerToLegacyUserID(&v),
		"client/owner ID flattens to its uint64 value")
}

func TestOwnerToLegacySystemType_PassThrough(t *testing.T) {
	require.Equal(t, "client", ownerToLegacySystemType("client"))
	require.Equal(t, "bank", ownerToLegacySystemType("bank"))
}

func TestDerefU64Ptr_NilIsZero(t *testing.T) {
	require.Equal(t, uint64(0), derefU64Ptr(nil))
}

func TestDerefU64Ptr_PtrPassThrough(t *testing.T) {
	v := uint64(11)
	require.Equal(t, uint64(11), derefU64Ptr(&v))
}
