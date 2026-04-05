package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

type SessionHandler struct {
	authClient authpb.AuthServiceClient
}

func NewSessionHandler(authClient authpb.AuthServiceClient) *SessionHandler {
	return &SessionHandler{authClient: authClient}
}

// ListMySessions godoc
// @Summary      List my active sessions
// @Description  Returns all active sessions for the authenticated user
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "sessions array"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/sessions [get]
func (h *SessionHandler) ListMySessions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	resp, err := h.authClient.ListSessions(c.Request.Context(), &authpb.ListSessionsRequest{
		UserId: uid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	sessions := make([]gin.H, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		sessions = append(sessions, gin.H{
			"id":             s.Id,
			"user_role":      s.UserRole,
			"ip_address":     s.IpAddress,
			"user_agent":     s.UserAgent,
			"device_id":      s.DeviceId,
			"system_type":    s.SystemType,
			"last_active_at": s.LastActiveAt,
			"created_at":     s.CreatedAt,
			"is_current":     s.IsCurrent,
		})
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

type revokeSessionRequest struct {
	SessionID int64 `json:"session_id" binding:"required"`
}

// RevokeSession godoc
// @Summary      Revoke a specific session
// @Description  Revokes a session by ID, logging out the device associated with it
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  revokeSessionRequest  true  "Session to revoke"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "validation error"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Failure      404  {object}  map[string]string  "session not found"
// @Router       /api/me/sessions/revoke [post]
func (h *SessionHandler) RevokeSession(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	var req revokeSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.RevokeSession(c.Request.Context(), &authpb.RevokeSessionRequest{
		SessionId:    req.SessionID,
		CallerUserId: uid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "session revoked successfully"})
}

type revokeAllSessionsRequest struct {
	CurrentRefreshToken string `json:"current_refresh_token" binding:"required"`
}

// RevokeAllSessions godoc
// @Summary      Revoke all other sessions
// @Description  Revokes all sessions except the current one (identified by the provided refresh token)
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  revokeAllSessionsRequest  true  "Current refresh token to keep"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "validation error"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/sessions/revoke-others [post]
func (h *SessionHandler) RevokeAllSessions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	var req revokeAllSessionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.RevokeAllSessions(c.Request.Context(), &authpb.RevokeAllSessionsRequest{
		UserId:              uid,
		CurrentRefreshToken: req.CurrentRefreshToken,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "all other sessions revoked successfully"})
}

// GetMyLoginHistory godoc
// @Summary      Get my login history
// @Description  Returns recent login attempts for the authenticated user
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Param        limit  query  int  false  "Max entries (default 50, max 100)"
// @Success      200  {object}  map[string]interface{}  "entries array"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/login-history [get]
func (h *SessionHandler) GetMyLoginHistory(c *gin.Context) {
	email, _ := c.Get("email")
	emailStr, ok := email.(string)
	if !ok || emailStr == "" {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	limit := int32(50)
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 32); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}

	resp, err := h.authClient.GetLoginHistory(c.Request.Context(), &authpb.LoginHistoryRequest{
		Email: emailStr,
		Limit: limit,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	entries := make([]gin.H, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		entries = append(entries, gin.H{
			"id":          e.Id,
			"ip_address":  e.IpAddress,
			"user_agent":  e.UserAgent,
			"device_type": e.DeviceType,
			"success":     e.Success,
			"created_at":  e.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}
