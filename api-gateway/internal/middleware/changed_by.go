// api-gateway/internal/middleware/changed_by.go
package middleware

import (
	"context"

	"github.com/exbanka/contract/changelog"
	"github.com/gin-gonic/gin"
)

// GRPCContextWithChangedBy reads principal_id from Gin context and sets
// it as gRPC x-changed-by metadata on the returned context. Use this
// before every mutating gRPC call.
func GRPCContextWithChangedBy(c *gin.Context) context.Context {
	principalID := c.GetInt64("principal_id")
	return changelog.SetChangedBy(c.Request.Context(), principalID)
}
