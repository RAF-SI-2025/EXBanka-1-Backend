package handler

import (
	"net/http"

	notificationpb "github.com/exbanka/contract/notificationpb"
	"github.com/gin-gonic/gin"
)

// templateInfoToJSON renders a proto TemplateInfo as the REST shape.
func templateInfoToJSON(t *notificationpb.TemplateInfo) gin.H {
	vars := make([]gin.H, 0, len(t.Variables))
	for _, v := range t.Variables {
		vars = append(vars, gin.H{"name": v.Name, "description": v.Description, "example": v.Example})
	}
	return gin.H{
		"type":            t.Type,
		"channel":         t.Channel,
		"description":     t.Description,
		"variables":       vars,
		"default_subject": t.DefaultSubject,
		"default_body":    t.DefaultBody,
		"current_subject": t.CurrentSubject,
		"current_body":    t.CurrentBody,
		"is_customized":   t.IsCustomized,
	}
}

// ListNotificationTemplates godoc
// @Summary      List notification templates (discovery)
// @Description  Lists every notification template type with its supported {{variables}}, default text, and current (possibly customized) text. The discovery endpoint for the template editor.
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel query string false "Filter by channel: email | push"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/notification-templates [get]
func (h *NotificationHandler) ListNotificationTemplates(c *gin.Context) {
	channel := c.Query("channel")
	if channel != "" {
		if _, err := oneOf("channel", channel, "email", "push"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	resp, err := h.notificationClient.ListTemplates(c.Request.Context(), &notificationpb.ListTemplatesRequest{Channel: channel})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Templates))
	for _, t := range resp.Templates {
		out = append(out, templateInfoToJSON(t))
	}
	c.JSON(http.StatusOK, gin.H{"templates": out})
}

// GetNotificationTemplate godoc
// @Summary      Get one notification template
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type, e.g. ACCOUNT_CREATED"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [get]
func (h *NotificationHandler) GetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.notificationClient.GetTemplate(c.Request.Context(), &notificationpb.GetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}

type setNotificationTemplateRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// SetNotificationTemplate godoc
// @Summary      Customize a notification template
// @Description  Sets the subject/body override for a template type. Placeholders must use {{variable_name}} and reference only that type's known variables.
// @Tags         notification-templates
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type"
// @Param        body    body setNotificationTemplateRequest true "subject + body"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [put]
func (h *NotificationHandler) SetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	var req setNotificationTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Subject == "" || req.Body == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "subject and body are required")
		return
	}
	updatedBy := uint64(c.GetInt64("principal_id"))
	resp, err := h.notificationClient.SetTemplate(c.Request.Context(), &notificationpb.SetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
		Subject: req.Subject, Body: req.Body, UpdatedBy: updatedBy,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}

// ResetNotificationTemplate godoc
// @Summary      Reset a notification template to its default
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [delete]
func (h *NotificationHandler) ResetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.notificationClient.ResetTemplate(c.Request.Context(), &notificationpb.ResetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}
