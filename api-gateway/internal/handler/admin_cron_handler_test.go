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

	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/handler"
	gatewaykafka "github.com/exbanka/api-gateway/internal/kafka"
	adminpb "github.com/exbanka/contract/adminpb"
)

// fakeAdminCron implements adminpb.AdminCronClient with per-method fn fields.
type fakeAdminCron struct {
	listFn    func(*adminpb.ListCronsRequest) (*adminpb.ListCronsResponse, error)
	getFn     func(*adminpb.GetCronRequest) (*adminpb.CronInfoMsg, error)
	triggerFn func(*adminpb.TriggerRequest) (*adminpb.CronCtrlResponse, error)
	pauseFn   func(*adminpb.PauseRequest) (*adminpb.CronCtrlResponse, error)
	resumeFn  func(*adminpb.ResumeRequest) (*adminpb.CronCtrlResponse, error)
}

func (f *fakeAdminCron) ListCrons(_ context.Context, in *adminpb.ListCronsRequest, _ ...grpc.CallOption) (*adminpb.ListCronsResponse, error) {
	if f.listFn != nil {
		return f.listFn(in)
	}
	return &adminpb.ListCronsResponse{}, nil
}
func (f *fakeAdminCron) GetCron(_ context.Context, in *adminpb.GetCronRequest, _ ...grpc.CallOption) (*adminpb.CronInfoMsg, error) {
	if f.getFn != nil {
		return f.getFn(in)
	}
	return &adminpb.CronInfoMsg{}, nil
}
func (f *fakeAdminCron) TriggerCron(_ context.Context, in *adminpb.TriggerRequest, _ ...grpc.CallOption) (*adminpb.CronCtrlResponse, error) {
	if f.triggerFn != nil {
		return f.triggerFn(in)
	}
	return &adminpb.CronCtrlResponse{}, nil
}
func (f *fakeAdminCron) PauseCron(_ context.Context, in *adminpb.PauseRequest, _ ...grpc.CallOption) (*adminpb.CronCtrlResponse, error) {
	if f.pauseFn != nil {
		return f.pauseFn(in)
	}
	return &adminpb.CronCtrlResponse{}, nil
}
func (f *fakeAdminCron) ResumeCron(_ context.Context, in *adminpb.ResumeRequest, _ ...grpc.CallOption) (*adminpb.CronCtrlResponse, error) {
	if f.resumeFn != nil {
		return f.resumeFn(in)
	}
	return &adminpb.CronCtrlResponse{}, nil
}

var _ adminpb.AdminCronClient = (*fakeAdminCron)(nil)

// deadAuditPub is an AuditProducer pointed at an unreachable broker. Its
// publishes fail fast (connection refused, ~1ms) and are swallowed, so the
// handler's best-effort audit emit does not block the HTTP response.
func deadAuditPub() *gatewaykafka.AuditProducer {
	return gatewaykafka.NewAuditProducer("127.0.0.1:1")
}

func adminCronRouter(clients []*grpcclients.AdminCronClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewAdminCronHandler(clients, deadAuditPub())
	r := gin.New()
	emp := func(c *gin.Context) { c.Set("principal_id", int64(7)) }
	r.GET("/api/v3/admin/crons", emp, h.List)
	r.GET("/api/v3/admin/crons/:service/:name", emp, h.Get)
	r.POST("/api/v3/admin/crons/:service/:name/trigger", emp, h.Trigger)
	r.POST("/api/v3/admin/crons/:service/:name/pause", emp, h.Pause)
	r.POST("/api/v3/admin/crons/:service/:name/resume", emp, h.Resume)
	return r
}

func acDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestAdminCron_List_OkPlusUnreachablePlusNil(t *testing.T) {
	okClient := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		listFn: func(*adminpb.ListCronsRequest) (*adminpb.ListCronsResponse, error) {
			return &adminpb.ListCronsResponse{Crons: []*adminpb.CronInfoMsg{
				{Name: "tax-collection", Service: "stock-service", RunCount: 3},
			}}, nil
		},
	}}
	errClient := &grpcclients.AdminCronClient{Service: "credit-service", Client: &fakeAdminCron{
		listFn: func(*adminpb.ListCronsRequest) (*adminpb.ListCronsResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{okClient, errClient, nil})
	rec := acDo(r, "GET", "/api/v3/admin/crons", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `"services"`)
	require.Contains(t, body, `"status":"ok"`)
	require.Contains(t, body, `"status":"unreachable"`)
	require.Contains(t, body, `"tax-collection"`)
}

func TestAdminCron_Get_Success(t *testing.T) {
	var captured *adminpb.GetCronRequest
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		getFn: func(in *adminpb.GetCronRequest) (*adminpb.CronInfoMsg, error) {
			captured = in
			return &adminpb.CronInfoMsg{Name: in.Name, Service: "stock-service", IsPaused: true}, nil
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "GET", "/api/v3/admin/crons/stock-service/tax-collection", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "tax-collection", captured.Name)
	require.Contains(t, rec.Body.String(), `"is_paused":true`)
}

func TestAdminCron_Get_ServiceNotFound(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "GET", "/api/v3/admin/crons/unknown-service/x", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "service not found")
}

func TestAdminCron_Get_GRPCError(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		getFn: func(*adminpb.GetCronRequest) (*adminpb.CronInfoMsg, error) {
			return nil, status.Error(codes.NotFound, "no cron")
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "GET", "/api/v3/admin/crons/stock-service/nope", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminCron_Trigger_Success(t *testing.T) {
	var captured *adminpb.TriggerRequest
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		triggerFn: func(in *adminpb.TriggerRequest) (*adminpb.CronCtrlResponse, error) {
			captured = in
			return &adminpb.CronCtrlResponse{Status: "triggered"}, nil
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/stock-service/tax-collection/trigger", `{"force":true,"reason":"manual run"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "tax-collection", captured.Name)
	require.True(t, captured.Force)
	require.Equal(t, int64(7), captured.TriggeredBy)
	require.Contains(t, rec.Body.String(), `"status":"triggered"`)
}

func TestAdminCron_Trigger_ServiceNotFound(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/ghost/x/trigger", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminCron_Trigger_GRPCError(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		triggerFn: func(*adminpb.TriggerRequest) (*adminpb.CronCtrlResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/stock-service/tax/trigger", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAdminCron_Pause_Success(t *testing.T) {
	var captured *adminpb.PauseRequest
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		pauseFn: func(in *adminpb.PauseRequest) (*adminpb.CronCtrlResponse, error) {
			captured = in
			return &adminpb.CronCtrlResponse{Status: "paused"}, nil
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/stock-service/tax/pause", `{"reason":"maint"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(7), captured.PausedBy)
	require.Contains(t, rec.Body.String(), `"status":"paused"`)
}

func TestAdminCron_Pause_ServiceNotFound(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/ghost/x/pause", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminCron_Resume_Success(t *testing.T) {
	var captured *adminpb.ResumeRequest
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		resumeFn: func(in *adminpb.ResumeRequest) (*adminpb.CronCtrlResponse, error) {
			captured = in
			return &adminpb.CronCtrlResponse{Status: "running"}, nil
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/stock-service/tax/resume", `{"reason":"done"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(7), captured.ResumedBy)
	require.Contains(t, rec.Body.String(), `"status":"running"`)
}

func TestAdminCron_Resume_GRPCError(t *testing.T) {
	cl := &grpcclients.AdminCronClient{Service: "stock-service", Client: &fakeAdminCron{
		resumeFn: func(*adminpb.ResumeRequest) (*adminpb.CronCtrlResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "not paused")
		},
	}}
	r := adminCronRouter([]*grpcclients.AdminCronClient{cl})
	rec := acDo(r, "POST", "/api/v3/admin/crons/stock-service/tax/resume", "")
	require.Equal(t, http.StatusConflict, rec.Code)
}
