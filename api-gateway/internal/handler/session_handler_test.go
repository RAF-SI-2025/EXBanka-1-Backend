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
	authpb "github.com/exbanka/contract/authpb"
)

func sessionRouter(h *handler.SessionHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Set("email", "user@x.com")
	}
	r.GET("/api/v2/me/sessions", withCtx, h.ListMySessions)
	r.POST("/api/v2/me/sessions/revoke", withCtx, h.RevokeSession)
	r.POST("/api/v2/me/sessions/revoke-others", withCtx, h.RevokeAllSessions)
	r.GET("/api/v2/me/login-history", withCtx, h.GetMyLoginHistory)
	return r
}

func TestSession_ListMySessions_Success(t *testing.T) {
	st := &stubAuthClient{
		listSessionsFn: func(req *authpb.ListSessionsRequest) (*authpb.ListSessionsResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			return &authpb.ListSessionsResponse{
				Sessions: []*authpb.SessionInfo{{Id: 1, IsCurrent: true}},
			}, nil
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/sessions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"is_current":true`)
}

func TestSession_ListMySessions_BadUserContext(t *testing.T) {
	h := handler.NewSessionHandler(&stubAuthClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/sessions", func(c *gin.Context) {
		c.Set("user_id", "not-an-int") // wrong type
		h.ListMySessions(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/sessions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSession_ListMySessions_GRPCError(t *testing.T) {
	st := &stubAuthClient{
		listSessionsFn: func(*authpb.ListSessionsRequest) (*authpb.ListSessionsResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/sessions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSession_RevokeSession_Success(t *testing.T) {
	st := &stubAuthClient{
		revokeSessionFn: func(req *authpb.RevokeSessionRequest) (*authpb.RevokeSessionResponse, error) {
			require.Equal(t, int64(7), req.SessionId)
			require.Equal(t, int64(42), req.CallerUserId)
			return &authpb.RevokeSessionResponse{}, nil
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	body := `{"session_id":7}`
	req := httptest.NewRequest("POST", "/api/v2/me/sessions/revoke", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "session revoked successfully")
}

func TestSession_RevokeSession_BadBody(t *testing.T) {
	h := handler.NewSessionHandler(&stubAuthClient{})
	r := sessionRouter(h)
	body := `{}` // session_id required
	req := httptest.NewRequest("POST", "/api/v2/me/sessions/revoke", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSession_RevokeSession_NotFound(t *testing.T) {
	st := &stubAuthClient{
		revokeSessionFn: func(*authpb.RevokeSessionRequest) (*authpb.RevokeSessionResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	body := `{"session_id":7}`
	req := httptest.NewRequest("POST", "/api/v2/me/sessions/revoke", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSession_RevokeAllSessions_Success(t *testing.T) {
	st := &stubAuthClient{
		revokeAllFn: func(req *authpb.RevokeAllSessionsRequest) (*authpb.RevokeAllSessionsResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			require.Equal(t, "rt-token", req.CurrentRefreshToken)
			return &authpb.RevokeAllSessionsResponse{}, nil
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	body := `{"current_refresh_token":"rt-token"}`
	req := httptest.NewRequest("POST", "/api/v2/me/sessions/revoke-others", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "all other sessions revoked")
}

func TestSession_RevokeAllSessions_BadBody(t *testing.T) {
	h := handler.NewSessionHandler(&stubAuthClient{})
	r := sessionRouter(h)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/me/sessions/revoke-others", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSession_GetMyLoginHistory_Success(t *testing.T) {
	st := &stubAuthClient{
		loginHistoryFn: func(req *authpb.LoginHistoryRequest) (*authpb.LoginHistoryResponse, error) {
			require.Equal(t, "user@x.com", req.Email)
			require.Equal(t, int32(50), req.Limit)
			return &authpb.LoginHistoryResponse{
				Entries: []*authpb.LoginHistoryEntry{{Id: 1, Success: true}},
			}, nil
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/login-history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":true`)
}

func TestSession_GetMyLoginHistory_CustomLimit(t *testing.T) {
	st := &stubAuthClient{
		loginHistoryFn: func(req *authpb.LoginHistoryRequest) (*authpb.LoginHistoryResponse, error) {
			require.Equal(t, int32(20), req.Limit)
			return &authpb.LoginHistoryResponse{}, nil
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/login-history?limit=20", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSession_GetMyLoginHistory_MissingEmail(t *testing.T) {
	h := handler.NewSessionHandler(&stubAuthClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/login-history", func(c *gin.Context) {
		c.Set("user_id", int64(42))
		// Don't set email
		h.GetMyLoginHistory(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/login-history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSession_GetMyLoginHistory_GRPCError(t *testing.T) {
	st := &stubAuthClient{
		loginHistoryFn: func(*authpb.LoginHistoryRequest) (*authpb.LoginHistoryResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewSessionHandler(st)
	r := sessionRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/me/login-history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
