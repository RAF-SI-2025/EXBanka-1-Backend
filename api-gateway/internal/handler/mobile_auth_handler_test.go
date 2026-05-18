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

func mobileRouter(h *handler.MobileAuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(42))
		c.Set("device_id", "dev-1")
	}
	r.POST("/api/v2/mobile/auth/request-activation", h.RequestActivation)
	r.POST("/api/v2/mobile/auth/activate", h.ActivateDevice)
	r.POST("/api/v2/mobile/auth/refresh", h.RefreshMobileToken)
	r.GET("/api/v2/mobile/device", withCtx, h.GetDeviceInfo)
	r.POST("/api/v2/mobile/device/deactivate", withCtx, h.DeactivateDevice)
	r.POST("/api/v2/mobile/device/transfer", withCtx, h.TransferDevice)
	r.POST("/api/v2/mobile/device/biometrics", withCtx, h.SetBiometrics)
	r.GET("/api/v2/mobile/device/biometrics", withCtx, h.GetBiometrics)
	return r
}

func TestMobileAuth_RequestActivation_Success(t *testing.T) {
	st := &stubAuthClient{
		requestMobileFn: func(req *authpb.MobileActivationRequest) (*authpb.MobileActivationResponse, error) {
			require.Equal(t, "x@y.com", req.Email)
			return &authpb.MobileActivationResponse{Success: true, Message: "ok"}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	body := `{"email":"x@y.com"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/request-activation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":true`)
}

func TestMobileAuth_RequestActivation_BadEmail(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{"email":"not-an-email"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/request-activation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMobileAuth_ActivateDevice_Success(t *testing.T) {
	st := &stubAuthClient{
		activateMobileFn: func(req *authpb.ActivateMobileDeviceRequest) (*authpb.ActivateMobileDeviceResponse, error) {
			require.Equal(t, "x@y.com", req.Email)
			require.Equal(t, "123456", req.Code)
			require.Equal(t, "iPhone", req.DeviceName)
			return &authpb.ActivateMobileDeviceResponse{
				AccessToken: "at", RefreshToken: "rt", DeviceId: "d1", DeviceSecret: "s1",
			}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	body := `{"email":"x@y.com","code":"123456","device_name":"iPhone"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/activate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"access_token":"at"`)
}

func TestMobileAuth_ActivateDevice_BadCodeLength(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{"email":"x@y.com","code":"123","device_name":"iPhone"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/activate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "code must be 6 digits")
}

func TestMobileAuth_ActivateDevice_BadBody(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/activate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMobileAuth_RefreshToken_Success(t *testing.T) {
	st := &stubAuthClient{
		refreshMobileFn: func(req *authpb.RefreshMobileTokenRequest) (*authpb.RefreshMobileTokenResponse, error) {
			require.Equal(t, "rt", req.RefreshToken)
			require.Equal(t, "device-xyz", req.DeviceId)
			return &authpb.RefreshMobileTokenResponse{AccessToken: "at2", RefreshToken: "rt2"}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	body := `{"refresh_token":"rt"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", "device-xyz")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"at2"`)
}

func TestMobileAuth_RefreshToken_MissingDeviceID(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{"refresh_token":"rt"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "X-Device-ID header required")
}

func TestMobileAuth_RefreshToken_BadBody(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", "d1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMobileAuth_GetDeviceInfo_Success(t *testing.T) {
	st := &stubAuthClient{
		getDeviceInfoFn: func(req *authpb.GetDeviceInfoRequest) (*authpb.GetDeviceInfoResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			return &authpb.GetDeviceInfoResponse{DeviceName: "iPhone-12", Status: "active"}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/mobile/device", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"iPhone-12"`)
}

func TestMobileAuth_GetDeviceInfo_GRPCError(t *testing.T) {
	st := &stubAuthClient{
		getDeviceInfoFn: func(*authpb.GetDeviceInfoRequest) (*authpb.GetDeviceInfoResponse, error) {
			return nil, status.Error(codes.NotFound, "")
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/mobile/device", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMobileAuth_DeactivateDevice_Success(t *testing.T) {
	st := &stubAuthClient{
		deactivateDeviceFn: func(req *authpb.DeactivateDeviceRequest) (*authpb.DeactivateDeviceResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			require.Equal(t, "dev-1", req.DeviceId)
			return &authpb.DeactivateDeviceResponse{Success: true}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/mobile/device/deactivate", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":true`)
}

func TestMobileAuth_TransferDevice_Success(t *testing.T) {
	st := &stubAuthClient{
		transferDeviceFn: func(req *authpb.TransferDeviceRequest) (*authpb.TransferDeviceResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			require.Equal(t, "x@y.com", req.Email)
			return &authpb.TransferDeviceResponse{Success: true, Message: "ok"}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	body := `{"email":"x@y.com"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/device/transfer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMobileAuth_TransferDevice_BadBody(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `{"email":"bad"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/device/transfer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMobileAuth_SetBiometrics_Success(t *testing.T) {
	st := &stubAuthClient{
		setBiometricsFn: func(req *authpb.SetBiometricsRequest) (*authpb.SetBiometricsResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			require.Equal(t, "dev-1", req.DeviceId)
			require.True(t, req.Enabled)
			return &authpb.SetBiometricsResponse{Success: true}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	body := `{"enabled":true}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/device/biometrics", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMobileAuth_SetBiometrics_BadBody(t *testing.T) {
	h := handler.NewMobileAuthHandler(&stubAuthClient{})
	r := mobileRouter(h)
	body := `not-json`
	req := httptest.NewRequest("POST", "/api/v2/mobile/device/biometrics", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMobileAuth_GetBiometrics_Success(t *testing.T) {
	st := &stubAuthClient{
		getBiometricsFn: func(req *authpb.GetBiometricsRequest) (*authpb.GetBiometricsResponse, error) {
			require.Equal(t, int64(42), req.UserId)
			require.Equal(t, "dev-1", req.DeviceId)
			return &authpb.GetBiometricsResponse{Enabled: true}, nil
		},
	}
	h := handler.NewMobileAuthHandler(st)
	r := mobileRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/mobile/device/biometrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"enabled":true`)
}
