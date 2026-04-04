// api-gateway/internal/middleware/auth_test.go
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequirePermission_NoPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok, "response should contain an 'error' object")
	assert.Equal(t, "forbidden", errObj["code"], "error code should be 'forbidden'")
	assert.Equal(t, "insufficient permissions", errObj["message"], "error message should indicate insufficient permissions")
}

func TestRequirePermission_HasPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"employees.read", "clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	assert.Equal(t, true, resp["ok"], "handler should have executed and returned ok:true")
}

func TestRequirePermission_MissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.create"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok, "response should contain an 'error' object")
	assert.Equal(t, "forbidden", errObj["code"], "error code should be 'forbidden'")
	assert.Equal(t, "insufficient permissions", errObj["message"], "user has clients.read but not employees.create")
}
