package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

// ClientAuthMiddleware validates JWT tokens for clients (role="client").
// It reuses the same auth-service ValidateToken RPC and checks that the role is "client".
func ClientAuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
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

		// Check by system_type first (new), fall back to role=="client" (legacy)
		if resp.SystemType == "employee" {
			abortWithError(c, http.StatusForbidden, "forbidden", "token not authorized for client routes")
			return
		}
		if resp.SystemType != "client" && resp.Role != "client" {
			abortWithError(c, http.StatusForbidden, "forbidden", "client access required")
			return
		}

		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("system_type", resp.SystemType)
		c.Set("permissions", resp.Permissions)
		c.Next()
	}
}
