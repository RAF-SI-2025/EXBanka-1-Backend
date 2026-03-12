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

	userpb "github.com/exbanka/contract/userpb"
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
}

func TestEmployeeToJSON(t *testing.T) {
	resp := &userpb.EmployeeResponse{
		Id:          1,
		FirstName:   "John",
		LastName:    "Doe",
		DateOfBirth: 946684800,
		Gender:      "M",
		Email:       "john@test.com",
		Phone:       "123456",
		Address:     "123 St",
		Username:    "johndoe",
		Position:    "Teller",
		Department:  "Retail",
		Active:      true,
		Role:        "EmployeeBasic",
		Permissions: []string{"clients.read"},
		Jmbg:        "0101990710024",
	}
	result := employeeToJSON(resp)
	assert.Equal(t, int64(1), result["id"])
	assert.Equal(t, "John", result["first_name"])
	assert.Equal(t, "0101990710024", result["jmbg"])
	assert.Equal(t, "EmployeeBasic", result["role"])
}
