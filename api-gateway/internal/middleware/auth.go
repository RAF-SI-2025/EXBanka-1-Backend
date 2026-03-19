package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

func AuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{
			Token: parts[1],
		})
		if err != nil || !resp.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		// Block client tokens from accessing employee-only routes
		if resp.SystemType == "client" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "token not authorized for employee routes"})
			return
		}

		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("roles", resp.Roles)
		c.Set("system_type", resp.SystemType)
		c.Set("permissions", resp.Permissions)
		c.Next()
	}
}

// AnyAuthMiddleware accepts either an employee JWT or a client JWT.
// Use this for routes that should be accessible by both roles.
func AnyAuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{
			Token: parts[1],
		})
		if err != nil || !resp.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("roles", resp.Roles)
		c.Set("system_type", resp.SystemType)
		c.Set("permissions", resp.Permissions)
		c.Next()
	}
}

func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms, exists := c.Get("permissions")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no permissions"})
			return
		}
		permList, ok := perms.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid permissions format"})
			return
		}
		for _, p := range permList {
			if p == permission {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
}
