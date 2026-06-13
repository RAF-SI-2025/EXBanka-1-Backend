package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	perms "github.com/exbanka/contract/permissions"
)

// abortWithError sends a structured error and aborts the middleware chain.
func abortWithError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// bearerToken extracts the raw token from the Authorization header, writing a
// 401 and returning ok=false on a missing/malformed header.
func bearerToken(c *gin.Context) (string, bool) {
	header := c.GetHeader("Authorization")
	if header == "" {
		abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing authorization header")
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid authorization format")
		return "", false
	}
	return parts[1], true
}

// writeVerifyFailure maps a non-OK verify outcome to the right 401:
// token_expired (stale/expired → client refreshes) vs unauthorized (bad/
// revoked → client logs out).
func writeVerifyFailure(c *gin.Context, kind VerifyKind) {
	if kind == VerifyTokenExpired {
		abortWithError(c, http.StatusUnauthorized, "token_expired", "token expired")
		return
	}
	abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid or revoked token")
}

// AuthMiddleware verifies the access token (locally via ES256, falling back to
// gRPC) and admits employees only — client tokens are rejected here.
func AuthMiddleware(v *TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			return
		}
		p, kind := v.Verify(c.Request.Context(), token)
		if kind != VerifyOK {
			writeVerifyFailure(c, kind)
			return
		}
		// Block client tokens from accessing employee-only routes.
		if p.PrincipalType == "client" {
			abortWithError(c, http.StatusForbidden, "forbidden", "token not authorized for employee routes")
			return
		}
		setPrincipalContext(c, p)
		c.Next()
	}
}

// AnyAuthMiddleware accepts either an employee JWT or a client JWT.
// Use this for routes that should be accessible by both roles.
func AnyAuthMiddleware(v *TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			return
		}
		p, kind := v.Verify(c.Request.Context(), token)
		if kind != VerifyOK {
			writeVerifyFailure(c, kind)
			return
		}
		setPrincipalContext(c, p)
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

// DenyClientToken rejects requests carrying a client JWT (admits employees /
// non-client principals only). Must be chained after AnyAuthMiddleware (which
// sets "principal_type"). Used for market data that Celina 3 restricts to
// actuaries — clients may trade only stocks and futures, never forex or options.
func DenyClientToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if principalType, _ := c.Get("principal_type"); principalType == "client" {
			abortWithError(c, http.StatusForbidden, "forbidden", "not available to client accounts")
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

// PermMode selects how RequirePermissionOrClient evaluates the permission
// list for employee principals.
type PermMode int

const (
	// PermAll requires the employee to hold every listed permission.
	PermAll PermMode = iota
	// PermAny requires the employee to hold at least one listed permission.
	PermAny
)

// RequirePermissionOrClient gates a route that both clients and employees may
// use. Client principals always pass — their access is constrained by
// resource-ownership checks in the handler, not by permissions. Employee
// principals are still permission-gated: PermAll requires all listed
// permissions, PermAny requires at least one.
func RequirePermissionOrClient(mode PermMode, ps ...perms.Permission) gin.HandlerFunc {
	wants := make([]string, len(ps))
	for i, p := range ps {
		wants[i] = string(p)
	}
	return func(c *gin.Context) {
		if c.GetString("principal_type") == "client" {
			c.Next()
			return
		}
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
		switch mode {
		case PermAny:
			for _, w := range wants {
				if set[w] {
					c.Next()
					return
				}
			}
			abortWithError(c, http.StatusForbidden, "forbidden", "insufficient permissions")
		default: // PermAll
			for _, w := range wants {
				if !set[w] {
					abortWithError(c, http.StatusForbidden, "forbidden", "missing permission "+w)
					return
				}
			}
			c.Next()
		}
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

// GetCallerPermissions returns the caller's JWT permission list from the gin
// context. Returns nil (empty) when the context has no permissions (unauthenticated
// or client token). Useful in handlers that need the full permission set rather
// than a single HasPermission check.
func GetCallerPermissions(c *gin.Context) []string {
	raw, exists := c.Get("permissions")
	if !exists {
		return nil
	}
	list, ok := raw.([]string)
	if !ok {
		return nil
	}
	return list
}
