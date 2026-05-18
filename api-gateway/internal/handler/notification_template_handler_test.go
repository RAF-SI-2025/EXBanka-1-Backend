package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	notificationpb "github.com/exbanka/contract/notificationpb"
)

func ntRouter(h *handler.NotificationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(99)); c.Next() }
	r.GET("/notification-templates", withCtx, h.ListNotificationTemplates)
	r.GET("/notification-templates/:channel/:type", withCtx, h.GetNotificationTemplate)
	r.PUT("/notification-templates/:channel/:type", withCtx, h.SetNotificationTemplate)
	r.DELETE("/notification-templates/:channel/:type", withCtx, h.ResetNotificationTemplate)
	return r
}

func TestNotificationTemplate_List(t *testing.T) {
	cl := &stubNotificationClient{listTplFn: func(in *notificationpb.ListTemplatesRequest) (*notificationpb.ListTemplatesResponse, error) {
		require.Equal(t, "email", in.Channel)
		return &notificationpb.ListTemplatesResponse{Templates: []*notificationpb.TemplateInfo{
			{Type: "ACTIVATION", Channel: "email", Variables: []*notificationpb.TemplateVariable{{Name: "first_name"}}},
		}}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/notification-templates?channel=email", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"first_name"`)
}

func TestNotificationTemplate_List_BadChannel(t *testing.T) {
	r := ntRouter(handler.NewNotificationHandler(&stubNotificationClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/notification-templates?channel=sms", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Set_Success(t *testing.T) {
	cl := &stubNotificationClient{setTplFn: func(in *notificationpb.SetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		require.Equal(t, "CONFIRMATION", in.Type)
		require.Equal(t, "email", in.Channel)
		require.Equal(t, uint64(99), in.UpdatedBy)
		return &notificationpb.TemplateInfo{Type: in.Type, IsCustomized: true}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi","body":"Hello {{first_name}}"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestNotificationTemplate_Set_MissingFields(t *testing.T) {
	r := ntRouter(handler.NewNotificationHandler(&stubNotificationClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Set_GRPCValidationError(t *testing.T) {
	cl := &stubNotificationClient{setTplFn: func(*notificationpb.SetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		return nil, status.Error(codes.InvalidArgument, "unknown variables [frist_name]")
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi","body":"{{frist_name}}"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Reset(t *testing.T) {
	cl := &stubNotificationClient{resetTplFn: func(in *notificationpb.ResetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		return &notificationpb.TemplateInfo{Type: in.Type, IsCustomized: false}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/notification-templates/email/CONFIRMATION", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
