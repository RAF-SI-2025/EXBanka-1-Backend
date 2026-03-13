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

		if resp.Role != "client" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "client access required"})
			return
		}

		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("permissions", resp.Permissions)
		c.Next()
	}
}
