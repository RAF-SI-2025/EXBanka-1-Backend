// api-gateway/internal/handler/employee_handler_test.go
package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCreateEmployee_MissingRequired(t *testing.T) {
	r := setupTestRouter()
	body := map[string]interface{}{
		"first_name": "John",
	}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/employees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/employees", func(c *gin.Context) {
		var createReq createEmployeeRequest
		if err := c.ShouldBindJSON(&createReq); err != nil {
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
	// Body only has first_name; last_name, email, jmbg, username, role, date_of_birth are missing
	assert.Contains(t, errMsg, "LastName", "error should mention missing LastName field")
	assert.Contains(t, errMsg, "Email", "error should mention missing Email field")
	assert.Contains(t, errMsg, "JMBG", "error should mention missing JMBG field")
}

func TestCreateEmployee_InvalidEmail(t *testing.T) {
	r := setupTestRouter()
	body := map[string]interface{}{
		"first_name":    "John",
		"last_name":     "Doe",
		"date_of_birth": 946684800,
		"email":         "not-valid",
		"username":      "johndoe",
		"role":          "EmployeeBasic",
		"jmbg":          "0101990710024",
	}
	jsonBody, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/employees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	r.POST("/api/employees", func(c *gin.Context) {
		var createReq createEmployeeRequest
		if err := c.ShouldBindJSON(&createReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errMsg, ok := resp["error"].(string)
	assert.True(t, ok, "response should contain an 'error' string")
	assert.Contains(t, errMsg, "email", "error should mention the email field validation failure")
}

func TestGetEmployee_InvalidID(t *testing.T) {
	r := setupTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/employees/abc", nil)
	r.GET("/api/employees/:id", func(c *gin.Context) {
		_, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
	assert.Equal(t, "invalid id", errMsg, "error message should be 'invalid id'")
}
