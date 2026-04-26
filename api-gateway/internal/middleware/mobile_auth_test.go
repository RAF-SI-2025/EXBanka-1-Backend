// api-gateway/internal/middleware/mobile_auth_test.go
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"context"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
)

// mobileAuthClientStub overrides ValidateToken and ValidateDeviceSignature.
// Methods not under test panic to make unexpected calls obvious.
type mobileAuthClientStub struct {
	validateTokenFn           func(*authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error)
	validateDeviceSignatureFn func(*authpb.ValidateDeviceSignatureRequest) (*authpb.ValidateDeviceSignatureResponse, error)
}

func (m *mobileAuthClientStub) ValidateToken(_ context.Context, req *authpb.ValidateTokenRequest, _ ...grpc.CallOption) (*authpb.ValidateTokenResponse, error) {
	if m.validateTokenFn != nil {
		return m.validateTokenFn(req)
	}
	return &authpb.ValidateTokenResponse{Valid: false}, nil
}

func (m *mobileAuthClientStub) ValidateDeviceSignature(_ context.Context, req *authpb.ValidateDeviceSignatureRequest, _ ...grpc.CallOption) (*authpb.ValidateDeviceSignatureResponse, error) {
	if m.validateDeviceSignatureFn != nil {
		return m.validateDeviceSignatureFn(req)
	}
	return &authpb.ValidateDeviceSignatureResponse{Valid: true}, nil
}

// Remaining methods — delegate to mockAuthClient which panics on all of them.
func (m *mobileAuthClientStub) Login(_ context.Context, _ *authpb.LoginRequest, _ ...grpc.CallOption) (*authpb.LoginResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RefreshToken(_ context.Context, _ *authpb.RefreshTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshTokenResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) Logout(_ context.Context, _ *authpb.LogoutRequest, _ ...grpc.CallOption) (*authpb.LogoutResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RequestPasswordReset(_ context.Context, _ *authpb.PasswordResetRequest, _ ...grpc.CallOption) (*authpb.PasswordResetResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) ResetPassword(_ context.Context, _ *authpb.ResetPasswordRequest, _ ...grpc.CallOption) (*authpb.ResetPasswordResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) ActivateAccount(_ context.Context, _ *authpb.ActivateAccountRequest, _ ...grpc.CallOption) (*authpb.ActivateAccountResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) SetAccountStatus(_ context.Context, _ *authpb.SetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.SetAccountStatusResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) GetAccountStatus(_ context.Context, _ *authpb.GetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) GetAccountStatusBatch(_ context.Context, _ *authpb.GetAccountStatusBatchRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusBatchResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) CreateAccount(_ context.Context, _ *authpb.CreateAccountRequest, _ ...grpc.CallOption) (*authpb.CreateAccountResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RequestMobileActivation(_ context.Context, _ *authpb.MobileActivationRequest, _ ...grpc.CallOption) (*authpb.MobileActivationResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) ActivateMobileDevice(_ context.Context, _ *authpb.ActivateMobileDeviceRequest, _ ...grpc.CallOption) (*authpb.ActivateMobileDeviceResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RefreshMobileToken(_ context.Context, _ *authpb.RefreshMobileTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshMobileTokenResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) DeactivateDevice(_ context.Context, _ *authpb.DeactivateDeviceRequest, _ ...grpc.CallOption) (*authpb.DeactivateDeviceResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) TransferDevice(_ context.Context, _ *authpb.TransferDeviceRequest, _ ...grpc.CallOption) (*authpb.TransferDeviceResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) GetDeviceInfo(_ context.Context, _ *authpb.GetDeviceInfoRequest, _ ...grpc.CallOption) (*authpb.GetDeviceInfoResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) ListSessions(_ context.Context, _ *authpb.ListSessionsRequest, _ ...grpc.CallOption) (*authpb.ListSessionsResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RevokeSession(_ context.Context, _ *authpb.RevokeSessionRequest, _ ...grpc.CallOption) (*authpb.RevokeSessionResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) RevokeAllSessions(_ context.Context, _ *authpb.RevokeAllSessionsRequest, _ ...grpc.CallOption) (*authpb.RevokeAllSessionsResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) GetLoginHistory(_ context.Context, _ *authpb.LoginHistoryRequest, _ ...grpc.CallOption) (*authpb.LoginHistoryResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) SetBiometricsEnabled(_ context.Context, _ *authpb.SetBiometricsRequest, _ ...grpc.CallOption) (*authpb.SetBiometricsResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) GetBiometricsEnabled(_ context.Context, _ *authpb.GetBiometricsRequest, _ ...grpc.CallOption) (*authpb.GetBiometricsResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) CheckBiometricsEnabled(_ context.Context, _ *authpb.CheckBiometricsRequest, _ ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error) {
	panic("not implemented")
}
func (m *mobileAuthClientStub) ResendActivationEmail(_ context.Context, _ *authpb.ResendActivationEmailRequest, _ ...grpc.CallOption) (*authpb.ResendActivationEmailResponse, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// extractBearerToken
// ---------------------------------------------------------------------------

func TestExtractBearerToken_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		token := extractBearerToken(c)
		assert.Equal(t, "my-token", token)
		c.Status(http.StatusOK)
	})
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
}

func TestExtractBearerToken_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		token := extractBearerToken(c)
		assert.Equal(t, "", token)
		c.Status(http.StatusOK)
	})
	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
}

