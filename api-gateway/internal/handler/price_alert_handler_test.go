package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

type stubPriceAlertClient struct {
	createFn func(*stockpb.CreatePriceAlertRequest) (*stockpb.PriceAlertResponse, error)
	updateFn func(*stockpb.UpdatePriceAlertRequest) (*stockpb.PriceAlertResponse, error)
	getFn    func(*stockpb.GetPriceAlertRequest) (*stockpb.PriceAlertResponse, error)
	deleteFn func(*stockpb.DeletePriceAlertRequest) (*stockpb.DeletePriceAlertResponse, error)
	listMyFn func(*stockpb.ListMyPriceAlertsRequest) (*stockpb.ListMyPriceAlertsResponse, error)
}

func (s *stubPriceAlertClient) CreateAlert(_ context.Context, in *stockpb.CreatePriceAlertRequest, _ ...grpc.CallOption) (*stockpb.PriceAlertResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &stockpb.PriceAlertResponse{}, nil
}
func (s *stubPriceAlertClient) UpdateAlert(_ context.Context, in *stockpb.UpdatePriceAlertRequest, _ ...grpc.CallOption) (*stockpb.PriceAlertResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	return &stockpb.PriceAlertResponse{}, nil
}
func (s *stubPriceAlertClient) GetAlert(_ context.Context, in *stockpb.GetPriceAlertRequest, _ ...grpc.CallOption) (*stockpb.PriceAlertResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.PriceAlertResponse{}, nil
}
func (s *stubPriceAlertClient) DeleteAlert(_ context.Context, in *stockpb.DeletePriceAlertRequest, _ ...grpc.CallOption) (*stockpb.DeletePriceAlertResponse, error) {
	if s.deleteFn != nil {
		return s.deleteFn(in)
	}
	return &stockpb.DeletePriceAlertResponse{}, nil
}
func (s *stubPriceAlertClient) ListMy(_ context.Context, in *stockpb.ListMyPriceAlertsRequest, _ ...grpc.CallOption) (*stockpb.ListMyPriceAlertsResponse, error) {
	if s.listMyFn != nil {
		return s.listMyFn(in)
	}
	return &stockpb.ListMyPriceAlertsResponse{}, nil
}

var _ stockpb.PriceAlertServiceClient = (*stubPriceAlertClient)(nil)

func priceAlertRouter(h *handler.PriceAlertHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/api/v3/me/price-alerts", withCli, h.Create)
	r.GET("/api/v3/me/price-alerts/:id", withCli, h.Get)
	r.PUT("/api/v3/me/price-alerts/:id", withCli, h.Update)
	r.DELETE("/api/v3/me/price-alerts/:id", withCli, h.Delete)
	r.GET("/api/v3/me/price-alerts", withCli, h.ListMy)
	return r
}

func paDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestPriceAlert_Create_Success(t *testing.T) {
	var captured *stockpb.CreatePriceAlertRequest
	cl := &stubPriceAlertClient{createFn: func(in *stockpb.CreatePriceAlertRequest) (*stockpb.PriceAlertResponse, error) {
		captured = in
		return &stockpb.PriceAlertResponse{Id: 1}, nil
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `{"listing_id":5,"condition":"gte","threshold":"100.0"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, uint64(5), captured.ListingId)
	require.Equal(t, "gte", captured.Condition)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestPriceAlert_Create_BadBody(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `nope`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid body")
}

func TestPriceAlert_Create_BadCondition(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `{"listing_id":5,"condition":"sideways","threshold":"100"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "condition must be one of")
}

func TestPriceAlert_Create_MissingFields(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `{"listing_id":0,"condition":"gte","threshold":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_id and threshold are required")
}

func TestPriceAlert_Create_RecurringCooldownOutOfRange(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `{"listing_id":5,"condition":"gte","threshold":"100","is_recurring":true,"cooldown_seconds":10}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "cooldown_seconds must be in")
}

func TestPriceAlert_Create_GRPCError(t *testing.T) {
	cl := &stubPriceAlertClient{createFn: func(*stockpb.CreatePriceAlertRequest) (*stockpb.PriceAlertResponse, error) {
		return nil, status.Error(codes.NotFound, "no listing")
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "POST", "/api/v3/me/price-alerts", `{"listing_id":5,"condition":"gte","threshold":"100"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPriceAlert_Get_Success(t *testing.T) {
	var captured *stockpb.GetPriceAlertRequest
	cl := &stubPriceAlertClient{getFn: func(in *stockpb.GetPriceAlertRequest) (*stockpb.PriceAlertResponse, error) {
		captured = in
		return &stockpb.PriceAlertResponse{Id: in.Id}, nil
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "GET", "/api/v3/me/price-alerts/9", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(9), captured.Id)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestPriceAlert_Get_BadID(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "GET", "/api/v3/me/price-alerts/abc", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}

func TestPriceAlert_Update_Success(t *testing.T) {
	var captured *stockpb.UpdatePriceAlertRequest
	cl := &stubPriceAlertClient{updateFn: func(in *stockpb.UpdatePriceAlertRequest) (*stockpb.PriceAlertResponse, error) {
		captured = in
		return &stockpb.PriceAlertResponse{Id: in.Id}, nil
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "PUT", "/api/v3/me/price-alerts/3", `{"condition":"lte","threshold":"50","active":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(3), captured.Id)
	require.Equal(t, "lte", captured.Condition)
	require.True(t, captured.Active)
}

func TestPriceAlert_Update_BadID(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "PUT", "/api/v3/me/price-alerts/x", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPriceAlert_Update_BadBody(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "PUT", "/api/v3/me/price-alerts/3", `not-json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid body")
}

func TestPriceAlert_Update_BadCondition(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "PUT", "/api/v3/me/price-alerts/3", `{"condition":"weird"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "condition must be one of")
}

func TestPriceAlert_Delete_Success(t *testing.T) {
	var captured *stockpb.DeletePriceAlertRequest
	cl := &stubPriceAlertClient{deleteFn: func(in *stockpb.DeletePriceAlertRequest) (*stockpb.DeletePriceAlertResponse, error) {
		captured = in
		return &stockpb.DeletePriceAlertResponse{}, nil
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "DELETE", "/api/v3/me/price-alerts/4", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(4), captured.Id)
}

func TestPriceAlert_Delete_BadID(t *testing.T) {
	r := priceAlertRouter(handler.NewPriceAlertHandler(&stubPriceAlertClient{}))
	rec := paDo(r, "DELETE", "/api/v3/me/price-alerts/zz", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPriceAlert_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyPriceAlertsRequest
	cl := &stubPriceAlertClient{listMyFn: func(in *stockpb.ListMyPriceAlertsRequest) (*stockpb.ListMyPriceAlertsResponse, error) {
		captured = in
		return &stockpb.ListMyPriceAlertsResponse{}, nil
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "GET", "/api/v3/me/price-alerts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(42), captured.OwnerId)
	require.Contains(t, rec.Body.String(), `"alerts"`)
}

func TestPriceAlert_ListMy_GRPCError(t *testing.T) {
	cl := &stubPriceAlertClient{listMyFn: func(*stockpb.ListMyPriceAlertsRequest) (*stockpb.ListMyPriceAlertsResponse, error) {
		return nil, status.Error(codes.Internal, "boom")
	}}
	r := priceAlertRouter(handler.NewPriceAlertHandler(cl))
	rec := paDo(r, "GET", "/api/v3/me/price-alerts", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
