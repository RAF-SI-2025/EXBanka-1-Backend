// api-gateway/internal/middleware/auth_test.go
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
)

// ---------------------------------------------------------------------------
// mockAuthClient — minimal hand-rolled mock of authpb.AuthServiceClient.
// Only ValidateToken is exercised by the middleware; all other methods panic.
// ---------------------------------------------------------------------------

type mockAuthClient struct {
	resp *authpb.ValidateTokenResponse
	err  error
}

func (m *mockAuthClient) ValidateToken(_ context.Context, _ *authpb.ValidateTokenRequest, _ ...grpc.CallOption) (*authpb.ValidateTokenResponse, error) {
	return m.resp, m.err
}

func (m *mockAuthClient) Login(_ context.Context, _ *authpb.LoginRequest, _ ...grpc.CallOption) (*authpb.LoginResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) RefreshToken(_ context.Context, _ *authpb.RefreshTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshTokenResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) Logout(_ context.Context, _ *authpb.LogoutRequest, _ ...grpc.CallOption) (*authpb.LogoutResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) RequestPasswordReset(_ context.Context, _ *authpb.PasswordResetRequest, _ ...grpc.CallOption) (*authpb.PasswordResetResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) ResetPassword(_ context.Context, _ *authpb.ResetPasswordRequest, _ ...grpc.CallOption) (*authpb.ResetPasswordResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) ActivateAccount(_ context.Context, _ *authpb.ActivateAccountRequest, _ ...grpc.CallOption) (*authpb.ActivateAccountResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) SetAccountStatus(_ context.Context, _ *authpb.SetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.SetAccountStatusResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) GetAccountStatus(_ context.Context, _ *authpb.GetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) GetAccountStatusBatch(_ context.Context, _ *authpb.GetAccountStatusBatchRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusBatchResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) CreateAccount(_ context.Context, _ *authpb.CreateAccountRequest, _ ...grpc.CallOption) (*authpb.CreateAccountResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) RequestMobileActivation(_ context.Context, _ *authpb.MobileActivationRequest, _ ...grpc.CallOption) (*authpb.MobileActivationResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) ActivateMobileDevice(_ context.Context, _ *authpb.ActivateMobileDeviceRequest, _ ...grpc.CallOption) (*authpb.ActivateMobileDeviceResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) RefreshMobileToken(_ context.Context, _ *authpb.RefreshMobileTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshMobileTokenResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) DeactivateDevice(_ context.Context, _ *authpb.DeactivateDeviceRequest, _ ...grpc.CallOption) (*authpb.DeactivateDeviceResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) TransferDevice(_ context.Context, _ *authpb.TransferDeviceRequest, _ ...grpc.CallOption) (*authpb.TransferDeviceResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) ValidateDeviceSignature(_ context.Context, _ *authpb.ValidateDeviceSignatureRequest, _ ...grpc.CallOption) (*authpb.ValidateDeviceSignatureResponse, error) {
	panic("not implemented")
}
func (m *mockAuthClient) GetDeviceInfo(_ context.Context, _ *authpb.GetDeviceInfoRequest, _ ...grpc.CallOption) (*authpb.GetDeviceInfoResponse, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

func serveWithMiddleware(mw gin.HandlerFunc) (*gin.Engine, *httptest.ResponseRecorder, *http.Request) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	return r, w, req
}

func TestRequirePermission_NoPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok, "response should contain an 'error' object")
	assert.Equal(t, "forbidden", errObj["code"], "error code should be 'forbidden'")
	assert.Equal(t, "insufficient permissions", errObj["message"], "error message should indicate insufficient permissions")
}

func TestRequirePermission_HasPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"employees.read", "clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.read"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	assert.Equal(t, true, resp["ok"], "handler should have executed and returned ok:true")
}

func TestRequirePermission_MissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("permissions", []string{"clients.read"})
		c.Next()
	})
	r.Use(RequirePermission("employees.create"))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err, "response should be valid JSON")
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok, "response should contain an 'error' object")
	assert.Equal(t, "forbidden", errObj["code"], "error code should be 'forbidden'")
	assert.Equal(t, "insufficient permissions", errObj["message"], "user has clients.read but not employees.create")
}

// ---------------------------------------------------------------------------
// AnyAuthMiddleware
// ---------------------------------------------------------------------------

func TestAnyAuthMiddleware_EmployeeToken_SetsUserID(t *testing.T) {
	client := &mockAuthClient{
		resp: &authpb.ValidateTokenResponse{
			Valid:      true,
			UserId:     42,
			SystemType: "employee",
			Email:      "emp@example.com",
		},
	}
	r, w, req := serveWithMiddleware(AnyAuthMiddleware(client))
	// Replace the handler so we can inspect context values
	gin.SetMode(gin.TestMode)
	r2 := gin.New()
	r2.Use(AnyAuthMiddleware(client))
	r2.GET("/test", func(c *gin.Context) {
		uid, exists := c.Get("user_id")
		require.True(t, exists, "user_id should be set in context")
		assert.Equal(t, int64(42), uid)
		sysType, _ := c.Get("system_type")
		assert.Equal(t, "employee", sysType)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req.Header.Set("Authorization", "Bearer valid-employee-token")
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, req)
	assert.Equal(t, http.StatusOK, w2.Code)
	_ = r // suppress unused warning
	_ = w
}

func TestAnyAuthMiddleware_ClientToken_SetsUserID(t *testing.T) {
	client := &mockAuthClient{
		resp: &authpb.ValidateTokenResponse{
			Valid:      true,
			UserId:     99,
			SystemType: "client",
			Email:      "client@example.com",
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AnyAuthMiddleware(client))
	r.GET("/test", func(c *gin.Context) {
		uid, exists := c.Get("user_id")
		require.True(t, exists, "user_id should be set in context for client tokens")
		assert.Equal(t, int64(99), uid)
		sysType, _ := c.Get("system_type")
		assert.Equal(t, "client", sysType)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer valid-client-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ---------------------------------------------------------------------------
// AuthMiddleware — rejects client tokens
// ---------------------------------------------------------------------------

func TestAuthMiddleware_RejectsClientToken(t *testing.T) {
	client := &mockAuthClient{
		resp: &authpb.ValidateTokenResponse{
			Valid:      true,
			UserId:     99,
			SystemType: "client",
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(client))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer valid-client-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// AuthMiddleware blocks client tokens with 403
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "forbidden", errObj["code"])
}