func TestExtractBearerToken_BadScheme(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		token := extractBearerToken(c)
		assert.Equal(t, "", token)
		c.Status(http.StatusOK)
	})
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic abc123")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
}

// ---------------------------------------------------------------------------
// sha256Hex
// ---------------------------------------------------------------------------

func TestSha256Hex_EmptyBody(t *testing.T) {
	result := sha256Hex([]byte{})
	// SHA256 of empty bytes is a known constant
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", result)
	assert.Len(t, result, 64)
}

func TestSha256Hex_KnownValue(t *testing.T) {
	result := sha256Hex([]byte("hello"))
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", result)
}

// ---------------------------------------------------------------------------
// MobileAuthMiddleware
// ---------------------------------------------------------------------------

func mobileTestRouter(authClient authpb.AuthServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MobileAuthMiddleware(authClient))
	r.GET("/mobile", func(c *gin.Context) {
		deviceID, _ := c.Get("device_id")
		c.JSON(http.StatusOK, gin.H{"device_id": deviceID})
	})
	return r
}

func TestMobileAuthMiddleware_MissingToken(t *testing.T) {
	client := &mobileAuthClientStub{}
	r := mobileTestRouter(client)
	req, _ := http.NewRequest("GET", "/mobile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "unauthorized", errObj["code"])
}

func TestMobileAuthMiddleware_InvalidToken(t *testing.T) {
	client := &mobileAuthClientStub{
		validateTokenFn: func(_ *authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error) {
			return &authpb.ValidateTokenResponse{Valid: false}, nil
		},
	}
	r := mobileTestRouter(client)
	req, _ := http.NewRequest("GET", "/mobile", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMobileAuthMiddleware_NonMobileDeviceType(t *testing.T) {
	client := &mobileAuthClientStub{
		validateTokenFn: func(_ *authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error) {
			return &authpb.ValidateTokenResponse{
				Valid:      true,
				UserId:     1,
				DeviceType: "web",
				DeviceId:   "dev-123",
			}, nil
		},
	}
	r := mobileTestRouter(client)
	req, _ := http.NewRequest("GET", "/mobile", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	req.Header.Set("X-Device-ID", "dev-123")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "forbidden", errObj["code"])
	assert.Contains(t, errObj["message"], "mobile device token")
}

func TestMobileAuthMiddleware_MissingDeviceIDHeader(t *testing.T) {
	client := &mobileAuthClientStub{
		validateTokenFn: func(_ *authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error) {
			return &authpb.ValidateTokenResponse{
				Valid:      true,
				UserId:     1,
				DeviceType: "mobile",
				DeviceId:   "dev-123",
			}, nil
		},
	}
	r := mobileTestRouter(client)
	req, _ := http.NewRequest("GET", "/mobile", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	// No X-Device-ID header
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "forbidden", errObj["code"])
}

func TestMobileAuthMiddleware_DeviceIDMismatch(t *testing.T) {
	client := &mobileAuthClientStub{
		validateTokenFn: func(_ *authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error) {
			return &authpb.ValidateTokenResponse{
				Valid:      true,
				UserId:     1,
				DeviceType: "mobile",
				DeviceId:   "dev-123",
			}, nil
		},
	}
	r := mobileTestRouter(client)
	req, _ := http.NewRequest("GET", "/mobile", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	req.Header.Set("X-Device-ID", "wrong-device")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestMobileAuthMiddleware_ValidRequest_SetsContext(t *testing.T) {
	client := &mobileAuthClientStub{
		validateTokenFn: func(_ *authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error) {
			return &authpb.ValidateTokenResponse{
				Valid:       true,
				UserId:      42,
				Email:       "user@example.com",
				DeviceType:  "mobile",
				DeviceId:    "dev-abc",
				SystemType:  "client",
				Permissions: []string{"accounts.read"},
			}, nil
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MobileAuthMiddleware(client))
	r.GET("/mobile", func(c *gin.Context) {
		uid, _ := c.Get("user_id")
		deviceID, _ := c.Get("device_id")
		deviceType, _ := c.Get("device_type")
		sysType, _ := c.Get("system_type")
		c.JSON(http.StatusOK, gin.H{
			"user_id":     uid,
			"device_id":   deviceID,
			"device_type": deviceType,
			"system_type": sysType,
		})
	})

	req, _ := http.NewRequest("GET", "/mobile", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set("X-Device-ID", "dev-abc")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(42), resp["user_id"])
	assert.Equal(t, "dev-abc", resp["device_id"])
	assert.Equal(t, "mobile", resp["device_type"])
	assert.Equal(t, "client", resp["system_type"])
}

// ---------------------------------------------------------------------------
// RequireDeviceSignature
// ---------------------------------------------------------------------------

func makeDeviceSigRouter(authClient authpb.AuthServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Pre-set device_id in context (normally done by MobileAuthMiddleware)
	r.POST("/secure", func(c *gin.Context) {
		c.Set("device_id", "dev-abc")
	}, RequireDeviceSignature(authClient), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestRequireDeviceSignature_MissingTimestamp(t *testing.T) {
	client := &mobileAuthClientStub{}
	r := makeDeviceSigRouter(client)
	req, _ := http.NewRequest("POST", "/secure", strings.NewReader("{}"))
	req.Header.Set("X-Device-Signature", "some-sig")
	// No X-Device-Timestamp
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireDeviceSignature_MissingSignature(t *testing.T) {
	client := &mobileAuthClientStub{}
	r := makeDeviceSigRouter(client)
	req, _ := http.NewRequest("POST", "/secure", strings.NewReader("{}"))
	req.Header.Set("X-Device-Timestamp", "2024-01-01T00:00:00Z")
	// No X-Device-Signature
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireDeviceSignature_InvalidSignature(t *testing.T) {
	client := &mobileAuthClientStub{
		validateDeviceSignatureFn: func(_ *authpb.ValidateDeviceSignatureRequest) (*authpb.ValidateDeviceSignatureResponse, error) {
			return &authpb.ValidateDeviceSignatureResponse{Valid: false}, nil
		},
	}
	r := makeDeviceSigRouter(client)
	req, _ := http.NewRequest("POST", "/secure", strings.NewReader(`{"key":"val"}`))
	req.Header.Set("X-Device-Timestamp", "2024-01-01T00:00:00Z")
	req.Header.Set("X-Device-Signature", "bad-sig")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "forbidden", errObj["code"])
	assert.Contains(t, errObj["message"], "invalid device signature")
}

func TestRequireDeviceSignature_ValidSignature_Passes(t *testing.T) {
	client := &mobileAuthClientStub{
		validateDeviceSignatureFn: func(req *authpb.ValidateDeviceSignatureRequest) (*authpb.ValidateDeviceSignatureResponse, error) {
			assert.Equal(t, "dev-abc", req.DeviceId)
			assert.Equal(t, "2024-01-01T00:00:00Z", req.Timestamp)
			return &authpb.ValidateDeviceSignatureResponse{Valid: true}, nil
		},
	}
	r := makeDeviceSigRouter(client)
	req, _ := http.NewRequest("POST", "/secure", strings.NewReader(`{"key":"val"}`))
	req.Header.Set("X-Device-Timestamp", "2024-01-01T00:00:00Z")
	req.Header.Set("X-Device-Signature", "good-sig")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireDeviceSignature_MissingDeviceIDInContext(t *testing.T) {
	client := &mobileAuthClientStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Don't set device_id in context
	r.POST("/secure", RequireDeviceSignature(client), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req, _ := http.NewRequest("POST", "/secure", strings.NewReader("{}"))
	req.Header.Set("X-Device-Timestamp", "ts")
	req.Header.Set("X-Device-Signature", "sig")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
