// api-gateway/internal/handler/auth_handler_test.go
package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestLogin_MissingFields(t *testing.T) {
	r := setupTestRouter()
	body := map[string]string{}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/auth/login", func(c *gin.Context) {
		var loginReq loginRequest
		if err := c.ShouldBindJSON(&loginReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errMsg, ok := resp["error"].(string)
	assert.True(t, ok, "response should contain an 'error' string")
	assert.Contains(t, errMsg, "Email", "error should mention missing Email field")
	assert.Contains(t, errMsg, "Password", "error should mention missing Password field")
}

func TestLogin_InvalidEmail(t *testing.T) {
	r := setupTestRouter()
	body := map[string]string{"email": "not-an-email", "password": "pass"}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/auth/login", func(c *gin.Context) {
		var loginReq loginRequest
		if err := c.ShouldBindJSON(&loginReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errMsg, ok := resp["error"].(string)
	assert.True(t, ok, "response should contain an 'error' string")
	assert.Contains(t, errMsg, "email", "error should mention the email field")
}

func TestRefreshToken_MissingField(t *testing.T) {
	r := setupTestRouter()
	body := map[string]string{}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/refresh", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/auth/refresh", func(c *gin.Context) {
		var refreshReq refreshRequest
		if err := c.ShouldBindJSON(&refreshReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errMsg, ok := resp["error"].(string)
	assert.True(t, ok, "response should contain an 'error' string")
	assert.Contains(t, errMsg, "RefreshToken", "error should mention missing RefreshToken field")
}

func TestActivateAccount_MissingFields(t *testing.T) {
	r := setupTestRouter()
	body := map[string]string{"token": "abc"}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/auth/activate", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/auth/activate", func(c *gin.Context) {
		var activateReq activateRequest
		if err := c.ShouldBindJSON(&activateReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	})
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errMsg, ok := resp["error"].(string)
	assert.True(t, ok, "response should contain an 'error' string")
	assert.Contains(t, errMsg, "Password", "error should mention missing Password field")
	assert.Contains(t, errMsg, "ConfirmPassword", "error should mention missing ConfirmPassword field")
}
