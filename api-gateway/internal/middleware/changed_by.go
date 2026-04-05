// api-gateway/internal/middleware/changed_by.go
package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/exbanka/contract/changelog"
)

// GRPCContextWithChangedBy reads user_id from Gin context and sets it as
// gRPC x-changed-by metadata on the returned context. Use this before
// every mutating gRPC call.
func GRPCContextWithChangedBy(c *gin.Context) context.Context {
	userID := c.GetInt64("user_id")
	return changelog.SetChangedBy(c.Request.Context(), userID)
}
