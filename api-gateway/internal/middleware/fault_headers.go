package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/contract/shared/grpcmw"
	"github.com/exbanka/contract/shared/saga"
)

// faultHTTPHeaders are the X-Saga-* request headers a test harness sends to
// drive saga fault-injection scenarios (SG-05..11). The canonical names; the
// transport lowercases them for gRPC metadata.
var faultHTTPHeaders = []string{
	"X-Saga-Force-Fail",
	"X-Saga-Force-Fail-Kind",
	"X-Saga-Compensate-Fail",
	"X-Saga-Compensate-Fail-Times",
	"X-Saga-Inject-Delay",
}

// FaultHeaderForwarder copies any X-Saga-* request headers onto the request
// context so the gRPC client interceptor forwards them to the downstream
// service's saga executor. It is a no-op in production: saga.FaultsEnabled is a
// compile-time false constant there, so the body is dead-code-eliminated and no
// fault directive can ever be forwarded.
func FaultHeaderForwarder() gin.HandlerFunc {
	return func(c *gin.Context) {
		if saga.FaultsEnabled {
			h := make(map[string]string, len(faultHTTPHeaders))
			for _, name := range faultHTTPHeaders {
				if v := c.GetHeader(name); v != "" {
					h[strings.ToLower(name)] = v
				}
			}
			if len(h) > 0 {
				c.Request = c.Request.WithContext(grpcmw.WithFaultHeaders(c.Request.Context(), h))
			}
		}
		c.Next()
	}
}
