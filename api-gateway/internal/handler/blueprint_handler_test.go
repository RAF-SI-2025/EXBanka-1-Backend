// api-gateway/internal/handler/blueprint_handler_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	userpb "github.com/exbanka/contract/userpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func blueprintRouter(h *handler.BlueprintHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(1)) }
	r.GET("/blueprints", withCtx, h.ListBlueprints)
	r.POST("/blueprints", withCtx, h.CreateBlueprint)
	r.GET("/blueprints/:id", withCtx, h.GetBlueprint)
	r.PUT("/blueprints/:id", withCtx, h.UpdateBlueprint)
	r.DELETE("/blueprints/:id", withCtx, h.DeleteBlueprint)
	r.POST("/blueprints/:id/apply", withCtx, h.ApplyBlueprint)
	return r
}

func TestBlueprint_ListBlueprints(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("GET", "/blueprints", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestBlueprint_ListBlueprints_BadType(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("GET", "/blueprints?type=foo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_CreateBlueprint_BadType(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	body := `{"name":"x","type":"weird","values":{}}`
	req := httptest.NewRequest("POST", "/blueprints", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_ApplyBlueprint_BadTargetID(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	body := `{"target_id":-1}`
	req := httptest.NewRequest("POST", "/blueprints/1/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_CreateBlueprint_Success(t *testing.T) {
	bp := &stubBlueprintClient{
		createFn: func(req *userpb.CreateBlueprintRequest) (*userpb.BlueprintResponse, error) {
			require.Equal(t, "MyBP", req.Name)
			return &userpb.BlueprintResponse{Id: 1, Name: req.Name}, nil
		},
	}
	h := handler.NewBlueprintHandler(bp)
	r := blueprintRouter(h)
	body := `{"name":"MyBP","description":"x","type":"employee","values":{"key":"v"}}`
	req := httptest.NewRequest("POST", "/blueprints", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestBlueprint_CreateBlueprint_BadJSON(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("POST", "/blueprints", strings.NewReader(`{garbage`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_GetBlueprint_Success(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("GET", "/blueprints/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestBlueprint_GetBlueprint_BadID(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("GET", "/blueprints/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_UpdateBlueprint_Success(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	body := `{"name":"NewName","description":"new","permission_codes":["a","b"]}`
	req := httptest.NewRequest("PUT", "/blueprints/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestBlueprint_UpdateBlueprint_BadID(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("PUT", "/blueprints/abc", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_DeleteBlueprint_Success(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("DELETE", "/blueprints/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestBlueprint_DeleteBlueprint_BadID(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("DELETE", "/blueprints/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlueprint_ApplyBlueprint_Success(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	body := `{"target_id":42}`
	req := httptest.NewRequest("POST", "/blueprints/1/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestBlueprint_ApplyBlueprint_BadID(t *testing.T) {
	h := handler.NewBlueprintHandler(&stubBlueprintClient{})
	r := blueprintRouter(h)
	req := httptest.NewRequest("POST", "/blueprints/abc/apply", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
