package handler

import (
	"net/http"
	"strconv"

	notificationpb "github.com/exbanka/contract/notificationpb"
	"github.com/gin-gonic/gin"
)

type NotificationHandler struct {
	notificationClient notificationpb.NotificationServiceClient
}

func NewNotificationHandler(nc notificationpb.NotificationServiceClient) *NotificationHandler {
	return &NotificationHandler{notificationClient: nc}
}

// @Summary List notifications
// @Description List persistent notifications for the authenticated user (paginated, filterable by read status)
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Items per page (default 20, max 100)"
// @Param read query string false "Filter by read status: 'read', 'unread', or omit for all"
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/me/notifications [get]
func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	userID := c.GetInt64("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	readFilter := c.Query("read")

	resp, err := h.notificationClient.ListNotifications(c.Request.Context(), &notificationpb.ListNotificationsRequest{
		UserId:     uint64(userID),
		Page:       int32(page),
		PageSize:   int32(pageSize),
		ReadFilter: readFilter,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]gin.H, 0, len(resp.Notifications))
	for _, n := range resp.Notifications {
		items = append(items, gin.H{
			"id":         n.Id,
			"type":       n.Type,
			"title":      n.Title,
			"message":    n.Message,
			"is_read":    n.IsRead,
			"ref_type":   n.RefType,
			"ref_id":     n.RefId,
			"created_at": n.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"notifications": items,
		"total":         resp.Total,
	})
}

// @Summary Get unread notification count
// @Description Returns the number of unread notifications for the authenticated user
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/me/notifications/unread-count [get]
func (h *NotificationHandler) GetUnreadCount(c *gin.Context) {
	userID := c.GetInt64("user_id")

	resp, err := h.notificationClient.GetUnreadCount(c.Request.Context(), &notificationpb.GetUnreadCountRequest{
		UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": resp.Count})
}

// @Summary Mark notification as read
// @Description Marks a single notification as read for the authenticated user
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{} "Notification not found"
// @Router /api/v2/me/notifications/{id}/read [post]
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := c.GetInt64("user_id")
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid notification id")
		return
	}

	_, err = h.notificationClient.MarkNotificationRead(c.Request.Context(), &notificationpb.MarkNotificationReadRequest{
		Id:     id,
		UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// @Summary Mark all notifications as read
// @Description Marks all unread notifications as read for the authenticated user
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/me/notifications/read-all [post]
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := c.GetInt64("user_id")

	resp, err := h.notificationClient.MarkAllNotificationsRead(c.Request.Context(), &notificationpb.MarkAllNotificationsReadRequest{
		UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "count": resp.Count})
}
