package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

type PriceAlertHandler struct {
	client stockpb.PriceAlertServiceClient
}

func NewPriceAlertHandler(client stockpb.PriceAlertServiceClient) *PriceAlertHandler {
	return &PriceAlertHandler{client: client}
}

type createPriceAlertRequest struct {
	ListingID       uint64 `json:"listing_id"`
	Condition       string `json:"condition"`
	Threshold       string `json:"threshold"`
	IsRecurring     bool   `json:"is_recurring"`
	CooldownSeconds int32  `json:"cooldown_seconds"`
	EmailToo        bool   `json:"email_too"`
}

// Create godoc
// @Summary      Create a price alert
// @Tags         PriceAlerts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createPriceAlertRequest true "alert"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/price-alerts [post]
func (h *PriceAlertHandler) Create(c *gin.Context) {
	var req createPriceAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := oneOf("condition", req.Condition, "gte", "lte", "daily_change_pct_gte", "daily_change_pct_lte"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.ListingID == 0 || req.Threshold == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "listing_id and threshold are required")
		return
	}
	if req.IsRecurring && (req.CooldownSeconds < 60 || req.CooldownSeconds > 86400) {
		apiError(c, http.StatusBadRequest, ErrValidation, "cooldown_seconds must be in [60, 86400] for recurring alerts")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.CreateAlert(c.Request.Context(), &stockpb.CreatePriceAlertRequest{
		OwnerType:       identity.OwnerType,
		OwnerId:         derefU64(identity.OwnerID),
		ListingId:       req.ListingID,
		Condition:       req.Condition,
		Threshold:       req.Threshold,
		IsRecurring:     req.IsRecurring,
		CooldownSeconds: req.CooldownSeconds,
		EmailToo:        req.EmailToo,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"alert": resp})
}

// Get godoc
// @Summary      Get one of the caller's price alerts
// @Tags         PriceAlerts
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "alert id"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/price-alerts/{id} [get]
func (h *PriceAlertHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.GetAlert(c.Request.Context(), &stockpb.GetPriceAlertRequest{
		Id:        id,
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alert": resp})
}

type updatePriceAlertRequest struct {
	Condition       string `json:"condition"`
	Threshold       string `json:"threshold"`
	IsRecurring     bool   `json:"is_recurring"`
	CooldownSeconds int32  `json:"cooldown_seconds"`
	EmailToo        bool   `json:"email_too"`
	Active          bool   `json:"active"`
}

// Update godoc
// @Summary      Update a price alert
// @Tags         PriceAlerts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "alert id"
// @Param        body body updatePriceAlertRequest true "alert fields to update"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/price-alerts/{id} [put]
func (h *PriceAlertHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req updatePriceAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Condition != "" {
		if _, err := oneOf("condition", req.Condition, "gte", "lte", "daily_change_pct_gte", "daily_change_pct_lte"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.UpdateAlert(c.Request.Context(), &stockpb.UpdatePriceAlertRequest{
		Id:              id,
		OwnerType:       identity.OwnerType,
		OwnerId:         derefU64(identity.OwnerID),
		Condition:       req.Condition,
		Threshold:       req.Threshold,
		IsRecurring:     req.IsRecurring,
		CooldownSeconds: req.CooldownSeconds,
		EmailToo:        req.EmailToo,
		Active:          req.Active,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alert": resp})
}

// Delete godoc
// @Summary      Delete a price alert
// @Tags         PriceAlerts
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "alert id"
// @Success      204 {string} string ""
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/price-alerts/{id} [delete]
func (h *PriceAlertHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if _, err := h.client.DeleteAlert(c.Request.Context(), &stockpb.DeletePriceAlertRequest{
		Id:        id,
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMy godoc
// @Summary      List the caller's price alerts
// @Tags         PriceAlerts
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/price-alerts [get]
func (h *PriceAlertHandler) ListMy(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyPriceAlertsRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": resp.Alerts})
}
