package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
	perms "github.com/exbanka/contract/permissions"
)

// abortWithError sends a structured error and aborts the middleware chain.
func abortWithError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func AuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing authorization header")
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid authorization format")
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{
			Token: parts[1],
		})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Block client tokens from accessing employee-only routes
		if resp.PrincipalType == "client" {
			abortWithError(c, http.StatusForbidden, "forbidden", "token not authorized for employee routes")
			return
		}

		setTokenContext(c, resp)
		c.Next()
	}
}

// setTokenContext populates the gin context with all JWT claim fields.
//
// The keys "principal_id" and "principal_type" are the post-Spec-C names
// (was: "user_id" and "system_type"). All downstream readers in api-gateway
// use these keys; the legacy keys are no longer set anywhere.
func setTokenContext(c *gin.Context, resp *authpb.ValidateTokenResponse) {
	c.Set("principal_id", resp.PrincipalId)
	c.Set("email", resp.Email)
	c.Set("role", resp.Role)
	c.Set("roles", resp.Roles)
	c.Set("principal_type", resp.PrincipalType)
	c.Set("permissions", resp.Permissions)
	c.Set("device_id", resp.DeviceId)
	c.Set("first_name", resp.FirstName)
	c.Set("last_name", resp.LastName)
	c.Set("account_active", resp.AccountActive)
	c.Set("biometrics_enabled", resp.BiometricsEnabled)
}

// AnyAuthMiddleware accepts either an employee JWT or a client JWT.
// Use this for routes that should be accessible by both roles.
func AnyAuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing authorization header")
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid authorization format")
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{
			Token: parts[1],
		})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		setTokenContext(c, resp)
		c.Next()
	}
}

// RequireClientToken rejects requests that do not carry a client JWT.
// Must be chained after AnyAuthMiddleware (which sets "principal_type" in the context).
func RequireClientToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		principalType, _ := c.Get("principal_type")
		if principalType != "client" {
			abortWithError(c, http.StatusForbidden, "forbidden", "client token required")
			return
		}
		c.Next()
	}
}

// RequirePermission admits the request only if the caller holds the given
// typed permission. Accepting a typed value (rather than a magic string) means
// router authors can no longer typo a permission code: anything not in the
// generated catalog fails to compile.
func RequirePermission(p perms.Permission) gin.HandlerFunc {
	want := string(p)
	return func(c *gin.Context) {
		raw, exists := c.Get("permissions")
		if !exists {
			abortWithError(c, http.StatusForbidden, "forbidden", "no permissions")
			return
		}
		permList, ok := raw.([]string)
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid permissions format")
			return
		}
		for _, h := range permList {
			if h == want {
				c.Next()
				return
			}
		}
		abortWithError(c, http.StatusForbidden, "forbidden", "insufficient permissions")
	}
}

// RequireAnyPermission admits the request if the caller holds at least one
// of the listed typed permissions. Used for routes whose visibility is
// scoped: e.g. GET /api/orders accepts both `orders.read.all` and
// `orders.read.own` — the handler then dispatches based on which the
// caller holds (see HasPermission below).
func RequireAnyPermission(ps ...perms.Permission) gin.HandlerFunc {
	wants := make([]string, len(ps))
	for i, p := range ps {
		wants[i] = string(p)
	}
	return func(c *gin.Context) {
		raw, exists := c.Get("permissions")
		if !exists {
			abortWithError(c, http.StatusForbidden, "forbidden", "no permissions")
			return
		}
		permList, ok := raw.([]string)
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid permissions format")
			return
		}
		for _, want := range wants {
			for _, have := range permList {
				if have == want {
					c.Next()
					return
				}
			}
		}
		abortWithError(c, http.StatusForbidden, "forbidden", "insufficient permissions")
	}
}

// RequireAllPermissions returns a middleware that requires every listed
// typed permission to be present in the JWT's permissions claim. Use when an
// action sits at the intersection of multiple capability gates.
func RequireAllPermissions(ps ...perms.Permission) gin.HandlerFunc {
	wants := make([]string, len(ps))
	for i, p := range ps {
		wants[i] = string(p)
	}
	return func(c *gin.Context) {
		raw, ok := c.Get("permissions")
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "no permissions")
			return
		}
		have, ok := raw.([]string)
		if !ok {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid permissions format")
			return
		}
		set := make(map[string]bool, len(have))
		for _, p := range have {
			set[p] = true
		}
		for _, want := range wants {
			if !set[want] {
				abortWithError(c, http.StatusForbidden, "forbidden", "missing permission "+want)
				return
			}
		}
		c.Next()
	}
}

// HasPermission reports whether the caller holds the given permission
// code. Useful inside handlers that need to dispatch by scope (e.g.,
// returning all orders if the caller has orders.read.all, otherwise
// filtering to their own).
func HasPermission(c *gin.Context, code string) bool {
	perms, exists := c.Get("permissions")
	if !exists {
		return false
	}
	list, ok := perms.([]string)
	if !ok {
		return false
	}
	for _, p := range list {
		if p == code {
			return true
		}
	}
	return false
}
