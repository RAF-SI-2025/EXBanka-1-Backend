package handler

import (
	"context"

	"github.com/gin-gonic/gin"
)

// businessAuditor is the narrow publish interface the handlers use to emit
// business-action audit events. *gatewaykafka.AuditProducer satisfies it. It is
// an optional dependency: handlers guard on nil so unit tests need not wire it.
type businessAuditor interface {
	PublishBusinessAction(ctx context.Context, action string, actorEmployeeID int64, targetType, targetID, detail string)
}

// auditBusinessAction is a best-effort helper that reads the actor (JWT
// principal_id) from the gin context and publishes a business-action audit
// event. No-op when the auditor is not wired. Never blocks the response.
func auditBusinessAction(c *gin.Context, a businessAuditor, action, targetType, targetID, detail string) {
	if a == nil {
		return
	}
	actor := c.GetInt64("principal_id")
	a.PublishBusinessAction(context.Background(), action, actor, targetType, targetID, detail)
}
