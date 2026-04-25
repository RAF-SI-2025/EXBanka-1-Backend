// api-gateway/internal/handler/role_handler_test.go
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

	userpb "github.com/exbanka/contract/userpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func roleRouter(h *handler.RoleHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("user_id", int64(1)) }
	r.GET("/roles", withCtx, h.ListRoles)
	r.GET("/roles/:id", withCtx, h.GetRole)
	r.POST("/roles", withCtx, h.CreateRole)
	r.PUT("/roles/:id/permissions", withCtx, h.UpdateRolePermissions)
	r.GET("/permissions", withCtx, h.ListPermissions)
	r.PUT("/employees/:id/roles", withCtx, h.SetEmployeeRoles)
	r.PUT("/employees/:id/permissions", withCtx, h.SetEmployeeAdditionalPermissions)
	return r
}

func TestRole_ListRoles(t *testing.T) {
	user := &stubUserClient{
		listRolesFn: func(_ *userpb.ListRolesRequest) (*userpb.ListRolesResponse, error) {
			return &userpb.ListRolesResponse{Roles: []*userpb.RoleResponse{{Id: 1, Name: "EmployeeBasic"}}}, nil
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	req := httptest.NewRequest("GET", "/roles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "EmployeeBasic")
}

func TestRole_ListRoles_GRPCError(t *testing.T) {
	user := &stubUserClient{
		listRolesFn: func(_ *userpb.ListRolesRequest) (*userpb.ListRolesResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	req := httptest.NewRequest("GET", "/roles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRole_GetRole_Success(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("GET", "/roles/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRole_GetRole_BadID(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("GET", "/roles/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_CreateRole_Success(t *testing.T) {
	user := &stubUserClient{
		createRoleFn: func(req *userpb.CreateRoleRequest) (*userpb.RoleResponse, error) {
			require.Equal(t, "MyRole", req.Name)
			return &userpb.RoleResponse{Id: 5, Name: req.Name}, nil
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	body := `{"name":"MyRole","description":"x","permission_codes":["a","b"]}`
	req := httptest.NewRequest("POST", "/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestRole_CreateRole_MissingName(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("POST", "/roles", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_UpdateRolePermissions_Success(t *testing.T) {
	user := &stubUserClient{
		updateRolePermsFn: func(req *userpb.UpdateRolePermissionsRequest) (*userpb.RoleResponse, error) {
			require.Equal(t, int64(7), req.RoleId)
			require.Equal(t, []string{"a", "b"}, req.PermissionCodes)
			return &userpb.RoleResponse{Id: 7}, nil
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	body := `{"permission_codes":["a","b"]}`
	req := httptest.NewRequest("PUT", "/roles/7/permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRole_UpdateRolePermissions_BadID(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("PUT", "/roles/abc/permissions", strings.NewReader(`{"permission_codes":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_UpdateRolePermissions_MissingField(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("PUT", "/roles/1/permissions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_ListPermissions(t *testing.T) {
	user := &stubUserClient{
		listPermissionsFn: func(_ *userpb.ListPermissionsRequest) (*userpb.ListPermissionsResponse, error) {
			return &userpb.ListPermissionsResponse{Permissions: []*userpb.PermissionResponse{{Id: 1, Code: "x", Description: "y", Category: "z"}}}, nil
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	req := httptest.NewRequest("GET", "/permissions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"x"`)
}

func TestRole_SetEmployeeRoles_Success(t *testing.T) {
	user := &stubUserClient{
		setEmployeeRolesFn: func(req *userpb.SetEmployeeRolesRequest) (*userpb.EmployeeResponse, error) {
			require.Equal(t, int64(7), req.EmployeeId)
			return &userpb.EmployeeResponse{Id: 7}, nil
		},
	}
	h := handler.NewRoleHandler(user)
	r := roleRouter(h)
	body := `{"role_names":["EmployeeBasic"]}`
	req := httptest.NewRequest("PUT", "/employees/7/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRole_SetEmployeeRoles_BadID(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("PUT", "/employees/abc/roles", strings.NewReader(`{"role_names":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRole_SetEmployeeAdditionalPermissions_Success(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	body := `{"permission_codes":["x"]}`
	req := httptest.NewRequest("PUT", "/employees/1/permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRole_SetEmployeeAdditionalPermissions_BadID(t *testing.T) {
	h := handler.NewRoleHandler(&stubUserClient{})
	r := roleRouter(h)
	req := httptest.NewRequest("PUT", "/employees/abc/permissions", strings.NewReader(`{"permission_codes":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
