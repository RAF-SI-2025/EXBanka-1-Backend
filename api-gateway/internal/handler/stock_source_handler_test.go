package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

type stubSourceAdminClient struct {
	switchResp *stockpb.SwitchSourceResponse
	switchErr  error
	statusResp *stockpb.SourceStatus
	lastSrcReq string
}

func (s *stubSourceAdminClient) SwitchSource(ctx context.Context, req *stockpb.SwitchSourceRequest, opts ...grpc.CallOption) (*stockpb.SwitchSourceResponse, error) {
	s.lastSrcReq = req.Source
	return s.switchResp, s.switchErr
}

func (s *stubSourceAdminClient) GetSourceStatus(ctx context.Context, req *stockpb.GetSourceStatusRequest, opts ...grpc.CallOption) (*stockpb.SourceStatus, error) {
	return s.statusResp, nil
}

func TestStockSource_Switch_Valid(t *testing.T) {
	client := &stubSourceAdminClient{
		switchResp: &stockpb.SwitchSourceResponse{Status: &stockpb.SourceStatus{
			Source: "generated", Status: "idle", StartedAt: "2026-04-13T12:00:00Z",
		}},
	}
	h := handler.NewStockSourceHandler(client)
	router := gin.New()
	router.POST("/api/v1/admin/stock-source", h.SwitchSource)

	body := bytes.NewBufferString(`{"source":"generated"}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/stock-source", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, "generated", client.lastSrcReq)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "generated", got["source"])
}

func TestStockSource_Switch_RejectsInvalidSource(t *testing.T) {
	client := &stubSourceAdminClient{}
	h := handler.NewStockSourceHandler(client)
	router := gin.New()
	router.POST("/api/v1/admin/stock-source", h.SwitchSource)

	req := httptest.NewRequest("POST", "/api/v1/admin/stock-source", bytes.NewBufferString(`{"source":"garbage"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestStockSource_GetStatus(t *testing.T) {
	client := &stubSourceAdminClient{
		statusResp: &stockpb.SourceStatus{Source: "simulator", Status: "reseeding", StartedAt: "2026-04-13T12:00:00Z"},
	}
	h := handler.NewStockSourceHandler(client)
	router := gin.New()
	router.GET("/api/v1/admin/stock-source", h.GetSourceStatus)

	req := httptest.NewRequest("GET", "/api/v1/admin/stock-source", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "simulator", got["source"])
	require.Equal(t, "reseeding", got["status"])
}
