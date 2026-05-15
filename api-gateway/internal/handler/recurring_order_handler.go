package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

type RecurringOrderHandler struct {
	client stockpb.RecurringOrderServiceClient
}

func NewRecurringOrderHandler(client stockpb.RecurringOrderServiceClient) *RecurringOrderHandler {
	return &RecurringOrderHandler{client: client}
}

type createRecurringOrderRequest struct {
	ListingID     uint64 `json:"listing_id"`
	Side          string `json:"side"`
	Quantity      int64  `json:"quantity"`
	AccountID     uint64 `json:"account_id"`
	Interval      string `json:"interval"`
	DayOfWeek     int32  `json:"day_of_week"`
	DayOfMonth    int32  `json:"day_of_month"`
	StartDateUnix int64  `json:"start_date_unix"`
	EndDateUnix   int64  `json:"end_date_unix"`
}

// Create godoc
// @Summary      Create a recurring (weekly/monthly) Market order template
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createRecurringOrderRequest true "template"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders [post]
func (h *RecurringOrderHandler) Create(c *gin.Context) {
	var req createRecurringOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := oneOf("side", req.Side, "buy", "sell"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if _, err := oneOf("interval", req.Interval, "weekly", "monthly"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.Quantity <= 0 || req.ListingID == 0 || req.AccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "quantity, listing_id, account_id are required")
		return
	}
	switch req.Interval {
	case "weekly":
		if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
			apiError(c, http.StatusBadRequest, ErrValidation, "day_of_week must be 0..6")
			return
		}
	case "monthly":
		if req.DayOfMonth < 1 || req.DayOfMonth > 28 {
			apiError(c, http.StatusBadRequest, ErrValidation, "day_of_month must be 1..28")
			return
		}
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.CreateOrder(c.Request.Context(), &stockpb.CreateRecurringOrderRequest{
		OwnerType:     identity.OwnerType,
		OwnerId:       derefU64(identity.OwnerID),
		ListingId:     req.ListingID,
		Side:          req.Side,
		Quantity:      req.Quantity,
		AccountId:     req.AccountID,
		Interval:      req.Interval,
		DayOfWeek:     req.DayOfWeek,
		DayOfMonth:    req.DayOfMonth,
		StartDateUnix: req.StartDateUnix,
		EndDateUnix:   req.EndDateUnix,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"recurring_order": resp})
}

// Get godoc
// @Summary      Get one of the caller's recurring orders
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "recurring order id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders/{id} [get]
func (h *RecurringOrderHandler) Get(c *gin.Context) {
	h.actOnID(c, h.client.GetOrder)
}

// Pause godoc
// @Summary      Pause a recurring order
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "recurring order id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders/{id}/pause [post]
func (h *RecurringOrderHandler) Pause(c *gin.Context) {
	h.actOnID(c, h.client.PauseOrder)
}

// Resume godoc
// @Summary      Resume a paused recurring order
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "recurring order id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders/{id}/resume [post]
func (h *RecurringOrderHandler) Resume(c *gin.Context) {
	h.actOnID(c, h.client.ResumeOrder)
}

// Cancel godoc
// @Summary      Cancel a recurring order
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "recurring order id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders/{id}/cancel [post]
func (h *RecurringOrderHandler) Cancel(c *gin.Context) {
	h.actOnID(c, h.client.CancelOrder)
}

// ListMy godoc
// @Summary      List the caller's recurring orders
// @Tags         RecurringOrders
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-orders [get]
func (h *RecurringOrderHandler) ListMy(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyRecurringOrdersRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_orders": resp.Items})
}

// actOnID centralises Get/Pause/Resume/Cancel — each takes the same
// (id, owner) request and returns RecurringOrderResponse.
type recurringOrderCall func(ctx context.Context, in *stockpb.GetRecurringOrderRequest, opts ...grpc.CallOption) (*stockpb.RecurringOrderResponse, error)

func (h *RecurringOrderHandler) actOnID(c *gin.Context, call recurringOrderCall) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := call(c.Request.Context(), &stockpb.GetRecurringOrderRequest{
		Id: id, OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_order": resp})
}
