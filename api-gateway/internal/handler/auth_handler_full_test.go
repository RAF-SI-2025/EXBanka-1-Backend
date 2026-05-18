// api-gateway/internal/handler/auth_handler_full_test.go
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

	authpb "github.com/exbanka/contract/authpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func authRouter(h *handler.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", h.Login)
	r.POST("/refresh", h.RefreshToken)
	r.POST("/password-reset", h.RequestPasswordReset)
	r.POST("/reset-password", h.ResetPassword)
	r.POST("/activate", h.ActivateAccount)
	r.POST("/resend", h.ResendActivationEmail)
	r.POST("/logout", h.Logout)
	return r
}

func TestAuthLogin_Success(t *testing.T) {
	auth := &stubAuthClient{
		loginFn: func(req *authpb.LoginRequest) (*authpb.LoginResponse, error) {
			require.Equal(t, "user@example.com", req.Email)
			return &authpb.LoginResponse{AccessToken: "AT", RefreshToken: "RT"}, nil
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)

	body := `{"email":"user@example.com","password":"P@ss12345"}`
	req := httptest.NewRequest("POST", "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "AT")
	require.Contains(t, rec.Body.String(), "RT")
}

func TestAuthLogin_BadJSON(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/login", strings.NewReader(`{garbage`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthLogin_GRPCError_MapsToHTTPStatus(t *testing.T) {
	auth := &stubAuthClient{
		loginFn: func(_ *authpb.LoginRequest) (*authpb.LoginResponse, error) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/login", strings.NewReader(`{"email":"a@b.com","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "unauthorized")
}

func TestAuthRefresh_Success(t *testing.T) {
	auth := &stubAuthClient{
		refreshFn: func(_ *authpb.RefreshTokenRequest) (*authpb.RefreshTokenResponse, error) {
			return &authpb.RefreshTokenResponse{AccessToken: "new-AT", RefreshToken: "new-RT"}, nil
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/refresh", strings.NewReader(`{"refresh_token":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "new-AT")
}

func TestAuthRefresh_MissingToken(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/refresh", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthRequestPasswordReset_AlwaysReturnsOK(t *testing.T) {
	// Even when grpc fails (which the handler ignores), the response is 200 by spec.
	auth := &stubAuthClient{
		requestPasswordResetFn: func(_ *authpb.PasswordResetRequest) (*authpb.PasswordResetResponse, error) {
			return nil, status.Error(codes.NotFound, "no such email")
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/password-reset", strings.NewReader(`{"email":"x@y.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "if the email exists")
}

func TestAuthRequestPasswordReset_BadEmail(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/password-reset", strings.NewReader(`{"email":"not-an-email"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthResetPassword_Success(t *testing.T) {
	auth := &stubAuthClient{
		resetPasswordFn: func(_ *authpb.ResetPasswordRequest) (*authpb.ResetPasswordResponse, error) {
			return &authpb.ResetPasswordResponse{}, nil
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)
	body := `{"token":"abc","new_password":"NewP@ss12","confirm_password":"NewP@ss12"}`
	req := httptest.NewRequest("POST", "/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "password reset successfully")
}

func TestAuthResetPassword_GRPCError(t *testing.T) {
	auth := &stubAuthClient{
		resetPasswordFn: func(_ *authpb.ResetPasswordRequest) (*authpb.ResetPasswordResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "weak password")
		},
	}
	h := handler.NewAuthHandler(auth)
	r := authRouter(h)
	body := `{"token":"abc","new_password":"x","confirm_password":"x"}`
	req := httptest.NewRequest("POST", "/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "validation_error")
}

func TestAuthActivateAccount_Success(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	body := `{"token":"abc","password":"P@ss12345","confirm_password":"P@ss12345"}`
	req := httptest.NewRequest("POST", "/activate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "account activated successfully")
}

func TestAuthActivateAccount_MissingFields(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/activate", strings.NewReader(`{"token":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthResendActivation_Success(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/resend", strings.NewReader(`{"email":"x@y.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "if the email is registered")
}

func TestAuthResendActivation_BadEmail(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/resend", strings.NewReader(`{"email":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthLogout_Success(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/logout", strings.NewReader(`{"refresh_token":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "logged out successfully")
}

func TestAuthLogout_MissingToken(t *testing.T) {
	h := handler.NewAuthHandler(&stubAuthClient{})
	r := authRouter(h)
	req := httptest.NewRequest("POST", "/logout", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
