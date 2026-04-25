package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	notificationpb "github.com/exbanka/contract/notificationpb"
)

func notificationRouter(h *handler.NotificationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Set("system_type", "client")
	}
	r.GET("/api/v2/me/notifications", withCtx, h.ListNotifications)
	r.GET("/api/v2/me/notifications/unread-count", withCtx, h.GetUnreadCount)
	r.POST("/api/v2/me/notifications/:id/read", withCtx, h.MarkRead)
	r.POST("/api/v2/me/notifications/read-all", withCtx, h.MarkAllRead)
	return r
}

func TestNotification_List_Default(t *testing.T) {
	st := &stubNotificationClient{
		listFn: func(req *notificationpb.ListNotificationsRequest) (*notificationpb.ListNotificationsResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(20), req.PageSize)
			return &notificationpb.ListNotificationsResponse{
				Notifications: []*notificationpb.NotificationEntry{{Id: 1, Title: "hi"}},
				Total:         1,
			}, nil
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/notifications", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total":1`)
	require.Contains(t, rec.Body.String(), `"hi"`)
}

func TestNotification_List_FilterByRead(t *testing.T) {
	st := &stubNotificationClient{
		listFn: func(req *notificationpb.ListNotificationsRequest) (*notificationpb.ListNotificationsResponse, error) {
			require.Equal(t, "unread", req.ReadFilter)
			return &notificationpb.ListNotificationsResponse{}, nil
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/notifications?read=unread", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestNotification_List_GRPCError(t *testing.T) {
	st := &stubNotificationClient{
		listFn: func(*notificationpb.ListNotificationsRequest) (*notificationpb.ListNotificationsResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/notifications", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestNotification_GetUnreadCount(t *testing.T) {
	st := &stubNotificationClient{
		unreadFn: func(req *notificationpb.GetUnreadCountRequest) (*notificationpb.GetUnreadCountResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			return &notificationpb.GetUnreadCountResponse{Count: 7}, nil
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/notifications/unread-count", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"unread_count":7`)
}

func TestNotification_GetUnreadCount_GRPCError(t *testing.T) {
	st := &stubNotificationClient{
		unreadFn: func(*notificationpb.GetUnreadCountRequest) (*notificationpb.GetUnreadCountResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/notifications/unread-count", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestNotification_MarkRead_Success(t *testing.T) {
	st := &stubNotificationClient{
		markReadFn: func(req *notificationpb.MarkNotificationReadRequest) (*notificationpb.MarkNotificationReadResponse, error) {
			require.Equal(t, uint64(11), req.Id)
			require.Equal(t, uint64(42), req.UserId)
			return &notificationpb.MarkNotificationReadResponse{}, nil
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/notifications/11/read", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":true`)
}

func TestNotification_MarkRead_BadID(t *testing.T) {
	h := handler.NewNotificationHandler(&stubNotificationClient{})
	r := notificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/notifications/abc/read", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid notification id")
}

func TestNotification_MarkRead_NotFound(t *testing.T) {
	st := &stubNotificationClient{
		markReadFn: func(*notificationpb.MarkNotificationReadRequest) (*notificationpb.MarkNotificationReadResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/notifications/11/read", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestNotification_MarkAllRead(t *testing.T) {
	st := &stubNotificationClient{
		markAllReadFn: func(req *notificationpb.MarkAllNotificationsReadRequest) (*notificationpb.MarkAllNotificationsReadResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			return &notificationpb.MarkAllNotificationsReadResponse{Count: 5}, nil
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/notifications/read-all", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"count":5`)
}

func TestNotification_MarkAllRead_GRPCError(t *testing.T) {
	st := &stubNotificationClient{
		markAllReadFn: func(*notificationpb.MarkAllNotificationsReadRequest) (*notificationpb.MarkAllNotificationsReadResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewNotificationHandler(st)
	r := notificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/me/notifications/read-all", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
