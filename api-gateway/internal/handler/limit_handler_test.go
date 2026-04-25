// api-gateway/internal/handler/limit_handler_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/api-gateway/internal/handler"
)

func limitRouter(h *handler.LimitHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("user_id", int64(1)) }
	r.GET("/employees/:id/limits", withCtx, h.GetEmployeeLimits)
	r.PUT("/employees/:id/limits", withCtx, h.SetEmployeeLimits)
	r.POST("/employees/:id/limits/template", withCtx, h.ApplyLimitTemplate)
	r.GET("/limits/templates", withCtx, h.ListLimitTemplates)
	r.POST("/limits/templates", withCtx, h.CreateLimitTemplate)
	r.GET("/clients/:id/limits", withCtx, h.GetClientLimits)
	r.PUT("/clients/:id/limits", withCtx, h.SetClientLimits)
	return r
}

func TestLimit_GetEmployeeLimits(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("GET", "/employees/1/limits", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_GetEmployeeLimits_BadID(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("GET", "/employees/abc/limits", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_SetEmployeeLimits_Success(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	body := `{"max_loan_approval_amount":"50000","max_single_transaction":"10000"}`
	req := httptest.NewRequest("PUT", "/employees/1/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_SetEmployeeLimits_BadID(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("PUT", "/employees/abc/limits", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_ApplyLimitTemplate_Success(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	body := `{"template_name":"BasicTeller"}`
	req := httptest.NewRequest("POST", "/employees/1/limits/template", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_ApplyLimitTemplate_MissingTemplateName(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("POST", "/employees/1/limits/template", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_ListLimitTemplates(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("GET", "/limits/templates", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_CreateLimitTemplate_Success(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	body := `{"name":"NewTemplate","description":"x","max_single_transaction":"100"}`
	req := httptest.NewRequest("POST", "/limits/templates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestLimit_CreateLimitTemplate_MissingName(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("POST", "/limits/templates", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_GetClientLimits(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("GET", "/clients/1/limits", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_GetClientLimits_BadID(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("GET", "/clients/abc/limits", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_SetClientLimits_Success(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	body := `{"daily_limit":"100","monthly_limit":"1000","transfer_limit":"50"}`
	req := httptest.NewRequest("PUT", "/clients/1/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestLimit_SetClientLimits_MissingFields(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("PUT", "/clients/1/limits", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLimit_SetClientLimits_BadID(t *testing.T) {
	h := handler.NewLimitHandler(&stubEmployeeLimitClient{}, &stubClientLimitClient{})
	r := limitRouter(h)
	req := httptest.NewRequest("PUT", "/clients/abc/limits", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
