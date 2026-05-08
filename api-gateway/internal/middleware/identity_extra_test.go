package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func runReadPrincipal(t *testing.T, set func(c *gin.Context)) uint64 {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	set(c)
	return readPrincipalID(c)
}

func TestReadPrincipalID_AllBranches(t *testing.T) {
	cases := []struct {
		name string
		set  func(*gin.Context)
		want uint64
	}{
		{"missing", func(c *gin.Context) {}, 0},
		{"uint64", func(c *gin.Context) { c.Set("principal_id", uint64(7)) }, 7},
		{"int64-pos", func(c *gin.Context) { c.Set("principal_id", int64(8)) }, 8},
		{"int64-zero", func(c *gin.Context) { c.Set("principal_id", int64(0)) }, 0},
		{"int64-neg", func(c *gin.Context) { c.Set("principal_id", int64(-3)) }, 0},
		{"int-pos", func(c *gin.Context) { c.Set("principal_id", int(9)) }, 9},
		{"int-zero", func(c *gin.Context) { c.Set("principal_id", int(0)) }, 0},
		{"int-neg", func(c *gin.Context) { c.Set("principal_id", int(-2)) }, 0},
		{"unknown-type", func(c *gin.Context) { c.Set("principal_id", "string") }, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadPrincipal(t, tc.set)
			if got != tc.want {
				t.Fatalf("%s: got %d want %d", tc.name, got, tc.want)
			}
		})
	}
}
