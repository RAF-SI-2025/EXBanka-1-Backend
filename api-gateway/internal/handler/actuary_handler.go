package handler

import (
	"net/http"
	"strconv"

	"github.com/exbanka/api-gateway/internal/middleware"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/gin-gonic/gin"
)

type ActuaryHandler struct {
	client userpb.ActuaryServiceClient
}

func NewActuaryHandler(client userpb.ActuaryServiceClient) *ActuaryHandler {
	return &ActuaryHandler{client: client}
}

func (h *ActuaryHandler) ListActuaries(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListActuaries(c.Request.Context(), &userpb.ListActuariesRequest{
		Search:   c.Query("search"),
		Position: c.Query("position"),
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"actuaries": emptyIfNil(resp.Actuaries), "total_count": resp.TotalCount})
}

func (h *ActuaryHandler) SetActuaryLimit(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid actuary id")
		return
	}
	var req struct {
		Limit string `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Limit == "" {
		apiError(c, 400, ErrValidation, "limit is required")
		return
	}

	resp, err := h.client.SetActuaryLimit(middleware.GRPCContextWithChangedBy(c), &userpb.SetActuaryLimitRequest{
		Id: id, Limit: req.Limit,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *ActuaryHandler) ResetActuaryLimit(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid actuary id")
		return
	}

	resp, err := h.client.ResetActuaryUsedLimit(middleware.GRPCContextWithChangedBy(c), &userpb.ResetActuaryUsedLimitRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *ActuaryHandler) SetNeedApproval(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid actuary id")
		return
	}
	var req struct {
		NeedApproval bool `json:"need_approval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}

	resp, err := h.client.SetNeedApproval(middleware.GRPCContextWithChangedBy(c), &userpb.SetNeedApprovalRequest{
		Id: id, NeedApproval: req.NeedApproval,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
