package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	userpb "github.com/exbanka/contract/userpb"
)

type RoleHandler struct {
	userClient userpb.UserServiceClient
}

func NewRoleHandler(userClient userpb.UserServiceClient) *RoleHandler {
	return &RoleHandler{userClient: userClient}
}

// ListRoles godoc
// @Summary      List all roles
// @Description  Returns all roles with their associated permission codes. Requires employees.permissions permission.
// @Tags         roles
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "roles array"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/roles [get]
func (h *RoleHandler) ListRoles(c *gin.Context) {
	resp, err := h.userClient.ListRoles(c.Request.Context(), &userpb.ListRolesRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	roles := make([]gin.H, 0, len(resp.Roles))
	for _, r := range resp.Roles {
		roles = append(roles, roleToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

// GetRole godoc
// @Summary      Get role by ID
// @Description  Returns a single role with its permission codes. Requires employees.permissions permission.
// @Tags         roles
// @Produce      json
// @Param        id  path  int  true  "Role ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "role details"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      404  {object}  map[string]string       "not found"
// @Router       /api/v2/roles/{id} [get]
func (h *RoleHandler) GetRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.userClient.GetRole(c.Request.Context(), &userpb.GetRoleRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, roleToJSON(resp))
}

type createRoleRequest struct {
	Name            string   `json:"name" binding:"required"`
	Description     string   `json:"description"`
	PermissionCodes []string `json:"permission_codes"`
}

// CreateRole godoc
// @Summary      Create a new role
// @Description  Creates a new role with the given permission codes. Requires employees.permissions permission.
// @Tags         roles
// @Accept       json
// @Produce      json
// @Param        body  body  createRoleRequest  true  "Role data"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created role"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/roles [post]
func (h *RoleHandler) CreateRole(c *gin.Context) {
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	resp, err := h.userClient.CreateRole(c.Request.Context(), &userpb.CreateRoleRequest{
		Name:            req.Name,
		Description:     req.Description,
		PermissionCodes: req.PermissionCodes,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, roleToJSON(resp))
}

type updateRolePermissionsRequest struct {
	PermissionCodes []string `json:"permission_codes" binding:"required"`
}

// UpdateRolePermissions godoc
// @Summary      Update role permissions
// @Description  Replaces all permissions for the given role. Requires employees.permissions permission.
// @Tags         roles
// @Accept       json
// @Produce      json
// @Param        id    path  int                           true  "Role ID"
// @Param        body  body  updateRolePermissionsRequest  true  "Permission codes"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated role"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/roles/{id}/permissions [put]
func (h *RoleHandler) UpdateRolePermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	var req updateRolePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	resp, err := h.userClient.UpdateRolePermissions(c.Request.Context(), &userpb.UpdateRolePermissionsRequest{
		RoleId:          id,
		PermissionCodes: req.PermissionCodes,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, roleToJSON(resp))
}

type assignPermissionRequest struct {
	Permission string `json:"permission" binding:"required"`
}

// AssignPermissionToRole godoc
// @Summary      Assign a permission to a role
// @Description  Grants a single permission to the role identified by ID. The permission is validated against the codegened catalog. Idempotent — granting a permission already held is a no-op. Requires roles.permissions.assign permission.
// @Tags         roles
// @Accept       json
// @Produce      json
// @Param        id         path  int                      true  "Role ID"
// @Param        body       body  assignPermissionRequest  true  "Permission code to grant"
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      400  {object}  map[string]interface{}  "validation error or permission not in catalog"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      403  {object}  map[string]interface{}  "forbidden"
// @Failure      404  {object}  map[string]interface{}  "role not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v3/roles/{id}/permissions [post]
func (h *RoleHandler) AssignPermissionToRole(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req assignPermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	_, err = h.userClient.AssignPermissionToRole(middleware.GRPCContextWithChangedBy(c), &userpb.AssignPermissionToRoleRequest{
		RoleId:     id,
		Permission: req.Permission,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RevokePermissionFromRole godoc
// @Summary      Revoke a permission from a role
// @Description  Removes a single permission grant from the role identified by ID. Idempotent — revoking a permission not held is a no-op. Requires roles.permissions.revoke permission.
// @Tags         roles
// @Produce      json
// @Param        id          path  int     true  "Role ID"
// @Param        permission  path  string  true  "Permission code to revoke"
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      400  {object}  map[string]interface{}  "validation error"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      403  {object}  map[string]interface{}  "forbidden"
// @Failure      404  {object}  map[string]interface{}  "role not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v3/roles/{id}/permissions/{permission} [delete]
func (h *RoleHandler) RevokePermissionFromRole(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	_, err = h.userClient.RevokePermissionFromRole(middleware.GRPCContextWithChangedBy(c), &userpb.RevokePermissionFromRoleRequest{
		RoleId:     id,
		Permission: c.Param("permission"),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListPermissions godoc
// @Summary      List all permissions
// @Description  Returns all permission codes with descriptions and categories. Requires employees.permissions permission.
// @Tags         permissions
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "permissions array"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/permissions [get]
func (h *RoleHandler) ListPermissions(c *gin.Context) {
	resp, err := h.userClient.ListPermissions(c.Request.Context(), &userpb.ListPermissionsRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	perms := make([]gin.H, 0, len(resp.Permissions))
	for _, p := range resp.Permissions {
		perms = append(perms, gin.H{
			"id":          p.Id,
			"code":        p.Code,
			"description": p.Description,
			"category":    p.Category,
		})
	}
	c.JSON(http.StatusOK, gin.H{"permissions": perms})
}

type setEmployeeRolesRequest struct {
	RoleNames []string `json:"role_names" binding:"required"`
}

// SetEmployeeRoles godoc
// @Summary      Set employee roles
// @Description  Replaces all roles for the given employee. Requires employees.permissions permission.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        id    path  int                     true  "Employee ID"
// @Param        body  body  setEmployeeRolesRequest  true  "Role names"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/employees/{id}/roles [put]
func (h *RoleHandler) SetEmployeeRoles(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	var req setEmployeeRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	resp, err := h.userClient.SetEmployeeRoles(middleware.GRPCContextWithChangedBy(c), &userpb.SetEmployeeRolesRequest{
		EmployeeId: id,
		RoleNames:  req.RoleNames,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, employeeToJSONWithActive(resp, false))
}

type setEmployeePermissionsRequest struct {
	PermissionCodes []string `json:"permission_codes" binding:"required"`
}

// SetEmployeeAdditionalPermissions godoc
// @Summary      Set employee additional permissions
// @Description  Replaces the additional (per-employee) permissions for the given employee. Requires employees.permissions permission.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        id    path  int                            true  "Employee ID"
// @Param        body  body  setEmployeePermissionsRequest  true  "Permission codes"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/v2/employees/{id}/permissions [put]
func (h *RoleHandler) SetEmployeeAdditionalPermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	var req setEmployeePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	resp, err := h.userClient.SetEmployeeAdditionalPermissions(middleware.GRPCContextWithChangedBy(c), &userpb.SetEmployeePermissionsRequest{
		EmployeeId:      id,
		PermissionCodes: req.PermissionCodes,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, employeeToJSONWithActive(resp, false))
}

func roleToJSON(r *userpb.RoleResponse) gin.H {
	return gin.H{
		"id":          r.Id,
		"name":        r.Name,
		"description": r.Description,
		"permissions": r.Permissions,
	}
}
