package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	stockpb "github.com/exbanka/contract/stockpb"
)

type StockOrderHandler struct {
	client stockpb.OrderGRPCServiceClient
}

func NewStockOrderHandler(client stockpb.OrderGRPCServiceClient) *StockOrderHandler {
	return &StockOrderHandler{client: client}
}

func (h *StockOrderHandler) CreateOrder(c *gin.Context) {
	var req struct {
		ListingID  uint64  `json:"listing_id"`
		HoldingID  uint64  `json:"holding_id"`
		Direction  string  `json:"direction"`
		OrderType  string  `json:"order_type"`
		Quantity   int64   `json:"quantity"`
		LimitValue *string `json:"limit_value"`
		StopValue  *string `json:"stop_value"`
		AllOrNone  bool    `json:"all_or_none"`
		Margin     bool    `json:"margin"`
		AccountID  uint64  `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}

	direction, err := oneOf("direction", req.Direction, "buy", "sell")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	orderType, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if direction == "buy" && req.ListingID == 0 {
		apiError(c, 400, ErrValidation, "listing_id is required for buy orders")
		return
	}
	if direction == "buy" && req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "account_id is required for buy orders")
		return
	}
	if direction == "sell" && req.HoldingID == 0 {
		apiError(c, 400, ErrValidation, "holding_id is required for sell orders")
		return
	}
	if (orderType == "limit" || orderType == "stop_limit") && req.LimitValue == nil {
		apiError(c, 400, ErrValidation, "limit_value is required for limit/stop_limit orders")
		return
	}
	if (orderType == "stop" || orderType == "stop_limit") && req.StopValue == nil {
		apiError(c, 400, ErrValidation, "stop_value is required for stop/stop_limit orders")
		return
	}

	userID := c.GetInt64("user_id")
	systemType := c.GetString("system_type")

	grpcReq := &stockpb.CreateOrderRequest{
		UserId:     uint64(userID),
		SystemType: systemType,
		ListingId:  req.ListingID,
		HoldingId:  req.HoldingID,
		Direction:  direction,
		OrderType:  orderType,
		Quantity:   req.Quantity,
		AllOrNone:  req.AllOrNone,
		Margin:     req.Margin,
		AccountId:  req.AccountID,
	}
	if req.LimitValue != nil {
		grpcReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		grpcReq.StopValue = req.StopValue
	}

	resp, err := h.client.CreateOrder(c.Request.Context(), grpcReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *StockOrderHandler) ListMyOrders(c *gin.Context) {
	userID := c.GetInt64("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListMyOrders(c.Request.Context(), &stockpb.ListMyOrdersRequest{
		UserId:    uint64(userID),
		Status:    c.Query("status"),
		Direction: c.Query("direction"),
		OrderType: c.Query("order_type"),
		Page:      int32(page),
		PageSize:  int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": resp.Orders, "total_count": resp.TotalCount})
}

func (h *StockOrderHandler) GetMyOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	userID := c.GetInt64("user_id")

	resp, err := h.client.GetOrder(c.Request.Context(), &stockpb.GetOrderRequest{
		Id: id, UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockOrderHandler) CancelOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	userID := c.GetInt64("user_id")

	resp, err := h.client.CancelOrder(c.Request.Context(), &stockpb.CancelOrderRequest{
		Id: id, UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockOrderHandler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListOrders(c.Request.Context(), &stockpb.ListOrdersRequest{
		Status:     c.Query("status"),
		AgentEmail: c.Query("agent_email"),
		Direction:  c.Query("direction"),
		OrderType:  c.Query("order_type"),
		Page:       int32(page),
		PageSize:   int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": resp.Orders, "total_count": resp.TotalCount})
}

func (h *StockOrderHandler) ApproveOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	supervisorID := c.GetInt64("user_id")

	resp, err := h.client.ApproveOrder(c.Request.Context(), &stockpb.ApproveOrderRequest{
		Id: id, SupervisorId: uint64(supervisorID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockOrderHandler) DeclineOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	supervisorID := c.GetInt64("user_id")

	resp, err := h.client.DeclineOrder(c.Request.Context(), &stockpb.DeclineOrderRequest{
		Id: id, SupervisorId: uint64(supervisorID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
