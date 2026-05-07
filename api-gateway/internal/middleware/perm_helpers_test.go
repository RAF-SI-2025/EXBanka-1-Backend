// api-gateway/internal/middleware/perm_helpers_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	perms "github.com/exbanka/contract/permissions"
)

// servePerms wires a router that injects the given permissions slice (or
// `nil` to skip injection) and then runs the supplied middleware. Returns the
// recorder after a GET /test.
func servePerms(t *testing.T, mw gin.HandlerFunc, permList interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if permList != nil {
		r.Use(func(c *gin.Context) {
			c.Set("permissions", permList)
			c.Next()
		})
	}
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// RequireAnyPermission
// ---------------------------------------------------------------------------

func TestRequireAnyPermission_AllowsWhenOneMatches(t *testing.T) {
	w := servePerms(t,
		RequireAnyPermission(perms.Permission("foo.bar"), perms.Permission("baz.qux")),
		[]string{"baz.qux"},
	)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAnyPermission_RejectsWhenNoneMatches(t *testing.T) {
	w := servePerms(t,
		RequireAnyPermission(perms.Permission("foo.bar"), perms.Permission("baz.qux")),
		[]string{"other.perm"},
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "insufficient permissions")
}

func TestRequireAnyPermission_RejectsWhenPermissionsKeyMissing(t *testing.T) {
	w := servePerms(t,
		RequireAnyPermission(perms.Permission("foo.bar")),
		nil,
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "no permissions")
}

func TestRequireAnyPermission_RejectsWhenWrongType(t *testing.T) {
	w := servePerms(t,
		RequireAnyPermission(perms.Permission("foo.bar")),
		"not-a-slice",
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "invalid permissions format")
}

// ---------------------------------------------------------------------------
// RequireAllPermissions
// ---------------------------------------------------------------------------

func TestRequireAllPermissions_AllowsWhenAllPresent(t *testing.T) {
	w := servePerms(t,
		RequireAllPermissions(perms.Permission("foo.bar"), perms.Permission("baz.qux")),
		[]string{"foo.bar", "baz.qux", "extra.perm"},
	)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAllPermissions_RejectsWhenOneMissing(t *testing.T) {
	w := servePerms(t,
		RequireAllPermissions(perms.Permission("foo.bar"), perms.Permission("baz.qux")),
		[]string{"foo.bar"},
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "missing permission baz.qux")
}

func TestRequireAllPermissions_RejectsWhenNoneMatches(t *testing.T) {
	w := servePerms(t,
		RequireAllPermissions(perms.Permission("foo.bar")),
		[]string{"other"},
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequireAllPermissions_RejectsWhenKeyMissing(t *testing.T) {
	w := servePerms(t,
		RequireAllPermissions(perms.Permission("foo.bar")),
		nil,
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "no permissions")
}

func TestRequireAllPermissions_RejectsWrongType(t *testing.T) {
	w := servePerms(t,
		RequireAllPermissions(perms.Permission("foo.bar")),
		map[string]string{"not": "slice"},
	)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "invalid permissions format")
}

// ---------------------------------------------------------------------------
// HasPermission
// ---------------------------------------------------------------------------

func TestHasPermission_PresentReturnsTrue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"foo.bar", "baz.qux"})
		c.Next()
	})
	r.GET("/x", func(c *gin.Context) {
		assert.True(t, HasPermission(c, "foo.bar"))
		assert.True(t, HasPermission(c, "baz.qux"))
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHasPermission_AbsentReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"foo.bar"})
		c.Next()
	})
	r.GET("/x", func(c *gin.Context) {
		assert.False(t, HasPermission(c, "missing.perm"))
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHasPermission_NoKeyReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		assert.False(t, HasPermission(c, "foo.bar"))
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHasPermission_WrongTypeReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", "not-a-slice")
		c.Next()
	})
	r.GET("/x", func(c *gin.Context) {
		assert.False(t, HasPermission(c, "foo.bar"))
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// ---------------------------------------------------------------------------
// AuthMiddleware extra branches
// ---------------------------------------------------------------------------

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	client := &mockAuthClient{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(client))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_BadHeaderFormat(t *testing.T) {
	client := &mockAuthClient{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(client))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Token abc")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAnyAuthMiddleware_MissingHeader(t *testing.T) {
	client := &mockAuthClient{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AnyAuthMiddleware(client))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAnyAuthMiddleware_BadHeaderFormat(t *testing.T) {
	client := &mockAuthClient{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AnyAuthMiddleware(client))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "BadHeader")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
