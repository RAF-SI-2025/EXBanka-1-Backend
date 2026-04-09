package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

// MobileAuthMiddleware validates that the request comes from a registered mobile device.
// Checks: valid JWT with device_type="mobile", X-Device-ID header matches device_id claim.
func MobileAuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearerToken(c)
		if token == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing authorization token")
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{Token: token})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Require mobile device type
		if resp.DeviceType != "mobile" {
			abortWithError(c, http.StatusForbidden, "forbidden", "this endpoint requires a mobile device token")
			return
		}

		// Require X-Device-ID header matching token claim
		headerDeviceID := c.GetHeader("X-Device-ID")
		if headerDeviceID == "" || headerDeviceID != resp.DeviceId {
			abortWithError(c, http.StatusForbidden, "forbidden", "device ID mismatch or missing X-Device-ID header")
			return
		}

		// Set context values (same as AuthMiddleware + device fields)
		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("roles", resp.Roles)
		c.Set("system_type", resp.SystemType)
		c.Set("permissions", resp.Permissions)
		c.Set("device_type", resp.DeviceType)
		c.Set("device_id", resp.DeviceId)

		c.Next()
	}
}

// RequireDeviceSignature validates the HMAC request signature from a mobile device.
// Must be chained AFTER MobileAuthMiddleware (needs device_id from context).
func RequireDeviceSignature(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceID, _ := c.Get("device_id")
		deviceIDStr, ok := deviceID.(string)
		if !ok || deviceIDStr == "" {
			abortWithError(c, http.StatusForbidden, "forbidden", "device_id not found in context")
			return
		}

		timestamp := c.GetHeader("X-Device-Timestamp")
		signature := c.GetHeader("X-Device-Signature")
		if timestamp == "" || signature == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing X-Device-Timestamp or X-Device-Signature headers")
			return
		}

		// Read body for SHA256 (need to buffer it for the handler)
		bodyBytes, err := c.GetRawData()
		if err != nil {
			abortWithError(c, http.StatusBadRequest, "validation_error", "failed to read request body")
			return
		}
		// Restore body for downstream handlers
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		bodySHA := sha256Hex(bodyBytes)

		resp, err := authClient.ValidateDeviceSignature(c.Request.Context(), &authpb.ValidateDeviceSignatureRequest{
			DeviceId:   deviceIDStr,
			Timestamp:  timestamp,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			BodySha256: bodySHA,
			Signature:  signature,
		})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid device signature")
			return
		}

		c.Next()
	}
}

func extractBearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return ""
	}
	return parts[1]
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
