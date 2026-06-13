package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/version"
)

func TestVersionHandler_GetVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v3/version", handler.NewVersionHandler().GetVersion)

	req := httptest.NewRequest(http.MethodGet, "/api/v3/version", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// The endpoint reports whatever version is baked into the binary; in a
	// plain `go test` build that is the in-package default.
	require.Equal(t, version.Version, body["version"])
	require.NotEmpty(t, body["version"])
}
