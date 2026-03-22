package helpers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/exbanka/test-app/internal/client"
)

// RequireStatus asserts the HTTP status code matches.
func RequireStatus(t *testing.T, resp *client.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		t.Fatalf("expected status %d, got %d. Body: %s", expected, resp.StatusCode, string(resp.RawBody))
	}
}

// RequireField asserts a field exists in the response body.
func RequireField(t *testing.T, resp *client.Response, field string) interface{} {
	t.Helper()
	if resp.Body == nil {
		t.Fatalf("response body is nil, expected field %q", field)
	}
	val, ok := resp.Body[field]
	if !ok {
		t.Fatalf("field %q not found in response. Body: %s", field, string(resp.RawBody))
	}
	return val
}

// RequireFieldEquals asserts a field matches the expected value.
func RequireFieldEquals(t *testing.T, resp *client.Response, field string, expected interface{}) {
	t.Helper()
	actual := RequireField(t, resp, field)
	if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected) {
		t.Fatalf("field %q: expected %v, got %v", field, expected, actual)
	}
}

// RequireBodyContains asserts the raw body contains a substring.
func RequireBodyContains(t *testing.T, resp *client.Response, substr string) {
	t.Helper()
	if len(resp.RawBody) == 0 {
		t.Fatalf("response body is empty, expected to contain %q", substr)
	}
	body := string(resp.RawBody)
	if !strings.Contains(body, substr) {
		t.Fatalf("response body does not contain %q. Body: %s", substr, body)
	}
}

// GetStringField extracts a string field from response.
func GetStringField(t *testing.T, resp *client.Response, field string) string {
	t.Helper()
	val := RequireField(t, resp, field)
	s, ok := val.(string)
	if !ok {
		t.Fatalf("field %q is not a string: %v (type %T)", field, val, val)
	}
	return s
}

// GetNumberField extracts a numeric field from response (JSON numbers are float64).
func GetNumberField(t *testing.T, resp *client.Response, field string) float64 {
	t.Helper()
	val := RequireField(t, resp, field)
	n, ok := val.(float64)
	if !ok {
		t.Fatalf("field %q is not a number: %v (type %T)", field, val, val)
	}
	return n
}
