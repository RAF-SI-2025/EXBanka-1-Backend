package handler

import (
	"net/http"
	"strconv"
	"strings"

	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type StockOrderHandler struct {
	client        stockpb.OrderGRPCServiceClient
	accountClient accountpb.AccountServiceClient
}

func NewStockOrderHandler(client stockpb.OrderGRPCServiceClient, accountClient accountpb.AccountServiceClient) *StockOrderHandler {
	return &StockOrderHandler{client: client, accountClient: accountClient}
}

// CreateOrder godoc
// @Summary      Place a securities order (stock/futures/forex/option) for the authenticated user
// @Description  Validates forex-specific constraints (forex must be buy, requires base_account_id, base_account_id must differ from account_id).
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        body body object true "Order. security_type is optional ('stock'|'futures'|'forex'|'option'); required for forex validation. base_account_id is required for forex buy orders."
// @Security     BearerAuth
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{} "validation_error — forex orders must be direction=buy; forex orders require base_account_id; base_account_id must differ from account_id"
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{} "business_rule_violation — insufficient available balance"
// @Router       /api/v1/me/orders [post]
func (h *StockOrderHandler) CreateOrder(c *gin.Context) {
	var req struct {
		SecurityType  string  `json:"security_type"`
		ListingID     uint64  `json:"listing_id"`
		HoldingID     uint64  `json:"holding_id"`
		Direction     string  `json:"direction"`
		OrderType     string  `json:"order_type"`
		Quantity      int64   `json:"quantity"`
		LimitValue    *string `json:"limit_value"`
		StopValue     *string `json:"stop_value"`
		AllOrNone     bool    `json:"all_or_none"`
		Margin        bool    `json:"margin"`
		AccountID     uint64  `json:"account_id"`
		BaseAccountID *uint64 `json:"base_account_id,omitempty"`
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

	// security_type is optional in the HTTP body (stock-service derives it from the listing),
	// but when provided must be one of the known kinds. When provided as "forex" we enforce
	// additional gateway-level constraints per the bank-safe settlement design (defense in depth).
	securityType := strings.ToLower(strings.TrimSpace(req.SecurityType))
	if securityType != "" {
		var stErr error
		securityType, stErr = oneOf("security_type", securityType, "stock", "futures", "forex", "option")
		if stErr != nil {
			apiError(c, 400, ErrValidation, stErr.Error())
			return
		}
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
	if direction == "buy" {
		acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
			return
		}
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

	// Forex-specific validation (defense in depth; stock-service re-validates).
	if securityType == "forex" {
		if direction != "buy" {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders must be direction=buy")
			return
		}
		if req.BaseAccountID == nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders require base_account_id")
			return
		}
	}
	if req.BaseAccountID != nil && *req.BaseAccountID == req.AccountID {
		apiError(c, http.StatusBadRequest, ErrValidation, "base_account_id must differ from account_id")
		return
	}
	// Ownership check for base account, when present.
	if req.BaseAccountID != nil {
		baseAcctResp, baseErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: *req.BaseAccountID})
		if baseErr != nil {
			handleGRPCError(c, baseErr)
			return
		}
		if ownErr := enforceOwnership(c, baseAcctResp.OwnerId); ownErr != nil {
			return
		}
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
	if req.BaseAccountID != nil {
		grpcReq.BaseAccountId = req.BaseAccountID
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
	c.JSON(http.StatusOK, gin.H{"orders": emptyIfNil(resp.Orders), "total_count": resp.TotalCount})
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

// CreateOrderOnBehalf godoc
// @Summary      Place stock/futures/forex/option order on behalf of a client
// @Description  Employee-only. Gateway verifies the account belongs to the named client; stock-service records acting_employee_id for audit.
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        body body object true "Order"
// @Security     BearerAuth
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v1/orders [post]
func (h *StockOrderHandler) CreateOrderOnBehalf(c *gin.Context) {
	var req struct {
		ClientID      uint64  `json:"client_id"`
		AccountID     uint64  `json:"account_id"`
		SecurityType  string  `json:"security_type"`
		ListingID     uint64  `json:"listing_id"`
		HoldingID     uint64  `json:"holding_id"`
		Direction     string  `json:"direction"`
		OrderType     string  `json:"order_type"`
		Quantity      int64   `json:"quantity"`
		LimitValue    *string `json:"limit_value"`
		StopValue     *string `json:"stop_value"`
		AllOrNone     bool    `json:"all_or_none"`
		Margin        bool    `json:"margin"`
		BaseAccountID *uint64 `json:"base_account_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 {
		apiError(c, 400, ErrValidation, "client_id is required")
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
	securityType := strings.ToLower(strings.TrimSpace(req.SecurityType))
	if securityType != "" {
		var stErr error
		securityType, stErr = oneOf("security_type", securityType, "stock", "futures", "forex", "option")
		if stErr != nil {
			apiError(c, 400, ErrValidation, stErr.Error())
			return
		}
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if direction == "buy" {
		if req.ListingID == 0 {
			apiError(c, 400, ErrValidation, "listing_id is required for buy orders")
			return
		}
		if req.AccountID == 0 {
			apiError(c, 400, ErrValidation, "account_id is required for buy orders")
			return
		}
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

	// Forex-specific validation (defense in depth; stock-service re-validates).
	if securityType == "forex" {
		if direction != "buy" {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders must be direction=buy")
			return
		}
		if req.BaseAccountID == nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders require base_account_id")
			return
		}
	}
	if req.BaseAccountID != nil && *req.BaseAccountID == req.AccountID {
		apiError(c, http.StatusBadRequest, ErrValidation, "base_account_id must differ from account_id")
		return
	}

	// Verify the account belongs to the named client (buy path only; sell uses holding).
	if direction == "buy" {
		acctResp, acctErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
		if acctErr != nil {
			handleGRPCError(c, acctErr)
			return
		}
		if acctResp.OwnerId != req.ClientID {
			apiError(c, 403, ErrForbidden, "account does not belong to client")
			return
		}
	}
	// Also verify the base account belongs to the named client when provided.
	if req.BaseAccountID != nil {
		baseAcctResp, baseErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: *req.BaseAccountID})
		if baseErr != nil {
			handleGRPCError(c, baseErr)
			return
		}
		if baseAcctResp.OwnerId != req.ClientID {
			apiError(c, 403, ErrForbidden, "base account does not belong to client")
			return
		}
	}

	employeeID := uint64(c.GetInt64("user_id"))
	grpcReq := &stockpb.CreateOrderRequest{
		UserId:             req.ClientID,
		SystemType:         "employee",
		ListingId:          req.ListingID,
		HoldingId:          req.HoldingID,
		Direction:          direction,
		OrderType:          orderType,
		Quantity:           req.Quantity,
		AllOrNone:          req.AllOrNone,
		Margin:             req.Margin,
		AccountId:          req.AccountID,
		ActingEmployeeId:   employeeID,
		OnBehalfOfClientId: req.ClientID,
	}
	if req.LimitValue != nil {
		grpcReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		grpcReq.StopValue = req.StopValue
	}
	if req.BaseAccountID != nil {
		grpcReq.BaseAccountId = req.BaseAccountID
	}

	resp, err := h.client.CreateOrder(c.Request.Context(), grpcReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
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
	c.JSON(http.StatusOK, gin.H{"orders": emptyIfNil(resp.Orders), "total_count": resp.TotalCount})
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
